#!/usr/bin/env bash
set -euo pipefail

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
gofmt_binary="$(resolve_external_tool gofmt)"
find_binary="$(resolve_external_tool find)"
rg_binary="$(resolve_external_tool rg)"

GOWORK=off "$go_binary" run ./scripts/sqlc_config_outputs --git-binary "$git_binary" --preflight >/dev/null
configured_outputs="$(GOWORK=off "$go_binary" run ./scripts/sqlc_config_outputs --git-binary "$git_binary")"
manual_extensions="$(GOWORK=off "$go_binary" run ./scripts/sqlc_config_outputs --git-binary "$git_binary" --manual-extensions)"
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

is_manual_extension() {
  local candidate="$1"
  local registered
  while IFS= read -r registered; do
    if [[ -n "$registered" && "$candidate" == "$registered" ]]; then
      return 0
    fi
  done <<<"$manual_extensions"
  return 1
}

tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/super-dolphin-sqlc-postprocess.XXXXXX")"
trap 'rm -rf "$tmpdir"' EXIT

index=0
for dir in "${generated_dirs[@]}"; do
  if [[ ! -d "$dir" ]]; then
    echo "ERROR: missing generated SQLC directory for post-processing: $dir" >&2
    exit 1
  fi
  file_list="$tmpdir/find-$index.list"
  if ! "$find_binary" "$dir" -type f -name '*.go' -print0 >"$file_list"; then
    echo "ERROR: find failed while collecting SQLC files under $dir" >&2
    exit 1
  fi
  while IFS= read -r -d '' file; do
	if is_manual_extension "$file"; then
	  continue
	fi
    "$gofmt_binary" -w -r 'interface{} -> any' "$file"
	set +e
	"$rg_binary" -n 'interface\{\}' -- "$file"
	rg_status=$?
	set -e
	if [[ "$rg_status" -eq 0 ]]; then
	  echo "ERROR: SQLC post-processing left legacy interface{} syntax in generated output: $file" >&2
	  exit 1
	fi
	if [[ "$rg_status" -gt 1 ]]; then
	  echo "ERROR: rg failed while checking SQLC generated output $file (status $rg_status)" >&2
	  exit "$rg_status"
	fi
  done <"$file_list"
  index=$((index + 1))
done

echo "sqlc-postprocess: normalized generated empty interfaces to any"
