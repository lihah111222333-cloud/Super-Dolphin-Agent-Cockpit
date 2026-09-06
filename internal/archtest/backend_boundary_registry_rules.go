package archtest

const (
	appAdapterBoundaryOwner         BoundaryOwnerID = "app_adapter_boundary"
	appAdapterNarrowImportRuleID    BoundaryRuleID  = "app_adapter_narrow_import_surface"
	storeAdapterProductionPattern                   = "internal/app/storeadapter/**/*.go"
	runtimeAdapterProductionPattern                 = "internal/app/runtimeadapter/**/*.go"
)

// defaultAppAdapterOwner 登记 app adapter 拆分后的生产边界归属。
func defaultAppAdapterOwner() BackendBoundaryOwner {
	return BackendBoundaryOwner{
		ID:           appAdapterBoundaryOwner,
		FilePatterns: appAdapterRuleFilePatterns(appAdapterAllowPolicies()),
		Reason:       "app store and runtime adapters expose only audited domain-specific dependency seams",
	}
}

// defaultAppAdapterRule 将 aggregator、领域实现和运行时桥接的真实依赖收敛为逐文件许可。
func defaultAppAdapterRule() BackendBoundaryRule {
	allow := appAdapterAllowPolicies()
	return BackendBoundaryRule{
		ID:            appAdapterNarrowImportRuleID,
		Owner:         appAdapterBoundaryOwner,
		Reason:        "split app adapters may import only their actual child, domain, contract, platform, provider, and store dependencies",
		Kind:          BoundaryRuleAllowInternalImports,
		FilePatterns:  appAdapterRuleFilePatterns(allow),
		Allow:         allow,
		SkipTestFiles: true,
	}
}

func appAdapterProductionPatterns() []string {
	return []string{storeAdapterProductionPattern, runtimeAdapterProductionPattern}
}

// appAdapterRuleFilePatterns 从 allow facts 派生精确文件模式，并保留覆盖全部生产文件的递归模式。
func appAdapterRuleFilePatterns(allow []BoundaryImportPolicy) []string {
	patterns := appAdapterProductionPatterns()
	seen := map[string]bool{storeAdapterProductionPattern: true, runtimeAdapterProductionPattern: true}
	for _, policy := range allow {
		if !seen[policy.FilePattern] {
			patterns = append(patterns, policy.FilePattern)
			seen[policy.FilePattern] = true
		}
	}
	return patterns
}

// appAdapterAllowPolicies 按生产文件登记真实 internal imports，未登记路径由 canonical evaluator 拒绝。
func appAdapterAllowPolicies() []BoundaryImportPolicy {
	var policies []BoundaryImportPolicy
	policies = appendAppAdapterPolicies(policies, "internal/app/storeadapter/module.go", "store adapter aggregator imports only registered domain children",
		"internal/app/storeadapter/cron",
		"internal/app/storeadapter/dashboard",
		"internal/app/storeadapter/datasourcev2",
		"internal/app/storeadapter/feedback",
		"internal/app/storeadapter/insight",
		"internal/app/storeadapter/personalization",
		"internal/app/storeadapter/prompt",
		"internal/app/storeadapter/thread",
		"internal/app/storeadapter/turn",
		"internal/app/storeadapter/uistate",
	)
	policies = appendAppAdapterPolicies(policies, "internal/app/runtimeadapter/module.go", "runtime adapter aggregator imports only registered runtime children",
		"internal/app/runtimeadapter/builtintools",
		"internal/app/runtimeadapter/cachekeepalive",
		"internal/app/runtimeadapter/mcpcontrol",
		"internal/app/runtimeadapter/toolbridge",
	)

	policies = appendAppAdapterPolicies(policies, "internal/app/storeadapter/cron/adapter.go", "cron adapter owns only its domain module and store",
		"internal/app/internal/storeguard", "internal/module/cron", "internal/store/cron")
	policies = appendAppAdapterPolicies(policies, "internal/app/storeadapter/dashboard/adapter.go", "dashboard adapter projects its domain over audited dashboard stores",
		"internal/app/internal/storeguard", "internal/module/dashboard",
		"internal/store/agentstatus", "internal/store/ailog", "internal/store/auditlog",
		"internal/store/buslog", "internal/store/commandcard", "internal/store/dbquery",
		"internal/store/sharedfile", "internal/store/systemlog")
	policies = appendAppAdapterPolicies(policies, "internal/app/storeadapter/datasourcev2/adapter.go", "datasource adapter owns only its domain module and store",
		"internal/app/internal/storeguard", "internal/module/datasource_v2", "internal/store/datasourcev2")
	policies = appendAppAdapterPolicies(policies, "internal/app/storeadapter/feedback/adapter.go", "feedback adapter owns only its domain module and store",
		"internal/app/internal/storeguard", "internal/module/feedback", "internal/store/feedback")
	policies = appendAppAdapterPolicies(policies, "internal/app/storeadapter/insight/adapter.go", "insight adapter owns only its domain module and store",
		"internal/app/internal/storeguard", "internal/module/insight", "internal/store/insight")
	policies = appendAppAdapterPolicies(policies, "internal/app/storeadapter/personalization/adapter.go", "personalization adapter owns only its domain module and preference store",
		"internal/app/internal/storeguard", "internal/module/personalization", "internal/store/uipreference")
	policies = appendAppAdapterPolicies(policies, "internal/app/storeadapter/prompt/adapter.go", "prompt adapter owns only its domain module and prompt-related stores",
		"internal/app/internal/storeguard", "internal/module/prompt", "internal/store/sharedfile",
		"internal/store/uipreference")
	policies = appendAppAdapterPolicies(policies, "internal/app/storeadapter/turn/adapter.go", "turn adapter owns only its domain module and dedupe store",
		"internal/app/internal/storeguard", "internal/module/turn", "internal/store/turndedupe")
	policies = appendAppAdapterPolicies(policies, "internal/app/storeadapter/uistate/adapter.go", "UI state adapter owns only its domain module and UI stores",
		"internal/app/internal/storeguard", "internal/module/uistate", "internal/store/binding",
		"internal/store/uipreference")
	policies = appendAppAdapterPolicies(policies, "internal/app/storeadapter/thread/prompt.go", "thread prompt adapter owns only thread contracts, modules, and prompt store",
		"internal/contract", "internal/module/thread", "internal/module/threadprompt", "internal/store/prompt")
	policies = appendAppAdapterPolicies(policies, "internal/app/storeadapter/thread/store.go", "thread store adapter owns only thread contracts and stores",
		"internal/contract", "internal/module/thread", "internal/store/binding", "internal/store/thread")

	policies = appendAppAdapterPolicies(policies, "internal/app/runtimeadapter/builtintools/adapter.go", "built-in tool bridge imports only its actual contracts, modules, provider, and store",
		"internal/contract", "internal/module/prompt", "internal/module/uistate",
		"internal/provider/unified", "internal/store/uipreference")
	policies = appendAppAdapterPolicies(policies, "internal/app/runtimeadapter/cachekeepalive/adapter.go", "cache keepalive bridge imports only its contract and backing stores",
		"internal/contract", "internal/store/binding", "internal/store/thread")
	policies = appendAppAdapterPolicies(policies, "internal/app/runtimeadapter/mcpcontrol/adapter.go", "MCP control bridge imports only its platform controller and system log store",
		"internal/platform/mcpcontrol", "internal/store/systemlog")
	policies = appendAppAdapterPolicies(policies, "internal/app/runtimeadapter/toolbridge/adapter.go", "tool bridge imports only its actual contracts, runtime providers, platforms, and stores",
		"internal/contract", "internal/dto/provider", "internal/module/mcp_server",
		"internal/platform/difftracker", "internal/platform/toolbridge",
		"internal/provider/codexapp", "internal/provider/codexapp/protocol",
		"internal/store/binding", "internal/store/thread", "internal/store/uipreference")
	return policies
}

// appendAppAdapterPolicies 将单个生产文件的精确 import 前缀展开为 typed policies。
func appendAppAdapterPolicies(policies []BoundaryImportPolicy, filePattern, reason string, imports ...string) []BoundaryImportPolicy {
	return append(policies, boundaryPolicies(appAdapterBoundaryOwner, []string{filePattern}, imports, reason)...)
}

func defaultContractDTOFrameworkRule(patterns backendBoundaryPatterns) BackendBoundaryRule {
	files := combineBoundaryPatterns(patterns.contract, patterns.dto)
	return BackendBoundaryRule{
		ID:           "contract_dto_no_framework_imports",
		Owner:        "contract_boundary",
		Reason:       "contract and DTO packages stay transport-neutral and must not depend on runtime frameworks",
		Kind:         BoundaryRuleDenyImports,
		FilePatterns: files,
		Deny: boundaryPolicies(
			"contract_boundary",
			files,
			[]string{"go.uber.org/fx", "github.com/creachadair/jrpc2", "github.com/jackc/pgx/v5", "github.com/wailsapp/wails"},
			"contract and DTO packages must remain free of framework dependencies",
		),
		SkipTestFiles: true,
	}
}

func defaultContractReversePollutionRule(patterns backendBoundaryPatterns) BackendBoundaryRule {
	return BackendBoundaryRule{
		ID:           "contract_reverse_pollution",
		Owner:        "contract_boundary",
		Reason:       "contract may only define stable DTOs and ports, never depend on implementation details",
		Kind:         BoundaryRuleAllowRepositoryImports,
		FilePatterns: patterns.contract,
		Allow: boundaryPolicies("contract_boundary", patterns.contract, []string{
			"internal/contract",
			"internal/dto/agent",
			"internal/dto/mcp",
			"internal/dto/provider",
			"internal/dto/shared",
			"internal/dto/thread",
			"internal/dto/turn",
			"internal/dto/ui",
		}, "contract may import only the exact registered repository DTO packages"),
		SkipTestFiles: true,
	}
}

func defaultModuleSiblingRule(patterns backendBoundaryPatterns) BackendBoundaryRule {
	return BackendBoundaryRule{
		ID:            "module_horizontal_deep_import",
		Owner:         "module_boundary",
		Reason:        "module packages must not import sibling module internals; use contract DTOs or injected ports instead",
		Kind:          BoundaryRuleModuleSiblings,
		FilePatterns:  patterns.module,
		SkipTestFiles: true,
	}
}

func defaultModuleDatabaseRule(patterns backendBoundaryPatterns) BackendBoundaryRule {
	return BackendBoundaryRule{
		ID:           "module_no_direct_db_imports",
		Owner:        "module_boundary",
		Reason:       "module production code must not own direct database imports outside audited skill persistence seams",
		Kind:         BoundaryRuleDenyImports,
		FilePatterns: patterns.module,
		Deny: boundaryPolicies("module_boundary", patterns.module, []string{
			"database/sql",
			"github.com/jackc/pgx/v5",
			"github.com/jackc/pgx/v5/pgxpool",
			"github.com/jackc/pgx/v5/pgconn",
		}, "module code must not own direct database dependencies"),
		SkipTestFiles: true,
	}
}

func defaultModuleOuterImplementationRule(patterns backendBoundaryPatterns) BackendBoundaryRule {
	return BackendBoundaryRule{
		ID:           "module_no_outer_implementation_imports",
		Owner:        "module_boundary",
		Reason:       "business modules must depend on contracts and injected ports instead of provider, MCP server, or command implementations",
		Kind:         BoundaryRuleDenyImports,
		FilePatterns: patterns.module,
		Deny: boundaryPolicies(
			"module_boundary",
			patterns.module,
			[]string{"internal/provider", "internal/mcpserver", "cmd"},
			"module production code must not import outer implementation packages",
		),
		SkipTestFiles: true,
	}
}

func defaultModuleStoreRule(patterns backendBoundaryPatterns) BackendBoundaryRule {
	return BackendBoundaryRule{
		ID:           "module_no_store_imports",
		Owner:        "module_boundary",
		Reason:       "business modules own persistence ports and receive Store adapters from internal/app",
		Kind:         BoundaryRuleDenyImports,
		FilePatterns: patterns.module,
		Deny: boundaryPolicies(
			"module_boundary",
			patterns.module,
			[]string{"internal/store"},
			"module production code must not import Store implementations",
		),
		SkipTestFiles: true,
	}
}

func defaultMCPSidecarRule(patterns backendBoundaryPatterns) BackendBoundaryRule {
	denied := []string{
		"internal/app",
		"internal/module",
		"internal/provider",
		"cmd/agent-terminal",
		"internal/platform/rpc/server",
		"internal/platform/rpc/push",
		"internal/platform/rpc/notification",
		"internal/platform/pidregistry",
		"internal/platform/contentcrypto",
		"internal/platform/contentobject",
		"cmd/mcp-orch/orchestration/recoverycustody",
	}
	deniedPolicies := boundaryPolicies("mcp_sidecar_boundary", patterns.sidecar, denied, "sidecars must not reach unregistered host, state, provider, or process internals")
	deniedPolicies = append(deniedPolicies, boundaryPolicies("mcp_sidecar_boundary", []string{"cmd/mcp-recovery-custody/**/*.go"}, []string{"internal/platform/db/sqlite"}, "mcp-recovery-custody sidecar owns only its private SQLite primitive")...)
	return BackendBoundaryRule{
		ID:                 "mcp_sidecar_narrow_import_surface",
		Owner:              "mcp_sidecar_boundary",
		Reason:             "cmd/mcp-* may use local sidecar packages and explicit shared contracts/platform primitives, but not app host, provider, or module services",
		Kind:               BoundaryRuleAllowInternalImports,
		FilePatterns:       patterns.sidecar,
		Allow:              mcpSidecarAllowPolicies(),
		Deny:               deniedPolicies,
		Exceptions:         mcpSidecarNarrowExceptions(),
		SkipTestFiles:      true,
		DependencyPackages: []string{"cmd/mcp-orch", "cmd/mcp-lsp", "cmd/mcp-ida", "cmd/mcp-recovery-custody"},
	}
}

// mcpSidecarNarrowExceptions 为已审计的单文件导入建立精确例外，避免把
// process/content/authority primitive 扩大成整个 sidecar 的通用白名单。
func mcpSidecarNarrowExceptions() []BoundaryException {
	owner := BoundaryOwnerID("mcp_sidecar_boundary")
	exceptions := []BoundaryException{
		mcpSidecarPermanentException(owner, "managedruntime_pidregistry", "cmd/mcp-orch/standalone/managedruntime/verifier.go", "internal/platform/pidregistry", "managed runtime receipt verifier validates stable process identity"),
		mcpSidecarPermanentException(owner, "mcp_orch_transitive_pidregistry", "cmd/mcp-orch/main.go", "internal/platform/pidregistry", "orchestration sidecar transitive closure includes the audited managed runtime verifier"),
		mcpSidecarPermanentException(owner, "mcp_orch_transitive_contentcrypto", "cmd/mcp-orch/main.go", "internal/platform/contentcrypto", "orchestration sidecar transitive closure includes the audited CONTENT-1 owner"),
		mcpSidecarPermanentException(owner, "mcp_orch_transitive_contentobject", "cmd/mcp-orch/main.go", "internal/platform/contentobject", "orchestration sidecar transitive closure includes the audited CONTENT-1 owner"),
		mcpSidecarPermanentException(owner, "content_contract_object", "cmd/mcp-orch/store/content/contract.go", "internal/platform/contentobject", "CONTENT-1 contract binds opaque object receipts"),
		mcpSidecarPermanentException(owner, "content_mapping_object", "cmd/mcp-orch/store/content/mapping.go", "internal/platform/contentobject", "CONTENT-1 mapping validates object receipt identity"),
		mcpSidecarPermanentException(owner, "content_prepare_crypto", "cmd/mcp-orch/store/content/prepare_single.go", "internal/platform/contentcrypto", "CONTENT-1 prepare encrypts a single object"),
		mcpSidecarPermanentException(owner, "content_prepare_object", "cmd/mcp-orch/store/content/prepare_single.go", "internal/platform/contentobject", "CONTENT-1 prepare commits an encrypted object"),
		mcpSidecarPermanentException(owner, "content_store_crypto", "cmd/mcp-orch/store/content/store.go", "internal/platform/contentcrypto", "CONTENT-1 store owns encrypted content metadata"),
		mcpSidecarPermanentException(owner, "content_store_object", "cmd/mcp-orch/store/content/store.go", "internal/platform/contentobject", "CONTENT-1 store owns encrypted object receipts"),
		mcpSidecarPermanentException(owner, "content_validate_crypto", "cmd/mcp-orch/store/content/validate.go", "internal/platform/contentcrypto", "CONTENT-1 validation checks crypto envelope identity"),
		mcpSidecarPermanentException(owner, "custody_config_authority", "cmd/mcp-recovery-custody/config.go", "cmd/mcp-orch/orchestration/recoverycustody", "independent RecoveryCustody service parses its authority configuration"),
		mcpSidecarPermanentException(owner, "custody_service_authority", "cmd/mcp-recovery-custody/service.go", "cmd/mcp-orch/orchestration/recoverycustody", "independent RecoveryCustody service owns the authority runtime"),
		mcpSidecarPermanentException(owner, "custody_transitive_authority", "cmd/mcp-recovery-custody/main.go", "cmd/mcp-orch/orchestration/recoverycustody", "RecoveryCustody sidecar transitive closure is restricted to its authority subtree"),
		mcpSidecarPermanentException(owner, "custody_transitive_sqlite", "cmd/mcp-recovery-custody/main.go", "internal/platform/db/sqlite", "RecoveryCustody sidecar transitively owns its private SQLite authority"),
	}
	return exceptions
}

func mcpSidecarPermanentException(owner BoundaryOwnerID, id, file, importPrefix, reason string) BoundaryException {
	return BoundaryException{ID: id, Owner: owner, FilePattern: file, ImportPrefix: importPrefix,
		Class: BoundaryExceptionPermanent, Reason: reason}
}

func defaultMCPOrchRecoveryCustodyAuthorityRule(patterns backendBoundaryPatterns) BackendBoundaryRule {
	return BackendBoundaryRule{ID: "mcp_orch_no_embedded_recovery_custody_authority", Owner: "mcp_sidecar_boundary",
		Reason: "mcp-orch clients must reach RecoveryCustody through the transport-neutral Client and an independent service protocol adapter",
		Kind:   BoundaryRuleDenyImports, FilePatterns: patterns.mcpOrch, SkipTestFiles: true,
		Deny: boundaryPolicies("mcp_sidecar_boundary", patterns.mcpOrch, []string{"cmd/mcp-orch/orchestration/recoverycustody/sqliteauthority"}, "ordinary mcp-orch production code must not embed the SQLite authority implementation")}
}

func defaultCommandNarrowImportRule(patterns backendBoundaryPatterns) BackendBoundaryRule {
	return BackendBoundaryRule{
		ID:           "command_narrow_import_surface",
		Owner:        "command_boundary",
		Reason:       "standalone commands may import only their registered application or runtime seams",
		Kind:         BoundaryRuleAllowInternalImports,
		FilePatterns: commandBoundaryPatterns(patterns),
		Allow:        commandNarrowAllowPolicies(patterns),
	}
}

func defaultInternalSupportNarrowImportRule(patterns backendBoundaryPatterns) BackendBoundaryRule {
	return BackendBoundaryRule{
		ID:           "internal_support_narrow_import_surface",
		Owner:        "internal_support_boundary",
		Reason:       "support packages may import only descendants and explicitly registered shared seams",
		Kind:         BoundaryRuleAllowInternalImports,
		FilePatterns: internalSupportBoundaryPatterns(patterns),
		Allow:        internalSupportNarrowAllowPolicies(patterns),
	}
}
