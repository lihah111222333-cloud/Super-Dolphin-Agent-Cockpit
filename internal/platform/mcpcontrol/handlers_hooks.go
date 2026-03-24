package mcpcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/creachadair/jrpc2"
)

func handleHookSubscribe(
	ctx context.Context,
	registry *ToolRegistry,
	hookManager contract.HookManager,
	req dto.HookSubscribeRequest,
) (dto.HookSubscribeResponse, error) {
	if hookManager == nil {
		return dto.HookSubscribeResponse{}, errCapabilityMismatch("hook manager is not configured")
	}
	if err := validateHookSubscribeRequest(req); err != nil {
		return dto.HookSubscribeResponse{}, err
	}
	instance, err := resolveCurrentRegisteredInstance(ctx, registry)
	if err != nil {
		return dto.HookSubscribeResponse{}, err
	}
	resp, err := hookManager.Subscribe(ctx, instance.Lease, req)
	if err != nil {
		return dto.HookSubscribeResponse{}, mapHookHandlerError("subscribe", err)
	}
	return resp, nil
}

func handleHookResolve(
	ctx context.Context,
	registry *ToolRegistry,
	hookManager contract.HookManager,
	req dto.HookResolveRequest,
) (dto.HookResolveResponse, error) {
	if hookManager == nil {
		return dto.HookResolveResponse{}, errCapabilityMismatch("hook manager is not configured")
	}
	if err := validateHookResolveRequest(req); err != nil {
		return dto.HookResolveResponse{}, err
	}
	instance, err := resolveCurrentRegisteredInstance(ctx, registry)
	if err != nil {
		return dto.HookResolveResponse{}, err
	}
	resp, err := hookManager.Resolve(ctx, instance.Lease, req)
	if err != nil {
		return dto.HookResolveResponse{}, mapHookHandlerError("resolve", err)
	}
	return resp, nil
}

func handleHookPending(
	ctx context.Context,
	registry *ToolRegistry,
	hookManager contract.HookManager,
	req dto.HookPendingRequest,
) (dto.HookPendingResponse, error) {
	if hookManager == nil {
		return dto.HookPendingResponse{}, errCapabilityMismatch("hook manager is not configured")
	}
	instance, err := resolveCurrentRegisteredInstance(ctx, registry)
	if err != nil {
		return dto.HookPendingResponse{}, err
	}
	agentID, err := resolveHookPendingAgentID(instance, req)
	if err != nil {
		return dto.HookPendingResponse{}, err
	}
	reviews, err := hookManager.GetPendingReviews(ctx, agentID)
	if err != nil {
		return dto.HookPendingResponse{}, mapHookHandlerError("pending", err)
	}
	return dto.HookPendingResponse{Reviews: reviews}, nil
}

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
	if strings.TrimSpace(req.SubscriptionID) == "" {
		return errInvalidParams("hook subscription requires subscription_id")
	}
	for _, topic := range req.Topics {
		if strings.TrimSpace(topic) != "" {
			return nil
		}
	}
	return errInvalidParams("hook subscription requires at least one topic")
}

func validateHookResolveRequest(req dto.HookResolveRequest) error {
	if strings.TrimSpace(req.HookCallID) == "" {
		return errInvalidParams("hook resolve requires hook_call_id")
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		return errInvalidParams("hook resolve requires idempotency_key")
	}
	if strings.TrimSpace(req.Decision) == "" {
		return errInvalidParams("hook resolve decision must be approve or reject")
	}
	return nil
}

func mapHookHandlerError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var rpcErr *jrpc2.Error
	if errors.As(err, &rpcErr) {
		return err
	}
	var storeErr *platformdb.StoreError
	if errors.As(err, &storeErr) {
		return errInternal("hook %s failed", operation)
	}
	if isHookInvalidParams(err) {
		return errInvalidParams("%v", err)
	}
	if errors.Is(err, contract.ErrHookReviewPermissionDenied) {
		return errAuthFailed("%v", err)
	}
	return errInternal("hook %s failed: %v", operation, err)
}

func isHookInvalidParams(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.TrimSpace(err.Error())
	switch {
	case strings.HasPrefix(msg, "hook subscription requires "):
		return true
	case strings.HasPrefix(msg, "hook subscription has invalid filters"):
		return true
	case strings.HasPrefix(msg, "hook resolve requires "):
		return true
	case strings.HasPrefix(msg, "hook resolve decision must be "):
		return true
	default:
		return false
	}
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
