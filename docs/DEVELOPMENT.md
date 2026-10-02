# Development

<br/>

## Prerequisites

- Go at the version in `go.mod`
- Make

<br/>

## Build and test

```bash
make build        # ./bin/agentport
make test         # go test ./... -v -race -cover
make cover        # coverage report
make cover-check  # fail below COVER_MIN (cmd/main.go is excluded; it only calls into cmd/cli)
make fmt          # go fmt
make vet          # go vet
make ci           # tidy-check, fmt-check, vet, test, cover-check, build, then runs the binary
```

<br/>

## Golden files

Converter and CLI output is compared with files under `testdata/`. After an intended output change:

```bash
make golden                                # AGENTPORT_UPDATE_GOLDEN=1 go test ./...
git status --short -uall -- '*/testdata/*' # new golden files are untracked and show only here
git diff -- '*/testdata/*'                 # review every changed line before committing
```

The switch is an environment variable rather than a `-update` flag because `go test ./... -update` fails in every package that does not define the flag.

Fixtures are written by hand. Do not copy a real harness configuration into `testdata/`.

<br/>

## Verifying a harness fact

Every row in `docs/spec/` names its evidence. To re-verify Antigravity, run its bundled language server against a scratch config root and read the parsed result back over its local RPC; the exact commands are in the Reproduce section of [spec/antigravity.md](spec/antigravity.md#reproduce). Codex facts are checked against the source at the pinned tag, Claude Code facts against its docs and binary. An installed Claude Code updates itself off the pinned version, so fetch the pinned build instead: `npm pack @anthropic-ai/claude-code-darwin-arm64@<version>` (one package per platform) unpacks to the native binary, which `strings` reads.

`make e2e-antigravity` does this for the converter: it writes every skill, command and agent fixture into a scratch root (skills and commands as Antigravity skills, agents as Antigravity agents), starts the desktop app's language server against it, and checks that the loader parsed each one as written. It needs the Antigravity desktop app (macOS path by default; override `AG_LANGUAGE_SERVER`) and is skipped by `make test` and CI.

<br/>

## Workflow

```bash
make branch name=feature-name        # feature branch from main
make pr title="feat: add feature"    # test, push, open a PR with a generated body
```

<br/>

## Conventions

- Commits: Conventional Commits (`feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `ci:`, `chore:`).
- CI skips pushes that only touch Markdown or workflow files (`paths-ignore`).
