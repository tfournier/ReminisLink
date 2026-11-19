package device

import (
	"net"
	"testing"
)

func TestStorageOf(t *testing.T) {
	s, err := StorageOf(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if s.TotalBytes <= 0 || s.FreeBytes < 0 || s.FreeBytes > s.TotalBytes {
		t.Fatalf("storage = %+v", s)
	}
	if _, err := StorageOf("/does/not/exist/at/all"); err == nil {
		t.Fatal("missing path accepted")
	}
}

func TestPickIPv4(t *testing.T) {
	ip := net.ParseIP
	ifaces := []Iface{
		{Name: "lo", Up: true, Loopback: true, Addrs: []net.IP{ip("127.0.0.1")}},
		{Name: "eth0", Up: true, Addrs: []net.IP{ip("fe80::1"), ip("169.254.3.4"), ip("10.0.0.5")}},
		{Name: "wlan0", Up: true, Addrs: []net.IP{ip("fe80::2"), ip("192.168.1.42")}},
		{Name: "aaa", Up: false, Addrs: []net.IP{ip("172.16.0.1")}},
	}
	if got, ok := PickIPv4(ifaces, "wlan0"); !ok || got != "192.168.1.42" {
		t.Errorf("preferred: %q %v", got, ok)
	}
	if got, ok := PickIPv4(ifaces, "wlan9"); !ok || got != "10.0.0.5" {
		t.Errorf("fallback: %q %v", got, ok)
	}
	if got, ok := PickIPv4(ifaces[:1], "wlan0"); ok {
		t.Errorf("loopback only: %q", got)
	}
	if _, ok := PickIPv4(nil, "wlan0"); ok {
		t.Error("no interface")
	}
}

func TestIPv4DoesNotPanic(t *testing.T) {
	ip, ok := IPv4("wlan0")
	if ok && net.ParseIP(ip).To4() == nil {
		t.Fatalf("not an IPv4: %q", ip)
	}
}

func TestPickIPv4On(t *testing.T) {
	ip := net.ParseIP
	ifaces := []Iface{
		{Name: "lo", Up: true, Loopback: true, Addrs: []net.IP{ip("127.0.0.1")}},
		{Name: "usb0", Up: true, Addrs: []net.IP{ip("10.0.0.5")}},
		{Name: "wlan0", Up: true, Addrs: []net.IP{ip("fe80::2"), ip("192.168.1.42")}},
	}
	if got, ok := PickIPv4On(ifaces, "wlan0"); !ok || got != "192.168.1.42" {
		t.Errorf("wlan0: %q %v", got, ok)
	}
	// No fallback to another interface: no Wi-Fi.
	if got, ok := PickIPv4On(ifaces[:2], "wlan0"); ok {
		t.Errorf("without wlan0: %q", got)
	}
	down := []Iface{{Name: "wlan0", Up: false, Addrs: []net.IP{ip("192.168.1.42")}}}
	if got, ok := PickIPv4On(down, "wlan0"); ok {
		t.Errorf("wlan0 down: %q", got)
	}
}
