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
	t        *testing.T
	clock    *fakeClock
	key      []byte
	verifier *Verifier
	handler  http.Handler
	server   *httptest.Server
	seen     chan seenRequest
}

type seenRequest struct {
	client Client
	body   string
}

func newMWFixture(t *testing.T, maxBody int64) *mwFixture {
	t.Helper()
	return newMWFixtureWith(t, maxBody, 100)
}

func newMWFixtureWith(t *testing.T, maxBody int64, nonceCapacity int) *mwFixture {
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
	v := &Verifier{
		Clients: clients,
		Nonces:  NewNonceCache(NonceTTL, nonceCapacity, clock.Now),
		Now:     clock.Now,
	}
	f := &mwFixture{t: t, clock: clock, key: key, seen: make(chan seenRequest, 10), verifier: v}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, ok := ClientFromContext(r.Context())
		if !ok {
			t.Error("no client in context")
		}
		body, _ := io.ReadAll(r.Body)
		f.seen <- seenRequest{client: c, body: string(body)}
		w.WriteHeader(http.StatusNoContent)
	})
	// Route options are fixed at registration, as the M2 router will do.
	jsonRoute, saveRoute := RouteJSON, RouteSave
	if maxBody > 0 {
		jsonRoute.BodyLimit, saveRoute.BodyLimit = maxBody, maxBody
	}
	mux := http.NewServeMux()
	mux.Handle("PUT /api/v1/uploads/{uploadId}", v.Require(RouteChunk, h))
	mux.Handle("PUT /api/v1/games/{gameId}/save", v.Require(saveRoute, h))
	mux.Handle("/", v.Require(jsonRoute, h))
	f.handler = mux
	f.server = httptest.NewServer(f.handler)
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
	timeRaw        string // overrides time when set
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
	if o.timeRaw != "" {
		ts = o.timeRaw
	}
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
	// Far away times must not overflow the comparison (time.Time.Sub
	// saturates and the absolute value of the minimum Duration is negative).
	for _, raw := range []string{"0", "1", "99999999999", "9223372036854775807", "253402300800"} {
		status, code := f.do(f.request(signOpts{method: "GET", target: "/api/v1/device", timeRaw: raw}))
		if status != http.StatusUnauthorized || code != CodeClockSkew {
			t.Fatalf("time %s: %d %s, want 401 %s", raw, status, code, CodeClockSkew)
		}
	}
}

func TestWithinClockSkewExtremes(t *testing.T) {
	now := newFakeClock().Now()
	for _, ts := range []int64{0, -1 << 62, 1<<63 - 1, 99999999999} {
		if withinClockSkew(time.Unix(ts, 0), now) {
			t.Errorf("ts %d accepted", ts)
		}
	}
}

// A nonce must stay in the cache as long as the clock still accepts its
// timestamp: a request sent with time = now + 300 s can be replayed until
// now + 600 s, the exact instant the TTL ends.
func TestMiddlewareReplayAtExactWindowEdge(t *testing.T) {
	f := newMWFixture(t, 0)
	start := f.clock.Now()
	opts := signOpts{method: "GET", target: "/api/v1/device", time: start.Add(MaxClockSkew),
		nonce: EncodeB64U(bytes.Repeat([]byte{7}, 16))}
	if status, code := f.do(f.request(opts)); status != http.StatusNoContent {
		t.Fatalf("first request: %d %s", status, code)
	}
	<-f.seen
	f.clock.Advance(2 * MaxClockSkew)
	if status, code := f.do(f.request(opts)); status != 401 || code != CodeReplayedRequest {
		t.Fatalf("replay at the window edge: %d %s, want 401 %s", status, code, CodeReplayedRequest)
	}
	f.clock.Advance(time.Second)
	if status, code := f.do(f.request(opts)); status != 401 || code != CodeClockSkew {
		t.Fatalf("after the edge the clock refuses it: %d %s", status, code)
	}
}

func TestMiddlewareNonceCacheFull(t *testing.T) {
	f := newMWFixtureWith(t, 0, 1)
	if status, _ := f.do(f.request(signOpts{method: "GET", target: "/api/v1/device"})); status != http.StatusNoContent {
		t.Fatalf("first request: %d", status)
	}
	<-f.seen
	status, code := f.do(f.request(signOpts{method: "GET", target: "/api/v1/device"}))
	if status != http.StatusTooManyRequests || code != CodeTooManyAttempts {
		t.Fatalf("full cache: %d %s, want 429 %s", status, code, CodeTooManyAttempts)
	}
}

// countingReader counts the bytes read from it.
type countingReader struct {
	n, size int
}

func (c *countingReader) Read(p []byte) (int, error) {
	if c.n >= c.size {
		return 0, io.EOF
	}
	k := min(len(p), c.size-c.n)
	for i := range p[:k] {
		p[i] = 'x'
	}
	c.n += k
	return k, nil
}

func TestMiddlewareDoesNotReadBodyOfForgedRequest(t *testing.T) {
	f := newMWFixture(t, 0)
	// Signed with the wrong key, announcing the hash of a small body.
	req := f.request(signOpts{method: "PUT", target: "/api/v1/games/R2FtZQ/save", body: []byte("small"),
		key: bytes.Repeat([]byte{1}, KeySize)})
	body := &countingReader{size: BodyLimitSave}
	req.Body = io.NopCloser(body)
	req.ContentLength = BodyLimitSave
	req.RequestURI = "/api/v1/games/R2FtZQ/save"
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), CodeBadSignature) {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	if body.n != 0 {
		t.Fatalf("%d body bytes read before the signature was checked", body.n)
	}
}

func TestMiddlewareDefaultBodyLimits(t *testing.T) {
	f := newMWFixture(t, 0)
	big := bytes.Repeat([]byte{'a'}, BodyLimitJSON+1)
	status, code := f.do(f.request(signOpts{method: "POST", target: "/api/v1/games/R2FtZQ/save/backups", body: big}))
	if status != http.StatusRequestEntityTooLarge || code != CodeTooLarge {
		t.Fatalf("JSON route: %d %s", status, code)
	}
	save := bytes.Repeat([]byte{'s'}, 2*BodyLimitJSON)
	if status, code := f.do(f.request(signOpts{method: "PUT", target: "/api/v1/games/R2FtZQ/save", body: save})); status != http.StatusNoContent {
		t.Fatalf("save route: %d %s", status, code)
	}
	if got := <-f.seen; len(got.body) != len(save) {
		t.Fatalf("handler got %d bytes", len(got.body))
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
	// Still refused up to the TTL included; accepted again after (the
	// timestamp is then refreshed by the client).
	f.clock.Advance(NonceTTL)
	if status, code := f.do(f.request(opts)); status != 401 || code != CodeReplayedRequest {
		t.Fatalf("replay at TTL: %d %s", status, code)
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

// Replaying the headers of a request whose body is still being received
// must not make the pak read a second body.
func TestMiddlewareConcurrentReplayDoesNotReadBody(t *testing.T) {
	f := newMWFixture(t, 0)
	body := bytes.Repeat([]byte{'s'}, 1024)
	opts := signOpts{method: "PUT", target: "/api/v1/games/R2FtZQ/save", body: body,
		nonce: EncodeB64U(bytes.Repeat([]byte{8}, 16))}

	pr, pw := io.Pipe()
	first := f.request(opts)
	first.Body = pr
	first.RequestURI = "/api/v1/games/R2FtZQ/save"
	done := make(chan int)
	go func() {
		rec := httptest.NewRecorder()
		f.handler.ServeHTTP(rec, first)
		done <- rec.Code
	}()
	// Wait until the first request reserved its nonce.
	for !f.verifier.Nonces.Seen(testClientA, opts.nonce) {
		time.Sleep(time.Millisecond)
	}

	replay := f.request(opts)
	counter := &countingReader{size: BodyLimitSave}
	replay.Body = io.NopCloser(counter)
	replay.ContentLength = BodyLimitSave
	replay.RequestURI = "/api/v1/games/R2FtZQ/save"
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, replay)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), CodeReplayedRequest) {
		t.Fatalf("replay: %d %s", rec.Code, rec.Body.String())
	}
	if counter.n != 0 {
		t.Fatalf("%d body bytes of the replay were read", counter.n)
	}

	go func() { _, _ = pw.Write(body); _ = pw.Close() }()
	if code := <-done; code != http.StatusNoContent {
		t.Fatalf("first request: %d", code)
	}
	<-f.seen
}

func TestMiddlewareFailureAfterReservationConsumesNonce(t *testing.T) {
	f := newMWFixture(t, 0)
	opts := signOpts{method: "POST", target: "/api/v1/games/R2FtZQ/save/backups", body: []byte(`{"label":"a"}`),
		nonce: EncodeB64U(bytes.Repeat([]byte{9}, 16))}
	r := f.request(opts)
	r.Body = io.NopCloser(strings.NewReader(`{"label":"b"}`))
	if _, code := f.do(r); code != CodeBadSignature {
		t.Fatalf("modified body: %s", code)
	}
	if _, code := f.do(f.request(opts)); code != CodeReplayedRequest {
		t.Fatalf("same nonce after a body failure: %s, want %s", code, CodeReplayedRequest)
	}
}

func TestMiddlewareRetryAfterOnFullCache(t *testing.T) {
	f := newMWFixtureWith(t, 0, 1)
	if status, _ := f.do(f.request(signOpts{method: "GET", target: "/api/v1/device"})); status != http.StatusNoContent {
		t.Fatal("first request refused")
	}
	<-f.seen
	f.clock.Advance(4 * time.Minute)
	resp, err := http.DefaultClient.Do(f.request(signOpts{method: "GET", target: "/api/v1/device"}))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status %d", resp.StatusCode)
	}
	// The only nonce expires 10 min after it was added: 6 min from now.
	if got := resp.Header.Get("Retry-After"); got != "361" && got != "360" {
		t.Fatalf("Retry-After = %q", got)
	}
}

// The body limit comes from the route registration: spelling the path
// differently ("..", %2F, %73ave) cannot reach a larger limit.
func TestRouteOptionsAreFixedAtRegistration(t *testing.T) {
	f := newMWFixture(t, 0)
	big := bytes.Repeat([]byte{'a'}, 2*BodyLimitJSON)
	for _, target := range []string{
		"/api/v1/games/R2FtZQ/x/../save",
		"/api/v1/games/R2FtZQ%2Fsave",
		"/api/v1/games/R2FtZQ/%73ave/x",
		"/api/v1/games/R2FtZQ/cheats",
	} {
		req := f.request(signOpts{method: "PUT", target: target, body: big})
		rec := httptest.NewRecorder()
		req.RequestURI = target
		f.handler.ServeHTTP(rec, req)
		if rec.Code == http.StatusNoContent {
			<-f.seen
			t.Fatalf("%s: a 2 MiB body was accepted outside the save route", target)
		}
	}
	// UNSIGNED is only accepted on the route registered with RouteChunk.
	for _, target := range []string{"/api/v1/uploads/up_1/complete", "/api/v1/games/R2FtZQ/save"} {
		req := f.request(signOpts{method: "PUT", target: target, body: []byte("x"), unsigned: true})
		if status, code := f.do(req); status != 401 || code != CodeBadSignature {
			t.Fatalf("%s: %d %s", target, status, code)
		}
	}
	// A chunk may also be sent with its real hash, up to BodyLimitChunk.
	chunk := bytes.Repeat([]byte{'c'}, 4*BodyLimitJSON)
	if status, code := f.do(f.request(signOpts{method: "PUT", target: "/api/v1/uploads/up_1", body: chunk})); status != http.StatusNoContent {
		t.Fatalf("signed chunk: %d %s", status, code)
	}
	<-f.seen
}

func TestMiddlewareRefusesDuplicateQueryKeys(t *testing.T) {
	f := newMWFixture(t, 0)
	for _, target := range []string{
		"/api/v1/systems/x/games?folderId=a&folderId=b",
		"/api/v1/systems/x/games?folderId=a&folderId=a",
		"/api/v1/systems/x/games?folderId=a&%66olderId=b", // %66 is "f"
		"/api/v1/x?a&a=",
	} {
		req := f.request(signOpts{method: "GET", target: target})
		if status, code := f.do(req); status != http.StatusBadRequest || code != CodeInvalidRequest {
			t.Errorf("%s: got %d %s, want 400 %s", target, status, code, CodeInvalidRequest)
		}
		// The nonce is consumed: the same request again is a replay.
		again := req.Clone(req.Context())
		again.Body = http.NoBody
		if status, code := f.do(again); status != http.StatusUnauthorized || code != CodeReplayedRequest {
			t.Errorf("%s replayed: got %d %s, want 401 %s", target, status, code, CodeReplayedRequest)
		}
	}
	// Distinct keys and repeated empty pairs are fine.
	req := f.request(signOpts{method: "GET", target: "/api/v1/x?a=1&&b=1&c"})
	if status, code := f.do(req); status != http.StatusNoContent {
		t.Errorf("distinct keys: got %d %s", status, code)
	}
}

func TestMiddlewareRefusesSemicolons(t *testing.T) {
	f := newMWFixture(t, 0)
	for _, target := range []string{"/api/v1/x?a=1;b=2", "/api/v1/x?a=1&b=;"} {
		if status, code := f.do(f.request(signOpts{method: "GET", target: target})); status != http.StatusBadRequest || code != CodeInvalidRequest {
			t.Errorf("%s: got %d %s", target, status, code)
		}
	}
	// An encoded semicolon is a plain character.
	if status, code := f.do(f.request(signOpts{method: "GET", target: "/api/v1/x?a=1%3Bb"})); status != http.StatusNoContent {
		t.Errorf("encoded semicolon: %d %s", status, code)
	}
}

func TestMiddlewareDuplicateKeyNeedsValidSignature(t *testing.T) {
	f := newMWFixture(t, 0)
	req := f.request(signOpts{method: "GET", target: "/api/v1/x?a=1&a=2", key: bytes.Repeat([]byte{1}, KeySize)})
	if status, code := f.do(req); status != http.StatusUnauthorized || code != CodeBadSignature {
		t.Errorf("got %d %s, want 401 %s", status, code, CodeBadSignature)
	}
}

func TestNewVerifierSharesTheClock(t *testing.T) {
	clock := newFakeClock()
	v := NewVerifier(nil, clock.Now, 10, nil)
	if v.Now == nil || v.Nonces == nil {
		t.Fatal("NewVerifier left a field empty")
	}
	signedAt := clock.Now()
	if err := v.Nonces.Add(testClientA, "n", signedAt); err != nil {
		t.Fatal(err)
	}
	clock.Advance(NonceTTL)
	if !v.Nonces.Seen(testClientA, "n") {
		t.Fatal("nonce forgotten before its TTL on the shared clock")
	}
	clock.Advance(time.Second)
	if v.Nonces.Seen(testClientA, "n") {
		t.Fatal("nonce still remembered after its TTL: the cache does not use the verifier clock")
	}
	if got := NewVerifier(nil, nil, 0, nil); got.Now == nil {
		t.Fatal("nil clock not defaulted")
	}
}

// TestMiddlewareGate: the Gate runs after the nonce is reserved and before
// the body is read; its rejection is the answer, with its details.
func TestMiddlewareGate(t *testing.T) {
	f := newMWFixture(t, 0)
	var gotOpts RouteOptions
	calls := 0
	f.verifier.Gate = func(r *http.Request, c Client, opts RouteOptions) *Rejection {
		calls++
		gotOpts = opts
		if c.ID != testClientA {
			t.Errorf("gate client %q", c.ID)
		}
		if r.Header.Get("X-Pass") != "" {
			return nil
		}
		return &Rejection{Status: http.StatusLocked, Code: "read_only", Message: "no", Details: map[string]any{"writer": "Other"}}
	}
	const nonce = "AAAAAAAAAAAAAAAAAAAAAA"
	req := f.request(signOpts{method: "PUT", target: "/api/v1/games/R2FtZQ/save", body: []byte("small"), nonce: nonce})
	body := &countingReader{size: 5}
	req.Body = io.NopCloser(body)
	req.ContentLength = 5
	req.RequestURI = "/api/v1/games/R2FtZQ/save"
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusLocked || !strings.Contains(rec.Body.String(), `"writer":"Other"`) {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	if body.n != 0 {
		t.Fatalf("%d body bytes read before the gate refused", body.n)
	}
	if gotOpts.BodyLimit != BodyLimitSave {
		t.Errorf("gate got the options %+v", gotOpts)
	}
	// The nonce was reserved: the same request is now a replay, refused
	// before the gate.
	before := calls
	replay := f.request(signOpts{method: "PUT", target: "/api/v1/games/R2FtZQ/save", body: []byte("small"), nonce: nonce})
	if status, code := f.do(replay); status != 401 || code != CodeReplayedRequest {
		t.Fatalf("replay after the gate: %d %s", status, code)
	}
	if calls != before {
		t.Fatal("the gate saw a replayed request")
	}
	// A forged request never reaches the gate.
	if status, _ := f.do(f.request(signOpts{method: "GET", target: "/api/v1/systems", key: bytes.Repeat([]byte{1}, KeySize)})); status != 401 || calls != before {
		t.Fatalf("forged: %d, gate calls %d", status, calls-before)
	}
	// Let through.
	ok := f.request(signOpts{method: "POST", target: "/api/v1/x", body: []byte("{}")})
	ok.Header.Set("X-Pass", "1")
	if status, _ := f.do(ok); status != http.StatusNoContent {
		t.Fatalf("let through: %d", status)
	}
	if s := <-f.seen; s.body != "{}" {
		t.Fatalf("body %q", s.body)
	}
	// An UNSIGNED chunk goes through the gate too.
	chunk := f.request(signOpts{method: "PUT", target: "/api/v1/uploads/u1", body: []byte("abc"), unsigned: true})
	if status, code := f.do(chunk); status != http.StatusLocked || code != "read_only" {
		t.Fatalf("unsigned chunk: %d %s", status, code)
	}
}
