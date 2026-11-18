package cheats

import (
	"os"
	"path/filepath"
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
