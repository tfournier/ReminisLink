package paks

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/osadapter/nextui"
	"github.com/tfournier/ReminisLink/internal/store"
)

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCopyFolder(t *testing.T) {
	src := filepath.Join(t.TempDir(), "Clock.pak")
	write(t, filepath.Join(src, "launch.sh"), "#!/bin/sh\n")
	write(t, filepath.Join(src, "res", "a.png"), "png")
	write(t, filepath.Join(src, ".hidden"), "h")
	dest := t.TempDir()
	var done, total int64
	s, err := CopyFolder(context.Background(), src, dest, func(need int64) error { total = need; return nil },
		func(d, _ int64) { done = d })
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "Clock.pak" || s.Dir != filepath.Join(dest, "Clock.pak") || total != 14 || done != 14 {
		t.Fatalf("staged %+v total %d done %d", s, total, done)
	}
	if got, _ := os.ReadFile(filepath.Join(s.Dir, "res", "a.png")); string(got) != "png" {
		t.Fatalf("copied %q", got)
	}
}

func TestCopyFolderRefusals(t *testing.T) {
	base := t.TempDir()
	// A link anywhere refuses the folder.
	src := filepath.Join(base, "Link.pak")
	write(t, filepath.Join(src, "launch.sh"), "x")
	if err := os.Symlink("/etc", filepath.Join(src, "etc")); err != nil {
		t.Skip("no symbolic links here")
	}
	if _, err := CopyFolder(context.Background(), src, t.TempDir(), nil, nil); !isRefusal(err, "symbolic link") {
		t.Fatalf("link: %v", err)
	}
	// No launch.sh at its top.
	src = filepath.Join(base, "Empty.pak")
	write(t, filepath.Join(src, "bin", "launch.sh"), "x")
	if _, err := CopyFolder(context.Background(), src, t.TempDir(), nil, nil); !isRefusal(err, "no launch.sh") {
		t.Fatalf("no launch.sh: %v", err)
	}
	// Not a .pak folder.
	src = filepath.Join(base, "Folder")
	write(t, filepath.Join(src, "launch.sh"), "x")
	if _, err := CopyFolder(context.Background(), src, t.TempDir(), nil, nil); !isRefusal(err, "not a .pak folder") {
		t.Fatalf("not a pak: %v", err)
	}
	// The space check comes before any write.
	src = filepath.Join(base, "Big.pak")
	write(t, filepath.Join(src, "launch.sh"), "x")
	dest := t.TempDir()
	full := errors.New("full")
	if _, err := CopyFolder(context.Background(), src, dest, func(int64) error { return full }, nil); !errors.Is(err, full) {
		t.Fatalf("space: %v", err)
	}
	if des, _ := os.ReadDir(dest); len(des) != 0 {
		t.Fatal("written before the space check")
	}
}

func isRefusal(err error, want string) bool {
	fe, ok := IsFormatError(err)
	return ok && strings.Contains(fe.Reason, want)
}

func newTestManager(t *testing.T) (*Manager, string) {
	t.Helper()
	sd := t.TempDir()
	roots := nextui.RootsForSD(sd, "tg5040")
	write(t, filepath.Join(roots.Tools, "ReminisLink.pak", "launch.sh"), "x")
	write(t, filepath.Join(roots.Tools, "ReminisLink.pak", "pak.json"), `{"version":"v1.0.0","type":"TOOL"}`)
	write(t, filepath.Join(roots.Tools, "clock.pak", "launch.sh"), "abc")
	write(t, filepath.Join(roots.Tools, ".Hidden.pak", "launch.sh"), "x")
	write(t, filepath.Join(roots.Tools, "notes.txt"), "x")
	write(t, filepath.Join(roots.Tools, "Files.pak"), "a file, not a pak")
	write(t, filepath.Join(roots.Emus, "GBA.pak", "launch.sh"), "x")
	write(t, filepath.Join(roots.Emus, "GBA.pak", "pak.json"), `{"version": 3}`)
	return NewManager(roots, filepath.Join(roots.Tools, "ReminisLink.pak")), sd
}

func TestListPaks(t *testing.T) {
	m, sd := newTestManager(t)
	_ = os.Symlink(filepath.Join(sd, "Tools", "tg5040", "clock.pak"), filepath.Join(sd, "Tools", "tg5040", "Linked.pak"))
	list, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range list {
		names = append(names, p.Kind+":"+p.Name)
	}
	if strings.Join(names, ",") != "tool:clock,tool:ReminisLink,emu:GBA" {
		t.Fatalf("listed %v", names)
	}
	self := list[1]
	if !self.Self || self.Version == nil || *self.Version != "v1.0.0" || self.Path != "Tools/tg5040/ReminisLink.pak" {
		t.Fatalf("self %+v", self)
	}
	if list[0].Self || list[0].SizeBytes != 3 || list[0].Version != nil {
		t.Fatalf("clock %+v", list[0])
	}
	if list[2].Version != nil {
		t.Fatalf("a numeric version is no version: %+v", list[2])
	}
	// Every listed id resolves to its pak.
	for _, p := range list {
		got, dir, err := m.Resolve(p.ID)
		if err != nil || got.Name != p.Name || filepath.Base(dir) != p.Name+".pak" {
			t.Fatalf("resolve %s: %+v %s %v", p.ID, got, dir, err)
		}
	}
}

func TestResolveRefusals(t *testing.T) {
	m, sd := newTestManager(t)
	id := func(rel string) string { return "pk_" + library.EncodeID(rel) }
	for _, tc := range []struct {
		id   string
		want error
	}{
		{"", ErrInvalidID},
		{"clock", ErrInvalidID},
		{"pk_!!", ErrInvalidID},
		{id("Tools/tg5040/../tg5040/clock.pak"), ErrInvalidID},
		{id("Tools/tg5040/notes.txt"), ErrInvalidID},
		{id("Tools/tg5040/.Hidden.pak"), ErrInvalidID},
		{id("Roms/clock.pak"), ErrInvalidID},
		{id("Tools/tg5050/clock.pak"), ErrInvalidID},
		{id("Tools/tg5040"), ErrInvalidID},
		{id("Tools/tg5040/Missing.pak"), ErrNotFound},
		{id("Tools/tg5040/Files.pak"), ErrNotFound},
	} {
		if _, _, err := m.Resolve(tc.id); !errors.Is(err, tc.want) {
			t.Errorf("%q: %v, want %v", tc.id, err, tc.want)
		}
	}
	if err := os.Symlink(filepath.Join(sd, "Tools", "tg5040", "clock.pak"), filepath.Join(sd, "Tools", "tg5040", "Linked.pak")); err == nil {
		if _, _, err := m.Resolve(id("Tools/tg5040/Linked.pak")); !errors.Is(err, ErrNotFound) {
			t.Errorf("link: %v", err)
		}
	}
	// Case-insensitive file system or not, the kind comes from the folder.
	if m.KindOf(filepath.Join(sd, "TOOLS", "TG5040")) != KindTool || m.KindOf(filepath.Join(sd, "Emus", "tg5040")) != KindEmu || m.KindOf(sd) != "" {
		t.Fatal("KindOf")
	}
}

func TestKindFromMetadata(t *testing.T) {
	for typ, want := range map[string]string{"TOOL": KindTool, "emu": KindEmu, " Emu ": KindEmu, "APP": "", "": ""} {
		if got := KindFromMetadata(typ); got != want {
			t.Errorf("%q: %q", typ, got)
		}
	}
	if HasSuffix(".pak") || !HasSuffix("A.PAK") || DisplayName("Clock.Pak") != "Clock" {
		t.Fatal("suffix")
	}
}

// Review 3: the folders created for a first install are synced up to the
// SD root; an error is returned.
func TestEnsureDirSyncsNewFolders(t *testing.T) {
	sd := t.TempDir()
	m := NewManager(nextui.RootsForSD(sd, "tg5040"), "")
	var synced []string
	syncDir = func(dir string) error { synced = append(synced, dir); return nil }
	defer func() { syncDir = store.SyncDirChecked }()
	dir, err := m.EnsureDir(KindEmu)
	if err != nil {
		t.Fatal(err)
	}
	abs, _ := filepath.Abs(sd)
	want := []string{dir, filepath.Join(abs, "Emus"), abs}
	if strings.Join(synced, "|") != strings.Join(want, "|") {
		t.Fatalf("synced %v, want %v", synced, want)
	}
	// Existing: nothing to sync.
	synced = nil
	if _, err := m.EnsureDir(KindEmu); err != nil || len(synced) != 0 {
		t.Fatalf("existing: %v %v", synced, err)
	}
	failure := errors.New("fsync")
	syncDir = func(string) error { return failure }
	if _, err := m.EnsureDir(KindTool); !errors.Is(err, failure) {
		t.Fatalf("sync error: %v", err)
	}
}
