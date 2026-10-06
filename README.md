# agentport

[![CI](https://github.com/somaz94/agentport/actions/workflows/ci.yml/badge.svg)](https://github.com/somaz94/agentport/actions/workflows/ci.yml)

Keep your Claude Code skills, slash commands and subagents as the source of truth, and get working copies in **Codex** and **Antigravity** — translated, not copied.

> Early development. The design is in [docs/design.md](docs/design.md); the verified format of each harness is in [docs/spec/](docs/spec/).

<br/>

## Why

The three harnesses look alike and differ in ways that fail quietly:

- A Claude command uses `$ARGUMENTS`; Codex and Antigravity substitute nothing, and Codex's own importer skips every command that contains it.
- A Claude agent without `tools` may use every tool; an Antigravity agent without `tools` gets no shell, search or edit tool.
- Antigravity drops an agent whose `tools` is a comma string or whose `model` is not one of its tiers, and an unknown tool name fails the subagent when it starts. None of it is reported.
- Codex drops a whole agent role over one unknown key, has no per-agent tool allowlist, and reads files through the shell.

agentport converts with these rules, reports per field what was **mapped**, **transformed**, **approximated**, **dropped** or needs a **warning**, and tracks every file it writes so a re-run updates only its own output.

<br/>

## Install

```bash
# Homebrew (macOS, Linux)
brew install somaz94/tap/agentport

# Release binary, into /usr/local/bin
curl -sSL https://raw.githubusercontent.com/somaz94/agentport/main/scripts/install.sh | bash
# ... or into an existing directory you own
curl -sSL https://raw.githubusercontent.com/somaz94/agentport/main/scripts/install.sh | INSTALL_DIR="$HOME/.local/bin" bash

# From source
go install github.com/somaz94/agentport/cmd/agentport@latest
```

<br/>

## Quick start

```bash
agentport doctor            # what is installed, and where each harness keeps its files
agentport sync              # preview what would be written into each target
agentport sync --apply      # write it
agentport status            # later: what changed in the hub, or was edited in a target
```

No settings file is needed. Without one, `sync` writes into every harness that is set up, meaning its configuration directory exists; `agentport doctor` shows which are. If none is set up, `sync` stops and says so; run the harness once so that it creates its configuration directory, since agentport never creates one. To choose the targets, convert translation mirrors such as `skills-ko/`, or leave items out, see [Configuration](docs/design.md#configuration).

<br/>

## Usage

```bash
agentport map                 # what each harness calls a customization, and where it lives
agentport scan                # what is installed for each harness, and how portable each skill, command and agent is
agentport convert ~/.claude/skills/my-skill --to antigravity            # preview + loss report
agentport convert ~/.claude/skills/my-skill --to codex --out ./out      # write the converted skill
agentport convert ~/.claude/skills/my-skill --to codex --strict         # exit 2 if anything is lost
agentport convert ~/.claude/commands/my-command.md --to codex           # a command becomes a skill
agentport convert ~/.claude/agents/my-agent.md --to antigravity         # an agent stays an agent
agentport sync                # preview converting the whole hub into every target
agentport sync --apply        # write it: files someone edited, and files agentport does not track, are left alone
agentport sync --check        # write nothing; exit 3 when anything would change or needs attention
agentport status              # classify every unit in each target against the hub and the manifest
agentport adopt ~/.gemini/config/skills/my-skill   # bring an edit made in a target back into the hub
agentport doctor              # installed versions, locations, deprecated paths, manifests
```

Every command takes `-o json`. Settings such as the target harnesses and translation mirrors like `skills-ko/` come from a config file; see [Configuration](docs/design.md#configuration).

`agentport --help` lists the commands available in the build you have, and `agentport <command> --help` their flags.

<br/>

## What sync touches

- Only the user scope is synced: `~/.claude`, the hub, into each target's user-level directories. A project's `.claude/` is not.
- It writes skills and agents where each target loads them, and records every file it writes in `.agentport/manifest.json` inside that target's configuration directory; `agentport doctor` prints each manifest's path.
- A file it did not write is never changed or deleted, unless it already holds exactly what agentport would write: it is then recorded as agentport's. One that differs and sits where agentport would write is reported as `conflict`. Move or delete it, or leave the hub item out with `skip` (see [Configuration](docs/design.md#configuration)), then sync again.
- A file edited in the target since agentport wrote it is reported as `drift` and left alone. `agentport adopt <path>` carries the edit back into the hub; `agentport sync --apply --force` overwrites it.
- When a hub item is removed, its output is deleted on the next `--apply`, unless it was edited: it is then reported as `drift` until `adopt`, or `--force` stops tracking it and leaves it in place.

<br/>

## What changes in translation

- A command becomes a skill that starts only when invoked by name: neither Codex nor Antigravity lets the model start it from its description, unless the command sets `user-invocable: false`. Set `modelInvocableCommands: true` in the settings file, or pass `convert --model-invocable`, to allow it as Claude Code does.
- Neither target substitutes arguments. A converted command or skill whose body uses an argument placeholder (`$ARGUMENTS`, `$0`, `$1`, …) or a named argument starts with a short marked block that says what the arguments are; `adopt` removes it again.
- An agent converted for Antigravity is a subagent only (`mainAgent: false`): other agents can start it, but it stays out of the app's agent picker. Custom agents are an Antigravity app and CLI feature; the IDE build was not tested.
- Every conversion reports, field by field, what was mapped, transformed, approximated, dropped or warned about. `convert` prints the report for one item, and `scan` sums it up for everything installed.

<br/>

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | An error, or a failed `doctor` check |
| 2 | `--strict` was given and a conversion lost something; nothing was written |
| 3 | `sync --check` found something to write, a file edited in a target, or a conflict |

<br/>

## Versions

Each harness's format was verified against one pinned release, recorded at the top of its file in [docs/spec/](docs/spec/). `agentport doctor` compares the installed versions with those and warns when they differ.

<br/>

## Build

```bash
make build    # ./bin/agentport
make install  # go install into $GOBIN, or $GOPATH/bin when unset
make ci       # every check CI runs
```

See [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md).
