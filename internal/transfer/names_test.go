package transfer

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCleanFileName(t *testing.T) {
	ok := map[string]string{
		"Sky Garden Quest DX.gba": "Sky Garden Quest DX.gba",
		"Café Rêverie 🌱.gba":      "Café Rêverie 🌱.gba",
		`a"b*c:d<e>f?g|h.gba`:     "a_b_c_d_e_f_g_h.gba",
		"name. . ":                "name",
		"COM0.gba":                "COM0.gba",
		"CONSOLE.gba":             "CONSOLE.gba",
		"Family 👨\u200d👩.gba":     "Family 👨\u200d👩.gba",
		strings.Repeat("é", 127):  strings.Repeat("é", 127), // 254 bytes
	}
	for in, want := range ok {
		got, err := CleanFileName(in)
		if err != nil || got != want {
			t.Errorf("CleanFileName(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", ".", "..", "...", " ", "a/b", `a\b`, "../x", "a\x00b", "a\nb", "a\x7fb", "a\u0085b",
		"\xff.gba", strings.Repeat("a", 256), strings.Repeat("é", 128),
		"..hidden", ".hidden.gba", "abc\u202egba.exe", "a\u200bb.gba", "\ufeffx.gba",
		"CON", "con.gba", "Com1.txt", "LPT9", "nul.tar.gz", "aux .gba"} {
		if got, err := CleanFileName(in); err == nil {
			t.Errorf("CleanFileName(%q) = %q, want an error", in, got)
		}
	}
}

func TestTruncateName(t *testing.T) {
	long := strings.Repeat("é", 200) + ".gba"
	got := truncateName(long)
	if len(got) > MaxFileNameBytes || !strings.HasSuffix(got, ".gba") || !strings.HasPrefix(got, "éé") {
		t.Fatalf("truncateName = %q (%d bytes)", got, len(got))
	}
	if truncateName("short.gba") != "short.gba" {
		t.Fatal("short name changed")
	}
}

func TestDeduceName(t *testing.T) {
	u, _ := url.Parse("https://example.org/files/My%20Hack%3F.bps?token=1")
	cases := []struct {
		cd   string
		u    *url.URL
		want string
	}{
		{`attachment; filename="patch.ips"`, u, "patch.ips"},
		{`attachment; filename*=UTF-8''Caf%C3%A9.bps`, u, "Café.bps"},
		{`attachment; filename="C:\dir\x.zip"`, u, "x.zip"},
		{`attachment; filename=".."`, u, "My Hack_.bps"},
		{"", u, "My Hack_.bps"},
		{"garbage;;", u, "My Hack_.bps"},
		{"", &url.URL{Scheme: "https", Host: "example.org", Path: "/"}, DefaultDownloadName},
		{"", nil, DefaultDownloadName},
	}
	for _, c := range cases {
		if got := deduceName(c.cd, c.u); got != c.want {
			t.Errorf("deduceName(%q) = %q, want %q", c.cd, got, c.want)
		}
	}
}

func TestFreeNameAndFindFold(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"Game.gba", "game (2).GBA", ".hidden"} {
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := findFold(dir, "GAME.gba"); got != "Game.gba" {
		t.Errorf("findFold = %q", got)
	}
	if got := findFold(dir, "Other.gba"); got != "" {
		t.Errorf("findFold missing = %q", got)
	}
	if got := findFold(filepath.Join(dir, "nope"), "x"); got != "" {
		t.Errorf("findFold in missing dir = %q", got)
	}
	if got := freeName(dir, "Game.gba"); got != "Game (3).gba" {
		t.Errorf("freeName = %q", got)
	}
	if got := freeName(dir, "New.gba"); got != "New.gba" {
		t.Errorf("freeName free = %q", got)
	}
	if got := freeName(dir, ".hidden"); got != ".hidden (2)" {
		t.Errorf("freeName hidden = %q", got)
	}
	long := strings.Repeat("a", 251) + ".gba"
	if err := os.WriteFile(filepath.Join(dir, long), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := freeName(dir, long); len(got) > MaxFileNameBytes || !strings.HasSuffix(got, " (2).gba") {
		t.Errorf("freeName long = %q", got)
	}
}
