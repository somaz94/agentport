package instructions

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/somaz94/agentport/internal/harness"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func put(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	must(t, os.MkdirAll(filepath.Dir(p), 0o755))
	must(t, os.WriteFile(p, []byte(body), 0o644))
}

func headings(ss []Section) []string {
	var out []string
	for _, s := range ss {
		out = append(out, s.Heading)
	}
	return out
}

func TestSections(t *testing.T) {
	doc := "# Title\n\nintro\n\n<br/>\n\n## One\n\na\n\n```md\n## not a heading\n```\n\n### Sub\n\nb\n\n## Two\n\nc\n\n## One\n\nd\n"
	got := Sections([]byte(doc))
	want := []string{Preamble, "One", "Two", "One (2)"}
	if !reflect.DeepEqual(headings(got), want) {
		t.Fatalf("headings = %q; want %q", headings(got), want)
	}
	respaced := strings.ReplaceAll(doc, "\n\n", "\n\n\n") + "\n<br/>\n"
	respaced = strings.Replace(respaced, "a\n", "a   \n", 1)
	if !reflect.DeepEqual(Sections([]byte(respaced)), got) {
		t.Error("blank lines, trailing spaces and <br/> spacers changed a section hash")
	}
	edited := Sections([]byte(strings.Replace(doc, "\nc\n", "\nC\n", 1)))
	if edited[2].Hash == got[2].Hash || edited[1].Hash != got[1].Hash {
		t.Error("editing one section must change its hash and only its hash")
	}
	if s := Sections([]byte("## Only\n\nx\n")); len(s) != 1 || s[0].Heading != "Only" {
		t.Errorf("an empty preamble must be left out: %+v", s)
	}
	if s := Sections(nil); len(s) != 0 {
		t.Errorf("Sections(nil) = %+v", s)
	}
}

func TestFenceMarker(t *testing.T) {
	tests := map[string]string{
		"```":        "```",
		"  ````go":   "````",
		"~~~":        "~~~",
		"    ```":    "",
		"``":         "",
		"text ```":   "",
		"   ~~~~ sh": "~~~~",
	}
	for line, want := range tests {
		if got := fenceMarker(line); got != want {
			t.Errorf("fenceMarker(%q) = %q; want %q", line, got, want)
		}
	}
	// A shorter run inside a longer fence does not close it.
	doc := "````\n```\n## inside\n````\n## Outside\nx\n"
	if got := headings(Sections([]byte(doc))); !reflect.DeepEqual(got, []string{Preamble, "Outside"}) {
		t.Errorf("headings = %q", got)
	}
}

func TestCompareAndDiff(t *testing.T) {
	r := &Record{Sections: map[string]string{"Keep": "h1", "Edit": "h2", "Zed": "h3", "Gone": "h4"}}
	d := Compare(r, []Section{{"New", "x"}, {"Keep", "h1"}, {"Edit", "h2b"}})
	want := Diff{Added: []string{"New"}, Changed: []string{"Edit"}, Removed: []string{"Gone", "Zed"}}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("Compare = %+v; want %+v", d, want)
	}
	if d.Empty() || d.Len() != 4 {
		t.Errorf("Empty = %v, Len = %d", d.Empty(), d.Len())
	}
	if s := d.String(); s != `added "New"; changed "Edit"; removed "Gone", "Zed"` {
		t.Errorf("String = %s", s)
	}
	if d := Compare(r, []Section{{"Keep", "h1"}, {"Edit", "h2"}, {"Zed", "h3"}, {"Gone", "h4"}}); !d.Empty() || d.String() != "" {
		t.Errorf("unchanged sections = %+v", d)
	}
}

func TestPairs(t *testing.T) {
	all, err := Pairs(harness.Claude, nil)
	must(t, err)
	if len(all) != 2 || all[0].Target != harness.Codex || all[1].Target != harness.Antigravity {
		t.Fatalf("Pairs = %+v", all)
	}
	ag := all[1]
	if ag.Hub != filepath.FromSlash(".claude/CLAUDE.md") || ag.File != filepath.FromSlash(".gemini/config/AGENTS.md") || ag.Root != filepath.FromSlash(".gemini/config") {
		t.Errorf("antigravity pair = %+v", ag)
	}
	one, err := Pairs(harness.Claude, []harness.ID{harness.Antigravity})
	must(t, err)
	if len(one) != 1 || one[0] != ag {
		t.Errorf("Pairs(antigravity) = %+v", one)
	}
	if _, err := Pairs("nope", nil); err == nil {
		t.Error("an unknown hub was accepted")
	}
	if _, err := Pairs(harness.Claude, []harness.ID{"nope"}); err == nil {
		t.Error("an unknown target was accepted")
	}
}

func TestCheckAndMark(t *testing.T) {
	home := t.TempDir()
	pairs, err := Pairs(harness.Claude, []harness.ID{harness.Antigravity})
	must(t, err)
	p := pairs[0]
	state := func() Report {
		t.Helper()
		rep, err := Check(home, p)
		must(t, err)
		return rep
	}

	if rep := state(); rep.State != NoFile {
		t.Errorf("no AGENTS.md: %s", rep.State)
	}
	if _, err := Mark(home, p); err == nil {
		t.Error("Mark without a target file succeeded")
	}
	put(t, home, ".gemini/config/AGENTS.md", "condensed\n")
	if rep := state(); rep.State != NoHub {
		t.Errorf("no CLAUDE.md: %s", rep.State)
	}
	if _, err := Mark(home, p); err == nil {
		t.Error("Mark without a hub file succeeded")
	}
	put(t, home, ".claude/CLAUDE.md", "# G\n\n## A\n\na\n\n## B\n\nb\n")
	if rep := state(); rep.State != Unrecorded || rep.Sections != 3 {
		t.Errorf("before the first mark: %+v", rep)
	}
	n, err := Mark(home, p)
	must(t, err)
	if n != 3 {
		t.Errorf("Mark recorded %d sections", n)
	}
	if rep := state(); rep.State != Current || !rep.Diff.Empty() {
		t.Errorf("after the mark: %+v", rep)
	}
	r, err := Load(filepath.Join(home, p.Root))
	must(t, err)
	if r.Hub != ".claude/CLAUDE.md" || r.Version != SchemaVersion || len(r.Sections) != 3 {
		t.Errorf("record = %+v", r)
	}

	put(t, home, ".claude/CLAUDE.md", "# G\n\n## A\n\na edited\n\n## C\n\nc\n")
	rep := state()
	want := Diff{Added: []string{"C"}, Changed: []string{"A"}, Removed: []string{"B"}}
	if rep.State != Changed || !reflect.DeepEqual(rep.Diff, want) {
		t.Errorf("after editing the hub: %+v", rep)
	}
	if before, _ := os.ReadFile(filepath.Join(home, p.File)); string(before) != "condensed\n" {
		t.Error("Check or Mark touched AGENTS.md")
	}
}

func TestLoadErrors(t *testing.T) {
	root := t.TempDir()
	if r, err := Load(root); r != nil || err != nil {
		t.Errorf("missing record = %+v, %v", r, err)
	}
	put(t, root, ".agentport/instructions.json", "{")
	if _, err := Load(root); err == nil {
		t.Error("a malformed record was accepted")
	}
	put(t, root, ".agentport/instructions.json", `{"version": 99}`)
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "schema version 99") {
		t.Errorf("a future schema = %v", err)
	}
	must(t, os.Remove(filepath.Join(root, ".agentport/instructions.json")))
	must(t, os.Mkdir(filepath.Join(root, ".agentport/instructions.json"), 0o755))
	if _, err := Load(root); err == nil {
		t.Error("a directory in place of the record was read")
	}

	home := t.TempDir()
	pairs, _ := Pairs(harness.Claude, []harness.ID{harness.Antigravity})
	put(t, home, ".gemini/config/AGENTS.md", "x\n")
	put(t, home, ".claude/CLAUDE.md", "## A\n")
	put(t, home, ".gemini/config/.agentport/instructions.json", "{")
	if _, err := Check(home, pairs[0]); err == nil {
		t.Error("Check ignored a malformed record")
	}
}

func TestSaveErrors(t *testing.T) {
	r := &Record{Version: SchemaVersion, Sections: map[string]string{}}
	if err := r.Save(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("Save into a missing root succeeded")
	}
	root := t.TempDir()
	put(t, root, ".agentport", "a file, not a directory")
	if err := r.Save(root); err == nil {
		t.Error("Save through a file succeeded")
	}
	// A link planted in place of the record directory must not be followed out of the root.
	outside := t.TempDir()
	root = t.TempDir()
	must(t, os.Symlink(outside, filepath.Join(root, ".agentport")))
	if err := r.Save(root); err == nil {
		t.Error("Save followed a link out of the root")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("Save wrote outside the root: %v", entries)
	}
}
