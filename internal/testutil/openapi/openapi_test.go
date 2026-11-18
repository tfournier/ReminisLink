package openapi

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/tfournier/ReminisLink/internal/testutil"
)

func contract(t *testing.T) *Spec {
	t.Helper()
	s, err := Load(filepath.Join(testutil.RepoRoot(), "protocol", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestParseYAMLSubset(t *testing.T) {
	src := `# comment
a: 1
b: plain text: with colon
'200':
  description: |
    line one
      indented
    # not a comment

    last
  next: true
list:
  - x
  - { k: v, n: [1, 'two', "th\"ree"], e: {} }
  - name: first
    other: 2
  -
    deep: null
seq:
- one
- two
q: 'it''s'
f: [string, 'null']
url: http://{host}:{port}/api
`
	got, err := parseYAML(src)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"a": 1.0,
		"b": "plain text: with colon",
		"200": map[string]any{
			"description": "line one\n  indented\n# not a comment\n\nlast\n",
			"next":        true,
		},
		"list": []any{
			"x",
			map[string]any{"k": "v", "n": []any{1.0, "two", `th"ree`}, "e": map[string]any{}},
			map[string]any{"name": "first", "other": 2.0},
			map[string]any{"deep": nil},
		},
		"seq": []any{"one", "two"},
		"q":   "it's",
		"f":   []any{"string", "null"},
		"url": "http://{host}:{port}/api",
	}
	if !reflect.DeepEqual(got, want) {
		gj, _ := json.MarshalIndent(got, "", " ")
		t.Fatalf("got %s", gj)
	}
}

func TestParseYAMLRejects(t *testing.T) {
	for _, src := range []string{
		"a: 1\n  b: 2\n",       // bad indentation
		"a: >\n  folded\n",     // folded scalar
		"a: &anchor 1\n",       // anchor
		"a: 1\na: 2\n",         // duplicate key
		"a: { b: 1\n",          // unterminated flow
		"just a scalar line\n", // not a mapping entry
		"a: 'unterminated\n",   // bad quote
	} {
		if _, err := parseYAML(src); err == nil {
			t.Errorf("accepted %q", src)
		}
	}
}

func TestLoadContract(t *testing.T) {
	s := contract(t)
	if lookup(s.root, "info", "title") != "ReminisLink API" {
		t.Errorf("title = %v", lookup(s.root, "info", "title"))
	}
	paths, _ := s.root["paths"].(map[string]any)
	for _, p := range []string{"/hello", "/pair", "/clients/me", "/device", "/systems", "/systems/{systemId}/games", "/games/{gameId}", "/games/{gameId}/art"} {
		if paths[p] == nil {
			t.Errorf("path %s missing", p)
		}
	}
}

// Every JSON example of the contract must pass the validator: this checks
// the YAML reader and the validator against the file Redocly validates.
func TestContractExamplesAreValid(t *testing.T) {
	s := contract(t)
	paths, _ := s.root["paths"].(map[string]any)
	checked := 0
	var names []string
	for p := range paths {
		names = append(names, p)
	}
	sort.Strings(names)
	for _, p := range names {
		ops, _ := paths[p].(map[string]any)
		for method, op := range ops {
			responses, _ := lookup(op, "responses").(map[string]any)
			for code, resp := range responses {
				r, err := s.resolveRef(resp)
				if err != nil {
					t.Fatal(err)
				}
				example := lookup(r, "content", "application/json", "example")
				if example == nil {
					continue
				}
				body, _ := json.Marshal(example)
				status, _ := strconv.Atoi(code)
				h := http.Header{"Content-Type": {"application/json"}}
				if hs, ok := r["headers"].(map[string]any); ok {
					for name, hd := range hs {
						if c, ok := lookup(hd, "schema", "const").(string); ok {
							h.Set(name, c)
						}
					}
				}
				if err := s.ValidateResponse(method, p, status, h, body); err != nil {
					t.Errorf("example: %v", err)
				}
				checked++
			}
		}
	}
	if checked < 40 {
		t.Errorf("only %d examples checked", checked)
	}
}

func TestValidateResponseFailures(t *testing.T) {
	s := contract(t)
	jsonHeader := http.Header{"Content-Type": {"application/json; charset=utf-8"}}
	system := `{"id":"R2FtZQ","label":"G","folder":"G (G)","tag":"G","gameCount":1}`
	ok := `{"systems":[` + system + `]}`
	if err := s.ValidateResponse("GET", "/systems", 200, jsonHeader, []byte(ok)); err != nil {
		t.Fatalf("valid response refused: %v", err)
	}
	for name, tc := range map[string]struct {
		method, path string
		status       int
		header       http.Header
		body         string
		want         string
	}{
		"undeclared status": {"GET", "/systems", 418, jsonHeader, ok, "not declared"},
		"unknown operation": {"POST", "/systems", 200, jsonHeader, ok, "no such operation"},
		"wrong media type":  {"GET", "/systems", 200, http.Header{"Content-Type": {"text/plain"}}, ok, "not in"},
		"invalid JSON":      {"GET", "/systems", 200, jsonHeader, `{"systems":`, "invalid JSON"},
		"missing property":  {"GET", "/systems", 200, jsonHeader, `{"systems":[{"id":"R2FtZQ","label":"G","folder":"G","tag":"G"}]}`, `missing required property "gameCount"`},
		"extra property":    {"GET", "/systems", 200, jsonHeader, `{"systems":[],"extra":1}`, `"extra" is not in the contract`},
		"wrong type":        {"GET", "/systems", 200, jsonHeader, `{"systems":{}}`, "type object, want array"},
		"bad pattern":       {"GET", "/systems", 200, jsonHeader, `{"systems":[{"id":"a/b","label":"G","folder":"G","tag":"G","gameCount":1}]}`, "does not match"},
		"negative count":    {"GET", "/systems", 200, jsonHeader, `{"systems":[{"id":"a","label":"G","folder":"G","tag":"G","gameCount":-1}]}`, "minimum"},
		"non integer":       {"GET", "/systems", 200, jsonHeader, `{"systems":[{"id":"a","label":"G","folder":"G","tag":"G","gameCount":1.5}]}`, "want integer"},
		"bad enum":          {"GET", "/systems", 401, jsonHeader, `{"error":{"code":"nope","message":"m","details":{}}}`, "not in enum"},
		"body on 204":       {"DELETE", "/clients/me", 204, http.Header{}, `{}`, "body not allowed"},
		"bad date":          {"GET", "/hello", 200, jsonHeader, strings.Replace(helloExample, `"2026-11-18T17:00:00Z"`, `"yesterday"`, 1), "date-time"},
		"bad const header":  {"GET", "/games/{gameId}/art", 200, http.Header{"Content-Type": {"image/png"}, "Cache-Control": {"no-store"}}, "png", "Cache-Control"},
	} {
		err := s.ValidateResponse(tc.method, tc.path, tc.status, tc.header, []byte(tc.body))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error with %q", name, err, tc.want)
		}
	}
}

const helloExample = `{"deviceId":"7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10","deviceName":"B","model":"brick","platform":"tg5040","os":"nextui","osVersion":null,"pakVersion":"1","apiVersion":1,"minClientVersion":"1.0.0","serverTime":"2026-11-18T17:00:00Z","pairingOpen":true}`

func TestValidateNullable(t *testing.T) {
	s := contract(t)
	h := http.Header{"Content-Type": {"application/json"}}
	if err := s.ValidateResponse("GET", "/hello", 200, h, []byte(helloExample)); err != nil {
		t.Fatalf("null osVersion: %v", err)
	}
	device := `{"deviceId":"7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10","deviceName":"B","model":"desktop","platform":"tg5040","os":"nextui","osVersion":null,"pakVersion":"1","network":{"ipv4":"10.0.0.1","ssid":null},"battery":null,"storage":[],"uptimeSeconds":0}`
	if err := s.ValidateResponse("GET", "/device", 200, h, []byte(device)); err != nil {
		t.Fatalf("null battery: %v", err)
	}
	bad := strings.Replace(device, `"10.0.0.1"`, `"::1"`, 1)
	if err := s.ValidateResponse("GET", "/device", 200, h, []byte(bad)); err == nil || !strings.Contains(err.Error(), "IPv4") {
		t.Fatalf("IPv6 accepted: %v", err)
	}
	bad = strings.Replace(device, `"battery":null`, `"battery":{"percent":101,"charging":false}`, 1)
	if err := s.ValidateResponse("GET", "/device", 200, h, []byte(bad)); err == nil {
		t.Fatal("battery 101% accepted")
	}
}
