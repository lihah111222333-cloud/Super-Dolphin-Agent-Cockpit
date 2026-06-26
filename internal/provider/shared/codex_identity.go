package shared

import (
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// CodexIdentity 是 provider 层暴露的 Codex 身份别名。
// 真实定义位于 contract 包，保留别名用于兼容既有调用方。
type CodexIdentity = contract.CodexIdentity

// Codex 身份哨兵错误统一委托 contract 包定义，避免 provider 层复制错误值。
var (
	ErrCodexHomeRequired          = contract.ErrCodexHomeRequired
	ErrCodexInstanceKeyRequired   = contract.ErrCodexInstanceKeyRequired
	ErrCodexModelProviderRequired = contract.ErrCodexModelProviderRequired
	ErrCodexHomeNotFound          = contract.ErrCodexHomeNotFound
	ErrCodexIdentityInvalidType   = contract.ErrCodexIdentityInvalidType
)

// ResolveCodexIdentity 解析 provider 传入的 Codex 身份配置。
// 这里仅保留 provider 层入口，校验规则集中在 contract 包维护。
func ResolveCodexIdentity(config map[string]any) (CodexIdentity, error) {
	return contract.ResolveCodexIdentity(config)
}

// CanonicalizeCodexHome 规范化 provider 侧传入的 Codex home 路径。
// 路径存在性和格式校验由 contract 包统一执行。
func CanonicalizeCodexHome(raw string) (string, error) {
	return contract.CanonicalizeCodexHome(raw)
}
