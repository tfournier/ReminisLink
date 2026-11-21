package nextui

import (
	"os"
	"path/filepath"
	"testing"
)

// writeSupply writes the sysfs files of a fake power supply.
func writeSupply(t *testing.T, dir, supply string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, supply), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, v := range files {
		if err := os.WriteFile(filepath.Join(dir, supply, name), []byte(v+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// Each platform reports the model its MinUI.pak/launch.sh exports, and
// reads the battery from its own power supplies (platform.c).
func TestPlatforms(t *testing.T) {
	for _, tc := range []struct {
		platform, device, isNext string
		model                    string
	}{
		{"tg5040", "brick", "yes", "brick"},
		{"tg5040", "brickpro", "yes", "brickpro"},
		{"tg5040", "", "yes", "smartpro"},
		{"tg5050", "smartpros", "yes", "smartpros"},
		// tg5050 exports DEVICE only for the Smart Pro S.
		{"tg5050", "", "yes", "smartpros"},
		{"h700", "rg35xxplus", "yes", "h700"},
		{"h700", "rg40xxv", "yes", "h700"},
		{"h700", "", "yes", "h700"},
		{"my355", "my355", "yes", "my355"},
		// Off the console: no IS_NEXT, no known DEVICE.
		{"h700", "", "", ModelDesktop},
		{"my355", "toaster", "", ModelDesktop},
		// A DEVICE of another platform is not trusted.
		{"my355", "brick", "", ModelDesktop},
	} {
		env := map[string]string{"PLATFORM": tc.platform, "DEVICE": tc.device, "IS_NEXT": tc.isNext}
		a := New(RootsForSD(t.TempDir(), tc.platform), func(k string) string { return env[k] })
		info, err := a.DeviceInfo()
		if err != nil || info.Model != tc.model || info.Platform != tc.platform || info.OS != OSName {
			t.Errorf("%+v: info = %+v, %v", tc, info, err)
		}
	}
}

func TestPlatformBattery(t *testing.T) {
	for _, tc := range []struct {
		platform, device, bat, usb string
	}{
		{"tg5040", "brick", "axp2202-battery", "axp2202-usb"},
		{"tg5050", "smartpros", "axp2202-battery", "axp2202-usb"},
		{"h700", "rg35xxplus", "axp2202-battery", "axp2202-usb"},
		{"my355", "my355", "battery", "ac"},
	} {
		dir := t.TempDir()
		writeSupply(t, dir, tc.bat, map[string]string{"capacity": "64", "time_to_full_now": "1200"})
		writeSupply(t, dir, tc.usb, map[string]string{"online": "1"})
		// A supply of another platform is ignored.
		writeSupply(t, dir, "decoy", map[string]string{"capacity": "5"})
		env := map[string]string{"PLATFORM": tc.platform, "DEVICE": tc.device, "IS_NEXT": "yes"}
		a := New(RootsForSD(t.TempDir(), tc.platform), func(k string) string { return env[k] })
		a.powerSupply = dir
		b := a.Battery()
		if b == nil || b.Percent != 64 || !b.Charging {
			t.Errorf("%s: battery = %+v", tc.platform, b)
		}
	}
}

// my355's launcher exports no ROMS_PATH (NextUI branch my355-latest,
// skeleton/SYSTEM/my355/paks/MinUI.pak/launch.sh): the default applies.
// The other folders follow PLATFORM.
func TestRootsPerPlatform(t *testing.T) {
	for _, p := range []string{"tg5040", "tg5050", "h700", "my355"} {
		env := map[string]string{"PLATFORM": p, "SDCARD_PATH": "/mnt/SDCARD"}
		r := RootsFromEnv(func(k string) string { return env[k] })
		if r.Roms != "/mnt/SDCARD/Roms" || r.Saves != "/mnt/SDCARD/Saves" ||
			r.System != "/mnt/SDCARD/.system/"+p || r.Tools != "/mnt/SDCARD/Tools/"+p ||
			r.Emus != "/mnt/SDCARD/Emus/"+p || r.Userdata != "/mnt/SDCARD/.userdata/"+p ||
			r.Logs != "/mnt/SDCARD/.userdata/"+p+"/logs" || r.Data != "/mnt/SDCARD/.userdata/shared/ReminisLink" {
			t.Errorf("%s: roots = %+v", p, r)
		}
	}
	if PlatformOf("unknown").Name != DefaultPlatform || PlatformOf("").Name != DefaultPlatform {
		t.Error("PlatformOf of an unknown platform")
	}
}
