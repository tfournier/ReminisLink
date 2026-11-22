package knulli

import (
	"path/filepath"

	"github.com/tfournier/ReminisLink/internal/osadapter"
)

// LauncherName is the launcher script of the pak in roms/tools.
const LauncherName = DataDirName + ".sh"

// sdRules are the protected places of the SD card browser on Knulli (M23,
// docs/specs/05-console-pak.md §3.4): /userdata/system (settings, logs,
// the ReminisLink data) and the hidden entries of /userdata are read
// only; roms, saves, bios, cheats, screenshots and roms/tools are never
// moved to the trash; the folder of the pak and its launcher are
// ReminisLink's.
func sdRules(r osadapter.Roots) osadapter.SDRules {
	rules := osadapter.SDRules{HiddenRootSystem: true}
	if r.Userdata != "" {
		rules.System = append(rules.System, r.Userdata)
	}
	for _, p := range []string{r.Roms, r.Saves, filepath.Join(r.SD, "bios"), r.Cheats, r.Screenshots, filepath.Join(r.Roms, "tools")} {
		if p != "" {
			rules.Roots = append(rules.Roots, p)
		}
	}
	if r.Pak != "" {
		rules.Data = append(rules.Data, filepath.Join(filepath.Dir(r.Pak), LauncherName))
	}
	return rules
}
