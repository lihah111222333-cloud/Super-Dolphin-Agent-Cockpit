Severity: High
Section: §14 Operational Guardrails; §4.1/§12 Task 1 fx module wiring
Issue: `observability service is nil/unavailable -> instrumentation must no-op` makes missing fx wiring or failed service construction silently invisible.
Why true: The plan explicitly requires a nil/unavailable service to no-op (§14), while the repo policy requires fail-fast on abnormal/missing configuration and forbids silent fallback. In fx/module wiring, a missing provider or failed JSONL/index initialization would therefore look like a successful app with no traces, defeating the feature and hiding integration bugs.
Fix: Model observability as an explicit required fx dependency. If tracing is enabled, construction/wiring failures must return an error and stop startup. If tracing is intentionally disabled, bind an explicit `DisabledService` with status metadata so RPC/status reports `disabled`, not an accidental nil no-op.

Severity: Medium
Section: §4.1 Package Layout; §8 Dashboard Query API; §12 Task 6
Issue: The RPC ownership boundary is ambiguous: the plan allows either `internal/module/observability/rpc.go` or “platform-provided handlers”.
Why true: The core service is placed under `internal/platform/observability`, but Dashboard methods are application/module-facing RPC endpoints. Letting platform provide RPC handlers risks coupling platform infrastructure back upward into app/RPC registration concerns, while placing all query API in platform can blur module boundaries and fx ownership.
Fix: Keep `internal/platform/observability` limited to trace types, service, sinks, indexes, and config. Add a thin `internal/module/observability` RPC adapter that depends on the platform service and registers `observability/*` methods through normal module/fx wiring. Make this the only allowed Task 6 path.

Severity: Medium
Section: §10.1 Code Anchor Default; §17 Success Criteria
Issue: Static anchors with hard-coded line numbers are not robust enough for agent code定位 over time.
Why true: The plan requires anchors like `observability.Anchor("internal/module/turn/service.go", "PrepareTurn", 116)` and success depends on agents jumping directly to file/function/line. Source edits will drift line numbers immediately unless every anchor is maintained, but the plan does not require generation, validation, or tests that check anchors against current symbols.
Fix: Treat file+function as the stable anchor and line as best-effort. Add anchor validation tests or a generator that verifies every static anchor resolves to the named function in the current source. For dynamic slow/error stacks, prefer runtime caller frames to provide current lines.
