package transfer

import (
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
		"Flag 🏴\U000e0067\U000e0062\U000e0073\U000e0063\U000e0074\U000e007f.gba": "Flag 🏴\U000e0067\U000e0062\U000e0073\U000e0063\U000e0074\U000e007f.gba",
		"CONSOLE.gba":            "CONSOLE.gba",
		"Family 👨\u200d👩.gba":    "Family 👨\u200d👩.gba",
		strings.Repeat("é", 127): strings.Repeat("é", 127), // 254 bytes
	}
	for in, want := range ok {
		got, err := CleanFileName(in)
		if err != nil || got != want {
			t.Errorf("CleanFileName(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", ".", "..", "...", " ", "a/b", `a\b`, "../x", "a\x00b", "a\nb", "a\x7fb", "a\u0085b",
		"\xff.gba", strings.Repeat("a", 256), strings.Repeat("é", 128),
		"..hidden", ".hidden.gba", "abc\u202egba.exe", "a\u200bb.gba", "\ufeffx.gba", "soft\u00adhyphen.gba",
		"tag\U000e0067\U000e007f.gba", "\U0001f3f4\U000e0067\U000e0062.gba", "x\U000e007f.gba", "\U0001f3f4\U000e007f\U000e007f.gba",
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

func TestFreeNameAndFindFold(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"Game.gba", "game (2).GBA", ".hidden"} {
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Another case is the same file on exFAT (the NextUI card) and APFS;
	// on a case-sensitive file system (Knulli on ext4) it is another one.
	folds := foldsCase(dir)
	if got, want := findFold(dir, "GAME.gba"), map[bool]string{true: "Game.gba", false: ""}[folds]; got != want {
		t.Errorf("findFold = %q, want %q", got, want)
	}
	if got := findFold(dir, "Other.gba"); got != "" {
		t.Errorf("findFold missing = %q", got)
	}
	if got := findFold(filepath.Join(dir, "nope"), "x"); got != "" {
		t.Errorf("findFold in missing dir = %q", got)
	}
	if got, want := freeName(dir, "Game.gba"), map[bool]string{true: "Game (3).gba", false: "Game (2).gba"}[folds]; got != want {
		t.Errorf("freeName = %q, want %q", got, want)
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

// M8: names are normalized to NFC before anything else (an iPhone gives
// decomposed NFD names).
func TestNamesAreNormalizedToNFC(t *testing.T) {
	nfd := "Café Rêverie.gba" // e + U+0301, e + U+0302
	nfc := "Café Rêverie.gba"
	if got, err := CleanFileName(nfd); err != nil || got != nfc {
		t.Fatalf("CleanFileName(NFD) = %q %v, want %q", got, err, nfc)
	}
	if got, err := CleanFileName(nfc); err != nil || got != nfc {
		t.Fatalf("CleanFileName(NFC) = %q %v", got, err)
	}
}

// Codex review of M20: on a case-sensitive file system (Knulli on ext4),
// Game.gba and game.gba are two files: no conflict, and an overwrite
// never trashes the other one.
func TestUploadNextToAnotherCase(t *testing.T) {
	e := newEnv(t)
	probe := e.path("Roms/" + gbaSystem + "/Starlight Courier.gba")
	if _, err := os.Lstat(strings.ToUpper(probe[:len(probe)-4]) + ".GBA"); err == nil {
		t.Skip("the temporary folder ignores case: run on Linux (make emulation)")
	}
	if got := findFold(filepath.Dir(probe), "starlight courier.gba"); got != "" {
		t.Fatalf("findFold took %q", got)
	}
	for _, rule := range []string{"", ConflictOverwrite} {
		id := e.uploaded(romTarget("STARLIGHT COURIER.gba"), []byte("other game "+rule), rule)
		e.complete(id)
	}
	if e.read("Roms/"+gbaSystem+"/Starlight Courier.gba") == "" || !e.exists("Roms/"+gbaSystem+"/STARLIGHT COURIER.gba") {
		t.Fatal("the games of two cases do not coexist")
	}
	if entries, _, _ := e.trash.List(); len(entries) != 1 {
		// Only the second upload replaces the first one of the same name.
		t.Fatalf("trash: %+v", entries)
	}
}

// foldsCase reports whether the file system of dir ignores case.
func foldsCase(dir string) bool {
	probe := filepath.Join(dir, "case-probe")
	if err := os.WriteFile(probe, nil, 0o644); err != nil {
		return true
	}
	defer os.Remove(probe)
	_, err := os.Lstat(filepath.Join(dir, "CASE-PROBE"))
	return err == nil
}
