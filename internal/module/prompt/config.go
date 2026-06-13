package prompt

import (
	"os"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

const (
	envEnablePromptRegistry            = "ENABLE_PROMPT_REGISTRY"
	envEnablePromptAssembly            = "ENABLE_PROMPT_ASSEMBLY"
	envEnableSystemContextCacheBreaker = "ENABLE_PROMPT_SYSTEM_CONTEXT_CACHE_BREAKER"
	envClaudeSimple                    = "CLAUDE_CODE_SIMPLE"
	// DISABLE_BUILTIN_STATIC_SECTIONS: set to true/1 to skip registering the
	// hardcoded Claude-Code-parity static sections (identity / system_constraints
	// / engineering / actions / tool_preferences / style / output_efficiency).
	// Dynamic providers (language, env info, skill catalog, etc.) still run.
	// Use this when the operator wants a DB-backed prompt_template to be the
	// sole source of truth for the static system prompt instead of having
	// mergeTemplateSections layer on top of built-ins.
	envDisableBuiltinStaticSections = "DISABLE_BUILTIN_STATIC_SECTIONS"
)

type Config struct {
	EnableRegistry                  bool
	EnableAssembly                  bool
	EnableSystemContextCacheBreaker bool
}

// NewConfig 创建配置。
func NewConfig(_ *contract.Config) *Config {
	return &Config{
		EnableRegistry:                  parseBoolEnv(envEnablePromptRegistry, false),
		EnableAssembly:                  parseBoolEnv(envEnablePromptAssembly, false),
		EnableSystemContextCacheBreaker: parseBoolEnv(envEnableSystemContextCacheBreaker, false),
	}
}

func parseBoolEnv(key string, fallback bool) bool {
	raw := os.Getenv(key)
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}
