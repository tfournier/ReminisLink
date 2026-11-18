// Package discovery announces the pak on the local network with Bonjour
// (mDNS / DNS-SD, docs/specs/03-protocole.md §2). It uses the built-in
// responder of github.com/grandcat/zeroconf: avahi-daemon is not running on
// NextUI.
package discovery

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/grandcat/zeroconf"
)

const (
	// ServiceType is the DNS-SD service type of the pak.
	ServiceType = "_reminislink._tcp"
	// Domain is the mDNS domain.
	Domain = "local."
	// TXTVersion is the version of the TXT record format ("v").
	TXTVersion = 1
	// maxInstanceBytes is the DNS label limit of an instance name.
	maxInstanceBytes = 63
)

// Info is what the TXT record carries.
type Info struct {
	DeviceID   string // id
	APIVersion int    // api
	Model      string // model
	Platform   string // platform
	OS         string // os
	OSVersion  string // osv, omitted when unknown ("")
	PakVersion string // cv
}

// TXT returns the TXT record entries, in a fixed order. Values are ASCII:
// any other byte, and "=" in a value, is replaced by "_".
func TXT(info Info) []string {
	txt := []string{
		"v=" + strconv.Itoa(TXTVersion),
		"id=" + ascii(info.DeviceID),
		"api=" + strconv.Itoa(info.APIVersion),
		"model=" + ascii(info.Model),
		"platform=" + ascii(info.Platform),
		"os=" + ascii(info.OS),
	}
	if info.OSVersion != "" {
		txt = append(txt, "osv="+ascii(info.OSVersion))
	}
	return append(txt, "cv="+ascii(info.PakVersion))
}

func ascii(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c > 0x7e || c == '=' {
			c = '_'
		}
		b.WriteByte(c)
	}
	return b.String()
}

// InstanceName returns name cut to the 63-byte DNS label limit, on a rune
// boundary. An empty name gives "ReminisLink".
func InstanceName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "ReminisLink"
	}
	for len(name) > maxInstanceBytes {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	return name
}

// Announcement is a running Bonjour announcement.
type Announcement struct {
	server *zeroconf.Server
}

// Announce registers the service on the given interfaces (nil: every
// multicast interface) until Shutdown.
func Announce(name string, port int, info Info, ifaces []net.Interface) (*Announcement, error) {
	server, err := zeroconf.Register(InstanceName(name), ServiceType, Domain, port, TXT(info), ifaces)
	if err != nil {
		return nil, fmt.Errorf("bonjour: %w", err)
	}
	return &Announcement{server: server}, nil
}

// Shutdown withdraws the announcement (goodbye packets).
func (a *Announcement) Shutdown() {
	if a != nil && a.server != nil {
		a.server.Shutdown()
		a.server = nil
	}
}
