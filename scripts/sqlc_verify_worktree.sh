#!/usr/bin/env bash
set -euo pipefail

generated_dirs=(
  "internal/store/sqlc"
  "cmd/mcp-orch/store/sqlc"
)

root="$(git rev-parse --show-toplevel)"
cd "$root"

tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/super-dolphin-sqlc-worktree.XXXXXX")"
trap 'rm -rf "$tmpdir"' EXIT

before="$tmpdir/before"
mkdir -p "$before"

snapshot_generated_dirs() {
  local dir
  for dir in "${generated_dirs[@]}"; do
    if [[ ! -d "$dir" ]]; then
      echo "ERROR: missing generated SQLC directory before verification: $dir" >&2
      exit 1
    fi
    mkdir -p "$before/$(dirname "$dir")"
    cp -a "$dir" "$before/$dir"
  done
}

compare_generated_dirs() {
  local changed=0
  local dir
  local diff_file

  for dir in "${generated_dirs[@]}"; do
    diff_file="$tmpdir/$(printf '%s' "$dir" | tr '/' '_').diff"
    if ! diff -ruN "$before/$dir" "$dir" >"$diff_file"; then
      changed=1
      echo "ERROR: generated SQLC directory changed after make sqlc-generate: $dir" >&2
      cat "$diff_file" >&2
    fi
  done

  if [[ "$changed" -ne 0 ]]; then
    echo "ERROR: sqlc-verify-worktree requires generated output to be stable before/after regeneration." >&2
    echo "Diagnostic git status for generated directories:" >&2
    git status --porcelain --untracked-files=all -- "${generated_dirs[@]}" >&2 || true
    exit 1
  fi
}

snapshot_generated_dirs
make sqlc-generate
compare_generated_dirs

echo "sqlc-verify-worktree: generated output is stable before/after regeneration"
