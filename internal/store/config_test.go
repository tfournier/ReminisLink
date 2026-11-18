package store

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func defaultName(id string) string { return "Brick " + id[len(id)-4:] }

func TestNewUUIDv4(t *testing.T) {
	id, err := NewUUIDv4(bytes.NewReader(bytes.Repeat([]byte{0xff}, 16)))
	if err != nil || id != "ffffffff-ffff-4fff-bfff-ffffffffffff" {
		t.Fatalf("got %q, %v", id, err)
	}
	a, _ := NewUUIDv4(nil)
	b, _ := NewUUIDv4(nil)
	if !uuidRe.MatchString(a) || a == b {
		t.Fatalf("random ids %q %q", a, b)
	}
	if _, err := NewUUIDv4(bytes.NewReader(nil)); err == nil {
		t.Fatal("short source accepted")
	}
}

func TestLoadConfigCreatesThenKeeps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ReminisLink", "config.json")
	c, err := LoadConfig(path, defaultName, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !uuidRe.MatchString(c.DeviceID) || c.DeviceName != defaultName(c.DeviceID) || c.Port != DefaultPort || c.Version != 1 {
		t.Fatalf("created %+v", c)
	}
	again, err := LoadConfig(path, func(string) string { return "other" }, nil)
	if err != nil || again != c {
		t.Fatalf("reloaded %+v, %v; want %+v", again, err, c)
	}
}

func TestLoadConfigFillsMissingFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"deviceId":"7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10","language":"fr","future":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(path, defaultName, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.DeviceID != "7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10" || c.DeviceName != "Brick 3a10" || c.Port != DefaultPort || c.Language != "fr" {
		t.Fatalf("got %+v", c)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"deviceName": "Brick 3a10"`) {
		t.Fatalf("not saved: %s", data)
	}
}

func TestLoadConfigRefusesBadFiles(t *testing.T) {
	for _, content := range []string{`{`, `{"version":2}`, `{"version":1,"deviceId":"NOT-A-UUID"}`} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(path, defaultName, nil); err == nil {
			t.Errorf("%s accepted", content)
		}
		if data, _ := os.ReadFile(path); string(data) != content {
			t.Errorf("%s rewritten", content)
		}
	}
}

func TestLayout(t *testing.T) {
	l := Layout{Root: "/d"}
	if l.ConfigFile() != filepath.Join("/d", "config.json") || l.ClientsFile() != filepath.Join("/d", "clients.json") ||
		l.LocksDir("GBA") != filepath.Join("/d", "locks", "GBA") {
		t.Fatal(l)
	}
}

func TestConfigSavesKeepOtherKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(`{"version":1,"deviceId":"7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10","creditSeconds":42,"creditOpen":false,"future":{"a":1}}`), 0o644)
	c, err := LoadConfig(path, defaultName, nil) // fills the missing fields: merge
	if err != nil {
		t.Fatal(err)
	}
	c.DeviceName = "Renamed"
	if err := NewConfigFile(path).Save(c); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	for _, want := range []string{`"deviceName": "Renamed"`, `"creditSeconds": 42`, `"future"`, `"port": 47821`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("%s missing in %s", want, data)
		}
	}
}
