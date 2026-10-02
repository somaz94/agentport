---
description: 'Deploy the current branch: build, push, verify. Use when the user asks to ship.'
argument-hint: '<env> [--dry-run]'
allowed-tools: Bash(make deploy *), Read
---
# Deploy

Deploy the current branch to $ARGUMENTS.

1. Build the image.
2. Push it and wait for the rollout.
