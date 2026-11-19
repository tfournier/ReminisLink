package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Error codes of the authentication layer (docs/specs/03-protocole.md §6).
const (
	CodeInvalidRequest  = "invalid_request"
	CodeUnknownClient   = "unknown_client"
	CodeBadSignature    = "bad_signature"
	CodeClockSkew       = "clock_skew"
	CodeReplayedRequest = "replayed_request"
	CodeTooLarge        = "too_large"
	CodeTooManyAttempts = "too_many_attempts"
	CodeInternal        = "internal"
	CodeShuttingDown    = "shutting_down"
)

// Signed body limits, chosen per route when it is registered. A signed body
// is read in memory to check its hash.
const (
	// BodyLimitJSON is the default (JSON bodies, docs/specs/03-protocole.md §1).
	BodyLimitJSON = 1 << 20
	// BodyLimitSave is for PUT /api/v1/games/{gameId}/save.
	BodyLimitSave = 64 << 20
	// BodyLimitArt is for PUT /api/v1/games/{gameId}/art (a PNG, M8).
	BodyLimitArt = 4 << 20
	// BodyLimitChunk is for PUT /api/v1/uploads/{uploadId} when a chunk is
	// sent with its real SHA-256 instead of UNSIGNED.
	BodyLimitChunk = 16 << 20
	// DefaultBodyBudget bounds the memory of all signed bodies read at the
	// same time; requests beyond it wait for room.
	DefaultBodyBudget = 128 << 20
)

// RouteOptions is what a signed route accepts. It is fixed when the route
// is registered, never deduced from the request path.
type RouteOptions struct {
	// BodyLimit is the largest signed body (0: BodyLimitJSON).
	BodyLimit int64
	// AllowUnsigned accepts X-RL-Content-SHA256: UNSIGNED; the body is then
	// left unread for the handler (upload chunks only).
	AllowUnsigned bool
	// TakesLease marks a route that changes the SD card: the Verifier's
	// Gate takes the write lease (M10) before the body is read, or refuses
	// the request.
	TakesLease bool
	// WhileDisconnected lets a client disconnected by the console through
	// (POST /api/v1/clients/me/connect only, M10).
	WhileDisconnected bool
}

// Rejection is the error answer chosen by a Gate.
type Rejection struct {
	Status  int
	Code    string
	Message string
	Details map[string]any
}

// Gate decides whether an authenticated request may go on. The Verifier
// calls it once the signature is valid and the nonce reserved, before the
// body is read: a rejected request never makes the pak read its body (the
// answer carries "Connection: close" when a body was announced, so the
// server does not wait for it either). A nil Rejection lets the request
// through with an Admission (may be nil).
type Gate func(r *http.Request, client Client, opts RouteOptions) (*Admission, *Rejection)

// Admission is what a Gate holds for a request it let through. Exactly one
// of its functions is called: Commit once the request is accepted (body
// verified, or right away for an UNSIGNED body, before the handler), and
// Rollback if the Verifier rejects the request after the Gate. A Rejection
// from Commit rejects the request (Rollback is then called). Nil functions
// are skipped.
type Admission struct {
	Commit   func() *Rejection
	Rollback func()
}

// Route options of the API v1 routes.
var (
	// RouteJSON: every signed route without a larger body.
	RouteJSON = RouteOptions{BodyLimit: BodyLimitJSON}
	// RouteSave: PUT /api/v1/games/{gameId}/save.
	RouteSave = RouteOptions{BodyLimit: BodyLimitSave}
	// RouteArt: PUT /api/v1/games/{gameId}/art.
	RouteArt = RouteOptions{BodyLimit: BodyLimitArt}
	// RouteChunk: PUT /api/v1/uploads/{uploadId}.
	RouteChunk = RouteOptions{BodyLimit: BodyLimitChunk, AllowUnsigned: true}
)

// ClientLookup finds a paired client. *ClientStore implements it.
type ClientLookup interface {
	Lookup(clientID string) (Client, bool)
}

// Verifier checks signed requests (docs/specs/03-protocole.md §4.3).
type Verifier struct {
	Clients    ClientLookup
	Nonces     *NonceCache      // must use the same clock as Now
	Now        func() time.Time // nil: time.Now
	BodyBudget int64            // 0: DefaultBodyBudget
	Logger     *slog.Logger     // nil: slog.Default()
	// Gate, when set, is the last check before the body is read (the
	// disconnection and the write lease of M10). Set it before serving.
	Gate Gate

	budgetOnce sync.Once
	budget     *byteBudget
}

// NewVerifier returns a Verifier and its nonce cache, both on the same clock
// (a nil now uses time.Now). Prefer it to filling the struct by hand: the
// nonce expiry and the clock skew check must compare the same instants.
// nonceCapacity <= 0 uses DefaultNonceCapacity.
func NewVerifier(clients ClientLookup, now func() time.Time, nonceCapacity int, logger *slog.Logger) *Verifier {
	if now == nil {
		now = time.Now
	}
	return &Verifier{
		Clients: clients,
		Nonces:  NewNonceCache(NonceTTL, nonceCapacity, now),
		Now:     now,
		Logger:  logger,
	}
}

type clientContextKey struct{}

// ClientFromContext returns the client authenticated by the middleware.
func ClientFromContext(ctx context.Context) (Client, bool) {
	c, ok := ctx.Value(clientContextKey{}).(Client)
	return c, ok
}

// WithClient returns a context carrying c (for tests and handlers).
func WithClient(ctx context.Context, c Client) context.Context {
	return context.WithValue(ctx, clientContextKey{}, c)
}

// Middleware is Require(RouteJSON, next).
func (v *Verifier) Middleware(next http.Handler) http.Handler {
	return v.Require(RouteJSON, next)
}

// Require verifies the signature of every request of a route registered
// with opts, then calls next with the client in the request context. The
// unsigned routes (GET /hello, POST /pair) must not be wrapped.
//
// Checks:
//  1. unknown client (missing X-RL-Client included) → 401 unknown_client;
//     then any missing or malformed signature header → 401 bad_signature;
//  2. clock skew > 300 s → 401 clock_skew;
//  3. nonce already seen → 401 replayed_request;
//  4. signature mismatch (constant time) → 401 bad_signature;
//     UNSIGNED on a route without AllowUnsigned → 401 bad_signature;
//  5. the nonce is reserved atomically: a concurrent copy of the request
//     gets 401 replayed_request, a full cache 429 too_many_attempts;
//  6. a query key present more than once, or a raw ";" in the query →
//     400 invalid_request (the nonce is consumed, docs/specs/03-protocole.md
//     §4.2; net/url ignores the pairs holding a ";");
//  7. the Gate, when set, may refuse the request (its body still unread);
//  8. body larger than the route limit → 413 too_large; body hash mismatch
//     → 401 bad_signature;
//  9. the Gate's Admission is committed, and may still refuse the request.
//
// Signature and body hash are checked in the reverse order of the spec
// (both answer bad_signature): the body of a forged request is never read.
// The nonce is reserved before the body is read, so replaying the headers
// of a request in progress never makes the pak read another body; a
// failure after step 5 consumes the nonce (the app retries with a new one).
// Bodies are read within a global memory budget (Verifier.BodyBudget):
// when it is used up, requests with a body wait for room; requests without
// a body never wait. Require panics if the route limit exceeds the budget
// (programming error), and fixes the budget size on first use.
//
// Note for the server (M2): a waiting request stops waiting only when its
// context is cancelled, and with HTTP/1.1 net/http does not cancel it when
// the client disconnects while its body is unread. The server must set
// ReadHeaderTimeout, an idle read deadline on connections, and a
// BaseContext cancelled at shutdown: that shutdown cancellation is what
// makes the 503 shutting_down answer to a cancelled wait accurate. Do not
// map a budget wait timeout onto that code.
func (v *Verifier) Require(opts RouteOptions, next http.Handler) http.Handler {
	if opts.BodyLimit <= 0 {
		opts.BodyLimit = BodyLimitJSON
	}
	if budget := v.bodyBudget().size; opts.BodyLimit > budget {
		panic(fmt.Sprintf("auth: route body limit %d exceeds the body budget %d", opts.BodyLimit, budget))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		client, reserved, ok := v.verify(w, r, opts)
		if reserved > 0 {
			defer v.bodyBudget().release(reserved)
		}
		if !ok {
			return
		}
		next.ServeHTTP(w, r.WithContext(WithClient(r.Context(), client)))
	})
}

func (v *Verifier) bodyBudget() *byteBudget {
	v.budgetOnce.Do(func() {
		size := v.BodyBudget
		if size <= 0 {
			size = DefaultBodyBudget
		}
		v.budget = newByteBudget(size)
	})
	return v.budget
}

// verify returns the client, the number of budget bytes held for the body
// (to release once the handler is done) and whether the request is valid.
func (v *Verifier) verify(w http.ResponseWriter, r *http.Request, opts RouteOptions) (Client, int64, bool) {
	log := v.Logger
	if log == nil {
		log = slog.Default()
	}
	now := time.Now
	if v.Now != nil {
		now = v.Now
	}
	var reserved int64
	// admission is the Gate's hold on the request: rolled back on any
	// rejection after the Gate, before the answer is written.
	var admission *Admission
	committed := false
	failWith := func(status int, code, message string, details map[string]any) (Client, int64, bool) {
		if admission != nil && !committed {
			committed = true
			if admission.Rollback != nil {
				admission.Rollback()
			}
		}
		// Never log keys, nonces or signatures.
		// Every value from the network is bounded (see LogString).
		log.Info("request rejected", "code", code, "method", LogString(r.Method, MaxLoggedMethod),
			"path", LogString(r.URL.Path, MaxLoggedPath), "client", LogClient(r.Header.Get(HeaderClient)),
			"remote", r.RemoteAddr)
		WriteError(w, status, code, message, details)
		return Client{}, reserved, false
	}
	fail := func(status int, code, message string) (Client, int64, bool) {
		return failWith(status, code, message, nil)
	}

	client, ok := v.Clients.Lookup(r.Header.Get(HeaderClient))
	if !ok {
		return fail(http.StatusUnauthorized, CodeUnknownClient, "Unknown client")
	}

	path, rawQuery := requestTarget(r)
	parts := RequestParts{
		Method:        r.Method,
		Path:          path,
		RawQuery:      rawQuery,
		Time:          r.Header.Get(HeaderTime),
		Nonce:         r.Header.Get(HeaderNonce),
		ContentSHA256: r.Header.Get(HeaderContentSHA256),
	}
	canonical, err := CanonicalRequest(parts)
	if err != nil {
		return fail(http.StatusUnauthorized, CodeBadSignature, "Malformed signed request")
	}

	ts, _ := strconv.ParseInt(parts.Time, 10, 64) // validated by CanonicalRequest
	signedAt := time.Unix(ts, 0)
	if !withinClockSkew(signedAt, now().Round(0)) {
		return fail(http.StatusUnauthorized, CodeClockSkew, "Request time is too far from the device time")
	}

	if v.Nonces.Seen(client.ID, parts.Nonce) {
		return fail(http.StatusUnauthorized, CodeReplayedRequest, "Nonce already used")
	}

	if !Verify(client.Key, canonical, r.Header.Get(HeaderSignature)) {
		return fail(http.StatusUnauthorized, CodeBadSignature, "Bad signature")
	}
	unsigned := parts.ContentSHA256 == UnsignedPayload
	if unsigned && !opts.AllowUnsigned {
		return fail(http.StatusUnauthorized, CodeBadSignature, "UNSIGNED body not allowed on this route")
	}

	switch err := v.Nonces.Add(client.ID, parts.Nonce, signedAt); {
	case errors.Is(err, ErrReplay):
		return fail(http.StatusUnauthorized, CodeReplayedRequest, "Nonce already used")
	case errors.Is(err, ErrNonceCacheFull):
		// The spec defines no code for this case yet (reported in the M1 notes).
		secs := int64(math.Ceil(v.Nonces.RetryAfter().Seconds()))
		w.Header().Set("Retry-After", strconv.FormatInt(max(secs, 1), 10))
		return fail(http.StatusTooManyRequests, CodeTooManyAttempts, "Too many requests")
	case err != nil:
		return fail(http.StatusInternalServerError, CodeInternal, "Internal error")
	}

	if dup, err := HasDuplicateQueryKey(rawQuery); err != nil || dup {
		// err cannot happen here (CanonicalRequest parsed the same query).
		return fail(http.StatusBadRequest, CodeInvalidRequest, "Duplicate query parameter")
	}
	if strings.Contains(rawQuery, ";") {
		// net/url drops the pairs holding a raw ";": the handler would not
		// see what was signed.
		return fail(http.StatusBadRequest, CodeInvalidRequest, "Semicolon in query")
	}
	if v.Gate != nil {
		adm, rej := v.Gate(r, client, opts)
		if rej != nil {
			if r.ContentLength != 0 {
				// Answer at once: without this net/http first drains up
				// to 256 KiB of the unread body to keep the connection.
				w.Header().Set("Connection", "close")
			}
			return failWith(rej.Status, rej.Code, rej.Message, rej.Details)
		}
		admission = adm
	}
	// accept commits the admission before the handler runs.
	accept := func(unread bool) bool {
		if admission == nil || admission.Commit == nil {
			committed = true
			return true
		}
		if rej := admission.Commit(); rej != nil {
			// failWith rolls the admission back.
			if unread && r.ContentLength != 0 {
				w.Header().Set("Connection", "close")
			}
			failWith(rej.Status, rej.Code, rej.Message, rej.Details)
			return false
		}
		committed = true
		return true
	}

	if unsigned {
		if !accept(true) {
			return Client{}, 0, false
		}
		return client, 0, true
	}
	if r.ContentLength > opts.BodyLimit {
		return fail(http.StatusRequestEntityTooLarge, CodeTooLarge, "Body too large")
	}
	want := opts.BodyLimit
	if r.ContentLength >= 0 {
		want = r.ContentLength
	}
	if want > 0 {
		// A request without a body never queues behind large uploads.
		reserved, err = v.bodyBudget().acquire(r.Context(), want)
		if err != nil {
			// Accurate once the server cancels BaseContext at shutdown.
			return fail(http.StatusServiceUnavailable, CodeShuttingDown, "Request cancelled")
		}
	}
	body, err := readBody(r, opts.BodyLimit)
	if errors.Is(err, errBodyTooLarge) {
		return fail(http.StatusRequestEntityTooLarge, CodeTooLarge, "Body too large")
	}
	if err != nil {
		return fail(http.StatusBadRequest, CodeInvalidRequest, "Cannot read body")
	}
	if ContentSHA256(body) != parts.ContentSHA256 {
		return fail(http.StatusUnauthorized, CodeBadSignature, "Body hash mismatch")
	}
	if !accept(false) {
		return Client{}, reserved, false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	return client, reserved, true
}

// withinClockSkew reports whether |t - now| <= MaxClockSkew. It compares
// instants instead of subtracting them: time.Time.Sub saturates for far
// away times and the absolute value of the minimum Duration stays negative.
func withinClockSkew(t, now time.Time) bool {
	return !t.Before(now.Add(-MaxClockSkew)) && !t.After(now.Add(MaxClockSkew))
}

var errBodyTooLarge = errors.New("body too large")

func readBody(r *http.Request, limit int64) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, errBodyTooLarge
	}
	return body, nil
}

// requestTarget returns the path (still encoded) and the raw query exactly
// as sent on the request line.
func requestTarget(r *http.Request) (path, rawQuery string) {
	target := r.RequestURI
	if strings.HasPrefix(target, "/") {
		path, rawQuery, _ = strings.Cut(target, "?")
		return path, rawQuery
	}
	// Absolute form or a request built without a request line.
	return r.URL.EscapedPath(), r.URL.RawQuery
}

// ErrorBody is the "error" object of every error response.
type ErrorBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
}

// WriteError writes { "error": { "code", "message", "details" } }.
func WriteError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	if details == nil {
		details = map[string]any{}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error ErrorBody `json:"error"`
	}{ErrorBody{Code: code, Message: message, Details: details}})
}
