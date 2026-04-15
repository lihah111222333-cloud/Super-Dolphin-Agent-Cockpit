package memory

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
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
)

var (
	ErrTeamMemoryDisabled      = errors.New("team memory is disabled")
	ErrInvalidTeamMemWritePath = errors.New("invalid team memory write path")
	ErrInvalidTeamMemKey       = errors.New("invalid team memory key")
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
}

type MemoryPathClass string

const (
	MemoryPathClassOther MemoryPathClass = "other"
	MemoryPathClassAuto  MemoryPathClass = "auto"
	MemoryPathClassAgent MemoryPathClass = "agent"
)

type TeamMemoryManager struct {
	cfg *Config
}

func NewConfig(platformCfg *platformconfig.Config) *Config {
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
	}
	if root := firstNonEmptyEnv(envMemoryRoot, envClaudeRemoteMemoryDir); root != "" {
		cfg.RootDir = root
	}
	return cfg
}

func NewTeamMemoryManager(cfg *Config) *TeamMemoryManager {
	if cfg == nil {
		cfg = &Config{}
	}
	return &TeamMemoryManager{cfg: cfg}
}

func (c *Config) IsMemoryEnabled() bool {
	return memoryProductEnabled(c) && ResolveMemoryGate(contract.BuildCtx{}, c).AutoEnabled
}

func (c *Config) HasAutoMemPathOverride() bool {
	return configuredAutoMemPathOverride(c) != ""
}

func (c *Config) ResolvedAutoMemPathOverride() string {
	return configuredAutoMemPathOverride(c)
}

func (c *Config) TrustedAutoMemPathSource() TrustedPathSettingSource {
	return resolveTrustedAutoMemPathSource(c)
}

func (c *Config) IsAutoMemPath(path string) bool {
	return ClassifyMemoryPath(c, path) == MemoryPathClassAuto
}

func (c *Config) IsAgentMemoryPath(path string) bool {
	return ClassifyMemoryPath(c, path) == MemoryPathClassAgent
}

func (c *Config) ClassifyMemoryPath(path string) MemoryPathClass {
	return ClassifyMemoryPath(c, path)
}

func ResolveMemoryGate(buildCtx contract.BuildCtx, cfg *Config) MemoryGateSnapshot {
	return resolveMemoryGate(buildCtx, cfg)
}

func ShouldStartRelevantMemoryPrefetch(snapshot MemoryGateSnapshot, turnInput contract.TurnInput, surfacedState RelevantPrefetchSurfacedState) bool {
	return shouldStartRelevantMemoryPrefetch(snapshot, turnInput, surfacedState)
}

func (m *TeamMemoryManager) GetTeamMemPath(buildCtx ...contract.BuildCtx) string {
	return teamMemPath(m, firstTeamBuildCtx(buildCtx))
}

func ClassifyMemoryPath(cfg *Config, path string) MemoryPathClass {
	switch {
	case isAutoMemPath(cfg, path):
		return MemoryPathClassAuto
	case IsAgentMemoryPath(cfg, path):
		return MemoryPathClassAgent
	default:
		return MemoryPathClassOther
	}
}

func IsAutoMemoryPath(cfg *Config, path string) bool {
	return ClassifyMemoryPath(cfg, path) == MemoryPathClassAuto
}

func IsAgentMemoryPath(cfg *Config, path string) bool {
	if cfg == nil || strings.TrimSpace(path) == "" {
		return false
	}
	return NewAgentMemoryManager(cfg).IsAgentMemoryPath(path)
}

func (m *TeamMemoryManager) IsTeamMemoryEnabled(buildCtx ...contract.BuildCtx) bool {
	return isTeamMemoryEnabled(m, firstTeamBuildCtx(buildCtx))
}

func GetMemoryScopeDisplay(scope MemoryScope) string {
	switch scope {
	case MemoryScopeUser:
		return "user-scoped agent memory"
	case MemoryScopeLocal:
		return "local-scoped agent memory"
	default:
		return "project-scoped agent memory"
	}
}

func agentMemoryFailureStatus(scope MemoryScope, err error) string {
	if err == nil {
		return "loaded"
	}
	if errors.Is(err, ErrInvalidProjectDir) && scope == MemoryScopeLocal {
		return "local_unavailable"
	}
	if errors.Is(err, ErrInvalidProjectDir) || errors.Is(err, ErrInvalidAgentScope) || errors.Is(err, ErrInvalidMemoryRoot) {
		return "deny"
	}
	return "ensure_dir_failed"
}

func agentMemorySuccessStatus(result agentMemoryLoadResult) string {
	if result.unreadable {
		return "unreadable"
	}
	if result.wasTruncated || result.wasByteTruncated {
		return "truncated"
	}
	return "loaded"
}

func (m *TeamMemoryManager) ValidateTeamMemWritePath(path string) error {
	return validateTeamMemWriteRequest(m, path)
}

func (m *TeamMemoryManager) ValidateTeamMemKey(key string) error {
	return validateTeamMemKeyRequest(m, key)
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

func HandleDateChange() {}

func LoadNestedMemoryPaths() []string {
	return []string{}
}

func defaultRootDir(platformCfg *platformconfig.Config) string {
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".multi-agent", "memory")
	}
	if platformCfg != nil && strings.TrimSpace(platformCfg.ProjectRoot) != "" {
		return filepath.Join(platformCfg.ProjectRoot, ".multi-agent", "memory")
	}
	return ""
}

func defaultProjectRoot(platformCfg *platformconfig.Config) string {
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
