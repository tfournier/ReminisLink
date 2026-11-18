package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Request signing (docs/specs/03-protocole.md §4).
const (
	// SignatureAlgorithm is the first line of the canonical string.
	SignatureAlgorithm = "RL1-HMAC-SHA256"
	// UnsignedPayload replaces the body hash on upload chunks only.
	UnsignedPayload = "UNSIGNED"
	// RequestNonceSize is the length of X-RL-Nonce once decoded.
	RequestNonceSize = 16
	// MaxClockSkew is the largest accepted |X-RL-Time - device time|.
	MaxClockSkew = 300 * time.Second
)

// Signature headers.
const (
	HeaderClient        = "X-RL-Client"
	HeaderTime          = "X-RL-Time"
	HeaderNonce         = "X-RL-Nonce"
	HeaderContentSHA256 = "X-RL-Content-SHA256"
	HeaderSignature     = "X-RL-Signature"
)

// ErrMalformed is returned for inputs that cannot be part of a canonical
// request (bad percent-encoding, control characters, malformed header...).
var ErrMalformed = errors.New("malformed signed request")

// ContentSHA256 returns the lowercase hex SHA-256 of body (an empty body
// gives the SHA-256 of the empty string).
func ContentSHA256(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// CanonicalQuery canonicalizes a raw query string (the part after "?", as
// sent). Each "&"-separated pair is split at its first "=" (a missing "="
// means an empty value), decoded ("+" is a space, %XX is a byte), then
// re-encoded per RFC 3986 where only A-Z a-z 0-9 - . _ ~ stay unencoded and
// %XX uses uppercase hex. Pairs are sorted by encoded key, then by encoded
// value (byte order), and joined with "&". Empty pairs ("a=1&&b=2") are
// skipped. An empty query gives "".
//
// "+" is decoded as a space because that is how Go's url.Values (and so the
// handlers) read it: the signature covers what the server acts on. Clients
// should never send a raw "+": a literal plus is %2B, a space is %20.
func CanonicalQuery(rawQuery string) (string, error) {
	if rawQuery == "" {
		return "", nil
	}
	type pair struct{ k, v string }
	var pairs []pair
	for _, part := range strings.Split(rawQuery, "&") {
		if part == "" {
			continue
		}
		k, v, _ := strings.Cut(part, "=")
		dk, err := queryUnescape(k)
		if err != nil {
			return "", err
		}
		dv, err := queryUnescape(v)
		if err != nil {
			return "", err
		}
		pairs = append(pairs, pair{EncodeRFC3986(dk), EncodeRFC3986(dv)})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].k != pairs[j].k {
			return pairs[i].k < pairs[j].k
		}
		return pairs[i].v < pairs[j].v
	})
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = p.k + "=" + p.v
	}
	return strings.Join(parts, "&"), nil
}

// EncodeRFC3986 percent-encodes every byte of s except A-Z a-z 0-9 - . _ ~.
func EncodeRFC3986(s string) string {
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isUnreserved(c) {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hexDigits[c>>4])
		b.WriteByte(hexDigits[c&0x0f])
	}
	return b.String()
}

func isUnreserved(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
		c == '-' || c == '.' || c == '_' || c == '~'
}

func queryUnescape(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '+':
			b.WriteByte(' ')
		case '%':
			if i+2 >= len(s) || !isHex(s[i+1]) || !isHex(s[i+2]) {
				return "", fmt.Errorf("%w: bad percent-encoding in query", ErrMalformed)
			}
			b.WriteByte(unhex(s[i+1])<<4 | unhex(s[i+2]))
			i += 2
		default:
			b.WriteByte(c)
		}
	}
	return b.String(), nil
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func unhex(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}

// HasDuplicateQueryKey reports whether a raw query names the same key more
// than once. Keys are compared once decoded ("a=1&%61=2" repeats "a"), the
// way the handlers read them. A malformed query is reported as an error
// (ErrMalformed). The pak refuses such requests with 400 invalid_request
// (docs/specs/03-protocole.md §4.2): with a repeated key, the handler and
// the canonical string could disagree on which value counts.
func HasDuplicateQueryKey(rawQuery string) (bool, error) {
	if rawQuery == "" {
		return false, nil
	}
	seen := make(map[string]bool)
	for _, part := range strings.Split(rawQuery, "&") {
		if part == "" {
			continue
		}
		k, _, _ := strings.Cut(part, "=")
		dk, err := queryUnescape(k)
		if err != nil {
			return false, err
		}
		if seen[dk] {
			return true, nil
		}
		seen[dk] = true
	}
	return false, nil
}

// RequestParts are the inputs of the canonical string. Path is the path as
// sent on the request line (still percent-encoded, without the query) and
// RawQuery the query as sent (without "?").
type RequestParts struct {
	Method        string
	Path          string
	RawQuery      string
	Time          string
	Nonce         string
	ContentSHA256 string
}

// CanonicalRequest builds the string to sign:
//
//	RL1-HMAC-SHA256\n<METHOD>\n<path>\n<canonical query>\n<time>\n<nonce>\n<content sha256>
//
// with no trailing newline. The method is uppercased. Fields that could break
// the line structure are rejected.
func CanonicalRequest(p RequestParts) (string, error) {
	// ASCII letters only, checked before uppercasing (strings.ToUpper would
	// turn some non-ASCII letters into ASCII ones, e.g. "ı" into "I").
	if p.Method == "" || strings.Trim(p.Method, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz") != "" {
		return "", fmt.Errorf("%w: method", ErrMalformed)
	}
	method := strings.ToUpper(p.Method)
	if !strings.HasPrefix(p.Path, "/") || hasControlOrSpace(p.Path) || strings.ContainsAny(p.Path, "?#") {
		return "", fmt.Errorf("%w: path", ErrMalformed)
	}
	if hasControlOrSpace(p.RawQuery) || strings.Contains(p.RawQuery, "#") {
		return "", fmt.Errorf("%w: query", ErrMalformed)
	}
	if !validTime(p.Time) {
		return "", fmt.Errorf("%w: %s", ErrMalformed, HeaderTime)
	}
	if !validRequestNonce(p.Nonce) {
		return "", fmt.Errorf("%w: %s", ErrMalformed, HeaderNonce)
	}
	if !validContentSHA256(p.ContentSHA256) {
		return "", fmt.Errorf("%w: %s", ErrMalformed, HeaderContentSHA256)
	}
	query, err := CanonicalQuery(p.RawQuery)
	if err != nil {
		return "", err
	}
	return strings.Join([]string{
		SignatureAlgorithm, method, p.Path, query, p.Time, p.Nonce, p.ContentSHA256,
	}, "\n"), nil
}

// Sign returns b64u(HMAC-SHA256(key, canonical)).
func Sign(key []byte, canonical string) string {
	return EncodeB64U(mac(key, canonical))
}

// Verify checks a b64u signature of canonical in constant time.
func Verify(key []byte, canonical, signature string) bool {
	return verifyMAC(mac(key, canonical), signature)
}

func hasControlOrSpace(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] <= 0x20 || s[i] == 0x7f {
			return true
		}
	}
	return false
}

// validTime accepts a canonical decimal Unix time: digits only, no sign, no
// leading zero, so one instant has one spelling.
func validTime(s string) bool {
	if s == "" || len(s) > 19 || (len(s) > 1 && s[0] == '0') {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	_, err := strconv.ParseInt(s, 10, 64)
	return err == nil
}

func validRequestNonce(s string) bool {
	b, err := DecodeB64U(s)
	return err == nil && len(b) == RequestNonceSize
}

func validContentSHA256(s string) bool {
	if s == UnsignedPayload {
		return true
	}
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
