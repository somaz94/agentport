// Package ir is the harness-neutral form every reader produces and every writer consumes.
package ir

import (
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/somaz94/agentport/internal/harness"
)

// Kind is the type of a customization.
type Kind string

// Customization kinds.
const (
	KindSkill        Kind = "skill"
	KindCommand      Kind = "command"
	KindAgent        Kind = "agent"
	KindInstructions Kind = "instructions"
	KindHook         Kind = "hook"
	KindMCP          Kind = "mcp"
)

// Kinds lists every kind in display order.
var Kinds = []Kind{KindSkill, KindCommand, KindAgent, KindInstructions, KindHook, KindMCP}

// ParseKind resolves a kind name.
func ParseKind(s string) (Kind, error) {
	for _, k := range Kinds {
		if string(k) == s {
			return k, nil
		}
	}
	return "", fmt.Errorf("unknown kind %q", s)
}

// Capability is a harness-neutral tool capability; writers map it to tool names.
type Capability string

// Tool capabilities.
const (
	CapRead      Capability = "read"
	CapSearch    Capability = "search"
	CapGlob      Capability = "glob"
	CapShell     Capability = "shell"
	CapEdit      Capability = "edit"
	CapWrite     Capability = "write"
	CapWebFetch  Capability = "web_fetch"
	CapWebSearch Capability = "web_search"
	CapDelegate  Capability = "delegate"
	CapAskUser   Capability = "ask_user"
	CapPlan      Capability = "plan"
)

// Capabilities lists every capability, in the order writers emit tool names for an agent that
// may use everything.
var Capabilities = []Capability{CapRead, CapSearch, CapGlob, CapShell, CapEdit, CapWrite, CapWebFetch, CapWebSearch, CapDelegate, CapAskUser, CapPlan}

// ToolSet is what an agent may call. The zero value grants nothing.
type ToolSet struct {
	// All means the source granted every tool, e.g. a Claude agent with no `tools` key.
	All bool
	// Caps holds the granted capabilities, deduplicated, in source order.
	Caps []Capability
	// Unknown holds source tool names that map to no capability.
	Unknown []string
	// Narrowed holds source entries whose arguments restrict a tool, such as `Bash(git push *)`.
	// No other harness can restrict a tool that way, so they grant it whole.
	Narrowed []string
	// Raw is the tool list as the source wrote it, so writing back to the same harness keeps it.
	Raw *yaml.Node
}

// Add appends c unless it is already present.
func (t *ToolSet) Add(c Capability) {
	if !t.Has(c) {
		t.Caps = append(t.Caps, c)
	}
}

// Granted returns the capabilities t grants, expanding All.
func (t ToolSet) Granted() []Capability {
	if t.All {
		return slices.Clone(Capabilities)
	}
	return t.Caps
}

// Has reports whether c is granted, either explicitly or through All.
func (t ToolSet) Has(c Capability) bool {
	if t.All {
		return true
	}
	for _, x := range t.Caps {
		if x == c {
			return true
		}
	}
	return false
}

// Invocation describes who may start a skill or command.
type Invocation struct {
	UserInvocable  bool
	ModelInvocable bool
	ArgumentHint   string
}

// Resource is a file bundled with a skill, kept byte for byte with its mode.
type Resource struct {
	Path string
	Mode fs.FileMode
	Data []byte
}

// Field is a frontmatter key the IR does not model, kept with its YAML value in source order so
// writing back to the source harness loses nothing.
type Field struct {
	Key   string
	Value *yaml.Node
}

// Note is something a reader could not carry into the IR, such as a bundled file it skipped.
// Writers report each note as a warning.
type Note struct {
	Field  string
	Detail string
}

// Source records where an item was read from.
type Source struct {
	Harness harness.ID
	Path    string
	// Rel is a command's slash-separated path below its commands directory, or its file name when
	// it was read from outside one; the command's name is derived from it.
	Rel string
}

// Item is one customization in harness-neutral form.
type Item struct {
	Kind        Kind
	Name        string
	Description string
	Body        string
	Invocation  Invocation
	// Tools is nil for kinds that carry no tool list.
	Tools *ToolSet
	// Model is the source value verbatim; empty means inherit.
	Model string
	// Effort is the reasoning effort in the source's terms; empty means the default.
	Effort string
	// Preload names the skills an agent loads in full when it starts.
	Preload    []string
	Resources  []Resource
	Extensions []Field
	// Native is the frontmatter as the source wrote it, in order, for a writer to the same harness.
	Native []Field
	Notes  []Note
	Source Source
}

// Extension returns the value of an unmodelled frontmatter key.
func (i *Item) Extension(key string) (*yaml.Node, bool) {
	for _, f := range i.Extensions {
		if f.Key == key {
			return f.Value, true
		}
	}
	return nil, false
}

var skillName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ValidateSkillName applies the Agent Skills naming rule: lowercase letters, digits and
// single hyphens, at most 64 characters. Antigravity does not enforce it, Codex caps the length,
// so a writer checks it before emitting a name that another harness might reject.
func ValidateSkillName(name string) error {
	if len(name) > 64 {
		return fmt.Errorf("skill name %q is longer than 64 characters", name)
	}
	if !skillName.MatchString(name) {
		return fmt.Errorf("skill name %q must be lowercase letters, digits and single hyphens", name)
	}
	return nil
}

// CommandSkillName derives a skill name from a command file path relative to its commands
// directory: `frontend/component.md` becomes `frontend-component`, mirroring Claude's
// `/frontend:component` namespace with a separator every harness accepts. Distinct paths can derive
// the same name (`A/b.md`, `a-b.md`), so callers check the whole commands directory for collisions.
func CommandSkillName(rel string) string {
	rel = strings.TrimSuffix(path.Clean(strings.ReplaceAll(rel, `\`, "/")), ".md")
	return strings.ToLower(strings.ReplaceAll(rel, "/", "-"))
}
