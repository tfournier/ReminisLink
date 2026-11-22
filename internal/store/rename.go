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
}

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
func (t *Trash) RenameGroup(label string, steps []RenameStep) error {
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
		j.Steps = append(j.Steps, renameRecord{From: filepath.ToSlash(rf), To: filepath.ToSlash(rt), Via: filepath.ToSlash(via)})
	}
	root.Close()
	dir := filepath.Join(t.renames, id)
	if err := t.mkdirInSD(dir); err != nil {
		return fmt.Errorf("rename: journal folder: %w", err)
	}
	if err := t.writeJSONConfined(dir, renameJournalFile, j, func(d string) error { return t.syncDir(d) }); err != nil {
		t.removeJournal(dir)
		return fmt.Errorf("rename: %w", err)
	}
	sd, err := filepath.Abs(t.sd)
	if err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	abs := func(rel string) string { return filepath.Join(sd, filepath.FromSlash(rel)) }
	for i, s := range j.Steps {
		if err := t.renameStep(abs(s.From), abs(s.To), abs(s.Via)); err != nil {
			if !t.rollbackRenames(sd, j.Steps[:i+1]) {
				t.log.Error("rename: roll back failed, left for the next start", "journal", id, "error", err)
				return fmt.Errorf("rename: %w", err)
			}
			t.removeJournal(dir)
			if errors.Is(err, fs.ErrExist) {
				return &RenameConflictError{Conflicts: []string{s.To}}
			}
			return fmt.Errorf("rename: %w", err)
		}
		if err := t.syncDir(filepath.Dir(abs(s.To))); err != nil {
			t.log.Warn("rename: sync failed", "error", err)
		}
	}
	t.removeJournal(dir)
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
func (t *Trash) rollbackRenames(sd string, steps []renameRecord) bool {
	root, err := t.openSD()
	if err != nil {
		t.log.Error("rename: roll back: cannot open the card", "error", err)
		return false
	}
	defer root.Close()
	ok := true
	abs := func(rel string) string { return filepath.Join(sd, filepath.FromSlash(rel)) }
	for i := len(steps) - 1; i >= 0; i-- {
		s := steps[i]
		fromThere := exactExists(root, s.From)
		switch {
		case exactExists(root, s.Via):
			if fromThere {
				t.log.Error("rename: roll back: old name taken", "file", s.From)
				ok = false
				continue
			}
			if err := t.renameBack(abs(s.Via), abs(s.From)); err != nil {
				t.log.Error("rename: roll back failed", "file", s.From, "error", err)
				ok = false
			}
		case exactExists(root, s.To) && !fromThere:
			if err := t.renameStepBack(abs(s.To), abs(s.From), abs(s.Via)); err != nil {
				t.log.Error("rename: roll back failed", "file", s.From, "error", err)
				ok = false
			}
		}
	}
	return ok
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

// removeJournal deletes a rename journal and its folder.
func (t *Trash) removeJournal(dir string) {
	root, err := t.openSD()
	if err != nil {
		return
	}
	defer root.Close()
	rel, err := t.confinedRel(root, dir)
	if err != nil {
		return
	}
	_ = root.Remove(filepath.Join(rel, renameJournalFile))
	_ = root.Remove(filepath.Join(rel, renameJournalFile+".tmp"))
	_ = root.Remove(rel)
	_ = t.syncDir(filepath.Dir(dir))
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
			t.removeJournal(dir)
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
			t.log.Info("rename: interrupted rename put back", "journal", d.Name(), "label", j.Label)
			t.removeJournal(dir)
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
