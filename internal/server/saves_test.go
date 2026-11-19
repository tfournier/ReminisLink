package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/saves"
)

func (f *fixture) writeSD(rel, content string) {
	f.t.Helper()
	p := filepath.Join(f.sd, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func TestListSaves(t *testing.T) {
	f := newFixture(t)
	f.writeSD("Saves/GBA/Starlight Courier.gba.rtc", "clock")
	f.writeSD("Saves/GBA/Lantern Keeper [b1].gba.rtc", "lone clock")
	r := f.get("/api/v1/saves", "/saves", http.StatusOK)
	var l saves.Listing
	r.JSON(t, &l)
	if l.Truncated || len(l.Saves) != 5 {
		t.Fatalf("listing %s", r.Body)
	}
	byID := map[string]saves.Summary{}
	for _, s := range l.Saves {
		byID[s.GameID] = s
	}
	sc := byID[library.EncodeID(scPath)]
	if sc.Path != "Saves/GBA/Starlight Courier.gba.sav" || sc.SHA256 != sha([]byte(f.read(sc.Path))) ||
		sc.RTC == nil || sc.RTC.SHA256 != sha([]byte("clock")) {
		t.Fatalf("Starlight Courier: %+v", sc)
	}
	if _, ok := byID[library.EncodeID(gbaFolder+"/Lantern Keeper [b1].gba")]; ok {
		t.Fatal("a game with only a clock file is listed")
	}
	// Signed route.
	if r := f.unsigned(http.MethodGet, "/api/v1/saves", nil); r.Status != http.StatusUnauthorized {
		t.Fatalf("unsigned: %d", r.Status)
	}
}

// A listing whose client went away stops without answering; during a
// shutdown it answers 503.
func TestListSavesCancelled(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/saves", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	f.server.handleListSaves(rec, req)
	if rec.Body.Len() != 0 {
		t.Fatalf("answer after cancellation: %s", rec.Body)
	}
	f.server.BeginShutdown()
	rec = httptest.NewRecorder()
	f.server.handleListSaves(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("shutdown: %d %s", rec.Code, rec.Body)
	}
}

func TestSaveClockRoute(t *testing.T) {
	f := newFixture(t)
	const path = "/games/{gameId}/save/rtc"
	g := gamePath(scPath) + "/save/rtc"
	wantCode(t, f.do(http.MethodGet, g, path, nil, http.StatusNotFound), CodeNotFound)
	f.writeSD("Saves/GBA/Starlight Courier.gba.rtc", "clock")
	r := f.do(http.MethodGet, g, path, nil, http.StatusOK)
	if string(r.Body) != "clock" || r.Header.Get("Content-Disposition") != `attachment; filename="Starlight Courier.gba.rtc"` {
		t.Fatalf("clock file: %q %v", r.Body, r.Header)
	}
	wantCode(t, f.do(http.MethodGet, gamePath(gbaFolder+"/Nope.gba")+"/save/rtc", path, nil, http.StatusNotFound), CodeNotFound)
	wantCode(t, f.do(http.MethodGet, "/api/v1/games/%2E%2E/save/rtc", path, nil, http.StatusBadRequest), CodeInvalidID)
}

// A clock file behind a linked tag folder (outside Saves/ or inside it)
// is never served.
func TestSaveClockRouteRefusesLinkedFolder(t *testing.T) {
	f := newFixture(t)
	const path = "/games/{gameId}/save/rtc"
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "Starlight Courier.gba.rtc"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	gbaSaves := filepath.Join(f.sd, "Saves/GBA")
	if err := os.Rename(gbaSaves, filepath.Join(f.sd, "Saves/GBA real")); err != nil {
		t.Fatal(err)
	}
	f.writeSD("Saves/GBA real/Starlight Courier.gba.rtc", "clock")
	for _, target := range []string{outside, "GBA real"} {
		os.Remove(gbaSaves)
		if err := os.Symlink(target, gbaSaves); err != nil {
			t.Skipf("no symbolic links here: %v", err)
		}
		r := f.do(http.MethodGet, gamePath(scPath)+"/save/rtc", path, nil, http.StatusNotFound)
		wantCode(t, r, CodeNotFound)
	}
}
