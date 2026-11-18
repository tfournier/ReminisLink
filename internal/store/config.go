package store

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
)

const (
	// DefaultPort is the preferred port of the API (docs/specs/03-protocole.md §1).
	DefaultPort = 47821
	// configVersion is the format version of config.json.
	configVersion = 1
)

// Config is config.json:
//
//	{ "version": 1, "deviceId": "<uuid v4>", "deviceName": "Brick 3a10", "port": 47821, "language": "fr" }
type Config struct {
	Version    int    `json:"version"`
	DeviceID   string `json:"deviceId"`
	DeviceName string `json:"deviceName"`
	Port       int    `json:"port"`
	Language   string `json:"language,omitempty"`
}

// The keys of config.json managed by CreditClock (creditSeconds,
// creditWall, creditOpen) are not in Config: only CreditClock reads and
// writes them, through ConfigFile.Update.

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// NewUUIDv4 returns a random lowercase UUID v4 read from r (nil: crypto/rand).
func NewUUIDv4(r io.Reader) (string, error) {
	if r == nil {
		r = rand.Reader
	}
	var b [16]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return "", fmt.Errorf("uuid: %w", err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// LoadConfig reads config.json, creating it on first launch: a new device
// id (UUID v4), the name given by defaultName(deviceID) and DefaultPort.
// Missing fields of an existing file are filled the same way and saved. A
// file that cannot be parsed is an error and is left untouched.
// random is the source of the device id (nil: crypto/rand).
func LoadConfig(path string, defaultName func(deviceID string) string, random io.Reader) (Config, error) {
	var c Config
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		c.Version = configVersion
	case err != nil:
		return Config{}, fmt.Errorf("read config: %w", err)
	default:
		if err := json.Unmarshal(data, &c); err != nil {
			return Config{}, fmt.Errorf("parse %s: %w", path, err)
		}
		if c.Version != configVersion {
			return Config{}, fmt.Errorf("parse %s: unsupported version %d", path, c.Version)
		}
		if c.DeviceID != "" && !uuidRe.MatchString(c.DeviceID) {
			return Config{}, fmt.Errorf("parse %s: invalid deviceId %q", path, c.DeviceID)
		}
	}
	changed := data == nil
	if c.DeviceID == "" {
		if c.DeviceID, err = NewUUIDv4(random); err != nil {
			return Config{}, err
		}
		changed = true
	}
	if c.DeviceName == "" {
		c.DeviceName = defaultName(c.DeviceID)
		changed = true
	}
	if c.Port <= 0 || c.Port > 65535 {
		c.Port = DefaultPort
		changed = true
	}
	if changed {
		if err := SaveConfig(path, c); err != nil {
			return Config{}, err
		}
	}
	return c, nil
}

// SaveConfig writes the fields of c into config.json (read, merge, atomic
// write): the keys Config does not know are kept.
func SaveConfig(path string, c Config) error {
	if err := NewConfigFile(path).Save(c); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}
