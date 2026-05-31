#!/usr/bin/env bash
# Guard commit titles. Commit titles must contain Chinese text.
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
  echo "FAIL: commit title guard: $*" >&2
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

title_has_chinese() {
  local title="$1"
  [[ "$title" =~ [一-龥] ]]
}

check_title() {
  local label="$1"
  local title="$2"

  if [ -z "$title" ]; then
    echo "FAIL: $label title must not be empty." >&2
    return 1
  fi
  if ! title_has_chinese "$title"; then
    echo "FAIL: $label title must contain Chinese text." >&2
    echo "  title: $title" >&2
    return 1
  fi
  return 0
}

check_message_file() {
  local msg_file="$1"
  local title

  if [ ! -f "$msg_file" ]; then
    fail_usage "commit message file does not exist: $msg_file"
  fi

  title="$(first_title_from_file "$msg_file")"
  check_title "commit" "$title"
  echo "✅ Chinese commit title guard OK"
}

check_range() {
  local range="$1"
  local commit
  local title
  local failures
  failures=0

  if ! git rev-list --reverse "$range" >/dev/null; then
    fail_usage "invalid commit range: $range"
  fi

  while IFS= read -r commit; do
    [ -n "$commit" ] || continue
    title="$(first_commit_title "$commit")"
    if ! check_title "commit ${commit:0:7}" "$title"; then
      failures=1
    fi
  done < <(git rev-list --reverse "$range")

  if [ "$failures" -ne 0 ]; then
    exit 1
  fi
  echo "✅ Chinese commit title guard OK"
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
