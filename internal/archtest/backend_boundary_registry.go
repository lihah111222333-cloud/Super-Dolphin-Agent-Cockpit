package archtest

// BoundaryOwnerID 标识后端边界规则拥有者。
type BoundaryOwnerID string

// BoundaryRuleID 标识一条稳定的后端边界规则。
type BoundaryRuleID string

// BoundaryRuleKind 标识规则求值方式。
type BoundaryRuleKind string

const (
	// BoundaryRuleDenyImports 拒绝匹配文件导入指定前缀。
	BoundaryRuleDenyImports BoundaryRuleKind = "deny_imports"
	// BoundaryRuleAllowInternalImports 限制匹配文件导入内部或 cmd 包的白名单。
	BoundaryRuleAllowInternalImports BoundaryRuleKind = "allow_internal_imports"
	// BoundaryRuleModuleSiblings 拒绝 module 层跨 owner 的具体实现导入。
	BoundaryRuleModuleSiblings BoundaryRuleKind = "module_siblings"
	// BoundaryRuleScopedImport 只允许指定文件范围导入被拒绝前缀。
	BoundaryRuleScopedImport BoundaryRuleKind = "scoped_import"
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
	contract []string
	module   []string
	provider []string
	platform []string
	sidecar  []string
	sqlc     []string
}

func defaultBackendBoundaryPatterns() backendBoundaryPatterns {
	return backendBoundaryPatterns{
		contract: []string{"internal/contract/**/*.go"},
		module:   []string{"internal/module/**/*.go"},
		provider: []string{
			"internal/provider/claudecli/**/*.go",
			"internal/provider/codexapp/**/*.go",
			"internal/provider/unified/**/*.go",
		},
		platform: []string{"internal/platform/**/*.go"},
		sidecar:  []string{"cmd/mcp-orch/**/*.go", "cmd/mcp-lsp/**/*.go", "cmd/mcp-ida/**/*.go"},
		sqlc:     []string{"internal/**/*.go", "cmd/**/*.go"},
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
		Exceptions: []BoundaryException{
			{ID: "skill_module_database_sql", Owner: "module_boundary", FilePattern: "internal/module/skill/module.go", ImportPrefix: "database/sql", Class: BoundaryExceptionPermanent, Reason: "skill module startup still injects the legacy tool-store database handle"},
			{ID: "skill_service_database_sql", Owner: "module_boundary", FilePattern: "internal/module/skill/service.go", ImportPrefix: "database/sql", Class: BoundaryExceptionPermanent, Reason: "skill service still owns the tool-store construction seam"},
			{ID: "skill_toolstore_database_sql", Owner: "module_boundary", FilePattern: "internal/module/skill/toolstore/store.go", ImportPrefix: "database/sql", Class: BoundaryExceptionPermanent, Reason: "toolstore remains the existing persistence subpackage for skill tools"},
		},
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
			{Owner: "sqlc_boundary", FilePattern: "internal/store/**/*.go", Reason: "store packages are the canonical anti-corruption wrappers around sqlc"},
			{Owner: "sqlc_boundary", FilePattern: "internal/platform/db/**/*.go", Reason: "platform DB schema verification may inspect sqlc-backed persistence boundaries"},
		},
		SkipTestFiles: true,
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
