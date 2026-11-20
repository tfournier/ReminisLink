package auth

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/tfournier/ReminisLink/internal/store"
)

// Client platforms of POST /pair (ClientPlatform in protocol/openapi.yaml).
const (
	PlatformApple   = "apple"
	PlatformAndroid = "android"
	PlatformWeb     = "web"
	PlatformOther   = "other"
)

// ValidPlatform reports whether p is a ClientPlatform.
func ValidPlatform(p string) bool {
	switch p {
	case PlatformApple, PlatformAndroid, PlatformWeb, PlatformOther:
		return true
	}
	return false
}

// HeaderPlatform is the informative, unsigned header naming the kind of
// device that sends a request (M13).
const HeaderPlatform = "X-RL-Platform"

// devicePlatforms are the accepted values of X-RL-Platform, in display
// order.
var devicePlatforms = []string{"iphone", "ipad", "mac", "android", "web"}

// ValidDevicePlatform reports whether p is an accepted X-RL-Platform value.
func ValidDevicePlatform(p string) bool { return slices.Contains(devicePlatforms, p) }

// Client is a paired client (an app installation, or an Apple identity
// shared by the devices of one iCloud account).
type Client struct {
	ID        string
	Name      string
	Platform  string // ClientPlatform: apple, android, web, other
	PublicKey ed25519.PublicKey
	// Platforms are the X-RL-Platform values seen for this client (iphone,
	// ipad, mac...), in display order.
	Platforms []string
	PairedAt  time.Time
	// Epoch is set by a ClientLookup that versions its clients (the M10
	// access generation of internal/server), read in the same step as the
	// key, so that a Gate can tell whether the pairing that verified the
	// request is still the current one. Always 0 from ClientStore.
	Epoch uint64
}

func (c Client) clone() Client {
	c.PublicKey = append(ed25519.PublicKey(nil), c.PublicKey...)
	c.Platforms = slices.Clone(c.Platforms)
	return c
}

// clientsFileVersion is the format version of clients.json (2: M13, public
// keys; 1 held the HMAC keys of RL1).
const clientsFileVersion = 2

// clients.json:
//
//	{
//	  "version": 2,
//	  "clients": [
//	    { "clientId": "...", "clientName": "...", "platform": "apple",
//	      "publicKey": "<b64u>", "platforms": ["iphone"], "pairedAt": "2026-11-18T17:00:00Z" }
//	  ]
//	}
//
// Only public keys: a copy of the SD card cannot sign for a client.
type clientsFile struct {
	Version int            `json:"version"`
	Clients []clientRecord `json:"clients"`
}

type clientRecord struct {
	ClientID   string    `json:"clientId"`
	ClientName string    `json:"clientName"`
	Platform   string    `json:"platform"`
	PublicKey  string    `json:"publicKey"`
	Platforms  []string  `json:"platforms"`
	PairedAt   time.Time `json:"pairedAt"`
}

// ClientStore keeps the paired clients in memory and in clients.json. Every
// change is written atomically before it becomes visible.
type ClientStore struct {
	mu      sync.RWMutex
	path    string
	clients map[string]Client
	wiped   int
}

// OpenClientStore loads path. A missing file is an empty store. A file of
// the RL1 format (version 1: shared HMAC keys) is replaced by an empty
// store at once (M13 migration); WipedRL1 tells how many clients it held.
func OpenClientStore(path string) (*ClientStore, error) {
	s := &ClientStore{path: path, clients: make(map[string]Client)}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read clients: %w", err)
	}
	var f struct {
		Version int               `json:"version"`
		Clients []json.RawMessage `json:"clients"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	switch f.Version {
	case 1:
		// RL1 clients hold a shared key the M13 pak no longer accepts:
		// they must pair again. Never log the keys, only the count.
		if err := s.saveLocked(map[string]Client{}); err != nil {
			return nil, err
		}
		s.wiped = len(f.Clients)
		return s, nil
	case clientsFileVersion:
	default:
		return nil, fmt.Errorf("parse %s: unsupported version %d", path, f.Version)
	}
	for _, raw := range f.Clients {
		var r clientRecord
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		if !ValidID(r.ClientID) {
			return nil, fmt.Errorf("parse %s: invalid client id %q", path, r.ClientID)
		}
		pub, err := DecodePublicKey(r.PublicKey)
		if err != nil {
			return nil, fmt.Errorf("parse %s: client %q has an invalid public key", path, r.ClientID)
		}
		if !ValidPlatform(r.Platform) {
			r.Platform = PlatformOther
		}
		var platforms []string
		for _, p := range devicePlatforms {
			if slices.Contains(r.Platforms, p) {
				platforms = append(platforms, p)
			}
		}
		s.clients[r.ClientID] = Client{ID: r.ClientID, Name: r.ClientName, Platform: r.Platform, PublicKey: pub,
			Platforms: platforms, PairedAt: r.PairedAt.UTC()}
	}
	return s, nil
}

// WipedRL1 returns how many RL1 clients OpenClientStore erased (0 when the
// file was already in the M13 format).
func (s *ClientStore) WipedRL1() int { return s.wiped }

// Lookup returns the client with this id.
func (s *ClientStore) Lookup(id string) (Client, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.clients[id]
	if ok {
		c = c.clone()
	}
	return c, ok
}

// List returns the clients sorted by id.
func (s *ClientStore) List() []Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Client, 0, len(s.clients))
	for _, c := range s.clients {
		out = append(out, c.clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Put adds a client or replaces the one with the same id (re-pairing
// replaces the previous public key, platform and seen platforms).
func (s *ClientStore) Put(c Client) error {
	if !ValidID(c.ID) {
		return fmt.Errorf("put client: invalid id %q", c.ID)
	}
	if len(c.PublicKey) != PublicKeySize {
		return fmt.Errorf("put client: public key must be %d bytes", PublicKeySize)
	}
	if !ValidPlatform(c.Platform) {
		return fmt.Errorf("put client: invalid platform %q", c.Platform)
	}
	for _, p := range c.Platforms {
		if !ValidDevicePlatform(p) {
			return fmt.Errorf("put client: invalid device platform %q", p)
		}
	}
	c = c.clone()
	c.Epoch = 0
	c.PairedAt = c.PairedAt.UTC().Truncate(time.Second)
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make(map[string]Client, len(s.clients)+1)
	for id, v := range s.clients {
		next[id] = v
	}
	next[c.ID] = c
	if err := s.saveLocked(next); err != nil {
		return err
	}
	s.clients = next
	return nil
}

// NotePlatform records that client id sent a request with the device
// platform p (X-RL-Platform). Only a platform not yet recorded changes
// clients.json. It reports whether it was added; an unknown client or an
// invalid platform changes nothing.
func (s *ClientStore) NotePlatform(id, p string) (bool, error) {
	if !ValidDevicePlatform(p) {
		return false, nil
	}
	s.mu.RLock()
	c, ok := s.clients[id]
	known := ok && slices.Contains(c.Platforms, p)
	s.mu.RUnlock()
	if !ok || known {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok = s.clients[id]
	if !ok || slices.Contains(c.Platforms, p) {
		return false, nil
	}
	c = c.clone()
	var platforms []string
	for _, v := range devicePlatforms {
		if v == p || slices.Contains(c.Platforms, v) {
			platforms = append(platforms, v)
		}
	}
	c.Platforms = platforms
	next := make(map[string]Client, len(s.clients))
	for k, v := range s.clients {
		next[k] = v
	}
	next[id] = c
	if err := s.saveLocked(next); err != nil {
		return false, err
	}
	s.clients = next
	return true, nil
}

// Remove deletes a client. It reports whether the client existed.
func (s *ClientStore) Remove(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.clients[id]; !ok {
		return false, nil
	}
	next := make(map[string]Client, len(s.clients))
	for k, v := range s.clients {
		if k != id {
			next[k] = v
		}
	}
	if err := s.saveLocked(next); err != nil {
		return false, err
	}
	s.clients = next
	return true, nil
}

// RemoveAll deletes every client and returns how many there were.
func (s *ClientStore) RemoveAll() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.clients)
	if n == 0 {
		return 0, nil
	}
	next := map[string]Client{}
	if err := s.saveLocked(next); err != nil {
		return 0, err
	}
	s.clients = next
	return n, nil
}

func (s *ClientStore) saveLocked(clients map[string]Client) error {
	f := clientsFile{Version: clientsFileVersion, Clients: make([]clientRecord, 0, len(clients))}
	for _, c := range clients {
		platforms := c.Platforms
		if platforms == nil {
			platforms = []string{}
		}
		f.Clients = append(f.Clients, clientRecord{
			ClientID: c.ID, ClientName: c.Name, Platform: c.Platform, PublicKey: EncodeB64U(c.PublicKey),
			Platforms: platforms, PairedAt: c.PairedAt,
		})
	}
	sort.Slice(f.Clients, func(i, j int) bool { return f.Clients[i].ClientID < f.Clients[j].ClientID })
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("encode clients: %w", err)
	}
	data = append(data, '\n')
	if err := store.WriteFileAtomic(s.path, data, 0o600); err != nil {
		return fmt.Errorf("save clients: %w", err)
	}
	return nil
}
