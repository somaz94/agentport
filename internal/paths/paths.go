// Package paths is the version-pinned table of where each harness keeps each kind of
// customization. Converters read locations from here only, so a harness moving a directory is a
// one-row change. The facts behind every row are in docs/spec/.
package paths

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/somaz94/agentport/internal/harness"
	"github.com/somaz94/agentport/internal/ir"
)

// Scope is where a customization applies.
type Scope string

// Scopes.
const (
	ScopeUser    Scope = "user"
	ScopeProject Scope = "project"
)

// ParseScope resolves a scope name.
func ParseScope(s string) (Scope, error) {
	switch Scope(s) {
	case ScopeUser, ScopeProject:
		return Scope(s), nil
	}
	return "", fmt.Errorf("unknown scope %q (want user or project)", s)
}

// ErrUnsupported means the harness has no location for that kind; the converter must map the
// item to another kind (a command becomes a skill) or report it as dropped.
var ErrUnsupported = errors.New("harness has no location for this kind")

// ErrInvalidName means an item name would not stay inside its directory.
var ErrInvalidName = errors.New("invalid item name")

// Layout is one harness's locations, verified against Version.
type Layout struct {
	Harness harness.ID
	Version string
	// User paths are relative to the home directory, project paths to the repository root.
	// `<name>` is replaced with the item name.
	user, project map[ir.Kind]string
	deprecated    []string
}

var layouts = map[harness.ID]Layout{
	harness.Claude: {
		Harness: harness.Claude,
		Version: "2.1.285",
		user: map[ir.Kind]string{
			ir.KindSkill:        ".claude/skills/<name>/SKILL.md",
			ir.KindCommand:      ".claude/commands/<name>.md",
			ir.KindAgent:        ".claude/agents/<name>.md",
			ir.KindInstructions: ".claude/CLAUDE.md",
		},
		project: map[ir.Kind]string{
			ir.KindSkill:        ".claude/skills/<name>/SKILL.md",
			ir.KindCommand:      ".claude/commands/<name>.md",
			ir.KindAgent:        ".claude/agents/<name>.md",
			ir.KindInstructions: "CLAUDE.md",
		},
	},
	harness.Codex: {
		Harness: harness.Codex,
		Version: "rust-v0.159.3",
		user: map[ir.Kind]string{
			ir.KindSkill:        ".agents/skills/<name>/SKILL.md",
			ir.KindAgent:        ".codex/agents/<name>.toml",
			ir.KindInstructions: ".codex/AGENTS.md",
		},
		project: map[ir.Kind]string{
			ir.KindSkill:        ".agents/skills/<name>/SKILL.md",
			ir.KindAgent:        ".codex/agents/<name>.toml",
			ir.KindInstructions: "AGENTS.md",
		},
		deprecated: []string{"~/.codex/skills", "~/.codex/prompts"},
	},
	harness.Antigravity: {
		Harness: harness.Antigravity,
		Version: "2.19.1",
		user: map[ir.Kind]string{
			ir.KindSkill:        ".gemini/config/skills/<name>/SKILL.md",
			ir.KindAgent:        ".gemini/config/agents/<name>.md",
			ir.KindInstructions: ".gemini/config/AGENTS.md",
		},
		project: map[ir.Kind]string{
			ir.KindSkill:        ".agents/skills/<name>/SKILL.md",
			ir.KindAgent:        ".agents/agents/<name>.md",
			ir.KindInstructions: "AGENTS.md",
		},
		deprecated: []string{".agent/", ".agents/workflows/", "~/.gemini/antigravity/skills"},
	},
}

// For returns the layout of h.
func For(h harness.ID) (Layout, error) {
	l, ok := layouts[h]
	if !ok {
		return Layout{}, fmt.Errorf("no layout for harness %q", h)
	}
	return l, nil
}

// Path returns where an item of kind named name lives, relative to the scope's root. Kinds with
// no per-item file (instructions) ignore name.
func (l Layout) Path(scope Scope, kind ir.Kind, name string) (string, error) {
	table, err := l.table(scope)
	if err != nil {
		return "", err
	}
	p, ok := table[kind]
	if !ok {
		return "", fmt.Errorf("%s %s at %s scope: %w", l.Harness.Title(), kind, scope, ErrUnsupported)
	}
	if strings.Contains(p, "<name>") {
		if err := ValidName(name); err != nil {
			return "", err
		}
		p = strings.ReplaceAll(p, "<name>", name)
	}
	return filepath.FromSlash(p), nil
}

// Dir returns the directory that holds every item of kind, relative to the scope's root:
// `.claude/skills` for Claude skills, `.codex/agents` for Codex agents.
func (l Layout) Dir(scope Scope, kind ir.Kind) (string, error) {
	table, err := l.table(scope)
	if err != nil {
		return "", err
	}
	p, ok := table[kind]
	if !ok {
		return "", fmt.Errorf("%s %s at %s scope: %w", l.Harness.Title(), kind, scope, ErrUnsupported)
	}
	i := strings.Index(p, "/<name>")
	if i < 0 {
		return "", fmt.Errorf("%s %s has no per-item directory", l.Harness.Title(), kind)
	}
	return filepath.FromSlash(p[:i]), nil
}

// ValidName reports whether name can be a single directory or file name inside an item directory.
// Item names come from frontmatter, so anything that could climb out of the directory is refused.
func ValidName(name string) error {
	if name == "." || strings.ContainsAny(name, `/\`) || !filepath.IsLocal(name) {
		return fmt.Errorf("%w: %q", ErrInvalidName, name)
	}
	return nil
}

// Supports reports whether l has a location for kind at scope.
func (l Layout) Supports(scope Scope, kind ir.Kind) bool {
	table, err := l.table(scope)
	if err != nil {
		return false
	}
	_, ok := table[kind]
	return ok
}

// Deprecated lists locations the harness still reads or used to read, for the planned `doctor` check.
// Entries starting with `~/` are user scope, the rest project scope.
func (l Layout) Deprecated() []string {
	return slices.Clone(l.deprecated)
}

func (l Layout) table(scope Scope) (map[ir.Kind]string, error) {
	switch scope {
	case ScopeUser:
		return l.user, nil
	case ScopeProject:
		return l.project, nil
	}
	return nil, fmt.Errorf("unknown scope %q", scope)
}
