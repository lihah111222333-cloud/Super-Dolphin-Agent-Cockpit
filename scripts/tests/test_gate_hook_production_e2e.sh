#!/usr/bin/env bash
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel 2>/dev/null) || {
  printf '%s\n' 'production hook E2E: cwd is not an active Git worktree' >&2
  exit 1
}
repo_root=$(cd "$repo_root" && pwd -P)

mode=${1:-}
case "$mode" in
  _cleanup-contract)
    [[ ${GATE_HOOK_E2E_CLEANUP_CONTRACT:-} == 1 ]] || {
      printf '%s\n' 'production hook E2E: cleanup contract mode is test-only' >&2
      exit 2
    }
    ;;
  *)
    printf '%s\n' 'usage: test_gate_hook_production_e2e.sh _cleanup-contract' >&2
    exit 2
    ;;
esac

evidence_root=${GATE_HOOK_E2E_EVIDENCE_DIR:-}
if [[ -z "$evidence_root" ]]; then
  evidence_root=$(mktemp -d -t gate-hook-production-e2e.XXXXXX)
else
  mkdir -p "$evidence_root"
  evidence_root=$(cd "$evidence_root" && pwd -P)
fi
chmod 700 "$evidence_root"

git_e2e_fixture=
git_e2e_repo_root=
git_e2e_worktree=
git_e2e_remote=
git_e2e_remote_name=
git_e2e_branch=
git_e2e_lock_dir=
git_e2e_lock_held=0

release_git_metadata_lock() {
  if [[ ${git_e2e_lock_held:-0} -eq 1 ]]; then
    rmdir "$git_e2e_lock_dir" >/dev/null 2>&1 || true
    git_e2e_lock_held=0
  fi
}

acquire_git_metadata_lock() {
  local attempt
  for ((attempt = 1; attempt <= 600; attempt++)); do
    if mkdir "$git_e2e_lock_dir" 2>/dev/null; then
      git_e2e_lock_held=1
      return 0
    fi
    sleep 0.05
  done
  printf 'production hook E2E: timed out acquiring Git metadata lock %s\n' "$git_e2e_lock_dir" >&2
  return 1
}

with_git_metadata_lock() {
  local command_status
  acquire_git_metadata_lock || return 1
  if "$@"; then
    command_status=0
  else
    command_status=$?
  fi
  release_git_metadata_lock
  return "$command_status"
}

remove_git_e2e_metadata() {
  local repository=$1 worktree=$2 branch=$3 remote_name=$4
  [[ -z "$worktree" ]] || git -C "$repository" worktree remove --force "$worktree" >/dev/null 2>&1 || true
  [[ -z "$branch" ]] || git -C "$repository" branch -D "$branch" >/dev/null 2>&1 || true
  [[ -z "$remote_name" ]] || git -C "$repository" remote remove "$remote_name" >/dev/null 2>&1 || true
}

cleanup_git_e2e() {
  local repository=${git_e2e_repo_root:-}
  local worktree=${git_e2e_worktree:-}
  local branch=${git_e2e_branch:-}
  local remote_name=${git_e2e_remote_name:-}
  local fixture=${git_e2e_fixture:-}
  release_git_metadata_lock
  if [[ -n "$repository" ]] && with_git_metadata_lock remove_git_e2e_metadata "$repository" "$worktree" "$branch" "$remote_name"; then
    git_e2e_worktree=
    git_e2e_branch=
    git_e2e_remote_name=
    git_e2e_remote=
    git_e2e_repo_root=
    [[ -z "$fixture" ]] || rm -rf "$fixture"
    git_e2e_fixture=
  elif [[ -n "$repository" ]]; then
    printf 'production hook E2E: cleanup could not acquire the Git metadata lock for %s\n' "$repository" >&2
  elif [[ -n "$fixture" ]]; then
    rm -rf "$fixture"
    git_e2e_fixture=
  fi
}

exit_from_git_e2e_signal() {
  exit "$1"
}

add_git_e2e_metadata() {
  local repository=$1 worktree=$2 branch=$3 remote_name=$4 remote=$5
  git -C "$repository" worktree add --detach "$worktree" HEAD || return 1
  git -C "$worktree" switch -c "$branch" || return 1
  git -C "$repository" remote add "$remote_name" "$remote" || return 1
}

prepare_git_e2e() {
  local repository=$1 identity common_dir
  identity="$(date +%s)-$$-$RANDOM"
  git_e2e_repo_root=$(cd "$repository" && pwd -P)
  git_e2e_fixture=$(mktemp -d -t gate-hook-git-e2e.XXXXXX)
  git_e2e_worktree="$git_e2e_fixture/worktree"
  git_e2e_remote="$git_e2e_fixture/remote.git"
  git_e2e_branch="gate-hook-e2e-$identity"
  git_e2e_remote_name="gate-hook-e2e-$identity"
  common_dir=$(git -C "$git_e2e_repo_root" rev-parse --path-format=absolute --git-common-dir)
  git_e2e_lock_dir="$common_dir/super-dolphin-gate-hook-e2e.lock"
  trap cleanup_git_e2e EXIT
  trap 'exit_from_git_e2e_signal 130' INT
  trap 'exit_from_git_e2e_signal 143' TERM
  git init --bare "$git_e2e_remote"
  with_git_metadata_lock add_git_e2e_metadata \
    "$git_e2e_repo_root" "$git_e2e_worktree" "$git_e2e_branch" "$git_e2e_remote_name" "$git_e2e_remote"
}

run_cleanup_contract() {
  prepare_git_e2e "${GATE_HOOK_E2E_CLEANUP_REPOSITORY:?}"
  printf '%s\n%s\n%s\n%s\n%s\n' \
    "$git_e2e_fixture" "$git_e2e_worktree" "$git_e2e_branch" "$git_e2e_remote_name" "$git_e2e_repo_root" \
    >"${GATE_HOOK_E2E_CLEANUP_STATE_FILE:?}"
  case ${GATE_HOOK_E2E_CLEANUP_OUTCOME:-success} in
    success) return 0 ;;
    failure) return 19 ;;
    int)
      kill -s INT "$$"
      exit_from_git_e2e_signal 130
      ;;
    term)
      kill -s TERM "$$"
      exit_from_git_e2e_signal 143
      ;;
    repeat)
      cleanup_git_e2e
      cleanup_git_e2e
      return 0
      ;;
    *) return 2 ;;
  esac
}

case "$mode" in
  _cleanup-contract) run_cleanup_contract ;;
esac
