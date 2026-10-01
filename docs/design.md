# Design

agentport keeps Claude Code as the hub and translates its customizations into Codex and Antigravity. This document records the decisions and the reasons for them. Facts about each harness's format, with their evidence, are in [spec/](spec/).

<br/>

## Hub model

- `~/.claude/` (and a project's `.claude/`) is the single source of truth. Targets are generated.
- A change made in a target can come back through `adopt`, which converts it back and merges it into the hub file while keeping hub-only fields such as `allowed-tools`. A full bidirectional three-way merge is out of scope.
- Paired translation mirrors (for example an `agents-ko/` beside `agents/`) are converted the same way into the matching target directory, where no harness loads them.

Existing tools were considered first. rulesync supports all three harnesses but moves the source into its own neutral directory, drops unknown fields with at most a warning, copies `tools` and hook matchers without translating them, and still generates targets the harnesses have removed. The built-in importers are one-way and one-shot, and Codex's skips any command that uses `$ARGUMENTS`. What agentport adds is the loss report as the product, semantic translation, the Claude directory as the hub, and ownership tracking.

<br/>

## Commands

| Command | Purpose |
|---|---|
| `map [kind]` | The crosswalk: what each harness calls a concept and where it lives |
| `scan` | What exists at each harness location, with a portability grade per item |
| `convert <path> --to <harness>` | Convert one item in any direction; preview by default |
| `sync --to <harnesses>` | Convert the whole hub; dry run by default, `--apply` writes, `--check` exits non-zero when anything would change, `--strict` exits 2 on any loss |
| `status` | Classify every target file (see [Sync safety](#sync-safety)) |
| `adopt <target-path>` | Bring a target edit back into the hub |
| `doctor` | Installed versions, the path table, use of deprecated locations |
| `link skills --to <harness>` | Use hub skills in place without copying (see [Link mode](#link-mode)) |

`agentport --help` is authoritative for what a given build implements.

<br/>

## Conversion pipeline

`reader(harness A) → IR → writer(harness B) + loss report`

- The IR (`internal/ir`) carries kind, name, description, body, invocation policy, a tool set expressed as capabilities (read, search, glob, shell, edit, write, web fetch, web search, delegate, ask user, plan), the model, bundled resources byte for byte with their file modes, and `Extensions` for source-only fields so a round trip back to the source loses nothing.
- Each source field ends in exactly one loss status: `mapped`, `transformed`, `approximated`, `dropped` or `warn`. Reports are available as text and JSON.
- Locations come only from the version-pinned table in `internal/paths`. Antigravity's directories have moved several times; a move is a one-row change there plus a `doctor` warning for the old location.
- Frontmatter is read leniently, with the same repair pass Claude Code and Codex apply, and written as strict YAML with `description` single-quoted.

<br/>

## Per-kind rules

<br/>

### Skill → skill

- The skill directory is copied byte for byte, executable bits included. Claude's `skills/synced/` (skills managed by claude.ai) is never a source.
- `name` and `description` carry over. `disable-model-invocation: true` stays as-is for Antigravity, which supports it, and becomes `policy.allow_implicit_invocation: false` in Codex's `agents/openai.yaml` sidecar. `user-invocable: false` becomes Antigravity's `disable-slash-command: true`; Codex has no counterpart. `allowed-tools` and `argument-hint` are dropped, the hint surviving in the arguments preamble below.
- Antigravity does not validate names and Codex only caps their length, so the writer applies the Agent Skills rule (lowercase letters, digits, single hyphens, 64 characters) itself.

<br/>

### Command → skill

- Codex removed custom prompts and Antigravity deprecated workflows, so a command becomes a skill. Its name is its path under the commands directory with `/` replaced by `-` (Claude shows it as `/a:b`).
- Neither target substitutes `$ARGUMENTS`. The body is left untouched and a marked preamble is prepended, so `adopt` can remove it exactly:

  ```markdown
  <!-- agentport:args:begin -->
  ## Arguments
  Invoked as `/commit [scope-hint | recommend-only]`. The text typed after the skill name is the arguments; wherever this file says `$ARGUMENTS`, use that text.
  <!-- agentport:args:end -->
  ```

  The preamble is safe to add even when the body has no placeholder, because Claude Code itself appends the arguments in that case. Positional arguments, `` !`cmd` `` injection and `@file` references produce an `approximated` entry.
- Codex model invocation is off by default for converted commands, so a mutating workflow never starts from a description match alone. Antigravity cannot turn it off for a skill with a slash command, which is a `warn`.
- An unquoted bracketed `argument-hint` is valid YAML and parses as a one-element sequence; the reader accepts that shape.

<br/>

### Agent → Antigravity agent

Antigravity reports none of these failures, so the writer checks all of them before writing:

- `name` and `description` are required, and the identity is `name`, not the file name.
- `tools` is a YAML list of names from the runtime-verified table in [spec/antigravity.md](spec/antigravity.md). An unknown name makes the subagent fail when it starts.
- A Claude agent with no `tools` may use every tool, while an Antigravity agent with no `tools` gets no shell, search or edit tool. Omitted or `*` therefore converts to the full list.
- `model` is one of `inherit`, `flash_lite`, `flash`, `pro`. Anything else drops the agent, so unmapped models become `inherit` with a loss entry.
- `mainAgent: false` keeps a converted subagent out of the app's agent picker; it is still invocable as a subagent.

<br/>

### Agent → Codex role

- Only keys Codex applies to a role are written: `name`, `description`, `developer_instructions` (all required), optionally `model`, `model_reasoning_effort`, `[features]`. One unknown key drops the whole role, and keys such as `sandbox_mode` parse but are not applied, so neither is ever emitted.
- Codex has no per-role tool allowlist, so `tools` is `dropped`. Codex has no file-read or search tool either: files are read through the shell. Turning the shell off (`[features] shell_tool = false`) would leave a read-only reviewer unable to read anything, so it is used only for an agent that has no read, search, glob, edit, write or shell capability at all. Otherwise the inability to enforce read-only is a `warn`.
- A symlinked role file fails at spawn, so roles are always copied.
- `effort` maps to `model_reasoning_effort`, with `max` becoming `xhigh`.

<br/>

### References inside bodies

- Agent names a body delegates to (for example "delegate to `git-commit-runner`") are checked against the set converted for that target; a missing one is a `warn`.
- Claude-only tool names in prose (`AskUserQuestion`, `TodoWrite`) produce a `warn` naming the target's counterpart.
- Paths under `~/.claude/` are not rewritten by default; on the same machine they still resolve. A `rewrite` rule in the config enables substitution.

<br/>

### Instructions, hooks, MCP

- **Instructions** are not generated. A hand-condensed `AGENTS.md` is the source for non-Claude harnesses; agentport checks its size against each harness's budget, warns when a `CLAUDE.md` section changed after `AGENTS.md` was last updated, and keeps one file per directory where Antigravity would otherwise load both `AGENTS.md` and `GEMINI.md`.
- **Hooks** map almost one to one into Codex, which reports its shell tool as `Bash` and lets the `Edit` / `Write` matchers select `apply_patch`. `apply_patch` carries no file path, so post-edit hooks need a shim that parses the patch. A converted Codex hook runs only after the user trusts it, which `sync` points out. Antigravity's events and payloads differ enough to need a normalizing shim.
- **MCP** converts between JSON and TOML when a server list exists.

<br/>

## Sync safety

- Dry run by default; `--apply` writes.
- Each target root has `.agentport/manifest.json` recording, per written file, its source, the source hash, the output hash and the generator version.
- Every target path is classified: `new`, `update` (source changed, target untouched), `unchanged`, `drift` (edited by hand — skipped and reported until `adopt` or `--force`), `orphan` (source gone and target untouched — deleted), `conflict` (an unmanaged file is in the way), `unmanaged` (never touched).
- No directory is removed wholesale and nothing is synced with a delete flag; only unedited manifest entries are deleted.
- Two runs on the same input produce no change the second time. Tests enforce it.

<br/>

## Configuration

`$XDG_CONFIG_HOME/agentport/config.yaml`, all keys optional:

```yaml
hub: claude
targets: [antigravity, codex]
scope: user
exclude: ['*-private', 'scratch-*']   # project directories never synced
excludeOssForks: true                 # skip repositories that have an `upstream` remote
pairs: {agents: agents-ko, commands: commands-ko, skills: skills-ko}
skip: ['skills/synced/**']
rewrite: []                           # e.g. {from: '~/.claude/CLAUDE.md', to: '~/.gemini/config/AGENTS.md'}
modelMap: {}                          # e.g. {opus: pro, haiku: flash}
```

<br/>

## Link mode

For trying hub skills without converting them:

- Antigravity: a `skills.json` entry pointing at the hub skills directory. The path must be absolute; the pinned version rejects `~/`.
- Codex: a symlink per skill under `~/.agents/skills/`; Codex follows directory symlinks.

Nothing is translated in this mode, so `$ARGUMENTS` stays literal and Claude-only fields are ignored, and it cannot carry commands or agents. `sync` remains the default.

Serving skills and commands from one MCP server to all three harnesses was rejected: prompt support differs per client, the native `/` menu and progressive disclosure are lost, and agents cannot be served that way.
