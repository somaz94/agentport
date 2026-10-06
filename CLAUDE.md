# CLAUDE.md — agentport

agentport translates Claude Code skills, commands and agents into Codex and Antigravity formats, reports field by field what each conversion kept and lost, and never changes or deletes a file it does not track. Design: [docs/design.md](docs/design.md). Harness facts: [docs/spec/](docs/spec/).

<br/>

## Build & Test

```bash
make build        # ./bin/agentport
make test         # go test ./... -v -race -cover
make cover-check  # coverage gate over cmd/cli and internal
make golden       # rewrite golden files from current output, then review the diff
make ci           # everything CI runs: tidy-check, fmt-check, vet, test, cover-check, build, then runs the binary
make e2e-antigravity  # load converted fixtures with the Antigravity app's own loader (macOS, app installed)
make e2e-codex        # load converted fixtures with the Codex CLI's app-server (codex on PATH)
```

<br/>

## Rules

- **Harness facts come from `docs/spec/`, not memory.** Each spec file pins the harness version it was verified against and marks every row with its evidence. When code needs a fact the spec lacks, verify it against that version first and add the row; do not mix rows from two versions in one file.
- **Locations come from `internal/paths` only.** No other package hard-codes a harness directory.
- **Writers emit strict YAML; readers accept what the harness accepts.** Claude Code and Codex repair invalid frontmatter before parsing, Antigravity silently drops an agent whose frontmatter does not parse. `internal/frontmatter` does both halves; do not hand-roll YAML elsewhere.
- **`sync` changes or deletes only files its manifest records**, and without `--force` only while they are still as written. A file agentport did not write enters the manifest only when it already holds exactly agentport's output. Every write and deletion goes through `internal/reconcile` and an `os.Root`. Keep `TestSecondSyncChangesNothing` and `TestUnmanagedFilesAreNeverTouched` passing.
- **Antigravity fails silently.** A missing `name` / `description`, a comma-string `tools`, an unknown `model` or an unknown tool name each drops or breaks the agent without an error. Every writer validates before it writes. Run `make e2e-antigravity` after changing an Antigravity writer.
- **Codex drops a role it cannot parse with only a startup warning**, an unknown key included. Run `make e2e-codex` after changing a Codex writer.
- **Fixtures are synthetic.** Never copy a real `~/.claude`, `~/.codex` or `~/.gemini` file into `testdata/`; personal paths and account names leak through fixtures more than through any other file.
- **Golden files are reviewed, not regenerated blindly.** `make golden` rewrites them; read the diff before committing.
- **Releases run on a `vX.Y.Z` tag push:** `release.yml` runs GoReleaser (`.goreleaser.yml`), and `changelog-generator.yml` commits `CHANGELOG.md`. Ask before changing any of these files or `.github/release.yml`.
- Coverage stays at or above the `COVER_MIN` in the Makefile. Comments are English and explain why, not what.
