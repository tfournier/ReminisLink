// Package device reads the state of the console: storage, network address
// (docs/specs/05-console-pak.md §6). Model, platform, OS version and
// battery come from the OS adapter.
package device

import (
	"fmt"
	"net"
	"sort"
)

// Storage is the space of a volume.
type Storage struct {
	TotalBytes int64
	FreeBytes  int64
}

// StorageOf returns the size and free space of the volume holding path
// (statfs; free space is what an unprivileged process can use).
func StorageOf(path string) (Storage, error) {
	total, free, err := statfs(path)
	if err != nil {
		return Storage{}, fmt.Errorf("statfs: %w", err)
	}
	return Storage{TotalBytes: clamp(total), FreeBytes: clamp(free)}, nil
}

func clamp(v uint64) int64 {
	if v > 1<<63-1 {
		return 1<<63 - 1
	}
	return int64(v)
}

// Iface is a network interface and its addresses.
type Iface struct {
	Name     string
	Up       bool
	Loopback bool
	Addrs    []net.IP
}

// Interfaces lists the network interfaces of the system.
func Interfaces() ([]Iface, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	out := make([]Iface, 0, len(ifs))
	for _, i := range ifs {
		it := Iface{Name: i.Name, Up: i.Flags&net.FlagUp != 0, Loopback: i.Flags&net.FlagLoopback != 0}
		addrs, err := i.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok {
				it.Addrs = append(it.Addrs, n.IP)
			}
		}
		out = append(out, it)
	}
	return out, nil
}

// PickIPv4 returns the IPv4 address to announce: the first one of the
// preferred interface if it has one, otherwise the first one of the other
// interfaces that are up (by name), loopback and link-local addresses
// excluded. ok is false when there is none (no Wi-Fi).
func PickIPv4(ifaces []Iface, preferred string) (ip string, ok bool) {
	sorted := append([]Iface(nil), ifaces...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if (sorted[i].Name == preferred) != (sorted[j].Name == preferred) {
			return sorted[i].Name == preferred
		}
		return sorted[i].Name < sorted[j].Name
	})
	for _, i := range sorted {
		if !i.Up || i.Loopback {
			continue
		}
		for _, a := range i.Addrs {
			v4 := a.To4()
			if v4 == nil || v4.IsLoopback() || v4.IsLinkLocalUnicast() || v4.IsUnspecified() {
				continue
			}
			return v4.String(), true
		}
	}
	return "", false
}

// IPv4On returns the IPv4 address of the named interface only (the Wi-Fi
// interface on the console, docs/specs/05-console-pak.md §2).
func IPv4On(name string) (string, bool) {
	ifaces, err := Interfaces()
	if err != nil {
		return "", false
	}
	return PickIPv4On(ifaces, name)
}

// PickIPv4On is PickIPv4 restricted to the interface called name.
func PickIPv4On(ifaces []Iface, name string) (string, bool) {
	var only []Iface
	for _, i := range ifaces {
		if i.Name == name {
			only = append(only, i)
		}
	}
	return PickIPv4(only, name)
}

// IPv4 returns the IPv4 address of the console, the one of the preferred
// interface first (the Wi-Fi interface given by the OS adapter).
func IPv4(preferred string) (string, bool) {
	ifaces, err := Interfaces()
	if err != nil {
		return "", false
	}
	return PickIPv4(ifaces, preferred)
}
