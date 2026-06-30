# Evidence

## 2026-06-30 Orchestration Setup

Status: orchestration only. No production implementation started.

Commands and tool checks used:

```bash
git status --short
rg --files | rg '(^AGENTS.md$|README.md$|docs/doc/codemap/README.md|\.agent/workflows|docs/plans/2026-06-30)'
find .agent/workflows -maxdepth 3 -type f 2>/dev/null | sort | tail -80
```

LSP/source checks used:

- `structure(document_symbol)` on `internal/platform/toolbridge/types.go`.
- `grep(text_search)` for `type ToolCallRequest`, `type HostToolRegistry`, `type MCPTool`, rollout glob literals, and scratchpad references.
- `file(read_file)` on `ToolCallRequest`, `HostToolRegistry`, `MCPTool`, Codex rollout discovery, historyjsonl Codex discovery, and thread scratchpad helpers.
- `xref(references)` for `ToolCallRequest` and `sanitizeScratchpadPath`.

Observed facts:

- `ToolCallRequest` currently has no stage/mode field.
- `MCPTool` currently has no read-only hint field.
- Codex rollout glob logic exists in both provider-local rollout reading and util history fallback reading, with different fallback semantics.
- Managed scratchpad path/sanitize/cleanup helpers are currently in `internal/module/thread/scratchpad.go`.
- The current worktree contains unrelated modified files and an untracked source plan.

Verification not run:

- No Go tests were run because this turn created orchestration docs only.

## 2026-06-30 Orchestration Review Fixes

Status: workflow-document fixes only. No production implementation started.

Fixed review findings:

- Approval gate and handoff now require an isolated implementation worktree, not a plain branch in the dirty main checkout.
- C1 writer-preview spike can only write one named ADR or one named plan amendment, not the whole `docs/plans/` directory.
- A0 must update A3's exact ownership and verification commands before A3 can start; A3 remains blocked without those concrete paths.
- README topology now matches `DAG.json`: Lane B and Lane C stay behind their declared dependencies.
- B1 dependency guard and B2 literal-placement guard responsibilities are split in ownership and task text.

Verification:

```bash
jq . .agent/workflows/20260630-reasonix-hardening-absorption/DAG.json >/dev/null
jq . .agent/workflows/20260630-reasonix-hardening-absorption/STATE.json >/dev/null
git diff --check -- .agent/workflows/20260630-reasonix-hardening-absorption
```

The red-flag placeholder scan was run against the workflow directory with evidence self-matches excluded.

## 2026-06-30 Continued Orchestration Review Fixes

Status: workflow-document fixes only. No production implementation started.

Fixed review findings:

- `DAG.json` now makes `B1-sessionpaths-core` depend on the Lane A implementation nodes, matching the README serial topology.
- Added `SOURCE_PLAN_SNAPSHOT.md` so isolated implementation worktrees can read the execution-relevant source plan content from the workflow package even if the original untracked plan file is absent.
- Replaced overlapping `internal/archtest/*sessionpaths*` / `*path*` ownership globs with explicit files: `sessionpaths_dependency_guard_test.go` and `sessionpaths_literal_guard_test.go`.
- Gate 0 now says tasks have concrete verification commands or an explicit pre-start blocker when A0 must fill exact delegation commands.

Verification:

```bash
jq . .agent/workflows/20260630-reasonix-hardening-absorption/DAG.json >/dev/null
jq . .agent/workflows/20260630-reasonix-hardening-absorption/STATE.json >/dev/null
git diff --check -- .agent/workflows/20260630-reasonix-hardening-absorption
```

## 2026-06-30 A0/A3 Ownership Boundary Fix

Status: workflow-document fix only. No production implementation started.

Fixed review finding:

- A0 no longer edits control files directly. It writes a patch-ready proposal into `CHECKS/EVIDENCE.md`; the orchestrator must then update `FILE_OWNERSHIP.tsv` and A3 verification commands before A3 can start.
- `FILE_OWNERSHIP.tsv` now lists A0's `CHECKS/EVIDENCE.md` append permission explicitly while keeping workflow control files under orchestrator ownership.
- A2/A3 now use `not_applicable_with_evidence` for the no-stage-source or no-delegation-entry cases, so Lane B is not permanently blocked by an evidence-backed non-applicable Lane A subtask.

## 2026-06-30 Skeptical Review State-Machine Fixes

Status: workflow-document fix only. No production implementation started.

Fixed review findings:

- Defined `not_applicable_with_evidence` as a dependency-satisfying terminal state and kept `blocked` as non-terminal for downstream scheduling.
- Replaced remaining stale A2 stop-state wording with the evidence-backed non-applicable state for the no-stage-source case.
- Added `NOT_APPLICABLE_WITH_EVIDENCE` to worker return statuses.
- Defined how worker return statuses map to lowercase DAG states, keeping `DONE_WITH_CONCERNS` and `NEEDS_CONTEXT` non-terminal until orchestrator review.
- Replaced A1's broad `internal/archtest/` write permission with the exact `internal/archtest/toolpolicy_dependency_guard_test.go` path.
- Tightened dirty-worktree wording so implementation lanes must use isolated worktrees and final staging remains owned-file-only.

Verification:

- Custom structural validation: `OK workflow structural validation passed`.
- Custom whitespace/conflict-marker validation over untracked workflow files: `OK workflow whitespace/conflict-marker validation passed`.
- JSON validation: both workflow JSON files parse with `jq`.
- Tracked-diff whitespace check: `git diff --check -- .agent/workflows/20260630-reasonix-hardening-absorption` produced no output, but the custom whitespace scan is the authoritative check because these files are untracked.
- Red-flag scan: no matches outside `CHECKS/EVIDENCE.md`.

## 2026-06-30 Stash Restore Boundary Fix

Status: workflow-document fix only. No production implementation started.

Observed state:

- Restored this workflow package and source plan from temporary stash commit `30dc0e34fc4d4e45151ccd4ecd098d49f694f673` third-parent untracked snapshot.
- Restored only `.agent/workflows/20260630-reasonix-hardening-absorption/` and `docs/plans/2026-06-30-reasonix-hardening-absorption-review.md`.
- Unrelated `.githooks`, older plan, and guard test changes are current dirty files and are also preserved in the stash snapshot.

Fixed review finding:

- `STATE.json` reports the dual boundary honestly: unrelated files are current dirty work and also stash-preserved; this workflow must not stage, edit, pop, or drop them.

Verification:

- Custom structural validation: `OK workflow structural validation passed`.
- Custom whitespace/conflict-marker validation over the workflow package and source plan: `OK workflow/source whitespace/conflict-marker validation passed`.
- JSON validation: `OK jq validation passed`.
- Red-flag scan outside `CHECKS/EVIDENCE.md`: no matches.
- Current unrelated dirty files remain outside workflow ownership: `.githooks/README.md`, `.githooks/pre-commit`, `docs/plans/2026-06-29-reasonix-design-absorption-plan.md`, `scripts/guard_fix_commits_have_tests_guard_test.go`, and `scripts/guard_fix_commits_have_tests_helpers_test.go`.
