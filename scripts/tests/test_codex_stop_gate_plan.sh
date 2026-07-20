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

run_frontend_gate_with_fake_tools() {
  local fixture="$1"
  local capture="$2"
  local lock_dir="$3"
  local npx_exit="${4:-0}"
  local fake_bin="${tmp_dir}/fake-bin"
  mkdir -p "${fake_bin}"
  cat >"${fake_bin}/npm" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
  cat >"${fake_bin}/npx" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >"${CAPTURE_PATH}"
exit "${FAKE_NPX_EXIT}"
EOF
  chmod +x "${fake_bin}/npm" "${fake_bin}/npx"
  PATH="${fake_bin}:${PATH}" CAPTURE_PATH="${capture}" FAKE_NPX_EXIT="${npx_exit}" \
    CODEX_STOP_GATE_CHANGED_FILES_FILE="${fixture}" CODEX_STOP_GATE_LOG_DIR="${tmp_dir}/logs" \
    CODEX_STOP_GATE_LOCK_DIR="${lock_dir}" CODEX_STOP_GATE_NPX_BIN="${fake_bin}/npx" bash "${gate}"
}

run_go_gate_with_fake_go() {
  local fixture="$1"
  local lock_dir="$2"
  local scenario="$3"
  local fake_bin="${tmp_dir}/fake-go-bin"
  mkdir -p "${fake_bin}"
  cat >"${fake_bin}/go" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == "run" && "${2:-}" == "./scripts/capcontract" && "${3:-}" == "--print-path-rules" ]]; then
  printf 'tree\tinternal/contract\n'
  exit 0
fi
if [[ "${1:-}" != "list" ]]; then
  printf 'unexpected fake go command: %s\n' "$*" >&2
  exit 99
fi

case "${FAKE_GO_LIST_SCENARIO}" in
  nonzero)
    printf 'go list exploded\n' >&2
    exit 23
    ;;
  empty)
    exit 0
    ;;
  partial)
    printf '%s\n' "example.com/super/internal/provider/codexapp"
    printf '%s\n' "__CODEX_STOP_GATE_GO_LIST_ERROR__ ./cmd/mcp-orch/tools: no required module provides package"
    exit 0
    ;;
  *)
    printf 'unknown fake go list scenario: %s\n' "${FAKE_GO_LIST_SCENARIO}" >&2
    exit 98
    ;;
esac
EOF
  chmod +x "${fake_bin}/go"
  FAKE_GO_LIST_SCENARIO="${scenario}" CODEX_STOP_GATE_GO_BIN="${fake_bin}/go" \
    CODEX_STOP_GATE_CHANGED_FILES_FILE="${fixture}" CODEX_STOP_GATE_LOG_DIR="${tmp_dir}/logs" \
    CODEX_STOP_GATE_LOCK_DIR="${lock_dir}" bash "${gate}"
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

assert_has_text() {
  local haystack="$1"
  local needle="$2"
  local label="$3"
  if ! grep -Fq "${needle}" <<< "${haystack}"; then
    printf '[test][FAIL] %s: missing text "%s"\n%s\n' "${label}" "${needle}" "${haystack}" >&2
    exit 1
  fi
  printf '[test][PASS] %s\n' "${label}"
}

rules_case_index=0
write_rules_go() {
  local output="$1"
  local path="$2"
  printf '#!/usr/bin/env bash\nprintf %%s %q\n' "${output}" >"${path}"
  chmod +x "${path}"
}

assert_rules_output_fails_closed() {
  local label="$1"
  local output="$2"
  local fake_go result
  rules_case_index=$((rules_case_index + 1))
  fake_go="${tmp_dir}/malformed-rules-go-${rules_case_index}"
  result="${tmp_dir}/malformed-rules-${rules_case_index}.out"
  write_rules_go "${output}" "${fake_go}"
  if CODEX_STOP_GATE_GO_BIN="${fake_go}" CODEX_STOP_GATE_CHANGED_FILES_FILE="${go_fixture}" \
    CODEX_STOP_GATE_LOG_DIR="${tmp_dir}/malformed-rules-logs-${rules_case_index}" bash "${gate}" --print-plan >"${result}" 2>&1; then
    printf '[test][FAIL] %s did not fail closed\n' "${label}" >&2
    cat "${result}" >&2
    exit 1
  fi
  if ! grep -Fq "capcontract path rules" "${result}"; then
    printf '[test][FAIL] %s missing actionable error\n' "${label}" >&2
    cat "${result}" >&2
    exit 1
  fi
  printf '[test][PASS] %s fails closed\n' "${label}"
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

go_list_nonzero_lock="${tmp_dir}/go-list-nonzero.lock"
go_list_nonzero_output="$(run_go_gate_with_fake_go "${go_fixture}" "${go_list_nonzero_lock}" nonzero)"
assert_has_text "${go_list_nonzero_output}" '"decision":"block"' "go list nonzero blocks stop gate"
assert_has_text "${go_list_nonzero_output}" 'Super-Dolphin Codex Stop gate failed: changed Go package tests.' "go list nonzero reports Go stage failure"
assert_not_contains "${go_list_nonzero_output}" '{"continue":true}' "go list nonzero does not continue"

go_list_empty_lock="${tmp_dir}/go-list-empty.lock"
go_list_empty_output="$(run_go_gate_with_fake_go "${go_fixture}" "${go_list_empty_lock}" empty)"
assert_has_text "${go_list_empty_output}" '"decision":"block"' "go list empty output blocks stop gate"
assert_has_text "${go_list_empty_output}" 'Super-Dolphin Codex Stop gate failed: changed Go package tests.' "go list empty output reports Go stage failure"
assert_not_contains "${go_list_empty_output}" '{"continue":true}' "go list empty output does not continue"

go_list_partial_lock="${tmp_dir}/go-list-partial.lock"
go_list_partial_output="$(run_go_gate_with_fake_go "${module_fixture}" "${go_list_partial_lock}" partial)"
assert_has_text "${go_list_partial_output}" '"decision":"block"' "partial go list resolution blocks stop gate"
assert_has_text "${go_list_partial_output}" 'Super-Dolphin Codex Stop gate failed: changed Go package tests.' "partial go list resolution reports Go stage failure"
assert_not_contains "${go_list_partial_output}" '{"continue":true}' "partial go list resolution does not continue"

mixed_fixture="${tmp_dir}/mixed.txt"
cat >"${mixed_fixture}" <<'EOF'
internal/platform/toolbridge/handler_peer_decode.go
frontend-app/src/App.jsx
EOF
mixed_plan="$(run_plan "${mixed_fixture}")"
assert_contains "${mixed_plan}" "execution parallel go frontend" "mixed changes run Go and frontend stages in parallel"

frontend_vitest_capture="${tmp_dir}/frontend-vitest-args.txt"
frontend_gate_lock="${tmp_dir}/frontend-gate.lock"
frontend_gate_output="$(run_frontend_gate_with_fake_tools "${frontend_fixture}" "${frontend_vitest_capture}" "${frontend_gate_lock}")"
assert_contains "${frontend_gate_output}" '{"continue":true}' "frontend hook completes with fake tools"
assert_contains "$(cat "${frontend_vitest_capture}")" "vitest run" "frontend hook preserves Vitest file parallelism"
if [[ -e "${frontend_gate_lock}" ]]; then
  printf '[test][FAIL] frontend hook releases its worktree lock\n' >&2
  exit 1
fi
printf '[test][PASS] frontend hook releases its worktree lock\n'

failed_frontend_gate_lock="${tmp_dir}/failed-frontend-gate.lock"
failed_frontend_gate_output="$(run_frontend_gate_with_fake_tools "${frontend_fixture}" "${frontend_vitest_capture}" "${failed_frontend_gate_lock}" 1)"
if ! grep -Fq '"reason":"Super-Dolphin Codex Stop gate failed: frontend vitest (frontend-app).' <<< "${failed_frontend_gate_output}"; then
  printf '[test][FAIL] parallel frontend stage propagates Vitest failure\n%s\n' "${failed_frontend_gate_output}" >&2
  exit 1
fi
printf '[test][PASS] parallel frontend stage propagates Vitest failure\n'
if [[ -e "${failed_frontend_gate_lock}" ]]; then
  printf '[test][FAIL] failed frontend hook releases its worktree lock\n' >&2
  exit 1
fi
printf '[test][PASS] failed frontend hook releases its worktree lock\n'

active_gate_lock="${tmp_dir}/active-gate.lock"
mkdir "${active_gate_lock}"
printf '%s\n' "$$" >"${active_gate_lock}/pid"
active_gate_output="$(run_frontend_gate_with_fake_tools "${frontend_fixture}" "${frontend_vitest_capture}" "${active_gate_lock}")"
assert_contains "${active_gate_output}" "{\"decision\":\"block\",\"reason\":\"Super-Dolphin Codex Stop gate blocked: another gate is active for this worktree (pid $$).\"}" "concurrent frontend hook fails fast"

stale_gate_lock="${tmp_dir}/stale-gate.lock"
mkdir "${stale_gate_lock}"
printf '%s\n' "999999" >"${stale_gate_lock}/pid"
stale_gate_output="$(run_frontend_gate_with_fake_tools "${frontend_fixture}" "${frontend_vitest_capture}" "${stale_gate_lock}")"
assert_contains "${stale_gate_output}" '{"continue":true}' "frontend hook reclaims a dead-owner lock"
if [[ -e "${stale_gate_lock}" ]]; then
  printf '[test][FAIL] reclaimed frontend hook releases its worktree lock\n' >&2
  exit 1
fi
printf '[test][PASS] reclaimed frontend hook releases its worktree lock\n'

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

failed_rules_go="${tmp_dir}/failed-rules-go"
cat >"${failed_rules_go}" <<'EOF'
#!/usr/bin/env bash
echo "synthetic path-rules failure" >&2
exit 23
EOF
chmod +x "${failed_rules_go}"
if CODEX_STOP_GATE_GO_BIN="${failed_rules_go}" CODEX_STOP_GATE_CHANGED_FILES_FILE="${go_fixture}" \
  CODEX_STOP_GATE_LOG_DIR="${tmp_dir}/failed-rules-logs" bash "${gate}" --print-plan >"${tmp_dir}/failed-rules.out" 2>&1; then
  printf '[test][FAIL] path-rules command failure did not fail closed\n' >&2
  cat "${tmp_dir}/failed-rules.out" >&2
  exit 1
fi
if ! grep -Fq "capcontract path rules" "${tmp_dir}/failed-rules.out"; then
  printf '[test][FAIL] path-rules failure missing actionable error\n' >&2
  cat "${tmp_dir}/failed-rules.out" >&2
  exit 1
fi
printf '[test][PASS] path-rules command failure fails closed\n'

canonical_rules_go="${tmp_dir}/canonical-rules-go"
write_rules_go $'tree\tinternal/provider\n' "${canonical_rules_go}"
canonical_rules_plan="$(CODEX_STOP_GATE_GO_BIN="${canonical_rules_go}" CODEX_STOP_GATE_CHANGED_FILES_FILE="${go_fixture}" \
  CODEX_STOP_GATE_LOG_DIR="${tmp_dir}/canonical-rules-logs" bash "${gate}" --print-plan | sort)"
assert_contains "${canonical_rules_plan}" "capcontract_check make capcontract-check" "canonical two-column rule remains accepted"

assert_rules_output_fails_closed "empty rule kind" $'\tinternal/provider\n'
assert_rules_output_fails_closed "empty rule path" $'tree\t\n'
assert_rules_output_fails_closed "trailing empty third column" $'tree\tinternal/provider\t\n'
assert_rules_output_fails_closed "non-empty third column" $'tree\tinternal/provider\textra\n'
assert_rules_output_fails_closed "double tab separator" $'tree\t\tinternal/provider\n'
assert_rules_output_fails_closed "additional tab field" $'tree\tinternal/provider\t\textra\n'
assert_rules_output_fails_closed "missing tab separator" $'tree internal/provider\n'
assert_rules_output_fails_closed "carriage return" $'tree\tinternal/provider\r\n'
assert_rules_output_fails_closed "internal blank line" $'tree\tinternal/provider\n\nexact\tscripts/capcontract.go\n'
assert_rules_output_fails_closed "trailing blank line" $'tree\tinternal/provider\n\n'
assert_rules_output_fails_closed "missing final newline" $'tree\tinternal/provider'

active_fixture="${tmp_dir}/active.txt"
cat >"${active_fixture}" <<'EOF'
go.mod
EOF
stop_active_output="$(run_gate_with_input "${active_fixture}" '{"hook_event_name":"Stop","stop_hook_active":true}')"
assert_contains "${stop_active_output}" '{"continue":true}' "Stop hook active turn should continue without recursion"

subagent_active_output="$(run_gate_with_input "${active_fixture}" '{"hook_event_name":"SubagentStop","stop_hook_active":true}')"
assert_contains "${subagent_active_output}" '{"continue":true}' "SubagentStop hook active turn should continue without recursion"

echo "[test] codex stop gate plan checks passed"
