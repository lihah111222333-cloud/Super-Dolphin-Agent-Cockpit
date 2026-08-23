#!/usr/bin/env bash
set -euo pipefail

if [[ -n "${MAKEFLAGS:-}" || -n "${MAKEOVERRIDES:-}" ]]; then
  echo "ERROR: inherited MAKEFLAGS/MAKEOVERRIDES are forbidden for SQLC diagnostics" >&2
  exit 1
fi

resolve_trusted_git() {
  local candidate
  for candidate in /usr/bin/git /usr/local/bin/git /opt/homebrew/bin/git; do
    if [[ -f "$candidate" && -x "$candidate" ]]; then
      (cd "$(dirname "$candidate")" && printf '%s/%s\n' "$(pwd -P)" "$(basename "$candidate")")
      return
    fi
  done
  echo "ERROR: trusted external Git is unavailable" >&2
  exit 1
}

trusted_git() {
  env -i GIT_NO_REPLACE_OBJECTS=1 GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null GIT_TERMINAL_PROMPT=0 GIT_ASKPASS=/bin/false GIT_OPTIONAL_LOCKS=0 LC_ALL=C "$git_binary" --no-replace-objects "$@"
}

diagnostic_start="$(pwd -P)"
git_binary="$(resolve_trusted_git)"
root="$(trusted_git -C "$diagnostic_start" rev-parse --show-toplevel)"
root="$(cd "$root" && pwd -P)"
cd "$root"
diagnostic_start="$root"
resolve_external_tool() {
  local name="$1"
  local binary
  binary="$(command -v "$name")" || {
    echo "ERROR: required local diagnostic tool is missing: $name" >&2
    exit 1
  }

  binary="$(cd "$(dirname "$binary")" && printf '%s/%s\n' "$(pwd -P)" "$(basename "$binary")")"
  if [[ "$binary" != /* || "$binary" == "$diagnostic_start/"* ]]; then
    echo "ERROR: candidate-local or non-absolute $name is forbidden for SQLC diagnostics: $binary" >&2
    exit 1
  fi
  printf '%s\n' "$binary"
}

go_binary="$(resolve_external_tool go)"
make_binary="$(resolve_external_tool make)"

GOWORK=off "$go_binary" run ./scripts/sqlc_config_outputs --git-binary "$git_binary" --preflight >/dev/null
configured_outputs="$(GOWORK=off "$go_binary" run ./scripts/sqlc_config_outputs --git-binary "$git_binary")"
generated_dirs=()
while IFS= read -r dir; do
  if [[ -n "$dir" ]]; then
    generated_dirs+=("$dir")
  fi
done <<<"$configured_outputs"
if [[ "${#generated_dirs[@]}" -eq 0 ]]; then
  echo "ERROR: SQLC config output set is empty" >&2
  exit 1
fi

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
    if ! diff -ruN -- "$before/$dir" "$dir" >"$diff_file"; then
      changed=1
      echo "ERROR: generated SQLC directory changed after make sqlc-generate-worktree: $dir" >&2
      cat "$diff_file" >&2
    fi
  done

  if [[ "$changed" -ne 0 ]]; then
    echo "ERROR: sqlc-verify-worktree requires generated output to be stable before/after regeneration." >&2
    echo "Diagnostic git status for generated directories:" >&2
    trusted_git -C "$root" status --porcelain --untracked-files=all -- "${generated_dirs[@]}" >&2
    exit 1
  fi
}

snapshot_generated_dirs
MAKEFLAGS= MAKEOVERRIDES= "$make_binary" sqlc-generate-worktree
GOWORK=off "$go_binary" run ./scripts/sqlc_config_outputs --git-binary "$git_binary" --verify-shape
compare_generated_dirs

echo "sqlc-verify-worktree: generated output is stable before/after regeneration"
