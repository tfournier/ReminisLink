package auth

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	testClientA = "3b1c8d2a-5e6f-4a7b-8c9d-0e1f2a3b4c5d"
	testClientB = "9e8d7c6b-5a49-4382-9170-6f5e4d3c2b1a"
)

func TestClientStoreLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ReminisLink", "clients.json")
	s, err := OpenClientStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.List()) != 0 {
		t.Fatal("new store not empty")
	}
	paired := time.Date(2026, 11, 18, 17, 0, 0, 0, time.UTC)
	keyA := bytes.Repeat([]byte{1}, PublicKeySize)
	if err := s.Put(Client{ID: testClientA, Name: "iPhone de Thomas", Platform: PlatformApple, PublicKey: keyA, PairedAt: paired}); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(Client{ID: testClientB, Name: "iPad", Platform: PlatformApple, PublicKey: bytes.Repeat([]byte{2}, PublicKeySize), PairedAt: paired}); err != nil {
		t.Fatal(err)
	}

	// Reload from disk.
	s2, err := OpenClientStore(path)
	if err != nil {
		t.Fatal(err)
	}
	c, ok := s2.Lookup(testClientA)
	if !ok || c.Name != "iPhone de Thomas" || !bytes.Equal(c.PublicKey, keyA) || !c.PairedAt.Equal(paired) {
		t.Fatalf("reloaded client = %+v, %v", c, ok)
	}
	if got := s2.List(); len(got) != 2 || got[0].ID != testClientA {
		t.Fatalf("List = %+v", got)
	}

	// Re-pairing replaces the key.
	keyA2 := bytes.Repeat([]byte{3}, PublicKeySize)
	if err := s2.Put(Client{ID: testClientA, Name: "iPhone", Platform: PlatformApple, PublicKey: keyA2, PairedAt: paired.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if c, _ := s2.Lookup(testClientA); !bytes.Equal(c.PublicKey, keyA2) || c.Name != "iPhone" {
		t.Fatalf("client not replaced: %+v", c)
	}
	if len(s2.List()) != 2 {
		t.Fatal("replace added a client")
	}

	// Removal.
	removed, err := s2.Remove(testClientB)
	if err != nil || !removed {
		t.Fatalf("Remove = %v, %v", removed, err)
	}
	if removed, err := s2.Remove(testClientB); err != nil || removed {
		t.Fatalf("second Remove = %v, %v", removed, err)
	}
	s3, err := OpenClientStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s3.Lookup(testClientB); ok {
		t.Fatal("removed client still on disk")
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temporary file left behind")
	}
}

func TestClientStoreFileFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clients.json")
	s, _ := OpenClientStore(path)
	key := bytes.Repeat([]byte{0xff}, PublicKeySize)
	if err := s.Put(Client{ID: testClientA, Name: "iPhone", Platform: PlatformApple, PublicKey: key, PairedAt: time.Date(2026, 11, 18, 17, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	clients := raw["clients"].([]any)
	c := clients[0].(map[string]any)
	if raw["version"] != float64(2) || c["clientId"] != testClientA || c["publicKey"] != EncodeB64U(key) ||
		c["platform"] != "apple" || c["pairedAt"] != "2026-11-18T17:00:00Z" || c["key"] != nil {
		t.Fatalf("unexpected file:\n%s", data)
	}
}

func TestClientStoreLookupReturnsCopy(t *testing.T) {
	s, _ := OpenClientStore(filepath.Join(t.TempDir(), "clients.json"))
	if err := s.Put(Client{ID: testClientA, Platform: PlatformApple, PublicKey: bytes.Repeat([]byte{1}, PublicKeySize)}); err != nil {
		t.Fatal(err)
	}
	c, _ := s.Lookup(testClientA)
	c.PublicKey[0] = 0
	if c2, _ := s.Lookup(testClientA); c2.PublicKey[0] != 1 {
		t.Fatal("Lookup exposes the internal key slice")
	}
}

func TestClientStoreRejectsBadInput(t *testing.T) {
	s, _ := OpenClientStore(filepath.Join(t.TempDir(), "clients.json"))
	if err := s.Put(Client{ID: "not-a-uuid", Platform: PlatformApple, PublicKey: make([]byte, PublicKeySize)}); err == nil {
		t.Error("invalid id accepted")
	}
	if err := s.Put(Client{ID: testClientA, Platform: PlatformApple, PublicKey: make([]byte, 16)}); err == nil {
		t.Error("short key accepted")
	}
	if err := s.Put(Client{ID: testClientA, Platform: "ios", PublicKey: make([]byte, PublicKeySize)}); err == nil {
		t.Error("invalid platform accepted")
	}
	if err := s.Put(Client{ID: testClientA, Platform: PlatformApple, PublicKey: make([]byte, PublicKeySize), Platforms: []string{"watch"}}); err == nil {
		t.Error("invalid device platform accepted")
	}
}

func TestClientStoreRejectsCorruptFile(t *testing.T) {
	for name, content := range map[string]string{
		"not-json":    "{",
		"bad-version": `{"version":3,"clients":[]}`,
		"bad-key":     `{"version":2,"clients":[{"clientId":"` + testClientA + `","clientName":"x","platform":"apple","publicKey":"AAAA","pairedAt":"2026-11-18T17:00:00Z"}]}`,
		"no-key":      `{"version":2,"clients":[{"clientId":"` + testClientA + `","clientName":"x","platform":"apple","key":"` + EncodeB64U(make([]byte, 32)) + `","pairedAt":"2026-11-18T17:00:00Z"}]}`,
		"bad-id":      `{"version":2,"clients":[{"clientId":"x","clientName":"x","platform":"apple","publicKey":"` + EncodeB64U(make([]byte, 32)) + `","pairedAt":"2026-11-18T17:00:00Z"}]}`,
	} {
		path := filepath.Join(t.TempDir(), "clients.json")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenClientStore(path); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestClientStoreFailedWriteKeepsState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clients.json")
	s, _ := OpenClientStore(path)
	if err := s.Put(Client{ID: testClientA, Platform: PlatformApple, PublicKey: make([]byte, PublicKeySize)}); err != nil {
		t.Fatal(err)
	}
	// Block the temporary file so the next write fails.
	if err := os.Mkdir(path+".tmp", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(Client{ID: testClientB, Platform: PlatformApple, PublicKey: make([]byte, PublicKeySize)}); err == nil {
		t.Fatal("write error not reported")
	}
	if _, ok := s.Lookup(testClientB); ok {
		t.Fatal("memory changed although the write failed")
	}
	if _, err := s.Remove(testClientA); err == nil {
		t.Fatal("write error not reported on remove")
	}
	if _, ok := s.Lookup(testClientA); !ok {
		t.Fatal("client removed from memory although the write failed")
	}
}

func TestClientStoreRemoveAll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clients.json")
	s, err := OpenClientStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := s.RemoveAll(); n != 0 || err != nil {
		t.Fatalf("empty store: %d %v", n, err)
	}
	now := time.Date(2026, 11, 18, 17, 0, 0, 0, time.UTC)
	for _, id := range []string{testClientA, testClientB} {
		if err := s.Put(Client{ID: id, Name: "Phone", Platform: PlatformApple, PublicKey: bytes.Repeat([]byte{2}, PublicKeySize), PairedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := s.RemoveAll(); n != 2 || err != nil {
		t.Fatalf("RemoveAll = %d %v", n, err)
	}
	if len(s.List()) != 0 {
		t.Fatal("clients left in memory")
	}
	again, err := OpenClientStore(path)
	if err != nil || len(again.List()) != 0 {
		t.Fatalf("clients left on disk: %v", err)
	}
}

// M13 migration: an RL1 clients.json (version 1, shared HMAC keys) is
// replaced by an empty M13 file; only the count is reported.
func TestClientStoreWipesRL1Clients(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clients.json")
	key := EncodeB64U(bytes.Repeat([]byte{7}, 32))
	rl1 := `{"version":1,"clients":[` +
		`{"clientId":"` + testClientA + `","clientName":"iPhone","key":"` + key + `","pairedAt":"2026-11-18T17:00:00Z"},` +
		`{"clientId":"` + testClientB + `","clientName":"iPad","key":"` + key + `","pairedAt":"2026-11-18T17:00:00Z"}]}`
	if err := os.WriteFile(path, []byte(rl1), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := OpenClientStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.WipedRL1() != 2 || len(s.List()) != 0 {
		t.Fatalf("wiped %d, left %d", s.WipedRL1(), len(s.List()))
	}
	data, _ := os.ReadFile(path)
	if bytes.Contains(data, []byte(key)) || !bytes.Contains(data, []byte(`"version": 2`)) {
		t.Fatalf("RL1 keys left on the card:\n%s", data)
	}
	again, err := OpenClientStore(path)
	if err != nil || again.WipedRL1() != 0 || len(again.List()) != 0 {
		t.Fatalf("second open: %v %d", err, again.WipedRL1())
	}
}

func TestClientStoreNotePlatform(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clients.json")
	s, _ := OpenClientStore(path)
	if err := s.Put(Client{ID: testClientA, Name: "Thomas", Platform: PlatformApple, PublicKey: make([]byte, PublicKeySize), Platforms: []string{"ipad"}}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		p    string
		want bool
	}{{"iphone", true}, {"iphone", false}, {"ipad", false}, {"toaster", false}, {"mac", true}} {
		got, err := s.NotePlatform(testClientA, tc.p)
		if err != nil || got != tc.want {
			t.Errorf("NotePlatform(%q) = %v %v", tc.p, got, err)
		}
	}
	if added, err := s.NotePlatform(testClientB, "iphone"); added || err != nil {
		t.Errorf("unknown client: %v %v", added, err)
	}
	again, _ := OpenClientStore(path)
	c, _ := again.Lookup(testClientA)
	if strings.Join(c.Platforms, ",") != "iphone,ipad,mac" {
		t.Fatalf("platforms = %v", c.Platforms)
	}
}
