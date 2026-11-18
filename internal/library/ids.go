// Package library lists systems and games and resolves the opaque
// identifiers the API uses for them.
package library

// Identifiers (docs/specs/03-protocole.md §5):
//
//	systemId = b64u(<system folder name>)
//	gameId   = b64u(<game path relative to Roms/, "/" separator>)
//	folderId = b64u(<folder path relative to Roms/, "/" separator>)
//
// b64u is base64url without padding over the UTF-8 bytes of the path. The
// bytes are taken as they are on disk: no Unicode normalization is applied,
// so an NFC and an NFD spelling of the same name give different identifiers.
//
// Decoding is strict. On top of the checks required by the spec (no ".."
// component, no absolute path, no NUL byte, result inside the root), it also
// rejects what the pak never produces and what could hide a traversal:
// non-canonical base64, invalid UTF-8, control characters, backslashes and
// non-canonical paths ("a//b", "./a", "a/", "a/."). A path that
// filepath.Clean would change is refused instead of being cleaned, so one
// file has exactly one identifier.

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// MaxIDPathBytes is the longest decoded path accepted (Linux PATH_MAX).
const MaxIDPathBytes = 4096

// ErrInvalidID is returned for any identifier that must be answered with
// 400 invalid_id.
var ErrInvalidID = errors.New("invalid id")

var b64u = base64.RawURLEncoding.Strict()

// EncodeID returns the identifier of a path relative to a root ("/" separator).
func EncodeID(rel string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(rel))
}

// DecodeID decodes an identifier into a relative, "/"-separated path and
// checks it is safe to join under a root. It does not touch the file system.
func DecodeID(id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("%w: empty", ErrInvalidID)
	}
	if len(id) > base64.RawURLEncoding.EncodedLen(MaxIDPathBytes) {
		return "", fmt.Errorf("%w: too long", ErrInvalidID)
	}
	raw, err := b64u.DecodeString(id)
	if err != nil || !isB64URLAlphabet(id) {
		return "", fmt.Errorf("%w: not base64url", ErrInvalidID)
	}
	p := string(raw)
	if err := checkRelPath(p); err != nil {
		return "", err
	}
	return p, nil
}

// isB64URLAlphabet reports whether s only uses A-Z a-z 0-9 - _. The
// standard decoder silently skips '\r' and '\n', even in strict mode.
func isB64URLAlphabet(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// DecodeSystemID decodes a systemId: a single folder name, without "/".
func DecodeSystemID(id string) (string, error) {
	p, err := DecodeID(id)
	if err != nil {
		return "", err
	}
	if strings.Contains(p, "/") {
		return "", fmt.Errorf("%w: system id with a path separator", ErrInvalidID)
	}
	return p, nil
}

func checkRelPath(p string) error {
	switch {
	case p == "":
		return fmt.Errorf("%w: empty path", ErrInvalidID)
	case !utf8.ValidString(p):
		return fmt.Errorf("%w: invalid UTF-8", ErrInvalidID)
	case strings.HasPrefix(p, "/"):
		return fmt.Errorf("%w: absolute path", ErrInvalidID)
	case strings.ContainsRune(p, '\\'):
		return fmt.Errorf("%w: backslash", ErrInvalidID)
	}
	for _, r := range p {
		if r == 0 {
			return fmt.Errorf("%w: NUL byte", ErrInvalidID)
		}
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: control character", ErrInvalidID)
		}
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return fmt.Errorf("%w: parent component", ErrInvalidID)
		}
		if part == "." {
			return fmt.Errorf("%w: dot component", ErrInvalidID)
		}
	}
	if path.Clean(p) != p {
		return fmt.Errorf("%w: non-canonical path", ErrInvalidID)
	}
	return nil
}

// ResolveID decodes id and returns the absolute path it names under root.
// The result is checked to stay inside root, lexically and after resolving
// symbolic links of the deepest existing ancestor (the SD card is exFAT and
// has none, but the desktop mode runs on other file systems).
func ResolveID(root, id string) (string, error) {
	rel, err := DecodeID(id)
	if err != nil {
		return "", err
	}
	return resolveUnder(root, rel)
}

func resolveUnder(root, rel string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve root %s: %w", root, err)
	}
	full := filepath.Join(absRoot, filepath.FromSlash(rel))
	if !within(absRoot, full) {
		return "", fmt.Errorf("%w: outside root", ErrInvalidID)
	}
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", fmt.Errorf("resolve root %s: %w", root, err)
	}
	existing := full
	for {
		real, err := filepath.EvalSymlinks(existing)
		if err == nil {
			if !within(realRoot, real) {
				return "", fmt.Errorf("%w: link outside root", ErrInvalidID)
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("resolve %s: %w", existing, err)
		}
		if existing == absRoot {
			break
		}
		existing = filepath.Dir(existing)
	}
	return full, nil
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
