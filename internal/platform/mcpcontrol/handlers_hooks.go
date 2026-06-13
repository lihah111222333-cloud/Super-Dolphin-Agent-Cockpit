package mcpcontrol

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/creachadair/jrpc2"
)

func handleHookSubscribe(
	ctx context.Context,
	registry *ToolRegistry,
	hookManager contract.HookManager,
	req dto.HookSubscribeRequest,
) (dto.HookSubscribeResponse, error) {
	return handleHookRPC(ctx, registry, hookManager, req, "subscribe", validateHookSubscribeInput,
		func(ctx context.Context, hookManager contract.HookManager, instance *ToolInstance, req dto.HookSubscribeRequest) (dto.HookSubscribeResponse, error) {
			return hookManager.Subscribe(ctx, instance.Lease, req)
		},
	)
}

func handleHookResolve(
	ctx context.Context,
	registry *ToolRegistry,
	hookManager contract.HookManager,
	req dto.HookResolveRequest,
) (dto.HookResolveResponse, error) {
	return handleHookRPC(ctx, registry, hookManager, req, "resolve", validateHookResolveInput,
		func(ctx context.Context, hookManager contract.HookManager, instance *ToolInstance, req dto.HookResolveRequest) (dto.HookResolveResponse, error) {
			return hookManager.Resolve(ctx, instance.Lease, req)
		},
	)
}

func handleHookPending(
	ctx context.Context,
	registry *ToolRegistry,
	hookManager contract.HookManager,
	req dto.HookPendingRequest,
) (dto.HookPendingResponse, error) {
	return handleHookRPC(ctx, registry, hookManager, req, "pending", nil,
		func(ctx context.Context, hookManager contract.HookManager, instance *ToolInstance, req dto.HookPendingRequest) (dto.HookPendingResponse, error) {
			agentID, err := resolveHookPendingAgentID(instance, req)
			if err != nil {
				return dto.HookPendingResponse{}, err
			}
			reviews, err := hookManager.GetPendingReviews(ctx, agentID)
			if err != nil {
				return dto.HookPendingResponse{}, err
			}
			return dto.HookPendingResponse{Reviews: reviews}, nil
		},
	)
}

// resolveHookPendingAgentID 解析hook待处理代理ID。
func resolveHookPendingAgentID(instance *ToolInstance, req dto.HookPendingRequest) (string, error) {
	instanceAgentID := ""
	if instance != nil {
		instanceAgentID = strings.TrimSpace(instance.AgentID)
	}
	requestAgentID := strings.TrimSpace(req.AgentID)
	switch {
	case instanceAgentID != "" && requestAgentID != "" && requestAgentID != instanceAgentID:
		return "", errAuthFailed("hook pending agent_id %q does not match registered agent_id %q", requestAgentID, instanceAgentID)
	case instanceAgentID != "":
		return instanceAgentID, nil
	case requestAgentID != "":
		return requestAgentID, nil
	default:
		return "", errInvalidParams("hook pending requires agent_id; shared-service peers must provide an agent-scoped query")
	}
}

func validateHookSubscribeRequest(req dto.HookSubscribeRequest) error {
	return asHookRPCError(validateHookSubscribeInput(req))
}

func validateHookResolveRequest(req dto.HookResolveRequest) error {
	return asHookRPCError(validateHookResolveInput(req))
}

func resolveCurrentRegisteredInstance(ctx context.Context, registry *ToolRegistry) (*ToolInstance, error) {
	server, err := serverFromContext(ctx)
	if err != nil {
		return nil, err
	}
	lease, ok := registry.lookupLeaseByServer(server)
	if !ok {
		return nil, errLeaseNotFound("mcp lease for current peer is not registered")
	}
	return resolveRegisteredInstance(registry, lease, false)
}

// lookupLeaseByServer 按服务端处理lookup租约。
func (r *ToolRegistry) lookupLeaseByServer(server *jrpc2.Server) (LeaseKey, bool) {
	if r == nil || server == nil {
		return LeaseKey{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for lease, instance := range r.instances {
		if instance == nil {
			continue
		}
		switch peer := instance.Peer.(type) {
		case jrpcPeer:
			if peer.server == server {
				return lease, true
			}
		case *jrpcPeer:
			if peer != nil && peer.server == server {
				return lease, true
			}
		}
	}
	return LeaseKey{}, false
}

func decodePayloadMap(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err == nil {
		return payload
	}
	return map[string]any{"payload": append(json.RawMessage(nil), raw...)}
}

func timeDurationMillis(timeoutMs int) time.Duration {
	if timeoutMs <= 0 {
		return defaultNotifyTimeout
	}
	return time.Duration(timeoutMs) * time.Millisecond
}
