// Package harness names the agent harnesses agentport converts between.
package harness

import (
	"fmt"
	"strings"
)

// ID identifies a harness.
type ID string

// Supported harnesses.
const (
	Claude      ID = "claude"
	Codex       ID = "codex"
	Antigravity ID = "antigravity"
)

// All lists the supported harnesses in display order.
var All = []ID{Claude, Codex, Antigravity}

var aliases = map[string]ID{
	"claude":      Claude,
	"claude-code": Claude,
	"codex":       Codex,
	"antigravity": Antigravity,
	"agy":         Antigravity,
}

// Parse resolves a harness name or alias, case-insensitively.
func Parse(s string) (ID, error) {
	if id, ok := aliases[strings.ToLower(strings.TrimSpace(s))]; ok {
		return id, nil
	}
	return "", fmt.Errorf("unknown harness %q (want one of: %s)", s, strings.Join(Names(), ", "))
}

// Names returns the canonical names of all supported harnesses.
func Names() []string {
	out := make([]string, len(All))
	for i, id := range All {
		out[i] = string(id)
	}
	return out
}

// Title returns the product name shown to users.
func (id ID) Title() string {
	switch id {
	case Claude:
		return "Claude Code"
	case Codex:
		return "Codex"
	case Antigravity:
		return "Antigravity"
	}
	return string(id)
}
