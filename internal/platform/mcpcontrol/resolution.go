package mcpcontrol

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
)

func resolveRegisteredInstance(registry *ToolRegistry, key dto.LeaseKey, allowStale bool) (*ToolInstance, error) {
	return lookupLease(leaseLookupOptions{
		registry:   registry,
		key:        key,
		allowStale: allowStale,
	})
}

// contextPayload 处理上下文载荷。
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

func contextAgentFields(instance *ToolInstance, snapshot *contract.AgentSnapshot) (string, string, string, int) {
	if snapshot == nil {
		return instance.AgentID, instance.ThreadID, instance.Status, instance.PID
	}
	return strings.TrimSpace(snapshot.ID), strings.TrimSpace(snapshot.ThreadID), strings.TrimSpace(snapshot.State), snapshot.PID
}

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

// FindActiveByKind 按kind查找active。
func (r *ToolRegistry) FindActiveByKind(clientKind string) []*ToolInstance {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.findActiveByKindLocked(strings.TrimSpace(clientKind))
}

// FindActiveForScope 为作用域查找active。
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
