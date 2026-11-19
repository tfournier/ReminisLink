package saves

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// openConfined refuses a link at any component, inside the saves folder
// or out of it; os.Root itself refuses an escape even without that check.
func TestOpenConfined(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "GBA", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(dir, "GBA", "sub", "x.sav"), filepath.Join(outside, "x.sav")} {
		if err := os.WriteFile(p, []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("GBA", filepath.Join(dir, "inside")); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	links := map[string]string{
		filepath.Join(dir, "out"):                   outside,
		filepath.Join(dir, "GBA", "link.sav"):       filepath.Join(dir, "GBA", "sub", "x.sav"),
		filepath.Join(dir, "GBA", "sub", "esc.sav"): filepath.Join(outside, "x.sav"),
	}
	for link, target := range links {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	f, fi, err := openConfined(root, filepath.Join("GBA", "sub", "x.sav"))
	if err != nil || fi.Size() != 4 {
		t.Fatalf("plain file: %v", err)
	}
	f.Close()
	for _, rel := range []string{
		filepath.Join("inside", "sub", "x.sav"), // link component inside the folder
		filepath.Join("out", "x.sav"),           // link component out of it
		filepath.Join("GBA", "link.sav"),        // linked file inside
		filepath.Join("GBA", "sub", "esc.sav"),  // linked file outside
		"GBA",                                   // not a regular file
	} {
		if f, _, err := openConfined(root, rel); !errors.Is(err, errOutside) {
			if f != nil {
				f.Close()
			}
			t.Errorf("%s: %v", rel, err)
		}
	}
	// The escape is refused by os.Root on its own (a link swapped in
	// after the checks).
	if f, err := root.Open(filepath.Join("out", "x.sav")); err == nil {
		f.Close()
		t.Error("os.Root followed a link out of the folder")
	}
	for _, p := range []string{filepath.Join(dir, "..", "x.sav"), dir, "/etc/passwd"} {
		if _, err := relToRoot(dir, p); !errors.Is(err, errOutside) {
			t.Errorf("relToRoot(%s): %v", p, err)
		}
	}
}
