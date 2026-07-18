#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)
fixture_root=$(mktemp -d -t gate-hook-contract.XXXXXX)
trap 'rm -rf "$fixture_root"' EXIT

bin_dir="$fixture_root/bin"
capture_dir="$fixture_root/capture"
mkdir -p "$bin_dir" "$capture_dir"

cat >"$bin_dir/super-dolphin-gate" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
: "${GATE_HOOK_CAPTURE_DIR:?}"
printf '%s' "$#" >"$GATE_HOOK_CAPTURE_DIR/argc"
index=0
for argument in "$@"; do
  printf '%s' "$argument" >"$GATE_HOOK_CAPTURE_DIR/arg.$index"
  index=$((index + 1))
done
printf '%s' "$PWD" >"$GATE_HOOK_CAPTURE_DIR/cwd"
if [[ "${GATE_HOOK_CAPTURE_SOURCE:-0}" == 1 ]]; then
  git write-tree >"$GATE_HOOK_CAPTURE_DIR/staged-tree"
fi
cat >"$GATE_HOOK_CAPTURE_DIR/stdin"
if [[ -n "${GATE_HOOK_STDOUT_FILE:-}" ]]; then
  cat "$GATE_HOOK_STDOUT_FILE"
fi
exit "${GATE_HOOK_EXIT_CODE:-0}"
EOF
chmod 0o755 "$bin_dir/super-dolphin-gate" 2>/dev/null || chmod 755 "$bin_dir/super-dolphin-gate"

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

assert_file_equals() {
  local path=$1 expected=$2 label=$3
  local actual
  actual=$(cat "$path")
  [[ "$actual" == "$expected" ]] || fail "$label: got <$actual>, want <$expected>"
}

reset_capture() {
  rm -f "$capture_dir"/*
}

run_with_status() {
  local status_file=$1
  shift
  set +e
  "$@"
  local status=$?
  set -e
  printf '%s' "$status" >"$status_file"
}

export PATH="$bin_dir:/usr/bin:/bin:/usr/sbin:/sbin"
export GATE_HOOK_CAPTURE_DIR="$capture_dir"

git_repo="$fixture_root/repository"
mkdir -p "$git_repo"
git -C "$git_repo" init -q
git -C "$git_repo" config user.name 'Hook Fixture'
git -C "$git_repo" config user.email 'hook-fixture@example.invalid'
printf '%s\n' 'base' >"$git_repo/tracked.txt"
git -C "$git_repo" add tracked.txt
git -C "$git_repo" commit -qm 'fixture base'

reset_capture
clean_tree=$(git -C "$git_repo" write-tree)
(
  cd "$git_repo"
  GATE_HOOK_CAPTURE_SOURCE=1 GATE_HOOK_EXIT_CODE=23 run_with_status \
    "$fixture_root/pre-commit.status" bash "$repo_root/.githooks/pre-commit"
)
assert_file_equals "$fixture_root/pre-commit.status" 23 "pre-commit exit code"
assert_file_equals "$capture_dir/argc" 2 "pre-commit argc"
assert_file_equals "$capture_dir/arg.0" hook "pre-commit arg 0"
assert_file_equals "$capture_dir/arg.1" pre-commit "pre-commit arg 1"
assert_file_equals "$capture_dir/cwd" "$git_repo" "clean pre-commit cwd"
assert_file_equals "$capture_dir/staged-tree" "$clean_tree" "clean pre-commit staged tree"
[[ ! -s "$capture_dir/stdin" ]] || fail "pre-commit forwarded unexpected stdin"

printf '%s\n' 'staged' >"$git_repo/tracked.txt"
git -C "$git_repo" add tracked.txt
staged_tree=$(git -C "$git_repo" write-tree)
printf '%s\n' 'unstaged' >>"$git_repo/tracked.txt"
reset_capture
(
  cd "$git_repo"
  GATE_HOOK_CAPTURE_SOURCE=1 run_with_status \
    "$fixture_root/staged-pre-commit.status" bash "$repo_root/.githooks/pre-commit"
)
assert_file_equals "$fixture_root/staged-pre-commit.status" 0 "staged pre-commit exit code"
assert_file_equals "$capture_dir/cwd" "$git_repo" "staged pre-commit cwd"
assert_file_equals "$capture_dir/staged-tree" "$staged_tree" "staged pre-commit tree"
git -C "$git_repo" diff --quiet -- tracked.txt && fail "staged pre-commit discarded the unstaged worktree change"

reset_capture
push_input="$fixture_root/pre-push.stdin"
printf '%s\n' \
  'refs/heads/main 1111111111111111111111111111111111111111 refs/heads/main 2222222222222222222222222222222222222222' \
  'refs/heads/topic 3333333333333333333333333333333333333333 refs/heads/topic 0000000000000000000000000000000000000000' >"$push_input"
GATE_HOOK_EXIT_CODE=29 run_with_status "$fixture_root/pre-push.status" \
  bash "$repo_root/.githooks/pre-push" 'upstream' 'ssh://git@example.invalid/team/repo.git' <"$push_input"
assert_file_equals "$fixture_root/pre-push.status" 29 "pre-push exit code"
assert_file_equals "$capture_dir/argc" 4 "pre-push argc"
assert_file_equals "$capture_dir/arg.0" hook "pre-push arg 0"
assert_file_equals "$capture_dir/arg.1" pre-push "pre-push arg 1"
assert_file_equals "$capture_dir/arg.2" upstream "pre-push remote name"
assert_file_equals "$capture_dir/arg.3" 'ssh://git@example.invalid/team/repo.git' "pre-push remote URL"
cmp -s "$push_input" "$capture_dir/stdin" || fail "pre-push stdin was not forwarded byte-for-byte"

reset_capture
codex_input="$fixture_root/codex.stdin"
codex_output="$fixture_root/codex.expected"
printf '%s\n' '{"session_id":"session-1","turn_id":"turn-1","cwd":"/tmp/repo","hook_event_name":"Stop","permission_mode":"default","stop_hook_active":false}' >"$codex_input"
printf '%s\n' '{"decision":"block","reason":"fixture decision"}' >"$codex_output"
GATE_HOOK_STDOUT_FILE="$codex_output" run_with_status "$fixture_root/codex.status" \
  bash "$repo_root/scripts/codex_stop_gate.sh" <"$codex_input" >"$fixture_root/codex.actual"
assert_file_equals "$fixture_root/codex.status" 0 "Codex exit code"
assert_file_equals "$capture_dir/argc" 2 "Codex argc"
assert_file_equals "$capture_dir/arg.0" hook "Codex arg 0"
assert_file_equals "$capture_dir/arg.1" codex "Codex arg 1"
cmp -s "$codex_input" "$capture_dir/stdin" || fail "Codex lifecycle JSON was not forwarded byte-for-byte"
cmp -s "$codex_output" "$fixture_root/codex.actual" || fail "Codex decision JSON was not forwarded byte-for-byte"

reset_capture
subagent_input="$fixture_root/subagent-stop.stdin"
printf '%s\n' '{"session_id":"session-1","turn_id":"turn-2","cwd":"/tmp/repo","hook_event_name":"SubagentStop","permission_mode":"plan","stop_hook_active":false,"agent_id":"agent-7"}' >"$subagent_input"
GATE_HOOK_STDOUT_FILE="$codex_output" run_with_status "$fixture_root/subagent-stop.status" \
  bash "$repo_root/scripts/codex_stop_gate.sh" <"$subagent_input" >"$fixture_root/subagent-stop.actual"
assert_file_equals "$fixture_root/subagent-stop.status" 0 "SubagentStop exit code"
assert_file_equals "$capture_dir/argc" 2 "SubagentStop argc"
assert_file_equals "$capture_dir/arg.0" hook "SubagentStop arg 0"
assert_file_equals "$capture_dir/arg.1" codex "SubagentStop arg 1"
cmp -s "$subagent_input" "$capture_dir/stdin" || fail "SubagentStop lifecycle JSON was not forwarded byte-for-byte"
cmp -s "$codex_output" "$fixture_root/subagent-stop.actual" || fail "SubagentStop decision JSON was not forwarded byte-for-byte"

missing_path=/usr/bin:/bin:/usr/sbin:/sbin
set +e
PATH=$missing_path bash "$repo_root/.githooks/pre-commit" >/dev/null 2>"$fixture_root/missing-pre-commit.err"
missing_commit_status=$?
PATH=$missing_path bash "$repo_root/.githooks/pre-push" origin https://example.invalid/repo.git </dev/null >/dev/null 2>"$fixture_root/missing-pre-push.err"
missing_push_status=$?
PATH=$missing_path bash "$repo_root/scripts/codex_stop_gate.sh" <"$codex_input" >"$fixture_root/missing-codex.json"
missing_codex_status=$?
set -e
[[ $missing_commit_status -ne 0 ]] || fail "pre-commit accepted a missing CLI"
[[ $missing_push_status -ne 0 ]] || fail "pre-push accepted a missing CLI"
[[ $missing_codex_status -eq 0 ]] || fail "Codex missing-CLI decision must exit zero"
python3 - "$fixture_root/missing-codex.json" <<'PY'
import json
import pathlib
import sys

decision = json.loads(pathlib.Path(sys.argv[1]).read_text())
if decision.get("decision") != "block" or "not installed" not in decision.get("reason", ""):
    raise SystemExit(f"invalid missing-CLI Codex decision: {decision!r}")
PY

for entrypoint in \
  "$repo_root/.githooks/pre-commit" \
  "$repo_root/.githooks/pre-push" \
  "$repo_root/scripts/codex_stop_gate.sh"; do
  if grep -Eq '(^|[^[:alnum:]_])(go|npm|npx|make)([^[:alnum:]_]|$)|go[[:space:]]+run' "$entrypoint"; then
    fail "$entrypoint contains a forbidden host gate command"
  fi
done

python3 - "$repo_root/.codex/hooks.json" <<'PY'
import json
import pathlib
import sys

document = json.loads(pathlib.Path(sys.argv[1]).read_text())
for event in ("Stop", "SubagentStop"):
    command = document["hooks"][event][0]["hooks"][0]["command"]
    if "scripts/codex_stop_gate.sh" not in command:
        raise SystemExit(f"{event} does not call the installed gate CLI launcher: {command!r}")
PY

production_e2e="$repo_root/scripts/tests/test_gate_hook_production_e2e.sh"
[[ -x "$production_e2e" ]] || fail "production hook E2E driver is not executable"
grep -Fq 'git -C "$worktree/nested" commit' "$production_e2e" || fail "production E2E does not invoke Git commit"
grep -Fq 'git -C "$worktree/nested" push' "$production_e2e" || fail "production E2E does not invoke Git push"
grep -Fq 'branch -D "$branch"' "$production_e2e" || fail "production E2E leaks its temporary branch"
grep -Fq 'scripts/codex_stop_gate.sh' "$production_e2e" || fail "production E2E bypasses the Codex thin entrypoint"
if grep -Eq 'fake|mock|recordingHookCoordinator|provision production' "$production_e2e"; then
  fail "production hook E2E contains a fixture or provisioning bypass"
fi

printf '%s\n' 'gate hook entrypoint contracts: PASS'
