package openapi

import (
	"encoding/json"
	"net/http"
	"os"
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
	system := `{"id":"R2FtZQ","label":"G","folder":"G (G)","tag":"G","gameCount":1,"emulatorInstalled":true}`
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
		"undeclared status":             {"GET", "/systems", 418, jsonHeader, ok, "not declared"},
		"unknown operation":             {"POST", "/systems", 200, jsonHeader, ok, "no such operation"},
		"wrong media type":              {"GET", "/systems", 200, http.Header{"Content-Type": {"text/plain"}}, ok, "not in"},
		"invalid JSON":                  {"GET", "/systems", 200, jsonHeader, `{"systems":`, "invalid JSON"},
		"missing property":              {"GET", "/systems", 200, jsonHeader, `{"systems":[{"id":"R2FtZQ","label":"G","folder":"G","tag":"G"}]}`, `missing required property "gameCount"`},
		"extra property":                {"GET", "/systems", 200, jsonHeader, `{"systems":[],"extra":1}`, `"extra" is not in the contract`},
		"wrong type":                    {"GET", "/systems", 200, jsonHeader, `{"systems":{}}`, "type object, want array"},
		"bad pattern":                   {"GET", "/systems", 200, jsonHeader, `{"systems":[{"id":"a/b","label":"G","folder":"G","tag":"G","gameCount":1,"emulatorInstalled":true}]}`, "does not match"},
		"negative count":                {"GET", "/systems", 200, jsonHeader, `{"systems":[{"id":"a","label":"G","folder":"G","tag":"G","gameCount":-1,"emulatorInstalled":true}]}`, "minimum"},
		"non integer":                   {"GET", "/systems", 200, jsonHeader, `{"systems":[{"id":"a","label":"G","folder":"G","tag":"G","gameCount":1.5,"emulatorInstalled":true}]}`, "want integer"},
		"bad enum":                      {"GET", "/systems", 401, jsonHeader, `{"error":{"code":"nope","message":"m","details":{}}}`, "not in enum"},
		"body on 204":                   {"DELETE", "/clients/me", 204, http.Header{}, `{}`, "body not allowed"},
		"bad date":                      {"GET", "/hello", 200, jsonHeader, strings.Replace(helloExample, `"2026-11-18T17:00:00Z"`, `"yesterday"`, 1), "date-time"},
		"allOf extra property":          {"GET", "/games", 200, jsonHeader, allGames(`,"extra":1`), `"extra" is not in the contract`},
		"allOf missing branch property": {"GET", "/games", 200, jsonHeader, strings.Replace(allGames(""), `"folderPath":null,`, "", 1), `missing required property "folderPath"`},
		"allOf bad branch property":     {"GET", "/games", 200, jsonHeader, strings.Replace(allGames(""), `"folderId":null`, `"folderId":1`, 1), "folderId"},
		"bad const header":              {"GET", "/games/{gameId}/art", 200, http.Header{"Content-Type": {"image/png"}, "Cache-Control": {"no-store"}}, "png", "Cache-Control"},
	} {
		err := s.ValidateResponse(tc.method, tc.path, tc.status, tc.header, []byte(tc.body))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error with %q", name, err, tc.want)
		}
	}
}

// allGames is a GET /games answer with one game (LibraryGame: allOf of
// Game and its own properties), extra appended to the game.
func allGames(extra string) string {
	return `{"games":[{"id":"R2FtZQ","systemId":"U3lz","folderId":null,"folderPath":null,"name":"G","fileName":"G.gba","kind":"file",` +
		`"sizeBytes":1,"modifiedAt":"2026-11-18T17:00:00Z","hasSave":false,"stateCount":0,"lockedStateCount":0,"cheatCount":0,"hasArt":false` +
		extra + `}],"truncated":false,"generatedAt":"2026-11-18T17:00:00Z"}`
}

const helloExample = `{"deviceId":"7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10","deviceName":"B","model":"brick","platform":"tg5040","os":"nextui","osVersion":null,"pakVersion":"1","apiVersion":2,"minClientVersion":"1.0.0","serverTime":"2026-11-18T17:00:00Z","pairingOpen":true}`

func TestValidateNullable(t *testing.T) {
	s := contract(t)
	h := http.Header{"Content-Type": {"application/json"}}
	if err := s.ValidateResponse("GET", "/hello", 200, h, []byte(helloExample)); err != nil {
		t.Fatalf("null osVersion: %v", err)
	}
	device := `{"deviceId":"7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10","deviceName":"B","model":"desktop","platform":"tg5040","os":"nextui","osVersion":null,"pakVersion":"1","network":{"ipv4":"10.0.0.1","ssid":null},"battery":null,"storage":[],"uptimeSeconds":0,"connectedClients":[]}`
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

func TestOperationsWithStatus(t *testing.T) {
	// M13: the write lease and its 423 answer are gone.
	if ops := contract(t).OperationsWithStatus("423"); len(ops) != 0 {
		t.Fatalf("operations with 423: %v", ops)
	}
	ops := contract(t).OperationsWithStatus("409")
	if len(ops) == 0 || !sort.StringsAreSorted(ops) {
		t.Fatalf("operations with 409: %v", ops)
	}
	found := false
	for _, op := range ops {
		if op == "POST /uploads/{uploadId}/complete" {
			found = true
		}
	}
	if !found {
		t.Errorf("POST /uploads/{uploadId}/complete missing from %v", ops)
	}
}

// A LibraryGame (allOf of Game and its own properties) accepts the
// properties of both branches.
func TestValidateAllOf(t *testing.T) {
	h := http.Header{"Content-Type": {"application/json"}}
	if err := contract(t).ValidateResponse("GET", "/games", 200, h, []byte(allGames(""))); err != nil {
		t.Fatal(err)
	}
}

// A property declared by one allOf branch only lifts the implicit "no
// undeclared property" rule of the others: an explicit
// additionalProperties of another branch still applies to it.
func TestValidateAllOfExplicitAdditional(t *testing.T) {
	p := filepath.Join(t.TempDir(), "spec.yaml")
	doc := `openapi: 3.1.0
components:
  schemas:
    Strings:
      allOf:
        - type: object
          properties:
            x: { type: integer }
        - type: object
          additionalProperties: { type: string }
    Closed:
      allOf:
        - type: object
          properties:
            x: { type: integer }
        - type: object
          properties:
            y: { type: integer }
          additionalProperties: false
    Implicit:
      allOf:
        - type: object
          properties:
            x: { type: integer }
        - type: object
          properties:
            y: { type: integer }
`
	if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		schema string
		value  map[string]any
		ok     bool
	}{
		{"Strings", map[string]any{"x": float64(1)}, false}, // x is not a string
		{"Closed", map[string]any{"y": float64(1)}, true},   // declared
		{"Closed", map[string]any{"x": float64(1)}, false},  // x not allowed by the second branch
		{"Implicit", map[string]any{"x": float64(1), "y": float64(2)}, true},
		{"Implicit", map[string]any{"z": float64(1)}, false}, // declared by none
	} {
		err := s.ValidateSchema(tc.schema, tc.value)
		if (err == nil) != tc.ok {
			t.Errorf("%s %v: %v, want ok=%v", tc.schema, tc.value, err, tc.ok)
		}
	}
}

// The example of the event stream (GET /events, M14) is not JSON: each of
// its events must name a declared type, have an increasing id, and carry
// data matching the schema of its type.
func TestEventStreamExampleIsValid(t *testing.T) {
	s := contract(t)
	media := lookup(s.root, "paths", "/events", "get", "responses", "200", "content", "text/event-stream")
	example, ok := lookup(media, "example").(string)
	if !ok || example == "" {
		t.Fatal("no example for GET /events")
	}
	schemas := map[string]string{"hello": "EventHello", "shutdown": "EventShutdown", "device": "EventDevice", "library": "EventLibrary", "job": "Job"}
	// The schemas listed in contentSchema are those of the event types.
	var listed []string
	for _, ref := range lookup(media, "schema", "contentSchema", "oneOf").([]any) {
		r, _ := ref.(map[string]any)["$ref"].(string)
		listed = append(listed, strings.TrimPrefix(r, "#/components/schemas/"))
	}
	var want []string
	for _, name := range schemas {
		want = append(want, name)
	}
	sort.Strings(listed)
	sort.Strings(want)
	if !reflect.DeepEqual(listed, want) {
		t.Errorf("contentSchema lists %v, want %v", listed, want)
	}
	lastID, events := 0, 0
	var types []string
	for _, block := range strings.Split(strings.TrimSpace(example), "\n\n") {
		var id, typ, data string
		for _, line := range strings.Split(block, "\n") {
			if strings.HasPrefix(line, ":") {
				continue // comment (keep-alive)
			}
			field, value, ok := strings.Cut(line, ": ")
			if !ok {
				t.Fatalf("bad line %q", line)
			}
			switch field {
			case "id":
				id = value
			case "event":
				typ = value
			case "data":
				data = value
			default:
				t.Fatalf("unknown field %q", field)
			}
		}
		if typ == "" {
			continue
		}
		n, err := strconv.Atoi(id)
		if err != nil || n <= lastID {
			t.Errorf("event %s: id %q after %d", typ, id, lastID)
		}
		lastID = n
		var v any
		if err := json.Unmarshal([]byte(data), &v); err != nil {
			t.Fatalf("event %s: %v", typ, err)
		}
		if err := s.ValidateSchema(schemas[typ], v); err != nil {
			t.Errorf("event %s: %v", typ, err)
		}
		types = append(types, typ)
		events++
	}
	if events < 3 || types[0] != "hello" {
		t.Errorf("events %v", types)
	}
}
