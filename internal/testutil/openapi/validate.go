package openapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"mime"
	"net"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Spec is a loaded OpenAPI document.
type Spec struct {
	root map[string]any

	mu      sync.Mutex
	regexps map[string]*regexp.Regexp
}

// schemaKeywords are the keywords the validator understands; the others
// that may appear in a schema only document it.
var schemaKeywords = map[string]bool{
	"$ref": true, "type": true, "enum": true, "const": true, "properties": true, "required": true,
	"additionalProperties": true, "items": true, "minimum": true, "maximum": true, "minLength": true,
	"maxLength": true, "minItems": true, "maxItems": true, "pattern": true, "format": true, "oneOf": true, "anyOf": true, "allOf": true,
	// Documentation only.
	"description": true, "example": true, "examples": true, "default": true, "title": true,
	"contentMediaType": true, "deprecated": true,
}

// Load reads an OpenAPI 3.1 document. It fails on schema keywords the
// validator does not support.
func Load(path string) (*Spec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	doc, err := parseYAML(string(data))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	root, ok := doc.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: not a mapping", path)
	}
	s := &Spec{root: root, regexps: map[string]*regexp.Regexp{}}
	schemas, _ := lookup(root, "components", "schemas").(map[string]any)
	if len(schemas) == 0 {
		return nil, fmt.Errorf("%s: no components.schemas", path)
	}
	for name, sc := range schemas {
		if err := checkKeywords(sc, "#/components/schemas/"+name); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func checkKeywords(node any, where string) error {
	sc, ok := node.(map[string]any)
	if !ok {
		return fmt.Errorf("%s: schema is not a mapping", where)
	}
	for k, v := range sc {
		if !schemaKeywords[k] {
			return fmt.Errorf("%s: unsupported schema keyword %q", where, k)
		}
		switch k {
		case "properties":
			props, _ := v.(map[string]any)
			for name, p := range props {
				if err := checkKeywords(p, where+"/properties/"+name); err != nil {
					return err
				}
			}
		case "items":
			if err := checkKeywords(v, where+"/items"); err != nil {
				return err
			}
		case "additionalProperties":
			if _, isBool := v.(bool); !isBool {
				if err := checkKeywords(v, where+"/additionalProperties"); err != nil {
					return err
				}
			}
		case "oneOf", "anyOf", "allOf":
			list, _ := v.([]any)
			for i, item := range list {
				if err := checkKeywords(item, fmt.Sprintf("%s/%s/%d", where, k, i)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func lookup(node any, keys ...string) any {
	for _, k := range keys {
		m, ok := node.(map[string]any)
		if !ok {
			return nil
		}
		node = m[k]
	}
	return node
}

// resolveRef follows "#/..." references.
func (s *Spec) resolveRef(node any) (map[string]any, error) {
	for i := 0; i < 32; i++ {
		m, ok := node.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("not a mapping")
		}
		ref, ok := m["$ref"].(string)
		if !ok {
			return m, nil
		}
		if !strings.HasPrefix(ref, "#/") {
			return nil, fmt.Errorf("external $ref %q not supported", ref)
		}
		node = lookup(s.root, strings.Split(ref[2:], "/")...)
		if node == nil {
			return nil, fmt.Errorf("unresolved $ref %q", ref)
		}
	}
	return nil, fmt.Errorf("$ref loop")
}

// ValidateResponse checks a response of the operation method + path (the
// path as written in the contract, relative to /api/v1, e.g.
// "/systems/{systemId}/games"): the status must be declared, the media
// type must be one of the declared ones (no body when none is declared),
// a JSON body must match its schema (unknown properties are errors unless
// the schema allows them), and declared headers with a const must have
// that value.
func (s *Spec) ValidateResponse(method, path string, status int, header http.Header, body []byte) error {
	op := lookup(s.root, "paths", path, strings.ToLower(method))
	if op == nil {
		return fmt.Errorf("%s %s: no such operation in the contract", method, path)
	}
	responses, _ := lookup(op, "responses").(map[string]any)
	resp, ok := responses[strconv.Itoa(status)]
	if !ok {
		if resp, ok = responses["default"]; !ok {
			return fmt.Errorf("%s %s: status %d is not declared", method, path, status)
		}
	}
	r, err := s.resolveRef(resp)
	if err != nil {
		return fmt.Errorf("%s %s %d: %w", method, path, status, err)
	}
	if headers, ok := r["headers"].(map[string]any); ok {
		for name, h := range headers {
			hd, err := s.resolveRef(h)
			if err != nil {
				return err
			}
			if c, ok := lookup(hd, "schema", "const").(string); ok && header.Get(name) != c {
				return fmt.Errorf("%s %s %d: header %s = %q, want %q", method, path, status, name, header.Get(name), c)
			}
		}
	}
	content, _ := r["content"].(map[string]any)
	if len(content) == 0 {
		if len(body) != 0 {
			return fmt.Errorf("%s %s %d: body not allowed, got %d bytes", method, path, status, len(body))
		}
		return nil
	}
	mediaType, _, err := mime.ParseMediaType(header.Get("Content-Type"))
	if err != nil {
		return fmt.Errorf("%s %s %d: bad Content-Type %q", method, path, status, header.Get("Content-Type"))
	}
	media, ok := content[mediaType]
	if !ok {
		var declared []string
		for k := range content {
			declared = append(declared, k)
		}
		sort.Strings(declared)
		return fmt.Errorf("%s %s %d: Content-Type %s not in %v", method, path, status, mediaType, declared)
	}
	if mediaType != "application/json" {
		return nil
	}
	schema := lookup(media, "schema")
	if schema == nil {
		return fmt.Errorf("%s %s %d: no schema", method, path, status)
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	var v any
	if err := dec.Decode(&v); err != nil {
		return fmt.Errorf("%s %s %d: invalid JSON: %w", method, path, status, err)
	}
	if dec.More() {
		return fmt.Errorf("%s %s %d: data after the JSON value", method, path, status)
	}
	if err := s.validate(schema, v, "$"); err != nil {
		return fmt.Errorf("%s %s %d: %w", method, path, status, err)
	}
	return nil
}

// ValidateSchema checks a JSON value (decoded with encoding/json) against
// a schema of components.schemas.
func (s *Spec) ValidateSchema(name string, v any) error {
	return s.validate(map[string]any{"$ref": "#/components/schemas/" + name}, v, "$")
}

func jsonType(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64:
		if x == math.Trunc(x) {
			return "integer"
		}
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return fmt.Sprintf("%T", v)
}

func typeMatches(want, got string) bool {
	return want == got || (want == "number" && got == "integer")
}

func (s *Spec) validate(node any, v any, at string) error {
	return s.validateIn(node, v, at, nil)
}

// validateIn validates v against node. siblings are the properties
// declared by the other branches of an enclosing allOf: for the implicit
// strict rule, a property one branch does not declare is not an extra
// property when another branch declares it (and validates it); an
// explicit additionalProperties still applies to it. Properties declared
// by none are still refused (strict by default).
func (s *Spec) validateIn(node any, v any, at string, siblings map[string]bool) error {
	sc, err := s.resolveRef(node)
	if err != nil {
		return fmt.Errorf("%s: %w", at, err)
	}
	all, _ := sc["allOf"].([]any)
	if len(all) > 0 {
		allowed := map[string]bool{}
		for k := range siblings {
			allowed[k] = true
		}
		for _, sub := range append([]any{sc}, all...) {
			r, err := s.resolveRef(sub)
			if err != nil {
				return fmt.Errorf("%s: %w", at, err)
			}
			props, _ := r["properties"].(map[string]any)
			for k := range props {
				allowed[k] = true
			}
		}
		siblings = allowed
	}
	got := jsonType(v)
	switch t := sc["type"].(type) {
	case string:
		if !typeMatches(t, got) {
			return fmt.Errorf("%s: type %s, want %s", at, got, t)
		}
	case []any:
		ok := false
		for _, x := range t {
			if ts, _ := x.(string); typeMatches(ts, got) {
				ok = true
			}
		}
		if !ok {
			return fmt.Errorf("%s: type %s, want one of %v", at, got, t)
		}
	}
	if enum, ok := sc["enum"].([]any); ok {
		found := false
		for _, e := range enum {
			if equalJSON(e, v) {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("%s: %v not in enum %v", at, v, enum)
		}
	}
	if c, ok := sc["const"]; ok && !equalJSON(c, v) {
		return fmt.Errorf("%s: %v, want const %v", at, v, c)
	}
	switch x := v.(type) {
	case string:
		if err := s.validateString(sc, x, at); err != nil {
			return err
		}
	case float64:
		if m, ok := sc["minimum"].(float64); ok && x < m {
			return fmt.Errorf("%s: %v < minimum %v", at, x, m)
		}
		if m, ok := sc["maximum"].(float64); ok && x > m {
			return fmt.Errorf("%s: %v > maximum %v", at, x, m)
		}
	case []any:
		if m, ok := sc["minItems"].(float64); ok && float64(len(x)) < m {
			return fmt.Errorf("%s: %d items < minItems %v", at, len(x), m)
		}
		if m, ok := sc["maxItems"].(float64); ok && float64(len(x)) > m {
			return fmt.Errorf("%s: %d items > maxItems %v", at, len(x), m)
		}
		if items, ok := sc["items"]; ok {
			for i, item := range x {
				if err := s.validate(items, item, fmt.Sprintf("%s[%d]", at, i)); err != nil {
					return err
				}
			}
		}
	case map[string]any:
		if err := s.validateObject(sc, x, at, siblings); err != nil {
			return err
		}
	}
	for _, sub := range all {
		if err := s.validateIn(sub, v, at, siblings); err != nil {
			return err
		}
	}
	if anyOf, ok := sc["anyOf"].([]any); ok {
		var errs []string
		for _, sub := range anyOf {
			err := s.validate(sub, v, at)
			if err == nil {
				errs = nil
				break
			}
			errs = append(errs, err.Error())
		}
		if errs != nil {
			return fmt.Errorf("%s: no anyOf branch matches: %s", at, strings.Join(errs, "; "))
		}
	}
	if oneOf, ok := sc["oneOf"].([]any); ok {
		matches := 0
		var errs []string
		for _, sub := range oneOf {
			if err := s.validate(sub, v, at); err == nil {
				matches++
			} else {
				errs = append(errs, err.Error())
			}
		}
		if matches != 1 {
			return fmt.Errorf("%s: %d oneOf branches match, want 1 (%s)", at, matches, strings.Join(errs, "; "))
		}
	}
	return nil
}

func (s *Spec) validateString(sc map[string]any, x, at string) error {
	n := float64(utf8.RuneCountInString(x))
	if m, ok := sc["minLength"].(float64); ok && n < m {
		return fmt.Errorf("%s: length %v < minLength %v", at, n, m)
	}
	if m, ok := sc["maxLength"].(float64); ok && n > m {
		return fmt.Errorf("%s: length %v > maxLength %v", at, n, m)
	}
	if p, ok := sc["pattern"].(string); ok {
		re, err := s.regexp(p)
		if err != nil {
			return fmt.Errorf("%s: pattern %q: %w", at, p, err)
		}
		if !re.MatchString(x) {
			return fmt.Errorf("%s: %q does not match %q", at, x, p)
		}
	}
	switch sc["format"] {
	case "date-time":
		if _, err := time.Parse(time.RFC3339, x); err != nil {
			return fmt.Errorf("%s: %q is not a date-time", at, x)
		}
	case "ipv4":
		ip := net.ParseIP(x)
		if ip == nil || ip.To4() == nil || strings.Contains(x, ":") {
			return fmt.Errorf("%s: %q is not an IPv4 address", at, x)
		}
	}
	return nil
}

func (s *Spec) validateObject(sc map[string]any, x map[string]any, at string, siblings map[string]bool) error {
	if req, ok := sc["required"].([]any); ok {
		for _, r := range req {
			name, _ := r.(string)
			if _, ok := x[name]; !ok {
				return fmt.Errorf("%s: missing required property %q", at, name)
			}
		}
	}
	props, _ := sc["properties"].(map[string]any)
	extra := sc["additionalProperties"]
	keys := make([]string, 0, len(x))
	for k := range x {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if p, ok := props[k]; ok {
			if err := s.validate(p, x[k], at+"."+k); err != nil {
				return err
			}
			continue
		}
		switch e := extra.(type) {
		case bool:
			if !e {
				return fmt.Errorf("%s: property %q not allowed", at, k)
			}
		case nil:
			// Strict by default: the pak must not send fields the contract
			// does not describe. A property declared by another allOf
			// branch is described (and validated there); only this
			// implicit rule gives way, never an explicit
			// additionalProperties.
			if siblings[k] {
				continue
			}
			if props != nil || sc["type"] == "object" {
				return fmt.Errorf("%s: property %q is not in the contract", at, k)
			}
		default:
			if err := s.validate(e, x[k], at+"."+k); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Spec) regexp(p string) (*regexp.Regexp, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if re, ok := s.regexps[p]; ok {
		return re, nil
	}
	re, err := regexp.Compile(p)
	if err != nil {
		return nil, err
	}
	s.regexps[p] = re
	return re, nil
}

func equalJSON(a, b any) bool {
	ja, err1 := json.Marshal(a)
	jb, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && bytes.Equal(ja, jb)
}

// OperationsWithStatus returns the operations that declare a response
// with this status code, as "METHOD /path" (path relative to /api/v1, as
// written in the contract), sorted.
func (s *Spec) OperationsWithStatus(status string) []string {
	var out []string
	paths, _ := lookup(s.root, "paths").(map[string]any)
	for p, item := range paths {
		ops, _ := item.(map[string]any)
		for method, op := range ops {
			responses, _ := lookup(op, "responses").(map[string]any)
			if _, ok := responses[status]; ok {
				out = append(out, strings.ToUpper(method)+" "+p)
			}
		}
	}
	sort.Strings(out)
	return out
}
