# 13 Archtest 后端边界规则地图

> 由 `go run ./scripts/archtestmap` 从 `DefaultBackendBoundaryRegistry()` 自动生成。请勿手工维护本页事实。

- Owners: 14
- Canonical rules: 21
- Specialized guards: 9
- Governed backend surfaces: 27

## Rule owners

| Owner | File patterns | Reason |
|---|---|---|
| `app_adapter_boundary` | `internal/app/runtimeadapter/**/*.go`<br>`internal/app/runtimeadapter/builtintools/adapter.go`<br>`internal/app/runtimeadapter/cachekeepalive/adapter.go`<br>`internal/app/runtimeadapter/mcpcontrol/adapter.go`<br>`internal/app/runtimeadapter/module.go`<br>`internal/app/runtimeadapter/toolbridge/adapter.go`<br>`internal/app/storeadapter/**/*.go`<br>`internal/app/storeadapter/cron/adapter.go`<br>`internal/app/storeadapter/dashboard/adapter.go`<br>`internal/app/storeadapter/datasourcev2/adapter.go`<br>`internal/app/storeadapter/feedback/adapter.go`<br>`internal/app/storeadapter/insight/adapter.go`<br>`internal/app/storeadapter/memory/adapter.go`<br>`internal/app/storeadapter/module.go`<br>`internal/app/storeadapter/personalization/adapter.go`<br>`internal/app/storeadapter/prompt/adapter.go`<br>`internal/app/storeadapter/skill/adapter.go`<br>`internal/app/storeadapter/thread/prompt.go`<br>`internal/app/storeadapter/thread/store.go`<br>`internal/app/storeadapter/turn/adapter.go`<br>`internal/app/storeadapter/uistate/adapter.go` | app store and runtime adapters expose only audited domain-specific dependency seams |
| `command_boundary` | `cmd/agent-runtime/**/*.go`<br>`cmd/agent-terminal/**/*.go`<br>`cmd/codex-worktree-setup/**/*.go`<br>`cmd/super-dolphin-release-manifest/**/*.go`<br>`cmd/super-dolphin-updater/**/*.go` | standalone commands import only their registered host or runtime seams |
| `contract_boundary` | `internal/contract/**/*.go` | contract is the stable DTO and port surface; it must not depend on implementation packages |
| `fx_assembly` | `cmd/**/*.go`<br>`internal/**/*.go` | Fx belongs only to typed assembly scopes |
| `internal_support_boundary` | `internal/devtools/**/*.go`<br>`internal/dto/**/*.go`<br>`internal/testutil/**/*.go`<br>`internal/util/**/*.go` | shared support packages keep narrow, per-source internal dependency surfaces |
| `mcp_sidecar_boundary` | `cmd/mcp-ida/**/*.go`<br>`cmd/mcp-lsp/**/*.go`<br>`cmd/mcp-orch/**/*.go` | MCP sidecars are standalone entrypoints with only narrow shared internal dependencies |
| `mcpserver_family` | `cmd/mcp-ida/**/*.go`<br>`cmd/mcp-orch/**/*.go` | MCP server families must not couple to sibling tool implementations |
| `module_boundary` | `internal/module/**/*.go` | business modules own their internals and must communicate through contract or DTO ports |
| `platform_control_boundary` | `internal/platform/hooks/**/*.go`<br>`internal/platform/mcpcontrol/**/*.go` | hooks and MCP control stay decoupled and hooks do not own database lifecycle |
| `platform_runtime` | `internal/platform/**/*.go` | platform packages provide infrastructure primitives and must not depend upward on business or store ownership |
| `provider_runtime` | `internal/provider/**/*.go` | provider adapters own transport/runtime integration and must not reach into persistence internals |
| `public_pkg_boundary` | `pkg/**/*.go` | public pkg libraries must remain reusable without depending on repository internals or commands |
| `sqlc_boundary` | `cmd/**/*.go`<br>`internal/**/*.go` | sqlc generated code stays behind store and platform persistence boundaries |
| `store_dependency` | `internal/store/**/*.go`<br>`internal/store/**/module.go`<br>`internal/store/module.go` | store packages are anti-corruption adapters and may only consume their own package plus registered persistence ports |

## Canonical rules

| Rule | Owner | Kind | Files | Allow | Deny | Scope allow | Exceptions | Reason |
|---|---|---|---|---|---|---|---|---|
| `app_adapter_narrow_import_surface` | `app_adapter_boundary` | `allow_internal_imports` | `internal/app/runtimeadapter/**/*.go`<br>`internal/app/runtimeadapter/builtintools/adapter.go`<br>`internal/app/runtimeadapter/cachekeepalive/adapter.go`<br>`internal/app/runtimeadapter/mcpcontrol/adapter.go`<br>`internal/app/runtimeadapter/module.go`<br>`internal/app/runtimeadapter/toolbridge/adapter.go`<br>`internal/app/storeadapter/**/*.go`<br>`internal/app/storeadapter/cron/adapter.go`<br>`internal/app/storeadapter/dashboard/adapter.go`<br>`internal/app/storeadapter/datasourcev2/adapter.go`<br>`internal/app/storeadapter/feedback/adapter.go`<br>`internal/app/storeadapter/insight/adapter.go`<br>`internal/app/storeadapter/memory/adapter.go`<br>`internal/app/storeadapter/module.go`<br>`internal/app/storeadapter/personalization/adapter.go`<br>`internal/app/storeadapter/prompt/adapter.go`<br>`internal/app/storeadapter/skill/adapter.go`<br>`internal/app/storeadapter/thread/prompt.go`<br>`internal/app/storeadapter/thread/store.go`<br>`internal/app/storeadapter/turn/adapter.go`<br>`internal/app/storeadapter/uistate/adapter.go` | 90 policies across 19 file patterns | — | — | — | split app adapters may import only their actual child, domain, contract, platform, provider, and store dependencies |
| `command_narrow_import_surface` | `command_boundary` | `allow_internal_imports` | `cmd/agent-runtime/**/*.go`<br>`cmd/agent-terminal/**/*.go`<br>`cmd/codex-worktree-setup/**/*.go`<br>`cmd/super-dolphin-release-manifest/**/*.go`<br>`cmd/super-dolphin-updater/**/*.go` | 10 policies across 5 file patterns | — | — | — | standalone commands may import only their registered application or runtime seams |
| `contract_reverse_pollution` | `contract_boundary` | `deny_imports` | `internal/contract/**/*.go` | — | `internal/contract/**/*.go` → `cmd`<br>`internal/contract/**/*.go` → `frontend-app`<br>`internal/contract/**/*.go` → `internal/module`<br>`internal/contract/**/*.go` → `internal/provider`<br>`internal/contract/**/*.go` → `internal/store`<br>`internal/contract/**/*.go` → `internal/ui` | — | — | contract may only define stable DTOs and ports, never depend on implementation details |
| `fx_assembly_scope` | `fx_assembly` | `scoped_import` | `cmd/**/*.go`<br>`internal/**/*.go` | — | `cmd/**/*.go` → `go.uber.org/fx`<br>`internal/**/*.go` → `go.uber.org/fx` | `cmd/*/main.go` (`fx_command_entrypoint`)<br>`cmd/mcp-ida/**/*.go` (`fx_mcp_ida`)<br>`cmd/mcp-lsp/fx.go` (`fx_mcp_lsp`)<br>`cmd/mcp-orch/**/*.go` (`fx_mcp_orch`)<br>`internal/**/module.go` (`fx_module_file`)<br>`internal/app/**/*.go` (`fx_internal_app`) | — | Fx imports belong only to registered assembly entrypoints |
| `hooks_no_mcpcontrol` | `platform_control_boundary` | `deny_imports` | `internal/platform/hooks/**/*.go` | — | `internal/platform/hooks/**/*.go` → `internal/platform/mcpcontrol` | — | — | hooks publish contracts instead of importing MCP control implementations |
| `hooks_no_platform_db` | `platform_control_boundary` | `deny_imports` | `internal/platform/hooks/**/*.go` | — | `internal/platform/hooks/**/*.go` → `internal/platform/db` | — | — | hooks must not own database lifecycle in production or test helpers |
| `internal_support_narrow_import_surface` | `internal_support_boundary` | `allow_internal_imports` | `internal/devtools/**/*.go`<br>`internal/dto/**/*.go`<br>`internal/testutil/**/*.go`<br>`internal/util/**/*.go` | 12 policies across 4 file patterns | — | — | — | support packages may import only descendants and explicitly registered shared seams |
| `mcp_sidecar_narrow_import_surface` | `mcp_sidecar_boundary` | `allow_internal_imports` | `cmd/mcp-ida/**/*.go`<br>`cmd/mcp-lsp/**/*.go`<br>`cmd/mcp-orch/**/*.go` | 47 policies across 3 file patterns | 21 policies across 3 file patterns | — | — | cmd/mcp-* may use local sidecar packages and explicit shared contracts/platform primitives, but not app host, provider, or module services |
| `mcpcontrol_no_hooks` | `platform_control_boundary` | `deny_imports` | `internal/platform/mcpcontrol/**/*.go` | — | `internal/platform/mcpcontrol/**/*.go` → `internal/platform/hooks` | — | — | MCP control consumes injected hook ports instead of hook implementations |
| `mcpserver_ida_family` | `mcpserver_family` | `deny_imports` | `cmd/mcp-ida/**/*.go` | — | `cmd/mcp-ida/**/*.go` → `internal/tool/lsp`<br>`cmd/mcp-ida/**/*.go` → `internal/tool/orchestration` | — | — | IDA MCP servers must not depend on LSP or orchestration tool families |
| `mcpserver_orch_family` | `mcpserver_family` | `deny_imports` | `cmd/mcp-orch/**/*.go` | — | `cmd/mcp-orch/**/*.go` → `internal/tool/ida`<br>`cmd/mcp-orch/**/*.go` → `internal/tool/lsp` | — | — | orchestration MCP servers must not depend on LSP or IDA tool families |
| `module_horizontal_deep_import` | `module_boundary` | `module_siblings` | `internal/module/**/*.go` | — | — | — | — | module packages must not import sibling module internals; use contract DTOs or injected ports instead |
| `module_no_direct_db_imports` | `module_boundary` | `deny_imports` | `internal/module/**/*.go` | — | `internal/module/**/*.go` → `database/sql`<br>`internal/module/**/*.go` → `github.com/jackc/pgx/v5/pgconn`<br>`internal/module/**/*.go` → `github.com/jackc/pgx/v5/pgxpool`<br>`internal/module/**/*.go` → `github.com/jackc/pgx/v5` | — | — | module production code must not own direct database imports outside audited skill persistence seams |
| `module_no_store_imports` | `module_boundary` | `deny_imports` | `internal/module/**/*.go` | — | `internal/module/**/*.go` → `internal/store` | — | — | business modules own persistence ports and receive Store adapters from internal/app |
| `pkg_no_internal_imports` | `public_pkg_boundary` | `deny_imports` | `pkg/**/*.go` | — | `pkg/**/*.go` → `cmd`<br>`pkg/**/*.go` → `internal` | — | — | public pkg libraries must not depend on repository internals or command entrypoints |
| `platform_no_module` | `platform_runtime` | `deny_imports` | `internal/platform/**/*.go` | — | `internal/platform/**/*.go` → `internal/module` | — | — | platform infrastructure stays below business modules |
| `platform_no_store` | `platform_runtime` | `deny_imports` | `internal/platform/**/*.go` | — | `internal/platform/**/*.go` → `internal/store` | — | — | platform infrastructure must not depend on product store subpackages |
| `provider_no_platform_db` | `provider_runtime` | `deny_imports` | `internal/provider/**/*.go` | — | `internal/provider/**/*.go` → `internal/platform/db` | — | — | provider production code must not own SQLite handles or DB lifecycle |
| `provider_no_store` | `provider_runtime` | `deny_imports` | `internal/provider/**/*.go` | — | `internal/provider/**/*.go` → `internal/store` | — | — | provider adapters consume session contracts and must not import product store packages directly |
| `store_dependency_surface` | `store_dependency` | `store_imports` | `internal/store/**/*.go`<br>`internal/store/**/module.go`<br>`internal/store/module.go` | 14 policies across 3 file patterns | — | — | — | store packages must depend only on their own implementation package and registered persistence ports |
| `store_sqlc_store_platform_only` | `sqlc_boundary` | `scoped_import` | `cmd/**/*.go`<br>`internal/**/*.go` | — | `cmd/**/*.go` → `internal/store/sqlc`<br>`internal/**/*.go` → `internal/store/sqlc` | `internal/platform/db/**/*.go` (`platform_db`)<br>`internal/store/**/*.go` (`store`) | — | store/sqlc generated types must stay inside persistence implementation seams |

## Specialized guards

| Guard | Test file | Build tags | Runnable tests | Applies to | Reason |
|---|---|---|---|---|---|
| `backend_boundary_single_source` | `internal/archtest/backend_boundary_single_source_test.go` | — | `TestBackendBoundaryRuleFactsHaveOneSource` | `internal/archtest` | canonical backend boundary facts must not be duplicated by procedural evaluators |
| `backend_surface_governance` | `internal/archtest/backend_boundary_governance_test.go` | — | `TestValidateDefaultBackendBoundaryGovernance` | `internal/archtest` | the governance guard fails when a backend top-level Go surface is missing or stale |
| `code_size_budget` | `internal/guards/code_size_guard_test.go` | — | `TestCodeSizeBudgetBaselineIsActionable` | `internal/guards` | the repository guard keeps code size baselines non-empty and enforcing |
| `dependency_direction` | `internal/archtest/dependency_direction_test.go` | — | `TestDependencyDirection` | `internal/mcpserver` | dependency direction tests protect typed backend layer relationships |
| `fx_graph` | `internal/archtest/fx_graph_test.go` | — | `TestFxValidateApp` | `internal/app` | the desktop composition root must retain a valid Fx graph |
| `pkg_public_boundary` | `internal/archtest/backend_boundary_guard_coverage_test.go` | — | `TestPkgNoInternalImportsRuleRejectsRepositoryInternals` | `pkg/dagmetrics`<br>`pkg/dreammetrics`<br>`pkg/logger`<br>`pkg/skillmetrics` | public pkg libraries must reject both repository internals and command entrypoints |
| `rollback_skip_markers` | `internal/guards/rollback_skip_guard_test.go` | — | `TestGoTestsDoNotContainRollbackSkipMarkers` | `internal/guards` | the repository guard rejects hidden rollback skip markers in Go tests |
| `rpc_runtime_e2e` | `internal/e2e/rpc_runtime/runtime_e2e_test.go` | `e2e` | `TestAgentRuntimeRPCBlackBox`<br>`TestRPCRuntimeE2EEnvIsIsolated` | `internal/e2e` | the tagged RPC runtime suite validates the backend process boundary end to end |
| `ui_wails_boundary` | `internal/archtest/ui_wails_guard_test.go` | — | `TestUIWailsActiveAgentPredicateFromContract`<br>`TestUIWailsNoDirectUIStateImport` | `internal/ui` | Wails UI bindings consume contract-facing state instead of module implementations |

## Governed backend surfaces

| Surface | Canonical rules | Specialized guards | Reason |
|---|---|---|---|
| `cmd/agent-runtime` | `command_narrow_import_surface`<br>`fx_assembly_scope` | — | agent runtime process assembly |
| `cmd/agent-terminal` | `command_narrow_import_surface`<br>`fx_assembly_scope` | — | agent terminal process assembly |
| `cmd/codex-worktree-setup` | `command_narrow_import_surface`<br>`fx_assembly_scope` | — | Codex worktree LSP bootstrap command |
| `cmd/mcp-ida` | `fx_assembly_scope`<br>`mcp_sidecar_narrow_import_surface`<br>`mcpserver_ida_family` | — | IDA MCP sidecar boundary |
| `cmd/mcp-lsp` | `fx_assembly_scope`<br>`mcp_sidecar_narrow_import_surface` | — | LSP MCP sidecar boundary |
| `cmd/mcp-orch` | `fx_assembly_scope`<br>`mcp_sidecar_narrow_import_surface`<br>`mcpserver_orch_family` | — | orchestration MCP sidecar boundary |
| `cmd/super-dolphin-release-manifest` | `command_narrow_import_surface`<br>`fx_assembly_scope` | — | release manifest command assembly |
| `cmd/super-dolphin-updater` | `command_narrow_import_surface`<br>`fx_assembly_scope` | — | updater command assembly |
| `internal/app` | `app_adapter_narrow_import_surface`<br>`fx_assembly_scope` | `fx_graph` | desktop composition root |
| `internal/archtest` | `fx_assembly_scope` | `backend_boundary_single_source`<br>`backend_surface_governance` | architecture governance implementation |
| `internal/contract` | `contract_reverse_pollution` | — | stable DTO and port contracts |
| `internal/devtools` | `fx_assembly_scope`<br>`internal_support_narrow_import_surface` | — | backend developer tooling |
| `internal/dto` | `fx_assembly_scope`<br>`internal_support_narrow_import_surface` | — | transport-neutral data transfer objects |
| `internal/e2e` | — | `rpc_runtime_e2e` | backend end-to-end test surface |
| `internal/guards` | — | `code_size_budget`<br>`rollback_skip_markers` | repository-level test guard surface |
| `internal/mcpserver` | `fx_assembly_scope` | `dependency_direction` | shared MCP server implementations |
| `internal/module` | `fx_assembly_scope`<br>`module_horizontal_deep_import`<br>`module_no_direct_db_imports`<br>`module_no_store_imports` | — | business module ownership |
| `internal/platform` | `fx_assembly_scope`<br>`hooks_no_mcpcontrol`<br>`hooks_no_platform_db`<br>`mcpcontrol_no_hooks`<br>`platform_no_module`<br>`platform_no_store` | — | infrastructure runtime layer |
| `internal/provider` | `fx_assembly_scope`<br>`provider_no_platform_db`<br>`provider_no_store` | — | provider adapter runtime |
| `internal/store` | `fx_assembly_scope`<br>`store_dependency_surface`<br>`store_sqlc_store_platform_only` | — | persistence anti-corruption layer |
| `internal/testutil` | `fx_assembly_scope`<br>`internal_support_narrow_import_surface` | — | shared backend test support |
| `internal/ui` | `fx_assembly_scope` | `ui_wails_boundary` | Wails backend binding layer |
| `internal/util` | `fx_assembly_scope`<br>`internal_support_narrow_import_surface` | — | shared backend utilities |
| `pkg/dagmetrics` | `pkg_no_internal_imports` | `pkg_public_boundary` | public DAG metrics library |
| `pkg/dreammetrics` | `pkg_no_internal_imports` | `pkg_public_boundary` | public dream metrics library |
| `pkg/logger` | `pkg_no_internal_imports` | `pkg_public_boundary` | public logging library |
| `pkg/skillmetrics` | `pkg_no_internal_imports` | `pkg_public_boundary` | public skill metrics library |
