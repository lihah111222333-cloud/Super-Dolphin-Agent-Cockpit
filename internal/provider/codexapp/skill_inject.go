package codexapp

import (
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// defaultSkillManifestTokenBudget 与 P20.1 §3.7 建议保持一致，
// 后续 Phase 10 可通过 config.skill.token_budget 覆盖。
const defaultSkillManifestTokenBudget = 3000

// codexSkillInjectionPort 实现 contract.SkillInjectionPort。
//
// Codex CLI 当前版本没有类似 Claude Code 的 `.claude/skills/` 自动加载机制，
// 所有 skill body 都走本 harness 的 buildSkillPromptInput 注入。因此
// DetectNativeSkills 返回空切片，Resolver 不会把任何 skill 强制降级为 None。
//
// 若未来 Codex 推出类似 agents-md 外的原生 skill 路径（P20 调研时官方文档
// 已有 Skills 设计），在此扩展即可，不需要改 Resolver 消费侧。
type codexSkillInjectionPort struct{}

// NewSkillInjectionPort 构造 codex provider 的 Port 实例。
//
// Phase 7 集成点：driver / fx module 可注入这个 Port 给 turn resolver /
// prompt catalog provider 消费。目前返回值类型是 interface，便于单测 mock。
func NewSkillInjectionPort() contract.SkillInjectionPort {
	return codexSkillInjectionPort{}
}

// DetectNativeSkills 见 contract.SkillInjectionPort 接口文档。
// codexapp 返回空切片：没有原生 skill 机制。
func (codexSkillInjectionPort) DetectNativeSkills(_ string) []string {
	return nil
}

// ReservedTokens 见 contract.SkillInjectionPort 接口文档。
func (codexSkillInjectionPort) ReservedTokens() int {
	return defaultSkillManifestTokenBudget
}
