// Package frontmatter splits Markdown into YAML frontmatter and body, reads it leniently and
// writes it strictly.
//
// Reading is lenient because Claude Code (and, more narrowly, Codex) repairs invalid YAML before
// parsing, so a file it loads may not be strict YAML. Writing is strict because Antigravity drops an
// agent whose frontmatter does not parse, without any error.
package frontmatter

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

const delimiter = "---"

// Document is a Markdown file split into frontmatter and body.
type Document struct {
	// Fields is the frontmatter mapping, in source key order. Nil when the file has none.
	Fields *yaml.Node
	Body   string
	// Repaired reports that the frontmatter only parsed after the lenient repair pass.
	Repaired bool
}

// Parse splits data into frontmatter and body. A file without a leading `---` line has no
// frontmatter and is all body. A UTF-8 BOM is dropped and CRLF line endings become LF.
func Parse(data []byte) (*Document, error) {
	text := strings.ReplaceAll(strings.TrimPrefix(string(data), "\uFEFF"), "\r\n", "\n")
	if !strings.HasPrefix(text, delimiter+"\n") {
		return &Document{Body: text}, nil
	}
	rest := text[len(delimiter)+1:]
	var raw, body string
	switch {
	case strings.HasPrefix(rest, delimiter+"\n"):
		body = rest[len(delimiter)+1:]
	case rest == delimiter:
	default:
		end := strings.Index(rest, "\n"+delimiter+"\n")
		if end < 0 {
			if !strings.HasSuffix(rest, "\n"+delimiter) {
				return nil, errors.New("frontmatter has no closing --- line")
			}
			end = len(rest) - len(delimiter) - 1
			raw = rest[:end]
		} else {
			raw, body = rest[:end], rest[end+len(delimiter)+2:]
		}
	}

	doc := &Document{Body: body}
	fields, err := decode(raw)
	if err != nil {
		var repairErr error
		if fields, repairErr = decode(Repair(raw)); repairErr != nil {
			return nil, fmt.Errorf("parse frontmatter: %w", err)
		}
		doc.Repaired = true
	}
	doc.Fields = fields
	return doc, nil
}

func decode(raw string) (*yaml.Node, error) {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(raw), &root); err != nil {
		return nil, err
	}
	if root.Kind == 0 {
		return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}, nil
	}
	if len(root.Content) != 1 || root.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("frontmatter is not a mapping")
	}
	// yaml.v3 skips its duplicate-key check when decoding into a Node, and a strict parser on the
	// target side rejects the file.
	m := root.Content[0]
	seen := make(map[string]int, len(m.Content)/2)
	for i := 0; i+1 < len(m.Content); i += 2 {
		k := m.Content[i]
		if prev, dup := seen[k.Value]; dup {
			return nil, fmt.Errorf("line %d: key %q already defined at line %d", k.Line, k.Value, prev)
		}
		seen[k.Value] = k.Line
	}
	return m, nil
}

var (
	topLevelScalar = regexp.MustCompile(`^([A-Za-z0-9_.-]+):[ \t]+(.+?)[ \t]*$`)
	commentMarker  = regexp.MustCompile(`(^|[ \t])#`)
)

// Repair single-quotes every top-level value that does not parse on its own, such as
// `description: Use when: x`, a leading backtick or `argument-hint: [a] [b]`, and every value
// containing a comment marker, which Claude Code also keeps as text. Already-quoted values and
// values that parse alone, including flow lists like `[Read, Grep]`, are left alone.
func Repair(raw string) string {
	lines := strings.Split(raw, "\n")
	for i, line := range lines {
		m := topLevelScalar.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		key, val := m[1], m[2]
		if strings.ContainsAny(val[:1], `'"`) || (parsesAlone(val) && !commentMarker.MatchString(val)) {
			continue
		}
		lines[i] = key + ": '" + strings.ReplaceAll(val, "'", "''") + "'"
	}
	return strings.Join(lines, "\n")
}

func parsesAlone(val string) bool {
	var n yaml.Node
	return yaml.Unmarshal([]byte("k: "+val), &n) == nil
}

// Keys returns the frontmatter keys in order.
func (d *Document) Keys() []string {
	if d.Fields == nil {
		return nil
	}
	keys := make([]string, 0, len(d.Fields.Content)/2)
	for i := 0; i+1 < len(d.Fields.Content); i += 2 {
		keys = append(keys, d.Fields.Content[i].Value)
	}
	return keys
}

// Get returns the value node for key.
func (d *Document) Get(key string) (*yaml.Node, bool) {
	if d.Fields == nil {
		return nil, false
	}
	for i := 0; i+1 < len(d.Fields.Content); i += 2 {
		if d.Fields.Content[i].Value == key {
			return d.Fields.Content[i+1], true
		}
	}
	return nil, false
}

// Scalar returns the value of key when it is a scalar.
func (d *Document) Scalar(key string) (string, bool) {
	n, ok := d.Get(key)
	if !ok || n.Kind != yaml.ScalarNode {
		return "", false
	}
	return n.Value, true
}

// Set replaces key's value, or appends key when it is absent.
func (d *Document) Set(key string, value *yaml.Node) {
	if d.Fields == nil {
		d.Fields = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	}
	for i := 0; i+1 < len(d.Fields.Content); i += 2 {
		if d.Fields.Content[i].Value == key {
			d.Fields.Content[i+1] = value
			return
		}
	}
	d.Fields.Content = append(d.Fields.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}

// SetString sets key to a string scalar.
func (d *Document) SetString(key, value string) {
	d.Set(key, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
}

// SetList sets key to a block sequence of strings.
func (d *Document) SetList(key string, values []string) {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, v := range values {
		seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v})
	}
	d.Set(key, seq)
}

// Delete removes key if present.
func (d *Document) Delete(key string) {
	if d.Fields == nil {
		return
	}
	for i := 0; i+1 < len(d.Fields.Content); i += 2 {
		if d.Fields.Content[i].Value == key {
			d.Fields.Content = append(d.Fields.Content[:i], d.Fields.Content[i+2:]...)
			return
		}
	}
}

// Marshal renders the document as strict YAML frontmatter followed by the body. `description`
// is single-quoted because it routinely carries `: ` and backticks, except where encodeFields keeps
// yaml.v3's double quotes. The document is not modified.
func (d *Document) Marshal() ([]byte, error) {
	var out bytes.Buffer
	if d.Fields != nil {
		out.WriteString(delimiter + "\n")
		if len(d.Fields.Content) > 0 {
			text, err := encodeFields(d.Fields, true)
			if err != nil {
				return nil, err
			}
			out.Write(text)
		}
		out.WriteString(delimiter + "\n")
	}
	out.WriteString(d.Body)
	return out.Bytes(), nil
}

// encodeFields encodes m with its description single-quoted. yaml.v3 double-quotes any scalar with a
// character beyond U+FFFF, so with swap set a description single quotes can hold is written as a
// placeholder and put back by hand, unless that line carries more (a line comment, an anchor, a
// quoted key, a flow mapping): then yaml.v3's double quotes stand.
func encodeFields(m *yaml.Node, swap bool) ([]byte, error) {
	fields, desc := withQuotedDescription(m, swap)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(fields); err != nil {
		return nil, fmt.Errorf("encode frontmatter: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("encode frontmatter: %w", err)
	}
	if desc == "" {
		return buf.Bytes(), nil
	}
	placeholder := "\ndescription: '" + descriptionToken + "'\n"
	text := "\n" + buf.String()
	if strings.Count(text, placeholder) != 1 {
		return encodeFields(m, false)
	}
	text = strings.Replace(text, placeholder, "\ndescription: '"+strings.ReplaceAll(desc, "'", "''")+"'\n", 1)
	return []byte(text[1:]), nil
}

// descriptionToken holds the description's place while yaml.v3 encodes the rest; see encodeFields.
const descriptionToken = "agentport-description-placeholder"

// withQuotedDescription single-quotes m's description; with swap set, see encodeFields.
func withQuotedDescription(m *yaml.Node, swap bool) (*yaml.Node, string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == "description" && m.Content[i+1].Kind == yaml.ScalarNode {
			desc := *m.Content[i+1]
			desc.Style = yaml.SingleQuotedStyle
			var swapped string
			if swap && beyondBMP(desc.Value) && singleQuotable(desc.Value) {
				swapped, desc.Value = desc.Value, descriptionToken
			}
			cp := *m
			cp.Content = slices.Clone(m.Content)
			cp.Content[i+1] = &desc
			return &cp, swapped
		}
	}
	return m, ""
}

func beyondBMP(s string) bool {
	for _, r := range s {
		if r > 0xFFFF {
			return true
		}
	}
	return false
}

// singleQuotable reports whether every character of s is printable in YAML 1.2 and none is a line
// break yaml.v3 recognizes, which a single-quoted scalar would fold into a space.
func singleQuotable(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		switch {
		case r == 0x2028, r == 0x2029:
			return false
		case r == '\t', r >= 0x20 && r <= 0x7E, r >= 0xA0 && r <= 0xD7FF,
			r >= 0xE000 && r <= 0xFFFD && r != 0xFEFF, r >= 0x10000 && r <= 0x10FFFF:
		default:
			return false
		}
	}
	return true
}

// ReplaceBody returns data with its body replaced and its frontmatter kept byte for byte. It
// returns false when it cannot splice that way (a BOM, CRLF line endings, or no closing
// delimiter), so the caller can fall back to Marshal.
func ReplaceBody(data []byte, body string) ([]byte, bool) {
	text := string(data)
	if strings.HasPrefix(text, "\uFEFF") || strings.Contains(text, "\r\n") {
		return nil, false
	}
	open := delimiter + "\n"
	if !strings.HasPrefix(text, open) {
		return []byte(body), true
	}
	rest := text[len(open):]
	if strings.HasPrefix(rest, open) {
		return []byte(text[:2*len(open)] + body), true
	}
	end := strings.Index(rest, "\n"+open)
	if end < 0 {
		return nil, false
	}
	return []byte(text[:len(open)+end+1+len(open)] + body), true
}
