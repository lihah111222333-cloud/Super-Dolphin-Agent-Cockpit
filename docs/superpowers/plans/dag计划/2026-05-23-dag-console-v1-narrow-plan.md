# DAG Console v1 Narrow Implementation Plan

> **For agentic workers:** use `superpowers:subagent-driven-development` or `superpowers:executing-plans` task-by-task. Keep each task in an isolated worktree and do not push without current explicit approval.
> **Status 2026-05-23:** U1 is complete on local `main` through merge `81f0893b` (`merge: dag console e2e smoke`), with a follow-up seal to make selected-run nodes come from `dashboard/dagRun` / `task_get_run`. Do not expand this plan into U2/U3; use the follow-up section for remaining work.

## Goal

Build U1 from `docs/plans/dag-ui-decision-ledger.md`: a user-visible DAG Console that can list DAGs, inspect details, start a run, follow recent run history, open child thread links, and see `final_output`.

Completed scope:

- `fee60718` wired the dashboard DAG runtime bridge and backend guard alignment.
- `2912c602` added the two-pane DAG Console shell.
- `66afaffb` wired detail loading, Start, run history, node metadata, child thread navigation, and final output display.
- `f18bc138` added the focused Playwright smoke covering the console detail path; merge `81f0893b` integrated it into local `main`.
- Follow-up seal: selecting a run now loads the runtime run snapshot through `dashboard/dagRun`, so node status and `spawning_thread_id` come from `task_get_run` runtime nodes instead of template nodes.

This plan is intentionally narrower than U2/U3:

- No AI design button.
- No node edit form.
- No Mermaid/topology work.
- No TTL/soft-delete/bulk cleanup UI.
- No template library, fork preview, lineage, cost preview, HITL, external trigger UI, or multi-artifact bundle.

## Source Documents

- `docs/plans/dag-ui-decision-ledger.md`: authoritative UI scope and U1/U2/U3 split.
- `docs/plans/dag改造实施计划.md`: remaining T/F/H task accounting.
- `docs/plans/dag-lifecycle-c-a-implementation.md`: lifecycle, `final_output`, and sharedfile boundary.
- `docs/doc/codemap/01-terminal-ui-vue.md`: Vue entry points and reading boundaries.

## Product Contract

- `sharedfile` is storage/collaboration. It may hold final files, but it is not the only way users discover final results.
- User-facing final deliverables are indexed by run-level `metadata.final_output`.
- U1 keeps the single `final_output` pointer model. Bundle/multi-artifact output waits for real dogfood demand.
- Run detail nodes must be selected-run runtime nodes from `task_get_run`; template nodes are only a no-run fallback.
- Frontend must call dashboard RPCs; it must not call MCP tools directly.

## Task 0: Backend Guard Alignment — Complete

Completed by the mainline alignment slice:

- Preserve real `node.result` when `outputs.to_node_result=true` and the configured sharedfile already exists.
- Fail closed when Shared Files delete protection cannot check DAG `final_output` references.
- Add `dashboard/dagStart` as the Start CTA bridge to `contract.OrchestrationService.StartDAG`.

Verification:

```bash
./scripts/test_with_guard.sh ./internal/sidecar/orch/orchestration ./internal/app ./internal/module/memory ./internal/module/dashboard -count=1
```

## Task 1: DAG Console Shell — Complete

Files:

- `cmd/agent-terminal/frontend/vue-app/pages/DagsPage.js`
- `cmd/agent-terminal/frontend/vue-app/app.js`
- new focused components under `cmd/agent-terminal/frontend/vue-app/components/dag/`
- `cmd/agent-terminal/frontend/vue-app/dags-page.behavior.test.js`
- `cmd/agent-terminal/frontend/vue-app/app-root.behavior.test.js`

Requirements:

- Replace the current thin `DataPage` wrapper with a two-pane console.
- Keep modal only as transitional/narrow-screen behavior if needed.
- Show list fields needed for scanning: key/title/status/trigger/latest run/final output marker.
- Poll or refresh through existing dashboard bridge. Do not add WebSocket/subscription work in U1.

Verification:

```bash
cd cmd/agent-terminal/frontend
npx vitest run vue-app/dags-page.behavior.test.js vue-app/app-root.behavior.test.js
node scripts/size-guard.cjs
```

## Task 2: Detail, Start, Run History, Final Output — Complete

Files:

- `cmd/agent-terminal/frontend/vue-app/composables/useDagDetail.js`
- `cmd/agent-terminal/frontend/vue-app/pages/DagsPage.js`
- new components:
  - `components/dag/DagNodeList.js`
  - `components/dag/DagRunHistoryPanel.js`
  - `components/dag/DagFinalOutputPanel.js`

Requirements:

- Call `dashboard/dagStart` for Start.
- Show disabled reason when the DAG cannot be started.
- Show nodes with status, node type, provider/model/agent key when present, and child thread link via `spawning_thread_id`.
- Show recent runs and allow selecting a run.
- Selecting a run calls `dashboard/dagRun` and swaps the node list to that run's runtime node snapshot.
- Switch `final_output` display with the selected run.
- File outputs show path and open/read via existing Shared Files path-based RPC. Small text/json outputs show compact preview.
- `dashboard/dagRuns` errors must surface as an error state, not as "no final output".
- This is a recent-run panel, not a full event/error timeline. Event pagination and error summaries remain follow-up work.

Verification:

```bash
cd cmd/agent-terminal/frontend
npx vitest run vue-app/dag-detail.test.js vue-app/dags-page.behavior.test.js
npm run test:e2e -- tests/e2e/dag-console-detail.spec.js --project=chromium
node scripts/size-guard.cjs
```

## U3 Follow-Up: Shared Files / Retention / Cleanup

Shared Files work is intentionally outside U1. Keep the existing final-output highlight/filter behavior while building DAG Console v1.

U3 owns later Shared Files polish:

- stale-request guard for file viewer loading;
- clearer delete-protection errors;
- TTL, soft delete, bulk cleanup, pinned files, running-run protection UI, and export confirmation;
- reads/writes/lock_mode visualization.

## Full Verification

Executed for the completed U1 slice:

- In the feature worktree before merge: focused e2e, dashboard navigation e2e, frontend size guard, full frontend vitest, frontend build, and `git diff --check`.
- After merge `81f0893b` on local `main`: focused e2e + dashboard navigation e2e, frontend size guard, full frontend vitest, and frontend build.

Baseline command set:

```bash
./scripts/test_with_guard.sh ./internal/app ./internal/module/dashboard ./internal/module/memory -count=1
cd cmd/agent-terminal/frontend
npx vitest run
node scripts/size-guard.cjs
npm run build
```

If Go orchestration behavior changes in the same branch, also run:

```bash
./scripts/test_with_guard.sh ./internal/sidecar/orch/orchestration -count=1
```

## Completion Rule

Before merge-back:

- Run the verification above from the feature worktree.
- Request multi-agent review over backend, frontend, and docs.
- Merge into `main` with `git merge --no-ff`.
- Re-run decisive verification from `main`.
- Do not push without current explicit approval.
