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

- The IR (`ir.Item` in `internal/ir`) is the harness-neutral item. Three choices shape it: tools are a set of capabilities (`ir.Capabilities`: read, search, glob, shell, edit, write, web fetch, web search, delegate, ask user, plan) rather than tool names; bundled resources are kept byte for byte with their file modes; and fields only the source harness has are kept, so a round trip back to the source loses nothing.
- Every loss entry names a field and one status: `mapped`, `transformed`, `approximated`, `dropped` or `warn`. A field can collect several entries — a body can be both transformed (the arguments preamble) and approximated (shell injection the target does not run). Reports are available as text and JSON.
- Locations come only from the version-pinned table in `internal/paths`. Antigravity's directories have moved several times; a move is a one-row change there plus a `doctor` warning for the old location.
- Frontmatter is read leniently, with the same repair pass Claude Code and Codex apply, and written as strict YAML with `description` single-quoted.

<br/>

## Per-kind rules

<br/>

### Skill → skill

- The skill directory is copied byte for byte, executable bits included. Claude's `skills/synced/` (skills managed by claude.ai) is never a source.
- `name` and `description` carry over. `disable-model-invocation: true` stays as-is for Antigravity, which supports it, and becomes `policy.allow_implicit_invocation: false` in Codex's `agents/openai.yaml` sidecar. `user-invocable: false` becomes Antigravity's `disable-slash-command: true`; Codex has no counterpart. `allowed-tools` and `argument-hint` are dropped, the hint surviving in the arguments preamble below, from which the Codex and Antigravity readers recover it.
- Antigravity does not validate names and Codex only caps their length, so the writer warns when a name breaks the Agent Skills rule (lowercase letters, digits, single hyphens, 64 characters). A name that is not a safe directory name (`..`, a slash) is an error: names come from frontmatter and become directories.
- A Codex sidecar is never loosened: if a skill from another harness bundles `agents/openai.yaml` with implicit invocation off, the Codex output keeps it off and says so. Other targets keep a user-written sidecar as a bundled file (a project `.agents/skills` is read by Codex too) and skip the one-line sidecar agentport generates, whose meaning already lives in the invocation flags.
- Skill directories are read through symlinks; a symlinked subdirectory or special file inside one is skipped with a warning. Output is never written through a symlink.

<br/>

### Command → skill

- Codex removed custom prompts and Antigravity deprecated workflows, so a command becomes a skill. Its name is its path under the commands directory, lowercased, with `/` replaced by `-` (Claude shows it as `/a:b`). When the derived name differs from the file path, because the path nests or has capitals, the report marks `name` as `transformed`.
- A command keeps its derived name only when nothing else claims it:
  - A skill of the same name in the skills directory beside the commands directory wins, as it does in Claude Code ([spec/claude-code.md](spec/claude-code.md#commands-and-skills-are-one-system)). A skill's name is its `SKILL.md` `name`, or its directory name when `name` is missing or the frontmatter does not parse; claude.ai's `skills/synced/` does not count.
  - Commands whose paths derive the same name (`A/b.md` and `a-b.md`) are all skipped, since none has a better claim to it.
  - A file reached by two paths, through a link or a hard link, is skipped under both: Claude Code loads it once, under whichever path its walk reaches first, and agentport cannot tell which.
  - When the skills directory cannot be read, no command can be checked, so none is converted.
  - `scan` lists a skipped command with its reason, and `convert` refuses it.
- Claude Code ignores `name` and `paths` in a command, so neither is carried over; each produces a `warn`. In a skill, `name` would rename it and `paths` would limit when it loads — effects the command never had.
- Commands are listed the way Claude Code lists them ([spec/claude-code.md](spec/claude-code.md#how-commands-and-agents-are-listed)): symbolic links are followed and keep their own names, each directory is entered once, under the first path in name order, and an unreadable directory or a broken link is skipped. `convert` takes the nearest enclosing commands directory from the location table (`internal/paths`), which also gives the skills directory checked for collisions, and refuses a command that this listing never reaches. A command outside every commands directory needs `--from`; it is named after its file and has nothing to collide with.
- Neither target substitutes `$ARGUMENTS`. The body is left untouched and a marked preamble is prepended, so `adopt` can remove it exactly:

  ```markdown
  <!-- agentport:args:begin -->
  > **Arguments**: invoked as `/commit [scope-hint | recommend-only]`. The text typed after the skill name is the arguments; wherever this file says `$ARGUMENTS`, use that text.
  <!-- agentport:args:end -->
  ```

  A blockquote rather than a heading, so the preamble does not sit above the body's own title. Codex invokes skills as `$name`, so its preamble says so. Skills get the same preamble when their body uses a placeholder. Other syntax from [Body substitution](spec/claude-code.md#body-substitution) that the target does not expand, such as positional and named arguments, `${CLAUDE_*}` variables, `` !`cmd` `` injection and `@path` references, stays as written and produces an `approximated` entry.
- Converted commands are not model-invocable by default in either target — Antigravity's `disable-model-invocation: true`, Codex's sidecar `policy.allow_implicit_invocation: false` — so a mutating workflow never starts from a description match alone; the report marks it `transformed`. `convert --model-invocable` turns it back on, and a config setting does the same for `sync`. Two exceptions: a command with `user-invocable: false` keeps model invocation, since otherwise nothing could start it; and converting to a Claude Code skill keeps the command's original behavior.
- An unquoted bracketed `argument-hint` is valid YAML and parses as a one-element sequence; the reader accepts that shape.

<br/>

### Reading agents

- Claude Code skips an agent its [frontmatter rules](spec/claude-code.md#agent-frontmatter) reject, so agentport refuses such a file rather than converting something the hub never loads.
- A Claude Code `tools` value is read as Claude Code reads it ([spec/claude-code.md](spec/claude-code.md#agent-frontmatter)): omitted or `*` grants every tool, and old names are normalized ([spec/claude-code.md](spec/claude-code.md#tool-names)).
- A specifier such as `Bash(git push *)` still grants the tool. No other harness can narrow a tool, so an Antigravity agent gets it whole, reported `approximated`. `disallowedTools` removes the tools it names from the set; written back to Claude Code, the agent keeps it as written.
- An Antigravity agent that Antigravity would drop is refused: frontmatter that is not strict YAML, or a value the [agent frontmatter table](spec/antigravity.md#agent-frontmatter) marks as dropping the agent. Without `tools` it reads as Antigravity's default set.
- A Codex role without `name`, `description` or `developer_instructions` is refused, since Codex drops it. Every role can read, list and search files and run commands through the shell, edit files, search the web and delegate, so it reads as those capabilities; `[features] shell_tool = false` leaves only editing, web search and delegation ([spec/codex.md](spec/codex.md#tool-names)).
- An agent is found where its harness finds it: in the agents directory from the location table, which [Claude Code](spec/claude-code.md#how-commands-and-agents-are-listed) walks with its command loader, [Codex](spec/codex.md#locations) recursively and [Antigravity](spec/antigravity.md#locations) one level deep.
- `convert` decides the kind from the path: a `.toml` file is a Codex role, and a Markdown file is a command under a commands directory and an agent under an agents directory. `--kind agent` with `--from` reads an agent from anywhere else.
- Agents that share a name are all skipped: the harness loads one of them, and agentport cannot tell which. `scan` lists them with the reason, and `convert` refuses them. One file reached by two paths is one agent, since an agent is named by its `name`, not its path.
- A symlinked Codex role file is read but reported with a `warn`, since Codex fails such a role when it starts.
- The name becomes the output file's name, so one that is not a safe file name (`..`, a slash) is an error.
- Converting an agent to its own harness keeps every field as written; the rules below are for crossing harnesses.

<br/>

### Agent → Antigravity agent

Antigravity reports none of these failures, so the writer checks all of them before writing:

- `name` and `description` are required, and the identity is `name`, not the file name.
- `tools` is a YAML list of names from the runtime-verified table in [spec/antigravity.md](spec/antigravity.md#tool-names). An unknown name makes the subagent fail when it starts.
- Tool names keep the source's order, each capability becoming the Antigravity tools that cover it. A tool with no counterpart is `dropped`.
- A Claude agent with no `tools` may use every tool, while an Antigravity agent with no `tools` gets no shell, search or edit tool. Omitted or `*` therefore converts to the full list.
- `model` must be a tier Antigravity knows ([spec/antigravity.md](spec/antigravity.md#agent-frontmatter)); anything else drops the agent, so an unmapped model is not written: the agent gets the default, `inherit`, and the report marks it `approximated`.
- Claude Code `skills` becomes `preloadSkills`. Reasoning effort is `dropped`: Antigravity has no such setting.
- Fields only the source harness has are `dropped`, except those that shaped the tool set, which are `transformed`: Claude Code `disallowedTools` and Codex `[features] shell_tool`; the other `[features]` switches are `dropped`.
- `mainAgent: false` is added to an agent from another harness, keeping a converted subagent out of the app's agent picker; it is still invocable as a subagent. An Antigravity source keeps its own value.

<br/>

### Agent → Codex role

- Only keys Codex applies to a role are written: `name`, `description`, `developer_instructions` (all required), optionally `model`, `model_reasoning_effort`, `[features]`. One unknown key drops the whole role, and keys such as `sandbox_mode` parse but are not applied, so neither is ever emitted.
- Codex has no per-role tool allowlist, so `tools` is `dropped` unless the agent may use every tool. Codex has no file-read or search tool either: files are read through the shell. Turning the shell off (`[features] shell_tool = false`) would leave a read-only reviewer unable to read anything, so it is used only for an agent that has no read, search, glob, edit, write or shell capability at all. Otherwise the inability to enforce read-only is a `warn` for an agent with neither an edit nor a write tool, and so is the shell the role keeps for an agent that had none.
- A symlinked role file fails at spawn, so roles are always copied.
- A model from another harness is left out, so the role keeps the session's model ([spec/codex.md](spec/codex.md#agent-role-files)), reported `approximated`.
- `effort` carries over as `model_reasoning_effort`, with `max` becoming `xhigh` as Codex's own importer maps it ([spec/codex.md](spec/codex.md#built-in-claude-importer)); an effort given as a number is `dropped`.
- Preloaded skills are `dropped`; a role cannot preload them. Antigravity `mainAgent: false` is `mapped`, since a role only ever runs as a subagent.
- `developer_instructions` is written as a multi-line literal string when the text allows it, so backslashes and quotes stay as written. The written role is parsed back and its strings compared with the source before it is returned.

<br/>

### Agent → Claude Code agent

- A name Claude Code would skip ([spec/claude-code.md](spec/claude-code.md#agent-frontmatter)) is refused.
- Tools are rebuilt from the capabilities with Claude Code's names, in the source's order. An agent that may use every tool gets no `tools` key, which is how Claude Code grants them all. A name without a counterpart, such as Antigravity's `schedule`, is `dropped`.
- Antigravity `preloadSkills` becomes `skills`, and `mainAgent: false` is `mapped`, since every Claude Code agent is a subagent; `true` has no counterpart and is `dropped`.
- A source `model: inherit` is written out, because without a `model` Claude Code tries its subagent default first ([spec/claude-code.md](spec/claude-code.md#agent-frontmatter)).
- A model from another harness has no Claude Code counterpart, so it is left out and the agent gets Claude Code's default subagent model ([spec/claude-code.md](spec/claude-code.md#agent-frontmatter)), reported `approximated`.
- Codex `model_reasoning_effort` carries over as `effort` for the levels Claude Code also has ([spec/claude-code.md](spec/claude-code.md#agent-frontmatter)); any other is `dropped`.
- Fields only the source harness has are `dropped`, except Codex `[features] shell_tool`, which shaped the tool set and is reported `transformed`; the other `[features]` switches are `dropped`.

<br/>

### References inside bodies

- Claude Code tool names distinct enough to find in prose (for example `AskUserQuestion`, `TodoWrite`, `WebFetch`) produce a `warn` when a body leaves Claude Code, naming the target's counterpart or saying it has none. Names that are also plain words, such as `Read` or `Edit`, are not checked.
- `convert` checks the agent names a body mentions against the agents the target already has at the user scope, taking the source harness's user-scope agents as the names to look for. A mentioned agent the target lacks is a `warn` that suggests converting it too.
- `scan` checks tool names but not agent references: it grades portability, not what each harness has installed.
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
