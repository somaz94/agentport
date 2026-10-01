package frontmatter

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/somaz94/agentport/internal/golden"
)

func mustParse(t *testing.T, s string) *Document {
	t.Helper()
	d, err := Parse([]byte(s))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return d
}

func TestParseSplits(t *testing.T) {
	cases := []struct {
		name, in, body string
		keys           []string
	}{
		{"no frontmatter", "# Title\nbody\n", "# Title\nbody\n", nil},
		{"empty frontmatter", "---\n---\nbody\n", "body\n", []string{}},
		{"closed at end of file", "---\nname: a\n---", "", []string{"name"}},
		{"bare delimiters at end of file", "---\n---", "", []string{}},
		{"crlf", "---\r\nname: a\r\ndescription: b\r\n---\r\nbody\r\n", "body\n", []string{"name", "description"}},
		{"utf-8 bom", "\uFEFF---\nname: a\n---\nbody\n", "body\n", []string{"name"}},
		{"delimiter inside body is body", "---\nname: a\n---\nx\n---\ny\n", "x\n---\ny\n", []string{"name"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := mustParse(t, c.in)
			if d.Body != c.body {
				t.Errorf("Body = %q; want %q", d.Body, c.body)
			}
			if got := d.Keys(); c.keys == nil && got != nil || c.keys != nil && !reflect.DeepEqual(got, c.keys) {
				t.Errorf("Keys() = %#v; want %#v", got, c.keys)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	for name, in := range map[string]string{
		"unclosed":      "---\nname: a\nbody\n",
		"not mapping":   "---\n- a\n- b\n---\n",
		"unrepairable":  "---\nname: \"unterminated\n---\n",
		"duplicate key": "---\nname: a\nname: b\n---\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(in)); err == nil {
				t.Error("Parse succeeded; want an error")
			}
		})
	}
	_, err := Parse([]byte("---\nname: a\nname: b\n---\n"))
	if err == nil || !strings.Contains(err.Error(), "already defined at line 1") {
		t.Errorf("duplicate key error = %v; want the first definition's line", err)
	}
}

func TestRepair(t *testing.T) {
	cases := map[string]string{
		"description: Use when: something happens":      "Use when: something happens",
		"description: `make` runs it":                   "`make` runs it",
		"description: it's run when: x":                 "it's run when: x",
		"description: run it: now # really":             "run it: now # really",
		"argument-hint: [pr-number] [priority] [owner]": "[pr-number] [priority] [owner]",
		"description: [WIP] fix: x":                     "[WIP] fix: x",
	}
	for line, want := range cases {
		t.Run(line, func(t *testing.T) {
			d := mustParse(t, "---\nname: x\n"+line+"\n---\n")
			key := strings.SplitN(line, ":", 2)[0]
			if got, _ := d.Scalar(key); got != want || !d.Repaired {
				t.Errorf("value %q repaired=%v; want %q repaired=true", got, d.Repaired, want)
			}
		})
	}
	clean := mustParse(t, "---\nname: x\ntools: Read, Grep\n---\n")
	if clean.Repaired {
		t.Error("valid frontmatter was marked repaired")
	}
	// An unquoted bracketed hint is valid YAML, a one-element sequence; readers must accept that shape.
	hint := mustParse(t, "---\nargument-hint: [a | b] # note\n---\n")
	if n, ok := hint.Get("argument-hint"); !ok || hint.Repaired || n.Kind != yaml.SequenceNode {
		t.Errorf("argument-hint parsed as %v repaired=%v; want a sequence, unrepaired", n, hint.Repaired)
	}
	// Once repair runs, a flow list stays a list and a comment marker stays text.
	mixed := mustParse(t, "---\ntools: [Read, Grep]\nnote: keep # this\ndescription: a: b\n---\n")
	if n, _ := mixed.Get("tools"); n.Kind != yaml.SequenceNode {
		t.Errorf("tools became %v during repair; want a sequence", n.Kind)
	}
	if got, _ := mixed.Scalar("note"); got != "keep # this" {
		t.Errorf("note = %q during repair; want the comment kept as text", got)
	}
	if got := Repair("tools: [a, b]\n  nested: x: y\nquoted: 'a: b'"); got != "tools: [a, b]\n  nested: x: y\nquoted: 'a: b'" {
		t.Errorf("Repair touched flow, nested or quoted values: %q", got)
	}
}

func TestAccessors(t *testing.T) {
	d := mustParse(t, "---\nname: a\ntools:\n  - Read\n---\nbody\n")
	if _, ok := d.Scalar("tools"); ok {
		t.Error("String(tools) succeeded on a sequence")
	}
	if _, ok := d.Scalar("missing"); ok {
		t.Error("String(missing) succeeded")
	}
	d.SetString("name", "b")
	d.SetString("model", "inherit")
	d.SetList("tools", []string{"view_file", "grep_search"})
	d.Delete("missing")
	if got, _ := d.Scalar("name"); got != "b" {
		t.Errorf("name = %q after SetString", got)
	}
	if n, _ := d.Get("tools"); n.Kind != yaml.SequenceNode || len(n.Content) != 2 {
		t.Errorf("tools = %+v after SetList", n)
	}
	d.Delete("model")
	if !reflect.DeepEqual(d.Keys(), []string{"name", "tools"}) {
		t.Errorf("Keys() = %v after Delete", d.Keys())
	}

	empty := &Document{Body: "only body\n"}
	if empty.Keys() != nil {
		t.Error("Keys() on a document without frontmatter is not nil")
	}
	if _, ok := empty.Get("name"); ok {
		t.Error("Get on a document without frontmatter succeeded")
	}
	empty.Delete("name")
	empty.SetString("name", "c")
	if got, _ := empty.Scalar("name"); got != "c" {
		t.Errorf("SetString on a document without frontmatter: name = %q", got)
	}
}

func TestMarshalLeavesDocumentAlone(t *testing.T) {
	d := mustParse(t, "---\ndescription: plain\n---\n")
	n, _ := d.Get("description")
	out, err := d.Marshal()
	if err != nil || n.Style != 0 || !strings.Contains(string(out), "description: 'plain'") {
		t.Errorf("Marshal = %q, %v; node style now %v, want the copy quoted and the node untouched", out, err, n.Style)
	}
}

func TestMarshalWithoutFrontmatter(t *testing.T) {
	out, err := (&Document{Body: "just body\n"}).Marshal()
	if err != nil || string(out) != "just body\n" {
		t.Errorf("Marshal = %q, %v", out, err)
	}
	out, err = mustParse(t, "---\n---\nbody\n").Marshal()
	if err != nil || string(out) != "---\n---\nbody\n" {
		t.Errorf("Marshal(empty frontmatter) = %q, %v", out, err)
	}
}

func TestMarshalDoesNotWrapLongValues(t *testing.T) {
	long := strings.Repeat("word ", 60)
	d := &Document{}
	d.SetString("description", long)
	out, err := d.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(out), "\n") != 3 {
		t.Errorf("a long description was wrapped:\n%s", out)
	}
}

// TestRoundTrip checks every testdata/roundtrip case: the written form matches its golden file,
// is strict YAML, and parses back to the same fields and body as the input.
func TestRoundTrip(t *testing.T) {
	dirs, err := filepath.Glob(filepath.Join("testdata", "roundtrip", "*"))
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no round-trip cases: %v", err)
	}
	for _, dir := range dirs {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			in, err := os.ReadFile(filepath.Join(dir, "in.md"))
			if err != nil {
				t.Fatal(err)
			}
			src := mustParse(t, string(in))
			out, err := src.Marshal()
			if err != nil {
				t.Fatal(err)
			}
			golden.Assert(t, filepath.Join(dir, "out.md"), out)

			back := mustParse(t, string(out))
			if back.Repaired {
				t.Error("written frontmatter needed the repair pass; it must be strict YAML")
			}
			if back.Body != src.Body {
				t.Errorf("body changed: %q -> %q", src.Body, back.Body)
			}
			if !reflect.DeepEqual(decodeAll(t, src), decodeAll(t, back)) {
				t.Errorf("fields changed:\n%v\n%v", decodeAll(t, src), decodeAll(t, back))
			}
		})
	}
}

func decodeAll(t *testing.T, d *Document) map[string]any {
	t.Helper()
	m := map[string]any{}
	if d.Fields != nil {
		if err := d.Fields.Decode(&m); err != nil {
			t.Fatal(err)
		}
	}
	return m
}
