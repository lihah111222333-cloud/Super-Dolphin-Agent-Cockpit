package protocol

import "github.com/anthropic-ai/super-agent-v3/internal/contract"

// DynamicToolSchema 是 Codex protocol 层对 contract.DynamicToolSchema 的类型别名。
// 这里保留 protocol 包名，避免上层直接依赖 contract 包穿透 provider 边界。
type DynamicToolSchema = contract.DynamicToolSchema
