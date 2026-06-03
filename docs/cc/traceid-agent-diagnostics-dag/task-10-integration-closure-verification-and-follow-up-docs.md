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
- record RPC parity for `observability/trace/diagnose` as a follow-up unless the task is explicitly re-scoped;
- record `.agent/skills/trace-diagnosis/SKILL.md` as a follow-up unless the task is explicitly re-scoped.

## Requirements

- RPC parity is useful for UI and future peer forwarding, but not required for the first Codex-facing fix.
- Do not implement RPC parity in T10 without explicit re-scoping; if it is implemented later, route it through the same `DiagnoseTrace` platform API.
- Keep `observability/trace/get` as the raw event query.
- If the follow-up project skill is implemented later, it must instruct agents to use the tool, not parse JSONL directly.
- Final report must include the implemented default limit, maximum limit, diagnosis payload bound, tail max bytes, tail timeout, and tail concurrency.

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
