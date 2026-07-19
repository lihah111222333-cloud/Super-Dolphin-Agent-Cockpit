#!/usr/bin/env bash
# Guard: every fix commit must carry a bug-locking test in the same commit.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

usage() {
  cat >&2 <<'USAGE'
usage:
  scripts/guard_fix_commits_have_tests.sh --cached <commit-msg-file>
  scripts/guard_fix_commits_have_tests.sh --range <rev-range>
USAGE
}

is_fix_subject() {
  local subject="$1"
  case "$subject" in
    [Ff][Ii][Xx]:*|[Ff][Ii][Xx]!:*|[Ff][Ii][Xx]\ *|[Ff][Ii][Xx]\(*\):*|[Ff][Ii][Xx]\(*\)!:*|\[[Ff][Ii][Xx]\]*) return 0 ;;
    [Hh][Oo][Tt][Ff][Ii][Xx]:*|[Hh][Oo][Tt][Ff][Ii][Xx]!:*|[Hh][Oo][Tt][Ff][Ii][Xx]\ *|[Hh][Oo][Tt][Ff][Ii][Xx]\(*\):*|[Hh][Oo][Tt][Ff][Ii][Xx]\(*\)!:*|\[[Hh][Oo][Tt][Ff][Ii][Xx]\]*) return 0 ;;
    [Bb][Uu][Gg][Ff][Ii][Xx]:*|[Bb][Uu][Gg][Ff][Ii][Xx]!:*|[Bb][Uu][Gg][Ff][Ii][Xx]\ *|[Bb][Uu][Gg][Ff][Ii][Xx]\(*\):*|[Bb][Uu][Gg][Ff][Ii][Xx]\(*\)!:*|\[[Bb][Uu][Gg][Ff][Ii][Xx]\]*) return 0 ;;
    修复*|修正*|\[修复\]*|\[修正\]*) return 0 ;;
  esac
  return 1
}

first_commit_subject() {
  awk '
    /^[[:space:]]*#/ { next }
    /^[[:space:]]*$/ { next }
    { sub(/[[:space:]]+$/, ""); print; exit }
  ' "$1"
}

is_direct_test_path() {
  local path="$1"
  case "$path" in
    *_test.go|*_test.py|test_*.py) return 0 ;;
    *.test.js|*.test.jsx|*.test.mjs|*.test.cjs|*.test.ts|*.test.tsx) return 0 ;;
    *.spec.js|*.spec.jsx|*.spec.mjs|*.spec.cjs|*.spec.ts|*.spec.tsx) return 0 ;;
    tests/*|*/tests/*) return 0 ;;
  esac
  return 1
}

is_fixture_evidence_path() {
  local path="$1"
  case "$path" in
    testdata/*|*/testdata/*) return 0 ;;
    fixtures/*|*/fixtures/*|fixture/*|*/fixture/*) return 0 ;;
    golden/*|*/golden/*|*.golden|*.golden.*) return 0 ;;
    __snapshots__/*|*/__snapshots__/*|snapshots/*|*/snapshots/*|*.snap) return 0 ;;
  esac
  return 1
}

fixture_owner_dir() {
  local path="$1"
  case "$path" in
    */testdata/*) echo "${path%%/testdata/*}"; return 0 ;;
    testdata/*) echo "."; return 0 ;;
    */fixtures/*) echo "${path%%/fixtures/*}"; return 0 ;;
    fixtures/*) echo "."; return 0 ;;
    */fixture/*) echo "${path%%/fixture/*}"; return 0 ;;
    fixture/*) echo "."; return 0 ;;
    */golden/*) echo "${path%%/golden/*}"; return 0 ;;
    golden/*) echo "."; return 0 ;;
    */__snapshots__/*) echo "${path%%/__snapshots__/*}"; return 0 ;;
    __snapshots__/*) echo "."; return 0 ;;
    */snapshots/*) echo "${path%%/snapshots/*}"; return 0 ;;
    snapshots/*) echo "."; return 0 ;;
    *.golden|*.golden.*|*.snap)
      dirname "$path"
      return 0
      ;;
  esac
  return 1
}

path_dirname() {
  local path="$1"
  if [[ "$path" == */* ]]; then
    dirname "$path"
    return 0
  fi
  echo "."
}

direct_test_owner_dir() {
  local path="$1"
  case "$path" in
    */tests/*) echo "${path%%/tests/*}"; return 0 ;;
    tests/*) echo "."; return 0 ;;
    *)
      path_dirname "$path"
      return 0
      ;;
  esac
}

fixture_matches_production_dir() {
  local owner="$1"
  local prod_dir="$2"
  [ -n "$owner" ] || return 1
  [ "$owner" = "$prod_dir" ] && return 0
  [[ "$owner" == "$prod_dir"/* ]] && return 0
  parent_package_matches_production_dir "$owner" "$prod_dir" && return 0
  repository_tooling_test_matches_production_dir "$owner" "$prod_dir" && return 0
  frontend_app_integration_test_matches_production_dir "$owner" "$prod_dir" && return 0
  agent_terminal_integration_test_matches_production_dir "$owner" "$prod_dir" && return 0
  return 1
}

evidence_matches_production_dir() {
  fixture_matches_production_dir "$1" "$2"
}

parent_package_matches_production_dir() {
  local owner="$1"
  local prod_dir="$2"
  [ "$owner" != "." ] || return 1
  # Only deep package owners may cover child production packages; broad owners
  # such as internal or cmd would turn unrelated tests into false positives.
  case "$owner" in
    internal/*/*|cmd/*/*|frontend-app/src/*/*)
      [[ "$prod_dir" == "$owner"/* ]]
      return
      ;;
  esac
  return 1
}

repository_tooling_test_matches_production_dir() {
  local owner="$1"
  local prod_dir="$2"
  [ "$owner" = "scripts" ] || return 1
  # Repository tooling is guarded by scripts tests because hooks and Makefile
  # paths are integration surfaces rather than language package directories.
  case "$prod_dir" in
    .|.githooks|scripts)
      return 0
      ;;
  esac
  return 1
}

frontend_app_integration_test_matches_production_dir() {
  local owner="$1"
  local prod_dir="$2"
  case "$owner" in
    frontend-app|frontend-app/scripts)
      case "$prod_dir" in
        frontend-app/src|frontend-app/src/*)
          return 0
          ;;
      esac
      ;;
  esac
  return 1
}

agent_terminal_integration_test_matches_production_dir() {
  local owner="$1"
  local prod_dir="$2"
  [ "$owner" = "cmd/agent-terminal" ] || return 1
  case "$prod_dir" in
    internal/app|internal/platform/appupdaterecovery)
      return 0
      ;;
  esac
  return 1
}

explicit_bug_id_from_subject() {
  local subject="$1"
  printf '%s\n' "$subject" | grep -Eoi '([A-Z][A-Z0-9]+-[0-9]+|R[0-9]+|P[0-9]+)' | head -n 1 | tr '[:lower:]' '[:upper:]' || true
}

path_mentions_bug_id() {
  local path="$1"
  local bug_id="$2"
  [ -n "$bug_id" ] || return 1
  [[ "$(printf '%s' "$path" | tr '[:lower:]' '[:upper:]')" == *"$bug_id"* ]]
}

diff_stream_has_bug_locking_test() {
  local subject="${1:-}"
  local status path owner prod_dir bug_id
  local prod_dirs=()
  local direct_test_owners=()
  local direct_test_paths=()
  local fixture_owners=()
  bug_id="$(explicit_bug_id_from_subject "$subject")"
  while IFS= read -r -d '' status; do
    path=
    case "$status" in
      D*)
        IFS= read -r -d '' path || break
        continue
        ;;
      R*|C*)
        IFS= read -r -d '' _ || break
        IFS= read -r -d '' path || break
        ;;
      *)
        IFS= read -r -d '' path || break
        ;;
    esac
    if is_direct_test_path "$path"; then
      direct_test_owners+=("$(direct_test_owner_dir "$path")")
      direct_test_paths+=("$path")
      continue
    fi
    if is_fixture_evidence_path "$path"; then
      owner="$(fixture_owner_dir "$path" || true)"
      if [ -n "$owner" ]; then
        fixture_owners+=("$owner")
      fi
      continue
    fi
    prod_dirs+=("$(path_dirname "$path")")
  done
  if [ "${#direct_test_paths[@]}" -gt 0 ] && [ -n "$bug_id" ]; then
    for path in "${direct_test_paths[@]}"; do
      if path_mentions_bug_id "$path" "$bug_id"; then
        return 0
      fi
    done
  fi
  if [ "${#direct_test_owners[@]}" -gt 0 ] && [ "${#prod_dirs[@]}" -gt 0 ]; then
    for owner in "${direct_test_owners[@]}"; do
      for prod_dir in "${prod_dirs[@]}"; do
        if evidence_matches_production_dir "$owner" "$prod_dir"; then
          return 0
        fi
      done
    done
  fi
  if [ "${#fixture_owners[@]}" -gt 0 ] && [ "${#prod_dirs[@]}" -gt 0 ]; then
    for owner in "${fixture_owners[@]}"; do
      for prod_dir in "${prod_dirs[@]}"; do
        if evidence_matches_production_dir "$owner" "$prod_dir"; then
          return 0
        fi
      done
    done
  fi
  return 1
}

cached_diff_has_bug_locking_test() {
  local subject="${1:-}"
  diff_stream_has_bug_locking_test "$subject" < <(git diff --cached --name-status -z --diff-filter=ACMRD --)
}

commit_diff_has_bug_locking_test() {
  local commit="$1"
  local subject="${2:-}"
  diff_stream_has_bug_locking_test "$subject" < <(git diff-tree --no-commit-id --name-status -r -z --root --diff-filter=ACMRD "$commit")
}

commit_is_merge() {
  local commit="$1"
  local -a commit_line=()
  read -r -a commit_line < <(git rev-list --parents -n 1 "$commit")
  [ "${#commit_line[@]}" -gt 2 ]
}

fail_missing_cached_test() {
  local subject="$1"
  cat >&2 <<EOF
❌ fix 提交缺少锁定 bug 的测试
  subject: $subject
  规则: fix/hotfix/bugfix/修复 提交必须在同一提交修改测试、fixture、golden 或 snapshot。
  常见路径: *_test.go, *.test.ts, *.test.mjs, *.spec.ts, *.spec.mjs, tests/**, testdata/**, fixtures/**, golden/**, __snapshots__/**
EOF
}

fail_missing_commit_test() {
  local commit="$1"
  local subject="$2"
  cat >&2 <<EOF
❌ fix commit 缺少锁定 bug 的测试
  commit:  $commit
  subject: $subject
  规则: fix/hotfix/bugfix/修复 提交必须在同一提交修改测试、fixture、golden 或 snapshot。
EOF
}

check_cached_commit_message() {
  local msg_file="$1"
  if [ ! -f "$msg_file" ]; then
    echo "❌ commit-msg 文件不存在: $msg_file" >&2
    exit 2
  fi

  local subject
  subject="$(first_commit_subject "$msg_file")"
  if [ -z "$subject" ] || ! is_fix_subject "$subject"; then
    return 0
  fi

  # amend --no-edit or sequencer paths can reach commit-msg without a staged diff.
  # The pushed commit history is still checked by pre-push.
  if git diff --cached --quiet --; then
    echo "ℹ️  fix-test guard: staged diff 为空，交给 pre-push 检查最终 commit"
    return 0
  fi

  if cached_diff_has_bug_locking_test "$subject"; then
    echo "✅ fix-test guard OK"
    return 0
  fi

  fail_missing_cached_test "$subject"
  exit 1
}

check_range() {
  local rev_range="$1"
  local commit subject failures
  failures=0

  while IFS= read -r commit; do
    [ -n "$commit" ] || continue
    subject="$(git log -1 --format=%s "$commit")"
    if ! is_fix_subject "$subject"; then
      continue
    fi
    # Merge commits are checked through their child commits in the pushed range.
    # GitHub-style merge subjects often repeat the PR fix title with no own diff.
    if commit_is_merge "$commit"; then
      continue
    fi
    if commit_diff_has_bug_locking_test "$commit" "$subject"; then
      continue
    fi
    fail_missing_commit_test "$commit" "$subject"
    failures=1
  done < <(git rev-list --reverse "$rev_range")

  if [ "$failures" -ne 0 ]; then
    exit 1
  fi
  echo "✅ fix-test guard OK"
}

case "${1:-}" in
  --cached)
    if [ "$#" -ne 2 ]; then
      usage
      exit 2
    fi
    check_cached_commit_message "$2"
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
