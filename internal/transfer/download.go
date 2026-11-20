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

	"github.com/tfournier/ReminisLink/internal/store"
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

// Root certificates (decision D1, docs/specs/m0/questions.md): the console
// has no certificate store. The pak binary links gabagool, which imports
// github.com/BrandonKowalski/certifiable: its init installs an embedded
// bundle of root certificates in http.DefaultTransport (this is how the Pak
// Store downloads over HTTPS). Every outgoing HTTP client of the pak is
// therefore built from a clone of http.DefaultTransport (see baseTransport),
// never from a new http.Transport. A build without gabagool (CGO_ENABLED=0,
// development on a Mac) uses the system store, as Go does by default; on
// Linux, SSL_CERT_FILE still overrides the store for a test.

// baseTransport returns a clone of http.DefaultTransport (and so of its
// TLS configuration and root certificates), or a default transport when
// it was replaced by something else.
func baseTransport() *http.Transport {
	if t, ok := http.DefaultTransport.(*http.Transport); ok {
		return t.Clone()
	}
	return &http.Transport{}
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
	id, err := s.jobs.SubmitLabeled(JobDownload, downloadLabel(u, target), func(ctx context.Context, report func(int64, *int64)) (map[string]any, *Error) {
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
			t := baseTransport()
			t.DialContext = (&net.Dialer{Timeout: DownloadDialTimeout, KeepAlive: 30 * time.Second}).DialContext
			if t.TLSClientConfig == nil {
				t.TLSClientConfig = &tls.Config{}
			}
			if o.RootCAs != nil {
				t.TLSClientConfig.RootCAs = o.RootCAs
			}
			t.TLSClientConfig.MinVersion = tls.VersionTLS12
			t.TLSHandshakeTimeout = DownloadDialTimeout
			t.ResponseHeaderTimeout = s.idleTimeout()
			t.IdleConnTimeout = 90 * time.Second
			t.MaxIdleConns = 2
			// No proxy: the console downloads directly.
			t.Proxy = nil
			// ForceAttemptHTTP2 comes with the clone (true): kept, since
			// setting DialContext or TLSClientConfig would otherwise turn
			// HTTP/2 off. HTTP/2 serves Range requests the same way, and the
			// timeouts above apply to it too.
			rt = t
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
	return s.client, nil
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
	// checked: the expected SHA-256 is known, so a file assembled from two
	// versions of the resource is refused at the end.
	checked bool
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
	d := &download{s: s, u: u, path: filepath.Join(dir, "dl_"+s.randomHex(12)+partExt), hash: sha256.New(), total: -1,
		checked: want != "", report: report}
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

	// Resolve and install under the write lock (M13: a job waits for its
	// turn without a time limit) and the install lock (see CompleteUpload).
	if err := s.opts.Writes.Acquire(ctx); err != nil {
		return nil, newError(http.StatusServiceUnavailable, CodeShuttingDown, "Cancelled")
	}
	s.installMu.Lock()
	if ctx.Err() != nil {
		// Cancelled while waiting for the lock: nothing installed.
		s.installMu.Unlock()
		s.opts.Writes.Release()
		return nil, newError(http.StatusServiceUnavailable, CodeShuttingDown, "Cancelled")
	}
	result, apiErr, err := s.installDownload(target, d.name, d.path, onConflict)
	s.installMu.Unlock()
	s.opts.Writes.Release()
	if errors.Is(err, store.ErrReplaceFailed) {
		installed = true // the file is named by a pending replacement: kept
	}
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

// installDownload resolves the target and installs the downloaded file.
// Called with s.installMu held.
func (s *Service) installDownload(target Target, deduced, path, onConflict string) (map[string]any, *Error, error) {
	dest, apiErr, err := s.resolve(target, false)
	if apiErr != nil || err != nil {
		return nil, apiErr, err
	}
	if dest.name == "" {
		dest.name = deduced
		if dest.kind == KindROM && !s.opts.Adapter.IsGameFile(dest.name) {
			// The library would not list it: never install a ROM that
			// cannot be found.
			return nil, invalidName("Not a ROM file name: " + dest.name), nil
		}
	}
	return s.installLocked(dest, path, onConflict)
}

// Reasons of a failed download, the only texts that reach the log and
// the error of a job: never the text of a network error, which can carry
// a URL and its query (a signed link, a malformed redirect target).
var (
	errUnexpectedRange = errors.New("unexpected Content-Range")
	errNoData          = errors.New("no data received in time")
	errShortBody       = errors.New("body shorter than announced")
	errHTTPStatus      = errors.New("HTTP error status")
)

// downloadErrorText is a predefined short reason for err (see above).
func downloadErrorText(err error) string {
	var dns *net.DNSError
	var tlsRecord tls.RecordHeaderError
	var tlsAlert tls.AlertError
	var cert *tls.CertificateVerificationError
	var unknownAuth x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	var netErr net.Error
	switch {
	case errors.Is(err, errTooManyRedirects):
		return "too many redirects"
	case errors.Is(err, errBadRedirect):
		return "redirect to a URL that is not http or https"
	case errors.Is(err, errUnexpectedRange):
		return "unexpected Content-Range"
	case errors.Is(err, errNoData):
		return "no data received in time"
	case errors.Is(err, errShortBody):
		return "connection closed before the end"
	case errors.Is(err, errHTTPStatus):
		return err.Error() // "HTTP error status: 503", built by the pak
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.As(err, &dns):
		return "host not found"
	case errors.As(err, &cert), errors.As(err, &unknownAuth), errors.As(err, &hostname), errors.As(err, &invalid),
		errors.As(err, &tlsRecord), errors.As(err, &tlsAlert):
		return "secure connection failed"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection refused"
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
		return "connection lost"
	case errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.EHOSTUNREACH):
		return "network unreachable"
	case errors.As(err, &netErr) && netErr.Timeout():
		return "timeout"
	}
	return "network error"
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
	if d.written > 0 && d.tag == "" && !d.checked {
		// Neither a validator (ETag, Last-Modified) nor an expected
		// SHA-256: a resumed range could come from another version of the
		// resource, and nothing would tell. Start again from zero.
		if err := d.restart(); err != nil {
			return internal("Cannot write the file"), nil
		}
	}
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
			return nil, retryable{errUnexpectedRange}
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
		return nil, retryable{fmt.Errorf("%w: %d", errHTTPStatus, resp.StatusCode)}
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
				return nil, retryable{errNoData}
			}
			return nil, retryable{rerr}
		}
	}
	if d.total >= 0 && d.written != d.total {
		return nil, retryable{errShortBody}
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
