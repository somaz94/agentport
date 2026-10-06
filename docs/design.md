# Design

agentport keeps Claude Code as the hub and translates its customizations into Codex and Antigravity. This document records the decisions and the reasons for them. Facts about each harness's format, with their evidence, are in [spec/](spec/).

<br/>

## Hub model

- `~/.claude/` (and a project's `.claude/`) is the single source of truth. Targets are generated.
- A change made in a target can come back through [`adopt`](#adopt), which converts it back and merges it into the hub file while keeping hub-only fields such as `allowed-tools`. A full bidirectional three-way merge is out of scope.
- Paired translation mirrors (for example an `agents-ko/` beside `agents/`) are converted the same way into the matching target directory, where no harness loads them.

Existing tools were considered first. rulesync supports all three harnesses but moves the source into its own neutral directory, drops unknown fields with at most a warning, copies `tools` and hook matchers without translating them, and still generates targets the harnesses have removed. The built-in importers are one-way and one-shot, and Codex's skips any command that uses `$ARGUMENTS`. What agentport adds is the loss report as the product, semantic translation, the Claude directory as the hub, and ownership tracking.

<br/>

## Commands

| Command | Purpose |
|---|---|
| `map [kind]` | The crosswalk: what each harness calls a concept and where it lives |
| `scan` | What exists at each harness location, with a portability grade per item |
| `convert <path> --to <harness>` | Convert one item in any direction; preview by default |
| `sync [--to <harnesses>]` | Convert the whole hub into each target; dry run by default, `--apply` writes, `--check` writes nothing and exits 3 when a target is out of sync, `--strict` exits 2 on any loss and writes nothing (see [Sync](#sync)) |
| `status` | Classify every unit in each target, unmanaged ones included |
| `adopt <target-path>` | Bring an edit made in a target back into the hub (see [Adopt](#adopt)) |
| `doctor` | Installed versions against the verified ones, locations, deprecated locations in use, manifest health (see [Doctor](#doctor)) |
| `link skills --to <harness>` (planned) | Use hub skills in place without copying (see [Link mode](#link-mode)) |

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
- Neither target substitutes arguments. When the body uses an argument placeholder (`$ARGUMENTS`, `$ARGUMENTS[N]`, `$N`) or a named argument declared in `arguments:`, the body is left untouched and a marked preamble is prepended, so `adopt` can remove it exactly:

  ```markdown
  <!-- agentport:args:begin -->
  > **Arguments**: invoked as `/commit [scope-hint | recommend-only]`. The text typed after the skill name is the arguments; wherever this file says `$ARGUMENTS`, use that text.
  <!-- agentport:args:end -->
  ```

  A blockquote rather than a heading, so the preamble does not sit above the body's own title. Codex invokes skills as `$name`, so its preamble says so. A skill gets the same preamble on the same condition. Other syntax from [Body substitution](spec/claude-code.md#body-substitution) that the target does not expand, such as positional and named arguments, `${CLAUDE_*}` variables, `` !`cmd` `` injection and `@path` references, stays as written and produces an `approximated` entry.
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
- Paths under `~/.claude/` are not rewritten; on the same machine they still resolve. Antigravity asks before a subagent reads a file outside the conversation's workspace ([spec/antigravity.md](spec/antigravity.md#tool-names)), so there such a path waits for the user's approval and fails when nobody gives it.

<br/>

### Instructions, hooks, MCP

- **Instructions** are not generated. A hand-condensed `AGENTS.md` is the source for non-Claude harnesses; agentport checks its size against each harness's budget, warns when a `CLAUDE.md` section changed after `AGENTS.md` was last updated, and keeps one file per directory where Antigravity would otherwise load both `AGENTS.md` and `GEMINI.md`.
- **Hooks** map almost one to one into Codex, which reports its shell tool as `Bash` and lets the `Edit` / `Write` matchers select `apply_patch`. `apply_patch` carries no file path, so post-edit hooks need a shim that parses the patch. A converted Codex hook runs only after the user trusts it, which `sync` points out. Antigravity's events and payloads differ enough to need a normalizing shim.
- **MCP** converts between JSON and TOML when a server list exists.

<br/>

## Sync

`sync` converts every skill, command and agent in the hub into each target harness at the user scope. The project scope is not synced: Codex and Antigravity both read a project's `.agents/skills`, so their outputs would collide there.

- Dry run by default; `--apply` writes. Without `--to` or configured targets, every harness whose configuration directory (from the location table) exists is a target. A target named but not set up is refused rather than created; so is a target directory that is a link to nothing, or that resolves to the same place as a hub directory or another target's, through a link in either direction.
- Each target keeps `.agentport/manifest.json` in its configuration directory. For every file it tracks it records the hub item the file came from and fingerprints of both, the output's content and mode included (`manifest.Entry` in `internal/manifest`). Paths are relative to the home directory, because one harness can keep skills and agents in different trees.
- Every target file is classified by comparing what the manifest recorded, what the hub converts to now, and what is on disk, content and mode together:
  - `new`, `update` (what the hub converts to changed, the file did not) and `unchanged`.
  - `orphan`: its source is gone and the file is as written, so it is deleted.
  - `drift`: edited since it was written, so it is kept and reported until `adopt` or `--force`.
  - `conflict`: a file the manifest does not record is in the way, and is never touched.
  - `unmanaged`: anything else in the target's directories, never touched.

  A file that already holds exactly what the conversion produces is recorded, whoever wrote it, and is agentport's from then on: updated, and deleted as an orphan, like any file it wrote.
- A unit (a skill directory or an agent file) is a conflict as a whole, so nothing of it is written:
  - when its `SKILL.md` or agent file is someone else's, which keeps a hand-ported skill whole, bundled files included;
  - when it is a symbolic link, since agentport never writes through one;
  - when another unit in the target, not written by agentport, has the same name, since the target identifies skills and agents by name and loads only one;
  - when its path differs from another unit's only in letter case, which the default macOS and Windows file systems treat as one path; neither unit is written.
- Hub items are listed and checked as the hub harness loads them (see [Command → skill](#command--skill) and [Reading agents](#reading-agents)). An item reported `skipped` (a name collision, or a target that already reads it through a link) or `error` (unreadable, or failed to convert) keeps its earlier output as it is, together with anything else recorded at its target path. So does every item under a hub directory that could not be listed in full or is a link to nothing, since an item missing from it may only be unreadable; while a skill cannot be read, no command beside it is converted, since the skill's name is unknown.
- A `skip` pattern leaves an item out, and its earlier output becomes an orphan. It does not change which item wins a name.
- `--force` replaces drifted files. A drifted file whose source is gone is not deleted; agentport stops tracking it.
- Writes come before deletions, and both go through an `os.Root` opened on the target directory, so neither follows a link out of it; one whose path resolves into the hub is refused, whatever the plan said. The manifest records what was written even when a later file fails. A directory is removed only when deleting its orphans left it empty.
- Two runs on the same input produce no change the second time, and a file the manifest does not record is never changed or deleted. Tests enforce both.

<br/>

## Adopt

`adopt <target-path>` brings an edit made in a target back into the hub item it was converted from. The path is a skill directory, a file in one, or an agent file; the manifest names the hub item.

- The hub must still convert to what the manifest recorded writing. Otherwise the conversion changed since (the hub, the settings or agentport itself), and adopting the target as it is would undo that change. The manifest keeps hashes, not the earlier content, so there is no base for a three-way merge. `--force` adopts anyway, unless the hub item was renamed, and the preview shows what it undoes.
- What is adopted is the difference between the edited unit and what the hub converts to now, both read with the target's reader:
  - `description` and the body, without the arguments preamble, and the `argument-hint` recovered from it.
  - For a skill, the invocation flags and the portable keys (`license`, `compatibility`, `metadata`). Whether the model may start a converted command is a sync setting, not part of the command, so it is not adopted.
  - For an agent, the capabilities removed from or added to the tools Antigravity lists, applied to the hub's `tools` with every other entry, specifiers included, kept as written; an agent that had every tool gets the removed ones in `disallowedTools`. Codex `model_reasoning_effort` becomes `effort`, and Antigravity `preloadSkills` becomes `skills`.
  - Bundled files, when the hub item is a skill, copied back byte for byte with their modes, except below a link the hub reader does not follow. A file added beside a converted command or agent is listed, not adopted, and a file deleted in the target is reported, not deleted from the hub.
- What the hub cannot hold is left out and listed under `not adopted` in the preview: for example a rename, a model from another harness, or a key only the target has.
- A change to the body alone keeps the hub file's frontmatter byte for byte; any other change rewrites the frontmatter as strict YAML.
- Preview by default, as a diff of each hub file; `--apply` writes. The hub's new conversion then becomes what the manifest compares the unit with. A unit that still differs from it, because the hub could not hold an edit or only formatting differs, stays `drift` until `sync --force` rewrites it.

<br/>

## Doctor

`doctor` reports, at the user or project scope:

- The settings file: whether it loads.
- Each harness's installed version, read from `claude --version` and `codex --version` on `PATH` and from the Antigravity app bundle on macOS, against the version its facts in [spec/](spec/) were verified on.
- Where each harness keeps skills, commands and agents, and whether it is set up.
- Locations a harness deprecated or removed that still hold files, from the location table.
- At the user scope, each target manifest: whether it loads, and recorded files that are missing.
- At the user scope, an Antigravity configuration directory holding both `AGENTS.md` and `GEMINI.md`, which Antigravity loads both of.

A check that fails, such as a settings file or manifest that does not parse, exits 1; a warning does not.

<br/>

## Configuration

`$XDG_CONFIG_HOME/agentport/config.yaml` (`~/.config/agentport/config.yaml` when the variable is unset), or the file `--config` names. Every key is optional, and an unknown key is an error:

```yaml
hub: claude                      # must be claude
targets: [antigravity, codex]    # what sync and status use without --to; default: every harness set up
pairs: ['-ko']                   # translation mirrors: skills-ko/, commands-ko/, agents-ko/ beside the hub's directories
skip: ['commands/internal', 'agents/*-draft.md']   # hub items left out, relative to the hub directory
modelInvocableCommands: false    # let the model start converted commands, as Claude Code does
```

`targets`, `pairs` and `skip` above are examples; `hub` and `modelInvocableCommands` show the defaults.

- A pair directory is converted the same way into the matching directory of each target (`skills-ko/`, `agents-ko/`), which no harness loads. Removing a pair makes its earlier output orphans.
- `skip` patterns use Go's `path.Match` syntax, where `*` does not cross `/`; a pattern that matches a directory skips everything below it.
- `modelInvocableCommands` is what `convert --model-invocable` sets for one conversion. `sync` takes no flag for it, so `adopt` converts with the setting `sync` used.

Syncing the project scope will need repositories to exclude, path rewriting in bodies and a model map; none of them is a key yet.

<br/>

## Link mode

For trying hub skills without converting them:

- Antigravity: a `skills.json` entry pointing at the hub skills directory. The path must be absolute; the pinned version rejects `~/`.
- Codex: a symlink per skill under `~/.agents/skills/`; Codex follows directory symlinks.

Nothing is translated in this mode, so `$ARGUMENTS` stays literal and Claude-only fields are ignored, and it cannot carry commands or agents. `sync` remains the default.

Serving skills and commands from one MCP server to all three harnesses was rejected: prompt support differs per client, the native `/` menu and progressive disclosure are lost, and agents cannot be served that way.
