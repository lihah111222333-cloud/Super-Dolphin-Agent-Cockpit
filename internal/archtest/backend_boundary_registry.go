package archtest

// BoundaryOwnerID 标识后端边界规则拥有者。
type BoundaryOwnerID string

// BoundaryRuleID 标识一条稳定的后端边界规则。
type BoundaryRuleID string

// BoundaryRuleKind 标识规则求值方式。
type BoundaryRuleKind string

// BoundaryScopeID 标识经过 registry 注册的文件范围。
type BoundaryScopeID string

const (
	// BoundaryRuleDenyImports 拒绝匹配文件导入指定前缀。
	BoundaryRuleDenyImports BoundaryRuleKind = "deny_imports"
	// BoundaryRuleAllowInternalImports 限制匹配文件导入内部或 cmd 包的白名单。
	BoundaryRuleAllowInternalImports BoundaryRuleKind = "allow_internal_imports"
	// BoundaryRuleModuleSiblings 拒绝 module 层跨 owner 的具体实现导入。
	BoundaryRuleModuleSiblings BoundaryRuleKind = "module_siblings"
	// BoundaryRuleScopedImport 只允许指定文件范围导入被拒绝前缀。
	BoundaryRuleScopedImport BoundaryRuleKind = "scoped_import"
	// BoundaryRuleStoreImports 限制 store 包只依赖同 owner 子包和显式注册的持久化端口。
	BoundaryRuleStoreImports BoundaryRuleKind = "store_imports"
)

const (
	// BoundaryScopeStore 允许持久化 store 包消费受限实现。
	BoundaryScopeStore BoundaryScopeID = "store"
	// BoundaryScopePlatformDB 允许平台数据库包消费受限实现。
	BoundaryScopePlatformDB BoundaryScopeID = "platform_db"
	// BoundaryScopeFXInternalApp 允许应用根装配层导入 Fx。
	BoundaryScopeFXInternalApp BoundaryScopeID = "fx_internal_app"
	// BoundaryScopeFXModuleFile 允许模块装配文件导入 Fx。
	BoundaryScopeFXModuleFile BoundaryScopeID = "fx_module_file"
	// BoundaryScopeFXMCPOrch 允许 orchestration sidecar 组装 Fx 图。
	BoundaryScopeFXMCPOrch BoundaryScopeID = "fx_mcp_orch"
	// BoundaryScopeFXMCPIDA 允许 IDA sidecar 组装 Fx 图。
	BoundaryScopeFXMCPIDA BoundaryScopeID = "fx_mcp_ida"
	// BoundaryScopeFXCommandEntrypoint 允许 cmd 直属入口文件导入 Fx。
	BoundaryScopeFXCommandEntrypoint BoundaryScopeID = "fx_command_entrypoint"
	// BoundaryScopeFXMCPLSP 只允许 mcp-lsp 的 fx.go 组装 Fx 图。
	BoundaryScopeFXMCPLSP BoundaryScopeID = "fx_mcp_lsp"
)

// BoundaryExceptionClass 标识例外的生命周期。
type BoundaryExceptionClass string

const (
	// BoundaryExceptionPermanent 表示已接受的长期例外。
	BoundaryExceptionPermanent BoundaryExceptionClass = "permanent"
	// BoundaryExceptionTemporary 表示必须带移除条件的短期例外。
	BoundaryExceptionTemporary BoundaryExceptionClass = "temporary"
)

// BackendBoundaryRegistry 是后端边界规则的唯一事实源。
type BackendBoundaryRegistry struct {
	Owners []BackendBoundaryOwner
	Rules  []BackendBoundaryRule
}

// BackendBoundaryOwner 描述一个可审计的规则拥有者及其生产源码范围。
type BackendBoundaryOwner struct {
	ID           BoundaryOwnerID
	FilePatterns []string
	Reason       string
}

// BackendBoundaryRule 描述一条可求值的后端边界规则。
type BackendBoundaryRule struct {
	ID                 BoundaryRuleID
	Owner              BoundaryOwnerID
	Reason             string
	Kind               BoundaryRuleKind
	FilePatterns       []string
	Allow              []BoundaryImportPolicy
	Deny               []BoundaryImportPolicy
	ScopeAllow         []BoundaryFilePolicy
	Exceptions         []BoundaryException
	SkipTestFiles      bool
	DependencyPackages []string
}

// BoundaryImportPolicy 描述 owner、文件模式和导入前缀的审计策略。
type BoundaryImportPolicy struct {
	Owner        BoundaryOwnerID
	FilePattern  string
	ImportPrefix string
	Reason       string
}

// BoundaryFilePolicy 描述只按文件范围生效的审计策略。
type BoundaryFilePolicy struct {
	Owner       BoundaryOwnerID
	Scope       BoundaryScopeID
	FilePattern string
	Reason      string
}

// BoundaryException 描述经过审计的文件和导入例外。
type BoundaryException struct {
	ID           string
	Owner        BoundaryOwnerID
	FilePattern  string
	ImportPrefix string
	Class        BoundaryExceptionClass
	Reason       string
	RemoveWhen   string
}

// BoundaryEvaluation 记录统一求值器检查的范围、覆盖与违规。
type BoundaryEvaluation struct {
	CandidateFiles int
	MatchedFiles   int
	ByRule         map[BoundaryRuleID]int
	Excluded       []string
	Violations     []string
}

const backendBoundaryModulePath = "github.com/anthropic-ai/super-agent-v3"

// DefaultBackendBoundaryRegistry 返回可由调用方安全修改的默认规则深拷贝。
func DefaultBackendBoundaryRegistry() BackendBoundaryRegistry {
	return cloneBackendBoundaryRegistry(defaultBackendBoundaryRegistry())
}

// Rule 返回指定规则的深拷贝。
func (r BackendBoundaryRegistry) Rule(id BoundaryRuleID) (BackendBoundaryRule, bool) {
	for _, rule := range r.Rules {
		if rule.ID == id {
			return cloneBackendBoundaryRule(rule), true
		}
	}
	return BackendBoundaryRule{}, false
}

// OnionBoundaryRuleIDs 返回洋葱边界守卫应使用的 canonical 规则集合。
func OnionBoundaryRuleIDs() []BoundaryRuleID {
	return []BoundaryRuleID{
		"contract_reverse_pollution",
		"module_horizontal_deep_import",
		"module_no_direct_db_imports",
		"provider_no_store",
		"provider_no_platform_db",
		"platform_no_module",
		"platform_no_store",
		"store_sqlc_store_platform_only",
		"store_dependency_surface",
		"fx_assembly_scope",
		"mcpserver_orch_family",
		"mcpserver_ida_family",
		"hooks_no_mcpcontrol",
		"mcpcontrol_no_hooks",
		"hooks_no_platform_db",
	}
}

// CrossDomainBoundaryRuleIDs 返回跨域边界守卫应使用的 canonical 规则集合。
func CrossDomainBoundaryRuleIDs() []BoundaryRuleID {
	return []BoundaryRuleID{
		"contract_reverse_pollution",
		"module_horizontal_deep_import",
		"mcp_sidecar_narrow_import_surface",
		"provider_no_store",
		"provider_no_platform_db",
		"platform_no_module",
		"platform_no_store",
		"store_sqlc_store_platform_only",
		"store_dependency_surface",
		"fx_assembly_scope",
		"mcpserver_orch_family",
		"mcpserver_ida_family",
		"hooks_no_mcpcontrol",
		"mcpcontrol_no_hooks",
		"hooks_no_platform_db",
	}
}

// defaultBackendBoundaryRegistry 将每条可审计的边界事实集中为单一 registry，消费者不得复制这些列表。
func defaultBackendBoundaryRegistry() BackendBoundaryRegistry {
	patterns := defaultBackendBoundaryPatterns()
	return BackendBoundaryRegistry{
		Owners: defaultBackendBoundaryOwners(patterns),
		Rules:  defaultBackendBoundaryRules(patterns),
	}
}

type backendBoundaryPatterns struct {
	contract  []string
	module    []string
	provider  []string
	platform  []string
	sidecar   []string
	sqlc      []string
	store     []string
	storeMod  []string
	storeRoot []string
	fx        []string
	mcpOrch   []string
	mcpIDA    []string
	hooks     []string
	mcpctrl   []string
}

func defaultBackendBoundaryPatterns() backendBoundaryPatterns {
	return backendBoundaryPatterns{
		contract:  []string{"internal/contract/**/*.go"},
		module:    []string{"internal/module/**/*.go"},
		provider:  []string{"internal/provider/**/*.go"},
		platform:  []string{"internal/platform/**/*.go"},
		sidecar:   []string{"cmd/mcp-orch/**/*.go", "cmd/mcp-lsp/**/*.go", "cmd/mcp-ida/**/*.go"},
		sqlc:      []string{"internal/**/*.go", "cmd/**/*.go"},
		store:     []string{"internal/store/**/*.go"},
		storeMod:  []string{"internal/store/**/module.go"},
		storeRoot: []string{"internal/store/module.go"},
		fx:        []string{"internal/**/*.go", "cmd/**/*.go"},
		mcpOrch:   []string{"cmd/mcp-orch/**/*.go"},
		mcpIDA:    []string{"cmd/mcp-ida/**/*.go"},
		hooks:     []string{"internal/platform/hooks/**/*.go"},
		mcpctrl:   []string{"internal/platform/mcpcontrol/**/*.go"},
	}
}

func defaultBackendBoundaryOwners(patterns backendBoundaryPatterns) []BackendBoundaryOwner {
	return []BackendBoundaryOwner{
		{ID: "contract_boundary", FilePatterns: patterns.contract, Reason: "contract is the stable DTO and port surface; it must not depend on implementation packages"},
		{ID: "module_boundary", FilePatterns: patterns.module, Reason: "business modules own their internals and must communicate through contract or DTO ports"},
		{ID: "mcp_sidecar_boundary", FilePatterns: patterns.sidecar, Reason: "MCP sidecars are standalone entrypoints with only narrow shared internal dependencies"},
		{ID: "provider_runtime", FilePatterns: patterns.provider, Reason: "provider adapters own transport/runtime integration and must not reach into persistence internals"},
		{ID: "platform_runtime", FilePatterns: patterns.platform, Reason: "platform packages provide infrastructure primitives and must not depend upward on business or store ownership"},
		{ID: "sqlc_boundary", FilePatterns: patterns.sqlc, Reason: "sqlc generated code stays behind store and platform persistence boundaries"},
		{ID: "store_dependency", FilePatterns: combineBoundaryPatterns(patterns.store, patterns.storeMod, patterns.storeRoot), Reason: "store packages are anti-corruption adapters and may only consume their own package plus registered persistence ports"},
		{ID: "fx_assembly", FilePatterns: patterns.fx, Reason: "Fx belongs only to typed assembly scopes"},
		{ID: "mcpserver_family", FilePatterns: combineBoundaryPatterns(patterns.mcpOrch, patterns.mcpIDA), Reason: "MCP server families must not couple to sibling tool implementations"},
		{ID: "platform_control_boundary", FilePatterns: combineBoundaryPatterns(patterns.hooks, patterns.mcpctrl), Reason: "hooks and MCP control stay decoupled and hooks do not own database lifecycle"},
	}
}

func defaultBackendBoundaryRules(patterns backendBoundaryPatterns) []BackendBoundaryRule {
	return []BackendBoundaryRule{
		defaultContractReversePollutionRule(patterns),
		defaultModuleSiblingRule(patterns),
		defaultModuleDatabaseRule(patterns),
		defaultMCPSidecarRule(patterns),
		defaultProviderStoreRule(patterns),
		defaultProviderDatabaseRule(patterns),
		defaultPlatformModuleRule(patterns),
		defaultPlatformStoreRule(patterns),
		defaultSQLCStoreRule(patterns),
		defaultStoreDependencyRule(patterns),
		defaultFXAssemblyRule(patterns),
		defaultMCPServerOrchRule(patterns),
		defaultMCPServerIDARule(patterns),
		defaultHooksMCPControlRule(patterns),
		defaultMCPControlHooksRule(patterns),
		defaultHooksDatabaseRule(patterns),
	}
}

func defaultContractReversePollutionRule(patterns backendBoundaryPatterns) BackendBoundaryRule {
	return BackendBoundaryRule{
		ID:            "contract_reverse_pollution",
		Owner:         "contract_boundary",
		Reason:        "contract may only define stable DTOs and ports, never depend on implementation details",
		Kind:          BoundaryRuleDenyImports,
		FilePatterns:  patterns.contract,
		Deny:          boundaryPolicies("contract_boundary", patterns.contract, []string{"internal/store", "internal/module", "internal/provider", "internal/ui", "cmd", "frontend-app"}, "contract must not depend on implementation packages"),
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

func defaultMCPSidecarRule(patterns backendBoundaryPatterns) BackendBoundaryRule {
	return BackendBoundaryRule{
		ID:           "mcp_sidecar_narrow_import_surface",
		Owner:        "mcp_sidecar_boundary",
		Reason:       "cmd/mcp-* may use local sidecar packages and explicit shared contracts/platform primitives, but not app host, provider, or module services",
		Kind:         BoundaryRuleAllowInternalImports,
		FilePatterns: patterns.sidecar,
		Allow:        mcpSidecarAllowPolicies(),
		Deny: boundaryPolicies("mcp_sidecar_boundary", patterns.sidecar, []string{
			"internal/app",
			"internal/module",
			"internal/provider",
			"cmd/agent-terminal",
			"internal/platform/rpc/server",
			"internal/platform/rpc/push",
			"internal/platform/rpc/notification",
		}, "sidecars must not reach app host, module, provider, agent terminal, or RPC host internals"),
		SkipTestFiles:      true,
		DependencyPackages: []string{"cmd/mcp-orch", "cmd/mcp-lsp", "cmd/mcp-ida"},
	}
}

func defaultProviderStoreRule(patterns backendBoundaryPatterns) BackendBoundaryRule {
	return BackendBoundaryRule{ID: "provider_no_store", Owner: "provider_runtime", Reason: "provider adapters consume session contracts and must not import product store packages directly", Kind: BoundaryRuleDenyImports, FilePatterns: patterns.provider, Deny: boundaryPolicies("provider_runtime", patterns.provider, []string{"internal/store"}, "provider adapters must not own product store dependencies"), SkipTestFiles: true}
}

func defaultProviderDatabaseRule(patterns backendBoundaryPatterns) BackendBoundaryRule {
	return BackendBoundaryRule{ID: "provider_no_platform_db", Owner: "provider_runtime", Reason: "provider production code must not own SQLite handles or DB lifecycle", Kind: BoundaryRuleDenyImports, FilePatterns: patterns.provider, Deny: boundaryPolicies("provider_runtime", patterns.provider, []string{"internal/platform/db"}, "provider adapters must not own database lifecycle"), SkipTestFiles: true}
}

func defaultPlatformModuleRule(patterns backendBoundaryPatterns) BackendBoundaryRule {
	return BackendBoundaryRule{ID: "platform_no_module", Owner: "platform_runtime", Reason: "platform infrastructure stays below business modules", Kind: BoundaryRuleDenyImports, FilePatterns: patterns.platform, Deny: boundaryPolicies("platform_runtime", patterns.platform, []string{"internal/module"}, "platform infrastructure must not depend on module implementations"), SkipTestFiles: true}
}

func defaultPlatformStoreRule(patterns backendBoundaryPatterns) BackendBoundaryRule {
	return BackendBoundaryRule{ID: "platform_no_store", Owner: "platform_runtime", Reason: "platform infrastructure must not depend on product store subpackages", Kind: BoundaryRuleDenyImports, FilePatterns: patterns.platform, Deny: boundaryPolicies("platform_runtime", patterns.platform, []string{"internal/store"}, "platform infrastructure must not depend on product store packages"), SkipTestFiles: true}
}

func defaultSQLCStoreRule(patterns backendBoundaryPatterns) BackendBoundaryRule {
	return BackendBoundaryRule{
		ID:           "store_sqlc_store_platform_only",
		Owner:        "sqlc_boundary",
		Reason:       "store/sqlc generated types must stay inside persistence implementation seams",
		Kind:         BoundaryRuleScopedImport,
		FilePatterns: patterns.sqlc,
		Deny:         boundaryPolicies("sqlc_boundary", patterns.sqlc, []string{"internal/store/sqlc"}, "sqlc imports are restricted to persistence seams"),
		ScopeAllow: []BoundaryFilePolicy{
			boundaryFilePolicy("sqlc_boundary", BoundaryScopeStore, "store packages are the canonical anti-corruption wrappers around sqlc"),
			boundaryFilePolicy("sqlc_boundary", BoundaryScopePlatformDB, "platform DB schema verification may inspect sqlc-backed persistence boundaries"),
		},
		SkipTestFiles: true,
	}
}

// defaultStoreDependencyRule 将 store 同包内聚、共享端口和装配文件许可收敛为一条 typed 规则。
func defaultStoreDependencyRule(patterns backendBoundaryPatterns) BackendBoundaryRule {
	owner := BoundaryOwnerID("store_dependency")
	allow := boundaryPolicies(owner, patterns.store, []string{
		"internal/platform/config",
		"internal/platform/db",
		"internal/platform/sharedfilefs",
		"internal/platform/sharedfilegitignore",
		"internal/platform/sharedfilepath",
		"internal/store/sqlc",
		"internal/contract",
		"internal/dto",
		"github.com/jackc/pgx/v5/pgtype",
	}, "store packages may consume only registered persistence contracts and shared path adapters")
	allow = append(allow, boundaryPolicies(owner, patterns.storeMod, []string{
		"go.uber.org/fx",
		"github.com/jackc/pgx/v5/pgxpool",
	}, "store package module files may declare their Fx and pool constructors")...)
	allow = append(allow, boundaryPolicies(owner, patterns.storeRoot, []string{
		"internal/store",
		"go.uber.org/fx",
		"github.com/jackc/pgx/v5/pgxpool",
	}, "the store root module is the canonical store submodule aggregator")...)
	return BackendBoundaryRule{
		ID:            "store_dependency_surface",
		Owner:         owner,
		Reason:        "store packages must depend only on their own implementation package and registered persistence ports",
		Kind:          BoundaryRuleStoreImports,
		FilePatterns:  combineBoundaryPatterns(patterns.store, patterns.storeMod, patterns.storeRoot),
		Allow:         allow,
		SkipTestFiles: true,
	}
}

func combineBoundaryPatterns(groups ...[]string) []string {
	var combined []string
	for _, group := range groups {
		combined = append(combined, group...)
	}
	return combined
}

func defaultFXAssemblyRule(patterns backendBoundaryPatterns) BackendBoundaryRule {
	owner := BoundaryOwnerID("fx_assembly")
	return BackendBoundaryRule{
		ID:           "fx_assembly_scope",
		Owner:        owner,
		Reason:       "Fx imports belong only to registered assembly entrypoints",
		Kind:         BoundaryRuleScopedImport,
		FilePatterns: patterns.fx,
		Deny:         boundaryPolicies(owner, patterns.fx, []string{"go.uber.org/fx"}, "Fx must not leak outside assembly files"),
		ScopeAllow: []BoundaryFilePolicy{
			boundaryFilePolicy(owner, BoundaryScopeFXInternalApp, "internal/app owns the desktop root graph"),
			boundaryFilePolicy(owner, BoundaryScopeFXModuleFile, "module.go files declare package-local Fx modules"),
			boundaryFilePolicy(owner, BoundaryScopeFXMCPOrch, "mcp-orch owns its standalone Fx graph"),
			boundaryFilePolicy(owner, BoundaryScopeFXMCPIDA, "mcp-ida owns its standalone Fx graph"),
			boundaryFilePolicy(owner, BoundaryScopeFXCommandEntrypoint, "command entrypoints may assemble their process graph"),
			boundaryFilePolicy(owner, BoundaryScopeFXMCPLSP, "mcp-lsp centralizes Fx assembly in fx.go"),
		},
		SkipTestFiles: true,
	}
}

func defaultMCPServerOrchRule(patterns backendBoundaryPatterns) BackendBoundaryRule {
	return BackendBoundaryRule{ID: "mcpserver_orch_family", Owner: "mcpserver_family", Reason: "orchestration MCP servers must not depend on LSP or IDA tool families", Kind: BoundaryRuleDenyImports, FilePatterns: patterns.mcpOrch, Deny: boundaryPolicies("mcpserver_family", patterns.mcpOrch, []string{"internal/tool/lsp", "internal/tool/ida"}, "orchestration servers stay independent of sibling tool families"), SkipTestFiles: true}
}

func defaultMCPServerIDARule(patterns backendBoundaryPatterns) BackendBoundaryRule {
	return BackendBoundaryRule{ID: "mcpserver_ida_family", Owner: "mcpserver_family", Reason: "IDA MCP servers must not depend on LSP or orchestration tool families", Kind: BoundaryRuleDenyImports, FilePatterns: patterns.mcpIDA, Deny: boundaryPolicies("mcpserver_family", patterns.mcpIDA, []string{"internal/tool/lsp", "internal/tool/orchestration"}, "IDA servers stay independent of sibling tool families"), SkipTestFiles: true}
}

func defaultHooksMCPControlRule(patterns backendBoundaryPatterns) BackendBoundaryRule {
	return BackendBoundaryRule{ID: "hooks_no_mcpcontrol", Owner: "platform_control_boundary", Reason: "hooks publish contracts instead of importing MCP control implementations", Kind: BoundaryRuleDenyImports, FilePatterns: patterns.hooks, Deny: boundaryPolicies("platform_control_boundary", patterns.hooks, []string{"internal/platform/mcpcontrol"}, "hooks must not depend on MCP control"), SkipTestFiles: true}
}

func defaultMCPControlHooksRule(patterns backendBoundaryPatterns) BackendBoundaryRule {
	return BackendBoundaryRule{ID: "mcpcontrol_no_hooks", Owner: "platform_control_boundary", Reason: "MCP control consumes injected hook ports instead of hook implementations", Kind: BoundaryRuleDenyImports, FilePatterns: patterns.mcpctrl, Deny: boundaryPolicies("platform_control_boundary", patterns.mcpctrl, []string{"internal/platform/hooks"}, "MCP control must not depend on hooks"), SkipTestFiles: true}
}

func defaultHooksDatabaseRule(patterns backendBoundaryPatterns) BackendBoundaryRule {
	return BackendBoundaryRule{ID: "hooks_no_platform_db", Owner: "platform_control_boundary", Reason: "hooks must not own database lifecycle in production or test helpers", Kind: BoundaryRuleDenyImports, FilePatterns: patterns.hooks, Deny: boundaryPolicies("platform_control_boundary", patterns.hooks, []string{"internal/platform/db"}, "hooks must consume ports instead of database lifecycle"), SkipTestFiles: false}
}

// boundaryFilePolicy 从注册范围派生文件模式，避免调用方另建宽泛白名单。
func boundaryFilePolicy(owner BoundaryOwnerID, scope BoundaryScopeID, reason string) BoundaryFilePolicy {
	// 未注册 scope 故意保留空 pattern，由统一 registry validator 在求值前失败关闭。
	pattern, _ := boundaryScopeFilePattern(scope)
	return BoundaryFilePolicy{Owner: owner, Scope: scope, FilePattern: pattern, Reason: reason}
}

// boundaryScopeFilePattern 将 typed scope 映射为求值器支持的唯一文件模式。
func boundaryScopeFilePattern(scope BoundaryScopeID) (string, bool) {
	switch scope {
	case BoundaryScopeStore:
		return "internal/store/**/*.go", true
	case BoundaryScopePlatformDB:
		return "internal/platform/db/**/*.go", true
	case BoundaryScopeFXInternalApp:
		return "internal/app/**/*.go", true
	case BoundaryScopeFXModuleFile:
		return "internal/**/module.go", true
	case BoundaryScopeFXMCPOrch:
		return "cmd/mcp-orch/**/*.go", true
	case BoundaryScopeFXMCPIDA:
		return "cmd/mcp-ida/**/*.go", true
	case BoundaryScopeFXCommandEntrypoint:
		return "cmd/*/main.go", true
	case BoundaryScopeFXMCPLSP:
		return "cmd/mcp-lsp/fx.go", true
	default:
		return "", false
	}
}

// boundaryPolicies 将同一 owner 的模式和导入前缀展开为可审计的精确策略。
func boundaryPolicies(owner BoundaryOwnerID, filePatterns, prefixes []string, reason string) []BoundaryImportPolicy {
	policies := make([]BoundaryImportPolicy, 0, len(filePatterns)*len(prefixes))
	for _, pattern := range filePatterns {
		for _, prefix := range prefixes {
			policies = append(policies, BoundaryImportPolicy{Owner: owner, FilePattern: pattern, ImportPrefix: prefix, Reason: reason})
		}
	}
	return policies
}

// mcpSidecarAllowPolicies 保留各 sidecar 的既有精确导入白名单，禁止泛化为全局平台许可。
func mcpSidecarAllowPolicies() []BoundaryImportPolicy {
	owner := BoundaryOwnerID("mcp_sidecar_boundary")
	patterns := []string{"cmd/mcp-orch/**/*.go", "cmd/mcp-lsp/**/*.go", "cmd/mcp-ida/**/*.go"}
	policies := boundaryPolicies(owner, patterns, []string{
		"internal/contract",
		"internal/dto",
		"internal/mcpserver/common",
		"internal/platform/config",
		"internal/platform/rlimit",
		"internal/platform/runner",
		"internal/platform/runtimeenv",
		"internal/platform/runtimesafe",
		"internal/platform/securefs",
		"internal/platform/shared",
		"internal/util",
	}, "shared sidecar contract or platform primitive")
	policies = append(policies, boundaryPolicies(owner, []string{"cmd/mcp-orch/**/*.go"}, []string{
		"internal/platform/bus",
		"internal/platform/db",
		"internal/platform/discovery",
		"internal/platform/eventsurface",
		"internal/platform/metrics",
		"internal/platform/notify",
		"internal/platform/rpc",
		"internal/platform/sharedfilefs",
		"internal/platform/sharedfilegitignore",
		"internal/platform/sharedfilepath",
		"internal/platform/statemachine",
	}, "orchestration sidecar runtime primitive")...)
	policies = append(policies, boundaryPolicies(owner, []string{"cmd/mcp-lsp/**/*.go"}, []string{
		"internal/platform/discovery",
		"internal/platform/metrics",
	}, "LSP sidecar runtime primitive")...)
	policies = append(policies, boundaryPolicies(owner, []string{"cmd/mcp-ida/**/*.go"}, []string{
		"internal/platform/metrics",
	}, "IDA sidecar runtime primitive")...)
	return policies
}

func cloneBackendBoundaryRegistry(registry BackendBoundaryRegistry) BackendBoundaryRegistry {
	cloned := BackendBoundaryRegistry{
		Owners: make([]BackendBoundaryOwner, len(registry.Owners)),
		Rules:  make([]BackendBoundaryRule, len(registry.Rules)),
	}
	for i, owner := range registry.Owners {
		cloned.Owners[i] = BackendBoundaryOwner{ID: owner.ID, FilePatterns: append([]string(nil), owner.FilePatterns...), Reason: owner.Reason}
	}
	for i, rule := range registry.Rules {
		cloned.Rules[i] = cloneBackendBoundaryRule(rule)
	}
	return cloned
}

func cloneBackendBoundaryRule(rule BackendBoundaryRule) BackendBoundaryRule {
	cloned := rule
	cloned.FilePatterns = append([]string(nil), rule.FilePatterns...)
	cloned.Allow = append([]BoundaryImportPolicy(nil), rule.Allow...)
	cloned.Deny = append([]BoundaryImportPolicy(nil), rule.Deny...)
	cloned.ScopeAllow = append([]BoundaryFilePolicy(nil), rule.ScopeAllow...)
	cloned.Exceptions = append([]BoundaryException(nil), rule.Exceptions...)
	cloned.DependencyPackages = append([]string(nil), rule.DependencyPackages...)
	return cloned
}
