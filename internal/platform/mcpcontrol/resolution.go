package mcpcontrol

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

// resolveRegisteredInstance 按租约查找已注册实例，默认拒绝 stale peer 参与控制面调用。
func resolveRegisteredInstance(registry *ToolRegistry, key dto.LeaseKey, allowStale bool) (*ToolInstance, error) {
	return lookupLease(leaseLookupOptions{
		registry:   registry,
		key:        key,
		allowStale: allowStale,
	})
}

// contextPayload 根据请求 scope 生成最小上下文载荷，只暴露该 scope 允许读取的字段。
func contextPayload(scope string, instance *ToolInstance, snapshot *contract.AgentSnapshot) (map[string]any, error) {
	agentID, threadID, status, pid := contextAgentFields(instance, snapshot)
	switch strings.TrimSpace(scope) {
	case dto.ScopeAgentRuntime:
		return map[string]any{
			"agent_id":    agentID,
			"binary_name": instance.BinaryName,
			"client_kind": instance.ClientKind,
			"peer_kind":   instance.PeerKind,
			"pid":         pid,
			"status":      status,
		}, nil
	case dto.ScopeThreadBinding:
		return map[string]any{
			"agent_id":    agentID,
			"thread_id":   threadID,
			"instance_id": instance.Lease.InstanceID,
			"generation":  instance.Lease.Generation,
		}, nil
	case dto.ScopeWorkspaceRun:
		return map[string]any{
			"binary_name":   instance.BinaryName,
			"capabilities":  platformshared.CloneStrings(instance.Capabilities),
			"subscriptions": platformshared.CloneStrings(instance.Subscriptions),
		}, nil
	case dto.ScopeConfigSnapshot:
		return map[string]any{
			"capabilities":   platformshared.CloneStrings(instance.Capabilities),
			"client_kind":    instance.ClientKind,
			"config_version": instance.ConfigVersion,
			"peer_kind":      instance.PeerKind,
			"subscriptions":  platformshared.CloneStrings(instance.Subscriptions),
		}, nil
	default:
		return nil, errScopeNotAllowed("unsupported mcp context scope %q", scope)
	}
}

// contextAgentFields 优先使用 orchestration 快照，缺失时回退到注册表实例上的实时字段。
func contextAgentFields(instance *ToolInstance, snapshot *contract.AgentSnapshot) (string, string, string, int) {
	if snapshot == nil {
		return instance.AgentID, instance.ThreadID, instance.Status, instance.PID
	}
	return strings.TrimSpace(snapshot.ID), strings.TrimSpace(snapshot.ThreadID), strings.TrimSpace(snapshot.State), snapshot.PID
}

// buildContextResponse 将上下文 map 编码为协议响应，并记录本次读取的观测时间。
func buildContextResponse(scope string, payload map[string]any) (dto.ContextResponse, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return dto.ContextResponse{}, err
	}
	return dto.ContextResponse{
		Source:     dto.ContextSourceLive,
		ObservedAt: time.Now().UnixMilli(),
		Scope:      scope,
		Payload:    raw,
	}, nil
}

// FindActiveByKind 返回指定 client kind 的 active 实例快照，供共享控制面路由使用。
func (r *ToolRegistry) FindActiveByKind(clientKind string) []*ToolInstance {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.findActiveByKindLocked(strings.TrimSpace(clientKind))
}

// FindActiveForScope 先精确匹配 agent/thread，再放宽到 agent，最后才选择 shared-service peer。
func (r *ToolRegistry) FindActiveForScope(scope ToolScope) []*ToolInstance {
	scope = normalizeToolScope(scope)
	peers := r.FindActiveByKind(scope.Family)
	if len(peers) == 0 {
		return nil
	}
	if scope.AgentID != "" && scope.ThreadID != "" {
		if exact := filterActivePeers(peers, func(inst *ToolInstance) bool {
			return strings.TrimSpace(inst.AgentID) == scope.AgentID && strings.TrimSpace(inst.ThreadID) == scope.ThreadID
		}); len(exact) != 0 {
			return exact
		}
	}
	if scope.AgentID != "" {
		if relaxed := filterActivePeers(peers, func(inst *ToolInstance) bool {
			return strings.TrimSpace(inst.AgentID) == scope.AgentID
		}); len(relaxed) != 0 {
			return relaxed
		}
	}
	return filterActivePeers(peers, func(inst *ToolInstance) bool {
		return inst.Shared &&
			strings.TrimSpace(inst.PeerKind) == dto.PeerKindSharedService &&
			strings.TrimSpace(inst.ClientKind) == scope.Family
	})
}

// findActiveByKindLocked 在 byClientKind 索引内读取 active 实例；调用方必须已持有读锁。
func (r *ToolRegistry) findActiveByKindLocked(clientKind string) []*ToolInstance {
	keys, ok := r.byClientKind[clientKind]
	if !ok {
		return nil
	}
	var result []*ToolInstance
	for key := range keys {
		inst, ok := r.instances[key]
		if ok && inst.Status == dto.StatusActive {
			result = append(result, inst)
		}
	}
	return result
}

// filterActivePeers 保留符合谓词的实例，nil peer 或 nil 谓词都会被排除。
func filterActivePeers(peers []*ToolInstance, keep func(*ToolInstance) bool) []*ToolInstance {
	if keep == nil {
		return nil
	}
	var result []*ToolInstance
	for _, peer := range peers {
		if peer != nil && keep(peer) {
			result = append(result, peer)
		}
	}
	return result
}
