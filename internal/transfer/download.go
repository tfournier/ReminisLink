package transfer

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Download limits (docs/specs/04-api-v1.md POST /downloads, 05 §5.6).
const (
	MaxDownloadBytes    = 4 << 30
	MaxRedirects        = 5
	MaxDownloadRetries  = 3
	DownloadDialTimeout = 15 * time.Second
	DownloadIdleTimeout = 60 * time.Second
	DefaultRetryDelay   = time.Second
	// MaxURLBytes bounds the URL of a download.
	MaxURLBytes = 8 << 10
	// spaceCheckEvery: with an unknown size, the free space is checked
	// again after this many bytes.
	spaceCheckEvery = 4 << 20
)

// DefaultCABundle is the certificate bundle used when Go finds no system
// certificate store.
// TODO(M0): check that /etc/ssl/certs/ca-certificates.crt exists on the
// console. Cheat Manager trusts the bundle embedded by
// github.com/BrandonKowalski/certifiable (imported by gabagool, which
// replaces http.DefaultTransport's roots); that module is not an allowed
// direct dependency, so the pak loads the system store instead.
const DefaultCABundle = "/etc/ssl/certs/ca-certificates.crt"

// LoadRootCAs returns the system certificate store, or the PEM bundle at
// bundle when the system store is missing or empty.
func LoadRootCAs(bundle string) (*x509.CertPool, error) {
	return loadRootCAs(x509.SystemCertPool, bundle)
}

func loadRootCAs(system func() (*x509.CertPool, error), bundle string) (*x509.CertPool, error) {
	if pool, err := system(); err == nil && pool != nil && !pool.Equal(x509.NewCertPool()) {
		return pool, nil
	}
	if bundle == "" {
		bundle = DefaultCABundle
	}
	data, err := os.ReadFile(bundle)
	if err != nil {
		return nil, fmt.Errorf("no certificate store: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data) {
		return nil, errors.New("no certificate in the bundle")
	}
	return pool, nil
}

// DownloadRequest is the body of POST /downloads.
type DownloadRequest struct {
	URL        *string `json:"url"`
	Target     *Target `json:"target"`
	SHA256     *string `json:"sha256"`
	OnConflict *string `json:"onConflict"`
}

// checkURL accepts absolute http and https URLs only.
func checkURL(raw string) (*url.URL, *Error) {
	if raw == "" || len(raw) > MaxURLBytes {
		return nil, badRequest("Invalid url")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, badRequest("Only http and https URLs are allowed")
	}
	return u, nil
}

// safeURL is what the log shows of a URL: scheme and host, never the path
// or the query (they may hold tokens).
func safeURL(u *url.URL) string { return u.Scheme + "://" + u.Host }

// StartDownload checks a download request and queues its job.
func (s *Service) StartDownload(req DownloadRequest) (string, *Error, error) {
	if req.URL == nil || req.Target == nil {
		return "", badRequest("Missing field"), nil
	}
	u, apiErr := checkURL(*req.URL)
	if apiErr != nil {
		return "", apiErr, nil
	}
	want := ""
	if req.SHA256 != nil {
		if !validSHA256(*req.SHA256) {
			return "", badRequest("Invalid sha256"), nil
		}
		want = *req.SHA256
	}
	onConflict := ConflictFail
	if req.OnConflict != nil {
		onConflict = *req.OnConflict
	}
	if !validConflict(onConflict) {
		return "", badRequest("Invalid onConflict"), nil
	}
	target := *req.Target
	if _, apiErr, err := s.resolve(target, false); apiErr != nil || err != nil {
		if apiErr != nil && apiErr.Code == CodeStateLocked {
			// POST /downloads has no 409 in the contract: the job fails.
			apiErr = nil
		} else {
			return "", apiErr, err
		}
	}
	id, err := s.jobs.Submit(JobDownload, func(ctx context.Context, report func(int64, *int64)) (map[string]any, *Error) {
		return s.runDownload(ctx, u, target, want, onConflict, report)
	})
	if errors.Is(err, ErrTooManyJobs) {
		return "", tooMany("Too many jobs"), nil
	}
	if errors.Is(err, ErrJobsClosed) {
		return "", newError(http.StatusServiceUnavailable, CodeShuttingDown, "Shutting down"), nil
	}
	if err != nil {
		return "", nil, err
	}
	s.log.Info("download queued", "job", id, "url", safeURL(u), "kind", target.Kind)
	return id, nil, nil
}

// httpClient returns the client of the downloads, built on first use.
func (s *Service) httpClient() (*http.Client, error) {
	s.clientOnce.Do(func() {
		o := s.opts.Downloads
		rt := o.Transport
		if rt == nil {
			roots := o.RootCAs
			if roots == nil {
				var err error
				if roots, err = LoadRootCAs(o.CABundle); err != nil {
					s.clientErr = err
					return
				}
			}
			rt = &http.Transport{
				DialContext:           (&net.Dialer{Timeout: DownloadDialTimeout, KeepAlive: 30 * time.Second}).DialContext,
				TLSClientConfig:       &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
				TLSHandshakeTimeout:   DownloadDialTimeout,
				ResponseHeaderTimeout: s.idleTimeout(),
				IdleConnTimeout:       90 * time.Second,
				MaxIdleConns:          2,
				// No proxy: the console downloads directly.
			}
		}
		s.client = &http.Client{
			Transport: rt,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) > MaxRedirects {
					return errTooManyRedirects
				}
				if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
					return errBadRedirect
				}
				return nil
			},
		}
	})
	return s.client, s.clientErr
}

func (s *Service) idleTimeout() time.Duration {
	if s.opts.Downloads.IdleTimeout > 0 {
		return s.opts.Downloads.IdleTimeout
	}
	return DownloadIdleTimeout
}

// Redirect errors (not retried).
var (
	errTooManyRedirects = errors.New("too many redirects")
	errBadRedirect      = errors.New("redirect to a URL that is not http or https")
)

// retryable is a failure worth retrying (network, cut body).
type retryable struct{ err error }

func (r retryable) Error() string { return r.err.Error() }
func (r retryable) Unwrap() error { return r.err }

// download is the state of one download job.
type download struct {
	s       *Service
	u       *url.URL
	path    string // temporary file in uploads/
	file    *os.File
	hash    hash.Hash
	written int64
	total   int64  // -1: unknown
	name    string // deduced from the first response
	tag     string // validator for If-Range
	report  func(int64, *int64)
}

func (s *Service) runDownload(ctx context.Context, u *url.URL, target Target, want, onConflict string, report func(int64, *int64)) (map[string]any, *Error) {
	client, err := s.httpClient()
	if err != nil {
		s.log.Error("download: no HTTP client", "error", err)
		return nil, internal("No certificate store")
	}
	dir := s.opts.Layout.UploadsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, internal("Cannot create the temporary file")
	}
	d := &download{s: s, u: u, path: filepath.Join(dir, "dl_"+s.randomHex(12)+partExt), hash: sha256.New(), total: -1, report: report}
	if d.file, err = os.OpenFile(d.path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644); err != nil {
		return nil, internal("Cannot create the temporary file")
	}
	installed := false
	defer func() {
		if d.file != nil {
			d.file.Close()
		}
		if !installed {
			os.Remove(d.path)
		}
	}()

	delay := s.opts.Downloads.RetryDelay
	if delay <= 0 {
		delay = DefaultRetryDelay
	}
	for attempt := 0; ; attempt++ {
		apiErr, err := d.fetch(ctx, client)
		if apiErr == nil && err == nil {
			break
		}
		if ctx.Err() != nil {
			return nil, newError(http.StatusServiceUnavailable, CodeShuttingDown, "Cancelled")
		}
		var r retryable
		if apiErr != nil || !errors.As(err, &r) || attempt >= MaxDownloadRetries {
			if apiErr == nil {
				s.log.Warn("download failed", "url", safeURL(u), "error", downloadErrorText(err))
				apiErr = internal("Download failed: " + downloadErrorText(err))
			}
			return nil, apiErr
		}
		s.log.Info("download interrupted, retrying", "url", safeURL(u), "offset", d.written, "attempt", attempt+1, "error", downloadErrorText(err))
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, newError(http.StatusServiceUnavailable, CodeShuttingDown, "Cancelled")
		}
		delay *= 2
	}
	if want != "" && hex.EncodeToString(d.hash.Sum(nil)) != want {
		return nil, newError(http.StatusConflict, CodeChecksumMismatch, "SHA-256 mismatch")
	}
	if err := d.file.Sync(); err != nil {
		return nil, internal("Cannot write the file")
	}
	if err := d.file.Close(); err != nil {
		return nil, internal("Cannot write the file")
	}
	d.file = nil

	dest, apiErr, err := s.resolve(target, false)
	if apiErr != nil || err != nil {
		if err != nil {
			s.log.Error("download: resolve", "error", err)
			return nil, internal("Internal error")
		}
		return nil, apiErr
	}
	if dest.name == "" {
		dest.name = d.name
	}
	if dest.kind == KindROM && (filepath.Ext(dest.name) == "" || !s.opts.Adapter.IsGameFile(dest.name)) {
		// The library would not list it: never install a ROM that cannot
		// be found.
		return nil, invalidName("Not a ROM file name: " + dest.name)
	}
	result, apiErr, err := s.install(dest, d.path, onConflict)
	if err != nil {
		s.log.Error("download: install", "error", err)
		return nil, internal("Internal error")
	}
	if apiErr != nil {
		return nil, apiErr
	}
	installed = true
	s.log.Info("download finished", "url", safeURL(u), "bytes", d.written)
	return result, nil
}

// downloadErrorText is a short reason without the URL (url.Error repeats
// it, with its query).
func downloadErrorText(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	msg := err.Error()
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return msg
}

// fetch sends one request (resuming with Range when bytes are already
// there) and copies the body. A retryable error means the request can be
// sent again.
func (d *download) fetch(ctx context.Context, client *http.Client) (*Error, error) {
	attemptCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, d.u.String(), nil)
	if err != nil {
		return badRequest("Invalid url"), nil
	}
	req.Header.Set("User-Agent", "ReminisLink")
	if d.written > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(d.written, 10)+"-")
		if d.tag != "" {
			req.Header.Set("If-Range", d.tag)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, errTooManyRedirects) || errors.Is(err, errBadRedirect) {
			return badRequest("Download failed: " + downloadErrorText(err)), nil
		}
		return nil, retryable{err}
	}
	defer resp.Body.Close()

	switch {
	case d.written > 0 && resp.StatusCode == http.StatusPartialContent:
		start, total, ok := parseContentRange(resp.Header.Get("Content-Range"))
		if !ok || start != d.written {
			return nil, retryable{errors.New("unexpected Content-Range")}
		}
		if total >= 0 {
			d.total = total
		}
	case resp.StatusCode == http.StatusOK:
		if d.written > 0 {
			// Range not supported (or the file changed): start again.
			if err := d.restart(); err != nil {
				return internal("Cannot write the file"), nil
			}
		}
		d.total = resp.ContentLength
		d.name = deduceName(resp.Header.Get("Content-Disposition"), resp.Request.URL)
		d.tag = resp.Header.Get("ETag")
		if d.tag == "" || strings.HasPrefix(d.tag, "W/") {
			d.tag = resp.Header.Get("Last-Modified")
		}
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return notFound("Download failed: HTTP " + strconv.Itoa(resp.StatusCode)), nil
	case resp.StatusCode >= 500 || resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooManyRequests:
		return nil, retryable{fmt.Errorf("HTTP %d", resp.StatusCode)}
	default:
		return internal("Download failed: HTTP " + strconv.Itoa(resp.StatusCode)), nil
	}
	if d.total > MaxDownloadBytes {
		return newError(http.StatusRequestEntityTooLarge, CodeTooLarge, "File too large"), nil
	}
	if d.total >= 0 {
		if apiErr := d.checkSpace(d.total - d.written); apiErr != nil {
			return apiErr, nil
		}
	}
	d.progress()
	return d.copyBody(attemptCtx, cancel, resp.Body)
}

func (d *download) restart() error {
	if err := d.file.Truncate(0); err != nil {
		return err
	}
	if _, err := d.file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	d.written = 0
	d.hash.Reset()
	return nil
}

func (d *download) progress() {
	var total *int64
	if d.total >= 0 {
		t := d.total
		total = &t
	}
	d.report(d.written, total)
}

// checkSpace requires the free space for n more bytes plus the margin.
func (d *download) checkSpace(n int64) *Error {
	free, err := d.s.opts.FreeSpace(filepath.Dir(d.path))
	if err != nil {
		return nil // cannot tell: the write will fail if the card is full
	}
	if need := required(n); free < need {
		return insufficientStorage(need, free)
	}
	return nil
}

// copyBody writes the body to the file, with an inactivity timeout:
// cancel is called when no byte arrives for the idle timeout.
func (d *download) copyBody(ctx context.Context, cancel context.CancelFunc, body io.Reader) (*Error, error) {
	idle := d.s.idleTimeout()
	var mu sync.Mutex
	timedOut := false
	timer := time.AfterFunc(idle, func() {
		mu.Lock()
		timedOut = true
		mu.Unlock()
		cancel()
	})
	defer timer.Stop()
	buf := make([]byte, 64<<10)
	sinceCheck := int64(0)
	var lastReport time.Time // the first bytes are reported at once
	for {
		n, rerr := body.Read(buf)
		if n > 0 {
			timer.Reset(idle)
			if d.written+int64(n) > MaxDownloadBytes {
				return newError(http.StatusRequestEntityTooLarge, CodeTooLarge, "File too large"), nil
			}
			if _, err := d.file.Write(buf[:n]); err != nil {
				if errors.Is(err, syscall.ENOSPC) {
					free, _ := d.s.opts.FreeSpace(filepath.Dir(d.path))
					return insufficientStorage(required(int64(n)), free), nil
				}
				return internal("Cannot write the file"), nil
			}
			d.hash.Write(buf[:n])
			d.written += int64(n)
			sinceCheck += int64(n)
			if d.total < 0 && sinceCheck >= spaceCheckEvery {
				sinceCheck = 0
				if apiErr := d.checkSpace(spaceCheckEvery); apiErr != nil {
					return apiErr, nil
				}
			}
			if time.Since(lastReport) >= 200*time.Millisecond {
				lastReport = time.Now()
				d.progress()
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			mu.Lock()
			to := timedOut
			mu.Unlock()
			if to {
				return nil, retryable{errors.New("no data for " + idle.String())}
			}
			return nil, retryable{rerr}
		}
	}
	if d.total >= 0 && d.written != d.total {
		return nil, retryable{errors.New("body shorter than announced")}
	}
	d.progress()
	return nil, nil
}

// parseContentRange reads "bytes <start>-<end>/<total|*>".
func parseContentRange(v string) (start, total int64, ok bool) {
	rest, found := strings.CutPrefix(v, "bytes ")
	if !found {
		return 0, 0, false
	}
	rng, tot, found := strings.Cut(rest, "/")
	if !found {
		return 0, 0, false
	}
	first, _, found := strings.Cut(rng, "-")
	if !found {
		return 0, 0, false
	}
	start, err := strconv.ParseInt(first, 10, 64)
	if err != nil || start < 0 {
		return 0, 0, false
	}
	total = -1
	if tot != "*" {
		if total, err = strconv.ParseInt(tot, 10, 64); err != nil || total < 0 {
			return 0, 0, false
		}
	}
	return start, total, true
}
