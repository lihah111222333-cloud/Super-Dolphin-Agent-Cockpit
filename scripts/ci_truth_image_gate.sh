#!/usr/bin/env bash
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel) || {
  printf '%s\n' 'CI truth-image gate blocked: repository root is unavailable.' >&2
  exit 1
}
source "$repo_root/.githooks/trusted-gate-launcher.sh"
if ! gate_bin=$(trusted_gate_launcher "$repo_root"); then
  printf '%s\n' 'CI truth-image gate blocked: trusted super-dolphin-gate launcher is unavailable.' >&2
  exit 1
fi

if [[ $# -ne 1 ]]; then
  printf '%s\n' 'usage: ci_truth_image_gate.sh [local-fast|push|release]' >&2
  exit 1
fi

profile=$1
case "$profile" in
  local-fast|push|release) ;;
  *)
    printf 'CI truth-image gate blocked: unsupported profile %q.\n' "$profile" >&2
    exit 1
    ;;
esac

cd "$repo_root"

# gate 负责三阶段 token handshake。前两个阶段与 Git hook 一样不得检查 repository/config，
# 从而确保真实 token 不会通过 argv 传递或被此 adapter 意外持久化。
if [[ -z "${SUPER_DOLPHIN_CI_AGENT_TOKEN+x}" ]]; then
  case "$profile" in
    local-fast) exec "$gate_bin" remote hook pre-commit ;;
    push) exec "$gate_bin" remote hook pre-push ;;
    release) exec "$gate_bin" remote run ;;
  esac
fi
if [[ "${SUPER_DOLPHIN_CI_AGENT_TOKEN}" == "issue" ]]; then
  unset SUPER_DOLPHIN_CI_AGENT_TOKEN
  exec "$gate_bin" remote run --agent-token=issue
fi

remote_config=${SUPER_DOLPHIN_GATE_REMOTE_CONFIG:-$(git config --local --get super-dolphin.remote.config || true)}
remote_ledger=${SUPER_DOLPHIN_GATE_LEDGER:-$(git config --local --get super-dolphin.remote.ledger || true)}
if [[ -z "$remote_config" || -z "$remote_ledger" ]]; then
  printf '%s\n' 'CI truth-image gate blocked: remote CI requires remote config and duration ledger paths.' >&2
  exit 1
fi

commit_sha=$(git rev-parse --verify 'HEAD^{commit}') || {
  printf '%s\n' 'CI truth-image gate blocked: remote CI requires an existing HEAD commit.' >&2
  exit 1
}
staged_tree=$(git write-tree) || {
  printf '%s\n' 'CI truth-image gate blocked: cannot capture the authoritative staged tree.' >&2
  exit 1
}
if ! gate_bin=$(trusted_gate_launcher_for_tree "$repo_root" "$staged_tree"); then
  printf '%s\n' 'CI truth-image gate blocked: trusted launcher receipt does not match the candidate tree.' >&2
  exit 1
fi

case "$profile" in
  local-fast)
    exec "$gate_bin" remote hook pre-commit \
      --config "$remote_config" \
      --ledger "$remote_ledger" \
      --repository "$repo_root" \
      --tree "$staged_tree" \
      --parent "$commit_sha"
    ;;
  push)
    remote_name=${SUPER_DOLPHIN_CI_REMOTE_NAME:-origin}
    if [[ -z "$remote_name" || "$remote_name" != "${remote_name//[[:space:]]/}" ]]; then
      printf '%s\n' 'CI truth-image gate blocked: push requires a non-empty remote name.' >&2
      exit 1
    fi
    branch_name=$(git symbolic-ref --quiet --short HEAD) || {
      printf '%s\n' 'CI truth-image gate blocked: push requires a branch-backed HEAD.' >&2
      exit 1
    }
    remote_url=$(git remote get-url "$remote_name") || {
      printf 'CI truth-image gate blocked: push remote %q has no configured URL.\n' "$remote_name" >&2
      exit 1
    }
    local_ref="refs/heads/$branch_name"
    remote_ref=$(git config --local --get "branch.$branch_name.merge" || true)
    if [[ -z "$remote_ref" ]]; then
      remote_ref="$local_ref"
    fi
    if [[ "$remote_ref" != refs/heads/* ]]; then
      printf 'CI truth-image gate blocked: push remote ref %q is not a branch ref.\n' "$remote_ref" >&2
      exit 1
    fi
    remote_branch=${remote_ref#refs/heads/}
    remote_sha=$(git rev-parse --verify "$remote_name/$remote_branch^{commit}" 2>/dev/null || true)
    if [[ -z "$remote_sha" ]]; then
      remote_sha=$(printf '%*s' "${#commit_sha}" '' | tr ' ' '0')
    fi
    printf '%s %s %s %s\n' "$local_ref" "$commit_sha" "$remote_ref" "$remote_sha" |
      "$gate_bin" remote hook pre-push \
        --config "$remote_config" \
        --ledger "$remote_ledger" \
        --repository "$repo_root" \
        "$remote_name" "$remote_url"
    ;;
  release)
    commit_tree=$(git rev-parse --verify "${commit_sha}^{tree}") || {
      printf '%s\n' 'CI truth-image gate blocked: cannot resolve the release commit tree.' >&2
      exit 1
    }
    if [[ "$staged_tree" != "$commit_tree" ]]; then
      printf '%s\n' 'CI truth-image gate blocked: release requires the staged index to match HEAD; commit or unstage the candidate changes first.' >&2
      exit 1
    fi
    exec "$gate_bin" remote run \
      --config "$remote_config" \
      --ledger "$remote_ledger" \
      --repository "$repo_root" \
      --scenario full \
      --profile release \
      --entrypoint release \
      --commit "$commit_sha"
    ;;
esac
