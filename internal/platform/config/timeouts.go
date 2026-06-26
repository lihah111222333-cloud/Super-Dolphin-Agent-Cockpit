package config

import (
	"context"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/util/ctxutil"
)

const (
	LaunchTimeout             = ctxutil.LaunchTimeout
	StartupTimeout            = ctxutil.StartupTimeout
	ShutdownTimeout           = ctxutil.ShutdownTimeout
	InitialThreadIDTimeout    = ctxutil.InitialThreadIDTimeout
	SessionCloseTimeout       = ctxutil.SessionCloseTimeout
	HealthCheckPeriod         = ctxutil.HealthCheckPeriod
	StallDetectDelay          = ctxutil.StallDetectDelay
	DBQueryTimeout            = ctxutil.DBQueryTimeout
	TxCleanupTimeout          = ctxutil.TxCleanupTimeout
	RPCRequestTimeout         = ctxutil.RPCRequestTimeout
	InterruptSettleTimeout    = ctxutil.InterruptSettleTimeout
	AsyncLaunchTimeout        = ctxutil.AsyncLaunchTimeout
	DreamConsolidationTimeout = ctxutil.DreamConsolidationTimeout
	PromptIntentDraftTimeout  = ctxutil.PromptIntentDraftTimeout
)

// WithTimeout 用仓库统一工具创建带超时的 context。
// 调用方仍必须调用返回的 cancel，避免 timer 资源泄漏。
func WithTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return ctxutil.WithTimeout(ctx, timeout)
}

// WithTimeoutIfNone 仅在父 context 没有 deadline 时追加 timeout。
// 已有更严格 deadline 时会保留上游边界，避免嵌套调用悄悄放宽取消时间。
func WithTimeoutIfNone(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return ctxutil.WithTimeoutIfNone(ctx, timeout)
}

// WithInitialThreadIDTimeout 为启动阶段等待首个 threadID 设置统一上限。
func WithInitialThreadIDTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctxutil.WithInitialThreadIDTimeout(ctx)
}

// WithSessionCloseTimeout 为 provider 会话关闭流程设置统一上限。
func WithSessionCloseTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctxutil.WithSessionCloseTimeout(ctx)
}

// WithDBQueryTimeout 为普通数据库查询设置统一上限，避免 UI/RPC 请求长期占用连接。
func WithDBQueryTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctxutil.WithDBQueryTimeout(ctx)
}

// WithTxCleanupTimeout 为事务回滚或清理路径设置独立上限。
// 清理超时不应继承业务请求的长 deadline，否则失败路径会拖慢关闭。
func WithTxCleanupTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctxutil.WithTxCleanupTimeout(ctx)
}

// WithRPCRequestTimeout 为 JSON-RPC 请求处理设置统一上限。
func WithRPCRequestTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctxutil.WithRPCRequestTimeout(ctx)
}

// WithPeerTimeout 为 MCP/peer 调用创建指定上限，并沿用 ctxutil 的 deadline 保留策略。
func WithPeerTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return ctxutil.WithPeerTimeout(ctx, timeout)
}
