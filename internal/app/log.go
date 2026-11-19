package app

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
)

// LogFileName is the log file in $LOGS_PATH (docs/specs/05-console-pak.md §8).
const LogFileName = "ReminisLink.txt"

// Size limits of the log file on the SD card.
const (
	// LogRotateBytes: a larger log file is renamed LogOldFileName at launch.
	LogRotateBytes = 5 << 20
	// LogCapBytes: past this size during a run, the sampled lines
	// (unsigned and rejected requests) are no longer written.
	LogCapBytes = 20 << 20
	// LogOldFileName keeps the previous log file.
	LogOldFileName = "ReminisLink.old.txt"
)

// logFile is the log file and its size, counted as it is written.
type logFile struct {
	f    *os.File
	size atomic.Int64
}

func (l *logFile) Write(p []byte) (int, error) {
	n, err := l.f.Write(p)
	l.size.Add(int64(n))
	return n, err
}

// full reports that the file reached LogCapBytes.
func (l *logFile) full() bool { return l.size.Load() >= LogCapBytes }

// openLog sends slog to <dir>/ReminisLink.txt, and to also when not nil
// (stderr in headless mode). A file larger than LogRotateBytes is first
// renamed ReminisLink.old.txt (replacing the previous one).
func openLog(dir string, also io.Writer) (*slog.Logger, *logFile, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("create log folder: %w", err)
	}
	path := filepath.Join(dir, LogFileName)
	if fi, err := os.Stat(path); err == nil && fi.Size() > LogRotateBytes {
		if err := os.Rename(path, filepath.Join(dir, LogOldFileName)); err != nil {
			return nil, nil, fmt.Errorf("rotate log: %w", err)
		}
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("open log: %w", err)
	}
	lf := &logFile{f: f}
	if fi, err := f.Stat(); err == nil {
		lf.size.Store(fi.Size())
	}
	var w io.Writer = lf
	if also != nil {
		w = io.MultiWriter(lf, also)
	}
	return slog.New(slog.NewTextHandler(w, nil)), lf, nil
}
