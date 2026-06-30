# Production Risk Remediation Evidence Ledger

Controller evidence for `docs/pians/2026-06-29-production-risk-remediation-plan.md`.

The rows below record the integrated evidence surface after merge `4da1b000` plus the local follow-up fix for `P1-25` and `P3-07`.

## Active Evidence

| ID | Lane | RED | GREEN | Commit | Residual Risk |
|---|---|---|---|---|---|
| P0-01 | mcp-runtime-security | lane fail-first security tests rejected untrusted stdio MCP command/env inheritance | `./scripts/test_with_guard.sh ./internal/platform/toolbridge ./internal/provider/shared ./internal/provider/codexapp ./internal/provider/claudecli ./internal/module/thread ./internal/module/mcp_server ./internal/mcpserver/common ./internal/contract ./internal/dto/provider -count=1` | 4da1b000 | none |
| P1-01 | mcp-runtime-security | lane fail-first tests rejected private HTTP MCP URL and unsafe headers | same as P0-01 lane command | 4da1b000 | none |
| P1-02 | provider-security-logging | lane fail-first tests rejected unknown approval/sandbox values | `./scripts/test_with_guard.sh ./internal/module/thread ./internal/module/memory ./internal/provider/claudecli ./internal/provider/codexapp ./internal/provider/unified ./internal/provider/dreamexec ./internal/contract ./internal/dto/provider -count=1` | 4da1b000 | none |
| P1-03 | provider-security-logging | lane fail-first dream executor tests required no-tools/read-only/min-env policy | same as P1-02 lane command | 4da1b000 | none |
| P1-04 | thread-session-lifecycle | lane fail-first racing Stop/Archive/Delete vs Resume tests reproduced resume unblock gap | `./scripts/test_with_guard.sh ./internal/module/thread ./internal/module/memory ./internal/store/thread ./internal/provider/unified ./internal/provider/claudecli ./internal/provider/codexapp ./internal/contract ./internal/dto/provider -count=1` | 4da1b000 | none |
| P1-05 | thread-session-lifecycle | lane fail-first auto-resume test rejected missing prompt snapshot | same as P1-04 lane command | 4da1b000 | none |
| P1-06 | thread-session-lifecycle | lane fail-first persist failure test reproduced ghost runtime | same as P1-04 lane command | 4da1b000 | none |
| P1-08 | app-graph-readiness | lane fail-first fx graph tests rejected missing toolbridge dependencies | `./scripts/test_with_guard.sh ./internal/app ./internal/contract -count=1` | 4da1b000 | none |
| P1-09 | mcp-orch-protocol | lane fail-first slow wakeup tests reproduced duplicate dispatch risk | `./scripts/test_with_guard.sh ./cmd/mcp-orch/... -count=1` and `make sqlc-verify` | 4da1b000 | none |
| P1-10 | mcp-orch-protocol | lane fail-first automation timeout test showed saved timeout was not enforced | same as P1-09 lane command | 4da1b000 | none |
| P1-11 | mcp-runtime-security | lane fail-first malformed tools/list tests rejected missing tools array and empty names | same as P0-01 lane command | 4da1b000 | none |
| P1-12 | lsp-perf-observability | lane fail-first peer-down test rejected partial tools/list success | `./scripts/test_with_guard.sh ./cmd/mcp-lsp/... ./internal/provider/codexapp ./internal/provider/shared ./internal/provider/claudecli ./internal/provider/unified ./internal/module/turn ./internal/module/uistate ./internal/module/dashboard ./internal/module/cron ./internal/module/memory/team ./internal/platform/bus ./internal/platform/toolbridge ./internal/platform/mcpcontrol ./internal/platform/observability -count=1` | 4da1b000 | none |
| P1-13 | provider-security-logging | lane fail-first provider event tests detected raw payload leakage | same as P1-02 lane command | 4da1b000 | none |
| P1-14 | lsp-perf-observability | lane fail-first preview tests detected unredacted tool/event previews | same as P1-12 lane command | 4da1b000 | none |
| P1-15 | store-schema-config | lane fail-first sharedfile tests injected gitignore protection failure | `./scripts/test_with_guard.sh ./internal/store/sharedfile ./cmd/mcp-orch/store/sharedfile ./internal/platform/sharedfilepath ./internal/platform/sharedfilegitignore ./internal/platform/db ./internal/store/prompt ./internal/module/prompt ./internal/module/uistate ./internal/module/memory ./internal/platform/hooks ./internal/platform/runtimeenv -count=1` and `make sqlc-verify` | 4da1b000 | none |
| P1-16 | store-schema-config | lane fail-first DB lock test showed transaction was not BEGIN IMMEDIATE | same as P1-15 lane command | 4da1b000 | none |
| P1-17 | store-schema-config | lane fail-first prompt routing tests reproduced match_when null/object drift | same as P1-15 lane command | 4da1b000 | none |
| P1-18 | thread-session-lifecycle | lane fail-first prompt resolution test blocked empty system prompt launch | same as P1-04 lane command | 4da1b000 | none |
| P1-19 | store-schema-config | lane fail-first builtin tool preference read error test blocked silent defaulting | same as P1-15 lane command | 4da1b000 | none |
| P1-20 | thread-session-lifecycle | lane fail-first malformed runtime config tests reproduced nil-runtime fallback | same as P1-04 lane command | 4da1b000 | none |
| P1-21 | store-schema-config | lane fail-first hook resolver test reversed readback fallback | same as P1-15 lane command | 4da1b000 | none |
| P1-22 | frontend-wails | lane fail-first backend strict decode tests rejected unknown turn fields | `./scripts/test_with_guard.sh ./internal/module/turn ./internal/module/dashboard ./internal/platform/rpc ./internal/ui/wails ./cmd/mcp-orch/tools ./cmd/mcp-orch/orchestration/nodeexec -count=1` and frontend lint/test/build | 4da1b000 | none |
| P1-23 | frontend-wails | lane fail-first frontend facade tests rejected unknown turn/start fields | same as P1-22 lane command | 4da1b000 | none |
| P1-24 | frontend-wails | lane fail-first surface tests caught missing thread/start skill fields | same as P1-22 lane command | 4da1b000 | none |
| P1-25 | frontend-wails | `./scripts/test_with_guard.sh -run TestApplyThreadStoppedResetsPatchSequence ./internal/module/uistate -count=1` failed with `Generation:0` | `./scripts/test_with_guard.sh -run TestApplyThreadStoppedResetsPatchSequence ./internal/module/uistate -count=1` | working-tree | none |
| P1-26 | frontend-wails | lane fail-first Wails websocket tests rejected cross-loopback origin | same as P1-22 lane command | 4da1b000 | none |
| P1-27 | frontend-wails | lane fail-first shared-file preview tests rejected absolute/path traversal input | same as P1-22 lane command | 4da1b000 | none |
| P1-28 | release-ci-guard | lane fail-first Windows package guard test reproduced missing installer dependency success | `go test ./scripts -run 'Package|Release|Frontend|Guard|Commit|Evidence' -count=1` | 4da1b000 | none |
| P1-29 | release-ci-guard | lane fail-first macOS package guard test reproduced non-atomic install | same as P1-28 lane command | 4da1b000 | none |
| P1-30 | release-ci-guard | lane fail-first release asset matrix guard detected missing platform assets | same as P1-28 lane command | 4da1b000 | none |
| P1-31 | release-ci-guard | lane fail-first skill mirror validation detected mirror drift | `python3 scripts/validate_super_agent_skills.py` | 4da1b000 | none |
| P1-33 | lsp-perf-observability | lane fail-first server pool capacity test exceeded live process cap | same as P1-12 lane command | 4da1b000 | none |
| P1-34 | mcp-orch-protocol | lane fail-first shutdown drain test reproduced unbounded StopAllAgents | same as P1-09 lane command | 4da1b000 | none |
| P1-35 | lsp-perf-observability | lane fail-first LSP rename rollback test left buffer dirty | same as P1-12 lane command | 4da1b000 | none |
| P1-36 | lsp-perf-observability | lane fail-first tool result cache test lost persist failure evidence | same as P1-12 lane command | 4da1b000 | none |
| P2-02 | mcp-runtime-security | lane fail-first peer parity tests reproduced list/call selection mismatch | same as P0-01 lane command | 4da1b000 | none |
| P2-03 | lsp-perf-observability | lane fail-first mcp-lsp schema parity test caught missing language_id | same as P1-12 lane command | 4da1b000 | none |
| P2-04 | lsp-perf-observability | lane fail-first mcp-lsp action parity test caught missing structure actions | same as P1-12 lane command | 4da1b000 | none |
| P2-05 | lsp-perf-observability | lane fail-first timeout middleware test showed handler context survived timeout | same as P1-12 lane command | 4da1b000 | none |
| P2-06 | frontend-wails | lane fail-first clipboard tests rejected non-image or oversized payloads | same as P1-22 lane command | 4da1b000 | none |
| P2-07 | frontend-wails | lane fail-first code scope test reproduced explicit invalid project fallback | same as P1-22 lane command | 4da1b000 | none |
| P2-08 | frontend-wails | lane fail-first image preview tests rejected fake or oversized images | same as P1-22 lane command | 4da1b000 | none |
| P2-09 | frontend-wails | lane fail-first workflow config tests surfaced malformed config as blocking diagnostic | same as P1-22 lane command | 4da1b000 | none |
| P2-10 | frontend-wails | lane fail-first workflow display adapter tests rejected parse-as-empty behavior | same as P1-22 lane command | 4da1b000 | none |
| P2-11 | frontend-wails | lane fail-first chat action tests detected swallowed UI action failures | same as P1-22 lane command | 4da1b000 | none |
| P2-12 | frontend-wails | lane fail-first approval timeout tests kept busy state stuck | same as P1-22 lane command | 4da1b000 | none |
| P2-13 | frontend-wails | lane fail-first approval notice tests required visible feedback | same as P1-22 lane command | 4da1b000 | none |
| P2-14 | lsp-perf-observability | lane fail-first log detail tests required sanitized raw/extra detail surface | same as P1-12 lane command | 4da1b000 | none |
| P2-15 | lsp-perf-observability | lane fail-first trace write tests detected swallowed trace errors | same as P1-12 lane command | 4da1b000 | none |
| P2-16 | lsp-perf-observability | lane fail-first provider/toolbridge trace tests rejected generic operation failed only | same as P1-12 lane command | 4da1b000 | none |
| P2-17 | store-schema-config | lane fail-first prompt write/import tests rejected non-object match_when | same as P1-15 lane command | 4da1b000 | none |
| P2-18 | store-schema-config | lane fail-first auto-dream intent tests rejected malformed intent fallback | same as P1-15 lane command | 4da1b000 | none |
| P2-19 | provider-security-logging | lane fail-first auto-dream queue overflow tests reproduced dropped enqueue | same as P1-02 lane command | 4da1b000 | none |
| P2-20 | provider-security-logging | lane fail-first auto-dream snapshot tests required last failure state | same as P1-02 lane command | 4da1b000 | none |
| P2-21 | store-schema-config | lane fail-first memory index tests rejected read-failure-as-miss | same as P1-15 lane command | 4da1b000 | none |
| P2-22 | store-schema-config | lane fail-first video.env parser tests rejected malformed line skipping | same as P1-15 lane command | 4da1b000 | none |
| P2-23 | store-schema-config | lane fail-first schema floor test caught stale migration floor | `make sqlc-verify` and store/schema tests | 4da1b000 | none |
| P2-25 | release-ci-guard | lane fail-first frontend embed verifier tests caught web-dist drift | `make frontend-embed-verify` and scripts frontend embed guard tests | 4da1b000 | none |
| P2-26 | release-ci-guard | lane fail-first pre-push path matrix tests caught missing sqlc/codemap/skill gates | `go test ./scripts -run 'Package|Release|Frontend|Guard|Commit|Evidence' -count=1` | 4da1b000 | none |
| P2-29 | mcp-orch-protocol | lane fail-first query plan test caught missing wakeup dispatch partial index | same as P1-09 lane command | 4da1b000 | none |
| P2-30 | lsp-perf-observability | lane fail-first cron queue pressure tests reproduced unbounded progress backlog | same as P1-12 lane command | 4da1b000 | none |
| P2-31 | lsp-perf-observability | lane fail-first team sync watcher tests reproduced uncapped recursive scan | same as P1-12 lane command | 4da1b000 | none |
| P2-32 | release-ci-guard | lane fail-first release wrapper tests caught missing clean tree/source revision gate | same as P1-28 lane command | 4da1b000 | none |
| P3-01 | mcp-runtime-security | lane fail-first runtime MCP trust-boundary tests required centralized policy | same as P0-01 lane command | 4da1b000 | none |
| P3-02 | lsp-perf-observability | lane fail-first secret corpus preview tests required shared redaction helper | same as P1-12 lane command | 4da1b000 | none |
| P3-03 | release-ci-guard | lane fail-first release preflight tests required clean tree, assets, installer and embed checks | same as P1-28 lane command | 4da1b000 | none |
| P3-05 | lsp-perf-observability | lane fail-first trace field tests required standard error_preview/error_code/provider_exit_code fields | same as P1-12 lane command | 4da1b000 | none |
| P3-06 | frontend-wails | lane fail-first RPC contract audit tests caught DTO facade drift | frontend `audit:rpc-contracts` and lane frontend tests | 4da1b000 | none |

## Adjusted Readiness Dispositions

| ID | Disposition | Evidence |
|---|---|---|
| P1-07 | readiness diagnostic retained missing facade call-time fail-fast and added production graph visibility | `./scripts/test_with_guard.sh ./internal/app ./internal/contract -count=1`; merge `4da1b000` |

## Guard-Only Dispositions

| ID | Disposition | Evidence |
|---|---|---|
| P1-32 | default and strict code-size guard paths enforce function comments | `make guard`; `./scripts/test_with_guard.sh ./internal/archtest -count=1`; merge `4da1b000` |
| P2-01 | production runtime nil ToolProvider path not applicable; constructor/graph guard retained | `internal/mcpserver/common/server_test.go`; production constructors checked in `cmd/mcp-lsp/fx.go` and `cmd/mcp-orch/runtime.go`; merge `4da1b000` |
| P2-24 | guard coverage/name gap closed through full guard target and script tests | `make guard`; scripts guard tests; merge `4da1b000` |
| P2-27 | critical frontend skips removed and critical-skip guard added | `node frontend-app/scripts/no-critical-skip.mjs`; frontend tests; merge `4da1b000` |
| P2-28 | fx.Invoke guard skeleton replaced with AST fixtures | `./scripts/test_with_guard.sh ./internal/archtest -count=1`; merge `4da1b000` |
| P3-04 | guard naming/coverage clarified through Makefile/CI/pre-push gates | `make guard`; scripts guard tests; merge `4da1b000` |

## Evidence-Only Dispositions

| ID | Disposition | Evidence |
|---|---|---|
| P3-07 | evidence index created for every active, adjusted, guard-only, and evidence-only queue ID | `python3 scripts/validate_risk_evidence.py --plan docs/pians/2026-06-29-production-risk-remediation-plan.md --evidence docs/pians/2026-06-29-production-risk-remediation-evidence.md` |
