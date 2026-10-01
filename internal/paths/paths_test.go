package paths

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/somaz94/agentport/internal/harness"
	"github.com/somaz94/agentport/internal/ir"
)

func TestPath(t *testing.T) {
	cases := []struct {
		h     harness.ID
		scope Scope
		kind  ir.Kind
		want  string
	}{
		{harness.Claude, ScopeUser, ir.KindSkill, ".claude/skills/demo/SKILL.md"},
		{harness.Claude, ScopeProject, ir.KindCommand, ".claude/commands/demo.md"},
		{harness.Claude, ScopeProject, ir.KindInstructions, "CLAUDE.md"},
		{harness.Codex, ScopeUser, ir.KindSkill, ".agents/skills/demo/SKILL.md"},
		{harness.Codex, ScopeProject, ir.KindAgent, ".codex/agents/demo.toml"},
		{harness.Antigravity, ScopeUser, ir.KindAgent, ".gemini/config/agents/demo.md"},
		{harness.Antigravity, ScopeProject, ir.KindSkill, ".agents/skills/demo/SKILL.md"},
		{harness.Antigravity, ScopeUser, ir.KindInstructions, ".gemini/config/AGENTS.md"},
	}
	for _, c := range cases {
		l, err := For(c.h)
		if err != nil {
			t.Fatal(err)
		}
		got, err := l.Path(c.scope, c.kind, "demo")
		if err != nil || got != filepath.FromSlash(c.want) {
			t.Errorf("%s %s %s = %q, %v; want %q", c.h, c.scope, c.kind, got, err, c.want)
		}
	}
}

func TestPathRejects(t *testing.T) {
	cx, _ := For(harness.Codex)
	if _, err := cx.Path(ScopeUser, ir.KindCommand, "demo"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Codex command path error = %v; want ErrUnsupported", err)
	}
	cl, _ := For(harness.Claude)
	for _, name := range []string{"", "a/b", `a\b`, ".", ".."} {
		if _, err := cl.Path(ScopeUser, ir.KindSkill, name); !errors.Is(err, ErrInvalidName) {
			t.Errorf("Path(%q) error = %v; want ErrInvalidName", name, err)
		}
	}
	if _, err := cl.Path("", ir.KindSkill, "demo"); err == nil {
		t.Error("Path accepted an empty scope; it must not default to the home directory")
	}
	if _, err := For("cursor"); err == nil {
		t.Error("For(cursor) succeeded")
	}
}

func TestLayoutFacts(t *testing.T) {
	for _, h := range harness.All {
		l, err := For(h)
		if err != nil || l.Version == "" {
			t.Errorf("%s: layout %+v, %v; every layout needs a pinned version", h, l, err)
		}
		for _, s := range []Scope{ScopeUser, ScopeProject} {
			if !l.Supports(s, ir.KindSkill) || !l.Supports(s, ir.KindAgent) {
				t.Errorf("%s does not support skills and agents at %s scope", h, s)
			}
		}
	}
	ag, _ := For(harness.Antigravity)
	if ag.Supports(ScopeUser, ir.KindCommand) || ag.Supports("", ir.KindSkill) {
		t.Error("Antigravity supports commands, or Supports accepted an empty scope")
	}
	dep := ag.Deprecated()
	if len(dep) == 0 {
		t.Fatal("Antigravity has no deprecated locations")
	}
	dep[0] = "changed"
	if ag.Deprecated()[0] == "changed" {
		t.Error("Deprecated() exposes the shared table")
	}
}

func TestDir(t *testing.T) {
	cases := []struct {
		h     harness.ID
		scope Scope
		kind  ir.Kind
		want  string
	}{
		{harness.Claude, ScopeUser, ir.KindSkill, ".claude/skills"},
		{harness.Claude, ScopeProject, ir.KindCommand, ".claude/commands"},
		{harness.Codex, ScopeUser, ir.KindAgent, ".codex/agents"},
		{harness.Antigravity, ScopeUser, ir.KindSkill, ".gemini/config/skills"},
		{harness.Antigravity, ScopeProject, ir.KindAgent, ".agents/agents"},
	}
	for _, c := range cases {
		l, _ := For(c.h)
		if got, err := l.Dir(c.scope, c.kind); err != nil || got != filepath.FromSlash(c.want) {
			t.Errorf("%s %s %s Dir = %q, %v; want %q", c.h, c.scope, c.kind, got, err, c.want)
		}
	}
	cl, _ := For(harness.Claude)
	if _, err := cl.Dir(ScopeUser, ir.KindInstructions); err == nil {
		t.Error("Dir(instructions) succeeded; instructions have no per-item directory")
	}
	ag, _ := For(harness.Antigravity)
	if _, err := ag.Dir(ScopeUser, ir.KindCommand); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Antigravity command Dir error = %v; want ErrUnsupported", err)
	}
	if _, err := cl.Dir("", ir.KindSkill); err == nil {
		t.Error("Dir accepted an empty scope")
	}
}

func TestParseScope(t *testing.T) {
	for _, s := range []string{"user", "project"} {
		if got, err := ParseScope(s); err != nil || string(got) != s {
			t.Errorf("ParseScope(%q) = %q, %v", s, got, err)
		}
	}
	if _, err := ParseScope("global"); err == nil {
		t.Error("ParseScope(global) succeeded")
	}
}
