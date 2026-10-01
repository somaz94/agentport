# Claude Code customization format

Pinned to **Claude Code 2.1.285**, verified 2026-10-01.

| Mark | Meaning |
|---|---|
| **docs** | code.claude.com/docs/en/{skills, sub-agents, hooks, memory, tools-reference}, fetched 2026-10-01 |
| **binary** | `strings` of the 2.1.285 native binary, matched against a parser or schema literal |
| **changelog** | `CHANGELOG.md` in `anthropics/claude-code` |

Claude Code is agentport's hub, so this file describes what the **reader** must accept.

<br/>

## Locations

| Kind | User | Project |
|---|---|---|
| Skill | `~/.claude/skills/<dir>/SKILL.md` | `.claude/skills/<dir>/SKILL.md` (nested `.claude/skills` also load, namespaced) |
| Command | `~/.claude/commands/**/*.md` | `.claude/commands/**/*.md` |
| Agent | `~/.claude/agents/*.md` | `.claude/agents/*.md` |
| Instructions | `~/.claude/CLAUDE.md` | `CLAUDE.md`, `.claude/CLAUDE.md`, `CLAUDE.local.md`, `.claude/rules/*.md` |
| Hooks | `~/.claude/settings.json` `hooks` | `.claude/settings.json` `hooks` |

`~/.claude/skills/synced/` holds skills synced from claude.ai; they load as `/anthropic-skills:<name>` and are not the user's own source.

<br/>

## Commands and skills are one system

Commands were merged into skills: `.claude/commands/deploy.md` and `.claude/skills/deploy/SKILL.md` both create `/deploy` (docs). A command accepts every skill key except `name` and `paths`; its name is the file name, and a subdirectory becomes a `:` namespace (`frontend/component.md` → `/frontend:component`). On a name collision the skill wins over the command (docs).

<br/>

## Skill frontmatter

No key is required. `name` defaults to the directory name; a missing `description` falls back to the first non-empty body line (docs). No name format is enforced. Unknown keys are not errors — they only emit telemetry (binary).

| Key | Meaning | Evidence |
|---|---|---|
| `name`, `description` | Identity and listing text. `description` + `when_to_use` are truncated at 1,536 chars in the listing | docs |
| `when_to_use` | Appended to `description` | docs |
| `argument-hint` | Autocomplete hint | docs |
| `arguments` | Named positional arguments for `$name` substitution; space-separated string or YAML list | docs |
| `disable-model-invocation` | `true` blocks model auto-invocation, preloading into subagents, and scheduled firing | docs |
| `user-invocable` | `false` hides it from the `/` menu | docs |
| `allowed-tools`, `disallowed-tools` | Tool pre-approval / removal while active; space- or comma-separated string, or YAML list. `disallowedTools` is an accepted alias | docs, binary |
| `model` | `/model` values or `inherit` | docs |
| `effort` | `low` … `max` (binary also takes an integer) | docs, binary |
| `context`, `agent`, `background` | `context: fork` runs in a subagent of type `agent` | docs |
| `hooks`, `paths`, `shell` | Invocation hooks, auto-activation globs, `bash` / `powershell` | docs |
| `license`, `compatibility`, `metadata` | Agent Skills spec fields; accepted, not acted on | docs |
| `version` | Accepted and kept as bookkeeping, not surfaced to users | binary |

The portable subset accepted by claude.ai uploads and the Skills API is `name`, `description`, `license`, `compatibility`, `metadata`, `allowed-tools`; any other key is a hard error there (docs).

Frontmatter parsing is lenient: a repair pass re-quotes values matching ``[{}[\]*&#!|>%@`]`` or `: ` before parsing (binary). A file Claude Code reads can therefore be invalid strict YAML, and every writer must emit strict YAML.

agentport's reader repairs only when the strict parse fails, then single-quotes each top-level value that does not parse on its own (`Use when: x`, a leading backtick, `[a] [b]`) or contains a comment marker. It deliberately differs from Claude Code in one way: a value that parses on its own as a flow list (`tools: [Read, Grep]`) stays a list, where Claude Code would turn it into a string.

<br/>

## Body substitution

Applied to command and skill bodies, in this order (binary): base-directory line (skills only) → arguments → `${CLAUDE_SKILL_DIR}`, `${CLAUDE_PROJECT_DIR}`, `${CLAUDE_SESSION_ID}`, `${CLAUDE_EFFORT}` → shell injection. Plugin skills also get `${CLAUDE_PLUGIN_ROOT}` and `${CLAUDE_PLUGIN_DATA}` (docs).

| Syntax | Meaning | Evidence |
|---|---|---|
| `$ARGUMENTS` | The whole argument string as typed | docs |
| `$ARGUMENTS[N]`, `$N` | 0-based argument, shell-style quoting. Missing → left as literal text | docs, binary |
| `$name` | From `arguments:`, by position. Missing → empty string | docs |
| `\$` | Escapes a placeholder | binary |
| no placeholder used | Claude Code appends `ARGUMENTS: <input>` to the body | docs, binary |
| `` !`cmd` `` | Shell injection, only at line start or after whitespace; a fenced block opened with ` ```! ` is the multi-line form. Non-zero exit aborts the invocation | docs, binary |
| `@path` | Becomes a file attachment. Undocumented, still active in 2.1.285 | binary |

Argument values are inserted literally and are not re-expanded (binary).

<br/>

## Agent frontmatter

`name` and `description` are required; a file missing either is skipped (binary). `name` may not contain `:` or start with `-`.

| Key | Values | Evidence |
|---|---|---|
| `tools` | Comma-separated string or YAML list; space-separated also works. Omitted → every tool available to subagents. `*` → all. Specifiers: `Bash(git push *)`, `Agent(worker, researcher)`, `mcp__<server>`, `mcp__<server>__*` | docs, binary |
| `disallowedTools` | Removed from the inherited or listed set; a specifier still removes the whole tool | docs |
| `model` | `inherit`, `sonnet`, `opus`, `haiku`, `fable`, or a full model ID. Omitted → `CLAUDE_CODE_SUBAGENT_MODEL`, then the main model | docs, binary |
| `permissionMode` | `default`, `acceptEdits`, `auto`, `dontAsk`, `bypassPermissions`, `plan` | docs |
| `skills` | Skills preloaded with full content | docs |
| `mcpServers`, `hooks` | Name or inline config; scoped hooks | docs |
| `maxTurns`, `effort`, `background`, `isolation`, `memory`, `color`, `initialPrompt`, `omitClaudeMd` | Runtime behaviour with no counterpart in other harnesses | docs |

<br/>

## Tool names

Canonical names a `tools` / `allowed-tools` list can use (docs, tools-reference): `Read`, `Write`, `Edit`, `Glob`, `Grep`, `Bash`, `PowerShell`, `WebFetch`, `WebSearch`, `Agent`, `AskUserQuestion`, `TodoWrite`, `TaskCreate`, `TaskGet`, `TaskList`, `TaskUpdate`, `TaskStop`, `NotebookEdit`, `Skill`, `LSP`, `Monitor`, `SendMessage`, `ToolSearch`, plus session-specific tools.

Aliases the reader must normalize (binary alias map): `Task` → `Agent` (renamed in 2.1.63), `KillShell` / `KillBash` → `TaskStop`, `ListMcpResources` → `ListMcpResourcesTool`, `ReadMcpResource` → `ReadMcpResourceTool`. `MultiEdit` and `LS` are gone from the tool table; treat them as `Edit` and `Glob`.

`TodoWrite` is off by default in favour of the `Task*` tools. `Glob` and `Grep` are absent by default on macOS and Linux, where search runs through `Bash`; they return for a subagent that lists them without `Bash` (docs).

<br/>

## Hooks

| Field | Behaviour | Evidence |
|---|---|---|
| `timeout` | **Seconds.** Defaults: 600 for `command` / `http` / `mcp_tool`, 30 for `prompt`, 60 for `agent`; lower on a few events | docs, binary |
| `matcher` | `*`, `""` or omitted match all. Letters, digits, `_`, `-`, space, `,`, `\|` only → exact names split on `\|` or `,`. Anything else → unanchored JavaScript regex | docs |
| handler `type` | `command`, `http`, `mcp_tool`, `prompt`, `agent` | docs |

Tool events whose `matcher` is a tool name: `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `PermissionRequest`, `PermissionDenied`. Session events used by agentport's hook mapping: `SessionStart`, `PreCompact`, `Stop`, `SessionEnd`, `UserPromptSubmit`, `SubagentStart`, `SubagentStop`.

<br/>

## AGENTS.md

Read natively since 2.1.277 (changelog), but only as a **fallback**: by default `AGENTS.md` and `.claude/AGENTS.md` are read when no `CLAUDE.md`, `.claude/CLAUDE.md` or `CLAUDE.local.md` exists in the working directory or above it. `~/.claude/CLAUDE.md` does not count toward that check. `AGENTS.override.md` and `.agents/` are not read (docs, binary).
