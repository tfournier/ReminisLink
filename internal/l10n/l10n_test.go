package l10n

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// Tests copied from internal/l10n of github.com/tfournier/nextui-cheat-manager.

var templateVar = regexp.MustCompile(`{{\s*\.(\w+)\s*}}`)

func vars(s string) string {
	var out []string
	for _, m := range templateVar.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func loadLocale(t *testing.T, code string) map[string]map[string]string {
	t.Helper()
	data, err := localeFS.ReadFile("locales/active." + code + ".toml")
	if err != nil {
		t.Fatalf("missing locale file for %s: %v", code, err)
	}
	var out map[string]map[string]string
	if err := toml.Unmarshal(data, &out); err != nil {
		t.Fatalf("invalid TOML for %s: %v", code, err)
	}
	return out
}

// Every declared message must be translated in every language, with the
// same template variables, and nothing else may be in the files.
func TestLocalesAreComplete(t *testing.T) {
	ids := map[string]bool{}
	for _, m := range All {
		if ids[m.ID] {
			t.Errorf("duplicate message ID %q", m.ID)
		}
		ids[m.ID] = true
	}
	for _, lang := range Languages {
		loc := loadLocale(t, lang.Code)
		for _, m := range All {
			tr, ok := loc[m.ID]
			if !ok {
				t.Errorf("[%s] missing %q", lang.Code, m.ID)
				continue
			}
			if tr["other"] == "" {
				t.Errorf("[%s] %q has no \"other\" form", lang.Code, m.ID)
			}
			for form, text := range tr {
				if got, want := vars(text), vars(m.Other); got != want {
					t.Errorf("[%s] %q (%s) uses variables {%s}, want {%s}", lang.Code, m.ID, form, got, want)
				}
			}
		}
		for id := range loc {
			if !ids[id] {
				t.Errorf("[%s] unknown message %q (not declared in messages.go)", lang.Code, id)
			}
		}
	}
	entries, err := localeFS.ReadDir("locales")
	if err != nil || len(entries) != len(Languages) {
		t.Errorf("%d locale files for %d languages (%v)", len(entries), len(Languages), err)
	}
}

// The English file must match the defaults declared in Go.
func TestEnglishMatchesDefaults(t *testing.T) {
	loc := loadLocale(t, "en")
	for _, m := range All {
		if loc[m.ID]["other"] != m.Other || loc[m.ID]["one"] != m.One {
			t.Errorf("%q differs between messages.go and active.en.toml", m.ID)
		}
	}
}

func TestLocalize(t *testing.T) {
	if err := Init("fr"); err != nil {
		t.Fatal(err)
	}
	if got := T(StateReady); got != "Prêt" {
		t.Errorf("T(StateReady) = %q", got)
	}
	if got := Tf(ItemName, "Name", "Brick du salon"); got != "Nom de la console : Brick du salon" {
		t.Errorf("Tf(ItemName) = %q", got)
	}
	if got := Tn(MsgForgotAll, 1); got != "1 appareil oublié." {
		t.Errorf("Tn(1) = %q", got)
	}
	if got := Tn(MsgForgotAll, 3); got != "3 appareils oubliés." {
		t.Errorf("Tn(3) = %q", got)
	}
	// Unsupported languages fall back to English.
	if err := SetLanguage("xx"); err != nil {
		t.Fatal(err)
	}
	if got := T(StateReady); got != "Ready" {
		t.Errorf("fallback T(StateReady) = %q", got)
	}
	if l, ok := Lookup("fr"); !ok || l.Name != "Français" || IsSupported("de") {
		t.Errorf("Lookup/IsSupported: %+v %v", l, ok)
	}
}
