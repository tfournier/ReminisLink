package testutil

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/auth"
	"github.com/tfournier/ReminisLink/internal/store"
)

// Client sends requests signed like the app (docs/specs/03-protocole.md §4).
type Client struct {
	BaseURL  string // e.g. httptest.Server.URL, without /api/v1
	ClientID string
	Key      []byte
	Now      func() time.Time // nil: time.Now
	HTTP     *http.Client     // nil: http.DefaultClient
}

// PairClient registers a new client with a random key in clients (as a
// successful pairing would) and returns a Client for baseURL.
func PairClient(t testing.TB, clients *auth.ClientStore, baseURL string) *Client {
	t.Helper()
	id, err := store.NewUUIDv4(nil)
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, auth.KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	if err := clients.Put(auth.Client{ID: id, Name: "Test iPhone", Key: key, PairedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	return &Client{BaseURL: baseURL, ClientID: id, Key: key}
}

// Request builds a signed request. target is the path and query as sent
// (e.g. "/api/v1/systems?x=1"), already in canonical encoding.
func (c *Client) Request(t testing.TB, method, target string, body []byte) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, c.BaseURL+target, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Sign(req, body); err != nil {
		t.Fatal(err)
	}
	return req
}

// Sign adds the X-RL-* headers to req, whose body is body.
func (c *Client) Sign(req *http.Request, body []byte) error {
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	nonce := make([]byte, auth.RequestNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	ts := strconv.FormatInt(now().Unix(), 10)
	content := auth.ContentSHA256(body)
	path := req.URL.EscapedPath()
	canonical, err := auth.CanonicalRequest(auth.RequestParts{
		Method: req.Method, Path: path, RawQuery: req.URL.RawQuery,
		Time: ts, Nonce: auth.EncodeB64U(nonce), ContentSHA256: content,
	})
	if err != nil {
		return err
	}
	req.Header.Set(auth.HeaderClient, c.ClientID)
	req.Header.Set(auth.HeaderTime, ts)
	req.Header.Set(auth.HeaderNonce, auth.EncodeB64U(nonce))
	req.Header.Set(auth.HeaderContentSHA256, content)
	req.Header.Set(auth.HeaderSignature, auth.Sign(c.Key, canonical))
	return nil
}

// Response is a response read in full.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Do sends a signed request and reads the response.
func (c *Client) Do(t testing.TB, method, target string, body []byte) Response {
	t.Helper()
	return c.Send(t, c.Request(t, method, target, body))
}

// Send sends any request with the client's HTTP client and reads the
// response.
func (c *Client) Send(t testing.TB, req *http.Request) Response {
	t.Helper()
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return Response{Status: resp.StatusCode, Header: resp.Header, Body: b}
}

// JSON decodes the body into v.
func (r Response) JSON(t testing.TB, v any) {
	t.Helper()
	if err := json.Unmarshal(r.Body, v); err != nil {
		t.Fatalf("decode %q: %v", r.Body, err)
	}
}

// ErrorCode returns error.code of an error body ("" if there is none).
func (r Response) ErrorCode() string {
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return ""
	}
	_ = json.Unmarshal(r.Body, &e)
	return e.Error.Code
}
