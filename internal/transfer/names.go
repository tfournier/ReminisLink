package transfer

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// MaxFileNameBytes is the longest file name (exFAT allows 255 characters;
// the spec counts bytes, docs/specs/03-protocole.md §5).
const MaxFileNameBytes = 255

// ErrInvalidName is returned by CleanFileName for a refused name.
var ErrInvalidName = errors.New("invalid file name")

// exFATForbidden are the characters exFAT refuses in a name, replaced by
// "_" (the separators "/" and "\" are refused before).
const exFATForbidden = `"*:<>?|`

// CleanFileName checks and cleans a file name given by the app
// (docs/specs/03-protocole.md §5). The name is first
// normalized to Unicode NFC (M8: the iPhone often gives decomposed NFD
// names, which NextUI and the pak screen draw badly). Then it is refused
// when empty,
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
// U+E0020-U+E007F inside a subdivision flag such as 🏴󠁧󠁢󠁳󠁣󠁴󠁿 only), and
// the names Windows reserves (CON, PRN, AUX, NUL, COM1-9, LPT1-9, with or
// without extension), so the card stays readable on a computer.
func CleanFileName(name string) (string, error) {
	if !utf8.ValidString(name) {
		return "", ErrInvalidName
	}
	name = norm.NFC.String(name)
	if strings.ContainsAny(name, `/\`) {
		return "", ErrInvalidName
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return "", ErrInvalidName
		}
		if unicode.Is(unicode.Cf, r) && r != 0x200c && r != 0x200d && !isTag(r) {
			return "", ErrInvalidName
		}
	}
	if !validTagSequences(name) {
		return "", ErrInvalidName
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

// Tag characters (U+E0020-U+E007F) only exist inside a subdivision flag:
// U+1F3F4 (black flag), tags U+E0020-U+E007E, then U+E007F (cancel tag).
const (
	blackFlag = 0x1f3f4
	cancelTag = 0xe007f
)

func isTag(r rune) bool { return r >= 0xe0020 && r <= cancelTag }

// validTagSequences accepts tag characters only in a complete flag
// sequence: after U+1F3F4 and ended by U+E007F.
func validTagSequences(name string) bool {
	inFlag, prev := false, rune(0)
	for _, r := range name {
		switch {
		case isTag(r) && r != cancelTag:
			if !inFlag && prev != blackFlag {
				return false
			}
			inFlag = true
		case r == cancelTag:
			if !inFlag {
				return false
			}
			inFlag = false
		case inFlag:
			return false // a flag sequence without its cancel tag
		}
		prev = r
	}
	return !inFlag
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

// findFold returns the name of the entry of dir equal to name ignoring
// case (exFAT is case-insensitive), preferring an exact match, or "" when
// there is none. An entry of another case counts only where the file
// system finds it under name (on ext4, Knulli M20, game.gba and Game.gba
// coexist). A folder that cannot be read counts as holding name.
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
	if match != "" {
		fa, errA := os.Lstat(filepath.Join(dir, match))
		fb, errB := os.Lstat(filepath.Join(dir, name))
		if errA != nil || errB != nil || !os.SameFile(fa, fb) {
			return ""
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
