package contract

import (
	"context"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

// ---------------------------------------------------------------------------
// Error code constants – domain-aware RPC error codes.
// These are plain integer constants with no framework dependency.
// ---------------------------------------------------------------------------

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

// CapabilityResolver returns the active provider capabilities from context.
type CapabilityResolver func(ctx context.Context) (dto.CapabilitySet, error)

// ---------------------------------------------------------------------------
// Thread-scoped context helpers (stdlib only).
// ---------------------------------------------------------------------------

type threadIDKey struct{}

// ThreadIDFrom extracts the thread ID previously set by ThreadScope.
// ThreadIDFrom 从跨模块契约处理线程ID。
func ThreadIDFrom(ctx context.Context) string {
	value, _ := ctx.Value(threadIDKey{}).(string)
	return value
}

// WithThreadID sets the thread ID in context.
// WithThreadID 设置线程ID。
func WithThreadID(ctx context.Context, threadID string) context.Context {
	return context.WithValue(ctx, threadIDKey{}, threadID)
}

// Thread RPC method names shared by the app-side thread module and remote
// orchestration launcher. Keep these in a dependency-light contract package so
// cmd/mcp-orch and internal/module/thread cannot silently drift apart.
const (
	ThreadRPCStart   = "thread/start"
	ThreadRPCFork    = "thread/fork"
	ThreadRPCStop    = "thread/stop"
	ThreadRPCArchive = "thread/archive"
	ThreadRPCNameSet = "thread/name/set"
	TurnRPCStart     = "turn/start"
	TurnRPCInterrupt = "turn/interrupt"
)
