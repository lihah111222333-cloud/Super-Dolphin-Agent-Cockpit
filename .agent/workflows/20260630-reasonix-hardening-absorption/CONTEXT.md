# Context

The source review concluded that V3 already absorbed the main Reasonix architecture boundaries. The remaining useful work is hardening, not migration.

Required absorption lanes:

- Lane A: create a single owner for plan/read-only/tool-trust policy. It must separate read-only, plan-safe, trust source, process-control, writer, memory mutation, approval finalizer, and shell command safety.
- Lane B: extract deterministic session/provider artifact path derivation into a leaf helper package without changing path layouts or data ownership.
- Lane C: run a writer preview contract spike. It inventories model-callable writers before any optional preview interface is designed.

Explicit non-absorption lanes:

- Provider wire-normalization remains conditional on future API chat providers.
- Tool schema canonical-cache remains conditional on measured performance, schema drift, or provider-wire validity evidence.
- Reasonix global registry, blank import wiring, event bus, workers, site, and accounts shells are not applicable to the V3 Fx/module/provider runtime.

Repository constraints:

- Source code and tests are the fact source.
- Keep docs-only review state separate from production readiness.
- Do not start implementation on `main` without explicit approval.
- Use isolated worktrees for implementation lanes.
- Preserve unrelated work, including current dirty files and matching temporary stash snapshots.
- Use LSP navigation for shared behavior changes before editing.
- Add focused tests before production code for non-trivial Go changes.
- Run `./scripts/test_with_guard.sh` on changed Go files/packages, then the lane-specific gates.

Current local state observed during orchestration:

- The restored current worktree contains this workflow package and the source plan as untracked files.
- Unrelated edits under `.githooks/`, `docs/plans/2026-06-29-reasonix-design-absorption-plan.md`, and `scripts/guard_fix_commits_have_tests_*` are currently dirty and are also preserved in temporary stash commit `30dc0e34fc4d4e45151ccd4ecd098d49f694f673`; do not edit, stage, restore, pop, or drop them for this workflow.
- The original source plan file is untracked: `docs/plans/2026-06-30-reasonix-hardening-absorption-review.md`.
- The execution-relevant source-plan content is copied into `SOURCE_PLAN_SNAPSHOT.md` so isolated worktree workers can proceed from the workflow package even if the original untracked plan is absent.
- This workflow package must not stage or commit those unrelated dirty or stash-preserved files unless the user explicitly asks.

Important source anchors confirmed before writing this workflow:

- `internal/platform/toolbridge/types.go` defines `ToolCallRequest` without a stage/mode field.
- `internal/platform/toolbridge/host_tools.go` defines `HostToolRegistry` and workflow template host tools.
- `internal/dto/mcp/tool.go` defines `MCPTool` without `readOnlyHint`.
- `internal/provider/codexapp/history_rollout.go` builds the Codex rollout glob with explicit Codex home semantics.
- `internal/util/historyjsonl/history.go` has a separate Codex history discovery path with empty-home fallback to `~/.codex`.
- `internal/module/thread/scratchpad.go` owns managed scratchpad path, sanitize, and cleanup behavior today.
