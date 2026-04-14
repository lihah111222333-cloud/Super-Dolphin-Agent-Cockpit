package memory

import (
	"os"
	"path/filepath"
	"strings"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

const (
	envMemoryRoot                  = "MULTI_AGENT_MEMORY_DIR"
	envClaudeRemoteMemoryDir       = "CLAUDE_CODE_REMOTE_MEMORY_DIR"
	envMemoryPathOverride          = "MULTI_AGENT_MEMORY_PATH_OVERRIDE"
	envClaudeMemoryPathOverride    = "CLAUDE_COWORK_MEMORY_PATH_OVERRIDE"
	envEnableMemorySystem          = "ENABLE_MEMORY_SYSTEM"
	envEnableMemoryTools           = "ENABLE_MEMORY_TOOLS"
	envMemorySkipIndex             = "MULTI_AGENT_MEMORY_SKIP_INDEX"
	envMemoryExtraGuidelines       = "MULTI_AGENT_MEMORY_EXTRA_GUIDELINES"
	envClaudeMemoryExtraGuidelines = "CLAUDE_COWORK_MEMORY_EXTRA_GUIDELINES"
	envFeatureKairos               = "MULTI_AGENT_MEMORY_FEATURE_KAIROS"
	envFeatureTeamMemory           = "MULTI_AGENT_MEMORY_FEATURE_TEAMMEM"
	envFeatureSearchPastContext    = "MULTI_AGENT_MEMORY_FEATURE_SEARCH_PAST_CONTEXT"
)

type MemoryFeatureFlags struct {
	Kairos            bool
	TeamMemory        bool
	SearchPastContext bool
}

type Config struct {
	Enabled             bool
	EnableTools         bool
	RootDir             string
	ProjectRoot         string
	AutoMemPathOverride string
	SkipIndex           bool
	ExtraGuidelines     []string
	Features            MemoryFeatureFlags
}

func NewConfig(platformCfg *platformconfig.Config) *Config {
	cfg := &Config{
		Enabled:             parseBoolEnv(envEnableMemorySystem, false),
		EnableTools:         parseBoolEnv(envEnableMemoryTools, false),
		RootDir:             defaultRootDir(platformCfg),
		ProjectRoot:         defaultProjectRoot(platformCfg),
		AutoMemPathOverride: firstNonEmptyEnv(envMemoryPathOverride, envClaudeMemoryPathOverride),
		SkipIndex:           parseBoolEnv(envMemorySkipIndex, false),
		ExtraGuidelines:     parseGuidelinesEnv(envMemoryExtraGuidelines, envClaudeMemoryExtraGuidelines),
		Features: MemoryFeatureFlags{
			Kairos:            parseBoolEnv(envFeatureKairos, false),
			TeamMemory:        parseBoolEnv(envFeatureTeamMemory, false),
			SearchPastContext: parseBoolEnv(envFeatureSearchPastContext, false),
		},
	}
	if root := firstNonEmptyEnv(envMemoryRoot, envClaudeRemoteMemoryDir); root != "" {
		cfg.RootDir = root
	}
	return cfg
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
