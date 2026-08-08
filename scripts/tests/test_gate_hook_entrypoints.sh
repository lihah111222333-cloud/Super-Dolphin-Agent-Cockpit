#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)
fixture_root=$(mktemp -d -t gate-hook-contract.XXXXXX)
fixture_root=$(cd "$fixture_root" && pwd -P)
source "$repo_root/.githooks/trusted-gate-launcher.sh"
real_os_home=$(trusted_launcher_os_home) || {
  printf 'FAIL: operating-system home directory could not be resolved\n' >&2
  exit 1
}
test_home=$(mktemp -d "$real_os_home/.gate-hook-contract-home.XXXXXX")
chmod 0700 "$test_home"
fake_os_bin="$fixture_root/os-bin"
mkdir -m 0700 "$fake_os_bin"
if [[ "$(uname -s)" == "Darwin" ]]; then
  printf '#!/usr/bin/env bash\nprintf '\''NFSHomeDirectory: %s\\n'\''\n' "$test_home" >"$fake_os_bin/dscl"
else
  printf '#!/usr/bin/env bash\nif [[ "${1:-}" == passwd ]]; then printf '\''fixture:x:0:0::%s:/bin/sh\\n'\''; fi\n' "$test_home" >"$fake_os_bin/getent"
fi
chmod 0700 "$fake_os_bin"/*
export PATH="$fake_os_bin:$PATH"
install_root="$test_home/.super-dolphin-gate-launchers"
mkdir -m 0700 "$install_root"
trap 'rm -rf -- "$test_home" "$fixture_root"' EXIT

bin_dir="$fixture_root/bin"
capture_dir="$fixture_root/capture"
mkdir -p "$bin_dir" "$capture_dir"

cat >"$bin_dir/super-dolphin-gate" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "launcher" && "${2:-}" == "verify" ]]; then
  [[ "$#" -eq 8 && "$3" == --repository && "$5" == --tree && "$7" == --receipt ]] || exit 65
  # Linked worktrees have a .git file, so query Git instead of checking .git as a directory.
  [[ "$(git -C "$4" rev-parse --is-inside-work-tree 2>/dev/null)" == true && "$6" =~ ^([0-9a-f]{40}|[0-9a-f]{64})$ ]] || exit 65
  [[ "$8" == "$(dirname "$0")/receipt.json" && -f "$8" ]] || exit 65
  [[ "$(cat "$8")" == "fixture-tree=$6" ]] || exit 65
  exit 0
fi
if [[ "${1:-}" == "closure" && "${2:-}" == "provenance" ]]; then
  [[ "${3:-}" == --tree && -n "${4:-}" ]] || exit 65
  printf '%s' "${4:-}" >"$GATE_HOOK_CAPTURE_DIR/closure-provenance-tree"
  if [[ "${GATE_HOOK_CLOSURE_PROVENANCE_MISMATCH:-0}" == 1 ]]; then
    printf 'sha256:%064d sha256:%064d\n' 0 1
  else
    printf 'sha256:%064d sha256:%064d\n' 0 0
  fi
  exit 0
fi
if [[ "${1:-}" == "closure" && "${2:-}" == "check" ]]; then
  if [[ "${GATE_HOOK_CLOSURE_DRIFT_ONCE:-0}" == 1 && ! -f "$GATE_HOOK_CAPTURE_DIR/closure-refreshed" ]]; then
    exit 12
  fi
  printf '%s' "${4:-}" >"$GATE_HOOK_CAPTURE_DIR/closure-check-tree"
  exit 0
fi
if [[ "${1:-}" == "closure" && "${2:-}" == "refresh-dependencies" ]]; then
  repository=$(git rev-parse --show-toplevel)
  printf '%s\n' '{"schema_version":"generated-runtime-deps"}' >"$repository/build/gate/runtime-deps.lock"
  printf '%s' "${4:-}" >"$GATE_HOOK_CAPTURE_DIR/closure-dependency-refresh-tree"
  exit 0
fi
if [[ "${1:-}" == "closure" && "${2:-}" == "refresh" ]]; then
  repository=$(git rev-parse --show-toplevel)
  printf '%s\n' 'generated Dockerfile' >"$repository/build/gate/Dockerfile"
  printf '%s\n' '{"schema_version":"test"}' >"$repository/build/gate/inputs.json"
  printf '%s' "${4:-}" >"$GATE_HOOK_CAPTURE_DIR/closure-refresh-tree"
  : >"$GATE_HOOK_CAPTURE_DIR/closure-refreshed"
  exit 0
fi
if [[ "${1:-}" == "frontend-code-size" && "${2:-}" == "check" ]]; then
  [[ "${3:-}" == --tree && -n "${4:-}" && "${5:-}" == --accepted-tree && -n "${6:-}" ]] || exit 65
  if [[ ! -e "$GATE_HOOK_CAPTURE_DIR/frontend-code-size-initial-check-tree" ]]; then
    printf '%s' "$4" >"$GATE_HOOK_CAPTURE_DIR/frontend-code-size-initial-check-tree"
  fi
  printf '%s' "$4" >"$GATE_HOOK_CAPTURE_DIR/frontend-code-size-check-tree"
  if [[ "${GATE_HOOK_FRONTEND_CODE_SIZE_ALWAYS_DRIFT:-0}" == 1 ]]; then
    exit 12
  fi
  if [[ "${GATE_HOOK_FRONTEND_CODE_SIZE_DRIFT_ONCE:-0}" == 1 && ! -f "$GATE_HOOK_CAPTURE_DIR/frontend-code-size-refreshed" ]]; then
    exit 12
  fi
  exit 0
fi
if [[ "${1:-}" == "frontend-code-size" && "${2:-}" == "refresh" ]]; then
  [[ "${3:-}" == --tree && -n "${4:-}" && "${5:-}" == --accepted-tree && -n "${6:-}" ]] || exit 65
  repository=$(git rev-parse --show-toplevel)
  printf '%s\n' '{"scope":"production","generated":true}' >"$repository/frontend-app/.frontend_code_size_guard_baseline.json"
  printf '%s\n' '{"scope":"test","generated":true}' >"$repository/frontend-app/.frontend_code_size_guard_baseline_test.json"
  printf '%s' "$4" >"$GATE_HOOK_CAPTURE_DIR/frontend-code-size-refresh-tree"
  : >"$GATE_HOOK_CAPTURE_DIR/frontend-code-size-refreshed"
	exit 0
fi
if [[ "${1:-}" == "codemap" && "${2:-}" == "check" ]]; then
  [[ "${3:-}" == --tree && -n "${4:-}" ]] || exit 65
  exit 0
fi
if [[ "${1:-}" == "codemap" && "${2:-}" == "refresh" ]]; then
  [[ "${3:-}" == --tree && -n "${4:-}" ]] || exit 65
  exit 0
fi
if [[ "${1:-}" == "capability-contract" && "${2:-}" == "check" ]]; then
  [[ "${3:-}" == --tree && -n "${4:-}" ]] || exit 65
  if [[ ! -e "$GATE_HOOK_CAPTURE_DIR/capability-contract-initial-check-tree" ]]; then
    printf '%s' "$4" >"$GATE_HOOK_CAPTURE_DIR/capability-contract-initial-check-tree"
  fi
  printf '%s' "$4" >"$GATE_HOOK_CAPTURE_DIR/capability-contract-check-tree"
  if [[ "${GATE_HOOK_CAPABILITY_CONTRACT_ALWAYS_DRIFT:-0}" == 1 ]]; then
    exit 12
  fi
  if [[ "${GATE_HOOK_CAPABILITY_CONTRACT_DRIFT_ONCE:-0}" == 1 && ! -f "$GATE_HOOK_CAPTURE_DIR/capability-contract-refreshed" ]]; then
    exit 12
  fi
  exit 0
fi
if [[ "${1:-}" == "capability-contract" && "${2:-}" == "refresh" ]]; then
  [[ "${3:-}" == --tree && -n "${4:-}" ]] || exit 65
  repository=$(git rev-parse --show-toplevel)
  mkdir -p "$repository/docs/doc/codemap/capability-contract"
  printf '%s\n' 'generated capability manifest' >"$repository/docs/doc/codemap/capability-contract/capability_manifest.json"
  printf '%s' "$4" >"$GATE_HOOK_CAPTURE_DIR/capability-contract-refresh-tree"
  : >"$GATE_HOOK_CAPTURE_DIR/capability-contract-refreshed"
  exit 0
fi
if [[ "${1:-}" == "project-map" && "${2:-}" == "check" ]]; then
  [[ "${3:-}" == --tree && -n "${4:-}" ]] || exit 65
  if [[ ! -e "$GATE_HOOK_CAPTURE_DIR/project-map-initial-check-tree" ]]; then
    printf '%s' "$4" >"$GATE_HOOK_CAPTURE_DIR/project-map-initial-check-tree"
  fi
  printf '%s' "$4" >"$GATE_HOOK_CAPTURE_DIR/project-map-check-tree"
  if [[ "${GATE_HOOK_PROJECT_MAP_ALWAYS_DRIFT:-0}" == 1 ]]; then
    exit 12
  fi
  if [[ "${GATE_HOOK_PROJECT_MAP_DRIFT_ONCE:-0}" == 1 && ! -f "$GATE_HOOK_CAPTURE_DIR/project-map-refreshed" ]]; then
    exit 12
  fi
  exit 0
fi
if [[ "${1:-}" == "project-map" && "${2:-}" == "refresh" ]]; then
  [[ "${3:-}" == --tree && -n "${4:-}" ]] || exit 65
  repository=$(git rev-parse --show-toplevel)
  rm -rf -- "$repository/docs/doc/codemap/project-map"
  mkdir -p "$repository/docs/doc/codemap/project-map"
  printf '%s\n' 'generated project map' >"$repository/docs/doc/codemap/project-map/AI_PROJECT_MAP.md"
  printf '%s' "$4" >"$GATE_HOOK_CAPTURE_DIR/project-map-refresh-tree"
  : >"$GATE_HOOK_CAPTURE_DIR/project-map-refreshed"
  exit 0
fi
: "${GATE_HOOK_CAPTURE_DIR:?}"
printf '%s' "$0" >"$GATE_HOOK_CAPTURE_DIR/launcher"
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
if [[ -n "${GATE_HOOK_STDERR_FILE:-}" ]]; then
  cat "$GATE_HOOK_STDERR_FILE" >&2
fi
exit "${GATE_HOOK_EXIT_CODE:-0}"
EOF
chmod 0o755 "$bin_dir/super-dolphin-gate" 2>/dev/null || chmod 755 "$bin_dir/super-dolphin-gate"
for forbidden_entrypoint in node make; do
  cat >"$bin_dir/$forbidden_entrypoint" <<EOF
#!/usr/bin/env bash
: >"$capture_dir/$forbidden_entrypoint-executed"
exit 97
EOF
  chmod 0o755 "$bin_dir/$forbidden_entrypoint" 2>/dev/null || chmod 755 "$bin_dir/$forbidden_entrypoint"
done
gate_git=${SUPER_DOLPHIN_GATE_GIT:-}
if [[ -z "$gate_git" ]]; then
  gate_git=$(command -v git)
fi
[[ -x "$gate_git" ]] || {
  printf 'FAIL: controlled git executable is unavailable\n' >&2
  exit 1
}
ln -s "$gate_git" "$bin_dir/git"
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
  provision_fixture_launchers
  set +e
  "$@"
  local status=$?
  set -e
  printf '%s' "$status" >"$status_file"
}

fixture_launcher_for_tree() {
  local repository=$1 tree=$2 digest launcher receipt
  digest=$(shasum -a 256 "$bin_dir/super-dolphin-gate" | awk '{print $1}')
  launcher="$install_root/v1/$tree/$digest/super-dolphin-gate"
  receipt="$(dirname "$launcher")/receipt.json"
  mkdir -p "$(dirname "$launcher")"
  if [[ ! -e "$launcher" ]]; then
    cp "$bin_dir/super-dolphin-gate" "$launcher"
    chmod 0500 "$launcher"
  fi
  if [[ ! -e "$receipt" ]]; then
    printf 'fixture-tree=%s\n' "$tree" >"$receipt"
    chmod 0400 "$receipt"
  fi
  git -C "$repository" config superdolphin.gateLauncherRoot "$install_root"
  printf '%s\n' "$launcher"
}

provision_fixture_launchers() {
  local repository tree commit launcher
  repository=$(git rev-parse --show-toplevel 2>/dev/null || true)
  [[ -n "$repository" ]] || return 0
  tree=$(git -C "$repository" write-tree 2>/dev/null || true)
  if [[ -n "$tree" ]]; then
    launcher=$(fixture_launcher_for_tree "$repository" "$tree")
    git -C "$repository" config superdolphin.gateLauncher "$launcher"
  fi
  while IFS= read -r commit; do
    [[ -n "$commit" ]] || continue
    tree=$(git -C "$repository" rev-parse --verify "${commit}^{tree}")
    fixture_launcher_for_tree "$repository" "$tree" >/dev/null
  done < <(git -C "$repository" for-each-ref --format='%(objectname)' refs/heads)
}

export PATH="$bin_dir:$fake_os_bin:/usr/bin:/bin:/usr/sbin:/sbin"
export GATE_HOOK_CAPTURE_DIR="$capture_dir"
export GATE_HOOK_FIXTURE_GATE_BIN="$bin_dir/super-dolphin-gate"
export SUPER_DOLPHIN_CI_AGENT_TOKEN=fixture-agent-token

git_repo="$fixture_root/repository"
mkdir -p "$git_repo"
git -C "$git_repo" init -q
git -C "$git_repo" config user.name 'Hook Fixture'
git -C "$git_repo" config user.email 'hook-fixture@example.invalid'
remote_config="$fixture_root/remote-ci.json"
remote_ledger="$fixture_root/ci-duration-ledger.sqlite"
git -C "$git_repo" config super-dolphin.remote.config "$remote_config"
git -C "$git_repo" config super-dolphin.remote.ledger "$remote_ledger"
git -C "$git_repo" config super-dolphin.remote.maxShards 1
mkdir -p "$git_repo/.githooks"
mkdir -p "$git_repo/build/gate"
mkdir -p "$git_repo/frontend-app" "$git_repo/scripts" "$git_repo/docs/doc/codemap/project-map"
mkdir -p "$git_repo/docs/doc/codemap/capability-contract"
cp "$repo_root/.githooks/trusted-gate-launcher.sh" "$git_repo/.githooks/trusted-gate-launcher.sh"
cat >"$git_repo/scripts/build-trusted-gate-launcher.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
tree=${1:?exact tree is required}
repository=$(git rev-parse --show-toplevel)
install_root=$(git -C "$repository" config --local --get superdolphin.gateLauncherRoot)
digest=$(shasum -a 256 "$GATE_HOOK_FIXTURE_GATE_BIN" | awk '{print $1}')
launcher="$install_root/v1/$tree/$digest/super-dolphin-gate"
mkdir -p "$(dirname "$launcher")"
cp "$GATE_HOOK_FIXTURE_GATE_BIN" "$launcher"
chmod 0500 "$launcher"
printf 'fixture-tree=%s\n' "$tree" >"$(dirname "$launcher")/receipt.json"
chmod 0400 "$(dirname "$launcher")/receipt.json"
printf '%s\n' "$launcher"
EOF
chmod 0755 "$git_repo/scripts/build-trusted-gate-launcher.sh"
printf '%s\n' 'base' >"$git_repo/tracked.txt"
printf '%s\n' 'stale Dockerfile' >"$git_repo/build/gate/Dockerfile"
printf '%s\n' '{"schema_version":"stale"}' >"$git_repo/build/gate/inputs.json"
printf '%s\n' '{"schema_version":"stale-runtime-deps"}' >"$git_repo/build/gate/runtime-deps.lock"
printf '%s\n' '{"scope":"production","generated":false}' >"$git_repo/frontend-app/.frontend_code_size_guard_baseline.json"
printf '%s\n' '{"scope":"test","generated":false}' >"$git_repo/frontend-app/.frontend_code_size_guard_baseline_test.json"
printf '%s\n' 'trusted generator' >"$git_repo/scripts/generate_ai_project_map.mjs"
printf '%s\n' 'stale project map' >"$git_repo/docs/doc/codemap/project-map/AI_PROJECT_MAP.md"
printf '%s\n' 'stale capability manifest' >"$git_repo/docs/doc/codemap/capability-contract/capability_manifest.json"
git -C "$git_repo" add tracked.txt .githooks/trusted-gate-launcher.sh build/gate/Dockerfile build/gate/inputs.json build/gate/runtime-deps.lock frontend-app/.frontend_code_size_guard_baseline.json frontend-app/.frontend_code_size_guard_baseline_test.json scripts/build-trusted-gate-launcher.sh scripts/generate_ai_project_map.mjs docs/doc/codemap/project-map/AI_PROJECT_MAP.md docs/doc/codemap/capability-contract/capability_manifest.json
git -C "$git_repo" commit -qm 'fixture base'
mkdir -p "$git_repo/nested"
source "$git_repo/.githooks/trusted-gate-launcher.sh"
fixture_launcher=$(fixture_launcher_for_tree "$git_repo" "$(git -C "$git_repo" write-tree)")
git -C "$git_repo" config superdolphin.gateLauncher "$fixture_launcher"
if trusted_gate_launcher "$git_repo" >/dev/null 2>&1; then
  :
else
  fail 'valid fixture launcher receipt was rejected'
fi
chmod 0600 "$(dirname "$fixture_launcher")/receipt.json"
printf '%s\n' 'fixture-tree=tampered' >"$(dirname "$fixture_launcher")/receipt.json"
chmod 0400 "$(dirname "$fixture_launcher")/receipt.json"
if trusted_gate_launcher "$git_repo" >/dev/null 2>&1; then
  fail 'tampered fixture launcher receipt passed verification'
fi
chmod 0600 "$(dirname "$fixture_launcher")/receipt.json"
printf 'fixture-tree=%s\n' "$(git -C "$git_repo" write-tree)" >"$(dirname "$fixture_launcher")/receipt.json"
chmod 0400 "$(dirname "$fixture_launcher")/receipt.json"
missing_fixture_tree=$(printf '%040d' 1)
if trusted_gate_launcher_for_tree "$git_repo" "$missing_fixture_tree" >/dev/null 2>&1; then
  fail 'missing exact-tree fixture launcher passed verification'
fi

printf '%s\n' 'initial exact-tree bind' >>"$git_repo/tracked.txt"
git -C "$git_repo" add tracked.txt
initial_bind_tree=$(git -C "$git_repo" write-tree)
rm -rf -- "$install_root/v1/$initial_bind_tree"
reset_capture
set +e
(
  cd "$git_repo/nested"
  GATE_HOOK_CAPTURE_SOURCE=1 bash "$repo_root/.githooks/pre-commit" \
    >"$fixture_root/initial-bind-pre-commit.out" 2>"$fixture_root/initial-bind-pre-commit.err"
)
initial_bind_status=$?
set -e
[[ "$initial_bind_status" -eq 0 ]] || fail "initial exact-tree launcher bind failed with status $initial_bind_status"
grep -Fq 'staged tree has no exact launcher; binding trusted launcher automatically' "$fixture_root/initial-bind-pre-commit.err" \
  || fail 'initial exact-tree launcher bind was not automatic'
trusted_gate_launcher_for_tree "$git_repo" "$initial_bind_tree" >/dev/null \
  || fail 'automatic initial bind did not install a verified exact-tree launcher'
assert_file_equals "$capture_dir/staged-tree" "$initial_bind_tree" "initial exact-tree bind remote gate tree"
git -C "$git_repo" restore --staged --worktree -- tracked.txt
cli_error="$fixture_root/cli-error.expected"
printf '%s\n' 'fixture coordinator failure; job=job-23; status: super-dolphin-gate status --job job-23' >"$cli_error"

reset_capture
clean_tree=$(git -C "$git_repo" write-tree)
(
  cd "$git_repo/nested"
  GATE_HOOK_CAPTURE_SOURCE=1 GATE_HOOK_STDERR_FILE="$cli_error" GATE_HOOK_EXIT_CODE=23 run_with_status \
    "$fixture_root/pre-commit.status" bash "$repo_root/.githooks/pre-commit" >"$fixture_root/pre-commit.out" 2>"$fixture_root/pre-commit.err"
)
assert_file_equals "$fixture_root/pre-commit.status" 23 "pre-commit exit code"
assert_file_equals "$capture_dir/argc" 13 "pre-commit argc without shard cap"
assert_file_equals "$capture_dir/arg.0" remote "pre-commit arg 0"
assert_file_equals "$capture_dir/arg.1" hook "pre-commit arg 1"
assert_file_equals "$capture_dir/arg.2" pre-commit "pre-commit arg 2"
assert_file_equals "$capture_dir/arg.3" --config "pre-commit config flag"
assert_file_equals "$capture_dir/arg.4" "$remote_config" "pre-commit config path"
assert_file_equals "$capture_dir/arg.5" --ledger "pre-commit ledger flag"
assert_file_equals "$capture_dir/arg.6" "$remote_ledger" "pre-commit ledger path"
assert_file_equals "$capture_dir/arg.7" --repository "pre-commit repository flag"
assert_file_equals "$capture_dir/arg.8" "$git_repo" "pre-commit repository"
assert_file_equals "$capture_dir/arg.9" --tree "pre-commit tree flag"
assert_file_equals "$capture_dir/arg.10" "$clean_tree" "pre-commit immutable tree"
assert_file_equals "$capture_dir/arg.11" --parent "pre-commit parent flag"
assert_file_equals "$capture_dir/arg.12" "$(git -C "$git_repo" rev-parse HEAD)" "pre-commit parent commit"
assert_file_equals "$capture_dir/cwd" "$git_repo/nested" "clean pre-commit cwd"
assert_file_equals "$capture_dir/staged-tree" "$clean_tree" "clean pre-commit staged tree"
assert_file_equals "$capture_dir/project-map-check-tree" "$clean_tree" "clean project-map staged tree"
cmp -s "$cli_error" "$fixture_root/pre-commit.out" || fail "pre-commit did not stream readable CLI output"
[[ ! -s "$capture_dir/stdin" ]] || fail "pre-commit forwarded unexpected stdin"

printf '%s\n' 'staged' >"$git_repo/tracked.txt"
git -C "$git_repo" add tracked.txt
staged_tree=$(git -C "$git_repo" write-tree)
printf '%s\n' 'unstaged' >>"$git_repo/tracked.txt"
reset_capture
(
  cd "$git_repo/nested"
  GATE_HOOK_CAPTURE_SOURCE=1 run_with_status \
    "$fixture_root/staged-pre-commit.status" bash "$repo_root/.githooks/pre-commit"
)
assert_file_equals "$fixture_root/staged-pre-commit.status" 0 "staged pre-commit exit code"
assert_file_equals "$capture_dir/cwd" "$git_repo/nested" "staged pre-commit cwd"
assert_file_equals "$capture_dir/staged-tree" "$staged_tree" "staged pre-commit tree"
git -C "$git_repo" diff --quiet -- tracked.txt && fail "staged pre-commit discarded the unstaged worktree change"

for closure_output in build/gate/Dockerfile build/gate/inputs.json build/gate/runtime-deps.lock; do
  git -C "$git_repo" restore --staged --worktree -- build/gate/Dockerfile build/gate/inputs.json build/gate/runtime-deps.lock
  printf '%s\n' 'staged closure output' >"$git_repo/$closure_output"
  git -C "$git_repo" add -- "$closure_output"
  printf '%s\n' 'unstaged closure output' >>"$git_repo/$closure_output"
  reset_capture
  (
    cd "$git_repo/nested"
    run_with_status "$fixture_root/unstaged-${closure_output##*/}.status" bash "$repo_root/.githooks/pre-commit" \
      2>"$fixture_root/unstaged-${closure_output##*/}.err"
  )
  assert_file_equals "$fixture_root/unstaged-${closure_output##*/}.status" 1 "unstaged $closure_output pre-commit exit code"
  grep -Fq 'gate-image closure outputs have unstaged changes' "$fixture_root/unstaged-${closure_output##*/}.err" || fail "unstaged $closure_output did not fail fast"
  [[ ! -e "$capture_dir/closure-check-tree" ]] || fail "unstaged $closure_output invoked closure check"
  [[ ! -e "$capture_dir/argc" ]] || fail "unstaged $closure_output invoked gate execution"
  git -C "$git_repo" diff --quiet -- "$closure_output" && fail "unstaged $closure_output was overwritten"
done
git -C "$git_repo" restore --staged --worktree -- build/gate/Dockerfile build/gate/inputs.json build/gate/runtime-deps.lock

closure_provenance_tree=$(git -C "$git_repo" write-tree)
reset_capture
(
  cd "$git_repo/nested"
  GATE_HOOK_CLOSURE_PROVENANCE_MISMATCH=1 run_with_status \
    "$fixture_root/closure-provenance-mismatch.status" bash "$repo_root/.githooks/pre-commit" \
    2>"$fixture_root/closure-provenance-mismatch.err"
)
assert_file_equals "$fixture_root/closure-provenance-mismatch.status" 1 "stale closure launcher pre-commit exit code"
grep -Fq 'closure-generator provenance does not match the staged tree' "$fixture_root/closure-provenance-mismatch.err" || fail "stale closure launcher did not fail closed"
assert_file_equals "$capture_dir/closure-provenance-tree" "$closure_provenance_tree" "closure provenance source tree"
[[ ! -e "$capture_dir/closure-check-tree" ]] || fail "stale closure launcher reached closure check"
[[ ! -e "$capture_dir/closure-refresh-tree" ]] || fail "stale closure launcher wrote closure outputs"

closure_drift_tree=$(git -C "$git_repo" write-tree)
reset_capture
(
  cd "$git_repo/nested"
  GATE_HOOK_CLOSURE_DRIFT_ONCE=1 GATE_HOOK_CAPTURE_SOURCE=1 run_with_status \
    "$fixture_root/closure-refresh-pre-commit.status" bash "$repo_root/.githooks/pre-commit" \
    2>"$fixture_root/closure-refresh-pre-commit.err"
)
assert_file_equals "$fixture_root/closure-refresh-pre-commit.status" 0 "closure refresh pre-commit continues after exact-tree launcher rebind"
grep -Fq 'rebinding trusted launcher once' "$fixture_root/closure-refresh-pre-commit.err" || fail "closure refresh did not rebind the exact-tree launcher"
assert_file_equals "$capture_dir/closure-provenance-tree" "$closure_drift_tree" "matching closure launcher provenance source tree"
assert_file_equals "$capture_dir/closure-dependency-refresh-tree" "$closure_drift_tree" "closure dependency refresh source tree"
dependency_refreshed_tree=$(cat "$capture_dir/closure-refresh-tree")
[[ "$dependency_refreshed_tree" != "$closure_drift_tree" ]] || fail "closure refresh did not receive the dependency-refreshed tree"
refreshed_tree=$(git -C "$git_repo" write-tree)
[[ "$refreshed_tree" != "$closure_drift_tree" ]] || fail "closure refresh did not update the staged tree"
[[ "$refreshed_tree" != "$dependency_refreshed_tree" ]] || fail "closure output refresh did not update the dependency-refreshed tree"
assert_file_equals "$capture_dir/closure-check-tree" "$refreshed_tree" "closure refreshed verification tree"
assert_file_equals "$capture_dir/staged-tree" "$refreshed_tree" "closure refresh remote gate tree"
assert_file_equals "$git_repo/build/gate/Dockerfile" 'generated Dockerfile' "closure refreshed Dockerfile"
assert_file_equals "$git_repo/build/gate/inputs.json" '{"schema_version":"test"}' "closure refreshed input manifest"
assert_file_equals "$git_repo/build/gate/runtime-deps.lock" '{"schema_version":"generated-runtime-deps"}' "closure refreshed runtime dependency lock"
git -C "$git_repo" diff --quiet -- tracked.txt && fail "closure refresh discarded the unstaged worktree change"

frontend_code_size_drift_tree=$(git -C "$git_repo" write-tree)
reset_capture
(
  cd "$git_repo/nested"
  GATE_HOOK_FRONTEND_CODE_SIZE_DRIFT_ONCE=1 GATE_HOOK_CAPTURE_SOURCE=1 run_with_status \
    "$fixture_root/frontend-code-size-refresh-pre-commit.status" bash "$repo_root/.githooks/pre-commit" \
    2>"$fixture_root/frontend-code-size-refresh-pre-commit.err"
)
assert_file_equals "$fixture_root/frontend-code-size-refresh-pre-commit.status" 0 "frontend code-size refresh continues after exact-tree launcher rebind"
grep -Fq 'rebinding trusted launcher once' "$fixture_root/frontend-code-size-refresh-pre-commit.err" || fail "frontend code-size refresh did not rebind the exact-tree launcher"
assert_file_equals "$capture_dir/frontend-code-size-initial-check-tree" "$frontend_code_size_drift_tree" "frontend code-size initial check tree"
assert_file_equals "$capture_dir/frontend-code-size-refresh-tree" "$frontend_code_size_drift_tree" "frontend code-size refresh source tree"
frontend_code_size_refreshed_tree=$(git -C "$git_repo" write-tree)
[[ "$frontend_code_size_refreshed_tree" != "$frontend_code_size_drift_tree" ]] || fail "frontend code-size refresh did not update the staged tree"
assert_file_equals "$capture_dir/frontend-code-size-check-tree" "$frontend_code_size_refreshed_tree" "frontend code-size refreshed verification tree"
assert_file_equals "$capture_dir/staged-tree" "$frontend_code_size_refreshed_tree" "frontend code-size refresh remote gate tree"
assert_file_equals "$git_repo/frontend-app/.frontend_code_size_guard_baseline.json" '{"scope":"production","generated":true}' "frontend production baseline refresh"
assert_file_equals "$git_repo/frontend-app/.frontend_code_size_guard_baseline_test.json" '{"scope":"test","generated":true}' "frontend test baseline refresh"
git -C "$git_repo" diff --quiet -- tracked.txt && fail "frontend code-size refresh discarded the unstaged worktree change"

capability_contract_drift_tree=$(git -C "$git_repo" write-tree)
reset_capture
(
  cd "$git_repo/nested"
  GATE_HOOK_CAPABILITY_CONTRACT_DRIFT_ONCE=1 GATE_HOOK_CAPTURE_SOURCE=1 run_with_status \
    "$fixture_root/capability-contract-refresh-pre-commit.status" bash "$repo_root/.githooks/pre-commit" \
    2>"$fixture_root/capability-contract-refresh-pre-commit.err"
)
assert_file_equals "$fixture_root/capability-contract-refresh-pre-commit.status" 0 "capability-contract refresh continues after exact-tree launcher rebind"
grep -Fq 'rebinding trusted launcher once' "$fixture_root/capability-contract-refresh-pre-commit.err" || fail "capability-contract refresh did not rebind the exact-tree launcher"
assert_file_equals "$capture_dir/capability-contract-initial-check-tree" "$capability_contract_drift_tree" "capability-contract initial check tree"
assert_file_equals "$capture_dir/capability-contract-refresh-tree" "$capability_contract_drift_tree" "capability-contract refresh source tree"
capability_contract_refreshed_tree=$(git -C "$git_repo" write-tree)
[[ "$capability_contract_refreshed_tree" != "$capability_contract_drift_tree" ]] || fail "capability-contract refresh did not update the staged tree"
assert_file_equals "$capture_dir/capability-contract-check-tree" "$capability_contract_refreshed_tree" "capability-contract refreshed check tree"
assert_file_equals "$capture_dir/staged-tree" "$capability_contract_refreshed_tree" "capability-contract refresh remote gate tree"
assert_file_equals "$git_repo/docs/doc/codemap/capability-contract/capability_manifest.json" 'generated capability manifest' "capability-contract refreshed output"

project_map_drift_tree=$(git -C "$git_repo" write-tree)
reset_capture
(
  cd "$git_repo/nested"
  GATE_HOOK_PROJECT_MAP_DRIFT_ONCE=1 GATE_HOOK_CAPTURE_SOURCE=1 run_with_status \
    "$fixture_root/project-map-refresh-pre-commit.status" bash "$repo_root/.githooks/pre-commit" \
    2>"$fixture_root/project-map-refresh-pre-commit.err"
)
assert_file_equals "$fixture_root/project-map-refresh-pre-commit.status" 0 "project-map refresh continues after exact-tree launcher rebind"
grep -Fq 'rebinding trusted launcher once' "$fixture_root/project-map-refresh-pre-commit.err" || fail "project-map refresh did not rebind the exact-tree launcher"
assert_file_equals "$capture_dir/project-map-initial-check-tree" "$project_map_drift_tree" "project-map initial check tree"
assert_file_equals "$capture_dir/project-map-refresh-tree" "$project_map_drift_tree" "project-map refresh source tree"
project_map_refreshed_tree=$(git -C "$git_repo" write-tree)
[[ "$project_map_refreshed_tree" != "$project_map_drift_tree" ]] || fail "project-map refresh did not update the staged tree"
assert_file_equals "$capture_dir/project-map-check-tree" "$project_map_refreshed_tree" "project-map refreshed verification tree"
assert_file_equals "$capture_dir/staged-tree" "$project_map_refreshed_tree" "project-map refresh remote gate tree"
assert_file_equals "$git_repo/docs/doc/codemap/project-map/AI_PROJECT_MAP.md" 'generated project map' "project-map refreshed output"
git -C "$git_repo" diff --quiet -- tracked.txt && fail "project-map refresh discarded the unstaged worktree change"

dirty_project_map_tree=$(git -C "$git_repo" write-tree)
tracked_index_before=$(git -C "$git_repo" show :tracked.txt)
printf '%s\n' 'unstaged project map output' >>"$git_repo/docs/doc/codemap/project-map/AI_PROJECT_MAP.md"
printf '%s\n' 'untracked user work' >"$git_repo/untracked-user.txt"
reset_capture
(
  cd "$git_repo/nested"
  GATE_HOOK_CAPTURE_SOURCE=1 run_with_status \
    "$fixture_root/unstaged-project-map.status" bash "$repo_root/.githooks/pre-commit"
)
assert_file_equals "$fixture_root/unstaged-project-map.status" 0 "unstaged project-map output pre-commit exit code"
assert_file_equals "$capture_dir/project-map-initial-check-tree" "$dirty_project_map_tree" "unstaged project-map initial check tree"
assert_file_equals "$capture_dir/project-map-refresh-tree" "$dirty_project_map_tree" "unstaged project-map refresh source tree"
assert_file_equals "$capture_dir/project-map-check-tree" "$dirty_project_map_tree" "unstaged project-map refreshed verification tree"
assert_file_equals "$capture_dir/staged-tree" "$dirty_project_map_tree" "unstaged project-map gate tree"
assert_file_equals "$git_repo/docs/doc/codemap/project-map/AI_PROJECT_MAP.md" 'generated project map' "unstaged project-map refreshed output"
git -C "$git_repo" diff --quiet -- docs/doc/codemap/project-map || fail "unstaged project-map output remained dirty after refresh"
git -C "$git_repo" diff --quiet -- tracked.txt && fail "unstaged project-map refresh discarded unrelated worktree changes"
[[ "$(git -C "$git_repo" show :tracked.txt)" == "$tracked_index_before" ]] || fail "unstaged project-map refresh changed an unrelated staged file"
assert_file_equals "$git_repo/untracked-user.txt" 'untracked user work' "unstaged project-map refresh preserved unrelated untracked file"
git -C "$git_repo" ls-files --error-unmatch -- untracked-user.txt >/dev/null 2>&1 && fail "unstaged project-map refresh staged an unrelated untracked file"
rm "$git_repo/untracked-user.txt"

untracked_project_map_tree=$(git -C "$git_repo" write-tree)
printf '%s\n' 'untracked project map output' >"$git_repo/docs/doc/codemap/project-map/untracked.md"
printf '%s\n' 'second untracked user work' >"$git_repo/untracked-user.txt"
reset_capture
(
  cd "$git_repo/nested"
  GATE_HOOK_CAPTURE_SOURCE=1 run_with_status \
    "$fixture_root/untracked-project-map.status" bash "$repo_root/.githooks/pre-commit"
)
assert_file_equals "$fixture_root/untracked-project-map.status" 0 "untracked project-map output pre-commit exit code"
assert_file_equals "$capture_dir/project-map-initial-check-tree" "$untracked_project_map_tree" "untracked project-map initial check tree"
assert_file_equals "$capture_dir/project-map-refresh-tree" "$untracked_project_map_tree" "untracked project-map refresh source tree"
assert_file_equals "$capture_dir/project-map-check-tree" "$untracked_project_map_tree" "untracked project-map refreshed verification tree"
assert_file_equals "$capture_dir/staged-tree" "$untracked_project_map_tree" "untracked project-map gate tree"
[[ ! -e "$git_repo/docs/doc/codemap/project-map/untracked.md" ]] || fail "untracked project-map output survived trusted refresh"
git -C "$git_repo" diff --quiet -- docs/doc/codemap/project-map || fail "untracked project-map output remained dirty after refresh"
assert_file_equals "$git_repo/untracked-user.txt" 'second untracked user work' "untracked project-map refresh preserved unrelated untracked file"
git -C "$git_repo" ls-files --error-unmatch -- untracked-user.txt >/dev/null 2>&1 && fail "untracked project-map refresh staged an unrelated untracked file"
rm "$git_repo/untracked-user.txt"

project_map_still_drifted_tree=$(git -C "$git_repo" write-tree)
reset_capture
(
  cd "$git_repo/nested"
  GATE_HOOK_PROJECT_MAP_ALWAYS_DRIFT=1 run_with_status \
    "$fixture_root/project-map-still-drifted.status" bash "$repo_root/.githooks/pre-commit" \
    2>"$fixture_root/project-map-still-drifted.err"
)
assert_file_equals "$fixture_root/project-map-still-drifted.status" 1 "project-map persistent drift pre-commit exit code"
assert_file_equals "$capture_dir/project-map-refresh-tree" "$project_map_still_drifted_tree" "project-map persistent drift refresh source tree"
assert_file_equals "$capture_dir/project-map-check-tree" "$project_map_still_drifted_tree" "project-map persistent drift refreshed verification tree"
grep -Fq 'project-map still drifted after one automatic refresh' "$fixture_root/project-map-still-drifted.err" || fail "persistent project-map drift did not block after one refresh"

printf '%s\n' 'malicious generator candidate' >>"$git_repo/scripts/generate_ai_project_map.mjs"
printf '%s\n' 'malicious Makefile candidate' >"$git_repo/Makefile"
git -C "$git_repo" add -- scripts/generate_ai_project_map.mjs Makefile
reset_capture
(
  cd "$git_repo/nested"
  GATE_HOOK_CAPTURE_SOURCE=1 run_with_status "$fixture_root/candidate-project-map-generator.status" bash "$repo_root/.githooks/pre-commit"
)
assert_file_equals "$fixture_root/candidate-project-map-generator.status" 0 "candidate project-map generator pre-commit exit code"
[[ ! -e "$capture_dir/node-executed" ]] || fail "candidate project-map generator was executed"
[[ ! -e "$capture_dir/make-executed" ]] || fail "candidate Makefile was executed"
assert_file_equals "$capture_dir/staged-tree" "$(git -C "$git_repo" write-tree)" "candidate project-map staged tree"
git -C "$git_repo" restore --staged --worktree -- scripts/generate_ai_project_map.mjs Makefile

linked_repo="$fixture_root/linked-repository"
git -C "$git_repo" worktree add -q -b hook-linked "$linked_repo" HEAD
printf '%s\n' 'linked staged' >"$linked_repo/tracked.txt"
git -C "$linked_repo" add tracked.txt
linked_tree=$(git -C "$linked_repo" write-tree)
mkdir -p "$linked_repo/nested"
reset_capture
(
  cd "$linked_repo/nested"
  GATE_HOOK_CAPTURE_SOURCE=1 run_with_status \
    "$fixture_root/linked-pre-commit.status" bash "$repo_root/.githooks/pre-commit"
)
assert_file_equals "$fixture_root/linked-pre-commit.status" 0 "linked pre-commit exit code"
assert_file_equals "$capture_dir/cwd" "$linked_repo/nested" "linked pre-commit cwd"
assert_file_equals "$capture_dir/staged-tree" "$linked_tree" "linked pre-commit staged tree"
assert_file_equals "$capture_dir/project-map-check-tree" "$linked_tree" "linked project-map staged tree"

reset_capture
push_input="$fixture_root/pre-push.stdin"
push_parent=$(git -C "$linked_repo" rev-parse --verify HEAD)
printf '%s\n' 'non-HEAD push candidate' >"$linked_repo/push-candidate.txt"
git -C "$linked_repo" add push-candidate.txt
push_candidate_tree=$(git -C "$linked_repo" write-tree)
push_candidate_commit=$(printf '%s\n\n' 'non-HEAD push candidate' | git -C "$linked_repo" commit-tree "$push_candidate_tree" -p "$push_parent")
git -C "$linked_repo" restore --staged --worktree -- push-candidate.txt
git -C "$linked_repo" update-ref refs/heads/push-candidate "$push_candidate_commit"
zero_oid=$(printf '%*s' "${#push_candidate_commit}" '' | tr ' ' '0')
expected_push_launcher=$(fixture_launcher_for_tree "$linked_repo" "$push_candidate_tree")
printf '%s\n%s' \
  "refs/heads/push-candidate $push_candidate_commit refs/heads/main $zero_oid" \
  "refs/heads/push-candidate $push_candidate_commit refs/heads/topic $zero_oid" >"$push_input"
(
  cd "$linked_repo/nested"
  GATE_HOOK_STDERR_FILE="$cli_error" GATE_HOOK_EXIT_CODE=29 run_with_status "$fixture_root/pre-push.status" \
    bash "$repo_root/.githooks/pre-push" 'upstream' 'ssh://git@example.invalid/team/repo.git' \
    <"$push_input" 2>"$fixture_root/pre-push.err"
)
assert_file_equals "$fixture_root/pre-push.status" 29 "pre-push exit code"
assert_file_equals "$capture_dir/argc" 11 "pre-push argc without shard cap"
assert_file_equals "$capture_dir/arg.0" remote "pre-push arg 0"
assert_file_equals "$capture_dir/arg.1" hook "pre-push arg 1"
assert_file_equals "$capture_dir/arg.2" pre-push "pre-push arg 2"
assert_file_equals "$capture_dir/arg.3" --config "pre-push config flag"
assert_file_equals "$capture_dir/arg.4" "$remote_config" "pre-push config path"
assert_file_equals "$capture_dir/arg.5" --ledger "pre-push ledger flag"
assert_file_equals "$capture_dir/arg.6" "$remote_ledger" "pre-push ledger path"
assert_file_equals "$capture_dir/arg.7" --repository "pre-push repository flag"
assert_file_equals "$capture_dir/arg.8" "$linked_repo" "pre-push repository"
assert_file_equals "$capture_dir/arg.9" upstream "pre-push remote name"
assert_file_equals "$capture_dir/arg.10" 'ssh://git@example.invalid/team/repo.git' "pre-push remote URL"
assert_file_equals "$capture_dir/cwd" "$linked_repo/nested" "pre-push cwd"
assert_file_equals "$capture_dir/launcher" "$expected_push_launcher" "pre-push launcher for non-HEAD pushed tree"
cmp -s "$push_input" "$capture_dir/stdin" || fail "pre-push stdin was not forwarded byte-for-byte"
cmp -s "$cli_error" "$fixture_root/pre-push.err" || fail "pre-push did not return readable CLI stderr"

different_push_input="$fixture_root/pre-push-different-trees.stdin"
printf '%s\n' 'second non-HEAD push candidate' >"$linked_repo/second-push-candidate.txt"
git -C "$linked_repo" add second-push-candidate.txt
second_push_candidate_tree=$(git -C "$linked_repo" write-tree)
second_push_candidate_commit=$(printf '%s\n\n' 'second non-HEAD push candidate' | git -C "$linked_repo" commit-tree "$second_push_candidate_tree" -p "$push_parent")
git -C "$linked_repo" restore --staged --worktree -- second-push-candidate.txt
git -C "$linked_repo" update-ref refs/heads/second-push-candidate "$second_push_candidate_commit"
second_zero_oid=$(printf '%*s' "${#second_push_candidate_commit}" '' | tr ' ' '0')
printf '%s\n' \
  "refs/heads/push-candidate $push_candidate_commit refs/heads/main $zero_oid" \
  "refs/heads/second-push-candidate $second_push_candidate_commit refs/heads/topic $second_zero_oid" >"$different_push_input"
reset_capture
(
  cd "$linked_repo/nested"
  run_with_status "$fixture_root/pre-push-different-trees.status" \
    bash "$repo_root/.githooks/pre-push" 'upstream' 'ssh://git@example.invalid/team/repo.git' \
    <"$different_push_input" 2>"$fixture_root/pre-push-different-trees.err"
)
assert_file_equals "$fixture_root/pre-push-different-trees.status" 1 "pre-push different trees fail-fast exit code"
grep -Fq 'multiple pushed trees require separate trusted gate invocations' "$fixture_root/pre-push-different-trees.err" || fail "pre-push different trees did not fail fast"
[[ ! -e "$capture_dir/argc" ]] || fail "pre-push different trees invoked the gate"

invalid_push_input="$fixture_root/pre-push-invalid.stdin"
printf '%s\n' "refs/heads/push-candidate $push_candidate_commit refs/heads/main $zero_oid extra" >"$invalid_push_input"
reset_capture
(
  cd "$linked_repo/nested"
  run_with_status "$fixture_root/pre-push-invalid.status" \
    bash "$repo_root/.githooks/pre-push" 'upstream' 'ssh://git@example.invalid/team/repo.git' \
    <"$invalid_push_input" 2>"$fixture_root/pre-push-invalid.err"
)
assert_file_equals "$fixture_root/pre-push-invalid.status" 1 "pre-push invalid input exit code"
grep -Fq 'must contain exactly four fields' "$fixture_root/pre-push-invalid.err" || fail "pre-push invalid input did not fail closed"
[[ ! -e "$capture_dir/argc" ]] || fail "pre-push invalid input invoked the gate"

no_repo="$fixture_root/not-a-repository"
mkdir -p "$no_repo"
reset_capture
(
  cd "$no_repo"
  run_with_status "$fixture_root/no-repo-pre-commit.status" bash "$repo_root/.githooks/pre-commit" \
    2>"$fixture_root/no-repo-pre-commit.err"
  run_with_status "$fixture_root/no-repo-pre-push.status" bash "$repo_root/.githooks/pre-push" \
    origin https://example.invalid/repo.git </dev/null 2>"$fixture_root/no-repo-pre-push.err"
)
assert_file_equals "$fixture_root/no-repo-pre-commit.status" 1 "pre-commit unresolved worktree exit code"
assert_file_equals "$fixture_root/no-repo-pre-push.status" 1 "pre-push unresolved worktree exit code"
grep -Fq 'repository root is unavailable' "$fixture_root/no-repo-pre-commit.err" || fail "pre-commit unresolved worktree error is not actionable"
grep -Fq 'repository root is unavailable' "$fixture_root/no-repo-pre-push.err" || fail "pre-push unresolved worktree error is not actionable"
[[ ! -e "$capture_dir/argc" ]] || fail "Git hook invoked the CLI without a resolved worktree"

missing_path=/usr/bin:/bin:/usr/sbin:/sbin
git -C "$git_repo" config --unset superdolphin.gateLauncher
set +e
(
  cd "$git_repo/nested"
  PATH=$missing_path bash "$repo_root/.githooks/pre-commit"
) >/dev/null 2>"$fixture_root/missing-pre-commit.err"
missing_commit_status=$?
(
  cd "$git_repo/nested"
  PATH=$missing_path bash "$repo_root/.githooks/pre-push" origin https://example.invalid/repo.git
) </dev/null >/dev/null 2>"$fixture_root/missing-pre-push.err"
missing_push_status=$?
set -e
fixture_launcher=$(fixture_launcher_for_tree "$git_repo" "$(git -C "$git_repo" write-tree)")
git -C "$git_repo" config superdolphin.gateLauncher "$fixture_launcher"
[[ $missing_commit_status -ne 0 ]] || fail "pre-commit accepted a missing CLI"
[[ $missing_push_status -ne 0 ]] || fail "pre-push accepted a missing CLI"
for entrypoint in \
  "$repo_root/.githooks/pre-commit" \
  "$repo_root/.githooks/pre-push"; do
  if grep -Eq '^[[:space:]]*(go|npm|npx|make)([[:space:]]|$)' "$entrypoint"; then
    fail "$entrypoint contains a forbidden host gate command"
  fi
done

production_e2e="$repo_root/scripts/tests/test_gate_hook_production_e2e.sh"
[[ -x "$production_e2e" ]] || fail "production hook E2E driver is not executable"
# The retired top-level git mode and its old hook/status evidence must stay absent.
if grep -Eq '^[[:space:]]*git\)[[:space:]]|hook pre-commit|gate hook passed:|source_tree=|status --job|wait --job' "$production_e2e"; then
  fail "production hook E2E contains retired git-mode or stale hook evidence"
fi
grep -Fq '_cleanup-contract)' "$production_e2e" || fail "production hook E2E lost the cleanup contract mode"
grep -Fq 'run_cleanup_contract' "$production_e2e" || fail "production hook E2E lost the cleanup contract implementation"
if grep -Eq 'fake|mock|recordingHookCoordinator|provision production' "$production_e2e"; then
  fail "production hook E2E contains a fixture or provisioning bypass"
fi

cleanup_contract_repo="$fixture_root/cleanup-contract-repository"
mkdir -p "$cleanup_contract_repo"
git -C "$cleanup_contract_repo" init -q
git -C "$cleanup_contract_repo" config user.name 'Cleanup Contract'
git -C "$cleanup_contract_repo" config user.email 'cleanup-contract@example.invalid'
printf '%s\n' 'cleanup contract' >"$cleanup_contract_repo/tracked.txt"
git -C "$cleanup_contract_repo" add tracked.txt
git -C "$cleanup_contract_repo" commit -qm 'fixture base'

verify_cleanup_contract() {
  local label=$1 expected_status=$2 actual_status=$3 state_file=$4 stderr_file=$5
  if [[ $actual_status -ne $expected_status ]]; then
    cat "$stderr_file" >&2
    fail "cleanup $label exit=$actual_status, want $expected_status"
  fi
  if grep -Fq 'unbound variable' "$stderr_file"; then
    cat "$stderr_file" >&2
    fail "cleanup $label used an expired local variable"
  fi
  if [[ ! -s "$state_file" ]]; then
    cat "$stderr_file" >&2
    fail "cleanup $label did not write resource state"
  fi
  local cleanup_fixture cleanup_worktree cleanup_branch cleanup_remote cleanup_repo
  {
    IFS= read -r cleanup_fixture
    IFS= read -r cleanup_worktree
    IFS= read -r cleanup_branch
    IFS= read -r cleanup_remote
    IFS= read -r cleanup_repo
  } <"$state_file"
  if [[ -e "$cleanup_fixture" ]]; then
    cat "$stderr_file" >&2
    fail "cleanup $label leaked fixture $cleanup_fixture"
  fi
  if git -C "$cleanup_repo" worktree list --porcelain | grep -Fqx "worktree $cleanup_worktree"; then
    cat "$stderr_file" >&2
    fail "cleanup $label leaked worktree"
  fi
  if git -C "$cleanup_repo" show-ref --verify --quiet "refs/heads/$cleanup_branch"; then
    cat "$stderr_file" >&2
    fail "cleanup $label leaked branch"
  fi
  if git -C "$cleanup_repo" remote | grep -Fqx "$cleanup_remote"; then
    cat "$stderr_file" >&2
    fail "cleanup $label leaked remote"
  fi
}

run_cleanup_contract() {
  local outcome=$1 state_file=$2 stdout_file=$3 stderr_file=$4
  GATE_HOOK_E2E_CLEANUP_CONTRACT=1 \
    GATE_HOOK_E2E_CLEANUP_OUTCOME="$outcome" \
    GATE_HOOK_E2E_CLEANUP_REPOSITORY="$cleanup_contract_repo" \
    GATE_HOOK_E2E_CLEANUP_STATE_FILE="$state_file" \
    GATE_HOOK_E2E_EVIDENCE_DIR="${state_file%.state}-evidence" \
    bash "$production_e2e" _cleanup-contract >"$stdout_file" 2>"$stderr_file"
}

assert_cleanup_contract() {
  local outcome expected_status state_file stdout_file stderr_file actual_status
  outcome=$1
  expected_status=$2
  state_file="$fixture_root/cleanup-$outcome.state"
  stdout_file="$fixture_root/cleanup-$outcome.stdout"
  stderr_file="$fixture_root/cleanup-$outcome.stderr"
  set +e
  run_cleanup_contract "$outcome" "$state_file" "$stdout_file" "$stderr_file"
  actual_status=$?
  set -e
  verify_cleanup_contract "$outcome" "$expected_status" "$actual_status" "$state_file" "$stderr_file"
}

assert_cleanup_contract success 0
assert_cleanup_contract failure 19
assert_cleanup_contract int 130
assert_cleanup_contract term 143
assert_cleanup_contract repeat 0

assert_parallel_cleanup_round() {
  local round=$1 slot state_file stdout_file stderr_file
  local -a pids=()
  local -a statuses=()
  for slot in 1 2; do
    state_file="$fixture_root/parallel-$round-$slot.state"
    stdout_file="$fixture_root/parallel-$round-$slot.stdout"
    stderr_file="$fixture_root/parallel-$round-$slot.stderr"
    run_cleanup_contract success "$state_file" "$stdout_file" "$stderr_file" &
    pids[slot]=$!
  done
  for slot in 1 2; do
    if wait "${pids[$slot]}"; then
      statuses[slot]=0
    else
      statuses[slot]=$?
    fi
  done
  for slot in 1 2; do
    verify_cleanup_contract \
      "parallel-$round-$slot" 0 "${statuses[$slot]}" \
      "$fixture_root/parallel-$round-$slot.state" "$fixture_root/parallel-$round-$slot.stderr"
  done
}

for round in 1 2 3; do
  assert_parallel_cleanup_round "$round"
done

printf '%s\n' 'gate hook entrypoint contracts: PASS'
