# Changelog

All notable changes to this project will be documented in this file.

## [v0.1.1](https://github.com/somaz94/agentport/compare/v0.1.0...v0.1.1) (2026-10-06)

### Bug Fixes

- stop args Strip deleting text and Hint truncating hints ([37d17bc](https://github.com/somaz94/agentport/commit/37d17bc0c539cca77db065f02236e41afb1ac2a0))

### Code Refactoring

- share the args unescaped-dollar prefix and fuzz Hint ([8f151db](https://github.com/somaz94/agentport/commit/8f151db87aa6c5e531b2ba2d005173d24176cf0e))
- simplify args Strip and Hint, stop Named panicking ([dda040e](https://github.com/somaz94/agentport/commit/dda040e201fd56d16308e2616426a9eec6ca286f))

### Contributors

- somaz

<br/>

## [v0.1.0](https://github.com/somaz94/agentport/releases/tag/v0.1.0) (2026-10-06)

### Features

- install with go install as agentport, with version from build info ([ce9d6fb](https://github.com/somaz94/agentport/commit/ce9d6fb7195c1d92e1b0558caf22edee12987703))
- sync the hub into each target, with status, adopt and doctor ([4ed4d20](https://github.com/somaz94/agentport/commit/4ed4d207cb4c712e8ef4762a069a3af35a69c11e))
- convert agents between Claude Code, Codex and Antigravity ([0de3278](https://github.com/somaz94/agentport/commit/0de3278019420f13cfcba8e4f518168d790c8a01))
- convert Claude Code commands to Codex and Antigravity skills ([b2829e9](https://github.com/somaz94/agentport/commit/b2829e913bc3fac6df2b3d137c862244a3af885b))
- convert skills between Claude Code, Codex and Antigravity, with convert and scan commands ([13ebb7c](https://github.com/somaz94/agentport/commit/13ebb7c0b19f49814b03a0ef3f4a05863d234fc2))
- scaffold the CLI with the IR, frontmatter, path table, loss and manifest types, and the map command ([6078b95](https://github.com/somaz94/agentport/commit/6078b957d0770a16958a64d0959a13f9900a3b88))

### Documentation

- document quick start, sync scope, exit codes and arg preamble ([c29c48b](https://github.com/somaz94/agentport/commit/c29c48bec000e15400163c3a5fc48143a39865df))
- add pinned harness format specs for Claude Code, Antigravity and Codex ([b3cef9f](https://github.com/somaz94/agentport/commit/b3cef9f6e948ef540d8f278b72dc75a4ef87a93e))

### Tests

- verify converted fixtures load in the Codex CLI app-server ([71e03e7](https://github.com/somaz94/agentport/commit/71e03e72e65577e387021b55d704fa7fa260402b))

### Continuous Integration

- release with GoReleaser and generate CHANGELOG.md on tag push ([d0afa6a](https://github.com/somaz94/agentport/commit/d0afa6aed03cd8df475c73590b68a6a2e5cfd88b))
- drop the personal-marker check; a maintainer's leak guard belongs in local tooling, not in the project ([463d832](https://github.com/somaz94/agentport/commit/463d8329fed4301b1b8593f08412dcfc97fee2c7))

### Chores

- license agentport under Apache-2.0 ([46f9471](https://github.com/somaz94/agentport/commit/46f94714d541d5b331177c5d8255506278ba35f1))
- align make ci with CI and document the pinned binary check ([612d1bb](https://github.com/somaz94/agentport/commit/612d1bb55895f48dd1ba96705f49a0535db2ed93))

### Contributors

- somaz

<br/>

