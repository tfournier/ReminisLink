package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
)

// ConfigFile is the single writer of config.json during a run: every
// change reads the file, changes some fields and writes it back
// atomically under one lock, keeping the fields it does not know.
type ConfigFile struct {
	path string
	mu   sync.Mutex
}

// NewConfigFile returns the writer of the config file at path.
func NewConfigFile(path string) *ConfigFile { return &ConfigFile{path: path} }

// Read returns the fields of the file (empty when it does not exist).
func (c *ConfigFile) Read() (map[string]json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.readLocked()
}

func (c *ConfigFile) readLocked() (map[string]json.RawMessage, error) {
	fields := map[string]json.RawMessage{}
	data, err := os.ReadFile(c.path)
	if errors.Is(err, fs.ErrNotExist) {
		return fields, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return fields, nil
}

// Update changes the fields with fn and writes the file atomically.
func (c *ConfigFile) Update(fn func(fields map[string]json.RawMessage) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	fields, err := c.readLocked()
	if err != nil {
		return err
	}
	if err := fn(fields); err != nil {
		return err
	}
	out, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	return WriteFileAtomic(c.path, append(out, '\n'), 0o644)
}

// Save writes the fields of cfg, keeping every other key of the file (the
// keys of CreditClock included). Settings changed during a run (milestone
// M7) must go through it.
func (c *ConfigFile) Save(cfg Config) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	var set map[string]json.RawMessage
	if err := json.Unmarshal(data, &set); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	return c.Update(func(fields map[string]json.RawMessage) error {
		for k, v := range set {
			fields[k] = v
		}
		return nil
	})
}
