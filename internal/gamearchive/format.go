// Package gamearchive reads and writes the game archives of M19
// (docs/specs/04-api-v1.md, protocol/openapi.yaml GameArchiveManifest): a
// zip holding the data of one game (save, clock file, states and their
// thumbnails, cheats, art, optionally the ROM) and a manifest.json that
// gives the role of every file. An importer maps each file by its role to
// the names the target console uses, never by its path in the archive.
//
// Reading never trusts the archive (rule 2 of CLAUDE.md): the central
// directory is bounded before it is read, every entry is checked (path,
// type, case-insensitive duplicates, compression ratio, sizes), the
// manifest must list exactly the file entries of the archive, and every
// extracted byte is checked against the manifest's size and SHA-256.
package gamearchive

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

// Format and version of the manifest.
const (
	FormatName = "reminislink-game-archive"
	Version    = 1
	// ManifestName is the entry of the manifest, at the top of the archive.
	ManifestName = "manifest.json"
	// FileSuffix ends the name of an exported archive.
	FileSuffix = ".reminislink.zip"
)

// Roles of the files (ArchiveRole in protocol/openapi.yaml).
const (
	RoleSave           = "save"
	RoleRTC            = "rtc"
	RoleState          = "state"
	RoleStateThumbnail = "state-thumbnail"
	RoleCheats         = "cheats"
	RoleArt            = "art"
	RoleROM            = "rom"
)

// Limits of an archive (GET /archives/{stagingId} in protocol/openapi.yaml).
const (
	MaxEntries         = 512
	MaxManifestBytes   = 1 << 20
	MaxROMBytes        = 4 << 30
	MaxDataBytes       = 256 << 20
	MaxArtBytes        = 4 << 20
	MaxCheatsBytes     = 1 << 20
	MaxTotalBytes      = 5 << 30
	MaxSlot            = 99
	MaxRatio           = 200
	RatioMinBytes      = 1 << 20
	macOSFolder        = "__MACOSX"
	pngSignature       = "\x89PNG\r\n\x1a\n"
	maxSourceTextBytes = 1024
)

var sha256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Source says where an archive comes from (GameArchiveSource).
type Source struct {
	DeviceName  *string `json:"deviceName"`
	OS          string  `json:"os"`
	Platform    *string `json:"platform"`
	PakVersion  *string `json:"pakVersion"`
	SystemTag   string  `json:"systemTag"`
	SystemLabel string  `json:"systemLabel"`
	GameName    string  `json:"gameName"`
	FileName    string  `json:"fileName"`
	RomName     string  `json:"romName"`
	Kind        string  `json:"kind"`
}

// File is a file of the manifest (GameArchiveFile).
type File struct {
	Path      string `json:"path"`
	Role      string `json:"role"`
	Slot      *int   `json:"slot,omitempty"`
	SizeBytes int64  `json:"sizeBytes"`
	SHA256    string `json:"sha256"`
}

// Manifest is manifest.json (GameArchiveManifest).
type Manifest struct {
	Format    string    `json:"format"`
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"createdAt"`
	Source    Source    `json:"source"`
	Files     []File    `json:"files"`
}

// FormatError is a refused archive (422 unsupported_format); its text, in
// English, says why (never a path of the console).
type FormatError struct{ Reason string }

func (e *FormatError) Error() string { return e.Reason }

func refuse(format string, args ...any) error {
	return &FormatError{Reason: "Invalid game archive: " + fmt.Sprintf(format, args...)}
}

// IsFormatError reports whether err is a FormatError, and returns it.
func IsFormatError(err error) (*FormatError, bool) {
	var fe *FormatError
	ok := errors.As(err, &fe)
	return fe, ok
}

// maxBytes is the largest file of a role.
func maxBytes(role string) int64 {
	switch role {
	case RoleROM:
		return MaxROMBytes
	case RoleArt:
		return MaxArtBytes
	case RoleCheats:
		return MaxCheatsBytes
	}
	return MaxDataBytes
}

// slotted reports whether a role carries a slot.
func slotted(role string) bool { return role == RoleState || role == RoleStateThumbnail }

// knownRole reports whether role is one of the roles of version 1.
func knownRole(role string) bool {
	switch role {
	case RoleSave, RoleRTC, RoleState, RoleStateThumbnail, RoleCheats, RoleArt, RoleROM:
		return true
	}
	return false
}

// Key identifies the place of a file in a game: its role and slot.
func (f File) Key() string {
	if f.Slot != nil {
		return fmt.Sprintf("%s/%d", f.Role, *f.Slot)
	}
	return f.Role
}
