// Package openapi validates HTTP responses against protocol/openapi.yaml in
// tests, without third-party dependencies.
//
// It reads the subset of YAML the contract uses (block mappings and
// sequences, single-line flow collections, plain and quoted scalars,
// literal block scalars, comment lines) and checks the subset of JSON
// Schema it uses ($ref, type, enum, const, properties, required,
// additionalProperties, items, minimum, maximum, minLength, maxLength,
// minItems, maxItems,
// pattern, format date-time and ipv4, oneOf, anyOf, allOf). Anything else
// in the file makes Load fail, so a contract change that needs more is
// noticed instead of being silently ignored.
package openapi

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type yamlLine struct {
	num    int // 1-based
	indent int
	text   string // without the indentation
}

type yamlParser struct {
	lines []yamlLine
	pos   int
}

// parseYAML parses a YAML document of the supported subset. Mappings are
// map[string]any, sequences []any, numbers float64.
func parseYAML(src string) (any, error) {
	p := &yamlParser{}
	for i, raw := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		if strings.Contains(raw, "\t") && strings.TrimLeft(raw, " ") != "" && strings.HasPrefix(strings.TrimLeft(raw, " "), "\t") {
			return nil, fmt.Errorf("line %d: tab indentation", i+1)
		}
		text := strings.TrimLeft(raw, " ")
		p.lines = append(p.lines, yamlLine{num: i + 1, indent: len(raw) - len(text), text: strings.TrimRight(text, " ")})
	}
	v, err := p.node(0)
	if err != nil {
		return nil, err
	}
	p.skip()
	if p.pos < len(p.lines) {
		return nil, fmt.Errorf("line %d: unexpected content", p.lines[p.pos].num)
	}
	return v, nil
}

func isBlankOrComment(l yamlLine) bool { return l.text == "" || strings.HasPrefix(l.text, "#") }

// skip moves past blank and comment lines (outside block scalars).
func (p *yamlParser) skip() {
	for p.pos < len(p.lines) && isBlankOrComment(p.lines[p.pos]) {
		p.pos++
	}
}

func isSeqItem(text string) bool { return text == "-" || strings.HasPrefix(text, "- ") }

// node parses the block node starting at the next line, which must be
// indented at least minIndent.
func (p *yamlParser) node(minIndent int) (any, error) {
	p.skip()
	if p.pos >= len(p.lines) || p.lines[p.pos].indent < minIndent {
		return nil, nil
	}
	l := p.lines[p.pos]
	if isSeqItem(l.text) {
		return p.seq(l.indent)
	}
	return p.mapping(l.indent)
}

func (p *yamlParser) mapping(indent int) (any, error) {
	m := map[string]any{}
	for {
		p.skip()
		if p.pos >= len(p.lines) {
			return m, nil
		}
		l := p.lines[p.pos]
		if l.indent < indent || (l.indent == indent && isSeqItem(l.text)) {
			return m, nil
		}
		if l.indent > indent {
			return nil, fmt.Errorf("line %d: bad indentation", l.num)
		}
		key, rest, ok := splitKey(l.text)
		if !ok {
			return nil, fmt.Errorf("line %d: expected \"key: value\"", l.num)
		}
		if _, dup := m[key]; dup {
			return nil, fmt.Errorf("line %d: duplicate key %q", l.num, key)
		}
		p.pos++
		v, err := p.value(rest, indent, l.num)
		if err != nil {
			return nil, err
		}
		m[key] = v
	}
}

func (p *yamlParser) seq(indent int) (any, error) {
	list := []any{}
	for {
		p.skip()
		if p.pos >= len(p.lines) {
			return list, nil
		}
		l := p.lines[p.pos]
		if l.indent != indent || !isSeqItem(l.text) {
			if l.indent > indent {
				return nil, fmt.Errorf("line %d: bad indentation", l.num)
			}
			return list, nil
		}
		item := strings.TrimPrefix(strings.TrimPrefix(l.text, "-"), " ")
		item = strings.TrimLeft(item, " ")
		switch {
		case item == "":
			p.pos++
			v, err := p.node(indent + 1)
			if err != nil {
				return nil, err
			}
			list = append(list, v)
		case item[0] == '{' || item[0] == '[':
			p.pos++
			v, err := parseFlow(item, l.num)
			if err != nil {
				return nil, err
			}
			list = append(list, v)
		default:
			if _, _, ok := splitKey(item); ok {
				// "- key: value" starts a mapping indented after "- ".
				p.lines[p.pos] = yamlLine{num: l.num, indent: indent + 2, text: item}
				v, err := p.mapping(indent + 2)
				if err != nil {
					return nil, err
				}
				list = append(list, v)
				continue
			}
			p.pos++
			v, err := scalar(item, l.num)
			if err != nil {
				return nil, err
			}
			list = append(list, v)
		}
	}
}

// value parses what follows "key:" on a line at indent.
func (p *yamlParser) value(rest string, indent, num int) (any, error) {
	switch {
	case rest == "":
		p.skip()
		if p.pos >= len(p.lines) {
			return nil, nil
		}
		next := p.lines[p.pos]
		if next.indent > indent || (next.indent == indent && isSeqItem(next.text)) {
			return p.node(next.indent)
		}
		return nil, nil
	case rest == "|" || rest == "|-" || rest == "|+":
		return p.blockScalar(indent, rest), nil
	case strings.HasPrefix(rest, ">"):
		return nil, fmt.Errorf("line %d: folded block scalars are not supported", num)
	case rest[0] == '{' || rest[0] == '[':
		return parseFlow(rest, num)
	case rest[0] == '&' || rest[0] == '*' || rest[0] == '!':
		return nil, fmt.Errorf("line %d: anchors, aliases and tags are not supported", num)
	}
	return scalar(rest, num)
}

// blockScalar reads the lines of a literal block scalar ("|").
func (p *yamlParser) blockScalar(parentIndent int, indicator string) string {
	var lines []string
	blockIndent := -1
	for p.pos < len(p.lines) {
		l := p.lines[p.pos]
		if l.text != "" && l.indent <= parentIndent {
			break
		}
		if l.text != "" && blockIndent < 0 {
			blockIndent = l.indent
		}
		if l.text == "" {
			lines = append(lines, "")
		} else {
			lines = append(lines, strings.Repeat(" ", l.indent-blockIndent)+l.text)
		}
		p.pos++
	}
	text := strings.Join(lines, "\n")
	switch indicator {
	case "|-":
		return strings.TrimRight(text, "\n")
	case "|+":
		return text + "\n"
	}
	return strings.TrimRight(text, "\n") + "\n"
}

// splitKey splits "key: value" (or "key:") at the first ": " outside
// quotes.
func splitKey(text string) (key, rest string, ok bool) {
	if text == "" {
		return "", "", false
	}
	if text[0] == '\'' || text[0] == '"' {
		s, n, err := quoted(text)
		if err != nil || n >= len(text) || text[n] != ':' {
			return "", "", false
		}
		after := text[n+1:]
		if after != "" && after[0] != ' ' {
			return "", "", false
		}
		return s, strings.TrimSpace(after), true
	}
	if text[0] == '{' || text[0] == '[' || text[0] == '-' && isSeqItem(text) {
		return "", "", false
	}
	if i := strings.Index(text, ": "); i > 0 {
		return text[:i], strings.TrimSpace(text[i+2:]), true
	}
	if strings.HasSuffix(text, ":") && len(text) > 1 {
		return text[:len(text)-1], "", true
	}
	return "", "", false
}

// quoted decodes the quoted string at the start of s and returns it with
// the number of bytes it used.
func quoted(s string) (string, int, error) {
	q := s[0]
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		c := s[i]
		switch {
		case q == '\'' && c == '\'':
			if i+1 < len(s) && s[i+1] == '\'' {
				b.WriteByte('\'')
				i++
				continue
			}
			return b.String(), i + 1, nil
		case q == '"' && c == '"':
			return b.String(), i + 1, nil
		case q == '"' && c == '\\':
			if i+1 >= len(s) {
				return "", 0, fmt.Errorf("unterminated escape")
			}
			i++
			switch s[i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case '"', '\\', '/':
				b.WriteByte(s[i])
			case 'u':
				if i+4 >= len(s) {
					return "", 0, fmt.Errorf("bad \\u escape")
				}
				r, err := strconv.ParseUint(s[i+1:i+5], 16, 32)
				if err != nil {
					return "", 0, fmt.Errorf("bad \\u escape")
				}
				b.WriteRune(rune(r))
				i += 4
			default:
				return "", 0, fmt.Errorf("unsupported escape \\%c", s[i])
			}
		default:
			b.WriteByte(c)
		}
	}
	return "", 0, fmt.Errorf("unterminated string")
}

var (
	intRe   = regexp.MustCompile(`^-?[0-9]+$`)
	floatRe = regexp.MustCompile(`^-?[0-9]+\.[0-9]+$`)
)

// scalar resolves a block-context scalar.
func scalar(s string, num int) (any, error) {
	if s[0] == '\'' || s[0] == '"' {
		v, n, err := quoted(s)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", num, err)
		}
		if strings.TrimSpace(s[n:]) != "" {
			return nil, fmt.Errorf("line %d: text after a quoted string", num)
		}
		return v, nil
	}
	return plain(s), nil
}

func plain(s string) any {
	switch s {
	case "true":
		return true
	case "false":
		return false
	case "null", "~", "":
		return nil
	}
	if intRe.MatchString(s) || floatRe.MatchString(s) {
		f, err := strconv.ParseFloat(s, 64)
		if err == nil {
			return f
		}
	}
	return s
}

// parseFlow parses a single-line flow collection.
func parseFlow(s string, num int) (any, error) {
	f := &flowParser{s: s}
	v, err := f.value()
	if err == nil {
		f.space()
		if f.i != len(f.s) {
			err = fmt.Errorf("text after the flow collection")
		}
	}
	if err != nil {
		return nil, fmt.Errorf("line %d: %w", num, err)
	}
	return v, nil
}

type flowParser struct {
	s string
	i int
}

func (f *flowParser) space() {
	for f.i < len(f.s) && f.s[f.i] == ' ' {
		f.i++
	}
}

func (f *flowParser) value() (any, error) {
	f.space()
	if f.i >= len(f.s) {
		return nil, fmt.Errorf("unexpected end of flow collection")
	}
	switch c := f.s[f.i]; c {
	case '{':
		return f.mapping()
	case '[':
		return f.seq()
	case '\'', '"':
		v, n, err := quoted(f.s[f.i:])
		if err != nil {
			return nil, err
		}
		f.i += n
		return v, nil
	}
	start := f.i
	for f.i < len(f.s) && !strings.ContainsRune(",]}", rune(f.s[f.i])) {
		f.i++
	}
	return plain(strings.TrimSpace(f.s[start:f.i])), nil
}

func (f *flowParser) mapping() (any, error) {
	m := map[string]any{}
	f.i++ // {
	for {
		f.space()
		if f.i >= len(f.s) {
			return nil, fmt.Errorf("unterminated flow mapping")
		}
		if f.s[f.i] == '}' {
			f.i++
			return m, nil
		}
		var key string
		if c := f.s[f.i]; c == '\'' || c == '"' {
			k, n, err := quoted(f.s[f.i:])
			if err != nil {
				return nil, err
			}
			key = k
			f.i += n
		} else {
			start := f.i
			for f.i < len(f.s) && f.s[f.i] != ':' {
				f.i++
			}
			key = strings.TrimSpace(f.s[start:f.i])
		}
		f.space()
		if f.i >= len(f.s) || f.s[f.i] != ':' {
			return nil, fmt.Errorf("expected ':' after flow key %q", key)
		}
		f.i++
		v, err := f.value()
		if err != nil {
			return nil, err
		}
		m[key] = v
		f.space()
		if f.i < len(f.s) && f.s[f.i] == ',' {
			f.i++
		}
	}
}

func (f *flowParser) seq() (any, error) {
	list := []any{}
	f.i++ // [
	for {
		f.space()
		if f.i >= len(f.s) {
			return nil, fmt.Errorf("unterminated flow sequence")
		}
		if f.s[f.i] == ']' {
			f.i++
			return list, nil
		}
		v, err := f.value()
		if err != nil {
			return nil, err
		}
		list = append(list, v)
		f.space()
		if f.i < len(f.s) && f.s[f.i] == ',' {
			f.i++
		}
	}
}
