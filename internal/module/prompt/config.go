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
	// envDisableBuiltinStaticSections 为 true 时不注册内置静态 prompt section。
	// 动态 provider 仍会运行；该开关用于让 DB prompt_template 独占静态系统提示。
	envDisableBuiltinStaticSections = "DISABLE_BUILTIN_STATIC_SECTIONS"
)

// Config 保存 prompt 模块的功能开关，由环境变量在启动时确定。
type Config struct {
	EnableRegistry                  bool
	EnableAssembly                  bool
	EnableSystemContextCacheBreaker bool
}

// NewConfig 从环境变量读取 prompt 模块开关。
// 这里不从 contract.Config 派生，确保启动时的实验开关与运行配置解耦。
func NewConfig(_ *contract.Config) *Config {
	return &Config{
		EnableRegistry:                  parseBoolEnv(envEnablePromptRegistry, false),
		EnableAssembly:                  parseBoolEnv(envEnablePromptAssembly, false),
		EnableSystemContextCacheBreaker: parseBoolEnv(envEnableSystemContextCacheBreaker, false),
	}
}

// parseBoolEnv 解析布尔环境变量，支持 1/true/yes/on 和 0/false/no/off，未设置时返回 fallback。
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
