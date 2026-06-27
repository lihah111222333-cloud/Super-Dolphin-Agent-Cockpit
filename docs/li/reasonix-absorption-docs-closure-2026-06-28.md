# Reasonix Absorption Docs Closure 2026-06-28

## Scope

This closure record is docs-only. It reviews the current Reasonix absorption plan, ADR, spike notes, and process constraints in `codex/reasonix-docs-closure-20260628`; it does not claim any production implementation phase is complete.

## Checked Materials

| File | Closure finding |
| --- | --- |
| `docs/li/2026-06-27-reasonix-architecture-absorption-plan.md` | V2 plan exists and keeps the right phase order: ADR, read-only spikes, then code phases. Its checkbox sections are implementation instructions, not current status. |
| `docs/adr/0002-session-ports-and-prefix-stability.md` | Present. Status remains `Proposed`; it records boundary-only absorption, phase gates, and rollback rules. |
| `docs/adr/0003-mcp-tool-lifecycle-state.md` | Not present in this lane because it is based on `main@9d7cda57`. Sibling lane `codex/reasonix-mcp-lifecycle-20260628` commit `42383b92` adds this ADR and should supply the lifecycle owner/storage/migration decision during integration. |
| `docs/li/reasonix-absorption-spikes/event-wire-methods.md` | Present. It identifies `internal/platform/eventsurface` and `ExpandNotifications` as the event-wire source of truth. |
| `docs/li/reasonix-absorption-spikes/prompt-prefix-shape.md` | Present. It anchors prefix-shape work to the existing prompt assembly facts rather than a parallel prompt model. |
| `docs/li/reasonix-absorption-spikes/mcp-tool-lifecycle.md` | Present. It limits current absorption to namespace ownership and compatibility tests, not lifecycle filtering. |
| `docs/plans/迁移/lsp-advanced-guide.md` | Reviewed as a process constraint for later code phases. No edit is needed in this docs-only lane. |

## Closure Checklist

- [x] Phase 0 ADR file exists.
- [x] Phase 1 spike directory contains the three required spike documents.
- [x] Phase 0/1 preflight checks pass in this isolated docs lane.
- [ ] MCP lifecycle decision is not closed in this lane alone; it becomes closed only after integrating sibling lane `42383b92`.
- [x] This lane does not modify production code.
- [ ] Phase 2+ implementation phases are not closed by this document.
- [ ] MCP per-tool lifecycle production filtering remains blocked until ADR 0003 from the sibling lane is integrated and its implementation gates are satisfied.

## Follow-On Route

1. Treat ADR 0002 plus this lane's three spike docs as the closed Phase 0/1 preflight on `main@9d7cda57`.
2. When integrating `codex/reasonix-mcp-lifecycle-20260628`, carry forward sibling commit `42383b92`; then ADR 0003 status should be recorded as supplied by that lane, not requested as a new ADR.
3. Execute Phase 2 next only in a separate implementation lane with the plan's session-port parity tests and per-file Go guards.
4. Keep Phase 5 limited to namespace helper centralization until the integrated ADR 0003 gates allow lifecycle state to affect production toolbridge filtering.
5. Keep `cmd/mcp-orch`, generated provider mirrors, and `cmd/agent-terminal/frontend` out of scope unless a later approved plan changes that boundary.
