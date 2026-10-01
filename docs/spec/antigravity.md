# Antigravity customization format

Pinned to **Antigravity 2.19.1** (desktop app, bundled language server 1.11.0), verified 2026-10-01. Antigravity IDE 2.5.5 is a separate build; custom agents are an app and CLI feature (antigravity.google/docs/subagents) and were not probed there. For always-on instructions the IDE's language server behaves the same as the app's (probe).

Every row below is backed by one of these evidence classes:

| Mark | Meaning |
|---|---|
| **runtime** | Observed in a real conversation: the running app invoked a fixture subagent, and the tools offered to it and its tool-call outcomes were read from the trajectory |
| **probe** | Observed: a fixture was loaded by the app's own language server and the parsed result read back over its local RPC (see [Reproduce](#reproduce)) |
| **binary** | The string or handler package exists in the 2.19.1 language server binary, but no probe exercised it |
| **docs** | The app's bundled docs (`~/.gemini/antigravity/builtin/skills/agy-customizations/`) |

Where the bundled docs and a probe disagree, the probe wins and the row says so.

<br/>

## Locations

| Kind | Global | Workspace | Evidence |
|---|---|---|---|
| Skill | `~/.gemini/config/skills/<dir>/SKILL.md` | `.agents/skills/<dir>/SKILL.md` (also `.agent/`, `_agents/`, `_agent/`), cwd up to repo root | probe (global), docs (workspace) |
| Agent | `~/.gemini/config/agents/*.md` | `.agents/agents/*.md` | probe (global), docs (workspace) |
| Rule | `~/.gemini/config/rules/*.md` | `.agents/rules/*.md` | probe (global) |
| Always-on instructions | `~/.gemini/config/AGENTS.md`, `~/.gemini/config/GEMINI.md` | `AGENTS.md` / `GEMINI.md` in each directory up to repo root | probe (global), docs (workspace) |
| External skill roots | `~/.gemini/config/skills.json` | `.agents/skills.json` | probe (global) |

All four kinds are hot-reloaded; no restart is needed after a write (probe).

<br/>

## Skill frontmatter

| Key | Parsed as | Notes | Evidence |
|---|---|---|---|
| `name` | identity | No format check: `Bad_Name` loads. The directory name may differ from `name`; `name` wins | probe |
| `description` | description | Optional — a skill without one still loads (agents differ) | probe |
| `disable-model-invocation` | `disableModelInvocation` | Supported. The model cannot auto-activate the skill | probe |
| `disable-slash-command` | `disableSlashCommand` | Hides the skill from the `/` menu | probe |
| `metadata.icon` | `icon` | | probe |
| `user-invocable`, `allowed-tools`, `argument-hint`, any other key | — | Ignored silently, the skill still loads | probe |

The body is stored verbatim. Nothing in the binary or the docs indicates `$ARGUMENTS` substitution.

<br/>

## Agent frontmatter

A file that fails any **required** check is dropped **silently**: no load error in the RPC state, no log line.

| Key | Type | Behaviour | Evidence |
|---|---|---|---|
| `name` | string, **required** | Identity. The file name is ignored (`a.md` with `name: b` loads as `b`). Missing → dropped | probe |
| `description` | string, **required** | Missing → dropped | probe |
| `tools` | YAML **list** | Names are **not validated at load** — `[Read, Grep]` loads — but **an unknown name fails the subagent at spawn** (`tool "command_status" not found in registry`). A comma-separated string (`tools: a, b`) drops the whole agent at load. `[]` grants no tools. `send_message` is always added | probe, runtime |
| `tools` omitted | — | Default set: `send_message`, `view_file`, `read_url_content`, `search_web`, `schedule`, `generate_image`, plus `manage_task` at runtime. No shell, search, or edit tool | probe, runtime |
| `model` | enum | `inherit` (default), `flash_lite`, `flash`, `pro`. Any other value (`opus`, a full model ID) drops the whole agent | probe |
| `preloadSkills` | list of skill names | Becomes the skill preload list. An unknown name does not fail the load | probe |
| `inheritMcp` | bool | `false` stops the agent inheriting the user's MCP servers | probe |
| `inheritCustomizations` | bool | `false` stops inheriting the user's skills, agents, plugins, rules and hooks | probe |
| `mainAgent` | bool | `false` keeps the agent loaded but removes it from the agent picker list; it is still invocable as a subagent | probe, runtime |
| `hidden`, `background` | bool | Accepted; no effect visible in the parsed config | probe |
| `mcpServers`, `hooks` | — | Tags exist in the binary; not probed | binary |
| Any other key | — | Ignored | probe |

The frontmatter must be strict YAML. `description: Use when: x` (an unquoted `: `) is a parse error and drops the agent. Quote every scalar that may contain `: ` or a backtick.

<br/>

## Tool names

Every name below was offered to a subagent that listed it, in a real 2.19.1 conversation (runtime). Because an unknown name fails the spawn, the converter must emit only names from this table.

| Capability | Antigravity tool | Notes |
|---|---|---|
| Read a file | `view_file` | |
| List a directory | `list_dir` | |
| Find files by name | `find_by_name` | |
| Search file contents | `grep_search` | |
| Run a shell command | `run_command` | |
| Check or stop a background command | `manage_task` | Not `command_status`, which is not a registered tool |
| Create / overwrite a file | `write_to_file` | |
| Edit a file | `replace_file_content` | `multi_replace_file_content` is accepted but not offered |
| Fetch a URL | `read_url_content` | |
| Web search | `search_web` | |
| Ask the user | `ask_question` | |
| Delegate to a subagent | `invoke_subagent` | |
| Message another agent | `send_message` | Always added |

The main agent of a 2.19.1 conversation is offered a different set: `run_command`, `manage_task`, `view_file`, `write_to_file`, `replace_file_content`, `read_url_content`, `search_web`, `ask_question`, `invoke_subagent`, `define_subagent`, `manage_subagents`, `schedule`, `send_message`, `generate_image`. It has no `grep_search`, `find_by_name` or `list_dir` and searches through `run_command` (runtime).

<br/>

## Rule frontmatter

| Key | Values | Behaviour | Evidence |
|---|---|---|---|
| `trigger` | `always_on`, `model_decision`, `glob`, `manual` | **Required.** A rule with no frontmatter, a frontmatter without `trigger`, or an unknown value is not loaded | probe |
| `description` | string | Shown in the rule title; used by `model_decision` | probe |
| `globs` | string | File pattern for `glob` | probe |

`AGENTS.md` / `GEMINI.md` take no frontmatter and are always on. When both exist in the same directory, **both are loaded** as separate global entries; keep one.

Size limits (docs): 24,000 bytes per rule file, truncated on a line boundary; always-on and global rules share a 20,000-token budget, over which rules are demoted to path pointers.

<br/>

## `skills.json`

```json
{"entries": [{"path": "/absolute/path/to/skills", "exclude": ["synced"]}]}
```

- `path` must be **absolute** in 2.19.1. A `~/` path is rejected with `must be an absolute path`, although the bundled docs allow it (probe).
- Each entry is scanned one level deep; `exclude` matches directory names (probe).
- Skills reached this way load with Claude-only keys ignored, so a Claude Code skill directory can be registered as-is (probe).

<br/>

## Reproduce

The desktop app's language server runs standalone against any config root, so a fixture can be checked without touching the real `~/.gemini`:

```bash
LS=/Applications/Antigravity.app/Contents/Resources/bin/language_server
SANDBOX=$(mktemp -d)
mkdir -p "$SANDBOX/gemini/config/agents" "$SANDBOX/home" "$SANDBOX/ws"
# write fixtures under $SANDBOX/gemini/config/{agents,skills,rules}

cd "$SANDBOX/ws" && HOME="$SANDBOX/home" "$LS" -standalone=true \
  -gemini_dir="$SANDBOX/gemini" -app_data_dir=antigravity -headless=true \
  -http_server_port=47391 -csrf_token=probe -cdp_port=47392 &

rpc() { curl -sS -X POST "http://localhost:47391/exa.language_server_pb.LanguageServerService/$1" \
  -H 'Content-Type: application/json' -H 'x-codeium-csrf-token: probe' -d '{}'; }
rpc GetCustomizationStates   # every loaded skill / agent / rule with its status
rpc GetAgentScripts          # agents in the picker, with resolved tools and model tier
rpc GetAllSkills             # parsed skill frontmatter
rpc GetAllRules              # rules and always-on instructions
```

Without `-standalone=true` the server waits for initial metadata on stdin and aborts. `GetAllCustomAgentConfigs` returns `deprecated` in 2.19.1; use `GetAgentScripts` and `GetCustomizationStates`.

The standalone server has no credentials, so runtime checks go through the running app instead. Its language server listens on localhost with `--csrf_token` in its arguments; `~/.gemini/antigravity/bin/agentapi new-conversation --model=flash "<prompt>"` starts a conversation when `ANTIGRAVITY_LS_ADDRESS`, `ANTIGRAVITY_CSRF_TOKEN` and `ANTIGRAVITY_PROJECT_ID` (an id from `~/.gemini/config/projects/`) are set, and `GetCascadeTrajectory {"cascadeId": "<id>"}` returns every step, including the tools offered to each subagent under `generatorMetadata[].chatModel.tools`. Launch the app from a clean environment: a shell inherited from VS Code carries `ELECTRON_RUN_AS_NODE=1`, which makes the app exit immediately.
