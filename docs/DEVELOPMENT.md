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
make cover-check  # fail below COVER_MIN (cmd/main.go is excluded; it only calls cli.Execute)
make fmt          # go fmt
make vet          # go vet
make ci           # fmt-check, vet, test, cover-check, markers, build
```

<br/>

## Golden files

Converter and CLI output is compared with files under `testdata/`. After an intended output change:

```bash
make golden       # AGENTPORT_UPDATE_GOLDEN=1 go test ./...
git diff testdata # review every changed line before committing
```

The switch is an environment variable rather than a `-update` flag because `go test ./... -update` fails in every package that does not define the flag.

Fixtures are written by hand. Do not copy a real harness configuration into `testdata/`.

<br/>

## Personal-marker check

`make markers` greps every tracked and untracked file for an extended regex of values that must never be published, such as an account name or an internal network range. The pattern itself is never committed:

- locally it is the first line of `${XDG_CONFIG_HOME:-~/.config}/agentport/markers`;
- in CI it is the `AGENTPORT_MARKERS` repository secret.

With neither set the check is skipped, which is the case for pull requests from forks.

<br/>

## Verifying a harness fact

Every row in `docs/spec/` names its evidence. To re-verify Antigravity, run its bundled language server against a scratch config root and read the parsed result back over its local RPC; the exact commands are in the Reproduce section of [spec/antigravity.md](spec/antigravity.md). Codex facts are checked against the source at the pinned tag, Claude Code facts against its docs and binary.

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
