package contract

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

const (
	LSPServiceJSTS   = "jsts"
	LSPServicePython = "python"
	LSPServiceRust   = "rust"
	LSPServiceJava   = "java"
	LSPServiceCSS    = "css"
	LSPServiceShell  = "shell"
	LSPServiceSQL    = "sql"
)

// LSPConfig 保存 language-service 启动、索引和项目适配配置。
type LSPConfig struct {
	NoiseDirNames                    []string
	GoDirectoryFilters               []string
	ProjectAdapters                  map[string]LSPProjectAdapterConfig
	DocumentFallbackLanguageIDs      []string
	DisableInitialWorkspaceBootstrap bool
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

var (
	RuntimeConfigProvider                     = RuntimeConfigField{Canonical: "provider"}
	RuntimeConfigPromptKey                    = RuntimeConfigField{Canonical: "promptKey", Aliases: []string{"prompt_key"}}
	RuntimeConfigCWD                          = RuntimeConfigField{Canonical: "cwd"}
	RuntimeConfigModel                        = RuntimeConfigField{Canonical: "model"}
	RuntimeConfigGitRoot                      = RuntimeConfigField{Canonical: "gitRoot", Aliases: []string{"git_root"}}
	RuntimeConfigIsWorktree                   = RuntimeConfigField{Canonical: "isWorktree", Aliases: []string{"is_worktree"}}
	RuntimeConfigLanguage                     = RuntimeConfigField{Canonical: "language"}
	RuntimeConfigEnabledTools                 = RuntimeConfigField{Canonical: "enabledTools", Aliases: []string{"enabled_tools", "tools"}}
	RuntimeConfigAdditionalWorkingDirectories = RuntimeConfigField{Canonical: "additionalWorkingDirectories", Aliases: []string{"additional_working_directories"}}
	RuntimeConfigSessionFlags                 = RuntimeConfigField{Canonical: "sessionFlags", Aliases: []string{"session_flags"}}
	RuntimeConfigSummary                      = RuntimeConfigField{Canonical: "summary"}
	RuntimeConfigOutputStyleConfig            = RuntimeConfigField{Canonical: "outputStyleConfig", Aliases: []string{"output_style_config"}}
	RuntimeConfigScratchpadDir                = RuntimeConfigField{Canonical: "scratchpadDir", Aliases: []string{"scratchpad_dir"}}
	RuntimeConfigFRCConfig                    = RuntimeConfigField{Canonical: "frcConfig", Aliases: []string{"frc_config"}}
	RuntimeConfigProviderNativeSkills         = RuntimeConfigField{Canonical: "providerNativeSkills", Aliases: []string{"provider_native_skills"}}
	RuntimeConfigDisableProviderNativeSkills  = RuntimeConfigField{Canonical: "disableProviderNativeSkills", Aliases: []string{"disable_provider_native_skills"}}
	RuntimeConfigMCPServers                   = RuntimeConfigField{Canonical: "mcpServers", Aliases: []string{"mcp_servers"}}
	RuntimeConfigMCPTools                     = RuntimeConfigField{Canonical: "mcpTools", Aliases: []string{"mcp_tools"}}
	RuntimeConfigMCPInstructions              = RuntimeConfigField{Canonical: "mcpInstructions", Aliases: []string{"mcp_instructions"}}
	RuntimeConfigMCPInstructionsDeltaEnabled  = RuntimeConfigField{Canonical: "mcpInstructionsDeltaEnabled", Aliases: []string{"mcp_instructions_delta_enabled"}}
	RuntimeConfigEnv                          = RuntimeConfigField{Canonical: "env"}
	RuntimeConfigAutoApprove                  = RuntimeConfigField{Canonical: "autoApprove", Aliases: []string{"auto_approve"}}
	RuntimeConfigBinaryDir                    = RuntimeConfigField{Canonical: "binary_dir", Aliases: []string{"binaryDir"}}
	RuntimeConfigCodexDisabledNativeTools     = RuntimeConfigField{Canonical: "codexDisabledNativeTools"}
)
