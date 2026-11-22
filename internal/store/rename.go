package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// Journaled renames (M23, POST /games/{gameId}/rename): a group of renames
// in place (each file or folder keeps its folder and takes a new name)
// that happens as a whole or not at all. Before the first rename, a
// journal renames/<id>/journal.json lists every step; once the last one
// is done (and synced), the journal is removed. A journal found at start
// (Recover) is a group a power cut interrupted: every step is put back,
// last first, so the files keep their old names.
//
// A rename never replaces an entry (renameNoReplaceInSD). A step whose new
// name already reaches the same entry (a change of case on exFAT, or of
// Unicode normalization on APFS) goes through a temporary hidden name in
// the same folder, recorded in the journal ("via").

const (
	renameJournalFile    = "journal.json"
	renameJournalVersion = 1
	renameTempPrefix     = ".reminislink-rename-"
)

// RenameStep renames From to To (absolute paths inside the SD card, in the
// same folder). Steps run in order: a later step names the state after the
// earlier ones (the playlist inside a renamed folder).
type RenameStep struct {
	From, To string
	// Identity is the file or folder that moves, when From does not exist
	// yet when the journal is written (the playlist inside a folder renamed
	// by an earlier step); nil: From is read. Recorded in the journal: a
	// roll back never touches another entry.
	Identity fs.FileInfo
}

// ErrRenameCommit: the journal of a rename could not be removed (or that
// removal made durable) once every step was done; the rename was rolled
// back, or, when even that failed, the journal stays valid for the next
// start. Never a success (Codex review of M23).
var ErrRenameCommit = errors.New("rename: journal could not be removed")

// RenameConflictError: the new name of a step is taken (Conflicts relative
// to the SD card, "/" separator); nothing was renamed.
type RenameConflictError struct {
	Conflicts []string
}

func (e *RenameConflictError) Error() string {
	return "rename: name taken: " + strings.Join(e.Conflicts, ", ")
}

// renameRecord is a step in the journal, relative to the SD card ("/").
type renameRecord struct {
	From string `json:"from"`
	To   string `json:"to"`
	Via  string `json:"via"`
	// The entry that moves, as a roll back recognizes it (Codex review of
	// M23): its kind (file or dir), and for a file its size and
	// modification time in nanoseconds. ID (device and inode) only
	// accepts faster: the exFAT driver of Linux numbers inodes in memory,
	// so after a restart the same file has another one; a different ID
	// never refuses alone.
	ID      string `json:"id,omitempty"`
	Kind    string `json:"kind,omitempty"`
	Size    int64  `json:"size,omitempty"`
	MtimeNS int64  `json:"mtimeNs,omitempty"`
}

// recordOf describes the entry fi for the journal.
func recordOf(r renameRecord, fi fs.FileInfo) renameRecord {
	r.ID = identityOf(fi)
	if fi.IsDir() {
		r.Kind = "dir"
		return r
	}
	r.Kind = "file"
	r.Size = fi.Size()
	r.MtimeNS = fi.ModTime().UnixNano()
	return r
}

// isEntry reports whether fi is the entry r recorded: the same inode
// (fast), or the same kind and, for a file, the same size and
// modification time. A folder is compared on its kind only: renaming the
// playlist inside it changes its date. A journal without a kind (older)
// accepts any entry.
func (r renameRecord) isEntry(fi fs.FileInfo) bool {
	if r.ID != "" && identityOf(fi) == r.ID {
		return true
	}
	switch r.Kind {
	case "":
		return true
	case "dir":
		return fi.IsDir()
	default:
		return fi.Mode().IsRegular() && fi.Size() == r.Size && fi.ModTime().UnixNano() == r.MtimeNS
	}
}

type renameJournal struct {
	Version int            `json:"version"`
	ID      string         `json:"id"`
	Label   string         `json:"label"`
	Steps   []renameRecord `json:"steps"`
}

// RenameGroup renames every step as one journaled operation. On a failure
// the steps done are put back (in reverse order) and the error is
// returned: a *RenameConflictError when a new name is taken. If a put back
// fails too, the journal stays and the next start finishes the roll back.
// verify (may be nil) runs once every step is done and synced, before the
// journal is removed: its error rolls the group back and is returned
// (the renamed game must be listed again by the OS).
func (t *Trash) RenameGroup(label string, steps []RenameStep, verify func() error) error {
	if len(steps) == 0 {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.renames == "" {
		return errors.New("rename: no journal folder")
	}
	root, err := t.openSD()
	if err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	id, err := t.newID(t.now())
	if err != nil {
		root.Close()
		return fmt.Errorf("rename: %w", err)
	}
	id = "rn" + strings.TrimPrefix(id, "tr")
	j := renameJournal{Version: renameJournalVersion, ID: id, Label: label}
	for i, s := range steps {
		rf, err := t.confinedRel(root, s.From)
		if err != nil {
			root.Close()
			return fmt.Errorf("rename: %w", err)
		}
		rt, err := t.confinedRel(root, s.To)
		if err != nil {
			root.Close()
			return fmt.Errorf("rename: %w", err)
		}
		if filepath.Dir(rf) != filepath.Dir(rt) || rf == "." || rt == "." {
			root.Close()
			return fmt.Errorf("rename: step %d leaves its folder", i)
		}
		via := filepath.Join(filepath.Dir(rf), renameTempPrefix+id+"-"+strconv.Itoa(i))
		info := s.Identity
		if info == nil {
			if info, err = root.Lstat(rf); err != nil {
				root.Close()
				return fmt.Errorf("rename: step %d: %w", i, withoutPathErr(err))
			}
		}
		j.Steps = append(j.Steps, recordOf(renameRecord{From: filepath.ToSlash(rf), To: filepath.ToSlash(rt), Via: filepath.ToSlash(via)}, info))
	}
	root.Close()
	dir := filepath.Join(t.renames, id)
	if err := t.mkdirInSD(dir); err != nil {
		return fmt.Errorf("rename: journal folder: %w", err)
	}
	if err := t.writeJSONConfined(dir, renameJournalFile, j, t.syncSD); err != nil {
		_ = t.removeJournal(dir)
		return fmt.Errorf("rename: %w", err)
	}
	sd, err := filepath.Abs(t.sd)
	if err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	abs := func(rel string) string { return filepath.Join(sd, filepath.FromSlash(rel)) }
	for i, s := range j.Steps {
		if err := t.renameStep(abs(s.From), abs(s.To), abs(s.Via)); err != nil {
			if !t.rollbackRenames(sd, j.Steps[:i+1]) || t.removeJournal(dir) != nil {
				t.log.Error("rename: roll back failed, left for the next start", "journal", id, "error", err)
				return fmt.Errorf("rename: %w", err)
			}
			if errors.Is(err, fs.ErrExist) {
				return &RenameConflictError{Conflicts: []string{s.To}}
			}
			return fmt.Errorf("rename: %w", err)
		}
		// A rename that is not durable is not done (Codex review of M23):
		// a power cut could keep some new names and lose others. The group
		// goes back, and the journal stays until that is durable too.
		if err := t.syncSD(filepath.Dir(abs(s.To))); err != nil {
			if !t.rollbackRenames(sd, j.Steps[:i+1]) || t.removeJournal(dir) != nil {
				t.log.Error("rename: roll back failed, left for the next start", "journal", id, "error", err)
			}
			return fmt.Errorf("rename: sync: %w", err)
		}
	}
	if verify != nil {
		if err := verify(); err != nil {
			if !t.rollbackRenames(sd, j.Steps) || t.removeJournal(dir) != nil {
				t.log.Error("rename: roll back failed, left for the next start", "journal", id, "error", err)
			}
			return err
		}
	}
	// The commit: the journal goes. If that fails (or is not durable), the
	// next start would undo the rename: so it is undone now, never
	// answered as a success (Codex review of M23).
	if err := t.removeJournal(dir); err != nil {
		t.log.Error("rename: journal not removed, rolling back", "journal", id, "error", err)
		// The journal is written again as a safety net for the next start,
		// but the files go back first whatever happens to it (Codex review
		// of M23, round 3).
		if werr := t.writeJSONConfined(dir, renameJournalFile, j, t.syncSD); werr != nil {
			t.log.Error("rename: journal cannot be written again", "journal", id, "error", werr)
		}
		if t.rollbackRenames(sd, j.Steps) {
			_ = t.removeJournal(dir)
		}
		return fmt.Errorf("%w: %w", ErrRenameCommit, err)
	}
	return nil
}

// RenameNoReplace renames src to dst inside the SD card, through no link,
// never replacing dst (fs.ErrExist when it exists): an added file of the
// SD card browser (M23).
func (t *Trash) RenameNoReplace(src, dst string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.renameExcl(src, dst)
}

// renameStep renames from to to without replacing anything; when to
// already reaches from (another case on a case-insensitive folder), it goes
// through via.
func (t *Trash) renameStep(from, to, via string) error {
	fi, err := os.Lstat(from)
	if err != nil {
		return withoutPathErr(err)
	}
	if ti, err := os.Lstat(to); err == nil && os.SameFile(fi, ti) && filepath.Base(from) != filepath.Base(to) {
		if err := t.renameExcl(from, via); err != nil {
			return err
		}
		if err := t.renameExcl(via, to); err != nil {
			if berr := t.renameBack(via, from); berr != nil {
				t.log.Error("rename: temporary name left", "error", berr)
			}
			return err
		}
		return nil
	}
	return t.renameExcl(from, to)
}

// rollbackRenames puts back the steps (relative to sd), last first, and
// reports whether every one went back (or was never done). A step is done
// when its new name exists under that exact spelling and its old one does
// not, or when its temporary name exists.
func (t *Trash) rollbackRenames(sd string, steps []renameRecord) (ok bool) {
	root, err := t.openSD()
	if err != nil {
		t.log.Error("rename: roll back: cannot open the card", "error", err)
		return false
	}
	defer root.Close()
	ok = true
	abs := func(rel string) string { return filepath.Join(sd, filepath.FromSlash(rel)) }
	// The folders of every step (put back now, or by an earlier roll back
	// whose sync failed), as they exist once the steps are back: synced
	// before the journal can go. A folder renamed back by an earlier step
	// is synced under its old name; a path that no longer exists is never
	// synced (Codex review of M23).
	defer func() {
		for d := range t.rollbackFolders(root, sd, steps) {
			if err := t.syncSD(d); err != nil {
				t.log.Error("rename: roll back: sync failed, journal kept", "error", err)
				ok = false
			}
		}
	}()
	for i := len(steps) - 1; i >= 0; i-- {
		s := steps[i]
		fromThere := exactExists(root, s.From)
		// Only the entry the step moved goes back: another one now at its
		// new or temporary name (made after a power cut) is never touched.
		ours := func(rel string) bool {
			fi, err := root.Lstat(filepath.FromSlash(rel))
			if err != nil {
				return false
			}
			if !s.isEntry(fi) {
				t.log.Warn("rename: roll back: another entry at the new name, left as it is", "file", rel)
				return false
			}
			return true
		}
		switch {
		case exactExists(root, s.Via) && ours(s.Via):
			if fromThere {
				t.log.Error("rename: roll back: old name taken", "file", s.From)
				ok = false
				continue
			}
			if err := t.renameBack(abs(s.Via), abs(s.From)); err != nil {
				t.log.Error("rename: roll back failed", "file", s.From, "error", err)
				ok = false
			}
		case exactExists(root, s.To) && !fromThere && ours(s.To):
			if err := t.renameStepBack(abs(s.To), abs(s.From), abs(s.Via)); err != nil {
				t.log.Error("rename: roll back failed", "file", s.From, "error", err)
				ok = false
			}
		}
	}
	return ok
}

// removeFile removes a file through root (tests replace it with
// removeJournalFile to simulate a failure).
func (t *Trash) removeFile(root *os.Root, rel string) error {
	if t.removeJournalFile != nil {
		return t.removeJournalFile(rel)
	}
	return root.Remove(rel)
}

// rollbackFolders are the folders to sync once steps are back: the parent
// of each step's old name, translated through the folders renamed back by
// the steps (a parent named after a step's new name, which no longer
// exists, is found under that step's old name), only when it exists.
func (t *Trash) rollbackFolders(root *os.Root, sd string, steps []renameRecord) map[string]bool {
	out := map[string]bool{}
	abs := func(rel string) string { return filepath.Join(sd, filepath.FromSlash(rel)) }
	// Read through the card root, no link at any component (Codex review
	// of M23, round 3): a folder replaced by a link is not there.
	exists := func(p string) bool {
		rel, err := t.confinedRel(root, p)
		if err != nil {
			return false
		}
		fi, err := root.Lstat(rel)
		return err == nil && fi.IsDir()
	}
	for _, s := range steps {
		d := filepath.Dir(abs(s.From))
		if !exists(d) {
			for _, o := range steps {
				to, from := abs(o.To), abs(o.From)
				if d == to || strings.HasPrefix(d, to+string(filepath.Separator)) {
					if cand := from + strings.TrimPrefix(d, to); exists(cand) {
						d = cand
						break
					}
				}
			}
		}
		if exists(d) {
			out[d] = true
		}
	}
	return out
}

// renameStepBack is renameStep for a roll back (renameBack, never
// replacing either).
func (t *Trash) renameStepBack(from, to, via string) error {
	fi, err := os.Lstat(from)
	if err != nil {
		return withoutPathErr(err)
	}
	if ti, err := os.Lstat(to); err == nil && os.SameFile(fi, ti) {
		if err := t.renameBack(from, via); err != nil {
			return err
		}
		return t.renameBack(via, to)
	}
	return t.renameBack(from, to)
}

// exactExists reports whether rel ("/" separator, relative to root) is an
// entry of its folder under this exact name (exFAT and APFS find a file
// under any case: the listing of the folder says how it is spelled).
func exactExists(root *os.Root, rel string) bool {
	dir, base := path.Split(rel)
	dir = strings.TrimSuffix(dir, "/")
	if dir == "" {
		dir = "."
	}
	des, err := fs.ReadDir(root.FS(), dir)
	if err != nil {
		return false
	}
	for _, d := range des {
		if d.Name() == base {
			return true
		}
	}
	return false
}

// removeJournal deletes a rename journal and its folder. The removal of
// the journal file must be durable (its folder synced): otherwise it is an
// error, and the journal may still be on the card after a power cut.
func (t *Trash) removeJournal(dir string) error {
	root, err := t.openSD()
	if err != nil {
		return err
	}
	defer root.Close()
	rel, err := t.confinedRel(root, dir)
	if err != nil {
		return err
	}
	if err := t.removeFile(root, filepath.Join(rel, renameJournalFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return withoutPathErr(err)
	}
	if err := t.syncSD(dir); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	_ = root.Remove(filepath.Join(rel, renameJournalFile+".tmp"))
	_ = root.Remove(rel)
	_ = t.syncSD(filepath.Dir(dir))
	return nil
}

// syncSD syncs a folder of the card through its root, refusing a link at
// any component (Codex review of M23, round 3: a folder of the card
// replaced by a link to another one is never synced through it). Tests
// that replace the sync (SetSyncDir) get it called after the check.
func (t *Trash) syncSD(dir string) error {
	root, err := t.openSD()
	if err != nil {
		return err
	}
	defer root.Close()
	rel, err := t.confinedRel(root, dir)
	if err != nil {
		return err
	}
	if t.syncReplaced {
		return t.syncDir(dir)
	}
	d, err := root.Open(rel)
	if err != nil {
		return withoutPathErr(err)
	}
	err = d.Sync()
	cerr := d.Close()
	if err != nil && !syncUnsupported(err) {
		return fmt.Errorf("sync: %w", withoutPathErr(err))
	}
	return cerr
}

// recoverRenamesLocked rolls back the rename groups a power cut
// interrupted (their journal is still there). Called with t.mu held.
func (t *Trash) recoverRenamesLocked() {
	if t.renames == "" {
		return
	}
	root, err := t.openSD()
	if err != nil {
		return
	}
	rel, err := t.confinedRel(root, t.renames)
	if err != nil {
		root.Close()
		t.log.Error("rename: recovery skipped, the journal folder is reached through a link", "error", err)
		return
	}
	des, err := fs.ReadDir(root.FS(), filepath.ToSlash(rel))
	root.Close()
	if err != nil {
		return
	}
	sd, err := filepath.Abs(t.sd)
	if err != nil {
		return
	}
	for _, d := range des {
		if !d.IsDir() || !strings.HasPrefix(d.Name(), "rn_") {
			continue
		}
		dir := filepath.Join(t.renames, d.Name())
		j, err := t.readRenameJournal(dir)
		if errors.Is(err, fs.ErrNotExist) {
			// The journal was never written (or removed): nothing moved.
			_ = t.removeJournal(dir)
			continue
		}
		if err != nil {
			t.log.Error("rename: unreadable journal kept", "journal", d.Name(), "error", err)
			continue
		}
		if !validJournal(j) {
			t.log.Error("rename: journal lists a place outside the card, kept", "journal", d.Name())
			continue
		}
		if t.rollbackRenames(sd, j.Steps) {
			if err := t.removeJournal(dir); err != nil {
				t.log.Error("rename: journal kept", "journal", d.Name(), "error", err)
				continue
			}
			t.log.Info("rename: interrupted rename put back", "journal", d.Name(), "label", j.Label)
		}
	}
}

// validJournal checks that every place of a journal stays in the card,
// each step in one folder.
func validJournal(j renameJournal) bool {
	for _, s := range j.Steps {
		for _, p := range []string{s.From, s.To, s.Via} {
			if !filepath.IsLocal(filepath.FromSlash(p)) {
				return false
			}
		}
		if path.Dir(s.From) != path.Dir(s.To) || path.Dir(s.From) != path.Dir(s.Via) {
			return false
		}
	}
	return true
}

// readRenameJournal reads a journal through the card root (no link).
func (t *Trash) readRenameJournal(dir string) (renameJournal, error) {
	var j renameJournal
	root, err := t.openSD()
	if err != nil {
		return j, err
	}
	defer root.Close()
	rel, err := t.confinedRel(root, filepath.Join(dir, renameJournalFile))
	if err != nil {
		return j, err
	}
	data, err := root.ReadFile(rel)
	if err != nil {
		return j, err
	}
	if err := json.Unmarshal(data, &j); err != nil {
		return j, err
	}
	if j.Version != renameJournalVersion {
		return j, fmt.Errorf("rename journal version %d", j.Version)
	}
	return j, nil
}
