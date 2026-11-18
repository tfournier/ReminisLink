package cheats

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/osadapter/nextui"
	"github.com/tfournier/ReminisLink/internal/testutil"
)

// sample is the test file of Cheat Manager (cht_test.go).
const sample = "# comment\r\ncheats = 3\r\n\r\ncheat0_desc = \"Infinite health\"\r\ncheat0_code = \"82000F20+0063\"\r\ncheat0_enable = false\r\ncheat0_handler = \"1\"\r\n\r\ncheat2_desc = \"Max money\"\r\ncheat2_code = \"XXXX YYYY\"\r\ncheat2_enable = true\r\n\r\ncheat1_desc = \"Debug\"\r\ncheat1_code = \"ABC-DEF\"\r\ncheat1_enable = false\r\n"

// TestParse and TestRoundTripAndEdit are copied from Cheat Manager.
func TestParse(t *testing.T) {
	cf := Parse([]byte(sample))
	if len(cf.Cheats) != 3 {
		t.Fatalf("want 3 cheats, got %d", len(cf.Cheats))
	}
	if cf.Cheats[1].Desc != "Debug" || cf.Cheats[2].Desc != "Max money" || !cf.Cheats[2].Enable {
		t.Fatalf("wrong order or values: %+v", cf.Cheats)
	}
	if len(cf.Cheats[0].Extras) != 1 || cf.Cheats[0].Extras[0].Key != "handler" {
		t.Fatalf("handler key lost: %+v", cf.Cheats[0])
	}
	if len(cf.Header) != 1 || cf.Header[0] != "# comment" {
		t.Fatalf("header lost: %q", cf.Header)
	}
}

func TestRoundTripAndEdit(t *testing.T) {
	cf := Parse([]byte(sample))
	cf.Cheats = append(cf.Cheats[:1], cf.Cheats[2:]...) // remove "Debug"
	cf.Cheats = append(cf.Cheats, Cheat{Desc: `God "mode"`, Code: "1234+5678"})
	out := string(cf.Bytes())
	for _, want := range []string{
		"cheats = 3\n",
		"cheat0_handler = \"1\"",
		"cheat1_desc = \"Max money\"",
		"cheat1_enable = true",
		"cheat2_desc = \"God 'mode'\"",
		"cheat2_enable = false",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
	again := Parse([]byte(out))
	if string(again.Bytes()) != out {
		t.Errorf("re-encoding is not stable")
	}
	// The count written matches what the game listing reads.
	if n, ok := DeclaredCount([]byte(out)); !ok || n != 3 || Count([]byte(out)) != 3 {
		t.Errorf("count %d %v", n, ok)
	}
}

type env struct {
	t   *testing.T
	sd  string
	a   *nextui.Adapter
	svc *Service
}

func newEnv(t *testing.T) *env {
	t.Helper()
	sd := testutil.CopySDCard(t)
	a := nextui.New(nextui.RootsForSD(sd, "tg5040"), nil)
	svc, err := New(Options{Adapter: a})
	if err != nil {
		t.Fatal(err)
	}
	return &env{t: t, sd: sd, a: a, svc: svc}
}

func (e *env) game(rel string) osadapter.Game {
	e.t.Helper()
	g, err := e.a.Game("Game Boy Advance (GBA)/" + rel)
	if err != nil {
		e.t.Fatal(err)
	}
	return g
}

func (e *env) read(rel string) string {
	e.t.Helper()
	data, err := os.ReadFile(filepath.Join(e.sd, filepath.FromSlash(rel)))
	if err != nil {
		e.t.Fatal(err)
	}
	return string(data)
}

const pgCht = "Cheats/GBA/Starlight Courier.gba.cht"

func TestGetPut(t *testing.T) {
	e := newEnv(t)
	g := e.game("Starlight Courier.gba")
	before := e.read(pgCht)
	list, err := e.svc.Get(g)
	if err != nil || len(list.Cheats) != 2 || list.ETag != ETagOf([]byte(before)) {
		t.Fatalf("get: %+v %v", list, err)
	}
	moon := list.Cheats[1]
	if moon.Index != 1 || moon.Description != "Moon jump" || !moon.Enabled || moon.Extra["cheat1_handler"] != "1" {
		t.Fatalf("cheat: %+v", moon)
	}
	if len(list.Cheats[0].Extra) != 0 || list.Cheats[0].Extra == nil {
		t.Fatalf("extra must be an empty object: %+v", list.Cheats[0])
	}

	// Reorder and toggle: the unknown key follows its cheat, renumbered.
	edited := []APICheat{moon, list.Cheats[0]}
	edited[0].Enabled = false
	edited[1].Extra = map[string]string{"address": "0x10"} // bare key accepted
	put, err := e.svc.Put(g, list.ETag, edited)
	if err != nil || len(put.Cheats) != 2 || put.ETag == list.ETag {
		t.Fatalf("put: %+v %v", put, err)
	}
	written := e.read(pgCht)
	for _, want := range []string{"cheats = 2\n", "cheat0_desc = \"Moon jump\"", "cheat0_enable = false", "cheat0_handler = \"1\"", "cheat1_address = \"0x10\""} {
		if !strings.Contains(written, want) {
			t.Errorf("missing %q in\n%s", want, written)
		}
	}
	if put.Cheats[0].Extra["cheat0_handler"] != "1" || put.ETag != ETagOf([]byte(written)) {
		t.Fatalf("answer: %+v", put)
	}
	// The previous file is the .bak; the listing count follows the file.
	if e.read(pgCht+BakSuffix) != before || CountFile(filepath.Join(e.sd, pgCht)) != 2 {
		t.Fatal(".bak or count")
	}

	// Stale ETag: 412.
	if _, err := e.svc.Put(g, list.ETag, edited); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale etag: %v", err)
	}
	// "*" matches an existing file only.
	if _, err := e.svc.Put(g, "*", edited); err != nil {
		t.Fatalf("*: %v", err)
	}
	if _, err := e.svc.Put(e.game("Lantern Keeper [b1].gba"), "*", edited); !errors.Is(err, ErrConflict) {
		t.Fatalf("* without file: %v", err)
	}

	// Invalid keys.
	for _, extra := range []map[string]string{{"desc": "x"}, {"cheat3_code": "x"}, {"bad key": "x"}, {"handler": "1", "cheat0_handler": "2"}} {
		bad := []APICheat{{Description: "x", Extra: extra}}
		if _, err := e.svc.Put(g, "*", bad); !errors.Is(err, ErrInvalidCheat) {
			t.Errorf("extra %v: %v", extra, err)
		}
	}

	// Empty list: the file is removed, its content stays in the .bak.
	cur, _ := e.svc.Get(g)
	last := e.read(pgCht)
	empty, err := e.svc.Put(g, cur.ETag, []APICheat{})
	if err != nil || len(empty.Cheats) != 0 || empty.ETag != ETagOf(nil) {
		t.Fatalf("empty: %+v %v", empty, err)
	}
	if _, err := os.Stat(filepath.Join(e.sd, pgCht)); err == nil {
		t.Fatal("file not removed")
	}
	if e.read(pgCht+BakSuffix) != last {
		t.Fatal(".bak of the removed file")
	}
	// A game without file: empty list, ETag of an empty file; a first PUT
	// creates the file.
	lk := e.game("Lantern Keeper [b1].gba")
	l, _ := e.svc.Get(lk)
	if len(l.Cheats) != 0 || l.ETag != ETagOf(nil) {
		t.Fatalf("no file: %+v", l)
	}
	if _, err := e.svc.Put(lk, l.ETag, []APICheat{{Description: "New", Code: "1"}}); err != nil {
		t.Fatal(err)
	}
	if CountFile(filepath.Join(e.sd, "Cheats/GBA/Lantern Keeper [b1].gba.cht")) != 1 {
		t.Fatal("created file")
	}
}

func TestImport(t *testing.T) {
	e := newEnv(t)
	g := e.game("Starlight Courier.gba")
	res, err := e.svc.Import(g, []byte(sample), ModeMerge)
	if err != nil || res.Imported != 3 || res.Total != 5 {
		t.Fatalf("merge: %+v %v", res, err)
	}
	list, _ := e.svc.Get(g)
	if list.Cheats[2].Description != "Infinite health" || list.Cheats[2].Extra["cheat2_handler"] != "1" {
		t.Fatalf("merged: %+v", list.Cheats)
	}
	res, err = e.svc.Import(g, []byte("cheat0_desc = \"Only\"\ncheat0_code = \"1\"\n"), ModeReplace)
	if err != nil || res.Imported != 1 || res.Total != 1 {
		t.Fatalf("replace: %+v %v", res, err)
	}
	if !strings.Contains(e.read(pgCht+BakSuffix), "Infinite health") {
		t.Fatal("previous file not kept")
	}
	// The comment lines of the imported file are used when there is no file.
	lk := e.game("Lantern Keeper [b1].gba")
	if _, err := e.svc.Import(lk, []byte(sample), ModeReplace); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(e.read("Cheats/GBA/Lantern Keeper [b1].gba.cht"), "# comment\ncheats = 3\n") {
		t.Fatal("header of the imported file")
	}
	if _, err := e.svc.Import(g, []byte("not a cheat file"), ModeMerge); !errors.Is(err, ErrNoCheat) {
		t.Fatalf("no cheat: %v", err)
	}
	if _, err := e.svc.Import(g, []byte(sample), "append"); !errors.Is(err, ErrInvalidMode) {
		t.Fatalf("mode: %v", err)
	}
}

func TestTooLarge(t *testing.T) {
	e := newEnv(t)
	g := e.game("Starlight Courier.gba")
	big := strings.Repeat("# padding line\n", MaxFileBytes/15+10)
	testutil.WriteFile(t, e.sd, pgCht, big)
	if _, err := e.svc.Get(g); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("get: %v", err)
	}
	if _, err := e.svc.Put(g, "*", nil); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("put: %v", err)
	}
}
