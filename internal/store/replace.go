package store

import (
	"errors"
	"fmt"

	"os"
	"path/filepath"
)

// Install is a new file put in place by Trash.Replace.
type Install struct {
	Src string // synced temporary file, on the SD card
	Dst string // destination, on the SD card
	// Size and SHA256 (optional, lowercase hex) describe Src: Recover only
	// finishes an interrupted replacement with a complete file.
	Size   int64
	SHA256 string
	// Dir: Src is a folder (a pak, M12), fully written and synced before
	// Replace; Size is then the total size of its regular files, and a
	// folder is complete when it holds exactly that (SHA256 unused).
	Dir bool
}

// ErrReplaceFailed wraps the error of an install that could not be
// finished nor rolled back: the entry stays "installing" and Recover
// handles it at the next start (nothing is lost, nothing is purged).
var ErrReplaceFailed = errors.New("trash: replacement left for the next start")

// Replace replaces files with a journal (docs/specs/05-console-pak.md §4,
// rule 1 of CLAUDE.md): items (the files being replaced, and those that
// go with them) move to a new trash entry, then each Install.Src is renamed
// to its Dst, and the entry becomes "done". entry.json records every step,
// so a power cut at any point is finished (the new files are complete) or
// rolled back (everything back in place) by Recover at the next start, and
// the entry is never listed, deleted, emptied nor purged before. On an
// error, everything is put back before returning; if even that fails,
// ErrReplaceFailed is returned and the next start finishes the job.
//
// Without items (nothing to replace), the installs are journaled the
// same way (M19, Codex review: a power cut between two renames must not
// leave half of them in place); the entry, empty, is removed once every
// install is in place, and never listed.
// The installs must not overwrite an existing file that is not in items.
func (t *Trash) Replace(reason, label string, items []MoveItem, installs []Install) (MoveResult, error) {
	if !validReasons[reason] {
		return MoveResult{}, fmt.Errorf("trash: unknown reason %q", reason)
	}
	if len(installs) == 0 {
		return t.Move(reason, label, items)
	}
	sd, err := filepath.Abs(t.sd)
	if err != nil {
		return MoveResult{}, fmt.Errorf("trash: %w", err)
	}
	records := make([]installRecord, len(installs))
	for i, in := range installs {
		src, err1 := relUnder(sd, in.Src)
		dst, err2 := relUnder(sd, in.Dst)
		if err := errors.Join(err1, err2); err != nil {
			return MoveResult{}, err
		}
		records[i] = installRecord{Src: src, Dst: dst, Size: in.Size, SHA256: in.SHA256, Dir: in.Dir}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	res, entryDir, e, err := t.moveLocked(reason, label, items, records)
	if err != nil {
		return MoveResult{}, err
	}
	for i, r := range records {
		renamed, err := t.installOne(sd, r)
		if err == nil {
			continue
		}
		// Roll back now: the new files back to their temporary place
		// (this one too when its rename succeeded and only a sync
		// failed), then, once the journal says so, the old ones back to
		// theirs. The journal stays until every step is durable.
		if t.uninstall(sd, records[:doneUpTo(i, renamed)]) {
			e.State = stateRestoring
			if t.saveEntry(entryDir, e) == nil && t.rollback(entryDir, sd, &e) {
				return MoveResult{}, fmt.Errorf("trash: install %s (everything put back): %w", r.Dst, err)
			}
		}
		t.log.Error("trash: replacement interrupted, finished at the next start", "entry", e.ID, "error", err)
		return MoveResult{}, fmt.Errorf("%w: install %s: %v", ErrReplaceFailed, r.Dst, err)
	}
	if len(e.Moves) == 0 {
		// Nothing was replaced: the journal is no longer needed.
		t.removeInstallJournal(entryDir, e)
		t.log.Info("installed", "reason", reason, "label", label, "installed", len(records))
		return MoveResult{Moved: []string{}}, nil
	}
	e.State = stateDone
	if err := t.saveEntry(entryDir, e); err != nil {
		// The new files are in place and the old ones in the entry: the
		// next start sees them complete and marks the entry done.
		t.log.Error("trash: replacement done, entry marked at the next start", "entry", e.ID, "error", err)
	}
	t.log.Info("replaced", "entry", e.ID, "reason", reason, "label", label, "installed", len(records))
	return res, nil
}

// relUnder returns p relative to sd ("/" separator), refusing a path
// outside it.
func relUnder(sd, p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("trash: %w", err)
	}
	rel, err := filepath.Rel(sd, abs)
	if err != nil || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("trash: %s is not inside the SD card", p)
	}
	return filepath.ToSlash(rel), nil
}

// installOne renames an install into place and syncs both folders.
// renamed reports that the rename happened (even if a sync then failed).
func (t *Trash) installOne(sd string, r installRecord) (renamed bool, err error) {
	src := filepath.Join(sd, filepath.FromSlash(r.Src))
	dst := filepath.Join(sd, filepath.FromSlash(r.Dst))
	if err := t.mkdirInSD(filepath.Dir(dst)); err != nil {
		return false, err
	}
	if err := t.rename(src, dst); err != nil {
		return false, withoutPathErr(err)
	}
	if err := t.syncDir(filepath.Dir(dst)); err != nil {
		return true, err
	}
	return true, t.syncDir(filepath.Dir(src))
}

// doneUpTo is the number of installs renamed when install i failed.
func doneUpTo(i int, renamed bool) int {
	if renamed {
		return i + 1
	}
	return i
}

// uninstall renames installed files back to their temporary place; false
// when one could not go back.
func (t *Trash) uninstall(sd string, done []installRecord) bool {
	ok := true
	for i := len(done) - 1; i >= 0; i-- {
		r := done[i]
		src := filepath.Join(sd, filepath.FromSlash(r.Src))
		dst := filepath.Join(sd, filepath.FromSlash(r.Dst))
		if err := t.restoreItem(dst, src); err != nil {
			ok = false
		}
	}
	return ok
}

// rollback puts the moved items of an entry back and removes it; false
// when something could not go back (the entry is then kept as it is).
func (t *Trash) rollback(entryDir, sd string, e *entryFile) bool {
	for _, rel := range e.Moves {
		src := filepath.Join(entryDir, filepath.FromSlash(rel))
		dst := filepath.Join(sd, filepath.FromSlash(rel))
		if !isUnder(sd, dst) {
			return false
		}
		if _, err := os.Lstat(src); err == nil {
			if t.restoreItem(src, dst) != nil {
				return false
			}
			continue
		}
		// Already moved back (by an earlier attempt whose sync may have
		// failed, Codex review of M8): it must be in place, and both
		// folders are synced again before the journal goes.
		if _, err := os.Lstat(dst); err != nil {
			t.log.Error("trash: a file of a roll back is neither in the entry nor in place", "entry", e.ID, "file", rel)
			return false
		}
		if t.syncDir(filepath.Dir(dst)) != nil || t.syncDir(filepath.Dir(src)) != nil {
			return false
		}
	}
	// Every file is back and durable: the journal can go.
	if err := os.RemoveAll(entryDir); err != nil {
		t.log.Warn("trash: cannot remove a rolled back entry", "entry", e.ID, "error", err)
	}
	return true
}

// installed reports whether the install r is complete at its destination.
func installed(sd string, r installRecord) bool {
	return complete(filepath.Join(sd, filepath.FromSlash(r.Dst)), r)
}

// complete reports whether path is a regular file of r.Size bytes with
// the SHA-256 r.SHA256 (when set), or, for a folder install, a folder
// (not a link) whose regular files total r.Size bytes.
func complete(path string, r installRecord) bool {
	if r.Dir {
		fi, err := os.Lstat(path)
		return err == nil && fi.IsDir() && dirSize(path) == r.Size
	}
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() != r.Size {
		return false
	}
	if r.SHA256 == "" {
		return true
	}
	sum, _, err := HashFile(path)
	return err == nil && sum == r.SHA256
}

// recoverInstall finishes or rolls back an entry left "installing" by a
// power cut (called by Recover with t.mu held): when every new file is
// complete, at its destination or still at its temporary place, the
// installation is finished and the entry becomes "done"; otherwise the
// installed files go back to their temporary place and the old files back
// to theirs. When neither is possible the entry stays "installing" (kept,
// never purged) and the next start tries again.
func (t *Trash) recoverInstall(dir, sd string, e *entryFile) error {
	finish := true
	for _, r := range e.Installs {
		if !filepath.IsLocal(filepath.FromSlash(r.Src)) || !filepath.IsLocal(filepath.FromSlash(r.Dst)) {
			finish = false
			break
		}
		if installed(sd, r) {
			continue
		}
		if !complete(filepath.Join(sd, filepath.FromSlash(r.Src)), r) {
			finish = false
			break
		}
	}
	if finish {
		for _, r := range e.Installs {
			if installed(sd, r) {
				continue
			}
			dst := filepath.Join(sd, filepath.FromSlash(r.Dst))
			if _, err := os.Lstat(dst); err == nil {
				finish = false // something else took the place: roll back
				break
			}
			if _, err := t.installOne(sd, r); err != nil {
				t.log.Error("trash: cannot finish an interrupted replacement", "entry", e.ID, "file", r.Dst, "error", err)
				return nil // kept as it is; tried again at the next start
			}
		}
	}
	if finish {
		if len(e.Moves) == 0 {
			t.removeInstallJournal(dir, *e)
			t.log.Info("trash: interrupted installation finished", "entry", e.ID)
			return nil
		}
		e.State = stateDone
		if err := t.saveEntry(dir, *e); err != nil {
			return err
		}
		t.log.Info("trash: interrupted replacement finished", "entry", e.ID, "files", e.Files)
		return nil
	}
	// Roll back: a new file goes back to its temporary place only when it
	// is complete there (the old file can then take its place again); an
	// incomplete one is left where it is.
	var installedBack []installRecord
	for _, r := range e.Installs {
		if filepath.IsLocal(filepath.FromSlash(r.Dst)) && installed(sd, r) {
			installedBack = append(installedBack, r)
		}
	}
	ok := t.uninstall(sd, installedBack)
	if ok {
		e.State = stateRestoring
		ok = t.saveEntry(dir, *e) == nil && t.rollback(dir, sd, e)
	}
	if !ok {
		t.log.Error("trash: interrupted replacement kept for the next start, its files could not go back", "entry", e.ID)
		return nil
	}
	t.log.Warn("trash: interrupted replacement rolled back (the new file was incomplete)", "entry", e.ID, "files", e.Files)
	return nil
}

// PendingSources returns the temporary files (absolute paths) of the
// replacements still in progress after Recover: the purge of the
// temporary folders must keep them.
func (t *Trash) PendingSources() map[string]bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := map[string]bool{}
	names, err := t.entryDirs()
	if err != nil {
		return out
	}
	sd, err := filepath.Abs(t.sd)
	if err != nil {
		return out
	}
	for _, name := range names {
		e, err := readEntry(filepath.Join(t.dir, name))
		if err != nil || (e.State != stateInstalling && e.State != stateRestoring) {
			continue
		}
		for _, r := range e.Installs {
			if filepath.IsLocal(filepath.FromSlash(r.Src)) {
				out[filepath.Join(sd, filepath.FromSlash(r.Src))] = true
			}
		}
	}
	return out
}

// removeInstallJournal removes the journal of a replacement that moved
// nothing (only installs), once every install is in place: such an entry
// is never listed. A failure leaves it "installing" (or "done" with
// nothing moved), which Recover finishes or removes at the next start.
func (t *Trash) removeInstallJournal(entryDir string, e entryFile) {
	if err := os.RemoveAll(entryDir); err != nil {
		t.log.Warn("trash: cannot remove the journal of an installation", "entry", e.ID, "error", err)
		return
	}
	_ = t.syncDir(filepath.Dir(entryDir))
}
