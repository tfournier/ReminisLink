// Package cheats reads RetroArch .cht files.
//
// The line format is the one of Cheat Manager (internal/cheats/cht.go of
// github.com/tfournier/nextui-cheat-manager), whose parser and writer will
// be copied here with the cheat routes (milestone M5). For now the pak only
// counts the cheats of a file.
package cheats

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"regexp"
	"strconv"
)

const (
	// MaxFileBytes is the largest .cht file read.
	MaxFileBytes = 8 << 20
	// HeaderBytes is how much of a file is read to find "cheats = N".
	HeaderBytes = 4 << 10
	// maxDeclared is the largest "cheats = N" trusted.
	maxDeclared = 100000
)

var (
	// cheatLine is copied from Cheat Manager.
	cheatLine = regexp.MustCompile(`^\s*cheat(\d+)_([A-Za-z0-9_]+)\s*=(.*)$`)
	// countLine is the "cheats = N" header RetroArch and Cheat Manager
	// write before the first cheat.
	countLine = regexp.MustCompile(`^\s*cheats\s*=\s*"?(\d{1,9})"?\s*$`)
)

// DeclaredCount returns N from the "cheats = N" line found in head (the
// start of a file) before any cheat line, and false when there is none.
func DeclaredCount(head []byte) (int, bool) {
	lines := bytes.Split(head, []byte("\n"))
	if len(head) == HeaderBytes && len(lines) > 1 {
		lines = lines[:len(lines)-1] // the last line may be cut
	}
	for _, line := range lines {
		line = bytes.TrimRight(line, "\r")
		if m := countLine.FindSubmatch(line); m != nil {
			n, err := strconv.Atoi(string(m[1]))
			if err != nil || n > maxDeclared {
				return 0, false
			}
			return n, true
		}
		if cheatLine.Match(line) {
			return 0, false
		}
	}
	return 0, false
}

// Count returns the number of cheats in the content of a .cht file: the
// number of distinct indexes N of the "cheatN_<key> = ..." lines, as
// Cheat Manager's parser counts them.
func Count(data []byte) int {
	seen := map[int]bool{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		m := cheatLine.FindSubmatch(sc.Bytes())
		if m == nil {
			continue
		}
		if idx, err := strconv.Atoi(string(m[1])); err == nil {
			seen[idx] = true
		}
	}
	return len(seen)
}

// CountFile returns the number of cheats of a .cht file (0 if it cannot be
// read). It reads only the start of the file when it declares its count
// ("cheats = N", as RetroArch and Cheat Manager write it), otherwise it
// parses the file (at most MaxFileBytes) like Cheat Manager.
func CountFile(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	head := make([]byte, HeaderBytes)
	n, err := io.ReadFull(f, head)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return 0
	}
	head = head[:n]
	if count, ok := DeclaredCount(head); ok {
		return count
	}
	rest, err := io.ReadAll(io.LimitReader(f, MaxFileBytes-int64(n)))
	if err != nil {
		return 0
	}
	return Count(append(head, rest...))
}
