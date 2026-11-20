package library

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSD(t *testing.T, sd, rel, data string) {
	t.Helper()
	p := filepath.Join(sd, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func names(f SDFolder) string {
	var out []string
	for _, e := range f.Entries {
		out = append(out, e.Kind[:2]+":"+e.Name)
	}
	return strings.Join(out, ",")
}

func TestBrowseRoot(t *testing.T) {
	l, sd := newLibrary(t)
	writeSD(t, sd, "b.txt", "bb")
	writeSD(t, sd, "A.txt", "a")
	root, err := l.Browse("")
	if err != nil {
		t.Fatal(err)
	}
	if root.FolderID != nil || root.ParentID != nil || root.Path != "" || root.Truncated {
		t.Fatalf("root %+v", root)
	}
	// Folders first (hidden ones listed), then files, ignoring case.
	got := names(root)
	if !strings.HasPrefix(got, "fo:.system,fo:.userdata,fo:Cheats,fo:Emus,fo:Roms,fo:Saves,fi:A.txt,fi:b.txt") {
		t.Fatalf("root: %s", got)
	}
	for _, e := range root.Entries {
		if (e.Kind == SDEntryFolder) != (e.SizeBytes == nil) {
			t.Fatalf("size of %+v", e)
		}
		if e.Name == "b.txt" && *e.SizeBytes != 2 {
			t.Fatalf("size %d", *e.SizeBytes)
		}
	}
	// Into a folder and back: parentId of a first-level folder is null.
	var roms SDEntry
	for _, e := range root.Entries {
		if e.Name == "Roms" {
			roms = e
		}
	}
	f, err := l.Browse(roms.ID)
	if err != nil || f.FolderID == nil || *f.FolderID != roms.ID || f.ParentID != nil || f.Path != "Roms" {
		t.Fatalf("Roms: %+v %v", f, err)
	}
	sub, err := l.Browse(f.Entries[0].ID)
	if err != nil || sub.ParentID == nil || *sub.ParentID != roms.ID {
		t.Fatalf("sub: %+v %v", sub, err)
	}
}

func TestBrowseHidesReminisLinkData(t *testing.T) {
	l, sd := newLibrary(t)
	writeSD(t, sd, ".userdata/shared/ReminisLink/clients.json", "{}")
	writeSD(t, sd, ".userdata/shared/ReminisLink/tls/key.pem", "secret")
	writeSD(t, sd, ".userdata/tg5040/logs/ReminisLink.txt", "log")
	writeSD(t, sd, ".userdata/tg5040/logs/ReminisLink.stderr.txt", "log")
	writeSD(t, sd, ".userdata/tg5040/logs/minui.txt", "other log")
	shared, err := l.Browse(SDEntryID(".userdata/shared"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(names(shared), "ReminisLink") || !strings.Contains(names(shared), "fo:.minui") {
		t.Fatalf("shared: %s", names(shared))
	}
	logs, err := l.Browse(SDEntryID(".userdata/tg5040/logs"))
	if err != nil || names(logs) != "fi:minui.txt" {
		t.Fatalf("logs: %s %v", names(logs), err)
	}
	// Not reachable either, whatever the case of the path (exFAT).
	for _, rel := range []string{
		".userdata/shared/ReminisLink", ".userdata/shared/ReminisLink/tls", ".userdata/shared/ReminisLink/tls/key.pem",
		".userdata/SHARED/reminislink/clients.json", ".userdata/tg5040/logs/ReminisLink.txt", ".userdata/tg5040/logs/reminislink.STDERR.txt",
	} {
		if _, err := l.Browse(SDEntryID(rel)); !errors.Is(err, ErrNotFound) {
			t.Errorf("browse %s: %v", rel, err)
		}
		if _, _, _, err := l.OpenSDEntry(SDEntryID(rel)); !errors.Is(err, ErrNotFound) {
			t.Errorf("open %s: %v", rel, err)
		}
		if _, _, err := l.ResolveSDEntry(SDEntryID(rel)); !errors.Is(err, ErrNotFound) {
			t.Errorf("resolve %s: %v", rel, err)
		}
	}
}

func TestBrowseRefusesLinksAndTraversal(t *testing.T) {
	l, sd := newLibrary(t)
	outside := t.TempDir()
	writeSD(t, outside, "secret.txt", "secret")
	if err := os.Symlink(outside, filepath.Join(sd, "Out")); err != nil {
		t.Skip("no symbolic links here")
	}
	if err := os.Symlink(filepath.Join(sd, "Roms"), filepath.Join(sd, "In")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(sd, "file-link")); err != nil {
		t.Fatal(err)
	}
	root, err := l.Browse("")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range root.Entries {
		if e.Name == "Out" || e.Name == "In" || e.Name == "file-link" {
			t.Fatalf("link listed: %+v", e)
		}
	}
	for _, rel := range []string{"Out", "Out/secret.txt", "In", "file-link"} {
		if _, err := l.Browse(SDEntryID(rel)); !errors.Is(err, ErrNotFound) {
			t.Errorf("browse %s: %v", rel, err)
		}
		if _, _, _, err := l.OpenSDEntry(SDEntryID(rel)); !errors.Is(err, ErrNotFound) {
			t.Errorf("open %s: %v", rel, err)
		}
	}
	for _, id := range []string{
		"se_" + EncodeID("../etc/passwd"), "se_" + EncodeID("/etc/passwd"), "se_" + EncodeID("Roms/../.."),
		"se_" + EncodeID("Roms//x"), "se_" + EncodeID("Roms\\x"), "se_" + EncodeID("a\x00b"), "se_%%", "sd_" + EncodeID("Roms"),
		"se_",
	} {
		if _, err := l.Browse(id); !errors.Is(err, ErrInvalidID) {
			t.Errorf("browse %q: %v", id, err)
		}
	}
	// A file is not a folder; a missing entry is missing.
	if _, err := l.Browse(SDEntryID("README.md")); !errors.Is(err, ErrNotFound) {
		t.Errorf("file as folder: %v", err)
	}
	if _, err := l.Browse(SDEntryID("Nothing")); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
}

func TestOpenSDEntry(t *testing.T) {
	l, sd := newLibrary(t)
	writeSD(t, sd, "Tools/tg5040/notes.txt", "hello")
	f, fi, name, err := l.OpenSDEntry(SDEntryID("Tools/tg5040/notes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	data, _ := io.ReadAll(f)
	if string(data) != "hello" || fi.Size() != 5 || name != "notes.txt" {
		t.Fatalf("%q %d %q", data, fi.Size(), name)
	}
	if _, _, _, err := l.OpenSDEntry(SDEntryID("Tools/tg5040")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("folder: %v", err)
	}
}

func TestBrowseTruncates(t *testing.T) {
	l, sd := newLibrary(t)
	dir := filepath.Join(sd, "Many")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxBrowseEntries+3; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%05d", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "zdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := l.Browse(SDEntryID("Many"))
	// A bounded selection in the listing order: the folder first, then
	// the first files by name; the last ones are left out.
	if err != nil || !f.Truncated || len(f.Entries) != MaxBrowseEntries || f.Entries[0].Name != "zdir" ||
		f.Entries[1].Name != "f00000" || f.Entries[MaxBrowseEntries-1].Name != fmt.Sprintf("f%05d", MaxBrowseEntries-2) {
		t.Fatalf("truncated %v %d %v", f.Truncated, len(f.Entries), err)
	}
}
