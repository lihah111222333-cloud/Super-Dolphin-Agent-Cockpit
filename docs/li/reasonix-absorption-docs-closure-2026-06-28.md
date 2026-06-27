# Reasonix Absorption Docs Closure 2026-06-28

## Scope

This closure record is maintained on integration branch `codex/reasonix-integration-20260628-current`. It preserves the original docs-closure review outcome, while recording the current integrated state: Phase 0/1 documentation preflight and the MCP lifecycle decision document are present, but this document does not claim every Phase 2+ production implementation and verification gate is complete.

## Checked Materials

| File | Closure finding |
| --- | --- |
| `docs/li/2026-06-27-reasonix-architecture-absorption-plan.md` | V2 plan exists and keeps the right phase order: ADR, read-only spikes, then code phases. Its checkbox sections are implementation instructions, not current status. |
| `docs/adr/0002-session-ports-and-prefix-stability.md` | Present. Status remains `Proposed`; it records boundary-only absorption, phase gates, and rollback rules. |
| `docs/adr/0003-mcp-tool-lifecycle-state.md` | Present in the current integration branch. Commit `42383b92` provides the lifecycle owner, storage, migration, and implementation-gate decision; ADR presence closes the decision document, not production filtering. |
| `docs/li/reasonix-absorption-spikes/event-wire-methods.md` | Present. It identifies `internal/platform/eventsurface` and `ExpandNotifications` as the event-wire source of truth. |
| `docs/li/reasonix-absorption-spikes/prompt-prefix-shape.md` | Present. It anchors prefix-shape work to the existing prompt assembly facts rather than a parallel prompt model. |
| `docs/li/reasonix-absorption-spikes/mcp-tool-lifecycle.md` | Present. It limits current absorption to namespace ownership and compatibility tests, not lifecycle filtering. |
| `docs/plans/迁移/lsp-advanced-guide.md` | Reviewed as a process constraint for later code phases. No edit is needed for this integration closure record. |

## Closure Checklist

- [x] Phase 0 ADR file exists.
- [x] Phase 1 spike directory contains the three required spike documents.
- [x] Phase 0/1 documentation preflight checks pass in the current integration branch.
- [x] MCP lifecycle decision documentation is closed in the current integration branch because ADR 0003 is integrated.
- [x] The original docs-closure lane changed documentation only; the current integration branch also contains later session, prefix, event-wire, and guard implementation lanes.
- [ ] Phase 2+ implementation phases are not closed by this document.
- [ ] MCP per-tool lifecycle production filtering remains blocked until a separate implementation lane satisfies ADR 0003's schema, backfill, compatibility, toolbridge, direct-call, and verification gates.

## Follow-On Route

1. Treat ADR 0002, ADR 0003, and the three spike docs as the current integrated documentation preflight.
2. Record commit `42383b92` as the already-integrated source of ADR 0003 for lifecycle ownership and storage decisions.
3. Continue Phase 2+ work through implementation-specific lanes with their required parity tests, per-file Go guards, and package-level verification.
4. Keep Phase 5 lifecycle filtering out of production paths until a later implementation lane satisfies ADR 0003's gates; ADR existence alone does not make filtering live.
5. Keep `cmd/mcp-orch`, generated provider mirrors, and `cmd/agent-terminal/frontend` out of scope unless a later approved plan changes that boundary.
