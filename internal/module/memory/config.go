package memory

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	teampkg "github.com/anthropic-ai/super-agent-v3/internal/module/memory/team"
)

const (
	envMemoryRoot                  = "MULTI_AGENT_MEMORY_DIR"
	envClaudeRemoteMemoryDir       = "CLAUDE_CODE_REMOTE_MEMORY_DIR"
	envMemoryPathOverride          = "MULTI_AGENT_MEMORY_PATH_OVERRIDE"
	envClaudeMemoryPathOverride    = "CLAUDE_COWORK_MEMORY_PATH_OVERRIDE"
	envEnableMemorySystem          = "ENABLE_MEMORY_SYSTEM"
	envClaudeDisableAutoMemory     = "CLAUDE_CODE_DISABLE_AUTO_MEMORY"
	envClaudeDisableClaudeMds      = "CLAUDE_CODE_DISABLE_CLAUDE_MDS"
	envClaudeSimple                = "CLAUDE_CODE_SIMPLE"
	envClaudeRemote                = "CLAUDE_CODE_REMOTE"
	envEnableMemoryTools           = "ENABLE_MEMORY_TOOLS"
	envMemorySkipIndex             = "MULTI_AGENT_MEMORY_SKIP_INDEX"
	envMemoryExtractOnStop         = "MULTI_AGENT_MEMORY_EXTRACT_ON_STOP"
	envMemoryExtraGuidelines       = "MULTI_AGENT_MEMORY_EXTRA_GUIDELINES"
	envClaudeMemoryExtraGuidelines = "CLAUDE_COWORK_MEMORY_EXTRA_GUIDELINES"
	envFeatureKairos               = "MULTI_AGENT_MEMORY_FEATURE_KAIROS"
	envFeatureTeamMemory           = "MULTI_AGENT_MEMORY_FEATURE_TEAMMEM"
	envFeatureSearchPastContext    = "MULTI_AGENT_MEMORY_FEATURE_SEARCH_PAST_CONTEXT"
	envHarnessKind                 = "MULTI_AGENT_HARNESS_CLI"
)

var (
	ErrTeamMemoryDisabled      = teampkg.ErrTeamMemoryDisabled
	ErrInvalidTeamMemWritePath = teampkg.ErrInvalidTeamMemWritePath
	ErrInvalidTeamMemKey       = teampkg.ErrInvalidTeamMemKey
)

// MemoryFeatureFlags 汇总 memory 模块运行时可选能力开关。
// 这些值在 NewConfig 中从环境变量冻结，避免运行中 env 漂移改变同一轮行为。
type MemoryFeatureFlags struct {
	Kairos            bool
	TeamMemory        bool
	SearchPastContext bool
}

// KairosConfig 保存 Kairos 记忆模式的显式配置快照。
type KairosConfig struct {
	Enabled bool
}

// NestedMemoryConfig 保存嵌套记忆规则的显式配置快照。
type NestedMemoryConfig struct {
	Enabled bool
}

// TrustedPathSettingSource 标记 auto memory 路径覆盖的来源可信度。
// 调用方用它区分策略注入、命令行、本地配置和用户设置，避免把不可信路径当成系统策略。
type TrustedPathSettingSource string

const (
	TrustedPathSettingSourceNone   TrustedPathSettingSource = ""
	TrustedPathSettingSourcePolicy TrustedPathSettingSource = "policy"
	TrustedPathSettingSourceFlag   TrustedPathSettingSource = "flag"
	TrustedPathSettingSourceLocal  TrustedPathSettingSource = "local"
	TrustedPathSettingSourceUser   TrustedPathSettingSource = "user"
)

// TrustedAutoMemPathOverride 是带来源标记的 auto memory 路径覆盖。
type TrustedAutoMemPathOverride struct {
	Path   string
	Source TrustedPathSettingSource
}

// Config 是 memory 模块在一次运行中的配置快照。
// 路径、功能开关和 harness 都应通过 NewConfig 构造，避免运行中读取实时环境变量造成 gate 抖动。
type Config struct {
	Enabled                    bool
	EnableTools                bool
	RootDir                    string
	ProjectRoot                string
	EnvAutoMemPathOverride     string
	TrustedAutoMemPathOverride TrustedAutoMemPathOverride
	AutoMemPathOverride        string
	SkipIndex                  bool
	ExtractOnStop              bool
	ExtraGuidelines            []string
	Features                   MemoryFeatureFlags
	Kairos                     KairosConfig
	NestedMemory               NestedMemoryConfig
	// Harness 记录启动时识别出的底层 CLI harness。
	// 该值冻结在 Config 中，避免运行中 os.Setenv 改变 overlay suppression 判断。
	//
	// 生产代码必须通过 NewConfig 构造 Config。空值回退只服务测试里的 `&Config{}` 字面量；
	// 新生产路径如果绕过 NewConfig 并依赖空 Harness，应视为配置装配错误。
	Harness MemoryHarness
}

// MemoryPathClass 是路径分类结果，用于区分 auto memory 路径和其他本地路径。
type MemoryPathClass string

const (
	MemoryPathClassOther MemoryPathClass = "other"
	MemoryPathClassAuto  MemoryPathClass = "auto"
)

// NewConfig 从平台配置和环境变量构建 memory 配置快照。
// auto-dream 手动开关会从 memory root 旁的 intent 文件恢复；读取失败时保留环境变量结果。
func NewConfig(platformCfg *contract.Config) *Config {
	kairosEnabled := parseBoolEnv(envFeatureKairos, false)
	envOverride := firstNonEmptyEnv(envMemoryPathOverride, envClaudeMemoryPathOverride)
	cfg := &Config{
		Enabled:                parseBoolEnv(envEnableMemorySystem, false),
		EnableTools:            parseBoolEnv(envEnableMemoryTools, false),
		RootDir:                defaultRootDir(platformCfg),
		ProjectRoot:            defaultProjectRoot(platformCfg),
		EnvAutoMemPathOverride: envOverride,
		AutoMemPathOverride:    envOverride,
		SkipIndex:              parseBoolEnv(envMemorySkipIndex, false),
		ExtractOnStop:          parseBoolEnv(envMemoryExtractOnStop, false),
		ExtraGuidelines:        parseGuidelinesEnv(envMemoryExtraGuidelines, envClaudeMemoryExtraGuidelines),
		Features: MemoryFeatureFlags{
			Kairos:            kairosEnabled,
			TeamMemory:        parseBoolEnv(envFeatureTeamMemory, false),
			SearchPastContext: parseBoolEnv(envFeatureSearchPastContext, false),
		},
		Kairos:       KairosConfig{Enabled: kairosEnabled},
		NestedMemory: NestedMemoryConfig{Enabled: false},
		Harness:      resolveMemoryHarness(),
	}
	if root := firstNonEmptyEnv(envMemoryRoot, envClaudeRemoteMemoryDir); root != "" {
		cfg.RootDir = root
	}
	if intent, err := ReadAutoDreamIntent(cfg.RootDir); err == nil && intent != nil {
		cfg.ExtractOnStop = *intent
	}
	return cfg
}

// IsMemoryEnabled 返回当前配置下 memory 功能是否真正开放。
// 它同时检查产品级开关和 gate 结果，避免只看 Enabled 就绕过 overlay/remote 等限制。
func (c *Config) IsMemoryEnabled() bool {
	return memoryProductEnabled(c) && ResolveMemoryGate(contract.BuildCtx{}, c).AutoEnabled
}

// HasAutoMemPathOverride 判断是否存在任意 auto memory 路径覆盖。
// 环境变量优先于 Config 中的可信覆盖，调用方不应自行重复读取 env。
func (c *Config) HasAutoMemPathOverride() bool {
	return configuredAutoMemPathOverride(c) != ""
}

// ResolvedAutoMemPathOverride 返回最终生效的 auto memory 路径覆盖。
// 返回空字符串表示没有覆盖，调用方应继续使用默认 root/project 推导。
func (c *Config) ResolvedAutoMemPathOverride() string {
	return configuredAutoMemPathOverride(c)
}

// TrustedAutoMemPathSource 返回当前可信 auto memory 路径覆盖的来源。
// 未配置时为 none；未知来源会被规范化成本地来源，避免出现未定义 trust level。
func (c *Config) TrustedAutoMemPathSource() TrustedPathSettingSource {
	return resolveTrustedAutoMemPathSource(c)
}

// IsAutoMemPath 判断给定路径是否落在当前 auto memory 路径范围内。
func (c *Config) IsAutoMemPath(path string) bool {
	return ClassifyMemoryPath(c, path) == MemoryPathClassAuto
}

// ClassifyMemoryPath 使用当前 Config 对路径做 memory 分类。
// 这是方法形式的便捷入口，分类规则仍集中在包级函数中。
func (c *Config) ClassifyMemoryPath(path string) MemoryPathClass {
	return ClassifyMemoryPath(c, path)
}

// ResolveMemoryGate 计算 build context 与 Config 共同决定的 memory gate 快照。
// 调用方应使用快照结果驱动 UI 和 prompt 注入，不要重复拼接 gate 条件。
func ResolveMemoryGate(buildCtx contract.BuildCtx, cfg *Config) MemoryGateSnapshot {
	return resolveMemoryGate(buildCtx, cfg)
}

// ShouldStartRelevantMemoryPrefetch 判断本轮是否可以启动 relevant memory 预取。
// 该函数同时考虑 gate、turn input 和已 surfaced 状态，避免重复启动后台预取。
func ShouldStartRelevantMemoryPrefetch(snapshot MemoryGateSnapshot, turnInput contract.TurnInput, surfacedState RelevantPrefetchSurfacedState) bool {
	return shouldStartRelevantMemoryPrefetch(snapshot, turnInput, surfacedState)
}

// ClassifyMemoryPath 将路径分类为 auto memory 或其他路径。
// cfg 为空时仅使用安全默认规则，不会把任意路径误判为 auto memory。
func ClassifyMemoryPath(cfg *Config, path string) MemoryPathClass {
	switch {
	case isAutoMemPath(cfg, path):
		return MemoryPathClassAuto
	default:
		return MemoryPathClassOther
	}
}

// IsAutoMemoryPath 是包级 auto memory 路径判断入口。
// 它用于没有 Config 方法接收者的调用点，规则与 Config.IsAutoMemPath 保持一致。
func IsAutoMemoryPath(cfg *Config, path string) bool {
	return ClassifyMemoryPath(cfg, path) == MemoryPathClassAuto
}

func memoryProductEnabled(cfg *Config) bool {
	return cfg == nil || cfg.Enabled
}

func envAutoMemPathOverride(cfg *Config) string {
	if override := firstNonEmptyEnv(envMemoryPathOverride, envClaudeMemoryPathOverride); override != "" {
		return override
	}
	if cfg == nil {
		return ""
	}
	return strings.TrimSpace(cfg.EnvAutoMemPathOverride)
}

func trustedAutoMemPathOverride(cfg *Config) (string, TrustedPathSettingSource) {
	if cfg == nil {
		return "", TrustedPathSettingSourceNone
	}
	if override := strings.TrimSpace(cfg.TrustedAutoMemPathOverride.Path); override != "" {
		return override, normalizeTrustedPathSettingSource(cfg.TrustedAutoMemPathOverride.Source)
	}
	if override := strings.TrimSpace(cfg.AutoMemPathOverride); override != "" {
		return override, TrustedPathSettingSourceLocal
	}
	return "", TrustedPathSettingSourceNone
}

func configuredAutoMemPathOverride(cfg *Config) string {
	if override := envAutoMemPathOverride(cfg); override != "" {
		return override
	}
	override, _ := trustedAutoMemPathOverride(cfg)
	return override
}

func resolveTrustedAutoMemPathSource(cfg *Config) TrustedPathSettingSource {
	_, source := trustedAutoMemPathOverride(cfg)
	return source
}

func normalizeTrustedPathSettingSource(source TrustedPathSettingSource) TrustedPathSettingSource {
	switch source {
	case TrustedPathSettingSourcePolicy, TrustedPathSettingSourceFlag, TrustedPathSettingSourceLocal, TrustedPathSettingSourceUser:
		return source
	default:
		return TrustedPathSettingSourceLocal
	}
}

func hasPersistentMemoryStorage(cfg *Config) bool {
	if firstNonEmptyEnv(
		envMemoryRoot,
		envClaudeRemoteMemoryDir,
	) != "" {
		return true
	}
	return configuredAutoMemPathOverride(cfg) != ""
}

// HandleDateChange 保留给日期切换触发器的空实现。
// 当前调用方通过重建 Config 处理日期变化，因此这里不能偷偷修改全局状态。
func HandleDateChange() {
	// Date-change hooks are currently handled by callers that rebuild Config.
	_ = struct{}{}
}

// LoadNestedMemoryPaths 返回嵌套记忆路径列表。
// 当前配置没有持久化 nested 路径来源，返回空切片表示“不追加额外路径”。
func LoadNestedMemoryPaths() []string {
	return []string{}
}

func defaultRootDir(platformCfg *contract.Config) string {
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".multi-agent", "memory")
	}
	if platformCfg != nil && strings.TrimSpace(platformCfg.ProjectRoot) != "" {
		return filepath.Join(platformCfg.ProjectRoot, ".multi-agent", "memory")
	}
	return ""
}

func defaultProjectRoot(platformCfg *contract.Config) string {
	if platformCfg != nil && strings.TrimSpace(platformCfg.ProjectRoot) != "" {
		return platformCfg.ProjectRoot
	}
	if dir, err := os.Getwd(); err == nil {
		return dir
	}
	return ""
}

func parseBoolEnv(key string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func firstNonEmptyEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func parseGuidelinesEnv(keys ...string) []string {
	raw := firstNonEmptyEnv(keys...)
	if raw == "" {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	guidelines := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			guidelines = append(guidelines, trimmed)
		}
	}
	if len(guidelines) == 0 {
		return nil
	}
	return guidelines
}
