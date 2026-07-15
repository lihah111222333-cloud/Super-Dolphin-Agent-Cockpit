#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
gate="${repo_root}/scripts/codex_stop_gate.sh"
tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/codex-stop-gate-test.XXXXXX")"
trap 'rm -rf "${tmp_dir}"' EXIT

run_plan() {
  local fixture="$1"
  CODEX_STOP_GATE_CHANGED_FILES_FILE="${fixture}" bash "${gate}" --print-plan | sort
}

run_gate_with_input() {
  local fixture="$1"
  local input="$2"
  CODEX_STOP_GATE_CHANGED_FILES_FILE="${fixture}" CODEX_STOP_GATE_LOG_DIR="${tmp_dir}/logs" \
    bash -c "printf '%s\n' \"\$1\" | bash \"\$2\"" _ "${input}" "${gate}"
}

assert_contains() {
  local haystack="$1"
  local needle="$2"
  local label="$3"
  if ! grep -Fxq "${needle}" <<< "${haystack}"; then
    printf '[test][FAIL] %s: missing "%s"\n%s\n' "${label}" "${needle}" "${haystack}" >&2
    exit 1
  fi
  printf '[test][PASS] %s\n' "${label}"
}

assert_not_contains() {
  local haystack="$1"
  local needle="$2"
  local label="$3"
  if grep -Fxq "${needle}" <<< "${haystack}"; then
    printf '[test][FAIL] %s: unexpected "%s"\n%s\n' "${label}" "${needle}" "${haystack}" >&2
    exit 1
  fi
  printf '[test][PASS] %s\n' "${label}"
}

go_fixture="${tmp_dir}/go.txt"
cat >"${go_fixture}" <<'EOF'
internal/provider/codexapp/support.go
cmd/mcp-orch/tools/pos.go
EOF
go_plan="$(run_plan "${go_fixture}")"
assert_contains "${go_plan}" "go_pkg ./internal/provider/codexapp" "Go package mapping includes codexapp"
assert_contains "${go_plan}" "go_pkg ./cmd/mcp-orch/tools" "Go package mapping includes mcp-orch tools"
assert_contains "${go_plan}" "guard go" "Go changes enable Go guard"
assert_contains "${go_plan}" "capcontract_check make capcontract-check" "Capability producer changes enable capcontract check"
assert_not_contains "${go_plan}" "guard frontend" "Go-only changes skip frontend guard"

module_fixture="${tmp_dir}/module.txt"
cat >"${module_fixture}" <<'EOF'
go.mod
EOF
module_plan="$(run_plan "${module_fixture}")"
assert_contains "${module_plan}" "go_pkg ./cmd/..." "go.mod change includes cmd packages"
assert_contains "${module_plan}" "go_pkg ./internal/..." "go.mod change includes internal packages"
assert_contains "${module_plan}" "go_pkg ./pkg/..." "go.mod change includes pkg packages"
assert_contains "${module_plan}" "go_pkg ./scripts/..." "go.mod change includes scripts packages"
assert_contains "${module_plan}" "guard go" "go.mod change enables Go guard"

frontend_fixture="${tmp_dir}/frontend.txt"
cat >"${frontend_fixture}" <<'EOF'
frontend-app/src/App.jsx
frontend-app/package.json
EOF
frontend_plan="$(run_plan "${frontend_fixture}")"
assert_contains "${frontend_plan}" "frontend_project frontend-app" "frontend mapping includes React frontend app"
assert_contains "${frontend_plan}" "guard frontend" "frontend changes enable frontend guard"
assert_not_contains "${frontend_plan}" "guard go" "frontend-only changes skip Go guard"
assert_not_contains "${frontend_plan}" "capcontract_check make capcontract-check" "frontend-only changes skip capcontract check"

ignored_frontend_fixture="${tmp_dir}/ignored-frontend.txt"
cat >"${ignored_frontend_fixture}" <<'EOF'
frontend-app/dist/index.html
EOF
ignored_frontend_plan="$(run_plan "${ignored_frontend_fixture}")"
assert_contains "${ignored_frontend_plan}" "none" "ignored frontend build output skips hook gates"

hook_fixture="${tmp_dir}/hook.txt"
cat >"${hook_fixture}" <<'EOF'
.codex/hooks.json
scripts/codex_stop_gate.sh
EOF
hook_plan="$(run_plan "${hook_fixture}")"
assert_contains "${hook_plan}" "hook_test scripts/tests/test_codex_stop_gate_plan.sh" "hook changes enable hook plan tests"

docs_fixture="${tmp_dir}/docs.txt"
cat >"${docs_fixture}" <<'EOF'
docs/plans/example.md
README.md
EOF
docs_plan="$(run_plan "${docs_fixture}")"
assert_contains "${docs_plan}" "none" "docs-only changes skip hook gates"
assert_not_contains "${docs_plan}" "capcontract_check make capcontract-check" "docs-only changes skip capcontract check"

active_fixture="${tmp_dir}/active.txt"
cat >"${active_fixture}" <<'EOF'
go.mod
EOF
stop_active_output="$(run_gate_with_input "${active_fixture}" '{"hook_event_name":"Stop","stop_hook_active":true}')"
assert_contains "${stop_active_output}" '{"continue":true}' "Stop hook active turn should continue without recursion"

subagent_active_output="$(run_gate_with_input "${active_fixture}" '{"hook_event_name":"SubagentStop","stop_hook_active":true}')"
assert_contains "${subagent_active_output}" '{"continue":true}' "SubagentStop hook active turn should continue without recursion"

echo "[test] codex stop gate plan checks passed"
