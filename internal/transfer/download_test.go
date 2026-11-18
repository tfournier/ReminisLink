package transfer

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fileServer serves data under several behaviours.
type fileServer struct {
	data     []byte
	requests atomic.Int32
	ranges   atomic.Int32
	release  chan struct{} // /slow waits on it
	srv      *httptest.Server
}

func newFileServer(t *testing.T, data []byte, tls bool) *fileServer {
	t.Helper()
	fs := &fileServer{data: data, release: make(chan struct{})}
	mux := http.NewServeMux()
	modTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	serve := func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			fs.ranges.Add(1)
		}
		w.Header().Set("ETag", `"v1"`)
		http.ServeContent(w, r, "", modTime, bytes.NewReader(fs.data))
	}
	mux.HandleFunc("/files/Sky%20Hack.bps", func(w http.ResponseWriter, r *http.Request) {
		fs.requests.Add(1)
		serve(w, r)
	})
	mux.HandleFunc("/files/", func(w http.ResponseWriter, r *http.Request) {
		fs.requests.Add(1)
		serve(w, r)
	})
	mux.HandleFunc("/nolength", func(w http.ResponseWriter, r *http.Request) {
		fs.requests.Add(1)
		w.Header().Set("Content-Disposition", `attachment; filename="from-header.ips"`)
		w.WriteHeader(http.StatusOK)
		half := len(fs.data) / 2
		w.Write(fs.data[:half])
		w.(http.Flusher).Flush()
		w.Write(fs.data[half:])
	})
	mux.HandleFunc("/redirect/", func(w http.ResponseWriter, r *http.Request) {
		fs.requests.Add(1)
		n, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/redirect/"))
		if n == 0 {
			http.Redirect(w, r, "/files/final.bin", http.StatusFound)
			return
		}
		http.Redirect(w, r, "/redirect/"+strconv.Itoa(n-1), http.StatusFound)
	})
	mux.HandleFunc("/to-ftp", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "ftp://example.org/x", http.StatusFound)
	})
	// /cut sends half of the file then drops the connection, the first
	// time only; then it serves ranges (or ignores them with ?norange).
	var cut atomic.Bool
	mux.HandleFunc("/cut/", func(w http.ResponseWriter, r *http.Request) {
		fs.requests.Add(1)
		if cut.CompareAndSwap(false, true) {
			w.Header().Set("ETag", `"v1"`)
			w.Header().Set("Content-Length", strconv.Itoa(len(fs.data)))
			w.WriteHeader(http.StatusOK)
			w.Write(fs.data[:len(fs.data)/2])
			w.(http.Flusher).Flush()
			conn, _, err := http.NewResponseController(w).Hijack()
			if err == nil {
				conn.Close()
			}
			return
		}
		if r.URL.Query().Has("norange") {
			r.Header.Del("Range")
		}
		serve(w, r)
	})
	// /stall sends half then waits (idle timeout on the client).
	var stalled atomic.Bool
	mux.HandleFunc("/stall/", func(w http.ResponseWriter, r *http.Request) {
		fs.requests.Add(1)
		if stalled.CompareAndSwap(false, true) {
			w.Header().Set("Content-Length", strconv.Itoa(len(fs.data)))
			w.WriteHeader(http.StatusOK)
			w.Write(fs.data[:len(fs.data)/2])
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
			return
		}
		serve(w, r)
	})
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(fs.data)))
		w.WriteHeader(http.StatusOK)
		w.Write(fs.data[:1])
		w.(http.Flusher).Flush()
		select {
		case <-fs.release:
		case <-r.Context().Done():
		}
	})
	mux.HandleFunc("/huge", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.FormatInt(MaxDownloadBytes+1, 10))
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/missing", http.NotFound)
	mux.HandleFunc("/error", func(w http.ResponseWriter, r *http.Request) {
		fs.requests.Add(1)
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	if tls {
		fs.srv = httptest.NewUnstartedServer(mux)
		fs.srv.Config.ErrorLog = log.New(io.Discard, "", 0) // refused handshakes are expected
		fs.srv.StartTLS()
	} else {
		fs.srv = httptest.NewServer(mux)
	}
	t.Cleanup(func() {
		close(fs.release)
		fs.srv.Close()
	})
	return fs
}

func (e *env) download(url string, target *Target, sha *string, onConflict string) string {
	e.t.Helper()
	req := DownloadRequest{URL: &url, Target: target, SHA256: sha}
	if onConflict != "" {
		req.OnConflict = &onConflict
	}
	id, apiErr, err := e.svc.StartDownload(req)
	if apiErr != nil || err != nil {
		e.t.Fatalf("start download: %v %v", apiErr, err)
	}
	return id
}

func staging() *Target { return &Target{Kind: KindStaging} }

func (e *env) stagedContent(j Job) (string, string) {
	e.t.Helper()
	id, _ := j.Result["stagingId"].(string)
	p, err := e.svc.StagingFile(id)
	if err != nil {
		e.t.Fatalf("staging file of %+v: %v", j, err)
	}
	data, _ := os.ReadFile(p)
	return p[strings.LastIndex(p, "/")+1:], string(data)
}

func TestDownloads(t *testing.T) {
	data := randomBytes(t, 300_000)
	fs := newFileServer(t, data, false)
	e := newEnv(t, func(o *Options) { o.Downloads.IdleTimeout = 300 * time.Millisecond })
	q := e.svc.Jobs()

	cases := []struct {
		path, name string
		ranges     bool
	}{
		{"/files/Sky%20Hack.bps", "Sky Hack.bps", false}, // with Content-Length
		{"/nolength", "from-header.ips", false},          // chunked, name from Content-Disposition
		{"/redirect/4", "final.bin", false},              // 5 redirects: allowed
		{"/cut/a.bin", "a.bin", true},                    // cut, then resumed with Range
		{"/stall/b.bin", "b.bin", true},                  // idle timeout, then resumed
	}
	for _, c := range cases {
		id := e.download(fs.srv.URL+c.path, staging(), ptr(sum(data)), "")
		j := waitJob(t, q, id, JobSucceeded, JobFailed)
		if j.State != JobSucceeded {
			t.Fatalf("%s: %+v", c.path, j.Error)
		}
		name, content := e.stagedContent(j)
		if name != c.name || content != string(data) {
			t.Errorf("%s: name %q, %d bytes", c.path, name, len(content))
		}
		if j.Progress.Done != int64(len(data)) {
			t.Errorf("%s: progress %+v", c.path, j.Progress)
		}
	}
	if fs.ranges.Load() != 2 {
		t.Errorf("Range requests: %d", fs.ranges.Load())
	}
	if des, _ := os.ReadDir(e.layout.UploadsDir()); len(des) != 0 {
		t.Errorf("leftovers %v", des)
	}
}

func TestDownloadRestartsWithoutRange(t *testing.T) {
	data := randomBytes(t, 100_000)
	fs := newFileServer(t, data, false)
	e := newEnv(t)
	id := e.download(fs.srv.URL+"/cut/c.bin?norange", staging(), nil, "")
	j := waitJob(t, e.svc.Jobs(), id, JobSucceeded, JobFailed)
	if _, content := e.stagedContent(j); content != string(data) {
		t.Fatalf("content: %d bytes", len(content))
	}
}

func TestDownloadToROMWithConflict(t *testing.T) {
	data := []byte("downloaded rom")
	fs := newFileServer(t, data, false)
	e := newEnv(t)
	q := e.svc.Jobs()
	// Name from the URL; it exists: fail (in the job), then rename.
	url := fs.srv.URL + "/files/Starlight%20Courier.gba"
	j := waitJob(t, q, e.download(url, &Target{Kind: KindROM, SystemID: gameID(gbaSystem)}, nil, ""), JobSucceeded, JobFailed)
	if j.State != JobFailed || j.Error.Code != CodeConflict {
		t.Fatalf("conflict: %+v", j)
	}
	j = waitJob(t, q, e.download(url, &Target{Kind: KindROM, SystemID: gameID(gbaSystem)}, nil, ConflictRename), JobSucceeded, JobFailed)
	if j.State != JobSucceeded || j.Result["gameId"] != gameID(gbaSystem+"/Starlight Courier (2).gba") {
		t.Fatalf("rename: %+v %+v", j, j.Error)
	}
	if e.read("Roms/"+gbaSystem+"/Starlight Courier (2).gba") != string(data) {
		t.Fatal("content")
	}
	// A given fileName wins over the URL.
	target := &Target{Kind: KindROM, SystemID: gameID(gbaSystem), FileName: ptr("Chosen.gba")}
	j = waitJob(t, q, e.download(url, target, nil, ""), JobSucceeded, JobFailed)
	if j.State != JobSucceeded || !e.exists("Roms/"+gbaSystem+"/Chosen.gba") {
		t.Fatalf("named: %+v", j)
	}
}

func TestDownloadFailures(t *testing.T) {
	data := randomBytes(t, 1000)
	fs := newFileServer(t, data, false)
	e := newEnv(t)
	q := e.svc.Jobs()
	cases := map[string]struct {
		path string
		sha  *string
		code string
	}{
		"404":            {"/missing", nil, CodeNotFound},
		"server error":   {"/error", nil, CodeInternal},
		"6 redirects":    {"/redirect/5", nil, CodeInvalidRequest},
		"to ftp":         {"/to-ftp", nil, CodeInvalidRequest},
		"too large":      {"/huge", nil, CodeTooLarge},
		"bad sha":        {"/files/x.bin", ptr(sum([]byte("other"))), CodeChecksumMismatch},
		"refused server": {"", nil, CodeInternal},
	}
	for name, c := range cases {
		url := fs.srv.URL + c.path
		if c.path == "" {
			url = "http://127.0.0.1:1/x"
		}
		j := waitJob(t, q, e.download(url, staging(), c.sha, ""), JobSucceeded, JobFailed)
		if j.State != JobFailed || j.Error.Code != c.code {
			t.Errorf("%s: %s %+v", name, j.State, j.Error)
		}
		if j.Error != nil && strings.Contains(j.Error.Message, "127.0.0.1:1/x") {
			t.Errorf("%s: URL in the error: %s", name, j.Error.Message)
		}
	}
	// 3 retries of a 500: 4 requests.
	if n := fs.requests.Load(); n < 4 {
		t.Errorf("requests %d", n)
	}
	// Not enough space for the announced size.
	*e.free = 1000
	j := waitJob(t, q, e.download(fs.srv.URL+"/files/x.bin", staging(), nil, ""), JobSucceeded, JobFailed)
	if j.Error == nil || j.Error.Code != CodeInsufficientStorage {
		t.Errorf("space: %+v", j.Error)
	}
	if des, _ := os.ReadDir(e.layout.UploadsDir()); len(des) != 0 {
		t.Errorf("leftovers %v", des)
	}
}

func TestDownloadRequestErrors(t *testing.T) {
	e := newEnv(t)
	for _, u := range []string{"", "ftp://example.org/x", "file:///etc/passwd", "example.org/x", "http://", "javascript:alert(1)"} {
		req := DownloadRequest{URL: ptr(u), Target: staging()}
		if _, apiErr, _ := e.svc.StartDownload(req); apiErr == nil || apiErr.Code != CodeInvalidRequest {
			t.Errorf("%q: %v", u, apiErr)
		}
	}
	bad := []DownloadRequest{
		{URL: ptr("http://x/y")},
		{URL: ptr("http://x/y"), Target: staging(), SHA256: ptr("nope")},
		{URL: ptr("http://x/y"), Target: staging(), OnConflict: ptr("x")},
		{URL: ptr("http://x/y"), Target: &Target{Kind: KindStaging, FileName: ptr("a/b")}},
		{URL: ptr("http://x/y"), Target: &Target{Kind: KindROM, SystemID: "!"}},
	}
	for i, req := range bad {
		if _, apiErr, _ := e.svc.StartDownload(req); apiErr == nil {
			t.Errorf("case %d accepted", i)
		}
	}
}

func TestDownloadCancelDeletesPartialFile(t *testing.T) {
	data := randomBytes(t, 10_000)
	fs := newFileServer(t, data, false)
	e := newEnv(t)
	q := e.svc.Jobs()
	id := e.download(fs.srv.URL+"/slow", staging(), nil, "")
	deadline := time.Now().Add(5 * time.Second)
	for {
		j, _ := q.Get(id)
		if j.Progress.Done == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("download did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if des, _ := os.ReadDir(e.layout.UploadsDir()); len(des) != 1 {
		t.Fatalf("partial file: %v", des)
	}
	j, apiErr := q.Cancel(id)
	if apiErr != nil || j.State != JobCancelled {
		t.Fatalf("cancel: %+v %v", j, apiErr)
	}
	if des, _ := os.ReadDir(e.layout.UploadsDir()); len(des) != 0 {
		t.Fatalf("partial file kept: %v", des)
	}
}

func TestDownloadHTTPS(t *testing.T) {
	data := []byte("over tls")
	fs := newFileServer(t, data, true)
	pool := x509.NewCertPool()
	pool.AddCert(fs.srv.Certificate())
	e := newEnv(t, func(o *Options) { o.Downloads.RootCAs = pool })
	j := waitJob(t, e.svc.Jobs(), e.download(fs.srv.URL+"/files/tls.bin", staging(), nil, ""), JobSucceeded, JobFailed)
	if j.State != JobSucceeded {
		t.Fatalf("%+v", j.Error)
	}
	// Without the test CA: refused.
	e2 := newEnv(t, func(o *Options) { o.Downloads.RootCAs = x509.NewCertPool() })
	j = waitJob(t, e2.svc.Jobs(), e2.download(fs.srv.URL+"/files/tls.bin", staging(), nil, ""), JobSucceeded, JobFailed)
	if j.State != JobFailed {
		t.Fatal("untrusted certificate accepted")
	}
}

func TestLoadRootCAs(t *testing.T) {
	fs := newFileServer(t, nil, true)
	pemData := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: fs.srv.Certificate().Raw})
	dir := t.TempDir()
	good, bad := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "bad.pem")
	os.WriteFile(good, pemData, 0o644)
	os.WriteFile(bad, []byte("nope"), 0o644)
	empty := func() (*x509.CertPool, error) { return x509.NewCertPool(), nil }
	failing := func() (*x509.CertPool, error) { return nil, errors.New("no store") }
	system := func() (*x509.CertPool, error) {
		p := x509.NewCertPool()
		p.AppendCertsFromPEM(pemData)
		return p, nil
	}
	if p, err := loadRootCAs(system, bad); err != nil || p == nil {
		t.Fatalf("system store: %v", err)
	}
	for _, sys := range []func() (*x509.CertPool, error){empty, failing} {
		if p, err := loadRootCAs(sys, good); err != nil || p.Equal(x509.NewCertPool()) {
			t.Fatalf("bundle fallback: %v", err)
		}
		if _, err := loadRootCAs(sys, bad); err == nil {
			t.Fatal("bad bundle accepted")
		}
		if _, err := loadRootCAs(sys, filepath.Join(dir, "missing.pem")); err == nil {
			t.Fatal("missing bundle accepted")
		}
	}
}

func TestParseContentRange(t *testing.T) {
	if s, total, ok := parseContentRange("bytes 10-19/20"); !ok || s != 10 || total != 20 {
		t.Fatal("parseContentRange")
	}
	for _, v := range []string{"", "bytes x-1/2", "bytes 1-2", "items 1-2/3", "bytes -1-2/3", "bytes 1-2/x"} {
		if _, _, ok := parseContentRange(v); ok {
			t.Errorf("%q accepted", v)
		}
	}
	if s, total, ok := parseContentRange("bytes 5-9/*"); !ok || s != 5 || total != -1 {
		t.Fatal("unknown total")
	}
}

func TestDownloadLogsNoQuery(t *testing.T) {
	fs := newFileServer(t, []byte("x"), false)
	e := newEnv(t)
	q := e.svc.Jobs()
	for _, u := range []string{"http://127.0.0.1:1/x?token=s3cret", fs.srv.URL + "/error?token=s3cret", fs.srv.URL + "/redirect/9?token=s3cret"} {
		j := waitJob(t, q, e.download(u, staging(), nil, ""), JobSucceeded, JobFailed)
		if j.State != JobFailed || strings.Contains(j.Error.Message, "s3cret") {
			t.Errorf("%s: %+v", u, j.Error)
		}
	}
	if strings.Contains(e.logs.String(), "s3cret") || strings.Contains(e.logs.String(), "/x?") {
		t.Fatalf("query in the log:\n%s", e.logs.String())
	}
}
