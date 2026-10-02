# Harness format specs

Field-level reference for each harness agentport reads or writes. Each file pins the version it was verified against in its first line, and marks every row with its evidence.

| File | Harness | Verified by |
|---|---|---|
| [claude-code.md](claude-code.md) | Claude Code (the hub) | Docs plus the native binary's parser literals |
| [antigravity.md](antigravity.md) | Antigravity desktop app | Fixtures loaded by the app's own language server, read back over its local RPC; tool names also checked in real conversations |
| [codex.md](codex.md) | Codex CLI | Source at a pinned release tag |

When a harness ships a new version, re-verify against it and update the pin in that file's first line; do not mix rows from two versions in one file.
