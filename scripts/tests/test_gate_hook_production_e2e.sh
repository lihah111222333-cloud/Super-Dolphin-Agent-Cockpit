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
  local fixture worktree remote hooks_env branch bad_tree clean_tree commit
  fixture=$(mktemp -d -t gate-hook-git-e2e.XXXXXX)
  worktree="$fixture/worktree"
  remote="$fixture/remote.git"
  branch="gate-hook-e2e-$(date +%s)-$$"
  cleanup_git_e2e() {
    git -C "$repo_root" worktree remove --force "$worktree" >/dev/null 2>&1 || true
    rm -rf "$fixture"
  }
  trap cleanup_git_e2e EXIT
  git -C "$repo_root" worktree add --detach "$worktree" HEAD
  git init --bare "$remote"
  git -C "$worktree" switch -c "$branch"
  git -C "$worktree" config user.name 'Gate Hook Production E2E'
  git -C "$worktree" config user.email 'gate-hook-e2e@example.invalid'
  git -C "$worktree" remote add e2e "$remote"
  mkdir -p "$worktree/nested"
  hooks_env=(env GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=core.hooksPath GIT_CONFIG_VALUE_0="$repo_root/.githooks")

  printf 'deliberate trailing whitespace \n' >"$worktree/.gate-hook-e2e"
  git -C "$worktree" add .gate-hook-e2e
  bad_tree=$(git -C "$worktree" write-tree)
  run_expected_violation "$bad_tree" "${hooks_env[@]}" git -C "$worktree/nested" commit -m '验证真实门禁违规反馈'

  printf '%s\n' 'production hook E2E' >"$worktree/.gate-hook-e2e"
  git -C "$worktree" add .gate-hook-e2e
  clean_tree=$(git -C "$worktree" write-tree)
  run_gate_command pre-commit "$clean_tree" "${hooks_env[@]}" git -C "$worktree/nested" commit -m '验证真实 Hook 端到端链路'
  commit=$(git -C "$worktree" rev-parse HEAD)
  [[ $(git -C "$worktree" rev-parse 'HEAD^{tree}') == "$clean_tree" ]]

  run_gate_command cli-pre-commit "$clean_tree" env -C "$worktree/nested" "$gate_bin" hook pre-commit
  run_gate_command pre-push "$clean_tree" "${hooks_env[@]}" git -C "$worktree/nested" push e2e "HEAD:refs/heads/$branch"
  [[ $(git --git-dir="$remote" rev-parse "refs/heads/$branch") == "$commit" ]]
  printf 'production Git hook E2E: PASS evidence=%s commit=%s tree=%s\n' "$evidence_root" "$commit" "$clean_tree"
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

if [[ "$mode" == git ]]; then
  run_git_e2e
else
  run_codex_e2e
fi
