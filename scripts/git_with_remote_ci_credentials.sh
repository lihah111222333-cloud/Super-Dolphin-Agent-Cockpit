#!/usr/bin/env bash
set -euo pipefail
set +x

usage() {
  printf '%s\n' 'usage: git_with_remote_ci_credentials.sh [--repository <path>] -- <git-arguments...>' >&2
}

repository=.
if [[ "${1-}" == "--repository" ]]; then
  if [[ $# -lt 3 || -z "${2-}" ]]; then
    usage
    exit 2
  fi
  repository=$2
  shift 2
fi
if [[ "${1-}" != "--" || $# -lt 2 ]]; then
  usage
  exit 2
fi
shift

repo_root=$(git -C "$repository" rev-parse --show-toplevel) || {
  printf '%s\n' 'remote CI Git blocked: repository root is unavailable.' >&2
  exit 1
}
repo_root=$(cd "$repo_root" && pwd -P) || {
  printf '%s\n' 'remote CI Git blocked: repository root is not canonical.' >&2
  exit 1
}
script_path=${BASH_SOURCE[0]}
if [[ ! -f "$script_path" || -L "$script_path" ]]; then
  printf '%s\n' 'remote CI Git blocked: credential launcher must be a regular non-symlink file.' >&2
  exit 1
fi
script_directory=$(cd "$(dirname "$script_path")" && pwd -P) || {
  printf '%s\n' 'remote CI Git blocked: credential launcher directory is not canonical.' >&2
  exit 1
}
launcher_contract="$(dirname "$script_directory")/.githooks/trusted-gate-launcher.sh"
if [[ ! -f "$launcher_contract" || -L "$launcher_contract" ]]; then
  printf '%s\n' 'remote CI Git blocked: credential launcher owner has no regular trusted launcher contract.' >&2
  exit 1
fi

# 只执行启动器所属仓库的受信契约，并在隔离子进程中验证目标仓库 Gate。
# 目标仓库内容不能修改随后持有凭据的当前 shell。
gate_bin=$(bash -c 'set -euo pipefail; source "$1"; trusted_gate_launcher "$2"' bash "$launcher_contract" "$repo_root") || {
  printf '%s\n' 'remote CI Git blocked: target repository has no verified trusted Gate; run make install-hooks there first.' >&2
  exit 1
}
if [[ "$gate_bin" != /* || ! -f "$gate_bin" || -L "$gate_bin" || ! -x "$gate_bin" ]]; then
  printf '%s\n' 'remote CI Git blocked: trusted Gate path is not a regular absolute executable.' >&2
  exit 1
fi

if [[ -z "${SUPER_DOLPHIN_CI_AGENT_TOKEN+x}" || "${SUPER_DOLPHIN_CI_AGENT_TOKEN-}" == "issue" ]]; then
  command -v python3 >/dev/null 2>&1 || {
    printf '%s\n' 'remote CI Git blocked: Python 3 is required to validate the agent-token bootstrap.' >&2
    exit 1
  }
  set +e
  agent_bootstrap=$("$gate_bin" remote run --agent-token=issue)
  bootstrap_status=$?
  set -e
  if [[ -z "$agent_bootstrap" ]]; then
    printf 'remote CI Git blocked: Gate returned no agent-token bootstrap (status=%d).\n' "$bootstrap_status" >&2
    exit 1
  fi
  SUPER_DOLPHIN_CI_AGENT_TOKEN=$(printf '%s' "$agent_bootstrap" | python3 -c '
import hashlib
import json
import re
import sys

try:
    value = json.load(sys.stdin)
    token = value["agent_token"]
    digest = "sha256:" + hashlib.sha256(token.encode("utf-8")).hexdigest()
    valid = (
        value.get("schema_version") == 1
        and value.get("kind") == "remote_ci_agent_token_bootstrap"
        and value.get("issued") is True
        and value.get("retry_required") is True
        and value.get("execute_ci") is False
        and value.get("reuse_environment_name") == "SUPER_DOLPHIN_CI_AGENT_TOKEN"
        and value.get("reuse_environment_value") == token
        and value.get("agent_token_digest") == digest
        and re.fullmatch(r"sdci1_[A-Za-z0-9_-]{43}", token) is not None
    )
    if not valid:
        raise ValueError("non-canonical bootstrap")
except (KeyError, TypeError, ValueError, json.JSONDecodeError) as error:
    print(f"remote CI Git blocked: invalid agent-token bootstrap: {error}", file=sys.stderr)
    raise SystemExit(1)
sys.stdout.write(token)
') || exit 1
  unset agent_bootstrap
fi
if [[ ! "$SUPER_DOLPHIN_CI_AGENT_TOKEN" =~ ^sdci1_[A-Za-z0-9_-]{43}$ ]]; then
  printf '%s\n' 'remote CI Git blocked: caller-owned agent token is not canonical.' >&2
  exit 1
fi

username_present=0
token_present=0
[[ -n "${SUPER_DOLPHIN_CI_GHCR_USERNAME+x}" ]] && username_present=1
[[ -n "${SUPER_DOLPHIN_CI_GHCR_TOKEN+x}" ]] && token_present=1
if [[ "$username_present" -ne "$token_present" ]]; then
  printf '%s\n' 'remote CI Git blocked: GHCR username and token must be supplied together.' >&2
  exit 1
fi
if [[ "$username_present" -eq 0 ]]; then
  command -v gh >/dev/null 2>&1 || {
    printf '%s\n' 'remote CI Git blocked: GitHub CLI is required to read the OS credential store.' >&2
    exit 1
  }
  SUPER_DOLPHIN_CI_GHCR_USERNAME=$(gh auth status --active --hostname github.com --json hosts \
    --jq '.hosts["github.com"][] | select(.active == true and .state == "success") | .login') || {
    printf '%s\n' 'remote CI Git blocked: GitHub CLI authentication status is unavailable.' >&2
    exit 1
  }
  if [[ -z "$SUPER_DOLPHIN_CI_GHCR_USERNAME" ]]; then
    printf '%s\n' 'remote CI Git blocked: GitHub CLI has no active authenticated github.com identity.' >&2
    exit 1
  fi
  SUPER_DOLPHIN_CI_GHCR_TOKEN=$(gh auth token --hostname github.com) || {
    printf '%s\n' 'remote CI Git blocked: GitHub token is unavailable from the OS credential store.' >&2
    exit 1
  }
fi

if [[ -z "$SUPER_DOLPHIN_CI_GHCR_USERNAME" || -z "$SUPER_DOLPHIN_CI_GHCR_TOKEN" \
  || "$SUPER_DOLPHIN_CI_GHCR_USERNAME" != "${SUPER_DOLPHIN_CI_GHCR_USERNAME//[$'\r\n\t ']/}" \
  || "$SUPER_DOLPHIN_CI_GHCR_TOKEN" != "${SUPER_DOLPHIN_CI_GHCR_TOKEN//[$'\r\n\t ']/}" \
  || ${#SUPER_DOLPHIN_CI_GHCR_USERNAME} -gt 256 || ${#SUPER_DOLPHIN_CI_GHCR_TOKEN} -gt 256 ]]; then
  printf '%s\n' 'remote CI Git blocked: GHCR credential values are empty, malformed, or too long.' >&2
  exit 1
fi

export SUPER_DOLPHIN_CI_AGENT_TOKEN
export SUPER_DOLPHIN_CI_GHCR_USERNAME
export SUPER_DOLPHIN_CI_GHCR_TOKEN
exec git -C "$repo_root" "$@"
