package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Codex review of M22, round 3: a put back never replaces a file that
// appeared after its check: the item stays in the entry, reported.
func TestPutBackNeverReplacesAFileMadeMeanwhile(t *testing.T) {
	tr, sd, _, _ := newTestTrash(t)
	a := write(t, sd, "Screenshots/A.png", "a")
	b := write(t, sd, "Screenshots/B.png", "b")
	calls := 0
	tr.rename = func(oldpath, newpath string) error {
		calls++
		if calls == 2 { // B
			return errors.New("card error")
		}
		return os.Rename(oldpath, newpath)
	}
	// Another process makes A again right before A goes back.
	tr.renameBack = func(oldpath, newpath string) error {
		if newpath == a {
			if err := os.WriteFile(a, []byte("new"), 0o644); err != nil {
				return err
			}
		}
		return tr.renameNoReplaceInSD(oldpath, newpath)
	}
	_, err := tr.Move(ReasonDeleteScreenshot, "2 screenshots", []MoveItem{{Path: a}, {Path: b}})
	var partial *PartialMoveError
	if !errors.As(err, &partial) || len(partial.Kept) != 1 || partial.Kept[0] != "Screenshots/A.png" || len(partial.Held) != 0 {
		t.Fatalf("partial: %v %+v", err, partial)
	}
	if read(t, a) != "new" {
		t.Fatal("the new file was replaced")
	}
	if read(t, b) != "b" {
		t.Fatal("B moved")
	}
	if read(t, filepath.Join(tr.Dir(), partial.EntryID, "Screenshots/A.png")) != "a" {
		t.Fatal("A is not kept in the entry")
	}
	entries, _, _ := tr.List()
	if len(entries) != 1 || entries[0].Held {
		t.Fatalf("trash %+v", entries)
	}
}
