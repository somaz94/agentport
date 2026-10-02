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

## Usage

```bash
agentport map                 # what each harness calls a customization, and where it lives
agentport scan                # what is installed for each harness, and how portable each skill and command is
agentport convert ~/.claude/skills/my-skill --to antigravity            # preview + loss report
agentport convert ~/.claude/skills/my-skill --to codex --out ./out      # write the converted skill
agentport convert ~/.claude/skills/my-skill --to codex --strict         # exit 2 if anything is lost
agentport convert ~/.claude/commands/my-command.md --to codex           # a command becomes a skill
```

Every command takes `-o json`.

`agentport --help` lists the commands available in the build you have.

<br/>

## Build

```bash
make build    # ./bin/agentport
make ci       # every check CI runs
```

See [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md).
