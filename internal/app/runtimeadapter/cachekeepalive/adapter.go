// Package cachekeepaliveadapter 将运行时 store 适配为 cache keepalive 查询端口。
package cachekeepaliveadapter

import (
	"context"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	bindingstore "github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/binding"
	threadstore "github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/thread"
	"go.uber.org/fx"
)

// Module 提供 cache keepalive 消费的两个 store 查询端口。
var Module = fx.Module("cachekeepaliveadapter",
	fx.Provide(
		provideCacheKeepaliveBindingLookup,
		provideCacheKeepaliveThreadLookup,
	),
)

type cacheKeepaliveBindingLookupAdapter struct {
	store bindingstore.Store
}

// provideCacheKeepaliveBindingLookup 把 binding store 裁剪成 keepalive 只读端口。
func provideCacheKeepaliveBindingLookup(store bindingstore.Store) contract.CacheKeepaliveBindingLookup {
	if store == nil {
		return nil
	}
	return cacheKeepaliveBindingLookupAdapter{store: store}
}

// GetCacheKeepaliveBindingByAgentID 只暴露 keepalive 判断 live binding 需要的字段。
func (a cacheKeepaliveBindingLookupAdapter) GetCacheKeepaliveBindingByAgentID(ctx context.Context, agentID string) (*contract.CacheKeepaliveBinding, error) {
	binding, err := a.store.GetByAgentID(ctx, agentID)
	if err != nil || binding == nil {
		return nil, err
	}
	return &contract.CacheKeepaliveBinding{
		AgentID:  binding.AgentID,
		Archived: binding.Archived,
	}, nil
}

type cacheKeepaliveThreadLookupAdapter struct {
	store threadstore.Store
}

// provideCacheKeepaliveThreadLookup 把 thread store 裁剪成 keepalive 启动事件回查端口。
func provideCacheKeepaliveThreadLookup(store threadstore.Store) contract.CacheKeepaliveThreadLookup {
	if store == nil {
		return nil
	}
	return cacheKeepaliveThreadLookupAdapter{store: store}
}

// GetCacheKeepaliveThreadByID 只返回 keepalive 反查 agentID 需要的线程身份字段。
func (a cacheKeepaliveThreadLookupAdapter) GetCacheKeepaliveThreadByID(ctx context.Context, threadID string) (*contract.CacheKeepaliveThreadRef, error) {
	thread, err := a.store.GetByThreadID(ctx, threadID)
	if err != nil || thread == nil {
		return nil, err
	}
	return &contract.CacheKeepaliveThreadRef{
		ThreadID: thread.ThreadID,
		AgentID:  thread.AgentID,
	}, nil
}
