#!/bin/sh
# UZ-C-004: an agent is told to ask the graph, not list spec/.
set -eu
root=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
cd "$root"
readme=README.md
if ! grep -q 'docdag query --binding' "$readme"; then
  echo "$readme: missing docdag query --binding" >&2
  exit 1
fi
if ! grep -q 'docdag context' "$readme"; then
  echo "$readme: missing docdag context" >&2
  exit 1
fi
