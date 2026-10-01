# Codex customization format

Pinned to **Codex CLI `rust-v0.159.3`** (commit `01fc69f`), verified 2026-10-01 by reading the source at that tag. Codex was not installed for this check, so nothing here is end-to-end tested yet.

| Mark | Meaning |
|---|---|
| **source** | `github.com/openai/codex` at `rust-v0.159.3`; paths below are relative to `codex-rs/` |
| **docs** | learn.chatgpt.com/docs, used only where it agrees with the source |
| **release** | GitHub release notes / merged PRs |

Where the docs and the source disagree, the source wins and the row says so.

<br/>

## Locations

| Kind | User | Project |
|---|---|---|
| Skill | `~/.agents/skills/<dir>/SKILL.md`; `$CODEX_HOME/skills/` still loads but is deprecated | `.agents/skills/` in every directory from the project root down to cwd; `.codex/skills/` |
| Agent role | `$CODEX_HOME/agents/**/*.toml` (recursive) | `.codex/agents/**/*.toml`, trusted projects only |
| Instructions | `$CODEX_HOME/AGENTS.override.md`, else `$CODEX_HOME/AGENTS.md` | `AGENTS.override.md` → `AGENTS.md` → `project_doc_fallback_filenames`, first existing per directory |
| Hooks | `$CODEX_HOME/hooks.json` or `[hooks]` in `config.toml` | `.codex/hooks.json`, trusted projects only |

`$CODEX_HOME` defaults to `~/.codex`. The project root is the nearest ancestor holding a `project_root_markers` entry (default `.git`). Custom prompts (`~/.codex/prompts`) were removed in `rust-v0.118.0` (PR #16115).

<br/>

## Skill frontmatter

Only three keys are read; everything else is ignored (source, `skills/src/parser.rs`).

| Key | Behaviour |
|---|---|
| `name` | Defaults to the parent directory name. Max 64 chars. Whitespace collapsed |
| `description` | **Required**, non-empty. Cut to 1,024 chars in the prompt catalog |
| `metadata.short-description` | Short listing text. `metadata` must be a mapping if present |

Invalid YAML gets a repair pass that single-quotes unquoted values containing `: `. The body is injected verbatim inside `<skill>` tags: **no placeholder substitution**, `$ARGUMENTS` included. Users invoke a skill with a `$name` mention or browse with `/skills`.

Discovery follows directory symlinks for user, repo and admin roots and skips hidden directories (source, `ext/skills/src/loader/discovery.rs`).

### Sidecar `agents/openai.yaml`

Read leniently; invalid YAML ignores the whole file with a warning.

| Key | Behaviour |
|---|---|
| `policy.allow_implicit_invocation` | Default `true`. `false` hides the skill from the model's catalog — the equivalent of Claude's `disable-model-invocation: true` |
| `interface.display_name`, `short_description`, `default_prompt`, `icon_small`, `icon_large`, `brand_color` | UI presentation |
| `dependencies.tools[]` | `type` + `value` required |

<br/>

## Agent role files

Identity is the `name` key, not the file name. A role file that is itself a **symlink fails at spawn** ("agent type is currently not available"); a symlinked parent directory is fine (source, `core/src/agent/role_tests.rs`).

| Key | Behaviour |
|---|---|
| `name` | **Required**, non-blank |
| `description` | **Required**, non-blank |
| `developer_instructions` | **Required**, non-blank. The agent's system prompt |
| `nickname_candidates` | Optional non-empty list; entries limited to ASCII letters, digits, space, `-`, `_` |
| `model`, `model_reasoning_effort`, `model_reasoning_summary`, `model_verbosity`, `personality`, `service_tier` | Applied to the spawned agent |
| `[features]` | Only `false` values for `shell_tool`, `apps`, `plugins`, `memories`, `request_permissions_tool` are applied; `true` is ignored |
| `[skills]` | Only `config = [{name\|path, enabled = false}]`, `bundled.enabled = false`, `include_instructions = false` |

**Unknown keys are fatal for the role.** The file is parsed with `deny_unknown_fields` over a flattened `config.toml` schema, so a key that is not a `config.toml` key drops the role with a startup warning ("Ignoring malformed agent role definition"). Keys that are valid in `config.toml` but not in the list above — `sandbox_mode`, `approval_policy`, `mcp_servers`, `web_search` — parse and are **silently not applied** (since `rust-v0.149.0`, PR #39299; the subagent docs still list `sandbox_mode` and `mcp_servers`).

A user role overrides a built-in one of the same name (`default`, `explorer`, `worker`). A project role overrides a user role field by field.

<br/>

## Tool names

There is **no per-role tool allowlist**. The only tool a role can remove is the shell (`[features] shell_tool = false`).

| Capability | Codex tool | Notes |
|---|---|---|
| Shell | `exec_command` (+ `write_stdin`) | Gate `shell_tool` (default on). Also the only way to **read, list or search files** — there are no built-in read / grep / glob tools |
| Edit / create files | `apply_patch` | Freeform grammar tool; gated by the model catalog, not by a user key |
| Web search | hosted `web_search` | Top-level `web_search = "disabled" \| "cached" \| "indexed" \| "live"` (default `cached`); not settable per role |
| Delegate | `spawn_agent` (arg `agent_type`) | Namespace `multi_agent_v1` by default; `agent_type` is offered only when a user-defined role exists |
| Ask the user | `request_user_input` | Root thread and Plan mode only |
| Plan / todo | `update_plan` | **Off by default** since `rust-v0.152.0`; `[tools.update_plan] enabled = true` |

<br/>

## AGENTS.md

Project docs share one `project_doc_max_bytes` budget, default 32,768 bytes; the file that crosses it is truncated and later files are skipped. The global `$CODEX_HOME/AGENTS.md` is loaded separately from that budget. Nothing project-level loads in an untrusted project (source, `core/src/agents_md.rs`).

<br/>

## Hooks

| Field | Behaviour |
|---|---|
| Events | `PreToolUse`, `PermissionRequest`, `PostToolUse`, `PreCompact`, `PostCompact`, `SessionStart`, `SessionEnd`, `UserPromptSubmit`, `SubagentStart`, `SubagentStop`, `Stop`, `Interrupt` |
| Handler `type` | `command` and `mcp_tool` run; `prompt` and `agent` parse but are skipped |
| `timeout` | Seconds, default 600 (`SessionEnd` / `Interrupt`: default 1, clamped to 1–3) |
| Trust | A non-managed hook runs only after the user trusts its exact definition (hash in `[hooks.state."<key>"] trusted_hash`). A converted hook does not fire until reviewed |
| `hooks.json` | Top-level keys limited to `description` and `hooks` |

Tool names in hook payloads and matchers are Claude-compatible: `exec_command` is reported as `tool_name: "Bash"` with `{"command": …}`, and the matcher names `Write` and `Edit` select `apply_patch`. The `apply_patch` payload is `{"command": "<patch text>"}` with **no file path field**; paths must be parsed from its `*** Add File:` / `*** Update File:` / `*** Delete File:` / `*** Move to:` lines (source, `core/src/tools/hook_names.rs`, `core/src/tools/handlers/apply_patch.rs`).

<br/>

## Built-in Claude importer

`/import` in the TUI (not a `codex` subcommand) converts Claude commands into skills named `source-command-<slug>`, and **skips any command whose body contains** `$ARGUMENTS`, `$<digit>`, `{{ … }}`, `` !` ``, or a token starting with `@`. Claude agents become role files with `name`, `description`, `developer_instructions`; `tools` is not mapped, and `permissionMode` is written to `sandbox_mode`, which a role does not apply (source, `core-plugins/src/command_migration.rs`, `external-agent-migration/src/subagents.rs`).
