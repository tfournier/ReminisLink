package nextui

// Platform is what differs between the NextUI platforms for the pak
// (milestone M20): the models behind the DEVICE variable and the power
// supplies of the battery. The paths are the same on every platform
// (MinUI.pak/launch.sh of each platform in NextUI). Sources, NextUI
// (github.com/LoveRetro/NextUI, main b6ec1343, my355 on the branch
// my355-latest a5c755bb): skeleton/SYSTEM/<platform>/paks/MinUI.pak/launch.sh,
// skeleton/SYSTEM/h700/bin/h700-device.sh and
// workspace/<platform>/platform/platform.c (PLAT_getBatteryStatus);
// docs/specs/05-console-pak.md §3.2.
type Platform struct {
	Name string
	// Models maps a DEVICE value exported by MinUI.pak/launch.sh to the
	// API model (Model enum of protocol/openapi.yaml).
	Models map[string]string
	// DefaultModel is the model of a console of this platform whose
	// launcher exported no known DEVICE (IS_NEXT=yes is set): tg5040
	// exports smartpro for any other TrimUI, tg5050 exports DEVICE only
	// for the Smart Pro S.
	DefaultModel string
	// BatterySupply and ChargerSupply are the folders of
	// /sys/class/power_supply NextUI reads (capacity, time_to_full_now;
	// online).
	BatterySupply, ChargerSupply string
}

// Platforms are the NextUI platforms the pak knows.
var Platforms = map[string]Platform{
	"tg5040": {
		Name:          "tg5040",
		Models:        map[string]string{"brick": "brick", "brickpro": "brickpro", "smartpro": "smartpro"},
		DefaultModel:  "smartpro",
		BatterySupply: "axp2202-battery", ChargerSupply: "axp2202-usb",
	},
	"tg5050": {
		Name:          "tg5050",
		Models:        map[string]string{"smartpros": "smartpros"},
		DefaultModel:  "smartpros",
		BatterySupply: "axp2202-battery", ChargerSupply: "axp2202-usb",
	},
	"h700": {
		Name: "h700",
		// h700-device.sh maps the stock RGXX_MODEL to these DEVICE
		// values; the API has a single model for the family.
		Models: map[string]string{
			"h700": "h700", "rg28xx": "h700", "rg34xx": "h700", "rg34xxsp": "h700", "rg35xxplus": "h700",
			"rg35xxh": "h700", "rg35xxpro": "h700", "rg35xxsp": "h700", "rg40xxh": "h700", "rg40xxv": "h700",
			"rgcubexx": "h700", "rgsp": "h700",
		},
		DefaultModel:  "h700",
		BatterySupply: "axp2202-battery", ChargerSupply: "axp2202-usb",
	},
	"my355": {
		Name:          "my355",
		Models:        map[string]string{"my355": "my355"},
		DefaultModel:  "my355",
		BatterySupply: "battery", ChargerSupply: "ac",
	},
}

// PlatformOf returns the platform named name (PLATFORM variable), tg5040
// when unknown or empty.
func PlatformOf(name string) Platform {
	if p, ok := Platforms[name]; ok {
		return p
	}
	return Platforms[DefaultPlatform]
}

// Model returns the API model of a DEVICE value, "" when it is not one of
// this platform (off the console: desktop and headless modes).
func (p Platform) Model(device string) string { return p.Models[device] }

// model returns the model of the console from DEVICE, then IS_NEXT
// (exported by every NextUI launcher), "" off the console.
func (a *Adapter) model() string {
	if m := a.platform.Model(a.env("DEVICE")); m != "" {
		return m
	}
	if a.env("IS_NEXT") == "yes" && a.env("PLATFORM") == a.platform.Name {
		return a.platform.DefaultModel
	}
	return ""
}
