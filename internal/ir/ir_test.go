package ir

import (
	"strings"
	"testing"
)

func TestParseKind(t *testing.T) {
	for _, k := range Kinds {
		got, err := ParseKind(string(k))
		if err != nil || got != k {
			t.Errorf("ParseKind(%q) = %q, %v", k, got, err)
		}
	}
	if _, err := ParseKind("workflow"); err == nil {
		t.Error("ParseKind(workflow) succeeded; want an error")
	}
}

func TestToolSet(t *testing.T) {
	var ts ToolSet
	if ts.Has(CapRead) {
		t.Error("zero ToolSet grants read")
	}
	ts.Add(CapRead)
	ts.Add(CapShell)
	ts.Add(CapRead)
	if len(ts.Caps) != 2 || ts.Caps[0] != CapRead || ts.Caps[1] != CapShell {
		t.Errorf("Caps = %v; want [read shell] without duplicates", ts.Caps)
	}
	if !ts.Has(CapShell) || ts.Has(CapEdit) {
		t.Errorf("Has: shell=%v edit=%v", ts.Has(CapShell), ts.Has(CapEdit))
	}
	if !(ToolSet{All: true}).Has(CapDelegate) {
		t.Error("All does not grant delegate")
	}
}

func TestValidateSkillName(t *testing.T) {
	for _, ok := range []string{"commit", "doc-tidy", "a1-b2", strings.Repeat("a", 64)} {
		if err := ValidateSkillName(ok); err != nil {
			t.Errorf("ValidateSkillName(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "Bad_Name", "-lead", "trail-", "double--hyphen", "has space", strings.Repeat("a", 65)} {
		if err := ValidateSkillName(bad); err == nil {
			t.Errorf("ValidateSkillName(%q) succeeded; want an error", bad)
		}
	}
}

func TestCommandSkillName(t *testing.T) {
	cases := map[string]string{
		"commit.md":             "commit",
		"frontend/component.md": "frontend-component",
		`frontend\Component.md`: "frontend-component",
		"./a/b/c.md":            "a-b-c",
		"oss-status.md":         "oss-status",
	}
	for in, want := range cases {
		if got := CommandSkillName(in); got != want {
			t.Errorf("CommandSkillName(%q) = %q; want %q", in, got, want)
		}
	}
}
