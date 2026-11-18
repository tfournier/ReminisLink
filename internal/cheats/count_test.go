package cheats

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCount(t *testing.T) {
	for data, want := range map[string]int{
		"":           0,
		"cheats = 0": 0,
		"cheats = 2\n\ncheat0_desc = \"A\"\ncheat0_code = \"1\"\ncheat0_enable = false\n\ncheat1_desc = \"B\"\ncheat1_handler = \"1\"\n": 2,
		"cheat3_desc = \"x\"\r\ncheat7_code = \"y\"\r\ncheat3_code = \"z\"\r\n":                                                          2,
		"# comment\ncheats = 5\n":  0,
		"  cheat0_desc=\"spaces\"": 1,
	} {
		if got := Count([]byte(data)); got != want {
			t.Errorf("Count(%q) = %d, want %d", data, got, want)
		}
	}
}

func TestCountFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "g.cht")
	if CountFile(p) != 0 {
		t.Error("missing file")
	}
	if err := os.WriteFile(p, []byte("cheat0_desc = \"a\"\ncheat1_desc = \"b\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := CountFile(p); got != 2 {
		t.Errorf("CountFile = %d", got)
	}
}

func TestDeclaredCount(t *testing.T) {
	for head, want := range map[string]int{
		"cheats = 12\n\ncheat0_desc = \"a\"\n": 12,
		"# made by me\r\ncheats = \"3\"\r\n":   3,
		"cheats=0\n":                           0,
	} {
		if got, ok := DeclaredCount([]byte(head)); !ok || got != want {
			t.Errorf("DeclaredCount(%q) = %d, %v; want %d", head, got, ok, want)
		}
	}
	for _, head := range []string{"", "cheat0_desc = \"a\"\ncheats = 9\n", "cheats = many\n", "cheats = 999999999\n"} {
		if _, ok := DeclaredCount([]byte(head)); ok {
			t.Errorf("DeclaredCount(%q) accepted", head)
		}
	}
}

func TestCountFileUsesTheHeader(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "declared.cht")
	// The header wins: only the start of the file is read.
	if err := os.WriteFile(p, []byte("cheats = 7\n\ncheat0_desc = \"a\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := CountFile(p); got != 7 {
		t.Errorf("declared: %d", got)
	}
	// No header within the first HeaderBytes: the whole file is parsed.
	var b strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, "cheat%d_desc = \"%s\"\n", i, strings.Repeat("d", 40))
	}
	p = filepath.Join(dir, "plain.cht")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := CountFile(p); got != 200 {
		t.Errorf("parsed: %d", got)
	}
}
