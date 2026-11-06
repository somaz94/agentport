---
name: release-notes
description: Draft release notes from the commits since the last tag.
argument-hint: [version]
allowed-tools: Bash(git log:*), Bash(git describe:*)
model: haiku
---
Draft the release notes for $ARGUMENTS.

Commits since the last tag:
!`git log --oneline $(git describe --tags --abbrev=0)..HEAD`

Group them by Conventional Commit type and skip chore commits.
