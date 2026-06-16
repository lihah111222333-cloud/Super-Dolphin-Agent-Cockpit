#!/usr/bin/env bash
set -euo pipefail

root="${1:-.}"
cd "$root"

run_generate=false
generate_cmd=()

if [ -n "${GO_GUARD_GENERATE_CMD:-}" ]; then
  run_generate=true
  generate_cmd=(bash -lc "$GO_GUARD_GENERATE_CMD")
elif [ -f Makefile ] && grep -Eq '^[[:alnum:]_.-]*generate[[:space:]]*:' Makefile; then
  run_generate=true
  generate_cmd=(make generate)
fi

if [ "$run_generate" != true ]; then
  echo "SKIP: no generator command found"
  exit 0
fi

if ! command -v git >/dev/null 2>&1 || ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  echo "WARN: not in a git worktree; cannot verify generated drift" >&2
  "${generate_cmd[@]}"
  exit 0
fi

before="$(mktemp)"
after="$(mktemp)"
trap 'rm -f "$before" "$after"' EXIT

git status --short > "$before"
"${generate_cmd[@]}"
git status --short > "$after"

if ! cmp -s "$before" "$after"; then
  echo "Generated artifact drift detected after: ${generate_cmd[*]}" >&2
  diff -u "$before" "$after" || true
  exit 1
fi

echo "Generated artifact drift check passed."
