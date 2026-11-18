package transfer

import (
	"errors"
	"io/fs"
	"mime"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxFileNameBytes is the longest file name (exFAT allows 255 characters;
// the spec counts bytes, docs/specs/03-protocole.md §5).
const MaxFileNameBytes = 255

// ErrInvalidName is returned by CleanFileName for a refused name.
var ErrInvalidName = errors.New("invalid file name")

// exFATForbidden are the characters exFAT refuses in a name, replaced by
// "_" (the separators "/" and "\" are refused before).
const exFATForbidden = `"*:<>?|`

// CleanFileName checks and cleans a file name given by the app or deduced
// from a download (docs/specs/03-protocole.md §5): refused when empty,
// "." or "..", holding "/", "\" or a control character, not valid UTF-8,
// or longer than MaxFileNameBytes; the characters exFAT forbids are
// replaced by "_". Trailing spaces and dots are dropped too: the Linux
// exFAT driver strips them, which would make the name on the card differ
// from the one checked for conflicts.
//
// Also refused, beyond the spec: names starting with "." (hidden, the
// library would not list them), Unicode format characters (category Cf:
// U+202E which reverses the displayed text, U+200B zero width space,
// U+00AD soft hyphen, U+FEFF...; kept: the joiners U+200C and U+200D for
// emoji sequences and the scripts that need them, and the tags
// U+E0020-U+E007F of the subdivision flags such as 🏴󠁧󠁢󠁳󠁣󠁴󠁿), and
// the names Windows reserves (CON, PRN, AUX, NUL, COM1-9, LPT1-9, with or
// without extension), so the card stays readable on a computer.
func CleanFileName(name string) (string, error) {
	if !utf8.ValidString(name) {
		return "", ErrInvalidName
	}
	if strings.ContainsAny(name, `/\`) {
		return "", ErrInvalidName
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return "", ErrInvalidName
		}
		if unicode.Is(unicode.Cf, r) && r != 0x200c && r != 0x200d && (r < 0xe0020 || r > 0xe007f) {
			return "", ErrInvalidName
		}
	}
	var b strings.Builder
	for _, r := range name {
		if strings.ContainsRune(exFATForbidden, r) {
			r = '_'
		}
		b.WriteRune(r)
	}
	clean := strings.TrimRight(b.String(), " .")
	if clean == "" || strings.HasPrefix(clean, ".") || len(clean) > MaxFileNameBytes || windowsReserved(clean) {
		return "", ErrInvalidName
	}
	return clean, nil
}

// windowsReserved reports whether the part of name before its first dot
// is a device name reserved by Windows.
func windowsReserved(name string) bool {
	base, _, _ := strings.Cut(name, ".")
	base = strings.ToUpper(strings.TrimRight(base, " "))
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) {
		return base[3] >= '1' && base[3] <= '9'
	}
	return false
}

// truncateName shortens name to MaxFileNameBytes, keeping its extension
// and whole UTF-8 characters.
func truncateName(name string) string {
	if len(name) <= MaxFileNameBytes {
		return name
	}
	ext := path.Ext(name)
	if len(ext) > 16 {
		ext = ""
	}
	base := name[:len(name)-len(ext)]
	keep := MaxFileNameBytes - len(ext)
	for keep > 0 && !utf8.RuneStart(base[keep]) {
		keep--
	}
	return strings.TrimRight(base[:keep], " .") + ext
}

// DefaultDownloadName is used when a download gives no usable name.
const DefaultDownloadName = "download"

// deduceName returns the name of a downloaded file: from the
// Content-Disposition header, else from the last segment of the URL, else
// DefaultDownloadName. The result is cleaned (CleanFileName) and shortened.
func deduceName(contentDisposition string, u *url.URL) string {
	var candidates []string
	if contentDisposition != "" {
		if _, params, err := mime.ParseMediaType(contentDisposition); err == nil {
			if n := params["filename"]; n != "" {
				// Keep the last segment of a path ("C:\dir\x.zip").
				if i := strings.LastIndexAny(n, `/\`); i >= 0 {
					n = n[i+1:]
				}
				candidates = append(candidates, n)
			}
		}
	}
	if u != nil {
		if seg := path.Base(u.Path); seg != "." && seg != "/" {
			candidates = append(candidates, seg)
		}
	}
	for _, c := range candidates {
		if n, err := CleanFileName(truncateName(c)); err == nil {
			return n
		}
	}
	return DefaultDownloadName
}

// findFold returns the name of the entry of dir equal to name ignoring
// case (exFAT is case-insensitive), preferring an exact match, or "" when
// there is none. A folder that cannot be read counts as holding name.
func findFold(dir, name string) string {
	des, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return ""
	}
	if err != nil {
		return name
	}
	match := ""
	for _, d := range des {
		if d.Name() == name {
			return name
		}
		if match == "" && strings.EqualFold(d.Name(), name) {
			match = d.Name()
		}
	}
	return match
}

// MaxRenameAttempts bounds the " (N)" suffixes tried by onConflict=rename.
const MaxRenameAttempts = 999

// freeName returns name, or "<base> (N)<ext>" with the smallest N >= 2
// that is free in dir (case-insensitive), or "" if none is.
func freeName(dir, name string) string {
	if findFold(dir, name) == "" {
		return name
	}
	ext := path.Ext(name)
	base := strings.TrimSuffix(name, ext)
	if base == "" { // ".hidden": no extension
		base, ext = name, ""
	}
	for n := 2; n <= MaxRenameAttempts; n++ {
		suffix := " (" + strconv.Itoa(n) + ")"
		b := base
		for len(b)+len(suffix)+len(ext) > MaxFileNameBytes && b != "" {
			_, size := utf8.DecodeLastRuneInString(b)
			b = b[:len(b)-size]
		}
		candidate := b + suffix + ext
		if findFold(dir, candidate) == "" {
			return candidate
		}
	}
	return ""
}
