package contract

import (
	"context"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

// BootstrapHookAfterHandler 是 bootstrap runtime 处理 ctl/hook/after 回调的函数边界。
// root assembly 通过该函数类型接入 hook 实现，避免 bootstrap 包依赖 orchestration 子包接口。
type BootstrapHookAfterHandler func(ctx context.Context, payload mcp.HookPayload) (mcp.AfterDecision, error)
