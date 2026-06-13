package shared

import (
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// CodexIdentity is an alias for contract.CodexIdentity.
// Kept for backward compatibility with existing provider-layer callers.
type CodexIdentity = contract.CodexIdentity

// Sentinel errors — aliases to the canonical definitions in contract.
var (
	ErrCodexHomeRequired          = contract.ErrCodexHomeRequired
	ErrCodexInstanceKeyRequired   = contract.ErrCodexInstanceKeyRequired
	ErrCodexModelProviderRequired = contract.ErrCodexModelProviderRequired
	ErrCodexHomeNotFound          = contract.ErrCodexHomeNotFound
	ErrCodexIdentityInvalidType   = contract.ErrCodexIdentityInvalidType
)

// ResolveCodexIdentity delegates to contract.ResolveCodexIdentity.
// ResolveCodexIdentity 解析codex身份。
func ResolveCodexIdentity(config map[string]any) (CodexIdentity, error) {
	return contract.ResolveCodexIdentity(config)
}

// CanonicalizeCodexHome delegates to contract.CanonicalizeCodexHome.
// CanonicalizeCodexHome 处理canonicalizecodexhome。
func CanonicalizeCodexHome(raw string) (string, error) {
	return contract.CanonicalizeCodexHome(raw)
}
