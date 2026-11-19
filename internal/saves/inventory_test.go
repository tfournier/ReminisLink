package saves

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/library"
)

func (e *env) write(rel, content string) {
	e.t.Helper()
	p := filepath.Join(e.sd, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func shaOf(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func (e *env) listAll() Listing {
	e.t.Helper()
	l, err := e.svc.ListAll(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	return l
}

func byGame(l Listing) map[string]Summary {
	m := map[string]Summary{}
	for _, s := range l.Saves {
		m[s.GameID] = s
	}
	return m
}

func (e *env) hashed() int {
	e.svc.hashes.mu.Lock()
	defer e.svc.hashes.mu.Unlock()
	return e.svc.hashes.hashed
}

const ps = "Sony PlayStation (PS)"

func TestListAll(t *testing.T) {
	e := newEnv(t)
	// An m3u game and a folder-cue game with a save, a clock file with
	// its save, a clock file alone.
	e.write("Saves/PS/Quiet Harbor.m3u.sav", "m3u save")
	e.write("Roms/"+ps+"/Tide Pool/Tide Pool.cue", `FILE "Tide Pool.bin" BINARY`)
	e.write("Roms/"+ps+"/Tide Pool/Tide Pool.bin", "bin")
	e.write("Saves/PS/Tide Pool.cue.sav", "cue save")
	e.write("Saves/GBA/Starlight Courier.gba.rtc", "clock")
	e.write("Saves/GBA/Lantern Keeper [b1].gba.rtc", "lone clock")

	l := e.listAll()
	if l.Truncated || !l.GeneratedAt.Equal(e.clk.Now()) {
		t.Fatalf("listing %+v", l)
	}
	got := byGame(l)
	want := map[string]string{ // game path -> save path
		gba + "/Starlight Courier.gba":                  "Saves/GBA/Starlight Courier.gba.sav",
		gba + "/Hacks/Starlight Courier Remix.gba":      "Saves/GBA/Starlight Courier Remix.gba.sav",
		gba + "/Café Rêverie (France).gba":              "Saves/GBA/café rêverie (france).gba.sav",
		"Game Boy Advance (MGBA)/Starlight Courier.gba": "Saves/MGBA/Starlight Courier.gba.sav",
		ps + "/Mossy Hollow":                            "Saves/PS/Mossy Hollow.m3u.sav",
		ps + "/Quiet Harbor.m3u":                        "Saves/PS/Quiet Harbor.m3u.sav",
		ps + "/Tide Pool":                               "Saves/PS/Tide Pool.cue.sav",
	}
	if len(got) != len(want) || len(l.Saves) != len(want) {
		t.Fatalf("got %d saves, want %d: %+v", len(l.Saves), len(want), l.Saves)
	}
	for game, save := range want {
		s, ok := got[library.EncodeID(game)]
		if !ok {
			t.Errorf("%s: not listed", game)
			continue
		}
		content := e.read(save)
		if s.Path != save || s.SHA256 != shaOf(content) || s.SizeBytes != int64(len(content)) {
			t.Errorf("%s: %+v", game, s)
		}
		fi, _ := os.Stat(filepath.Join(e.sd, save))
		if !s.ModifiedAt.Equal(fi.ModTime().UTC().Truncate(time.Second)) {
			t.Errorf("%s: date %v", game, s.ModifiedAt)
		}
	}
	sc := got[library.EncodeID(gba+"/Starlight Courier.gba")]
	if sc.SystemID != library.EncodeID(gba) || sc.GameName != "Starlight Courier" || sc.FileName != "Starlight Courier.gba" {
		t.Errorf("game fields %+v", sc)
	}
	if sc.RTC == nil || sc.RTC.Path != "Saves/GBA/Starlight Courier.gba.rtc" || sc.RTC.SHA256 != shaOf("clock") || sc.RTC.SizeBytes != 5 {
		t.Errorf("clock file %+v", sc.RTC)
	}
	if mh := got[library.EncodeID(ps+"/Mossy Hollow")]; mh.RTC != nil || mh.FileName != "Mossy Hollow" {
		t.Errorf("folder-m3u %+v", mh)
	}
	// Games without a save (Lantern Keeper has only a clock file) are not
	// listed.
	for _, g := range []string{gba + "/Lantern Keeper [b1].gba", gba + "/Pixel Garden 🌱.gba", "01) Game Boy (GB)/Puddle Jumper.gb"} {
		if _, ok := got[library.EncodeID(g)]; ok {
			t.Errorf("%s listed without a save", g)
		}
	}
}

func TestListAllNoSavesFolder(t *testing.T) {
	e := newEnv(t)
	if err := os.RemoveAll(filepath.Join(e.sd, "Saves")); err != nil {
		t.Fatal(err)
	}
	if l := e.listAll(); len(l.Saves) != 0 || l.Saves == nil {
		t.Fatalf("listing %+v", l)
	}
}

func TestListAllHashCache(t *testing.T) {
	e := newEnv(t)
	first := e.listAll()
	n := e.hashed()
	if n != len(first.Saves) {
		t.Fatalf("hashed %d files for %d saves", n, len(first.Saves))
	}
	// Nothing changed: nothing read again.
	second := e.listAll()
	if e.hashed() != n {
		t.Fatalf("unchanged files hashed again: %d -> %d", n, e.hashed())
	}
	if fmt.Sprint(first.Saves) != fmt.Sprint(second.Saves) {
		t.Fatal("listings differ")
	}

	// A save changed (new content, new date): hashed again.
	rel := "Saves/GBA/Starlight Courier.gba.sav"
	p := filepath.Join(e.sd, rel)
	e.write(rel, "played again")
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Fatal(err)
	}
	id := library.EncodeID(gba + "/Starlight Courier.gba")
	if s := byGame(e.listAll())[id]; s.SHA256 != shaOf("played again") {
		t.Fatalf("changed save %+v", s)
	}
	if e.hashed() != n+1 {
		t.Fatalf("hashed %d, want %d", e.hashed(), n+1)
	}

	// Replaced by another file of the same size and date (a rename over
	// it, as the pak does): hashed again.
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte("PLAYED AGAIN"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(tmp, later, later); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, p); err != nil {
		t.Fatal(err)
	}
	if s := byGame(e.listAll())[id]; s.SHA256 != shaOf("PLAYED AGAIN") {
		t.Fatalf("replaced save %+v", s)
	}

	// A save gone is forgotten by the cache.
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if _, ok := byGame(e.listAll())[id]; ok {
		t.Fatal("removed save listed")
	}
	e.svc.hashes.mu.Lock()
	_, cached := e.svc.hashes.m[p]
	e.svc.hashes.mu.Unlock()
	if cached {
		t.Fatal("removed save still cached")
	}
}

func TestListAllCancelled(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.svc.ListAll(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
	if e.hashed() != 0 {
		t.Fatal("hashed after cancellation")
	}
	// A listing waiting for another one stops with its context.
	e.svc.hashes.slot <- struct{}{}
	ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := e.svc.ListAll(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting: %v", err)
	}
	<-e.svc.hashes.slot
	// The slot is free again.
	if l := e.listAll(); len(l.Saves) == 0 {
		t.Fatal("no save after cancellations")
	}
}

// The listing never reads a file through a symbolic link nor outside the
// saves folder.
func TestListAllSkipsLinks(t *testing.T) {
	e := newEnv(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "Starlight Courier.gba.sav"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "Starlight Courier.gba.rtc"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A linked save file, a linked clock file next to a real save, and a
	// linked tag folder.
	sav := filepath.Join(e.sd, "Saves/GBA/Starlight Courier Remix.gba.sav")
	os.Remove(sav)
	if err := os.Symlink(filepath.Join(outside, "Starlight Courier.gba.sav"), sav); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "Starlight Courier.gba.rtc"), filepath.Join(e.sd, "Saves/GBA/Starlight Courier.gba.rtc")); err != nil {
		t.Fatal(err)
	}
	mgba := filepath.Join(e.sd, "Saves/MGBA")
	if err := os.RemoveAll(mgba); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, mgba); err != nil {
		t.Fatal(err)
	}
	got := byGame(e.listAll())
	for _, g := range []string{gba + "/Hacks/Starlight Courier Remix.gba", "Game Boy Advance (MGBA)/Starlight Courier.gba"} {
		if s, ok := got[library.EncodeID(g)]; ok {
			t.Errorf("%s: read through a link: %+v", g, s)
		}
	}
	sc, ok := got[library.EncodeID(gba+"/Starlight Courier.gba")]
	if !ok || sc.RTC != nil {
		t.Errorf("save with a linked clock file: %+v", sc)
	}
}

// Many games: one listing, then a cheap one from the cache.
func TestListAllManyGames(t *testing.T) {
	e := newEnv(t)
	const n = 1500
	sys := "Many (MANY)"
	for i := range n {
		name := fmt.Sprintf("Game %04d.gba", i)
		dir := "Roms/" + sys
		if i%2 == 1 {
			dir += "/Sub"
		}
		e.write(dir+"/"+name, "rom")
		e.write("Saves/MANY/"+name+".sav", fmt.Sprintf("save %d", i))
	}
	start := time.Now()
	l := e.listAll()
	count := 0
	for _, s := range l.Saves {
		if s.SystemID == library.EncodeID(sys) {
			count++
		}
	}
	if count != n {
		t.Fatalf("listed %d saves of %d", count, n)
	}
	before := e.hashed()
	l2 := e.listAll()
	if e.hashed() != before || len(l2.Saves) != len(l.Saves) {
		t.Fatalf("second listing hashed %d files", e.hashed()-before)
	}
	t.Logf("%d saves listed in %v", len(l.Saves), time.Since(start))
}

func TestListAllTruncated(t *testing.T) {
	e := newEnv(t)
	maxListedSaves = 3
	t.Cleanup(func() { maxListedSaves = MaxListedSaves })
	l := e.listAll()
	if !l.Truncated || len(l.Saves) != 3 {
		t.Fatalf("truncated listing: %v %d", l.Truncated, len(l.Saves))
	}
}

func TestOpenClock(t *testing.T) {
	e := newEnv(t)
	g := e.game("Starlight Courier.gba")
	if _, _, err := e.svc.OpenClock(g); !errors.Is(err, ErrNoClock) {
		t.Fatalf("no clock file: %v", err)
	}
	e.write("Saves/GBA/Starlight Courier.gba.rtc", "clock")
	f, name, err := e.svc.OpenClock(g)
	if err != nil || name != "Starlight Courier.gba.rtc" {
		t.Fatalf("open: %q %v", name, err)
	}
	f.Close()
}
