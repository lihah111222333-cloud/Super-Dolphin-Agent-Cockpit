package contract

import (
	"context"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

// RPC 错误码常量保持无框架依赖，供本地 JRPC 和远端 orchestration 共用。

const (
	CodeNotFound        = -31001
	CodeInvalidState    = -31002
	CodeConflict        = -31003
	CodeCapabilityGate  = -31004
	CodeApprovalTimeout = -31005
	CodeNotImplemented  = -31006
	CodeInvalidParams   = -31007
	CodeMethodNotFound  = -31008
)

// CapabilityResolver 从请求上下文解析当前 provider 能力集合。
// RPC handler 用它在入口处做能力门禁，避免下游模块重复感知 provider 细节。
type CapabilityResolver func(ctx context.Context) (dto.CapabilitySet, error)

// Thread-scoped context helpers 只依赖标准库，避免 contract 层引入 RPC 实现包。

type threadIDKey struct{}

// ThreadIDFrom 读取通过 WithThreadID 写入的线程 ID。
// 未设置时返回空字符串，调用方可自行决定是否 fail-fast。
func ThreadIDFrom(ctx context.Context) string {
	value, _ := ctx.Value(threadIDKey{}).(string)
	return value
}

// WithThreadID 在 context 中附加线程 ID，供 RPC 入口和下游审计共享。
func WithThreadID(ctx context.Context, threadID string) context.Context {
	return context.WithValue(ctx, threadIDKey{}, threadID)
}

// Thread RPC 方法名由 app 侧 thread 模块和远端 orchestration launcher 共用。
// 放在轻依赖 contract 包中，避免 cmd/mcp-orch 与 internal/module/thread 静默漂移。
const (
	ThreadRPCStart   = "thread/start"
	ThreadRPCFork    = "thread/fork"
	ThreadRPCStop    = "thread/stop"
	ThreadRPCArchive = "thread/archive"
	ThreadRPCNameSet = "thread/name/set"
	TurnRPCStart     = "turn/start"
	TurnRPCInterrupt = "turn/interrupt"
)
