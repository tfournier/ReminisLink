package discovery

import (
	"context"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/grandcat/zeroconf"
)

func TestTXT(t *testing.T) {
	info := Info{
		DeviceID: "7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10", APIVersion: 1, Model: "brick",
		Platform: "tg5040", OS: "nextui", OSVersion: "6.14.0", PakVersion: "1.0.0",
	}
	want := []string{"v=1", "id=7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10", "api=1", "model=brick",
		"platform=tg5040", "os=nextui", "osv=6.14.0", "cv=1.0.0"}
	if got := TXT(info); !reflect.DeepEqual(got, want) {
		t.Errorf("TXT = %q", got)
	}
	info.OSVersion = ""
	info.PakVersion = "dév=1\n"
	got := TXT(info)
	if len(got) != 7 || got[6] != "cv=d__v_1_" {
		t.Errorf("TXT without osv = %q", got)
	}
}

func TestInstanceName(t *testing.T) {
	if InstanceName("  ") != "ReminisLink" || InstanceName("Brick de Thomas") != "Brick de Thomas" {
		t.Error("simple names")
	}
	long := strings.Repeat("é", 40) // 80 bytes
	got := InstanceName(long)
	if len(got) != 62 || got != strings.Repeat("é", 31) {
		t.Errorf("cut name = %q (%d bytes)", got, len(got))
	}
}

// TestAnnounceAndBrowse needs multicast on the local network: it only runs
// with RL_MDNS_TEST=1. On macOS it browses with dns-sd (the system
// responder owns port 5353, an in-process browser does not see the
// answers), elsewhere with the zeroconf resolver.
func TestAnnounceAndBrowse(t *testing.T) {
	if os.Getenv("RL_MDNS_TEST") != "1" {
		t.Skip("set RL_MDNS_TEST=1 to test the real Bonjour announcement")
	}
	info := Info{DeviceID: "00000000-0000-4000-8000-000000000001", APIVersion: 1, Model: "desktop", Platform: "tg5040", OS: "nextui", PakVersion: "test"}
	const instance = "ReminisLink test"
	a, err := Announce(instance, 47899, info, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if runtime.GOOS == "darwin" {
		out, _ := exec.CommandContext(ctx, "dns-sd", "-L", instance, ServiceType, Domain).Output()
		if !strings.Contains(string(out), ":47899") || !strings.Contains(string(out), "id="+info.DeviceID) {
			t.Fatalf("dns-sd -L did not resolve the announcement:\n%s", out)
		}
		return
	}
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		t.Fatal(err)
	}
	entries := make(chan *zeroconf.ServiceEntry)
	if err := resolver.Browse(ctx, ServiceType, Domain, entries); err != nil {
		t.Fatal(err)
	}
	for e := range entries {
		if e.Instance == instance && e.Port == 47899 && reflect.DeepEqual(e.Text, TXT(info)) {
			return
		}
	}
	t.Fatal("announcement not found")
}
