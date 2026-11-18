// This file is copied from Cheat Manager, internal/cheats/cht.go of
// github.com/tfournier/nextui-cheat-manager (commit 918e419a), same author.
// Changes: the cheat line pattern is shared with count.go; Load and Save
// are replaced by the Service of service.go (atomic .bak, ETag).

package cheats

// RetroArch .cht format:
//
//	cheats = 2
//
//	cheat0_desc = "Infinite health"
//	cheat0_code = "82000F20+0063"
//	cheat0_enable = false
//
// Unknown keys (cheatN_handler, cheatN_address, ...) are preserved and cheats
// are renumbered on write.

import (
	"bufio"
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Extra is a cheat key that this tool does not edit but must keep.
type Extra struct {
	Key   string
	Value string
}

// Cheat is a single cheat entry.
type Cheat struct {
	Desc   string
	Code   string
	Enable bool
	Extras []Extra // other keys, in their original order
}

// File is the content of a .cht file.
type File struct {
	Header []string // non-cheat lines (comments...), preserved as-is
	Cheats []Cheat
}

// countKeyLine is the "cheats = ..." line of a file, rewritten on save.
var countKeyLine = regexp.MustCompile(`^\s*cheats\s*=`)

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	return s
}

// clean makes a value safe for the .cht format (no double quotes, no line breaks).
func clean(s string) string {
	s = strings.NewReplacer("\r", " ", "\n", " ", "\t", " ", `"`, "'").Replace(s)
	return strings.TrimSpace(s)
}

func isOn(v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	return v == "true" || v == "1"
}

// Parse decodes the content of a .cht file. It never fails: unknown lines are
// kept in the header.
func Parse(data []byte) *File {
	cf := &File{}
	byIdx := map[int]*Cheat{}
	var order []int

	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if m := cheatLine.FindStringSubmatch(line); m != nil {
			idx, _ := strconv.Atoi(m[1])
			c, ok := byIdx[idx]
			if !ok {
				c = &Cheat{}
				byIdx[idx] = c
				order = append(order, idx)
			}
			val := unquote(m[3])
			switch m[2] {
			case "desc":
				c.Desc = val
			case "code":
				c.Code = val
			case "enable":
				c.Enable = isOn(val)
			default:
				c.Extras = append(c.Extras, Extra{m[2], val})
			}
			continue
		}
		if countKeyLine.MatchString(line) || strings.TrimSpace(line) == "" {
			continue
		}
		cf.Header = append(cf.Header, line)
	}
	sort.Ints(order)
	for _, i := range order {
		cf.Cheats = append(cf.Cheats, *byIdx[i])
	}
	return cf
}

// Bytes encodes the file, renumbering cheats from 0.
func (cf *File) Bytes() []byte {
	var b bytes.Buffer
	for _, h := range cf.Header {
		b.WriteString(h + "\n")
	}
	fmt.Fprintf(&b, "cheats = %d\n\n", len(cf.Cheats))
	for i, c := range cf.Cheats {
		fmt.Fprintf(&b, "cheat%d_desc = \"%s\"\n", i, clean(c.Desc))
		fmt.Fprintf(&b, "cheat%d_code = \"%s\"\n", i, clean(c.Code))
		fmt.Fprintf(&b, "cheat%d_enable = %t\n", i, c.Enable)
		for _, e := range c.Extras {
			fmt.Fprintf(&b, "cheat%d_%s = \"%s\"\n", i, e.Key, clean(e.Value))
		}
		b.WriteString("\n")
	}
	return b.Bytes()
}
