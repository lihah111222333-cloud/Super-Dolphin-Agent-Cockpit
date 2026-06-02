# Review A - Architecture / Dependencies / Task Boundaries

Scope: 10 task docs under `docs/cc/observability-tracing/tasks`, checked against `docs/cc/observability-tracing/00-implementation-plan.md` and current repository wiring.

## Findings

### 1. High - Frontend trace flush depends on an ingest RPC that is scheduled only in the final closeout task

- **Affected file(s)**:
  - `docs/cc/observability-tracing/tasks/06-react-frontend-trace-emitter.md`
  - `docs/cc/observability-tracing/tasks/10-dashboard-query-ui-verification-docs.md`
- **Evidence**:
  - Task 06 depends only on `obs_04_fx_wiring_disabled_service` and only “preferably” after `obs_05_wails_rpc_dispatch_instrumentation`, while requiring React remote trace flushing and preferring dedicated `observability/frontend/ingest`.
  - Task 10, which depends on Task 06 and all other implementation tasks, is the first task doc that lists `observability/frontend/ingest { events }` among RPC methods.
  - Current RPC registration requires a backend handler map to exist: `internal/platform/rpc/module.go` registers only `platformrpc.HandlerMapResult` groups via `registerAllHandlers(server *Server, p serverParams)`. The repo currently has no `internal/module/observability` package.
- **Why it matters**: Task 06 cannot complete a real remote flush against `observability/frontend/ingest` before that backend RPC exists. Leaving ingest creation in Task 10 creates a dependency inversion/circular boundary: Task 10 depends on frontend flushing being done, but frontend flushing depends on Task 10’s ingest handler.
- **Exact recommended correction**: Move creation of `observability/frontend/ingest` into Task 04 or a new backend ingest task that runs before Task 06. Then change Task 06 `Depends on` to include that ingest task, and change Task 10 to “verify/extend query UI and docs” rather than being the first owner of frontend ingest.

### 2. Medium - Final closeout validation omits packages modified by required prerequisite tasks

- **Affected file(s)**:
  - `docs/cc/observability-tracing/tasks/10-dashboard-query-ui-verification-docs.md`
  - `docs/cc/observability-tracing/tasks/08-provider-toolbridge-spans.md`
  - `docs/cc/observability-tracing/tasks/09-bus-uistate-spans.md`
- **Evidence**:
  - Task 10 depends on `obs_08_provider_toolbridge_spans` and `obs_09_bus_uistate_spans`.
  - Task 08 modifies `internal/provider/codexapp/**`, `internal/provider/claudecli/**`, `internal/provider/unified/**`, `internal/platform/toolbridge/**`, and `internal/platform/difftracker/**`, and validates those packages.
  - Task 09 modifies `internal/platform/bus/sink.go` and `internal/module/uistate/**`, and validates both `./internal/platform/bus` and `./internal/module/uistate`.
  - Task 10 validation includes observability, Wails, RPC, thread, turn, and uistate packages, but omits `./internal/provider/...`, `./internal/platform/toolbridge`, `./internal/platform/difftracker`, and `./internal/platform/bus`.
- **Why it matters**: Task 10 is the integration/verification closeout and depends on all span tasks, but its stated backend validation does not cover several packages that earlier tasks are required to modify. That can let provider/toolbridge/difftracker/bus instrumentation regress after prerequisite task validation.
- **Exact recommended correction**: Extend Task 10 validation to include `./internal/platform/bus`, `./internal/platform/toolbridge`, `./internal/platform/difftracker`, and touched provider packages (or `./internal/provider/...` if acceptable). If runtime cost is too high, require Task 10 to collect and cite successful validation artifacts from Task 08 and Task 09 plus rerun tests for any package touched during closeout.

## Category checklist

- **Architecture / module ownership**: no high-confidence issue found beyond Finding 1. Platform/service ownership stays under `internal/platform/observability`, and app RPC ownership stays under `internal/module/observability` as required.
- **Dependency ordering**: Finding 1.
- **App / Fx / RPC wiring realism**: no high-confidence issue found in the stated `HandlerMapResult` and explicit `internal/app/modules.go` wiring approach.
- **File path / package realism**: no high-confidence issue found. Referenced paths checked in current repo where applicable, including `frontend-app`, Wails binding, RPC server, provider/toolbridge/difftracker, bus, and uistate packages.
- **Test command realism / coverage surface**: Finding 2.
- **Task boundary conflicts**: Finding 1; otherwise no high-confidence issue found.
- **Phase 1 persistence constraints**: no high-confidence issue found. The task docs consistently avoid PostgreSQL/sqlc/SQLite expansion for tracing.
