package codex

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

// tomlDoc writes a role file key by key. The TOML library reads roles but writes every string on one
// line, which would turn an agent's instructions into a single escaped line.
type tomlDoc struct {
	top, tables bytes.Buffer
}

var bareKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func tomlKey(k string) string {
	if bareKey.MatchString(k) {
		return k
	}
	return tomlString(k)
}

// str writes a one-line basic string.
func (d *tomlDoc) str(key, value string) {
	fmt.Fprintf(&d.top, "%s = %s\n", tomlKey(key), tomlString(value))
}

// text writes a multi-line string: a literal one when the value allows it, so backslashes and
// quotes read as written, else a basic one with escapes. The newline after the opening quotes is
// dropped by every TOML parser, so a value that starts with a newline keeps it.
func (d *tomlDoc) text(key, value string) {
	if literalOK(value) {
		fmt.Fprintf(&d.top, "%s = '''\n%s'''\n", tomlKey(key), value)
		return
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == '"':
			b.WriteString(`\"`)
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r == '\r':
			b.WriteString(`\r`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	fmt.Fprintf(&d.top, "%s = \"\"\"\n%s\"\"\"\n", tomlKey(key), b.String())
}

// literalOK reports whether value fits a multi-line literal string: no run of three single quotes
// and no control character but tab and newline.
func literalOK(value string) bool {
	if strings.Contains(value, "'''") {
		return false
	}
	for _, r := range value {
		if (r < 0x20 && r != '\t' && r != '\n') || r == 0x7f {
			return false
		}
	}
	return true
}

// value writes any other TOML value through the library; what it renders as a table goes after every
// top-level key, as TOML requires.
func (d *tomlDoc) value(key string, v any) error {
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	enc.Indent = ""
	if err := enc.Encode(map[string]any{key: v}); err != nil {
		return fmt.Errorf("encode %s: %w", key, err)
	}
	if bytes.HasPrefix(buf.Bytes(), []byte("[")) {
		if d.tables.Len() > 0 {
			d.tables.WriteByte('\n')
		}
		d.tables.Write(buf.Bytes())
	} else {
		d.top.Write(buf.Bytes())
	}
	return nil
}

func (d *tomlDoc) bytes() []byte {
	out := bytes.Clone(d.top.Bytes())
	if d.tables.Len() > 0 {
		out = append(out, '\n')
		out = append(out, d.tables.Bytes()...)
	}
	return out
}

func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == '"':
			b.WriteString(`\"`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\r':
			b.WriteString(`\r`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
