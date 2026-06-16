package contract

import (
	"context"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

// BootstrapHookAfterHandler is the after-hook entry the bootstrap runtime
// (internal/mcpserver/runtime/bootstrap) invokes when a ctl/hook/after
// callback lands. Exposing the handler as a plain function type here lets
// the cmd/mcp-orch root assembly wire the subpackage's hook implementation
// into bootstrap without typing on an orchestration subpackage interface
// (P22 P4 §278 — HookConsumer 不再作为子包直接导出的 bootstrap/hook 协议入口).
type BootstrapHookAfterHandler func(ctx context.Context, payload mcp.HookPayload) (mcp.AfterDecision, error)
