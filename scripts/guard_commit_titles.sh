#!/usr/bin/env bash
# Guard commit messages. Titles must contain Chinese text; non-empty bodies must too.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
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
  [[ "$text" =~ [一-龥] ]]
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

check_range() {
  local range="$1"
  local commit
  local title
  local body
  local failures
  failures=0

  if ! git rev-list --reverse "$range" >/dev/null; then
    fail_usage "invalid commit range: $range"
  fi

  while IFS= read -r commit; do
    [ -n "$commit" ] || continue
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
