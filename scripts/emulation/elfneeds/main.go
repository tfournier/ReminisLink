// Command elfneeds prints what a dynamically linked ELF binary needs from
// the system it runs on (milestone M20): its shared libraries (DT_NEEDED)
// and the newest glibc symbol version it uses. scripts/emulation/run.sh
// compares that version with the glibc of each platform.
//
//	go run ./scripts/emulation/elfneeds build/reminislink
//
// Output, one item per line:
//
//	needed <library>
//	glibc <version>    (absent for a static binary)
package main

import (
	"debug/elf"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: elfneeds <ELF binary>")
		os.Exit(2)
	}
	f, err := elf.Open(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "elfneeds:", err)
		os.Exit(1)
	}
	defer f.Close()
	libs, err := f.ImportedLibraries()
	if err != nil {
		fmt.Fprintln(os.Stderr, "elfneeds:", err)
		os.Exit(1)
	}
	for _, l := range libs {
		fmt.Println("needed", l)
	}
	needs, err := f.DynamicVersionNeeds()
	if err != nil {
		return // static binary: no version needs
	}
	newest := ""
	for _, n := range needs {
		for _, v := range n.Needs {
			if ver, ok := strings.CutPrefix(v.Dep, "GLIBC_"); ok && newer(ver, newest) {
				newest = ver
			}
		}
	}
	if newest != "" {
		fmt.Println("glibc", newest)
	}
}

// newer reports whether version a (e.g. "2.34") is after b ("" is before
// any version).
func newer(a, b string) bool {
	if b == "" {
		return true
	}
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(pa), len(pb)); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x > y
		}
	}
	return false
}
