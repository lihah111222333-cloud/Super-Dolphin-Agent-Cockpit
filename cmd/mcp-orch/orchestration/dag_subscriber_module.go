package orchestration

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
)

// ProvideDAGSubscriberNodeFlowStore narrows the aggregate taskdag.Store down
// to the NodeFlowStore needed by the DAG turn.completed subscriber
// (ADR-017 v1.2 §2.9). Type assertion is statically guarded by
// store_compile_assertions_test.go's
// `var _ NodeFlowStore = (*store)(nil)`.
//
// We mirror taskdag.ProvideDispatchNodeStore pattern (also a narrow-port
// adapter via type assertion). No new fx wrapper struct — direct interface
// return so fx can resolve `DAGSubscriberDeps.FlowStore`.
func ProvideDAGSubscriberNodeFlowStore(store taskdag.Store) taskdag.NodeFlowStore {
	return store
}

// ProvideDAGSubscriberStopAgentService narrows *service down to the
// single-method StopAgentService port required by the DAG subscriber's
// stop_helper call (ADR-016 v1.2 §3.2 contract #2). The wrapping is needed
// because fx resolves interfaces by their declared types — passing *service
// directly would shadow other StopAgentService consumers (none today, but
// the indirection keeps the contract narrow).
func ProvideDAGSubscriberStopAgentService(s *service) StopAgentService {
	return s
}

// ProvideDAGSubscriberAgentThreadLookup adapts the orchestration-internal
// AgentThreadStore (set on *service.agentThreads via runtime wiring) into
// the AgentThreadLookup narrow port. The store ALREADY satisfies the
// interface signature (GetByThreadID exists on AgentThreadStore), but fx
// resolves by declared interface — this adapter exposes the single
// GetByThreadID method without dragging ListAll / UpdateStatus into the
// subscriber's DI graph.
//
// Returning a nil AgentThreadLookup when *service has no agentThreads
// wired is intentional: StopSpawnedAgent's preflight handles a nil
// AgentThreadLookup with StopResultSkippedLookupFailed (stop_helper.go:150).
//
// ⚠️ P2 风险（W-A1 reviewer B 二审揭出，未阅手）：当前 nil 返回依赖
// 唯一 consumer（dag_turn_completed_subscriber.go:341 stopSpawnedAgentForSubscriber）
// 在调用前判 deps.AgentThreads == nil 即 return 的应用层短路；未来若新增
// AgentThreadLookup consumer 未判 nil 即 deref 会 nil panic。根治修法详 H13
// follow-up：改返非 nil 哨兵 lookup（GetByThreadID 永返 ErrNotFound）避免
// consumer 变多后隔离失效。
func ProvideDAGSubscriberAgentThreadLookup(s *service) AgentThreadLookup {
	if s == nil || s.agentThreads == nil {
		return nil
	}
	return agentThreadLookupAdapter{store: s.agentThreads}
}

// agentThreadLookupAdapter narrows AgentThreadStore (3 methods) down to
// AgentThreadLookup (1 method). Kept as a value type with a single embedded
// store pointer — no allocations on the lookup path.
type agentThreadLookupAdapter struct {
	store AgentThreadStore
}

func (a agentThreadLookupAdapter) GetByThreadID(ctx context.Context, threadID string) (*PersistedThread, error) {
	return a.store.GetByThreadID(ctx, threadID)
}
