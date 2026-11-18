package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/tfournier/ReminisLink/internal/store"
)

// Client is a paired installation of the app.
type Client struct {
	ID       string
	Name     string
	Key      []byte
	PairedAt time.Time
}

// clientsFileVersion is the format version of clients.json.
const clientsFileVersion = 1

// clients.json:
//
//	{
//	  "version": 1,
//	  "clients": [
//	    { "clientId": "...", "clientName": "...", "key": "<b64u>", "pairedAt": "2026-11-18T17:00:00Z" }
//	  ]
//	}
//
// The keys are stored in clear: the SD card is exFAT and has no permissions
// (accepted risk, docs/specs/05-console-pak.md §4).
type clientsFile struct {
	Version int            `json:"version"`
	Clients []clientRecord `json:"clients"`
}

type clientRecord struct {
	ClientID   string    `json:"clientId"`
	ClientName string    `json:"clientName"`
	Key        string    `json:"key"`
	PairedAt   time.Time `json:"pairedAt"`
}

// ClientStore keeps the paired clients in memory and in clients.json. Every
// change is written atomically before it becomes visible.
type ClientStore struct {
	mu      sync.RWMutex
	path    string
	clients map[string]Client
}

// OpenClientStore loads path. A missing file is an empty store.
func OpenClientStore(path string) (*ClientStore, error) {
	s := &ClientStore{path: path, clients: make(map[string]Client)}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read clients: %w", err)
	}
	var f clientsFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if f.Version != clientsFileVersion {
		return nil, fmt.Errorf("parse %s: unsupported version %d", path, f.Version)
	}
	for _, r := range f.Clients {
		key, err := DecodeB64U(r.Key)
		if err != nil || len(key) != KeySize {
			return nil, fmt.Errorf("parse %s: client %q has an invalid key", path, r.ClientID)
		}
		if !ValidID(r.ClientID) {
			return nil, fmt.Errorf("parse %s: invalid client id %q", path, r.ClientID)
		}
		s.clients[r.ClientID] = Client{ID: r.ClientID, Name: r.ClientName, Key: key, PairedAt: r.PairedAt.UTC()}
	}
	return s, nil
}

// Lookup returns the client with this id.
func (s *ClientStore) Lookup(id string) (Client, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.clients[id]
	if ok {
		c.Key = append([]byte(nil), c.Key...)
	}
	return c, ok
}

// List returns the clients sorted by id.
func (s *ClientStore) List() []Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Client, 0, len(s.clients))
	for _, c := range s.clients {
		c.Key = append([]byte(nil), c.Key...)
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Put adds a client or replaces the one with the same id (re-pairing
// replaces the previous key).
func (s *ClientStore) Put(c Client) error {
	if !ValidID(c.ID) {
		return fmt.Errorf("put client: invalid id %q", c.ID)
	}
	if len(c.Key) != KeySize {
		return fmt.Errorf("put client: key must be %d bytes", KeySize)
	}
	c.Key = append([]byte(nil), c.Key...)
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

func (s *ClientStore) saveLocked(clients map[string]Client) error {
	f := clientsFile{Version: clientsFileVersion, Clients: make([]clientRecord, 0, len(clients))}
	for _, c := range clients {
		f.Clients = append(f.Clients, clientRecord{
			ClientID: c.ID, ClientName: c.Name, Key: EncodeB64U(c.Key), PairedAt: c.PairedAt,
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
