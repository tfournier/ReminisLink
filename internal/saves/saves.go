// Package saves manages the in-game saves of the games and their backup
// copies (docs/specs/04-api-v1.md, Saves; docs/specs/05-console-pak.md
// §5.1).
//
// A backup is two files in backups/<TAG>/<ROM file name>/saves/ of the
// ReminisLink folder: <backupId>.sav, the copy, and <backupId>.json, its
// metadata (label, reason, date, size, SHA-256). The copy is written first
// (atomically), the metadata second: after a power loss a backup is either
// complete or absent from the list (a copy without metadata is never
// listed and never used).
//
// Order of the backups: each backup gets a sequence number, one more than
// the largest of the game. The list and the automatic limit use it, never
// the dates, which come from the console clock and are informative only.
package saves

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/store"
)

// Backup reasons (Backup.reason in protocol/openapi.yaml).
const (
	ReasonManual        = "manual"
	ReasonBeforeReplace = "before-replace"
	ReasonBeforeRestore = "before-restore"
	ReasonBeforeDelete  = "before-delete"
	ReasonBeforePatch   = "before-patch"
)

const (
	// MaxLabelRunes is the longest backup label.
	MaxLabelRunes = 80
	// MaxAutoBackups is the number of automatic backups (before-*) kept
	// per game; older ones go to the trash (05 §5.1).
	MaxAutoBackups = 10
	// LabelBeforeReplace is the label of the backup made before a save is
	// replaced (docs/specs/04-api-v1.md, PUT /games/{gameId}/save).
	LabelBeforeReplace = "Avant remplacement"

	metaVersion = 1
	savesDir    = "saves"
	copyExt     = ".sav"
	rtcExt      = ".rtc"
	metaExt     = ".json"
	// tempPrefix names the temporary files written in the uploads folder;
	// leftovers are deleted at start with the other orphan files there.
	tempPrefix = "sv_"
)

// Errors of the package.
var (
	ErrNoSave          = errors.New("the game has no save")
	ErrBackupNotFound  = errors.New("save backup not found")
	ErrInvalidBackupID = errors.New("invalid save backup id")
	ErrInvalidLabel    = errors.New("invalid label")
	ErrCorruptBackup   = errors.New("save backup does not match its checksum")
)

var backupIDRe = regexp.MustCompile(`^bk_\d{8}T\d{6}Z_[0-9a-f]{4}$`)

// ValidBackupID reports whether id has the form of a backup id.
func ValidBackupID(id string) bool { return backupIDRe.MatchString(id) }

// Backup is a save backup copy (Backup in protocol/openapi.yaml).
type Backup struct {
	ID        string    `json:"id"`
	Label     *string   `json:"label"`
	CreatedAt time.Time `json:"createdAt"`
	SizeBytes int64     `json:"sizeBytes"`
	Reason    string    `json:"reason"`
}

// meta is the content of <backupId>.json.
type meta struct {
	Version   int       `json:"version"`
	ID        string    `json:"id"`
	Seq       int64     `json:"seq"`
	Label     *string   `json:"label"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"createdAt"`
	SizeBytes int64     `json:"sizeBytes"`
	SHA256    string    `json:"sha256"`
	// RTCSHA256 is set when the clock file of the game (.rtc, written by
	// NextUI with the save) was copied with it, as <backupId>.rtc.
	RTCSHA256 string `json:"rtcSha256,omitempty"`
}

func (m meta) public() Backup {
	return Backup{ID: m.ID, Label: m.Label, CreatedAt: m.CreatedAt, SizeBytes: m.SizeBytes, Reason: m.Reason}
}

var validReasons = map[string]bool{
	ReasonManual: true, ReasonBeforeReplace: true, ReasonBeforeRestore: true, ReasonBeforeDelete: true, ReasonBeforePatch: true,
}

// Options are the dependencies of a Service.
type Options struct {
	Adapter osadapter.Adapter
	Layout  store.Layout
	Trash   *store.Trash
	// Lock serializes every change of the SD card files (shared with the
	// uploads, the states and the cheats). nil: a lock of its own.
	Lock   *sync.Mutex
	Now    func() time.Time // nil: time.Now (dates and ids only)
	Logger *slog.Logger     // nil: slog.Default()
	Rand   io.Reader        // nil: crypto/rand
}

// Service manages the saves and their backups.
type Service struct {
	o Options
}

// New returns a Service.
func New(o Options) (*Service, error) {
	if o.Adapter == nil || o.Trash == nil || o.Layout.Root == "" {
		return nil, errors.New("saves: missing dependency")
	}
	if o.Lock == nil {
		o.Lock = &sync.Mutex{}
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Rand == nil {
		o.Rand = rand.Reader
	}
	return &Service{o: o}, nil
}

// CheckLabel validates an optional label: at most MaxLabelRunes characters
// of valid UTF-8 without control characters. An empty label is no label.
func CheckLabel(label *string) (*string, error) {
	if label == nil || *label == "" {
		return nil, nil
	}
	l := *label
	if !utf8.ValidString(l) || utf8.RuneCountInString(l) > MaxLabelRunes {
		return nil, ErrInvalidLabel
	}
	for _, r := range l {
		if unicode.IsControl(r) {
			return nil, ErrInvalidLabel
		}
	}
	return &l, nil
}

// dir returns the backup folder of a game (an existing folder whose name
// differs only by case is reused: exFAT ignores case).
func (s *Service) dir(g osadapter.Game) string {
	tagDir := s.o.Layout.BackupsDir(g.System.Tag)
	romDir := store.FindFold(tagDir, g.ROMName, true)
	if romDir == "" {
		romDir = filepath.Join(tagDir, g.ROMName)
	}
	if d := store.FindFold(romDir, savesDir, true); d != "" {
		return d
	}
	return filepath.Join(romDir, savesDir)
}

// SaveFile returns the current save of g, or ErrNoSave.
func (s *Service) SaveFile(g osadapter.Game) (osadapter.File, error) {
	files, err := s.o.Adapter.GameFiles(g)
	if err != nil {
		return osadapter.File{}, fmt.Errorf("save: %w", err)
	}
	if files.Save == nil {
		return osadapter.File{}, ErrNoSave
	}
	return *files.Save, nil
}

// readMetas returns the complete backups of a folder (metadata and copy
// present), newest (largest sequence) first.
func (s *Service) readMetas(dir string) ([]meta, error) {
	des, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("save backups: %w", err)
	}
	var out []meta
	for _, d := range des {
		id, ok := strings.CutSuffix(d.Name(), metaExt)
		if !ok || !ValidBackupID(id) || !d.Type().IsRegular() {
			continue
		}
		m, err := readMeta(dir, id)
		if err != nil {
			s.o.Logger.Warn("save backups: unreadable backup", "backup", id, "error", err)
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Seq != out[j].Seq {
			return out[i].Seq > out[j].Seq
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

func readMeta(dir, id string) (meta, error) {
	var m meta
	data, err := os.ReadFile(filepath.Join(dir, id+metaExt))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, err
	}
	if m.Version != metaVersion || m.ID != id || !validReasons[m.Reason] {
		return m, errors.New("invalid metadata")
	}
	fi, err := os.Lstat(filepath.Join(dir, id+copyExt))
	if err != nil || !fi.Mode().IsRegular() {
		return m, errors.New("copy missing")
	}
	return m, nil
}

// List returns the backups of g, newest first.
func (s *Service) List(g osadapter.Game) ([]Backup, error) {
	s.o.Lock.Lock()
	defer s.o.Lock.Unlock()
	metas, err := s.readMetas(s.dir(g))
	if err != nil {
		return nil, err
	}
	out := make([]Backup, 0, len(metas))
	for _, m := range metas {
		out = append(out, m.public())
	}
	return out, nil
}

// Create backs up the current save of g ("duplicate the save"): a manual
// backup. ErrNoSave when there is no save.
func (s *Service) Create(g osadapter.Game, label *string) (Backup, error) {
	label, err := CheckLabel(label)
	if err != nil {
		return Backup{}, err
	}
	s.o.Lock.Lock()
	defer s.o.Lock.Unlock()
	files, err := s.o.Adapter.GameFiles(g)
	if err != nil {
		return Backup{}, fmt.Errorf("save: %w", err)
	}
	if files.Save == nil {
		return Backup{}, ErrNoSave
	}
	m, err := s.createLocked(g, files.Save.Path, rtcPath(files), ReasonManual, label)
	if err != nil {
		return Backup{}, err
	}
	return m.public(), nil
}

// rtcPath returns the clock file of the game, "" when it has none.
func rtcPath(files osadapter.GameFiles) string {
	if files.RTC == nil {
		return ""
	}
	return files.RTC.Path
}

// createLocked copies src (and the clock file rtc, if not "") as a new
// backup of g. Automatic backups past MaxAutoBackups go to the trash.
// Called with the lock held.
func (s *Service) createLocked(g osadapter.Game, src, rtc, reason string, label *string) (meta, error) {
	dir := s.dir(g)
	metas, err := s.readMetas(dir)
	if err != nil {
		return meta{}, err
	}
	var seq int64
	for _, m := range metas {
		seq = max(seq, m.Seq)
	}
	now := s.o.Now()
	id, err := store.NewStampID("bk", now, s.o.Rand, func(id string) bool {
		for _, ext := range []string{copyExt, metaExt, rtcExt} {
			if _, err := os.Lstat(filepath.Join(dir, id+ext)); !errors.Is(err, fs.ErrNotExist) {
				return true
			}
		}
		return false
	})
	if err != nil {
		return meta{}, fmt.Errorf("save backup: %w", err)
	}
	sum, size, err := store.CopyFileAtomic(src, filepath.Join(dir, id+copyExt))
	if err != nil {
		return meta{}, fmt.Errorf("save backup: %w", err)
	}
	m := meta{
		Version: metaVersion, ID: id, Seq: seq + 1, Label: label, Reason: reason,
		CreatedAt: now.UTC().Truncate(time.Second), SizeBytes: size, SHA256: sum,
	}
	if rtc != "" {
		if m.RTCSHA256, _, err = store.CopyFileAtomic(rtc, filepath.Join(dir, id+rtcExt)); err != nil {
			os.Remove(filepath.Join(dir, id+copyExt))
			return meta{}, fmt.Errorf("save backup: clock file: %w", err)
		}
	}
	if err := store.WriteJSONAtomic(filepath.Join(dir, id+metaExt), m); err != nil {
		os.Remove(filepath.Join(dir, id+copyExt))
		os.Remove(filepath.Join(dir, id+rtcExt))
		return meta{}, fmt.Errorf("save backup: %w", err)
	}
	s.o.Logger.Info("save backed up", "game", g.Path, "backup", id, "reason", reason, "bytes", size)
	if reason != ReasonManual {
		s.pruneLocked(g, dir, append([]meta{m}, metas...))
	}
	return m, nil
}

// pruneLocked moves the oldest automatic backups past MaxAutoBackups to the
// trash (never erased: 05 §5.1 "supprimées" means moved to the trash, as
// every deletion of ReminisLink). metas is newest first. Errors are
// logged: the backup that triggered the limit is already safe.
func (s *Service) pruneLocked(g osadapter.Game, dir string, metas []meta) {
	auto := 0
	for _, m := range metas {
		if m.Reason == ReasonManual {
			continue
		}
		auto++
		if auto <= MaxAutoBackups {
			continue
		}
		if err := s.trashBackup(g, dir, m.ID); err != nil {
			s.o.Logger.Warn("save backups: cannot move an old automatic backup to the trash", "backup", m.ID, "error", err)
		} else {
			s.o.Logger.Info("save backups: old automatic backup moved to the trash", "game", g.Path, "backup", m.ID)
		}
	}
}

// trashBackup moves a backup (copy and metadata) to the trash.
func (s *Service) trashBackup(g osadapter.Game, dir, id string) error {
	items := []store.MoveItem{
		{Path: filepath.Join(dir, id+copyExt)},
		{Path: filepath.Join(dir, id+metaExt), Internal: true},
	}
	if _, err := os.Lstat(filepath.Join(dir, id+rtcExt)); err == nil {
		items = append(items, store.MoveItem{Path: filepath.Join(dir, id+rtcExt)})
	}
	_, err := s.o.Trash.Move(store.ReasonDeleteBackup, g.Name, items)
	return err
}

// lookup returns the metadata and the copy of a backup of g.
func (s *Service) lookup(g osadapter.Game, id string) (meta, string, error) {
	if !ValidBackupID(id) {
		return meta{}, "", ErrInvalidBackupID
	}
	dir := s.dir(g)
	m, err := readMeta(dir, id)
	if err != nil {
		return meta{}, "", ErrBackupNotFound
	}
	return m, filepath.Join(dir, id+copyExt), nil
}

// OpenBackup opens the copy of a backup for reading and returns the file
// name to give it. The caller closes the file.
func (s *Service) OpenBackup(g osadapter.Game, id string) (*os.File, string, error) {
	s.o.Lock.Lock()
	defer s.o.Lock.Unlock()
	_, path, err := s.lookup(g, id)
	if err != nil {
		return nil, "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, "", fmt.Errorf("save backup: %w", err)
	}
	return f, strings.TrimSuffix(g.ROMName, filepath.Ext(g.ROMName)) + " (" + id + ")" + copyExt, nil
}

// OpenSave opens the current save of g for reading and returns its file
// name. ErrNoSave when there is none. The caller closes the file.
func (s *Service) OpenSave(g osadapter.Game) (*os.File, string, error) {
	s.o.Lock.Lock()
	defer s.o.Lock.Unlock()
	save, err := s.SaveFile(g)
	if err != nil {
		return nil, "", err
	}
	f, err := os.Open(save.Path)
	if err != nil {
		return nil, "", fmt.Errorf("save: %w", err)
	}
	return f, filepath.Base(save.Path), nil
}

// Delete moves a backup to the trash.
func (s *Service) Delete(g osadapter.Game, id string) error {
	s.o.Lock.Lock()
	defer s.o.Lock.Unlock()
	if _, _, err := s.lookup(g, id); err != nil {
		return err
	}
	if err := s.trashBackup(g, s.dir(g), id); err != nil {
		return fmt.Errorf("delete save backup: %w", err)
	}
	s.o.Logger.Info("save backup deleted (to the trash)", "game", g.Path, "backup", id)
	return nil
}

// Restore replaces the save of g with a backup. The current save, if any,
// is backed up first (before-restore); that backup is returned (nil when
// there was no save).
//
// Order (power loss at any point leaves a usable state): the backup is
// copied to a temporary file and checked against its SHA-256, the current
// save is backed up, then the temporary file is renamed over the save.
func (s *Service) Restore(g osadapter.Game, id string) (*Backup, error) {
	s.o.Lock.Lock()
	defer s.o.Lock.Unlock()
	m, path, err := s.lookup(g, id)
	if err != nil {
		return nil, err
	}
	tmp, err := s.tempFile()
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp) // already gone after a successful rename
	sum, _, err := store.CopyFileAtomic(path, tmp)
	if err != nil {
		return nil, fmt.Errorf("restore save: %w", err)
	}
	if sum != m.SHA256 {
		return nil, ErrCorruptBackup
	}
	rtcTmp := ""
	if m.RTCSHA256 != "" {
		rtcTmp = tmp + rtcExt
		defer os.Remove(rtcTmp)
		sum, _, err := store.CopyFileAtomic(filepath.Join(filepath.Dir(path), id+rtcExt), rtcTmp)
		if err != nil || sum != m.RTCSHA256 {
			return nil, ErrCorruptBackup
		}
	}
	res, err := s.installLocked(g, tmp, rtcTmp, ReasonBeforeRestore, nil)
	if err != nil {
		return nil, err
	}
	s.o.Logger.Info("save restored", "game", g.Path, "backup", id)
	return res, nil
}

// tempFile returns a free temporary path in the uploads folder (same SD
// card as the saves, so the final rename is atomic).
func (s *Service) tempFile() (string, error) {
	dir := s.o.Layout.UploadsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	b := make([]byte, 8)
	if _, err := io.ReadFull(s.o.Rand, b); err != nil {
		return "", err
	}
	return filepath.Join(dir, tempPrefix+hex.EncodeToString(b)+copyExt), nil
}

// WriteTemp writes a new save (body of PUT /games/{gameId}/save) to a
// synced temporary file on the SD card, to pass to Replace.
func (s *Service) WriteTemp(data []byte) (string, error) {
	tmp, err := s.tempFile()
	if err != nil {
		return "", err
	}
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

// Replace makes the synced file tmp the save of g (PUT /games/{gameId}/save):
// the current save is backed up first (before-replace, label "Avant
// remplacement"). It returns that backup, nil when there was no save. tmp
// is removed on failure.
func (s *Service) Replace(g osadapter.Game, tmp string) (*Backup, error) {
	s.o.Lock.Lock()
	defer s.o.Lock.Unlock()
	b, err := s.InstallLocked(g, tmp)
	if err != nil {
		os.Remove(tmp)
	}
	return b, err
}

// InstallLocked is Replace for a caller that already holds the lock (the
// "save" target of the uploads and downloads). tmp is left in place on
// failure.
func (s *Service) InstallLocked(g osadapter.Game, tmp string) (*Backup, error) {
	label := LabelBeforeReplace
	b, err := s.installLocked(g, tmp, "", ReasonBeforeReplace, &label)
	if err != nil {
		return nil, err
	}
	s.o.Logger.Info("save replaced", "game", g.Path)
	return b, nil
}

// installLocked backs up the current save (reason, label) and renames tmp
// over it. The save keeps the name it has on the card when it exists (it
// may differ by case from the adapter's name).
//
// The previous save is not also moved to the trash: its backup copy holds
// the same bytes, and renaming over the save keeps a save in place at
// every moment (moving it away first would leave the game without a save
// if the power is cut before the rename).
//
// rtcTmp, when not "", is a synced clock file installed next to the save
// (a restored backup that had one); otherwise the clock file in place is
// left as it is (it is backed up with the save): a backup without clock
// file never removes the current one.
func (s *Service) installLocked(g osadapter.Game, tmp, rtcTmp, reason string, label *string) (*Backup, error) {
	// Only the default save format is written (the error wraps
	// osadapter.ErrUnsupportedFormat).
	if err := s.o.Adapter.CheckSaveFormat(g); err != nil {
		return nil, err
	}
	files, err := s.o.Adapter.GameFiles(g)
	if err != nil {
		return nil, fmt.Errorf("save: %w", err)
	}
	dst := s.o.Adapter.SavePath(g)
	var backup *Backup
	if files.Save != nil {
		dst = files.Save.Path
		m, err := s.createLocked(g, files.Save.Path, rtcPath(files), reason, label)
		if err != nil {
			return nil, err
		}
		b := m.public()
		backup = &b
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return nil, fmt.Errorf("save: %w", err)
	}
	// The clock file first: a power cut between the two renames leaves the
	// previous save with the restored clock (the restore did not happen
	// for the save, run it again), rather than the restored save with a
	// clock of another save. Both previous files are in the backup just
	// made.
	if rtcTmp != "" {
		rtcDst := strings.TrimSuffix(dst, copyExt) + rtcExt
		if files.RTC != nil {
			rtcDst = files.RTC.Path
		}
		if err := os.Rename(rtcTmp, rtcDst); err != nil {
			return nil, fmt.Errorf("save: install the clock file: %w", err)
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		return nil, fmt.Errorf("save: install: %w", err)
	}
	store.SyncDir(filepath.Dir(dst))
	store.SyncDir(filepath.Dir(tmp))
	return backup, nil
}
