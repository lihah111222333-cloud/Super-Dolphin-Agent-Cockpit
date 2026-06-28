# Reasonix Remaining Execution Closure 2026-06-28

## Scope

This document reconciles `docs/li/2026-06-27-reasonix-architecture-absorption-plan.md`
against the current `main` branch after the Reasonix first-wave and second-wave
merge commits:

- `e57ee26d` `merge: 集成 Reasonix 第一波`
- `b42b1e9e` `merge: 集成 Reasonix 第二波 prompt`
- `9601e12e` `merge: 集成 Reasonix 第二波 toolbridge`

The plan document remains an implementation plan. Its unchecked task boxes are
historical instructions and must not be used as the live completion ledger. This
file is the current closure ledger.

## Reading Rules

- Reasonix architecture absorption Phase 0-7 is closed on current `main`.
- Final controller verification is closed for the Reasonix absorption plan's
  stated verification surface.
- Planned filename deviations are accepted when the same contract is implemented
  and covered by tests.
- Phase 5 was originally closed for namespace ownership and compatibility. The
  separate ADR 0003 MCP lifecycle backend/toolbridge follow-up has now also
  been completed on current `main`.
- ADR 0003 is closed through schema/store, owner-module API, discovery/backfill,
  toolbridge list filtering, direct-call denial, compatibility tests, and merge
  verification. UI lifecycle controls remain out of scope.

## Phase Status

| Phase | Current status | Evidence files | Closure note |
| --- | --- | --- | --- |
| Phase 0: ADR and execution gate | Complete. | `docs/adr/0002-session-ports-and-prefix-stability.md`; `docs/li/reasonix-absorption-docs-closure-2026-06-28.md` | ADR 0002 remains `Proposed`, but it records the accepted boundary and rollback gate for this absorption work. |
| Phase 1: read-only spikes | Complete. | `docs/li/reasonix-absorption-spikes/event-wire-methods.md`; `docs/li/reasonix-absorption-spikes/prompt-prefix-shape.md`; `docs/li/reasonix-absorption-spikes/mcp-tool-lifecycle.md`; `docs/adr/0003-mcp-tool-lifecycle-state.md` | Spike docs identify the source of truth for event wire, prompt prefix shape, and MCP lifecycle ownership. |
| Phase 2: typed session lifecycle/read ports | Complete. | `internal/contract/session.go`; `internal/contract/session_ports_test.go`; `internal/module/thread/contract_adapter.go`; `internal/module/thread/session_ports_test.go`; `internal/module/thread/session_ports_rpc_test.go`; `internal/module/thread/rpc.go`; `internal/app/session_ports.go`; `internal/app/session_ports_test.go`; `internal/app/modules.go` | Planned filenames changed: contract types live in `internal/contract/session.go`; lifecycle/status adapters live in `internal/module/thread/contract_adapter.go`. LSP references show `SessionPorts` is consumed by thread start/list/messages/fork/resume RPC handlers. |
| Phase 3: event wire contract | Complete. | `internal/platform/eventsurface/methods.go`; `internal/platform/eventsurface/methods_test.go`; `internal/platform/rpc/push.go`; `internal/platform/rpc/push_test.go`; `frontend-app/src/shared/api/eventWireMethods.js`; `frontend-app/src/shared/api/eventWire.js`; `frontend-app/src/shared/api/eventWire.test.js`; `frontend-app/src/shared/api/wailsBridge.js` | Backend typed method list, raw allowlist, compatibility prefixes, frontend parser, RPC push, and Wails bridge are covered by parity tests. |
| Phase 4: prompt prefix shape telemetry | Complete. | `internal/dto/provider/session.go`; `internal/contract/prompt.go`; `internal/module/prompt/assembler.go`; `internal/module/prompt/prefix_shape_test.go`; `internal/provider/codexapp/driver.go`; `internal/provider/codexapp/driver_session_test.go` | `PrefixShape` is on start assembly, not turn request; provider logging uses only shape metadata and tests guard prompt body leakage. |
| Phase 5: MCP namespace helper and lifecycle boundary | Complete. | `internal/platform/toolbridge/mcp_namespace.go`; `internal/platform/toolbridge/mcp_namespace_test.go`; `internal/platform/toolbridge/handler_peer_decode.go`; `internal/platform/toolbridge/handler_peer_decode_helpers.go`; `internal/platform/toolbridge/handler_host_tools.go`; `docs/adr/0003-mcp-tool-lifecycle-state.md` | Namespace helper is centralized. The original Reasonix phase closed on namespace ownership; the later ADR 0003 backend/toolbridge follow-up has also closed through owner API, backfill, list filtering, direct-call denial, and compatibility tests. |
| Phase 6: desktop dependency guard | Complete. | `internal/archtest/desktop_dependency_test.go`; `internal/archtest` helper files used by that test | Guard excludes allowed desktop assembly surfaces and catches Wails/UI dependency leakage into core runtime and MCP sidecars. |
| Phase 7: frontend session API facade | Complete. | `frontend-app/src/shared/api/sessionApi.js`; `frontend-app/src/shared/api/sessionApi.test.js`; `frontend-app/src/entities/client/model/useClientStore.js`; `frontend-app/src/pages/workflows/services/workflowPageService.js` | Facade delegates to guarded `backendApi.js` exports and tests reject raw bridge usage. Implementation added compatibility aliases used by existing callers. |
| Final verification section | Complete. | Section 8 of the plan; main merge verification record | Controller F ran final verification on current `main`: `make sqlc-verify`; package guard for the plan verification surface; `make guard`; `make build-plain`; `cd frontend-app && npm run lint && npm test && npm run build`; `git status --short --branch`. |

## ADR 0003 MCP Lifecycle Follow-Up State

ADR 0003 is not the same as the original Reasonix Phase 5 namespace absorption.
The original Reasonix phase closed on namespace ownership first; current `main`
has since completed the backend/toolbridge lifecycle follow-up:

- `internal/platform/db/sqlite/migrations/109_mcp_tool_lifecycle_states.sql`
- `sql/queries/mcp_tool_lifecycle.sql`
- root `sqlc.yaml` schema/query wiring
- generated `internal/store/sqlc/*` lifecycle query/model code
- `internal/contract/mcp_control.go` lifecycle DTO and store interfaces
- `internal/store/mcpserver/lifecycle_store.go`
- `internal/store/mcpserver/lifecycle_store_test.go`
- `internal/module/mcp_server/lifecycle.go`
- `internal/module/mcp_server/lifecycle_service_test.go`
- `internal/module/mcp_server/rpc_test.go`
- `internal/platform/toolbridge/handler_host_tools.go`
- `internal/platform/toolbridge/host_tools_lifecycle_test.go`
- `internal/platform/toolbridge/handler_peer_decode.go`
- `internal/platform/toolbridge/handler_peer_decode_helpers.go`
- `internal/platform/toolbridge/codex_surface_lifecycle_test.go`
- `internal/provider/e2e/lifecycle_wire_test.go`
- `internal/provider/codexapp/lifecycle_wire_test.go`
- `internal/provider/unified/manifest_test.go`
- `internal/contract/mcp_control_test.go`
- `internal/platform/toolbridge/http_mcp_client_test.go`
- `internal/platform/toolbridge/stdio_mcp_client_test.go`

LSP references confirm `contract.MCPToolLifecycleStore` is implemented by
`internal/store/mcpserver`, consumed by `internal/module/mcp_server`, and
adapted as the read-only lifecycle port for `internal/platform/toolbridge`.
LSP references also confirm `filterManagedPeerToolsByLifecycle` is reached from
the Codex list path and `ensureCodexSurfaceMCPToolActive` is reached from the
Codex surface tool call path.

## Current Evidence Notes

- `rg --files` shows ADR 0002, ADR 0003, all three spike docs, event wire
  files, session facade files, prompt prefix tests, MCP namespace helper files,
  MCP lifecycle schema/store files, and desktop dependency guard files in the
  current tree.
- LSP symbol lookup finds `contract.SessionPorts` in
  `internal/contract/session.go`, `thread.NewSessionPorts` in
  `internal/module/thread/contract_adapter.go`, and `app.newSessionPorts` in
  `internal/app/session_ports.go`.
- LSP references show `SessionPorts` is consumed by
  `internal/module/thread/rpc.go`, including start, list, messages, fork, and
  resume handlers.
- LSP diagnostics for the key Reasonix absorption files returned no diagnostics
  during final document review.

## Follow-On Conditions

- Do not reopen the Reasonix architecture absorption plan unless a new review
  finds a concrete regression in the merged code.
- Do not reopen ADR 0003 backend/toolbridge lifecycle work unless a new review
  finds a concrete regression in the merged code.
- Treat UI lifecycle controls or display as future product work, not as a
  missing backend/toolbridge gate.
