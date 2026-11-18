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

// MaxFileBytes is the largest .cht file read.
const MaxFileBytes = 8 << 20

// cheatLine is copied from Cheat Manager.
var cheatLine = regexp.MustCompile(`^\s*cheat(\d+)_([A-Za-z0-9_]+)\s*=(.*)$`)

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
// read). Files larger than MaxFileBytes are counted on their first
// MaxFileBytes.
func CountFile(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxFileBytes))
	if err != nil {
		return 0
	}
	return Count(data)
}
