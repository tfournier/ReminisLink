package auth

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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
	keyA := bytes.Repeat([]byte{1}, KeySize)
	if err := s.Put(Client{ID: testClientA, Name: "iPhone de Thomas", Key: keyA, PairedAt: paired}); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(Client{ID: testClientB, Name: "iPad", Key: bytes.Repeat([]byte{2}, KeySize), PairedAt: paired}); err != nil {
		t.Fatal(err)
	}

	// Reload from disk.
	s2, err := OpenClientStore(path)
	if err != nil {
		t.Fatal(err)
	}
	c, ok := s2.Lookup(testClientA)
	if !ok || c.Name != "iPhone de Thomas" || !bytes.Equal(c.Key, keyA) || !c.PairedAt.Equal(paired) {
		t.Fatalf("reloaded client = %+v, %v", c, ok)
	}
	if got := s2.List(); len(got) != 2 || got[0].ID != testClientA {
		t.Fatalf("List = %+v", got)
	}

	// Re-pairing replaces the key.
	keyA2 := bytes.Repeat([]byte{3}, KeySize)
	if err := s2.Put(Client{ID: testClientA, Name: "iPhone", Key: keyA2, PairedAt: paired.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if c, _ := s2.Lookup(testClientA); !bytes.Equal(c.Key, keyA2) || c.Name != "iPhone" {
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
	key := bytes.Repeat([]byte{0xff}, KeySize)
	if err := s.Put(Client{ID: testClientA, Name: "iPhone", Key: key, PairedAt: time.Date(2026, 11, 18, 17, 0, 0, 0, time.UTC)}); err != nil {
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
	if raw["version"] != float64(1) || c["clientId"] != testClientA || c["key"] != EncodeB64U(key) || c["pairedAt"] != "2026-11-18T17:00:00Z" {
		t.Fatalf("unexpected file:\n%s", data)
	}
}

func TestClientStoreLookupReturnsCopy(t *testing.T) {
	s, _ := OpenClientStore(filepath.Join(t.TempDir(), "clients.json"))
	if err := s.Put(Client{ID: testClientA, Key: bytes.Repeat([]byte{1}, KeySize)}); err != nil {
		t.Fatal(err)
	}
	c, _ := s.Lookup(testClientA)
	c.Key[0] = 0
	if c2, _ := s.Lookup(testClientA); c2.Key[0] != 1 {
		t.Fatal("Lookup exposes the internal key slice")
	}
}

func TestClientStoreRejectsBadInput(t *testing.T) {
	s, _ := OpenClientStore(filepath.Join(t.TempDir(), "clients.json"))
	if err := s.Put(Client{ID: "not-a-uuid", Key: make([]byte, KeySize)}); err == nil {
		t.Error("invalid id accepted")
	}
	if err := s.Put(Client{ID: testClientA, Key: make([]byte, 16)}); err == nil {
		t.Error("short key accepted")
	}
}

func TestClientStoreRejectsCorruptFile(t *testing.T) {
	for name, content := range map[string]string{
		"not-json":    "{",
		"bad-version": `{"version":2,"clients":[]}`,
		"bad-key":     `{"version":1,"clients":[{"clientId":"` + testClientA + `","clientName":"x","key":"AAAA","pairedAt":"2026-11-18T17:00:00Z"}]}`,
		"bad-id":      `{"version":1,"clients":[{"clientId":"x","clientName":"x","key":"` + EncodeB64U(make([]byte, 32)) + `","pairedAt":"2026-11-18T17:00:00Z"}]}`,
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
	if err := s.Put(Client{ID: testClientA, Key: make([]byte, KeySize)}); err != nil {
		t.Fatal(err)
	}
	// Block the temporary file so the next write fails.
	if err := os.Mkdir(path+".tmp", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(Client{ID: testClientB, Key: make([]byte, KeySize)}); err == nil {
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
		if err := s.Put(Client{ID: id, Name: "Phone", Key: bytes.Repeat([]byte{2}, KeySize), PairedAt: now}); err != nil {
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
