#!/usr/bin/env bash
# Guard commit messages. Titles must contain Chinese text; non-empty bodies must too.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENFORCEMENT_BASELINE_FILE="$ROOT_DIR/scripts/commit_title_enforcement_baseline.txt"
cd "$ROOT_DIR"

usage() {
  cat >&2 <<'USAGE'
usage:
  scripts/guard_commit_titles.sh --message <commit-msg-file>
  scripts/guard_commit_titles.sh --range <rev-range>
USAGE
}

fail_usage() {
  echo "FAIL: commit message guard: $*" >&2
  exit 2
}

first_title_from_file() {
  awk '
    /^[[:space:]]*#/ { next }
    /^[[:space:]]*$/ { next }
    { sub(/[[:space:]]+$/, ""); print; exit }
  ' "$1"
}

first_commit_title() {
  git log -1 --format='%B' "$1" | awk '
    /^[[:space:]]*#/ { next }
    /^[[:space:]]*$/ { next }
    { sub(/[[:space:]]+$/, ""); print; exit }
  '
}

body_from_file() {
  awk '
    /^[[:space:]]*#/ { next }
    !seen && /^[[:space:]]*$/ { next }
    !seen { seen = 1; next }
    /^[[:space:]]*$/ { next }
    { print }
  ' "$1"
}

commit_body() {
  git log -1 --format='%B' "$1" | awk '
    /^[[:space:]]*#/ { next }
    !seen && /^[[:space:]]*$/ { next }
    !seen { seen = 1; next }
    /^[[:space:]]*$/ { next }
    { print }
  '
}

has_chinese() {
  local text="$1"
  local pattern
  pattern=$'\xE4[\xB8-\xBF][\x80-\xBF]|[\xE5-\xE9][\x80-\xBF][\x80-\xBF]'

  (
    export LC_ALL=C
    [[ "$text" =~ $pattern ]]
  )
}

check_message_parts() {
  local label="$1"
  local title="$2"
  local body="$3"

  if [ -z "$title" ]; then
    echo "FAIL: $label title must not be empty." >&2
    return 1
  fi
  if ! has_chinese "$title"; then
    echo "FAIL: $label title must contain Chinese text." >&2
    echo "  title: $title" >&2
    return 1
  fi
  if [ -n "$body" ] && ! has_chinese "$body"; then
    echo "FAIL: $label body must contain Chinese text when present." >&2
    echo "  body: $(printf '%s\n' "$body" | sed -n '1p')" >&2
    return 1
  fi
  return 0
}

check_message_file() {
  local msg_file="$1"
  local title
  local body

  if [ ! -f "$msg_file" ]; then
    fail_usage "commit message file does not exist: $msg_file"
  fi

  title="$(first_title_from_file "$msg_file")"
  body="$(body_from_file "$msg_file")"
  check_message_parts "commit" "$title" "$body"
  echo "✅ Chinese commit message guard OK"
}

load_enforcement_baseline() {
  local baseline
  local line_count

  if [ ! -f "$ENFORCEMENT_BASELINE_FILE" ]; then
    fail_usage "enforcement baseline file does not exist: $ENFORCEMENT_BASELINE_FILE"
  fi
  line_count="$(awk 'END { print NR }' "$ENFORCEMENT_BASELINE_FILE")"
  baseline="$(cat "$ENFORCEMENT_BASELINE_FILE")"
  if [ "$line_count" -ne 1 ] || ! printf '%s\n' "$baseline" | LC_ALL=C grep -Eq '^[0-9a-f]{40}$'; then
    fail_usage "enforcement baseline must contain exactly one full 40-character lowercase commit SHA"
  fi
  if ! git rev-parse --verify --quiet "${baseline}^{commit}" >/dev/null; then
    fail_usage "enforcement baseline commit is not available: $baseline (use fetch-depth: 0)"
  fi
  printf '%s\n' "$baseline"
}

commit_is_grandfathered() {
  local commit="$1"
  local baseline="$2"
  local status

  if git merge-base --is-ancestor "$commit" "$baseline"; then
    return 0
  else
    status=$?
  fi
  if [ "$status" -eq 1 ]; then
    return 1
  fi
  fail_usage "cannot compare commit $commit with enforcement baseline $baseline"
}

check_range() {
  local range="$1"
  local baseline
  local commit
  local title
  local body
  local failures
  failures=0

  if ! git rev-list --reverse "$range" >/dev/null; then
    fail_usage "invalid commit range: $range"
  fi
  baseline="$(load_enforcement_baseline)"

  while IFS= read -r commit; do
    [ -n "$commit" ] || continue
    if commit_is_grandfathered "$commit" "$baseline"; then
      continue
    fi
    title="$(first_commit_title "$commit")"
    body="$(commit_body "$commit")"
    if ! check_message_parts "commit ${commit:0:7}" "$title" "$body"; then
      failures=1
    fi
  done < <(git rev-list --reverse "$range")

  if [ "$failures" -ne 0 ]; then
    exit 1
  fi
  echo "✅ Chinese commit message guard OK"
}

case "${1:-}" in
  --message)
    if [ "$#" -ne 2 ]; then
      usage
      exit 2
    fi
    check_message_file "$2"
    ;;
  --range)
    if [ "$#" -ne 2 ]; then
      usage
      exit 2
    fi
    check_range "$2"
    ;;
  *)
    usage
    exit 2
    ;;
esac
