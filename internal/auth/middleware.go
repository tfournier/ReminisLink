package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Error codes of the authentication layer (docs/specs/03-protocole.md §6).
const (
	CodeUnknownClient   = "unknown_client"
	CodeBadSignature    = "bad_signature"
	CodeClockSkew       = "clock_skew"
	CodeReplayedRequest = "replayed_request"
	CodeTooLarge        = "too_large"
	CodeTooManyAttempts = "too_many_attempts"
	CodeInternal        = "internal"
)

// Signed body limits. The verifier reads a signed body in memory to hash
// it, and only once the signature of the headers is valid.
const (
	// MaxJSONBody is the default limit (docs/specs/03-protocole.md §1).
	MaxJSONBody = 1 << 20
	// MaxSaveBody is the limit of PUT /api/v1/games/{gameId}/save.
	MaxSaveBody = 64 << 20
)

// DefaultBodyLimit returns the signed body limit of a route: MaxSaveBody
// for PUT /api/v1/games/{gameId}/save, MaxJSONBody otherwise.
func DefaultBodyLimit(method, path string) int64 {
	if strings.ToUpper(method) == http.MethodPut {
		if rest, ok := strings.CutPrefix(path, "/api/v1/games/"); ok {
			if id, ok := strings.CutSuffix(rest, "/save"); ok && id != "" && !strings.Contains(id, "/") {
				return MaxSaveBody
			}
		}
	}
	return MaxJSONBody
}

// ClientLookup finds a paired client. *ClientStore implements it.
type ClientLookup interface {
	Lookup(clientID string) (Client, bool)
}

// Verifier checks signed requests (docs/specs/03-protocole.md §4.3).
type Verifier struct {
	Clients   ClientLookup
	Nonces    *NonceCache
	Now       func() time.Time                // nil: time.Now
	BodyLimit func(method, path string) int64 // nil: DefaultBodyLimit
	Logger    *slog.Logger                    // nil: slog.Default()
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

// Middleware verifies the signature of every request it wraps, then calls
// next with the client in the request context. The unsigned routes
// (GET /hello, POST /pair) must not be wrapped.
//
// Checks:
//  1. unknown client (missing X-RL-Client included) → 401 unknown_client;
//     then any missing or malformed signature header → 401 bad_signature;
//  2. clock skew > 300 s → 401 clock_skew;
//  3. nonce already seen → 401 replayed_request;
//  4. signature mismatch (constant time) → 401 bad_signature;
//  5. UNSIGNED outside PUT /api/v1/uploads/{id}, or body hash mismatch →
//     401 bad_signature; body above the route limit → 413 too_large.
//
// Steps 4 and 5 are swapped compared with the spec: both answer
// bad_signature, and checking the signature (which covers the announced
// body hash) first means the body of a forged request is never read.
//
// The nonce is only recorded once the whole request is valid, so forged
// requests cannot fill the cache; recording is atomic, so two identical
// concurrent requests cannot both pass. A full cache answers 429
// too_many_attempts (fail closed).
func (v *Verifier) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		client, ok := v.verify(w, r)
		if !ok {
			return
		}
		next.ServeHTTP(w, r.WithContext(WithClient(r.Context(), client)))
	})
}

func (v *Verifier) verify(w http.ResponseWriter, r *http.Request) (Client, bool) {
	log := v.Logger
	if log == nil {
		log = slog.Default()
	}
	now := time.Now
	if v.Now != nil {
		now = v.Now
	}
	fail := func(status int, code, message string) (Client, bool) {
		// Never log keys, nonces or signatures.
		log.Info("request rejected", "code", code, "method", r.Method, "path", r.URL.Path,
			"client", r.Header.Get(HeaderClient), "remote", r.RemoteAddr)
		WriteError(w, status, code, message, nil)
		return Client{}, false
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
	if !withinClockSkew(time.Unix(ts, 0), now()) {
		return fail(http.StatusUnauthorized, CodeClockSkew, "Request time is too far from the device time")
	}

	if v.Nonces.Seen(client.ID, parts.Nonce) {
		return fail(http.StatusUnauthorized, CodeReplayedRequest, "Nonce already used")
	}

	if !Verify(client.Key, canonical, r.Header.Get(HeaderSignature)) {
		return fail(http.StatusUnauthorized, CodeBadSignature, "Bad signature")
	}

	if parts.ContentSHA256 == UnsignedPayload {
		if !AllowsUnsignedBody(parts.Method, path) {
			return fail(http.StatusUnauthorized, CodeBadSignature, "UNSIGNED body not allowed on this route")
		}
	} else {
		bodyLimit := DefaultBodyLimit
		if v.BodyLimit != nil {
			bodyLimit = v.BodyLimit
		}
		body, err := readBody(r, bodyLimit(parts.Method, path))
		if errors.Is(err, errBodyTooLarge) {
			return fail(http.StatusRequestEntityTooLarge, CodeTooLarge, "Body too large")
		}
		if err != nil {
			return fail(http.StatusBadRequest, "invalid_request", "Cannot read body")
		}
		if ContentSHA256(body) != parts.ContentSHA256 {
			return fail(http.StatusUnauthorized, CodeBadSignature, "Body hash mismatch")
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
	}

	switch err := v.Nonces.Add(client.ID, parts.Nonce); {
	case errors.Is(err, ErrReplay):
		return fail(http.StatusUnauthorized, CodeReplayedRequest, "Nonce already used")
	case errors.Is(err, ErrNonceCacheFull):
		// The spec defines no code for this case yet (reported in the M1 notes).
		return fail(http.StatusTooManyRequests, CodeTooManyAttempts, "Too many requests")
	case err != nil:
		return fail(http.StatusInternalServerError, CodeInternal, "Internal error")
	}
	return client, true
}

// withinClockSkew reports whether |t - now| <= MaxClockSkew. It compares
// instants instead of subtracting them: time.Time.Sub saturates for far
// away times and the absolute value of the minimum Duration stays negative.
func withinClockSkew(t, now time.Time) bool {
	return !t.Before(now.Add(-MaxClockSkew)) && !t.After(now.Add(MaxClockSkew))
}

// AllowsUnsignedBody reports whether X-RL-Content-SHA256: UNSIGNED is
// accepted: only PUT /api/v1/uploads/{uploadId}.
func AllowsUnsignedBody(method, path string) bool {
	if strings.ToUpper(method) != http.MethodPut {
		return false
	}
	const prefix = "/api/v1/uploads/"
	id, ok := strings.CutPrefix(path, prefix)
	return ok && id != "" && !strings.Contains(id, "/")
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

var errBodyTooLarge = errors.New("body too large")

func readBody(r *http.Request, limit int64) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	defer r.Body.Close()
	if r.ContentLength > limit {
		return nil, errBodyTooLarge
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, errBodyTooLarge
	}
	return body, nil
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
