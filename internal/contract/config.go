package contract

// SkillConfig holds skill-specific configuration.
type SkillConfig struct {
	ProgressiveDisclosure bool
	TokenBudget           int
}

// AgentConfig holds agent-specific configuration.
type AgentConfig struct {
	PersistentSubagentDefault bool
}

// NotifyConfig carries the P21 P2 external-webhook egress settings.
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
)

// LSPConfig holds language-service startup and indexing configuration.
type LSPConfig struct {
	NoiseDirNames                    []string
	GoDirectoryFilters               []string
	ProjectAdapters                  map[string]LSPProjectAdapterConfig
	DocumentFallbackLanguageIDs      []string
	DisableInitialWorkspaceBootstrap bool
}

// LSPProjectAdapterConfig holds per-language project discovery configuration.
type LSPProjectAdapterConfig struct {
	RootMarkers           []string
	IgnoredDirNames       []string
	FirstSourceExtensions []string
}

// Config is the root configuration struct shared across the application.
// The canonical constructor (New) lives in internal/platform/config; this
// file only hosts the type definitions so that lower layers (module, store)
// can depend on them without importing a platform package.
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

// RuntimeConfigField 描述运行时配置字段的规范名和兼容别名。
type RuntimeConfigField struct {
	Canonical string
	Aliases   []string
}

// Keys 返回规范名加别名，供配置读取函数按兼容顺序查找。
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
