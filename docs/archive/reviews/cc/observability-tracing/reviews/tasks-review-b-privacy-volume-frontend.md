# Review B - Privacy / Volume / Frontend Task Plan Review

Scope: independently reviewed the 10 task docs in `docs/cc/observability-tracing/tasks/` against `00-implementation-plan.md` and current repo evidence, focusing only on privacy/sanitization, log volume/OOM controls, JSONL packaging/permissions/retention, React `frontend-app` truthfulness, forbidden payload fields, and verification completeness.

## Findings

### 1. Medium - Integration closeout validation omits several dependent backend surfaces

- **Affected file(s):** `docs/cc/observability-tracing/tasks/10-dashboard-query-ui-verification-docs.md`
- **Evidence:** Task 10 depends on all prior tasks, including `obs_04_fx_wiring_disabled_service`, `obs_08_provider_toolbridge_spans`, and `obs_09_bus_uistate_spans` (`tasks/10...md:9-10`). Its backend validation runs only `./internal/platform/observability ./internal/module/observability ./internal/ui/wails ./internal/platform/rpc ./internal/module/thread ./internal/module/turn ./internal/module/uistate` (`tasks/10...md:72-79`). That omits `./internal/app` required by Task 04 validation (`tasks/04...md:54-58`), `./internal/platform/toolbridge ./internal/platform/difftracker ./internal/provider/...` required by Task 08 validation (`tasks/08...md:64-70`), and `./internal/platform/bus` required by Task 09 validation (`tasks/09...md:60-64`). The main plan requires verification for frontend bridge/store and backend tracing surfaces and lists provider/toolbridge-related work as part of the trace chain (`00-implementation-plan.md:725-740`, `796-815`).
- **Why it matters:** Task 10 is the integration/verification closeout. If it does not rerun or explicitly require the validation for app wiring, bus/uistate, provider, toolbridge, and difftracker spans, regressions in privacy filtering, volume sampling, or Fx/RPC registration can pass the final review despite being part of the declared DAG dependency chain.
- **Exact recommended correction:** In Task 10 validation, either add the missing packages to the backend command or require running each prior task validation command. A concrete replacement is:

```bash
./scripts/test_with_guard.sh \
  ./internal/platform/observability \
  ./internal/module/observability \
  ./internal/app \
  ./internal/ui/wails \
  ./internal/platform/rpc \
  ./internal/module/thread \
  ./internal/module/turn \
  ./internal/platform/toolbridge \
  ./internal/platform/difftracker \
  ./internal/provider/... \
  ./internal/platform/bus \
  ./internal/module/uistate \
  -count=1
```

Keep Task 08's existing allowance to narrow provider packages if the provider-wide run is too broad or slow, but require the exact narrowed package list to be reported.

## Category checklist

- **Privacy/sanitization:** No high-confidence issue found in the task docs. Task 01 requires all persisted strings to pass sanitizer before memory/JSONL; Tasks 05-09 add path-specific privacy tests.
- **Log volume/OOM controls:** No high-confidence issue found. Tasks 02, 03, 08, and 09 carry bounded retention, tail-scan, index, sampling, and high-frequency summary requirements.
- **JSONL packaging/permissions/retention:** No high-confidence issue found. Task 02 matches the main plan path, owner-only modes, rotation, retention, and scoped pruning requirements.
- **React `frontend-app` truthfulness:** No high-confidence issue found. The repo currently has `frontend-app/package.json`, `frontend-app/src/App.jsx`, and `frontend-app/src/shared/api/wailsBridge.js`; `createTraceContext()` and `callAPI()` exist and inject W3C trace metadata, matching the main plan and Task 06.
- **Forbidden payload fields:** No high-confidence issue found. Task 05 explicitly forbids reuse of `rpcParamPreview` / `ParamsPreview` / `params_preview`, and Tasks 06-09 forbid frontend previews, prompts, model output, file contents, tool results, and patch payload bodies.
- **Verification completeness:** One high-confidence issue found above.
