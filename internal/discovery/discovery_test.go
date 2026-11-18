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
	"github.com/miekg/dns"
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
	const instance = "ReminisLink v1.2 test"
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

func TestEscapeInstance(t *testing.T) {
	for in, want := range map[string]string{
		"Brick":           "Brick",
		"Brick v1.2":      `Brick\ v1\.2`,
		`a\b`:             `a\\b`,
		"(x);@\"y\"":      `\(x\)\;\@\"y\"`,
		"Thomas's Brické": `Thomas's\ Brick\195\169`,
	} {
		if got := EscapeInstance(in); got != want {
			t.Errorf("EscapeInstance(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"..Brick v1.2..": "Brick v1.2", " . ": "ReminisLink"} {
		if got := InstanceName(in); got != want {
			t.Errorf("InstanceName(%q) = %q, want %q", in, got, want)
		}
	}
}

// The escaped instance stays one label of the service, and decoding it
// gives back the exact string zeroconf registered (queries match it).
func TestEscapedInstanceRoundTrip(t *testing.T) {
	for _, name := range []string{"Brick v1.2", `back\slash.`, "Brické de Thomas (2)", "a;b@c\"d"} {
		inst := EscapeInstance(InstanceName(name))
		full := inst + "." + ServiceType + "." + Domain
		buf := make([]byte, 512)
		n, err := dns.PackDomainName(full, buf, 0, nil, false)
		if err != nil {
			t.Fatalf("%q: pack: %v", name, err)
		}
		back, _, err := dns.UnpackDomainName(buf[:n], 0)
		if err != nil || back != full {
			t.Fatalf("%q: round trip %q, want %q (%v)", name, back, full, err)
		}
		labels := dns.SplitDomainName(full)
		if len(labels) != 4 || labels[1] != "_reminislink" {
			t.Fatalf("%q: labels %q", name, labels)
		}
		if buf[0] != byte(len(InstanceName(name))) {
			t.Fatalf("%q: first label is %d bytes, want %d", name, buf[0], len(InstanceName(name)))
		}
	}
}
