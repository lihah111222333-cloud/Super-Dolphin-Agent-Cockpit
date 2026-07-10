package threadprompt

import "github.com/anthropic-ai/super-agent-v3/internal/contract"

// NewRuntimeCatalog 组合 typed PromptStore 与内置 prompt registry。
// 两个来源都为空时返回 nil；只有 builtin 时返回合法的只读 catalog。
func NewRuntimeCatalog(store PromptStore, builtin contract.BuiltinPromptRegistry) RuntimePromptCatalog {
	return newRuntimeCatalog(store, builtin)
}

// RegisterProviders 将 typed runtime catalog 注册到动态 section assembly 边界。
func RegisterProviders(registrar contract.DynamicSectionRegistrar, catalog RuntimePromptCatalog) error {
	return registerProviders(registrar, catalog)
}
