package states

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func hexSum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// listed indexes a listing by game name, system tag (from the path) and slot.
func listed(l Listing) map[string]Summary {
	out := map[string]Summary{}
	for _, s := range l.States {
		out[filepath.Base(filepath.Dir(s.Path))+"|"+s.GameName+"|"+string(rune('0'+s.Slot))] = s
	}
	return out
}

func TestListAllStates(t *testing.T) {
	e := newEnv(t)
	// Hashes computed in the past: the files are older than RacyMargin.
	e.svc.hashesOf().now = func() time.Time { return time.Now().Add(time.Hour) }
	l, err := e.svc.ListAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := listed(l)
	st0, ok := got["GBA-gpsp|Starlight Courier|0"]
	if !ok {
		t.Fatalf("slot 0 missing: %+v", l.States)
	}
	data, _ := os.ReadFile(e.path(statesGB + "Starlight Courier.gba.st0"))
	if st0.SHA256 != hexSum(data) || st0.SizeBytes != int64(len(data)) || st0.Path != statesGB+"Starlight Courier.gba.st0" {
		t.Errorf("slot 0: %+v", st0)
	}
	thumb, _ := os.ReadFile(e.path(".userdata/shared/.minui/GBA/Starlight Courier.gba.0.bmp"))
	if st0.Thumbnail == nil || st0.Thumbnail.SHA256 != hexSum(thumb) {
		t.Errorf("thumbnail of slot 0: %+v", st0.Thumbnail)
	}
	for _, k := range []string{"GBA-gpsp|Starlight Courier|1", "GBA-gpsp|Starlight Courier|9", "MGBA-mgba|Starlight Courier|0"} {
		if s, ok := got[k]; !ok || s.Thumbnail != nil {
			t.Errorf("%s: %+v (listed %v)", k, s, ok)
		}
	}
	if l.Truncated {
		t.Error("truncated")
	}

	// A second listing reads nothing unchanged; a replaced state is hashed again.
	p := e.path(statesGB + "Starlight Courier.gba.st0")
	before := e.svc.hashesOf().hashed[p]
	if _, err := e.svc.ListAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := e.svc.hashesOf().hashed[p]; n != before {
		t.Errorf("unchanged state hashed again (%d → %d)", before, n)
	}
	tmp := p + ".new"
	os.WriteFile(tmp, []byte("new state"), 0o644)
	os.Rename(tmp, p)
	l, _ = e.svc.ListAll(context.Background())
	if s := listed(l)["GBA-gpsp|Starlight Courier|0"]; s.SHA256 != hexSum([]byte("new state")) {
		t.Errorf("replaced state: %+v", s)
	}
}

func TestListAllStatesSkipsLinksAndTruncates(t *testing.T) {
	e := newEnv(t)
	// A state that is a link (to a file outside the states folder) is skipped.
	outside := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(outside, []byte("secret"), 0o644)
	link := e.path(statesGB + "Starlight Courier.gba.st2")
	if err := os.Symlink(outside, link); err != nil {
		t.Skip(err)
	}
	// A thumbnail that is not a PNG is null.
	os.WriteFile(e.path(".userdata/shared/.minui/GBA/Starlight Courier.gba.1.bmp"), []byte("BM not a png"), 0o644)
	l, err := e.svc.ListAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := listed(l)
	if _, ok := got["GBA-gpsp|Starlight Courier|2"]; ok {
		t.Error("a link was listed")
	}
	if s := got["GBA-gpsp|Starlight Courier|1"]; s.Thumbnail != nil {
		t.Errorf("not a PNG: %+v", s.Thumbnail)
	}

	old := maxListedStates
	maxListedStates = 2
	defer func() { maxListedStates = old }()
	l, _ = e.svc.ListAll(context.Background())
	if !l.Truncated || len(l.States) != 2 {
		t.Errorf("truncated %v, %d states", l.Truncated, len(l.States))
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.svc.ListAll(ctx); err == nil {
		t.Error("a cancelled listing succeeded")
	}
}
