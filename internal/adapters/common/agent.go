package common

import (
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/somaz94/agentport/internal/harness"
	"github.com/somaz94/agentport/internal/ir"
	"github.com/somaz94/agentport/internal/loss"
)

// ForeignAgentField reports what happened to f, a field only the agent's source harness has, in a
// target that writes a tool list: the fields that shaped the tool set are reflected there.
func ForeignAgentField(item *ir.Item, f ir.Field, r *loss.Report) {
	switch {
	case item.Source.Harness == harness.Claude && f.Key == "disallowedTools":
		r.Add(f.Key, loss.Transformed, "the denied tools were left out of tools")
	case item.Source.Harness == harness.Antigravity && f.Key == "mainAgent" && SubagentOnly(f.Value):
		r.Add(f.Key, loss.Mapped, "false: a subagent only, as every agent of the target is")
	case item.Source.Harness == harness.Codex && f.Key == "features" && hasKey(f.Value, "shell_tool"):
		r.Add(f.Key, loss.Transformed, "shell_tool is reflected in tools")
		var other []string
		for i := 0; i+1 < len(f.Value.Content); i += 2 {
			if k := f.Value.Content[i].Value; k != "shell_tool" {
				other = append(other, k)
			}
		}
		if len(other) > 0 {
			r.Addf(f.Key, loss.Dropped, "no counterpart: %s", strings.Join(other, ", "))
		}
	default:
		r.Addf(f.Key, loss.Dropped, "%s-only field", item.Source.Harness.Title())
	}
}

func hasKey(m *yaml.Node, key string) bool {
	if m.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return true
		}
	}
	return false
}

// IsBool reports whether n decodes as a boolean the way yaml.v3, which Antigravity reads frontmatter
// with, decodes one: true or false in three spellings, the YAML 1.1 words yes, no, on, off, y and n
// even when quoted, or nothing.
func IsBool(n *yaml.Node) bool {
	var b *bool
	return n.Decode(&b) == nil
}

// SubagentOnly reports whether n, an Antigravity mainAgent value, is false: the agent stays out of
// the picker and runs only as a subagent.
func SubagentOnly(n *yaml.Node) bool {
	var b *bool
	return n.Decode(&b) == nil && b != nil && !*b
}
