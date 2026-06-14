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

type MemoryFeatureFlags struct {
	Kairos            bool
	TeamMemory        bool
	SearchPastContext bool
}

type KairosConfig struct {
	Enabled bool
}

type NestedMemoryConfig struct {
	Enabled bool
}

type TrustedPathSettingSource string

const (
	TrustedPathSettingSourceNone   TrustedPathSettingSource = ""
	TrustedPathSettingSourcePolicy TrustedPathSettingSource = "policy"
	TrustedPathSettingSourceFlag   TrustedPathSettingSource = "flag"
	TrustedPathSettingSourceLocal  TrustedPathSettingSource = "local"
	TrustedPathSettingSourceUser   TrustedPathSettingSource = "user"
)

type TrustedAutoMemPathOverride struct {
	Path   string
	Source TrustedPathSettingSource
}

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
	// Harness records the underlying CLI harness identified at startup. It
	// is frozen here so a mid-run os.Setenv on `MULTI_AGENT_HARNESS_CLI`
	// cannot flip overlay suppression silently.
	//
	// Production code MUST construct Config through NewConfig so this field
	// is populated. The empty-string fallback (resolve from live env at
	// gate time) exists ONLY to keep `&Config{}` literals in tests
	// painless; it is NOT a supported production default. Reviewers should
	// treat any new production callsite that bypasses NewConfig and reads
	// the empty-Harness fallback as a bug. See gate.go::resolveHarnessFromConfig.
	Harness MemoryHarness
}

type MemoryPathClass string

const (
	MemoryPathClassOther MemoryPathClass = "other"
	MemoryPathClassAuto  MemoryPathClass = "auto"
)

// NewConfig 创建配置。
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

// IsMemoryEnabled 判断记忆enabled是否可用。
func (c *Config) IsMemoryEnabled() bool {
	return memoryProductEnabled(c) && ResolveMemoryGate(contract.BuildCtx{}, c).AutoEnabled
}

// HasAutoMemPathOverride 判断automem路径override是否可用。
func (c *Config) HasAutoMemPathOverride() bool {
	return configuredAutoMemPathOverride(c) != ""
}

// ResolvedAutoMemPathOverride 处理已解析automem路径override。
func (c *Config) ResolvedAutoMemPathOverride() string {
	return configuredAutoMemPathOverride(c)
}

// TrustedAutoMemPathSource 处理trustedautomem路径source。
func (c *Config) TrustedAutoMemPathSource() TrustedPathSettingSource {
	return resolveTrustedAutoMemPathSource(c)
}

// IsAutoMemPath 判断automem路径是否可用。
func (c *Config) IsAutoMemPath(path string) bool {
	return ClassifyMemoryPath(c, path) == MemoryPathClassAuto
}

// ClassifyMemoryPath 分类记忆路径。
func (c *Config) ClassifyMemoryPath(path string) MemoryPathClass {
	return ClassifyMemoryPath(c, path)
}

// ResolveMemoryGate 解析记忆gate。
func ResolveMemoryGate(buildCtx contract.BuildCtx, cfg *Config) MemoryGateSnapshot {
	return resolveMemoryGate(buildCtx, cfg)
}

// ShouldStartRelevantMemoryPrefetch 判断起点relevant记忆prefetch是否可用。
func ShouldStartRelevantMemoryPrefetch(snapshot MemoryGateSnapshot, turnInput contract.TurnInput, surfacedState RelevantPrefetchSurfacedState) bool {
	return shouldStartRelevantMemoryPrefetch(snapshot, turnInput, surfacedState)
}

// ClassifyMemoryPath 分类记忆路径。
func ClassifyMemoryPath(cfg *Config, path string) MemoryPathClass {
	switch {
	case isAutoMemPath(cfg, path):
		return MemoryPathClassAuto
	default:
		return MemoryPathClassOther
	}
}

// IsAutoMemoryPath 判断auto记忆路径是否可用。
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

// HandleDateChange 处理datechange。
func HandleDateChange() {
	// Date-change hooks are currently handled by callers that rebuild Config.
	_ = struct{}{}
}

// LoadNestedMemoryPaths 加载nested记忆路径。
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
