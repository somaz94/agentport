#!/usr/bin/env bash
# Fails when a personal marker (account name, internal domain, LAN range) reaches the repository.
# The regex is read from outside the repo because committing it would publish the markers it guards.
set -euo pipefail

cd "$(dirname "$0")/.."

pattern="${AGENTPORT_MARKERS:-}"
config="${XDG_CONFIG_HOME:-$HOME/.config}/agentport/markers"
if [ -z "$pattern" ] && [ -f "$config" ]; then
  pattern="$(head -n 1 "$config")"
fi
if [ -z "$pattern" ]; then
  echo "markers: no pattern configured, skipping (set AGENTPORT_MARKERS or $config)"
  exit 0
fi

if git grep --untracked -n -I -E -e "$pattern" -- .; then
  echo "markers: personal values found above; replace them with documentation values" >&2
  exit 1
fi
echo "markers: clean"
