# TraceId Agent Diagnostics DAG - 2026-06-03

Source finding:

- `docs/cc/agent-trace-diagnostics/traceid-agent-diagnostics-finding-2026-06-03.md`

Goal:

- Turn a user-provided `trace_id` into a Codex-discoverable, schema-validated, bounded, redacted diagnosis.
- Prevent agents from guessing local JSONL paths or exposing raw trace events to the model.

## DAG Nodes

| Node | Task | Depends On | Primary Packages |
| --- | --- | --- | --- |
| T01 | Trace diagnosis contract | none | `internal/platform/observability` |
| T02 | Query freshness and predicate alignment | T01 | `internal/platform/observability` |
| T03 | Tail degradation and warning propagation | T01, T02 | `internal/platform/observability` |
| T04 | Redacted model-facing projection | T01 | `internal/platform/observability` |
| T06 | Host dispatch output and CWD contract | T01, T08 | `internal/platform/toolbridge` |
| T05 | Host-direct trace tool registry | T03, T04, T06 | `internal/platform/toolbridge` |
| T07 | Codex surface wiring, gating, and duplicate handling | T05 | `internal/platform/toolbridge`, `internal/app` |
| T08 | Platform observability test gate | T02, T03, T04 | `internal/platform/observability` |
| T09 | Toolbridge and Codex surface test gate | T05, T07 | `internal/platform/toolbridge` |
| T10 | Integration closure, verification, and follow-up docs | T08, T09 | docs, verification |

## Completion Status

Integration branch: `feat/traceid-agent-diagnostics`.

| Node | Status | Integration Commit | Notes |
| --- | --- | --- | --- |
| T01 | Complete | `cfab2b5d` | Added platform diagnosis contract and initial diagnosis behavior. |
| T02/T03 | Complete | `a2bf7fb8` | Added fresh tail diagnosis, no completed-result cache, tail degradation and warning propagation. |
| T04 | Complete | `785e10e6` | Added bounded redacted model-facing projection. |
| T06 | Complete | `63822fd9` | Added CWD-optional host dispatch and structured host-tool output contract. |
| T05 | Complete | `cfd8e38d` | Registered `observability_trace_get` as a host-direct tool. |
| T07 | Complete | `3ea954e8` | Hardened Codex surface gating and reserved host-only duplicate/alias handling. |
| T08 | Complete | `a2bf7fb8`, `785e10e6` | Platform observability tests were committed with the behavior changes they lock. |
| T09 | Complete | `b9ca1b29` | Added Codex surface duplicate and trace schema test gate coverage. |
| T10 | Complete after closure verification | closure commit | Final docs update and final verification. |

Implemented tool contract:

- tool name: `observability_trace_get`;
- required input: `trace_id`;
- optional inputs: `limit`, `force_refresh`, `include_stack`;
- output: redacted `TraceDiagnosis` in `ToolCallResult.StructuredContent`, mirrored as JSON `inputText`;
- advertised only when tracing is enabled;
- disabled stale calls return degraded diagnosis rather than falling through to peer tools.

Implemented bounds and defaults:

- diagnosis default `limit`: 80;
- diagnosis maximum `limit`: 200;
- serialized diagnosis payload bound: 256 KiB;
- JSONL tail query window default: 20 MiB;
- JSONL tail timeout default: 750 ms;
- JSONL tail max concurrency default: 1.

## Edge List

```text
T01 -> T02
T01 -> T03
T01 -> T04
T01 -> T06
T02 -> T03
T03 -> T05
T04 -> T05
T06 -> T05
T05 -> T07
T02 -> T08
T03 -> T08
T04 -> T08
T05 -> T09
T07 -> T09
T08 -> T10
T08 -> T06
T09 -> T10
```

## Execution Rules

- Do not make agents parse `~/.super-dolphin/log/<project>/traces/*.jsonl` directly.
- Do not expose raw, unbounded `TraceEvent` data to a model-facing tool.
- Do not advertise `observability_trace_get` when tracing is disabled.
- If tail reads fail, return an explicit error or a degraded diagnosis; never return a clean diagnosis.
- If duplicate host-only tool names are returned by MCP peers, the Codex surface must not fail entirely.
- Treat T08 and T09 as test gates, not post-hoc test buckets; each behavior change must carry its own regression test in the same worktree commit.
- Maximum implementation parallelism is two worker agents; do not run three implementation workers against this DAG because the observability and toolbridge files are tightly shared.

## Safe Worktree Batches

| Batch | Worker A | Worker B | Notes |
| --- | --- | --- | --- |
| 0 | Repair DAG and contract docs | none | Completed before implementation starts. |
| 1 | T01 diagnosis contract | none | Freezes request fields, bounds, source/freshness, and raw-event ban. |
| 2 | T02 + T03 freshness/tail degradation with tests | T04 projection/redaction with tests | The only main two-worker implementation batch. |
| 3 | T08 platform test gate and observability integration check | optional read-only review | Must pass before toolbridge implementation starts. |
| 4 | T06 dispatch, CWD/result contract | none | Runs before T05 so the trace registry consumes a frozen host-dispatch contract. |
| 5 | T05 host registry + T07 wiring and duplicates | none | Kept single-worker to avoid toolbridge file conflicts. |
| 6 | T09 toolbridge test gate and cross-check | optional read-only review | Do not split writes across `handler_host_tools.go` and `handler_peer_decode.go`. |
| 7 | T10 final verification and follow-up report | none | RPC parity remains follow-up unless explicitly re-scoped. |

## Worktree Review And Merge Gate

Each implementation worktree must pass this gate before it can merge into the integration branch.

1. Implement only the assigned DAG node or batch in the worktree branch.
2. Run the verification command scoped to the changed packages.
3. Request two independent reviews against the worktree's uncommitted diff.
4. Both reviews must report no unresolved Critical or Important findings.
5. If either review raises a Critical or Important finding, fix it in the same worktree, rerun verification, and request two fresh reviews of the new diff.
6. After both reviews pass, commit the exact reviewed diff in the worktree branch.
7. Merge the committed worktree branch into `feat/traceid-agent-diagnostics`.
8. After the merge, rerun the affected verification command on the integration branch.

Required reviewer split:

- Reviewer A: production feasibility and correctness.
- Reviewer B: performance, security/privacy, risk, and maintainability.

Commit rules:

- Do not commit before both reviews pass.
- Do not change code between passing reviews and commit; any change invalidates the reviews.
- Do not use `git add .`; stage only the files owned by the worktree task.
- Do not merge uncommitted worktree changes.
- Keep non-reserved duplicate handling, disabled-tool behavior, and redaction rules covered by tests in the same worktree commit that changes them.

Suggested merge flow:

```bash
# In the worktree branch after implementation:
./scripts/test_with_guard.sh <affected packages> -count=1
git status --short
git diff -- <owned files>

# After two passing reviews:
git add <owned files>
git commit -m "<type>: <task summary>"

# In the integration worktree:
git switch feat/traceid-agent-diagnostics
git merge --no-ff <worktree-branch>
./scripts/test_with_guard.sh <affected packages> -count=1
```

## Verification Target

After all implementation tasks are complete:

```bash
./scripts/test_with_guard.sh ./internal/platform/observability ./internal/platform/toolbridge ./internal/module/observability -count=1
```

Follow-ups outside this DAG unless explicitly re-scoped:

- `observability/trace/diagnose` RPC parity through `internal/module/observability/rpc.go`;
- `.agent/skills/trace-diagnosis/SKILL.md` to instruct agents to call `observability_trace_get`;
- UI affordances that surface trace diagnosis actions directly to users.
