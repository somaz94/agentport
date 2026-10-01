// Package crosswalk answers "what does harness B call harness A's X?" — the table behind
// `agentport map`. Field-level facts and their evidence live in docs/spec/.
package crosswalk

import (
	"fmt"
	"slices"
	"strings"

	"github.com/somaz94/agentport/internal/harness"
)

// Row is one concept and its counterpart in every harness.
type Row struct {
	Kind    string                `json:"kind"`
	Concept string                `json:"concept"`
	Terms   map[harness.ID]string `json:"terms"`
}

// Rows is the crosswalk in display order.
var Rows = []Row{
	{
		Kind:    "instructions",
		Concept: "Always-on instructions",
		Terms: map[harness.ID]string{
			harness.Claude:      "CLAUDE.md (+ .claude/rules/*.md); AGENTS.md only when no CLAUDE.md exists",
			harness.Codex:       "AGENTS.md (AGENTS.override.md wins), 32 KiB budget for project files",
			harness.Antigravity: "AGENTS.md or GEMINI.md (both load if both exist); rules/*.md need a trigger",
		},
	},
	{
		Kind:    "command",
		Concept: "User-invoked prompt",
		Terms: map[harness.ID]string{
			harness.Claude:      "Slash command .claude/commands/<name>.md, merged into skills (/name)",
			harness.Codex:       "Custom prompts removed in rust-v0.118.0; use a skill ($name)",
			harness.Antigravity: "Workflows are deprecated; use a skill (/name)",
		},
	},
	{
		Kind:    "skill",
		Concept: "Skill (Agent Skills SKILL.md)",
		Terms: map[harness.ID]string{
			harness.Claude:      ".claude/skills/<name>/SKILL.md",
			harness.Codex:       ".agents/skills/<name>/SKILL.md (+ agents/openai.yaml sidecar)",
			harness.Antigravity: ".agents/skills/<name>/SKILL.md, ~/.gemini/config/skills/",
		},
	},
	{
		Kind:    "agent",
		Concept: "Subagent definition",
		Terms: map[harness.ID]string{
			harness.Claude:      ".claude/agents/<name>.md (Markdown + YAML)",
			harness.Codex:       ".codex/agents/<name>.toml (developer_instructions)",
			harness.Antigravity: ".agents/agents/<name>.md, ~/.gemini/config/agents/ (app and CLI only)",
		},
	},
	{
		Kind:    "delegate",
		Concept: "Tool that starts a subagent",
		Terms: map[harness.ID]string{
			harness.Claude:      "Agent (subagent_type)",
			harness.Codex:       "spawn_agent (agent_type)",
			harness.Antigravity: "invoke_subagent",
		},
	},
	{
		Kind:    "hook",
		Concept: "Lifecycle hooks",
		Terms: map[harness.ID]string{
			harness.Claude:      "settings.json hooks",
			harness.Codex:       "hooks.json or config.toml [hooks]; each hook must be trusted before it runs",
			harness.Antigravity: "hooks.json",
		},
	},
	{
		Kind:    "mcp",
		Concept: "MCP servers",
		Terms: map[harness.ID]string{
			harness.Claude:      ".mcp.json, ~/.claude.json",
			harness.Codex:       "config.toml [mcp_servers.<id>]",
			harness.Antigravity: "mcp_config.json",
		},
	},
	{
		Kind:    "plugin",
		Concept: "Plugin bundle",
		Terms: map[harness.ID]string{
			harness.Claude:      ".claude-plugin/plugin.json",
			harness.Codex:       "Reads the Claude plugin manifest",
			harness.Antigravity: "plugins/<name>/plugin.json",
		},
	},
	{
		Kind:    "root",
		Concept: "Configuration roots (user / project)",
		Terms: map[harness.ID]string{
			harness.Claude:      "~/.claude/ / .claude/",
			harness.Codex:       "~/.codex/ + ~/.agents/ / .codex/ + .agents/",
			harness.Antigravity: "~/.gemini/config/ / .agents/",
		},
	},
}

// Kinds returns the row kinds in display order.
func Kinds() []string {
	out := make([]string, len(Rows))
	for i, r := range Rows {
		out[i] = r.Kind
	}
	return out
}

// Filter returns the rows of kind, or every row when kind is empty.
func Filter(kind string) ([]Row, error) {
	if kind == "" {
		return slices.Clone(Rows), nil
	}
	for _, r := range Rows {
		if r.Kind == kind {
			return []Row{r}, nil
		}
	}
	return nil, fmt.Errorf("unknown kind %q (want one of: %s)", kind, strings.Join(Kinds(), ", "))
}
