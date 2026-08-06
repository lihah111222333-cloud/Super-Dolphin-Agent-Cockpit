package contract

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// SkillConfig 保存技能发现和加载时使用的运行配置。
type SkillConfig struct {
	ProgressiveDisclosure bool
	TokenBudget           int
}

// AgentConfig 保存 agent runtime 的默认行为配置。
type AgentConfig struct {
	PersistentSubagentDefault bool
}

// NotifyConfig 是外部 webhook 通知的出站策略配置。
// 私网放行、超时和队列容量都在这里进入 runtime，调用方不能用零值静默绕过策略。
type NotifyConfig struct {
	ChannelsJSON     string
	AllowPrivateCIDR bool
	TimeoutSeconds   int
	QueueCapacity    int
	DrainSeconds     int
}

// DependencyProfile declares which runtime dependency set a process is allowed to use.
type DependencyProfile string

const (
	// DependencyProfileDesktopHost allows desktop-only dependencies that are absent from sidecars.
	DependencyProfileDesktopHost DependencyProfile = "desktop_host"
	// DependencyProfileProduction requires production dependencies to be explicitly wired.
	DependencyProfileProduction DependencyProfile = "production"
	// DependencyProfileTest is reserved for Go test binaries with explicit test bootstrap.
	DependencyProfileTest DependencyProfile = "test"
)

// DependencyBootstrapMode describes the process bootstrap context used to resolve a profile.
type DependencyBootstrapMode string

const (
	// DependencyBootstrapDesktopHost is the trusted desktop host bootstrap.
	DependencyBootstrapDesktopHost DependencyBootstrapMode = "desktop_host"
	// DependencyBootstrapProduction is the default sidecar and packaged runtime bootstrap.
	DependencyBootstrapProduction DependencyBootstrapMode = "production"
	// DependencyBootstrapTest is allowed only inside Go test binaries.
	DependencyBootstrapTest DependencyBootstrapMode = "test"
)

// DependencyConfig is the typed dependency-mode envelope shared through Fx config.
type DependencyConfig struct {
	Profile DependencyProfile
}

var (
	// ErrUnsupportedDependencyMode marks a dependency that is intentionally absent in the current profile.
	ErrUnsupportedDependencyMode = errors.New("unsupported dependency mode")
	// ErrDependencyDeferred marks a dependency that is deferred by runtime mode instead of silently noop'd.
	ErrDependencyDeferred = errors.New("dependency deferred by runtime mode")
)

// DependencyModeError carries the dependency name and profile that produced a typed mode error.
type DependencyModeError struct {
	Err     error
	Name    string
	Profile DependencyProfile
}

// Error 输出包含依赖名和 profile 的可诊断文本，供启动失败和测试断言定位。
func (e DependencyModeError) Error() string {
	return fmt.Sprintf("%v: %s in %s profile", e.Err, e.Name, e.Profile)
}

// Unwrap 让调用方可以继续用 errors.Is/As 识别底层哨兵错误。
func (e DependencyModeError) Unwrap() error {
	return e.Err
}

// NewDependencyModeError 把依赖名称和 profile 写进错误，避免后续把 unsupported/deferred 当成普通成功。
func NewDependencyModeError(err error, name string, profile DependencyProfile) error {
	return DependencyModeError{Err: err, Name: name, Profile: profile}
}

// IsDependencyModeError 同时校验哨兵、依赖名和 profile，防止跨依赖误吞 typed mode error。
func IsDependencyModeError(err error, name string, profile DependencyProfile, target error) bool {
	var modeErr DependencyModeError
	if !errors.As(err, &modeErr) {
		return false
	}
	return errors.Is(modeErr.Err, target) && modeErr.Name == name && modeErr.Profile == profile
}

// DependencyAbsenceReason 命名非生产 profile 允许依赖缺席的原因。
type DependencyAbsenceReason string

const (
	// DependencyAbsenceDesktopExternal 表示该依赖由桌面宿主边界承接。
	DependencyAbsenceDesktopExternal DependencyAbsenceReason = "desktop_external"
	// DependencyAbsenceTestHarness 表示该依赖由测试装配显式提供或省略。
	DependencyAbsenceTestHarness DependencyAbsenceReason = "test_harness"
)

// DependencyAbsencePolicy 记录单个 profile 下允许缺席的依赖策略。
type DependencyAbsencePolicy struct {
	Name    string
	Profile DependencyProfile
	Reason  DependencyAbsenceReason
	Owner   string
	Error   error
}

func registeredDependencyAbsencePolicies() []DependencyAbsencePolicy {
	return []DependencyAbsencePolicy{
		{
			Name:    "runtime_reporter.orchestration_service",
			Profile: DependencyProfileDesktopHost,
			Reason:  DependencyAbsenceDesktopExternal,
			Owner:   "Lane D",
			Error:   ErrDependencyDeferred,
		},
		{
			Name:    "runtime_reporter.orchestration_service",
			Profile: DependencyProfileTest,
			Reason:  DependencyAbsenceDesktopExternal,
			Owner:   "Lane D",
			Error:   ErrDependencyDeferred,
		},
		{
			Name:    "toolbridge.agent_thread_lookup",
			Profile: DependencyProfileDesktopHost,
			Reason:  DependencyAbsenceDesktopExternal,
			Owner:   "Lane D",
			Error:   ErrUnsupportedDependencyMode,
		},
		{
			Name:    "toolbridge.thread_config_override_store",
			Profile: DependencyProfileDesktopHost,
			Reason:  DependencyAbsenceDesktopExternal,
			Owner:   "Lane D",
			Error:   ErrUnsupportedDependencyMode,
		},
		{
			Name:    "toolbridge.lifecycle_backfiller",
			Profile: DependencyProfileTest,
			Reason:  DependencyAbsenceTestHarness,
			Owner:   "Lane D",
			Error:   ErrUnsupportedDependencyMode,
		},
		{
			Name:    "toolbridge.skill_tools",
			Profile: DependencyProfileTest,
			Reason:  DependencyAbsenceTestHarness,
			Owner:   "Lane D",
			Error:   ErrUnsupportedDependencyMode,
		},
		{
			Name:    "thread.bind_session_generation",
			Profile: DependencyProfileDesktopHost,
			Reason:  DependencyAbsenceDesktopExternal,
			Owner:   "Lane D",
			Error:   ErrUnsupportedDependencyMode,
		},
		{
			Name:    "thread.bind_session_generation",
			Profile: DependencyProfileTest,
			Reason:  DependencyAbsenceDesktopExternal,
			Owner:   "Lane D",
			Error:   ErrUnsupportedDependencyMode,
		},
		{
			Name:    "dynamic_tools",
			Profile: DependencyProfileTest,
			Reason:  DependencyAbsenceTestHarness,
			Owner:   "provider contract",
			Error:   ErrUnsupportedDependencyMode,
		},
		{
			Name:    "approval_or_tool_diff",
			Profile: DependencyProfileTest,
			Reason:  DependencyAbsenceTestHarness,
			Owner:   "provider contract",
			Error:   ErrUnsupportedDependencyMode,
		},
	}
}

// RegisteredDependencyAbsencePolicies 返回当前注册的依赖缺席策略，调用方无法改写共享包级状态。
func RegisteredDependencyAbsencePolicies() []DependencyAbsencePolicy {
	return registeredDependencyAbsencePolicies()
}

// AllowsMissingDependency 判断指定 profile 是否允许该依赖缺席；未知依赖和生产 profile 都返回 false。
func AllowsMissingDependency(name string, profile DependencyProfile) bool {
	_, ok := lookupDependencyAbsencePolicy(name, profile)
	return ok
}

// MissingDependencyModeError 为缺席依赖返回 fail-fast 错误，只有已注册非生产策略才返回 typed mode error。
func MissingDependencyModeError(name string, profile DependencyProfile) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("dependency name is required")
	}
	if strings.TrimSpace(string(profile)) == "" {
		return fmt.Errorf("dependency profile is required for %q", name)
	}
	if !isKnownDependencyProfile(profile) {
		return fmt.Errorf("dependency profile %q is not supported for %q", profile, name)
	}
	if policy, ok := lookupDependencyAbsencePolicy(name, profile); ok {
		return NewDependencyModeError(policy.Error, policy.Name, policy.Profile)
	}
	if dependencyAbsencePolicyNameExists(name) {
		return fmt.Errorf("dependency %q is required in %s profile", name, profile)
	}
	return fmt.Errorf("unknown dependency absence policy %q in %s profile", name, profile)
}

// RequireDependency 校验构造期依赖是否存在；已登记的非生产缺席只允许构造继续，不转换成运行期 typed mode error。
func RequireDependency(name string, profile DependencyProfile, value any) error {
	name = strings.TrimSpace(name)
	if value != nil {
		return nil
	}
	if name == "" {
		return fmt.Errorf("dependency name is required")
	}
	if strings.TrimSpace(string(profile)) == "" {
		return fmt.Errorf("dependency profile is required for %q", name)
	}
	if !isKnownDependencyProfile(profile) {
		return fmt.Errorf("dependency profile %q is not supported for %q", profile, name)
	}
	if _, ok := lookupDependencyAbsencePolicy(name, profile); ok {
		return nil
	}
	if dependencyAbsencePolicyNameExists(name) {
		return fmt.Errorf("dependency %q is required in %s profile", name, profile)
	}
	return fmt.Errorf("unknown dependency absence policy %q in %s profile", name, profile)
}

// lookupDependencyAbsencePolicy 按 name/profile 精确查找注册策略，空值或未注册时返回 false。
func lookupDependencyAbsencePolicy(name string, profile DependencyProfile) (DependencyAbsencePolicy, bool) {
	name = strings.TrimSpace(name)
	if name == "" || strings.TrimSpace(string(profile)) == "" {
		return DependencyAbsencePolicy{}, false
	}
	for _, policy := range registeredDependencyAbsencePolicies() {
		if policy.Name == name && policy.Profile == profile {
			return policy, true
		}
	}
	return DependencyAbsencePolicy{}, false
}

func dependencyAbsencePolicyNameExists(name string) bool {
	name = strings.TrimSpace(name)
	for _, policy := range registeredDependencyAbsencePolicies() {
		if policy.Name == name {
			return true
		}
	}
	return false
}

func isKnownDependencyProfile(profile DependencyProfile) bool {
	switch profile {
	case DependencyProfileDesktopHost, DependencyProfileProduction, DependencyProfileTest:
		return true
	default:
		return false
	}
}

const (
	LSPServiceJSTS      = "jsts"
	LSPServicePython    = "python"
	LSPServiceRust      = "rust"
	LSPServiceJava      = "java"
	LSPServiceCSS       = "css"
	LSPServiceHTML      = "html"
	LSPServiceJSON      = "json"
	LSPServiceYAML      = "yaml"
	LSPServiceMarkdown  = "markdown"
	LSPServiceVue       = "vue"
	LSPServiceSvelte    = "svelte"
	LSPServiceClangd    = "clangd"
	LSPServiceSwift     = "swift"
	LSPServiceCSharp    = "csharp"
	LSPServicePHP       = "php"
	LSPServiceRuby      = "ruby"
	LSPServiceKotlin    = "kotlin"
	LSPServiceDart      = "dart"
	LSPServiceLua       = "lua"
	LSPServiceDocker    = "docker"
	LSPServiceTerraform = "terraform"
	LSPServiceGraphQL   = "graphql"
	LSPServicePrisma    = "prisma"
	LSPServiceShell     = "shell"
	LSPServiceSQL       = "sql"
)

// LSPConfig 保存 language-service 启动、索引和项目适配配置。
type LSPConfig struct {
	NoiseDirNames                    []string
	GoDirectoryFilters               []string
	ProjectAdapters                  map[string]LSPProjectAdapterConfig
	DocumentFallbackLanguageIDs      []string
	DisableInitialWorkspaceBootstrap bool
	IdleTimeout                      time.Duration
}

// LSPProjectAdapterConfig 保存单个语言服务的项目发现规则。
type LSPProjectAdapterConfig struct {
	RootMarkers           []string
	IgnoredDirNames       []string
	FirstSourceExtensions []string
}

// Config 是应用共享的根配置 wire 结构。
// 构造与默认值由 internal/platform/config 负责；contract 只放类型，供 module/store 低层包引用。
type Config struct {
	SQLitePath  string
	RPCAddr     string
	LogLevel    string
	ProjectRoot string
	Skill       SkillConfig
	Agent       AgentConfig
	Notify      NotifyConfig
	LSP         LSPConfig
	Dependency  DependencyConfig
}

// RuntimeConfigField 描述 runtime 配置字段的规范名和可接受别名。
// 配置读取方按 Keys 顺序查找，保证新旧 wire 名称兼容但仍收敛到规范名。
type RuntimeConfigField struct {
	Canonical string
	Aliases   []string
}

// Keys 返回规范名加别名的查找顺序。
func (f RuntimeConfigField) Keys() []string {
	keys := make([]string, 0, 1+len(f.Aliases))
	keys = append(keys, f.Canonical)
	keys = append(keys, f.Aliases...)
	return keys
}

// RuntimeConfigProvider 返回 provider 字段描述符的新值，避免暴露可变包级状态。
func RuntimeConfigProvider() RuntimeConfigField { return RuntimeConfigField{Canonical: "provider"} }

// RuntimeConfigPromptKey 返回 prompt key 字段描述符的新值。
func RuntimeConfigPromptKey() RuntimeConfigField {
	return RuntimeConfigField{Canonical: "promptKey", Aliases: []string{"prompt_key"}}
}

// RuntimeConfigCWD 返回 cwd 字段描述符的新值。
func RuntimeConfigCWD() RuntimeConfigField { return RuntimeConfigField{Canonical: "cwd"} }

// RuntimeConfigModel 返回 model 字段描述符的新值。
func RuntimeConfigModel() RuntimeConfigField { return RuntimeConfigField{Canonical: "model"} }

// RuntimeConfigGitRoot 返回 git root 字段描述符的新值。
func RuntimeConfigGitRoot() RuntimeConfigField {
	return RuntimeConfigField{Canonical: "gitRoot", Aliases: []string{"git_root"}}
}

// RuntimeConfigIsWorktree 返回 worktree 字段描述符的新值。
func RuntimeConfigIsWorktree() RuntimeConfigField {
	return RuntimeConfigField{Canonical: "isWorktree", Aliases: []string{"is_worktree"}}
}

// RuntimeConfigLanguage 返回 language 字段描述符的新值。
func RuntimeConfigLanguage() RuntimeConfigField { return RuntimeConfigField{Canonical: "language"} }

// RuntimeConfigEnabledTools 返回 enabled tools 字段描述符的新值。
func RuntimeConfigEnabledTools() RuntimeConfigField {
	return RuntimeConfigField{Canonical: "enabledTools", Aliases: []string{"enabled_tools", "tools"}}
}

// RuntimeConfigAdditionalWorkingDirectories 返回额外工作目录字段描述符的新值。
func RuntimeConfigAdditionalWorkingDirectories() RuntimeConfigField {
	return RuntimeConfigField{Canonical: "additionalWorkingDirectories", Aliases: []string{"additional_working_directories"}}
}

// RuntimeConfigSessionFlags 返回 session flags 字段描述符的新值。
func RuntimeConfigSessionFlags() RuntimeConfigField {
	return RuntimeConfigField{Canonical: "sessionFlags", Aliases: []string{"session_flags"}}
}

// RuntimeConfigSummary 返回 summary 字段描述符的新值。
func RuntimeConfigSummary() RuntimeConfigField { return RuntimeConfigField{Canonical: "summary"} }

// RuntimeConfigOutputStyleConfig 返回输出样式字段描述符的新值。
func RuntimeConfigOutputStyleConfig() RuntimeConfigField {
	return RuntimeConfigField{Canonical: "outputStyleConfig", Aliases: []string{"output_style_config"}}
}

// RuntimeConfigScratchpadDir 返回 scratchpad 目录字段描述符的新值。
func RuntimeConfigScratchpadDir() RuntimeConfigField {
	return RuntimeConfigField{Canonical: "scratchpadDir", Aliases: []string{"scratchpad_dir"}}
}

// RuntimeConfigFRCConfig 返回 FRC 字段描述符的新值。
func RuntimeConfigFRCConfig() RuntimeConfigField {
	return RuntimeConfigField{Canonical: "frcConfig", Aliases: []string{"frc_config"}}
}

// RuntimeConfigProviderNativeSkills 返回 provider 原生技能字段描述符的新值。
func RuntimeConfigProviderNativeSkills() RuntimeConfigField {
	return RuntimeConfigField{Canonical: "providerNativeSkills", Aliases: []string{"provider_native_skills"}}
}

// RuntimeConfigDisableProviderNativeSkills 返回禁用 provider 原生技能字段描述符的新值。
func RuntimeConfigDisableProviderNativeSkills() RuntimeConfigField {
	return RuntimeConfigField{Canonical: "disableProviderNativeSkills", Aliases: []string{"disable_provider_native_skills"}}
}

// RuntimeConfigMCPServers 返回 MCP servers 字段描述符的新值。
func RuntimeConfigMCPServers() RuntimeConfigField {
	return RuntimeConfigField{Canonical: "mcpServers", Aliases: []string{"mcp_servers"}}
}

// RuntimeConfigMCPTools 返回 MCP tools 字段描述符的新值。
func RuntimeConfigMCPTools() RuntimeConfigField {
	return RuntimeConfigField{Canonical: "mcpTools", Aliases: []string{"mcp_tools"}}
}

// RuntimeConfigMCPInstructions 返回 MCP instructions 字段描述符的新值。
func RuntimeConfigMCPInstructions() RuntimeConfigField {
	return RuntimeConfigField{Canonical: "mcpInstructions", Aliases: []string{"mcp_instructions"}}
}

// RuntimeConfigMCPInstructionsDeltaEnabled 返回 MCP 指令增量字段描述符的新值。
func RuntimeConfigMCPInstructionsDeltaEnabled() RuntimeConfigField {
	return RuntimeConfigField{Canonical: "mcpInstructionsDeltaEnabled", Aliases: []string{"mcp_instructions_delta_enabled"}}
}

// RuntimeConfigEnv 返回环境变量字段描述符的新值。
func RuntimeConfigEnv() RuntimeConfigField { return RuntimeConfigField{Canonical: "env"} }

// RuntimeConfigAutoApprove 返回自动批准字段描述符的新值。
func RuntimeConfigAutoApprove() RuntimeConfigField {
	return RuntimeConfigField{Canonical: "autoApprove", Aliases: []string{"auto_approve"}}
}

// RuntimeConfigBinaryDir 返回二进制目录字段描述符的新值。
func RuntimeConfigBinaryDir() RuntimeConfigField {
	return RuntimeConfigField{Canonical: "binary_dir", Aliases: []string{"binaryDir"}}
}

// RuntimeConfigCodexDisabledNativeTools 返回 Codex 原生工具禁用字段描述符的新值。
func RuntimeConfigCodexDisabledNativeTools() RuntimeConfigField {
	return RuntimeConfigField{Canonical: "codexDisabledNativeTools"}
}
