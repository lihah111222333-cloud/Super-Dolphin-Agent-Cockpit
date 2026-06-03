# Backend Observability Readiness Review - 2026-06-03

## Scope

Fresh review of the backend observability tracing path, centered on `internal/platform/observability/service.go` and adjacent RPC/UI query paths.

This revision intentionally focuses only on risks that satisfy both gates:

- Majority support from the five independent review agents: more than half, so at least 3/5 votes.
- No effective upper-layer protection after a follow-up reachability review.

Old reports under `docs/cc/observability-tracing/**` were not used as finding sources.

## Review Inputs

Seed concern from the user:

- `internal/platform/observability/service.go:239-242`: identical `Query` can return an old cached tail result without reading log files again.
- `internal/platform/observability/service.go:287-292`: cache lookup has no TTL, file mtime/size validation, or force-refresh parameter.
- `internal/platform/observability/service.go:109-130`: new `Record` writes do not clear tail cache.

Independent reviews:

- Five initial agents reviewed production readiness, performance, risk/security/privacy/reliability, accuracy/query semantics, and maintainability.
- One follow-up review agent traced each finding through source paths and looked for upper-layer protections or calling constraints.

Tooling note: repository policy asks sub-agents to use `mcp-go-agent-orchestration`, but that tool was not exposed in this Codex session. Delegated review used `multi_agent_v1`.

## Verdict

Original review verdict: not production-ready for enabled-by-default tracing dashboards until the tail cache freshness bug is fixed. The current branch includes the W1 fix evidence recorded below, but production-readiness should remain gated on final package verification.

Only one finding passed the strict focus gate at review time: stale JSONL tail cache. It received 5/5 initial votes, the follow-up review confirmed it is reachable from the current UI/RPC paths, and no upper layer neutralized it before the W1 service fix.

## Implementation Status

Current W1/W4 implementation evidence:

- The chosen fix removes the persistent completed tail result cache from `internal/platform/observability/service.go`.
- In-flight coalescing is retained through `inflight map[Query]*tailCall`, `tailCall(...)`, and `finishTailCall(...)`, so concurrent identical tail reads can still share one active read without reusing completed results later.
- W1 added `TestServiceQueryTailDoesNotReuseStaleResult` in `internal/platform/observability/service_test.go`, covering two identical sequential `IncludeTail:true` queries whose tail reader returns different results and must be called twice.
- W4 locks UI/API reachability evidence in `frontend-app/src/pages/observability/ObservabilityPage.test.jsx`: recent search and trace drilldown payloads omit `includeTail`, so the RPC default remains reachable from the observability page. `frontend-app/src/shared/api/backendApi.test.js` already exactly covers the relevant API facade payload shapes without `includeTail:false`.

Final DAG status:

- Tasks 03-06 landed broader service JSONL and RPC freshness regressions on the integration branch.
- W5 final package verification completed on `work/obs-tail-verify`; exact commands and results are recorded below.
- Merge checklist outcome: W1-W4 worker branches had two review agents each, were committed in their worktrees, and were merged into `integration/obs-tail-cache-freshness`. The final integration range remained scoped to OBS-F01 files and docs; unrelated untracked files were not staged.

## Focus Finding

### OBS-F01 - High - Tail Cache Can Return Permanently Stale Results

Vote:

- Initial review: 5/5 agents raised this risk.
- Follow-up reachability review: Confirmed, high confidence, high production relevance.

Pre-fix evidence:

- `internal/platform/observability/service.go:42` stores `cache map[Query]QueryResult`.
- `internal/platform/observability/service.go:239-241` returns `cachedTail(query)` before any file read.
- `internal/platform/observability/service.go:263-265` stores every successful tail query result.
- `internal/platform/observability/service.go:287-292` reads from cache without TTL, mtime, size, generation, or force-refresh checks.
- `internal/platform/observability/service.go:294-300` only clears all cached results after 64 distinct queries.
- `internal/platform/observability/service.go:109-132` records new events to memory/sink without invalidating tail cache.

Reachable paths:

- UI recent logs:
  `frontend-app/src/pages/observability/ObservabilityPage.jsx:153-154`
  calls `listObservabilityRecent(...)`.
- Frontend API payload:
  `frontend-app/src/shared/api/backendApi.js:224-235`
  builds the recent payload without forcing `includeTail:false`.
- RPC recent handler:
  `internal/module/observability/rpc.go:161`
  creates `platformobs.Query{IncludeTail: includeTail(...)}`.
- RPC default:
  `internal/module/observability/rpc.go:490-497`
  returns `true` when `includeTail` is omitted.
- Service query:
  `internal/platform/observability/service.go:153-169`
  calls `queryTail` when `IncludeTail` is true.
- Pre-fix tail cache:
  `internal/platform/observability/service.go:239-241`
  returned old cached results for the same `Query`. W1 removed this persistent completed-result cache.

Trace drilldown follows the same pattern:

- `frontend-app/src/pages/observability/ObservabilityPage.jsx:105`
  calls `getObservabilityTrace({ traceId, limit })` without `includeTail:false`.
- `frontend-app/src/shared/api/backendApi.js:205-210`
  preserves omitted `includeTail`.
- `internal/module/observability/rpc.go:136`
  uses the same `includeTail(...)` default.

Pre-fix new writes were reachable but did not invalidate the cache:

- `internal/module/observability/rpc.go:191-217` frontend ingest records events through `svc.Record`.
- `internal/platform/observability/service.go:109-132` writes accepted events to the in-memory index and sink.
- Before W1, no cache clear, generation bump, or file watermark update occurred on that path.

Pre-fix upper-layer protections checked:

- Explicit `includeTail:false` would avoid the tail cache, but the current observability UI does not pass it for recent logs or trace drilldown.
- `limit` only bounds returned results; it does not force a tail refresh.
- `OBS_TRACING_ENABLED=false` disables tracing, but default config enables tracing when not explicitly disabled.
- Tail file guards and query timeout limit read cost, not freshness.
- The pre-fix 64-entry cache reset was incidental and unrelated to file freshness.

Pre-fix production impact:

The observability page can show normal-looking but stale results for recent logs and trace drilldown. The same cache path is also reachable through the slow/error RPC endpoints, even though those endpoints are not currently first-class controls on the observability page. Newly written JSONL events can remain invisible for repeated identical tail-backed queries until process restart or until enough unrelated query shapes clear the entire tail cache.

This directly breaks incident diagnosis: an operator can refresh the same trace or recent-log query and still miss new errors because the service returns the old `QueryResult`.

Required fix from review:

- Remove the persistent tail result cache, or make freshness explicit with TTL plus file path/size/mtime or sink generation validation.
- Add a force-refresh query option if the UI needs manual refresh semantics.
- Invalidate or advance a cache generation after successful sink writes, rotation, and retention.
- Add a regression test:
  1. Query with `IncludeTail:true` and cache an empty or partial tail result.
  2. Append a matching event directly to the JSONL tail source, or use a tail-reader spy that changes its returned result after the first call.
  3. Repeat the identical query.
  4. Assert the new tail event is returned and the tail reader was consulted again or cache freshness was invalidated. Do not rely on `Service.Record` alone for this regression, because `Record` updates the in-memory index and can mask stale tail cache behavior.

## Non-Focus Findings

These were intentionally removed from the main blocker list because they did not pass both focus gates.

| Finding | Follow-up verdict | Reason not in focus |
| --- | --- | --- |
| OBS-F02 tail read failures silently degrade to memory | Confirmed | Initial support was 2/5, below majority. Keep as follow-up reliability work. |
| OBS-F03 memory/tail query predicates disagree | Downgraded | Real service API inconsistency, but current RPC/UI does not build the problematic combination query. |
| OBS-F04 tail miss reads/decodes broadly before filtering | Confirmed | Initial support was 1/5. Bounded by tail byte/file guards; keep as performance follow-up. |
| OBS-F05 cache evicts by whole-map reset at 64 entries | Confirmed | Initial support was 1/5. Performance/reliability follow-up, not the primary readiness blocker. |
| OBS-F06 timeout does not cover single-file read/parse | Confirmed | Initial support was 1/5. Real but bounded by tail byte limits; keep as robustness follow-up. |
| OBS-F07 decode errors dropped from query responses | Confirmed | Initial support was 1/5. Silent partial-result risk, but not majority-voted. |
| OBS-F08 frontend ingest can persist non-secret sensitive content | Downgraded | Current frontend bridge has allowlists/sanitization; remaining risk requires bypass or future caller drift. |
| OBS-F09 `Query` coupled to map-key comparability | Confirmed | Compile-time maintainability risk, not runtime production readiness. |
| OBS-F10 direct constructors silently normalize invalid config | Downgraded | Production fx path uses env parsing and fails fast; direct constructor concern is maintainability consistency. |

## Verification

W5 final integration verification ran on `work/obs-tail-verify` at `4819712e` after W1-W4 integration.

```bash
./scripts/test_with_guard.sh ./internal/platform/observability ./internal/module/observability -count=1
```

Result: exit 0. Guard passed, and these packages reported `ok`:

- `github.com/anthropic-ai/super-agent-v3/internal/archtest`
- `github.com/anthropic-ai/super-agent-v3/internal/platform/observability`
- `github.com/anthropic-ai/super-agent-v3/internal/module/observability`

```bash
cd frontend-app
npm test -- ObservabilityPage.test.jsx backendApi.test.js
```

Result: exit 0. Vitest reported 2 passed files and 26 passed tests:

- `src/shared/api/backendApi.test.js` - 22 tests
- `src/pages/observability/ObservabilityPage.test.jsx` - 4 tests

The first frontend verification attempt in the W5 worktree failed because dependencies were not installed (`vitest: not found`). `npm ci` was run in `frontend-app`, then the same target test command passed. `npm ci` reported one critical audit finding; dependency audit remediation was outside this OBS-F01 verification scope.

No broader `make test`, `make build-plain`, frontend lint, or frontend build command was run for this W5 verification.

This document update is docs-only and does not change Go behavior.
