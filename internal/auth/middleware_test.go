package auth

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type mwFixture struct {
	t      *testing.T
	clock  *fakeClock
	key    []byte
	server *httptest.Server
	seen   chan seenRequest
}

type seenRequest struct {
	client Client
	body   string
}

func newMWFixture(t *testing.T, maxBody int64) *mwFixture {
	t.Helper()
	clients, err := OpenClientStore(filepath.Join(t.TempDir(), "clients.json"))
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{0x42}, KeySize)
	if err := clients.Put(Client{ID: testClientA, Name: "iPhone", Key: key}); err != nil {
		t.Fatal(err)
	}
	clock := newFakeClock()
	f := &mwFixture{t: t, clock: clock, key: key, seen: make(chan seenRequest, 10)}
	v := &Verifier{
		Clients:      clients,
		Nonces:       NewNonceCache(NonceTTL, 100, clock.Now),
		Now:          clock.Now,
		MaxBodyBytes: maxBody,
	}
	f.server = httptest.NewServer(v.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, ok := ClientFromContext(r.Context())
		if !ok {
			t.Error("no client in context")
		}
		body, _ := io.ReadAll(r.Body)
		f.seen <- seenRequest{client: c, body: string(body)}
		w.WriteHeader(http.StatusNoContent)
	})))
	t.Cleanup(f.server.Close)
	return f
}

type signOpts struct {
	method, target string
	body           []byte
	unsigned       bool
	clientID       string
	key            []byte
	time           time.Time
	nonce          string
}

// request builds a request signed like the app does.
func (f *mwFixture) request(o signOpts) *http.Request {
	f.t.Helper()
	if o.clientID == "" {
		o.clientID = testClientA
	}
	if o.key == nil {
		o.key = f.key
	}
	if o.time.IsZero() {
		o.time = f.clock.Now()
	}
	if o.nonce == "" {
		n := make([]byte, RequestNonceSize)
		_, _ = rand.Read(n)
		o.nonce = EncodeB64U(n)
	}
	path, query, _ := strings.Cut(o.target, "?")
	content := ContentSHA256(o.body)
	if o.unsigned {
		content = UnsignedPayload
	}
	ts := strconv.FormatInt(o.time.Unix(), 10)
	canonical, err := CanonicalRequest(RequestParts{
		Method: o.method, Path: path, RawQuery: query, Time: ts, Nonce: o.nonce, ContentSHA256: content,
	})
	if err != nil {
		f.t.Fatal(err)
	}
	req, err := http.NewRequest(strings.ToUpper(o.method), f.server.URL+o.target, bytes.NewReader(o.body))
	if err != nil {
		f.t.Fatal(err)
	}
	req.Header.Set(HeaderClient, o.clientID)
	req.Header.Set(HeaderTime, ts)
	req.Header.Set(HeaderNonce, o.nonce)
	req.Header.Set(HeaderContentSHA256, content)
	req.Header.Set(HeaderSignature, Sign(o.key, canonical))
	return req
}

// do sends req and returns the status and the error code (if any).
func (f *mwFixture) do(req *http.Request) (int, string) {
	f.t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return resp.StatusCode, ""
	}
	var e struct {
		Error ErrorBody `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&e); err != nil {
		f.t.Fatalf("status %d with a non-JSON body: %v", resp.StatusCode, err)
	}
	if resp.Header.Get("Content-Type") != "application/json" {
		f.t.Fatalf("Content-Type = %q", resp.Header.Get("Content-Type"))
	}
	if e.Error.Details == nil {
		f.t.Fatal("details must be an object")
	}
	return resp.StatusCode, e.Error.Code
}

func TestMiddlewareAcceptsSignedRequests(t *testing.T) {
	f := newMWFixture(t, 0)
	cases := []signOpts{
		{method: "GET", target: "/api/v1/device"},
		{method: "GET", target: "/api/v1/systems/R2FtZQ/games?folderId=R2FtZS9I&sort=a%20b"},
		{method: "POST", target: "/api/v1/games/R2FtZQ/save/backups", body: []byte(`{"label":"Avant"}`)},
		{method: "DELETE", target: "/api/v1/clients/me"},
		{method: "PUT", target: "/api/v1/uploads/up_01J", body: []byte("chunk"), unsigned: true},
		{method: "get", target: "/api/v1/device"},
	}
	for _, c := range cases {
		status, code := f.do(f.request(c))
		if status != http.StatusNoContent {
			t.Fatalf("%s %s: %d %s", c.method, c.target, status, code)
		}
		got := <-f.seen
		if got.client.ID != testClientA || got.body != string(c.body) {
			t.Fatalf("%s %s: handler saw %+v", c.method, c.target, got)
		}
	}
}

func TestMiddlewareClockSkewBoundaries(t *testing.T) {
	f := newMWFixture(t, 0)
	now := f.clock.Now()
	for _, d := range []time.Duration{MaxClockSkew, -MaxClockSkew} {
		if status, code := f.do(f.request(signOpts{method: "GET", target: "/api/v1/device", time: now.Add(d)})); status != http.StatusNoContent {
			t.Fatalf("skew %v refused: %d %s", d, status, code)
		}
		<-f.seen
	}
	for _, d := range []time.Duration{MaxClockSkew + time.Second, -MaxClockSkew - time.Second} {
		status, code := f.do(f.request(signOpts{method: "GET", target: "/api/v1/device", time: now.Add(d)}))
		if status != http.StatusUnauthorized || code != CodeClockSkew {
			t.Fatalf("skew %v: %d %s", d, status, code)
		}
	}
}

func TestMiddlewareRejections(t *testing.T) {
	f := newMWFixture(t, 1024)
	otherKey := bytes.Repeat([]byte{0x43}, KeySize)
	cases := []struct {
		name   string
		build  func() *http.Request
		status int
		code   string
	}{
		{"missing-client", func() *http.Request {
			r := f.request(signOpts{method: "GET", target: "/api/v1/device"})
			r.Header.Del(HeaderClient)
			return r
		}, 401, CodeUnknownClient},
		{"unknown-client", func() *http.Request {
			return f.request(signOpts{method: "GET", target: "/api/v1/device", clientID: testClientB})
		}, 401, CodeUnknownClient},
		{"wrong-key", func() *http.Request {
			return f.request(signOpts{method: "GET", target: "/api/v1/device", key: otherKey})
		}, 401, CodeBadSignature},
		{"missing-signature", func() *http.Request {
			r := f.request(signOpts{method: "GET", target: "/api/v1/device"})
			r.Header.Del(HeaderSignature)
			return r
		}, 401, CodeBadSignature},
		{"missing-time", func() *http.Request {
			r := f.request(signOpts{method: "GET", target: "/api/v1/device"})
			r.Header.Del(HeaderTime)
			return r
		}, 401, CodeBadSignature},
		{"malformed-time", func() *http.Request {
			r := f.request(signOpts{method: "GET", target: "/api/v1/device"})
			r.Header.Set(HeaderTime, "yesterday")
			return r
		}, 401, CodeBadSignature},
		{"missing-nonce", func() *http.Request {
			r := f.request(signOpts{method: "GET", target: "/api/v1/device"})
			r.Header.Del(HeaderNonce)
			return r
		}, 401, CodeBadSignature},
		{"missing-content-sha", func() *http.Request {
			r := f.request(signOpts{method: "GET", target: "/api/v1/device"})
			r.Header.Del(HeaderContentSHA256)
			return r
		}, 401, CodeBadSignature},
		{"modified-body", func() *http.Request {
			r := f.request(signOpts{method: "POST", target: "/api/v1/games/R2FtZQ/save/backups", body: []byte(`{"label":"a"}`)})
			r.Body = io.NopCloser(strings.NewReader(`{"label":"b"}`))
			return r
		}, 401, CodeBadSignature},
		{"modified-path", func() *http.Request {
			r := f.request(signOpts{method: "DELETE", target: "/api/v1/games/R2FtZQ"})
			r.URL.Path = "/api/v1/games/R2FtZR"
			return r
		}, 401, CodeBadSignature},
		{"modified-query", func() *http.Request {
			r := f.request(signOpts{method: "DELETE", target: "/api/v1/games/R2FtZQ?keepSave=true"})
			r.URL.RawQuery = "keepSave=false"
			return r
		}, 401, CodeBadSignature},
		{"modified-method", func() *http.Request {
			r := f.request(signOpts{method: "GET", target: "/api/v1/jobs/job_1"})
			r.Method = "DELETE"
			return r
		}, 401, CodeBadSignature},
		{"unsigned-on-other-route", func() *http.Request {
			return f.request(signOpts{method: "PUT", target: "/api/v1/games/R2FtZQ/save", body: []byte("sav"), unsigned: true})
		}, 401, CodeBadSignature},
		{"unsigned-on-upload-create", func() *http.Request {
			return f.request(signOpts{method: "POST", target: "/api/v1/uploads/up_1", unsigned: true})
		}, 401, CodeBadSignature},
		{"body-too-large", func() *http.Request {
			return f.request(signOpts{method: "PUT", target: "/api/v1/games/R2FtZQ/save", body: bytes.Repeat([]byte{1}, 1025)})
		}, 413, CodeTooLarge},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, code := f.do(c.build())
			if status != c.status || code != c.code {
				t.Fatalf("got %d %s, want %d %s", status, code, c.status, c.code)
			}
		})
	}
	select {
	case s := <-f.seen:
		t.Fatalf("a rejected request reached the handler: %+v", s)
	default:
	}
}

func TestMiddlewareReplay(t *testing.T) {
	f := newMWFixture(t, 0)
	opts := signOpts{method: "GET", target: "/api/v1/device", nonce: EncodeB64U(bytes.Repeat([]byte{5}, 16))}
	if status, _ := f.do(f.request(opts)); status != http.StatusNoContent {
		t.Fatalf("first request: %d", status)
	}
	<-f.seen
	if status, code := f.do(f.request(opts)); status != 401 || code != CodeReplayedRequest {
		t.Fatalf("replay: %d %s", status, code)
	}
	// Still refused just before the TTL; accepted again after (the
	// timestamp is then refreshed by the client).
	f.clock.Advance(NonceTTL - time.Second)
	if status, code := f.do(f.request(opts)); status != 401 || code != CodeReplayedRequest {
		t.Fatalf("replay before TTL: %d %s", status, code)
	}
	f.clock.Advance(time.Second)
	if status, _ := f.do(f.request(opts)); status != http.StatusNoContent {
		t.Fatalf("after TTL: %d", status)
	}
	<-f.seen
}

func TestMiddlewareBadSignatureDoesNotBurnNonce(t *testing.T) {
	f := newMWFixture(t, 0)
	nonce := EncodeB64U(bytes.Repeat([]byte{6}, 16))
	forged := f.request(signOpts{method: "GET", target: "/api/v1/device", nonce: nonce, key: bytes.Repeat([]byte{1}, KeySize)})
	if status, code := f.do(forged); code != CodeBadSignature {
		t.Fatalf("forged: %d %s", status, code)
	}
	if status, _ := f.do(f.request(signOpts{method: "GET", target: "/api/v1/device", nonce: nonce})); status != http.StatusNoContent {
		t.Fatalf("legitimate request refused after a forged one: %d", status)
	}
	<-f.seen
}

func TestMiddlewareCheckOrder(t *testing.T) {
	f := newMWFixture(t, 0)
	// Unknown client wins over a bad signature and a skewed clock.
	r := f.request(signOpts{method: "GET", target: "/api/v1/device", clientID: testClientB, time: f.clock.Now().Add(time.Hour)})
	if _, code := f.do(r); code != CodeUnknownClient {
		t.Fatalf("got %s, want %s", code, CodeUnknownClient)
	}
	// Clock skew wins over a bad signature.
	r = f.request(signOpts{method: "GET", target: "/api/v1/device", key: bytes.Repeat([]byte{1}, KeySize), time: f.clock.Now().Add(time.Hour)})
	if _, code := f.do(r); code != CodeClockSkew {
		t.Fatalf("got %s, want %s", code, CodeClockSkew)
	}
}

func TestAllowsUnsignedBody(t *testing.T) {
	cases := map[string]bool{
		"PUT /api/v1/uploads/up_1":          true,
		"put /api/v1/uploads/up_1":          true,
		"PUT /api/v1/uploads/":              false,
		"PUT /api/v1/uploads/up_1/complete": false,
		"POST /api/v1/uploads/up_1":         false,
		"PUT /api/v1/games/x/save":          false,
		"PUT /api/v1/uploadsx/up_1":         false,
	}
	for in, want := range cases {
		m, p, _ := strings.Cut(in, " ")
		if got := AllowsUnsignedBody(m, p); got != want {
			t.Errorf("%s: %v, want %v", in, got, want)
		}
	}
}
