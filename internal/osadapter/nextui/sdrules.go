package nextui

import (
	"path/filepath"

	"github.com/tfournier/ReminisLink/internal/osadapter"
)

// SDRules are the protected places of the SD card browser (M23,
// docs/specs/05-console-pak.md §3.4).
func (a *Adapter) SDRules() osadapter.SDRules {
	if a.layout.SDRules != nil {
		return a.layout.SDRules(a.roots)
	}
	return DefaultSDRules(a.roots)
}

// DefaultSDRules are the NextUI rules: every hidden entry at the root of
// the card (.system, .userdata, .tmp_update...) is read only; the main
// folders (Roms, Saves, Cheats, Bios, Tools, Emus and their platform
// folders, Screenshots) are never moved to the trash.
func DefaultSDRules(r osadapter.Roots) osadapter.SDRules {
	var roots []string
	for _, p := range []string{r.Roms, r.Saves, r.Cheats, filepath.Join(r.SD, "Bios"), r.Tools, r.Emus, r.Screenshots} {
		if p != "" {
			roots = append(roots, p)
		}
	}
	if r.Tools != "" {
		roots = append(roots, filepath.Dir(r.Tools))
	}
	if r.Emus != "" {
		roots = append(roots, filepath.Dir(r.Emus))
	}
	return osadapter.SDRules{HiddenRootSystem: true, Roots: roots}
}
