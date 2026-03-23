package mcpcontrol

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

func resolveRegisteredInstance(registry *ToolRegistry, key dto.LeaseKey, allowStale bool) (*ToolInstance, error) {
	if registry == nil {
		return nil, errLeaseNotFound("mcp registry is not configured")
	}
	return registry.resolveLease(key, LeaseKey{}, allowStale)
}

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
			"capabilities":  cloneStrings(instance.Capabilities),
			"subscriptions": cloneStrings(instance.Subscriptions),
		}, nil
	case dto.ScopeConfigSnapshot:
		return map[string]any{
			"capabilities":   cloneStrings(instance.Capabilities),
			"client_kind":    instance.ClientKind,
			"config_version": instance.ConfigVersion,
			"peer_kind":      instance.PeerKind,
			"subscriptions":  cloneStrings(instance.Subscriptions),
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
