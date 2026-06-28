# Reasonix Absorption Docs Closure 2026-06-28

## Scope

This closure record was originally maintained on integration branch `codex/reasonix-integration-20260628-current`. It now records the current `main` state after the Reasonix first-wave and second-wave merge commits.

Remaining execution state is tracked in `docs/li/reasonix-remaining-execution-closure-2026-06-28.md`. That record now closes the Reasonix architecture absorption phases and the later ADR 0003 MCP lifecycle backend/toolbridge follow-up work.

## Checked Materials

| File | Closure finding |
| --- | --- |
| `docs/li/2026-06-27-reasonix-architecture-absorption-plan.md` | V2 plan exists and keeps the right phase order: ADR, read-only spikes, then code phases. Its checkbox sections are implementation instructions, not current status. Current completion state lives in `reasonix-remaining-execution-closure-2026-06-28.md`. |
| `docs/adr/0002-session-ports-and-prefix-stability.md` | Present. Status remains `Proposed`; it records boundary-only absorption, phase gates, and rollback rules. |
| `docs/adr/0003-mcp-tool-lifecycle-state.md` | Present. It provides lifecycle owner, storage, migration, and implementation-gate decisions. The later MCP lifecycle implementation has now satisfied the backend/toolbridge gates described by this ADR. |
| `docs/li/reasonix-absorption-spikes/event-wire-methods.md` | Present. It identifies `internal/platform/eventsurface` and `ExpandNotifications` as the event-wire source of truth. |
| `docs/li/reasonix-absorption-spikes/prompt-prefix-shape.md` | Present. It anchors prefix-shape work to the existing prompt assembly facts rather than a parallel prompt model. |
| `docs/li/reasonix-absorption-spikes/mcp-tool-lifecycle.md` | Present. It correctly limited the original Reasonix absorption to namespace ownership first; current `main` also contains the later lifecycle filtering and direct-call gate implementation. |
| `docs/plans/迁移/lsp-advanced-guide.md` | Reviewed as a process constraint for later code phases. No edit is needed for this integration closure record. |

## Closure Checklist

- [x] Phase 0 ADR file exists.
- [x] Phase 1 spike directory contains the three required spike documents.
- [x] Phase 0/1 documentation preflight checks pass on current `main`.
- [x] MCP lifecycle decision documentation is closed on current `main` because ADR 0003 is integrated.
- [x] The original docs-closure lane changed documentation only; current `main` also contains later session, prefix, event-wire, guard, and MCP schema/store implementation lanes.
- [x] Reasonix Phase 2+ implementation phases are closed by `docs/li/reasonix-remaining-execution-closure-2026-06-28.md` on current `main`.
- [x] MCP lifecycle schema/store first slice is integrated on current `main`.
- [x] MCP per-tool lifecycle backend/toolbridge filtering is integrated on current `main` through ADR 0003's owner API, backfill, compatibility, toolbridge, direct-call, and verification gates.

## Follow-On Route

1. Treat ADR 0002, ADR 0003, and the three spike docs as the current integrated documentation preflight.
2. Treat `docs/li/reasonix-remaining-execution-closure-2026-06-28.md` as the live Reasonix absorption closure ledger.
3. Treat `docs/li/mcp-tool-lifecycle-execution-plan-2026-06-28.md` as the closed ADR 0003 backend/toolbridge lifecycle execution ledger.
4. Treat lifecycle UI controls or display as future product work, not as missing Reasonix or ADR 0003 backend scope.
5. Keep `cmd/mcp-orch`, generated provider mirrors, and `cmd/agent-terminal/frontend` out of scope unless a later approved plan changes that boundary.
