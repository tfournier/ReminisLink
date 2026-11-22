package transfer

import (
	"strings"
	"testing"
)

// TestCleanGameName checks the names a game can be renamed to (M23):
// exFAT-forbidden characters are refused (never replaced), and so is any
// name CleanFileName would change.
func TestCleanGameName(t *testing.T) {
	for _, bad := range []string{
		"", " ", ".", "..", ".hidden", "a/b", `a\b`, "a:b", "a*b", "a?b", `a"b`, "a<b", "a>b", "a|b",
		"trailing ", "trailing.", "CON", "nul", "LPT1", "a\x00b", "a\x1fb", "a‮b", "a​b",
		strings.Repeat("x", 256), "\xff\xfe",
	} {
		if got, err := CleanGameName(bad); err == nil {
			t.Errorf("%q accepted as %q", bad, got)
		}
	}
	for in, want := range map[string]string{
		"Sky Garden Quest (Europe)": "Sky Garden Quest (Europe)",
		"Café Rêverie":            "Café Rêverie", // NFD from the iPhone → NFC
		"Pixel Garden 🌱":            "Pixel Garden 🌱",
		"[b1] Hack, v1.2":           "[b1] Hack, v1.2",
	} {
		if got, err := CleanGameName(in); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
}
