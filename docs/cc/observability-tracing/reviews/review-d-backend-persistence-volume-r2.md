# Review D Backend/Persistence/Volume R2

## 1. Verdict

approve-with-required-changes

The plan is broadly feasible for backend implementation, JSONL-first persistence, bounded in-memory query indexes, privacy controls, fx/RPC integration, and the no-PG/no-SQLite Phase 1 constraint. The required changes below should be made before implementation agents treat the document as execution-ready.

## 2. True positives / required changes

### Finding 1

- Severity: high
- Evidence:
  - `docs/cc/observability-tracing/00-implementation-plan.md:89-93` places trace files directly under `~/.super-dolphin/log/<project>/trace-YYYY-MM-DD.jsonl`.
  - `pkg/logger/logger.go:189-207` creates the existing log directory with `0755` and current app log files with `0644`.
  - `docs/cc/observability-tracing/00-implementation-plan.md:555-563` requires owner-only trace directory/file permissions and retention pruning.
- Why it matters: Directly writing trace JSONL into the existing project log directory makes the “trace directory owner-only” requirement ambiguous and risks inheriting the existing logger convention, which is not owner-only. Trace JSONL is more privacy-sensitive than ordinary logs and needs a separate permission boundary and pruning scope.
- Exact recommended doc change: In §3.2 and §9, change the Phase 1 path to `~/.super-dolphin/log/<project>/traces/trace-YYYY-MM-DD.jsonl`. Require `internal/platform/observability` to create `traces/` with `0700` on Unix-like platforms and trace files with `0600`; do not rely on `pkg/logger.InitWithFile` permissions. Retention/pruning must operate only inside that `traces/` directory and only on exact `trace-*.jsonl` matches.

### Finding 2

- Severity: high
- Evidence:
  - `internal/platform/rpc/server.go:69-75` captures `ParamsPreview` from raw RPC params.
  - `internal/platform/rpc/server.go:110-123` emits `params_preview` on RPC failure logs.
  - `docs/cc/observability-tracing/00-implementation-plan.md:399-400` says Wails/RPC traces must not include full raw params and should include method, param length, and safe param keys.
  - `docs/cc/observability-tracing/00-implementation-plan.md:310-335` forbids prompt/file/tool-result payload persistence and requires sanitizer coverage for persisted strings.
- Why it matters: A 160-byte raw params preview can still contain prompt/user text or sensitive identifiers. If implementation agents reuse the existing RPC request tracker fields for trace events, JSONL privacy guarantees are broken even without “full” params.
- Exact recommended doc change: In §7.3 and Task 2 acceptance, add: “Trace events must not reuse `rpcParamPreview`/`params_preview`. RPC trace metadata is limited to sanitized `method`, `param_keys`, `param_bytes`, correlation IDs, duration/status, and code anchor. Add a regression test where failed RPC params contain prompt/user text and assert that trace JSONL contains neither the text nor `params_preview`.”

### Finding 3

- Severity: medium
- Evidence:
  - `internal/app/modules.go:45-77` explicitly enumerates every app/platform/module Fx module; new modules are not auto-discovered.
  - `internal/platform/rpc/module.go:99-128` registers RPC handlers only from `rpc.HandlerMapResult` values already present in the Fx graph.
  - `docs/cc/observability-tracing/00-implementation-plan.md:752-754` only says to modify “internal/app or module fx wiring as appropriate.”
- Why it matters: The observability service and dashboard RPC handlers will not exist unless both the platform service module and the module-level RPC adapter are explicitly added to the app Fx graph. Leaving this vague creates a realistic implementation miss, especially because `rpc.Module` only aggregates registered handler maps.
- Exact recommended doc change: In Task 1/Task 6, explicitly list `Modify: internal/app/modules.go` and require adding `internal/platform/observability.Module` plus `internal/module/observability.Module`. State that `internal/module/observability` must return `rpc.HandlerMapResult` for `observability/*` handlers so `rpc.registerAllHandlers` registers them.

## 3. Non-issues verified

- Wails traceparent parsing and strict-handler metadata stripping are real: `internal/ui/wails/binding.go:54-62` parses context and strips `_ao*`; `internal/ui/wails/binding.go:199-221` validates `_aoTraceparent` and attaches trace IDs to context.
- `Server.Dispatch` is the correct local Wails bridge boundary: `internal/platform/rpc/server.go:266-288` executes registered handlers through a local jrpc2 server.
- The plan’s no-SQLite Phase 1 guarantee matches current dependencies: `go.mod:5-23` has no SQLite driver; existing `pgx` at `go.mod:10` is the current app database dependency, not a new tracing dependency.
- Existing `internal/util/historyjsonl/history.go:33-56` is provider-history JSONL reading, not a suitable trace sink; the plan correctly creates a new `internal/platform/observability` package instead of reusing it as durable trace storage.
- Bounded-index and sanitizer requirements are explicit enough in principle: `00-implementation-plan.md:267-277` requires hard capped indexes and safe eviction; `00-implementation-plan.md:212-217` and `310-335` require sanitizer coverage before JSONL/index persistence.
- Log-volume controls are explicitly addressed: `00-implementation-plan.md:279-308` separates always-kept events from sampled/summary-only high-frequency events.

## 4. Safe for implementation agents?

Safe after the three proposed document changes above. Without them, implementation agents could place private trace files in the existing non-owner-only log directory, accidentally copy existing `params_preview` behavior into JSONL, or forget to wire the new Fx/RPC modules into the app graph.
