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

// handleHookSubscribe 将当前注册 peer 订阅到 hook topic，入参校验和错误映射由 handleHookRPC 统一处理。
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

// handleHookResolve 处理当前 peer 对待决 hook 的决策，依赖 idempotency_key 防止重复决策。
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

// handleHookPending 读取当前 peer 可见的待决 hook；shared-service peer 必须显式传 agent_id。
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

// resolveHookPendingAgentID 约束 pending 查询只能落在 peer 自身 agent，shared-service 例外需显式指定。
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

// validateHookSubscribeRequest 暴露给测试和复用方，保持与 RPC 路径一致的错误映射。
func validateHookSubscribeRequest(req dto.HookSubscribeRequest) error {
	return asHookRPCError(validateHookSubscribeInput(req))
}

// validateHookResolveRequest 暴露给测试和复用方，保持与 RPC 路径一致的错误映射。
func validateHookResolveRequest(req dto.HookResolveRequest) error {
	return asHookRPCError(validateHookResolveInput(req))
}

// resolveCurrentRegisteredInstance 用当前 jrpc2 server 反查租约，再读取可调用实例快照。
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

// lookupLeaseByServer 在注册表中查找绑定当前 jrpc2 server 的租约，供自描述 RPC 使用。
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

// decodePayloadMap 将 approval payload 解成 map；非对象 JSON 会作为原始 payload 透传。
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

// timeDurationMillis 将客户端毫秒超时转换为 Duration，非正数使用默认 notify 超时。
func timeDurationMillis(timeoutMs int) time.Duration {
	if timeoutMs <= 0 {
		return defaultNotifyTimeout
	}
	return time.Duration(timeoutMs) * time.Millisecond
}
