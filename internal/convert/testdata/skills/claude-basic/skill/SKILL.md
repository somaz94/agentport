---
name: demo-skill
description: 'Scan a path: report findings. Use when the user asks to scan.'
argument-hint: '[path | --all]'
allowed-tools: Read, Grep, Bash
when_to_use: 'Before publishing a repository.'
license: Apache-2.0
metadata:
  short-description: Scan for findings
---
# Demo skill

Scan $ARGUMENTS and report each finding.

Run [the helper](scripts/run.sh) for the heavy lifting.
