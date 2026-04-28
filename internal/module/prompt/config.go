package prompt

import (
	"os"
	"strconv"
	"strings"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
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
	// P20.1 Phase 10 — skill progressive disclosure 开关与预算。
	// 默认开启：与上游 CLI 官方基线对齐（Claude Code description always in context +
	// Codex progressive disclosure 默认开启）。Phase 10 灰度试点结束。
	// 显式设 ENABLE_SKILL_PROGRESSIVE_DISCLOSURE=false 可回到旧语义：
	// SkillCatalogProvider 不注入，skill_catalog dynamic slot 渲染为空；
	// 上游旧 skill_expand_body / skill_read_resource 工具仍可通过 skill.Service 调用。
	envEnableSkillProgressiveDisclosure = "ENABLE_SKILL_PROGRESSIVE_DISCLOSURE"
	// SKILL_CATALOG_TOKEN_BUDGET 单位为 token；provider 内部按 ≈4 chars/token 换算。
	// 默认 3000 tokens ≈ 12000 chars，与 contract.SkillInjectionPort.ReservedTokens 默认一致。
	envSkillCatalogTokenBudget = "SKILL_CATALOG_TOKEN_BUDGET"
	// SKILL_CATALOG_META_INSTRUCTIONS 控制尾部是否追加 "How to use skills" 元指令。
	// 默认 true（Phase 9 行为），设 false 可关闭。
	envSkillCatalogMetaInstructions = "SKILL_CATALOG_META_INSTRUCTIONS"
	// SKILL_WRITER_FORMAT 控制 provider 写端 legacy/v1 切换；默认 legacy。
	envSkillWriterFormat = "SKILL_WRITER_FORMAT"
)

type Config struct {
	EnableRegistry                  bool
	EnableAssembly                  bool
	EnableSystemContextCacheBreaker bool
	// P20.1 Phase 10 grayscale：progressive disclosure 总开关。
	EnableSkillProgressiveDisclosure bool
	// SkillCatalogTokenBudget 单位 token；≤0 使用 provider 默认（3000 tokens）。
	SkillCatalogTokenBudget int
	// EmitSkillCatalogMetaInstructions 控制 manifest 尾部是否追加元指令。
	EmitSkillCatalogMetaInstructions bool
	// SkillWriterFormat 只反映当前 env 的有效值；provider 写端仍逐次 os.Getenv，
	// 避免测试间缓存污染。
	SkillWriterFormat string
}

func NewConfig(_ *platformconfig.Config) *Config {
	return &Config{
		EnableRegistry:                  parseBoolEnv(envEnablePromptRegistry, false),
		EnableAssembly:                  parseBoolEnv(envEnablePromptAssembly, false),
		EnableSystemContextCacheBreaker: parseBoolEnv(envEnableSystemContextCacheBreaker, false),
		// Phase 10 default：默认注册 SkillCatalogProvider；显式 false 可 opt-out。
		// meta-instructions 默认开启（Phase 9 行为）。
		EnableSkillProgressiveDisclosure: parseBoolEnv(envEnableSkillProgressiveDisclosure, true),
		SkillCatalogTokenBudget:          parseIntEnv(envSkillCatalogTokenBudget, 0),
		EmitSkillCatalogMetaInstructions: parseBoolEnv(envSkillCatalogMetaInstructions, true),
		SkillWriterFormat:                parseSkillWriterFormat(envSkillWriterFormat, "legacy"),
	}
}

func parseBoolEnv(key string, fallback bool) bool {
	raw, ok := os.LookupEnv(key)
	// Preserve legacy/automation opt-out for an explicitly present empty Phase 10 flag;
	// an unset flag still falls through to fallback.
	if key == envEnableSkillProgressiveDisclosure && ok && strings.TrimSpace(raw) == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

// parseIntEnv 解析整型环境变量；空值 / 无效值 / ≤0 → fallback。
func parseIntEnv(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func parseSkillWriterFormat(key, fallback string) string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "v1":
		return "v1"
	case "", "legacy":
		return fallback
	default:
		return fallback
	}
}
