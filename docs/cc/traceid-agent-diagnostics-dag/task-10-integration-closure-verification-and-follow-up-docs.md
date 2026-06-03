# T10 - Integration Closure, Verification, And Follow-Up Docs

Depends on: T08, T09

## Objective

Finish the implementation safely, verify the affected packages, and document follow-up paths.

## Source Anchors

- `internal/module/observability/rpc.go:106-137`
- `.agent/skills`
- `docs/cc/agent-trace-diagnostics/traceid-agent-diagnostics-finding-2026-06-03.md`

## Scope

Complete closure work after the host-direct MVP is implemented:

- run affected package tests through the guard wrapper;
- inspect any guard or baseline diff before reporting done;
- update this DAG or the source finding if implementation decisions differ;
- optionally add RPC parity for `observability/trace/diagnose`;
- optionally add `.agent/skills/trace-diagnosis/SKILL.md` so agents know to call `observability_trace_get` when users provide trace-like ids.

## Requirements

- RPC parity is useful for UI and future peer forwarding, but not required for the first Codex-facing fix.
- If RPC parity is implemented, route it through the same `DiagnoseTrace` platform API.
- Keep `observability/trace/get` as the raw event query.
- The project skill must instruct agents to use the tool, not parse JSONL directly.

## Acceptance Criteria

Run:

```bash
./scripts/test_with_guard.sh ./internal/platform/observability ./internal/platform/toolbridge ./internal/module/observability -count=1
```

Then report:

- changed files;
- verification command and result;
- any skipped follow-ups;
- any remaining risks.

