package archtest

// BoundaryOwnerID 标识后端边界规则拥有者。
type BoundaryOwnerID string

// BoundaryRuleID 标识一条稳定的后端边界规则。
type BoundaryRuleID string

// BoundaryGuardID 标识一个有真实 Go 测试入口的专项边界守卫。
type BoundaryGuardID string

// BoundarySurfaceID 标识一个由 registry 治理的后端顶层目录。
type BoundarySurfaceID string

// BoundaryRuleKind 标识规则求值方式。
type BoundaryRuleKind string

// BoundaryScopeID 标识经过 registry 注册的文件范围。
type BoundaryScopeID string

const (
	// BoundaryRuleDenyImports 拒绝匹配文件导入指定前缀。
	BoundaryRuleDenyImports BoundaryRuleKind = "deny_imports"
	// BoundaryRuleAllowInternalImports 限制匹配文件导入内部或 cmd 包的白名单。
	BoundaryRuleAllowInternalImports BoundaryRuleKind = "allow_internal_imports"
	// BoundaryRuleAllowRepositoryImports 限制匹配文件导入仓库包的精确白名单。
	BoundaryRuleAllowRepositoryImports BoundaryRuleKind = "allow_repository_imports"
	// BoundaryRuleAllowExternalImports 限制匹配文件导入外部 module root 的白名单。
	BoundaryRuleAllowExternalImports BoundaryRuleKind = "allow_external_imports"
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
	Owners   []BackendBoundaryOwner
	Rules    []BackendBoundaryRule
	Guards   []BackendBoundaryGuard
	Surfaces []BackendBoundarySurface

	canonicalSource string
}

// BackendBoundaryGuard 描述专项守卫的稳定入口和治理原因。
type BackendBoundaryGuard struct {
	ID        BoundaryGuardID
	File      string
	TestNames []string
	BuildTags []string
	AppliesTo []BoundarySurfaceID
	Reason    string
}

// BackendBoundarySurface 把后端顶层目录绑定到 canonical rule 或专项守卫。
type BackendBoundarySurface struct {
	Path     string
	RuleIDs  []BoundaryRuleID
	GuardIDs []BoundaryGuardID
	Reason   string
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

const backendBoundaryModulePath = "github.com/lihah111222333-cloud/super-dolphin-agent"

const defaultBackendBoundaryRegistrySource = "internal/archtest/backend_boundary_registry.go"

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
		"contract_dto_no_framework_imports",
		"contract_reverse_pollution",
		"module_horizontal_deep_import",
		"module_no_direct_db_imports",
		"module_no_outer_implementation_imports",
		"module_no_store_imports",
		"provider_external_import_surface",
		"provider_no_store",
		"provider_no_platform_db",
		"provider_unified_no_concrete_imports",
		"platform_no_module",
		"platform_no_store",
		"store_sqlc_store_platform_only",
		"store_dependency_surface",
		"app_adapter_narrow_import_surface",
		"fx_assembly_scope",
		"mcpserver_orch_family",
		"mcpserver_ida_family",
		"hooks_no_mcpcontrol",
		"mcpcontrol_no_hooks",
		"hooks_no_platform_db",
		"pkg_no_internal_imports",
	}
}

// CrossDomainBoundaryRuleIDs 返回跨域边界守卫应使用的 canonical 规则集合。
func CrossDomainBoundaryRuleIDs() []BoundaryRuleID {
	return []BoundaryRuleID{
		"contract_dto_no_framework_imports",
		"contract_reverse_pollution",
		"module_horizontal_deep_import",
		"module_no_outer_implementation_imports",
		"mcp_sidecar_narrow_import_surface",
		"provider_external_import_surface",
		"provider_no_store",
		"provider_no_platform_db",
		"provider_unified_no_concrete_imports",
		"platform_no_module",
		"platform_no_store",
		"store_sqlc_store_platform_only",
		"store_dependency_surface",
		"app_adapter_narrow_import_surface",
		"fx_assembly_scope",
		"mcpserver_orch_family",
		"mcpserver_ida_family",
		"hooks_no_mcpcontrol",
		"mcpcontrol_no_hooks",
		"hooks_no_platform_db",
		"pkg_no_internal_imports",
	}
}

// defaultBackendBoundaryRegistry 将每条可审计的边界事实集中为单一 registry，消费者不得复制这些列表。
func defaultBackendBoundaryRegistry() BackendBoundaryRegistry {
	patterns := defaultBackendBoundaryPatterns()
	return BackendBoundaryRegistry{
		Owners:          defaultBackendBoundaryOwners(patterns),
		Rules:           defaultBackendBoundaryRules(patterns),
		Guards:          defaultBackendBoundaryGuards(),
		Surfaces:        defaultBackendBoundarySurfaces(),
		canonicalSource: defaultBackendBoundaryRegistrySource,
	}
}

// defaultBackendBoundaryGuards 集中登记专项守卫入口及其可证明适用的后端 surface。
func defaultBackendBoundaryGuards() []BackendBoundaryGuard {
	return []BackendBoundaryGuard{
		{ID: "backend_surface_governance", File: "internal/archtest/backend_boundary_governance_test.go", TestNames: []string{"TestValidateDefaultBackendBoundaryGovernance"}, AppliesTo: []BoundarySurfaceID{"internal/archtest"}, Reason: "the governance guard fails when a backend top-level Go surface is missing or stale"},
		{ID: "acp_node_boundary", File: "internal/archtest/acp_node_boundary_guard_test.go", TestNames: []string{"TestACPNodeBoundary", "TestACPNodeNarrowImportBoundary"}, AppliesTo: []BoundarySurfaceID{"cmd/acp-node", "internal/devtools"}, Reason: "experimental ACP development code must remain isolated from production provider, UI, RPC, and persistence surfaces"},
		{ID: "backend_boundary_single_source", File: "internal/archtest/backend_boundary_single_source_test.go", TestNames: []string{"TestBackendBoundaryRuleFactsHaveOneSource"}, AppliesTo: []BoundarySurfaceID{"internal/archtest"}, Reason: "canonical backend boundary facts must not be duplicated by procedural evaluators"},
		{ID: "rpc_runtime_e2e", File: "internal/e2e/rpc_runtime/runtime_e2e_test.go", TestNames: []string{"TestAgentRuntimeRPCBlackBox", "TestRPCRuntimeE2EEnvIsIsolated"}, BuildTags: []string{"e2e"}, AppliesTo: []BoundarySurfaceID{"internal/e2e"}, Reason: "the tagged RPC runtime suite validates the backend process boundary end to end"},
		{ID: "ignored_go_tests", File: "internal/guards/ignored_test_guard_test.go", TestNames: []string{"TestTrackedGoTestsDoNotUseIgnoreBuildConstraint"}, AppliesTo: []BoundarySurfaceID{"internal/guards"}, Reason: "tracked Go tests must remain attached to an executable test chain instead of using the unowned ignore build tag"},
		{ID: "rollback_skip_markers", File: "internal/guards/rollback_skip_guard_test.go", TestNames: []string{"TestGoTestsDoNotContainRollbackSkipMarkers"}, AppliesTo: []BoundarySurfaceID{"internal/guards"}, Reason: "the repository guard rejects hidden rollback skip markers in Go tests"},
		{ID: "dependency_direction", File: "internal/archtest/dependency_direction_test.go", TestNames: []string{"TestDependencyDirection"}, AppliesTo: []BoundarySurfaceID{"internal/contract", "internal/dto", "internal/mcpserver", "internal/module", "internal/platform", "internal/provider", "internal/store"}, Reason: "dependency direction tests protect typed backend layer relationships"},
		{ID: "fx_graph", File: "internal/archtest/fx_graph_test.go", TestNames: []string{"TestFxValidateApp"}, AppliesTo: []BoundarySurfaceID{"internal/app"}, Reason: "the desktop composition root must retain a valid Fx graph"},
		{ID: "pkg_public_boundary", File: "internal/archtest/backend_boundary_guard_coverage_test.go", TestNames: []string{"TestPkgNoInternalImportsRuleRejectsRepositoryInternals"}, AppliesTo: []BoundarySurfaceID{"pkg/cronmetrics", "pkg/dagmetrics", "pkg/dreammetrics", "pkg/logger", "pkg/skillblocks", "pkg/skillmetrics"}, Reason: "public pkg libraries must reject both repository internals and command entrypoints"},
		{ID: "ui_wails_boundary", File: "internal/archtest/ui_wails_guard_test.go", TestNames: []string{"TestUIWailsNoDirectUIStateImport", "TestUIWailsActiveAgentPredicateFromContract"}, AppliesTo: []BoundarySurfaceID{"internal/ui"}, Reason: "Wails UI bindings consume contract-facing state instead of module implementations"},
	}
}

// defaultBackendBoundarySurfaces 显式登记当前后端一级目录的 canonical rule 与专项 guard 归属。
func defaultBackendBoundarySurfaces() []BackendBoundarySurface {
	return []BackendBoundarySurface{
		backendBoundarySurface("cmd/agent-runtime", "agent runtime process assembly", []BoundaryRuleID{"command_narrow_import_surface", "fx_assembly_scope"}, nil),
		backendBoundarySurface("cmd/agent-terminal", "agent terminal process assembly", []BoundaryRuleID{"command_narrow_import_surface", "fx_assembly_scope"}, nil),
		backendBoundarySurface("cmd/codex-worktree-setup", "Codex worktree LSP bootstrap command", []BoundaryRuleID{"command_narrow_import_surface", "fx_assembly_scope"}, nil),
		backendBoundarySurface("cmd/build-trusted-gate-launcher", "trusted gate launcher build command", []BoundaryRuleID{"command_narrow_import_surface", "fx_assembly_scope"}, nil),
		backendBoundarySurface("cmd/mcp-ida", "IDA MCP sidecar boundary", []BoundaryRuleID{"mcp_sidecar_narrow_import_surface", "fx_assembly_scope", "mcpserver_ida_family"}, nil),
		backendBoundarySurface("cmd/mcp-lsp", "LSP MCP sidecar boundary", []BoundaryRuleID{"mcp_sidecar_narrow_import_surface", "fx_assembly_scope"}, nil),
		backendBoundarySurface("cmd/mcp-orch", "orchestration MCP sidecar boundary", []BoundaryRuleID{"mcp_sidecar_narrow_import_surface", "fx_assembly_scope", "mcpserver_orch_family"}, nil),
		backendBoundarySurface("cmd/mcp-schema-compiler-helper", "one-shot MCP schema compiler helper", []BoundaryRuleID{"command_narrow_import_surface", "fx_assembly_scope"}, nil),
		backendBoundarySurface("cmd/super-dolphin-gate", "standalone gate coordinator and worker command", []BoundaryRuleID{"command_narrow_import_surface", "fx_assembly_scope"}, nil),
		backendBoundarySurface("cmd/super-dolphin-release-manifest", "release manifest command assembly", []BoundaryRuleID{"command_narrow_import_surface", "fx_assembly_scope"}, nil),
		backendBoundarySurface("cmd/super-dolphin-guard", "detached probation Guard command", []BoundaryRuleID{"command_narrow_import_surface", "fx_assembly_scope"}, nil),
		backendBoundarySurface("cmd/super-dolphin-updater", "updater command assembly", []BoundaryRuleID{"command_narrow_import_surface", "fx_assembly_scope"}, nil),
		backendBoundarySurface("internal/app", "desktop composition root", []BoundaryRuleID{"app_adapter_narrow_import_surface", "fx_assembly_scope"}, []BoundaryGuardID{"fx_graph"}),
		backendBoundarySurface("internal/archtest", "architecture governance implementation", []BoundaryRuleID{"fx_assembly_scope"}, []BoundaryGuardID{"backend_surface_governance", "backend_boundary_single_source"}),
		backendBoundarySurface("internal/contract", "stable DTO and port contracts", []BoundaryRuleID{"contract_dto_no_framework_imports", "contract_reverse_pollution"}, []BoundaryGuardID{"dependency_direction"}),
		backendBoundarySurface("internal/devtools", "backend developer tooling", []BoundaryRuleID{"internal_support_narrow_import_surface", "fx_assembly_scope"}, []BoundaryGuardID{"acp_node_boundary"}),
		backendBoundarySurface("cmd/acp-node", "experimental ACP development node", []BoundaryRuleID{"command_narrow_import_surface"}, []BoundaryGuardID{"acp_node_boundary"}),
		backendBoundarySurface("internal/dto", "transport-neutral data transfer objects", []BoundaryRuleID{"contract_dto_no_framework_imports", "internal_support_narrow_import_surface", "fx_assembly_scope"}, []BoundaryGuardID{"dependency_direction"}),
		backendBoundarySurface("internal/e2e", "backend end-to-end test surface", nil, []BoundaryGuardID{"rpc_runtime_e2e"}),
		backendBoundarySurface("internal/guards", "repository-level test guard surface", nil, []BoundaryGuardID{"ignored_go_tests", "rollback_skip_markers"}),
		backendBoundarySurface("internal/mcpserver", "shared MCP server implementations", []BoundaryRuleID{"fx_assembly_scope"}, []BoundaryGuardID{"dependency_direction"}),
		backendBoundarySurface("internal/module", "business module ownership", []BoundaryRuleID{"module_horizontal_deep_import", "module_no_direct_db_imports", "module_no_outer_implementation_imports", "module_no_store_imports", "fx_assembly_scope"}, []BoundaryGuardID{"dependency_direction"}),
		backendBoundarySurface("internal/platform", "infrastructure runtime layer", []BoundaryRuleID{"platform_no_module", "platform_no_store", "hooks_no_mcpcontrol", "mcpcontrol_no_hooks", "hooks_no_platform_db", "fx_assembly_scope"}, []BoundaryGuardID{"dependency_direction"}),
		backendBoundarySurface("internal/provider", "provider adapter runtime", []BoundaryRuleID{"provider_external_import_surface", "provider_no_store", "provider_no_platform_db", "provider_unified_no_concrete_imports", "fx_assembly_scope"}, []BoundaryGuardID{"dependency_direction"}),
		backendBoundarySurface("internal/store", "persistence anti-corruption layer", []BoundaryRuleID{"store_sqlc_store_platform_only", "store_dependency_surface", "fx_assembly_scope"}, []BoundaryGuardID{"dependency_direction"}),
		backendBoundarySurface("internal/testutil", "shared backend test support", []BoundaryRuleID{"internal_support_narrow_import_surface", "fx_assembly_scope"}, nil),
		backendBoundarySurface("internal/ui", "Wails backend binding layer", []BoundaryRuleID{"fx_assembly_scope"}, []BoundaryGuardID{"ui_wails_boundary"}),
		backendBoundarySurface("internal/util", "shared backend utilities", []BoundaryRuleID{"internal_support_narrow_import_surface", "fx_assembly_scope"}, nil),
		backendBoundarySurface("pkg/cronmetrics", "public cron recovery metrics library", []BoundaryRuleID{"pkg_no_internal_imports"}, []BoundaryGuardID{"pkg_public_boundary"}),
		backendBoundarySurface("pkg/dagmetrics", "public DAG metrics library", []BoundaryRuleID{"pkg_no_internal_imports"}, []BoundaryGuardID{"pkg_public_boundary"}),
		backendBoundarySurface("pkg/dreammetrics", "public dream metrics library", []BoundaryRuleID{"pkg_no_internal_imports"}, []BoundaryGuardID{"pkg_public_boundary"}),
		backendBoundarySurface("pkg/logger", "public logging library", []BoundaryRuleID{"pkg_no_internal_imports"}, []BoundaryGuardID{"pkg_public_boundary"}),
		backendBoundarySurface("pkg/skillblocks", "public skill injection marker parser", []BoundaryRuleID{"pkg_no_internal_imports"}, []BoundaryGuardID{"pkg_public_boundary"}),
		backendBoundarySurface("pkg/skillmetrics", "public skill metrics library", []BoundaryRuleID{"pkg_no_internal_imports"}, []BoundaryGuardID{"pkg_public_boundary"}),
	}
}

func backendBoundarySurface(path, reason string, rules []BoundaryRuleID, guards []BoundaryGuardID) BackendBoundarySurface {
	return BackendBoundarySurface{Path: path, RuleIDs: rules, GuardIDs: guards, Reason: reason}
}

type backendBoundaryPatterns struct {
	contract        []string
	module          []string
	provider        []string
	providerModules []string
	providerUnified []string
	platform        []string
	sidecar         []string
	agentRuntime    []string
	agentTerminal   []string
	codexWorktree   []string
	trustedLauncher []string
	gateCLI         []string
	releaseManifest []string
	guard           []string
	updater         []string
	devtools        []string
	dto             []string
	testutil        []string
	util            []string
	sqlc            []string
	store           []string
	storeMod        []string
	storeRoot       []string
	fx              []string
	mcpOrch         []string
	mcpIDA          []string
	schemaHelper    []string
	acpNode         []string
	hooks           []string
	mcpctrl         []string
	pkg             []string
}

// defaultBackendBoundaryPatterns 汇总架构边界扫描使用的规范路径模式。
func defaultBackendBoundaryPatterns() backendBoundaryPatterns {
	return backendBoundaryPatterns{
		contract:        []string{"internal/contract/**/*.go"},
		module:          []string{"internal/module/**/*.go"},
		provider:        []string{"internal/provider/**/*.go"},
		providerModules: []string{"internal/provider/**/module.go"},
		providerUnified: []string{"internal/provider/unified/**/*.go"},
		platform:        []string{"internal/platform/**/*.go"},
		sidecar:         []string{"cmd/mcp-orch/**/*.go", "cmd/mcp-lsp/**/*.go", "cmd/mcp-ida/**/*.go"},
		agentRuntime:    []string{"cmd/agent-runtime/**/*.go"},
		agentTerminal:   []string{"cmd/agent-terminal/**/*.go"},
		codexWorktree:   []string{"cmd/codex-worktree-setup/**/*.go"},
		trustedLauncher: []string{"cmd/build-trusted-gate-launcher/**/*.go"},
		gateCLI:         []string{"cmd/super-dolphin-gate/**/*.go"},
		releaseManifest: []string{"cmd/super-dolphin-release-manifest/**/*.go"},
		guard:           []string{"cmd/super-dolphin-guard/**/*.go"},
		updater:         []string{"cmd/super-dolphin-updater/**/*.go"},
		devtools:        []string{"internal/devtools/**/*.go"},
		dto:             []string{"internal/dto/**/*.go"},
		testutil:        []string{"internal/testutil/**/*.go"},
		util:            []string{"internal/util/**/*.go"},
		sqlc:            []string{"internal/**/*.go", "cmd/**/*.go"},
		store:           []string{"internal/store/**/*.go"},
		storeMod:        []string{"internal/store/**/module.go"},
		storeRoot:       []string{"internal/store/module.go"},
		fx:              []string{"internal/**/*.go", "cmd/**/*.go"},
		mcpOrch:         []string{"cmd/mcp-orch/**/*.go"},
		mcpIDA:          []string{"cmd/mcp-ida/**/*.go"},
		schemaHelper:    []string{"cmd/mcp-schema-compiler-helper/**/*.go"},
		acpNode:         []string{"cmd/acp-node/**/*.go"},
		hooks:           []string{"internal/platform/hooks/**/*.go"},
		mcpctrl:         []string{"internal/platform/mcpcontrol/**/*.go"},
		pkg:             []string{"pkg/**/*.go"},
	}
}

func defaultBackendBoundaryOwners(patterns backendBoundaryPatterns) []BackendBoundaryOwner {
	return []BackendBoundaryOwner{
		{ID: "contract_boundary", FilePatterns: combineBoundaryPatterns(patterns.contract, patterns.dto), Reason: "contract and DTO packages are the stable transport-neutral port surface"},
		{ID: "module_boundary", FilePatterns: patterns.module, Reason: "business modules own their internals and must communicate through contract or DTO ports"},
		{ID: "mcp_sidecar_boundary", FilePatterns: patterns.sidecar, Reason: "MCP sidecars are standalone entrypoints with only narrow shared internal dependencies"},
		{ID: "command_boundary", FilePatterns: commandBoundaryPatterns(patterns), Reason: "standalone commands import only their registered host or runtime seams"},
		{ID: "internal_support_boundary", FilePatterns: internalSupportBoundaryPatterns(patterns), Reason: "shared support packages keep narrow, per-source internal dependency surfaces"},
		{ID: "provider_runtime", FilePatterns: combineBoundaryPatterns(patterns.provider, patterns.providerModules, patterns.providerUnified), Reason: "provider adapters own transport/runtime integration and must not reach into persistence internals"},
		{ID: "platform_runtime", FilePatterns: patterns.platform, Reason: "platform packages provide infrastructure primitives and must not depend upward on business or store ownership"},
		{ID: "sqlc_boundary", FilePatterns: patterns.sqlc, Reason: "sqlc generated code stays behind store and platform persistence boundaries"},
		{ID: "store_dependency", FilePatterns: combineBoundaryPatterns(patterns.store, patterns.storeMod, patterns.storeRoot), Reason: "store packages are anti-corruption adapters and may only consume their own package plus registered persistence ports"},
		defaultAppAdapterOwner(),
		{ID: "fx_assembly", FilePatterns: patterns.fx, Reason: "Fx belongs only to typed assembly scopes"},
		{ID: "mcpserver_family", FilePatterns: combineBoundaryPatterns(patterns.mcpOrch, patterns.mcpIDA), Reason: "MCP server families must not couple to sibling tool implementations"},
		{ID: "platform_control_boundary", FilePatterns: combineBoundaryPatterns(patterns.hooks, patterns.mcpctrl), Reason: "hooks and MCP control stay decoupled and hooks do not own database lifecycle"},
		{ID: "public_pkg_boundary", FilePatterns: patterns.pkg, Reason: "public pkg libraries must remain reusable without depending on repository internals or commands"},
	}
}

// defaultBackendBoundaryRules 汇总仓库默认启用的后端依赖边界规则。
func defaultBackendBoundaryRules(patterns backendBoundaryPatterns) []BackendBoundaryRule {
	return []BackendBoundaryRule{
		defaultContractReversePollutionRule(patterns),
		defaultContractDTOFrameworkRule(patterns),
		defaultModuleSiblingRule(patterns),
		defaultModuleDatabaseRule(patterns),
		defaultModuleOuterImplementationRule(patterns),
		defaultModuleStoreRule(patterns),
		defaultMCPSidecarRule(patterns),
		defaultCommandNarrowImportRule(patterns),
		defaultInternalSupportNarrowImportRule(patterns),
		defaultProviderExternalImportRule(patterns),
		defaultProviderStoreRule(patterns),
		defaultProviderDatabaseRule(patterns),
		defaultProviderUnifiedConcreteRule(patterns),
		defaultPlatformModuleRule(patterns),
		defaultPlatformStoreRule(patterns),
		defaultSQLCStoreRule(patterns),
		defaultStoreDependencyRule(patterns),
		defaultAppAdapterRule(),
		defaultFXAssemblyRule(patterns),
		defaultMCPServerOrchRule(patterns),
		defaultMCPServerIDARule(patterns),
		defaultHooksMCPControlRule(patterns),
		defaultMCPControlHooksRule(patterns),
		defaultHooksDatabaseRule(patterns),
		defaultPublicPkgRule(patterns),
	}
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

func defaultProviderExternalImportRule(patterns backendBoundaryPatterns) BackendBoundaryRule {
	owner := BoundaryOwnerID("provider_runtime")
	allow := boundaryPolicies(owner, patterns.provider, []string{
		"github.com/BurntSushi/toml",
		"github.com/gorilla/websocket",
		"github.com/kelindar/event",
		"golang.org/x/net",
		"golang.org/x/sync",
		"golang.org/x/sys",
	}, "provider production code may import only registered external module roots")
	allow = append(allow, boundaryPolicies(owner, patterns.providerModules, []string{
		"go.uber.org/fx",
	}, "provider module files may declare package-local Fx modules")...)
	return BackendBoundaryRule{
		ID:            "provider_external_import_surface",
		Owner:         owner,
		Reason:        "provider production code keeps a closed external module surface, with Fx limited to module assembly files",
		Kind:          BoundaryRuleAllowExternalImports,
		FilePatterns:  combineBoundaryPatterns(patterns.provider, patterns.providerModules),
		Allow:         allow,
		SkipTestFiles: true,
	}
}

func defaultProviderStoreRule(patterns backendBoundaryPatterns) BackendBoundaryRule {
	return BackendBoundaryRule{ID: "provider_no_store", Owner: "provider_runtime", Reason: "provider adapters consume session contracts and must not import product store packages directly", Kind: BoundaryRuleDenyImports, FilePatterns: patterns.provider, Deny: boundaryPolicies("provider_runtime", patterns.provider, []string{"internal/store"}, "provider adapters must not own product store dependencies"), SkipTestFiles: true}
}

func defaultProviderDatabaseRule(patterns backendBoundaryPatterns) BackendBoundaryRule {
	return BackendBoundaryRule{ID: "provider_no_platform_db", Owner: "provider_runtime", Reason: "provider production code must not own SQLite handles or DB lifecycle", Kind: BoundaryRuleDenyImports, FilePatterns: patterns.provider, Deny: boundaryPolicies("provider_runtime", patterns.provider, []string{"internal/platform/db"}, "provider adapters must not own database lifecycle"), SkipTestFiles: true}
}

func defaultProviderUnifiedConcreteRule(patterns backendBoundaryPatterns) BackendBoundaryRule {
	return BackendBoundaryRule{
		ID:           "provider_unified_no_concrete_imports",
		Owner:        "provider_runtime",
		Reason:       "the unified provider dispatch layer must depend on shared contracts instead of concrete provider implementations",
		Kind:         BoundaryRuleAllowInternalImports,
		FilePatterns: patterns.providerUnified,
		Allow: boundaryPolicies(
			"provider_runtime",
			patterns.providerUnified,
			[]string{"internal/contract", "internal/dto", "internal/platform", "internal/provider/shared", "internal/util"},
			"unified provider code may depend only on shared contracts, platform utilities, and provider-shared adapters",
		),
		SkipTestFiles: true,
	}
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
	}, "store packages may consume only registered persistence contracts and shared path adapters")
	allow = append(allow, boundaryPolicies(owner, patterns.storeMod, []string{
		"go.uber.org/fx",
	}, "store package module files may declare their Fx and pool constructors")...)
	allow = append(allow, boundaryPolicies(owner, patterns.storeRoot, []string{
		"internal/store",
		"go.uber.org/fx",
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

func defaultPublicPkgRule(patterns backendBoundaryPatterns) BackendBoundaryRule {
	return BackendBoundaryRule{ID: "pkg_no_internal_imports", Owner: "public_pkg_boundary", Reason: "public pkg libraries must not depend on repository internals or command entrypoints", Kind: BoundaryRuleDenyImports, FilePatterns: patterns.pkg, Deny: boundaryPolicies("public_pkg_boundary", patterns.pkg, []string{"internal", "cmd"}, "public pkg libraries must remain reusable outside the repository"), SkipTestFiles: true}
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
		"internal/platform/toolbridge/mcpwire",
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
		"internal/platform/db/sqlite",
		"internal/platform/discovery",
		"internal/platform/metrics",
	}, "LSP sidecar runtime or SQLite diagnostics primitive")...)
	policies = append(policies, boundaryPolicies(owner, []string{"cmd/mcp-ida/**/*.go"}, []string{
		"internal/platform/metrics",
	}, "IDA sidecar runtime primitive")...)
	return policies
}

func commandBoundaryPatterns(patterns backendBoundaryPatterns) []string {
	return combineBoundaryPatterns(patterns.agentRuntime, patterns.agentTerminal, patterns.codexWorktree, patterns.trustedLauncher, patterns.gateCLI, patterns.releaseManifest, patterns.guard, patterns.updater, patterns.schemaHelper, patterns.acpNode)
}

func internalSupportBoundaryPatterns(patterns backendBoundaryPatterns) []string {
	return combineBoundaryPatterns(patterns.devtools, []string{
		"internal/devtools/archtestmap/**/*.go",
	}, patterns.dto, patterns.testutil, patterns.util)
}

// commandNarrowAllowPolicies 为每个 standalone command 绑定精确内部依赖白名单。
func commandNarrowAllowPolicies(patterns backendBoundaryPatterns) []BoundaryImportPolicy {
	owner := BoundaryOwnerID("command_boundary")
	policies := boundaryPolicies(owner, patterns.agentRuntime, []string{
		"internal/app",
		"internal/platform/rlimit",
		"internal/platform/runtimeenv",
	}, "agent runtime host or process primitive")
	policies = append(policies, boundaryPolicies(owner, patterns.agentTerminal, []string{
		"internal/app",
		"internal/platform/appupdaterecovery",
		"internal/platform/pidregistry",
		"internal/platform/rlimit",
		"internal/platform/runner",
		"internal/platform/runtimeenv",
	}, "agent terminal host or process primitive")...)
	policies = append(policies, boundaryPolicies(owner, patterns.codexWorktree, []string{
		"internal/platform/config",
		"internal/util/pathutil",
	}, "Codex worktree setup runtime primitive")...)
	policies = append(policies, boundaryPolicies(owner, patterns.trustedLauncher, []string{
		"internal/devtools/trustedlauncher",
	}, "trusted gate launcher build primitive")...)
	policies = append(policies, boundaryPolicies(owner, patterns.gateCLI, []string{
		"internal/devtools/alicloud/eci",
		"internal/devtools/alicloud/oss",
		"internal/devtools/capcontract",
		"internal/devtools/cicontract",
		"internal/devtools/codemapindex",
		"internal/devtools/coordinatoradmission",
		"internal/devtools/frontendcodesizetrusted",
		"internal/devtools/gate",
		"internal/devtools/gateprivate",
		"internal/devtools/gatehook",
		"internal/devtools/projectmaptrusted",
		"internal/devtools/remoteci",
		"internal/devtools/shardresource",
		"internal/devtools/sourceexport",
		"internal/devtools/trustedlauncher",
	}, "standalone gate coordinator, worker, hook, persistence, and remote CI runtime")...)
	policies = append(policies, boundaryPolicies(owner, patterns.releaseManifest, []string{
		"internal/module/appupdate",
	}, "release manifest update contract")...)
	policies = append(policies, boundaryPolicies(owner, patterns.guard, []string{
		"internal/platform/appupdaterecovery",
		"internal/platform/pidregistry",
		"internal/platform/runtimeenv",
	}, "detached Guard transaction and frozen environment seam")...)
	policies = append(policies, boundaryPolicies(owner, patterns.updater, []string{
		"internal/platform/appupdatefailure",
		"internal/platform/appupdaterecovery",
		"internal/platform/pidregistry",
		"internal/platform/runtimeenv",
		"internal/util/ctxutil",
		"internal/util/safego",
	}, "updater release transaction, context, or supervised reaper primitive")...)
	policies = append(policies, boundaryPolicies(owner, patterns.schemaHelper, []string{
		"internal/platform/toolbridge/schema",
	}, "one-shot schema compiler execution boundary")...)
	policies = append(policies, boundaryPolicies(owner, patterns.acpNode, []string{
		"internal/devtools/acpnode",
	}, "isolated ACP development harness")...)
	return policies
}

func internalSupportNarrowAllowPolicies(patterns backendBoundaryPatterns) []BoundaryImportPolicy {
	owner := BoundaryOwnerID("internal_support_boundary")
	policies := boundaryPolicies(owner, patterns.devtools, []string{
		"internal/devtools",
		"internal/platform/config",
		"internal/platform/db",
		"internal/util/ctxutil",
		"internal/util/safego",
	}, "developer tool implementation or SQLite smoke seam")
	policies = append(policies, boundaryPolicies(owner, []string{"internal/devtools/archtestmap/**/*.go"}, []string{
		"internal/archtest",
	}, "archtest map generator renders the canonical boundary registry")...)
	policies = append(policies, boundaryPolicies(owner, patterns.dto, []string{
		"internal/dto",
	}, "DTO descendant")...)
	policies = append(policies, boundaryPolicies(owner, patterns.testutil, []string{
		"internal/contract",
		"internal/testutil",
	}, "test contract or testutil descendant")...)
	policies = append(policies, boundaryPolicies(owner, patterns.util, []string{
		"internal/dto/provider",
		"internal/platform/config",
		"internal/platform/runtimesafe",
		"internal/platform/sessionpaths",
		"internal/platform/shared",
		"internal/util",
	}, "utility descendant or registered DTO/platform seam")...)
	return policies
}
