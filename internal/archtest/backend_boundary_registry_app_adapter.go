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
		"internal/app/storeadapter/memory",
		"internal/app/storeadapter/personalization",
		"internal/app/storeadapter/prompt",
		"internal/app/storeadapter/skill",
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
		"internal/store/prompt", "internal/store/sharedfile", "internal/store/systemlog")
	policies = appendAppAdapterPolicies(policies, "internal/app/storeadapter/datasourcev2/adapter.go", "datasource adapter owns only its domain module and store",
		"internal/app/internal/storeguard", "internal/module/datasource_v2", "internal/store/datasourcev2")
	policies = appendAppAdapterPolicies(policies, "internal/app/storeadapter/feedback/adapter.go", "feedback adapter owns only its domain module and store",
		"internal/app/internal/storeguard", "internal/module/feedback", "internal/store/feedback")
	policies = appendAppAdapterPolicies(policies, "internal/app/storeadapter/insight/adapter.go", "insight adapter owns only its domain module and store",
		"internal/app/internal/storeguard", "internal/module/insight", "internal/store/insight")
	policies = appendAppAdapterPolicies(policies, "internal/app/storeadapter/memory/adapter.go", "memory adapter owns only its shared-file port and store",
		"internal/app/internal/storeguard", "internal/module/memory/sharedfileport", "internal/store/sharedfile")
	policies = appendAppAdapterPolicies(policies, "internal/app/storeadapter/personalization/adapter.go", "personalization adapter owns only its domain module and preference store",
		"internal/app/internal/storeguard", "internal/module/personalization", "internal/store/uipreference")
	policies = appendAppAdapterPolicies(policies, "internal/app/storeadapter/prompt/adapter.go", "prompt adapter owns only its domain module and prompt-related stores",
		"internal/app/internal/storeguard", "internal/module/prompt", "internal/store/prompt",
		"internal/store/sharedfile", "internal/store/uipreference")
	policies = appendAppAdapterPolicies(policies, "internal/app/storeadapter/turn/adapter.go", "turn adapter owns only its domain module and dedupe store",
		"internal/app/internal/storeguard", "internal/module/turn", "internal/store/turndedupe")
	policies = appendAppAdapterPolicies(policies, "internal/app/storeadapter/uistate/adapter.go", "UI state adapter owns only its domain module and UI stores",
		"internal/app/internal/storeguard", "internal/module/uistate", "internal/store/binding",
		"internal/store/sharedfile", "internal/store/uipreference")
	policies = appendAppAdapterPolicies(policies, "internal/app/storeadapter/skill/adapter.go", "skill adapter owns only skill domain and persistence ports",
		"internal/module/skill", "internal/module/skill/toolstore", "internal/store/auditlog", "internal/store/skilltool")
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
