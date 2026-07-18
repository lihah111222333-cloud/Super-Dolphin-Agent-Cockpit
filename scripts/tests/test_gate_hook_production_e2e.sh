#!/usr/bin/env bash
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel 2>/dev/null) || {
  printf '%s\n' 'production hook E2E: cwd is not an active Git worktree' >&2
  exit 1
}
repo_root=$(cd "$repo_root" && pwd -P)
gate_bin=$(command -v super-dolphin-gate 2>/dev/null || true)
if [[ -z "$gate_bin" || ! -x "$gate_bin" ]]; then
  printf '%s\n' 'production hook E2E: trusted super-dolphin-gate CLI is not installed' >&2
  exit 1
fi

mode=${1:-}
case "$mode" in
  git|codex) ;;
  _cleanup-contract)
    [[ ${GATE_HOOK_E2E_CLEANUP_CONTRACT:-} == 1 ]] || {
      printf '%s\n' 'production hook E2E: cleanup contract mode is test-only' >&2
      exit 2
    }
    ;;
  *)
    printf '%s\n' 'usage: test_gate_hook_production_e2e.sh git|codex' >&2
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
git_e2e_worktree=
git_e2e_remote=
git_e2e_remote_name=
git_e2e_branch=

cleanup_git_e2e() {
  local worktree=${git_e2e_worktree:-}
  local branch=${git_e2e_branch:-}
  local remote_name=${git_e2e_remote_name:-}
  local fixture=${git_e2e_fixture:-}
  git_e2e_worktree=
  git_e2e_branch=
  git_e2e_remote_name=
  git_e2e_remote=
  git_e2e_fixture=
  [[ -z "$worktree" ]] || git -C "$repo_root" worktree remove --force "$worktree" >/dev/null 2>&1 || true
  [[ -z "$branch" ]] || git -C "$repo_root" branch -D "$branch" >/dev/null 2>&1 || true
  [[ -z "$remote_name" ]] || git -C "$repo_root" remote remove "$remote_name" >/dev/null 2>&1 || true
  [[ -z "$fixture" ]] || rm -rf "$fixture"
}

prepare_git_e2e() {
  local identity
  identity="$(date +%s)-$$-$RANDOM"
  git_e2e_fixture=$(mktemp -d -t gate-hook-git-e2e.XXXXXX)
  git_e2e_worktree="$git_e2e_fixture/worktree"
  git_e2e_remote="$git_e2e_fixture/remote.git"
  git_e2e_branch="gate-hook-e2e-$identity"
  git_e2e_remote_name="gate-hook-e2e-$identity"
  trap cleanup_git_e2e EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  git -C "$repo_root" worktree add --detach "$git_e2e_worktree" HEAD
  git init --bare "$git_e2e_remote"
  git -C "$git_e2e_worktree" switch -c "$git_e2e_branch"
  git -C "$git_e2e_worktree" config user.name 'Gate Hook Production E2E'
  git -C "$git_e2e_worktree" config user.email 'gate-hook-e2e@example.invalid'
  git -C "$git_e2e_worktree" remote add "$git_e2e_remote_name" "$git_e2e_remote"
}

extract_job_id() {
  sed -n 's/.*job=\([^;[:space:]]*\).*/\1/p' "$1" | head -n 1
}

validate_passed_status() {
  python3 - "$1" "$2" <<'PY'
import json
import pathlib
import sys

status = json.loads(pathlib.Path(sys.argv[1]).read_text())
expected_tree = sys.argv[2]
required = {
    "state": "passed",
    "terminal": True,
    "job_source_tree_sha": expected_tree,
    "image_provenance_source_tree_sha": expected_tree,
}
for field, expected in required.items():
    if status.get(field) != expected:
        raise SystemExit(f"production hook E2E: {field}={status.get(field)!r}, want {expected!r}")
if not status.get("job_id") or not status.get("receipt_id") or not status.get("gate_results"):
    raise SystemExit(f"production hook E2E: terminal status lacks job/receipt/gate evidence: {status!r}")
PY
}

run_gate_command() {
  local label=$1 expected_tree=$2
  shift 2
  local attempt output job wait_json wait_err
  for attempt in 1 2 3 4; do
    output="$evidence_root/$label.attempt-$attempt.log"
    if "$@" >"$output" 2>&1; then
      grep -Fq 'gate hook passed: job=' "$output" || {
        printf 'production hook E2E: %s passed without correlated hook evidence\n' "$label" >&2
        return 1
      }
      grep -Fq 'receipt=' "$output" || return 1
      grep -Fq "source_tree=$expected_tree" "$output" || return 1
      return 0
    fi
    job=$(extract_job_id "$output")
    if [[ -z "$job" ]]; then
      cat "$output" >&2
      return 1
    fi
    wait_json="$evidence_root/$label.wait-$attempt.json"
    wait_err="$evidence_root/$label.wait-$attempt.err"
    if ! "$gate_bin" wait --job "$job" >"$wait_json" 2>"$wait_err"; then
      cat "$output" >&2
      cat "$wait_json" >&2
      cat "$wait_err" >&2
      return 1
    fi
    validate_passed_status "$wait_json" "$expected_tree"
  done
  printf 'production hook E2E: %s did not consume its passed receipt after four attempts\n' "$label" >&2
  return 1
}

run_expected_violation() {
  local expected_tree=$1
  shift
  local output job wait_json wait_err
  output="$evidence_root/pre-commit-violation.log"
  if "$@" >"$output" 2>&1; then
    printf '%s\n' 'production hook E2E: deliberate whitespace violation unexpectedly passed' >&2
    return 1
  fi
  job=$(extract_job_id "$output")
  [[ -n "$job" ]] || {
    cat "$output" >&2
    return 1
  }
  grep -Fq "status: super-dolphin-gate status --job $job" "$output" || return 1
  grep -Fq "wait: super-dolphin-gate wait --job $job" "$output" || return 1
  wait_json="$evidence_root/pre-commit-violation.wait.json"
  wait_err="$evidence_root/pre-commit-violation.wait.err"
  if "$gate_bin" wait --job "$job" >"$wait_json" 2>"$wait_err"; then
    printf '%s\n' 'production hook E2E: deliberate violation reached a successful terminal state' >&2
    return 1
  fi
  python3 - "$wait_json" "$expected_tree" <<'PY'
import json
import pathlib
import sys

status = json.loads(pathlib.Path(sys.argv[1]).read_text())
if status.get("state") != "failed" or not status.get("terminal"):
    raise SystemExit(f"production hook E2E: violation status is not terminal failed: {status!r}")
if status.get("job_source_tree_sha") != sys.argv[2] or not status.get("job_id") or not status.get("gate_results"):
    raise SystemExit(f"production hook E2E: violation evidence is not source-bound: {status!r}")
PY
}

run_git_e2e() {
  git -C "$repo_root" diff --quiet
  git -C "$repo_root" diff --cached --quiet
  local hooks_env bad_tree clean_tree commit
  prepare_git_e2e
  mkdir -p "$git_e2e_worktree/nested"
  hooks_env=(env GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=core.hooksPath GIT_CONFIG_VALUE_0="$repo_root/.githooks")

  printf 'deliberate trailing whitespace \n' >"$git_e2e_worktree/.gate-hook-e2e"
  git -C "$git_e2e_worktree" add .gate-hook-e2e
  bad_tree=$(git -C "$git_e2e_worktree" write-tree)
  run_expected_violation "$bad_tree" "${hooks_env[@]}" git -C "$git_e2e_worktree/nested" commit -m '验证真实门禁违规反馈'

  printf '%s\n' 'production hook E2E' >"$git_e2e_worktree/.gate-hook-e2e"
  git -C "$git_e2e_worktree" add .gate-hook-e2e
  clean_tree=$(git -C "$git_e2e_worktree" write-tree)
  run_gate_command pre-commit "$clean_tree" "${hooks_env[@]}" git -C "$git_e2e_worktree/nested" commit -m '验证真实 Hook 端到端链路'
  commit=$(git -C "$git_e2e_worktree" rev-parse HEAD)
  [[ $(git -C "$git_e2e_worktree" rev-parse 'HEAD^{tree}') == "$clean_tree" ]]

  run_gate_command cli-pre-commit "$clean_tree" env -C "$git_e2e_worktree/nested" "$gate_bin" hook pre-commit
  run_gate_command pre-push "$clean_tree" "${hooks_env[@]}" git -C "$git_e2e_worktree/nested" push "$git_e2e_remote_name" "HEAD:refs/heads/$git_e2e_branch"
  [[ $(git --git-dir="$git_e2e_remote" rev-parse "refs/heads/$git_e2e_branch") == "$commit" ]]
  printf 'production Git hook E2E: PASS evidence=%s commit=%s tree=%s\n' "$evidence_root" "$commit" "$clean_tree"
}

run_git_cleanup_contract() {
  prepare_git_e2e
  printf '%s\n%s\n%s\n%s\n' \
    "$git_e2e_fixture" "$git_e2e_worktree" "$git_e2e_branch" "$git_e2e_remote_name" \
    >"${GATE_HOOK_E2E_CLEANUP_STATE_FILE:?}"
  case ${GATE_HOOK_E2E_CLEANUP_OUTCOME:-success} in
    success) return 0 ;;
    failure) return 19 ;;
    int) kill -s INT "$$" ;;
    term) kill -s TERM "$$" ;;
    repeat)
      cleanup_git_e2e
      cleanup_git_e2e
      return 0
      ;;
    *) return 2 ;;
  esac
}

run_codex_e2e() {
  local input output
  input="$evidence_root/codex-input.json"
  output="$evidence_root/codex-output.json"
  cat >"$input"
  python3 - "$input" "$repo_root" <<'PY'
import json
import os
import pathlib
import subprocess
import sys

payload = json.loads(pathlib.Path(sys.argv[1]).read_text())
event = payload.get("hook_event_name")
if event not in {"Stop", "SubagentStop"}:
    raise SystemExit("production hook E2E: expected an actual Stop or SubagentStop event")
cwd = payload.get("cwd")
if not isinstance(cwd, str) or not os.path.isabs(cwd):
    raise SystemExit("production hook E2E: lifecycle cwd must be absolute")
root = subprocess.check_output(["git", "-C", cwd, "rev-parse", "--show-toplevel"], text=True).strip()
if os.path.realpath(root) != os.path.realpath(sys.argv[2]):
    raise SystemExit(f"production hook E2E: lifecycle cwd resolved to {root!r}, want active worktree {sys.argv[2]!r}")
agent_id = payload.get("agent_id")
if event == "Stop" and agent_id is not None:
    raise SystemExit("production hook E2E: Stop must not fabricate agent_id")
if event == "SubagentStop" and (not isinstance(agent_id, str) or not agent_id.strip()):
    raise SystemExit("production hook E2E: SubagentStop requires the public agent_id")
PY
  bash "$repo_root/scripts/codex_stop_gate.sh" <"$input" >"$output"
  python3 - "$output" <<'PY'
import json
import pathlib
import sys

raw = pathlib.Path(sys.argv[1]).read_text()
decoder = json.JSONDecoder()
decision, end = decoder.raw_decode(raw)
if raw[end:].strip():
    raise SystemExit("production hook E2E: Codex hook emitted trailing non-JSON output")
if decision.get("continue") is not True:
    raise SystemExit(f"production hook E2E: Codex gate blocked: {decision!r}")
reason = decision.get("reason", "")
for token in ("job=", "receipt=", "source_tree=", "status: super-dolphin-gate status --job"):
    if token not in reason:
        raise SystemExit(f"production hook E2E: passed decision lacks {token!r}: {decision!r}")
PY
  printf 'production Codex hook E2E: PASS evidence=%s\n' "$evidence_root"
}

case "$mode" in
  git) run_git_e2e ;;
  codex) run_codex_e2e ;;
  _cleanup-contract) run_git_cleanup_contract ;;
esac
