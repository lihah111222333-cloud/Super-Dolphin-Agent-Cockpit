package mcpcontrol

import (
	"context"
	"errors"
	"fmt"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/creachadair/jrpc2"
)

type hookInvalidParamsError struct {
	message string
}

// Error 返回错误文本。
func (e *hookInvalidParamsError) Error() string {
	return e.message
}

type disconnectLeaseOptions struct {
	ctx     context.Context
	peer    Peer
	timeout bool
}

type leaseLookupOptions struct {
	registry   *ToolRegistry
	key        dto.LeaseKey
	expected   LeaseKey
	allowStale bool
}

type fanoutOperation struct {
	name   string
	invoke func(context.Context, Peer) error
}

func newHookInvalidParams(format string, args ...any) error {
	return &hookInvalidParamsError{message: fmt.Sprintf(format, args...)}
}

func asHookRPCError(err error) error {
	if err == nil {
		return nil
	}
	var invalid *hookInvalidParamsError
	if errors.As(err, &invalid) {
		return errInvalidParams("%s", invalid.Error())
	}
	return err
}

func validateHookSubscribeInput(req dto.HookSubscribeRequest) error {
	if strings.TrimSpace(req.SubscriptionID) == "" {
		return newHookInvalidParams("hook subscription requires subscription_id")
	}
	for _, topic := range req.Topics {
		if strings.TrimSpace(topic) != "" {
			return nil
		}
	}
	return newHookInvalidParams("hook subscription requires at least one topic")
}

func validateHookResolveInput(req dto.HookResolveRequest) error {
	if strings.TrimSpace(req.HookCallID) == "" {
		return newHookInvalidParams("hook resolve requires hook_call_id")
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		return newHookInvalidParams("hook resolve requires idempotency_key")
	}
	if strings.TrimSpace(req.Decision) == "" {
		return newHookInvalidParams("hook resolve decision must be approve or reject")
	}
	return nil
}

func resolveServerPeer(ctx context.Context) (server *jrpc2.Server, peer Peer, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = errPeerUnavailable("mcp control request must run inside a jrpc2 handler")
			server = nil
			peer = nil
		}
	}()
	server = jrpc2.ServerFromContext(ctx)
	if server == nil {
		return nil, nil, errPeerUnavailable("mcp control peer is not available")
	}
	peer = jrpcPeer{server: server}
	return server, peer, nil
}

func withResolvedInstance[Req any, Resp any](
	registry *ToolRegistry,
	req Req,
	lease func(Req) dto.LeaseKey,
	fn func(*ToolInstance) (Resp, error),
) (Resp, error) {
	var zero Resp
	instance, err := resolveRegisteredInstance(registry, lease(req), false)
	if err != nil {
		return zero, err
	}
	return fn(instance)
}

func withCurrentRegisteredInstance[Resp any](
	ctx context.Context,
	registry *ToolRegistry,
	fn func(*ToolInstance) (Resp, error),
) (Resp, error) {
	var zero Resp
	instance, err := resolveCurrentRegisteredInstance(ctx, registry)
	if err != nil {
		return zero, err
	}
	return fn(instance)
}

func handleHookRPC[Req any, Resp any](
	ctx context.Context,
	registry *ToolRegistry,
	hookManager contract.HookManager,
	req Req,
	operation string,
	validate func(Req) error,
	execute func(context.Context, contract.HookManager, *ToolInstance, Req) (Resp, error),
) (Resp, error) {
	var zero Resp
	if hookManager == nil {
		return zero, errCapabilityMismatch("hook manager is not configured")
	}
	if validate != nil {
		if err := validate(req); err != nil {
			return zero, asHookRPCError(err)
		}
	}
	return withCurrentRegisteredInstance(ctx, registry, func(instance *ToolInstance) (Resp, error) {
		resp, err := execute(ctx, hookManager, instance, req)
		if err != nil {
			return zero, mapHookHandlerError(operation, err)
		}
		return resp, nil
	})
}

// forEachInstanceBucket 为eachinstancebucket处理平台mcpcontrol。
func (r *ToolRegistry) forEachInstanceBucket(
	instance *ToolInstance,
	fn func(index map[string]map[LeaseKey]struct{}, bucket string, key LeaseKey),
) {
	if r == nil || instance == nil || fn == nil {
		return
	}
	key := instance.Lease
	for _, topic := range instance.Subscriptions {
		fn(r.bySubscription, topic, key)
	}
	for _, capability := range instance.Capabilities {
		fn(r.byCapability, capability, key)
	}
	fn(r.byAgent, instance.AgentID, key)
	fn(r.byThread, instance.ThreadID, key)
	fn(r.byClientKind, instance.ClientKind, key)
	fn(r.byInstance, instance.Lease.InstanceID, key)
	fn(r.byPeerKind, instance.PeerKind, key)
}

func (r *ToolRegistry) disconnectLease(key LeaseKey, opts disconnectLeaseOptions) error {
	var err error
	if key != (LeaseKey{}) {
		if opts.timeout {
			err = r.cleanupLeaseWithTimeout(opts.ctx, key)
		} else {
			r.cleanupLease(opts.ctx, key)
		}
	}
	closePeer(opts.peer)
	return err
}

// lookupLease 处理lookup租约。
func lookupLease(opts leaseLookupOptions) (*ToolInstance, error) {
	if opts.registry == nil {
		return nil, errLeaseNotFound("mcp registry is not configured")
	}
	normalized, err := normalizeLeaseKey(opts.key)
	if err != nil {
		return nil, err
	}
	opts.registry.mu.RLock()
	defer opts.registry.mu.RUnlock()

	instance := opts.registry.instances[normalized]
	if instance == nil {
		return nil, errLeaseNotFound("mcp lease %s/%d not found", normalized.InstanceID, normalized.Generation)
	}
	if opts.expected.InstanceID != "" && opts.expected != normalized {
		return nil, errLeaseNotFound("mcp lease %s/%d does not match expected key", normalized.InstanceID, normalized.Generation)
	}
	switch instance.Status {
	case dto.StatusDisconnected:
		return nil, errPeerUnavailable("mcp peer %s/%d is disconnected", normalized.InstanceID, normalized.Generation)
	case dto.StatusStale:
		if !opts.allowStale {
			return nil, errLeaseStale("mcp lease %s/%d is stale", normalized.InstanceID, normalized.Generation)
		}
	}
	return cloneInstance(instance), nil
}

// fanoutTargets 处理fanouttargets。
func (r *ToolRegistry) fanoutTargets(
	ctx context.Context,
	targets []sendTarget,
	method string,
	operation fanoutOperation,
) error {
	method = strings.TrimSpace(method)
	if method == "" {
		return errInvalidParams("mcp %s method is required", operation.name)
	}
	if len(targets) == 0 {
		return nil
	}

	workers := min(r.fanoutParallelism, len(targets))
	jobs := make(chan sendTarget, len(targets))
	errs := make(chan error, len(targets))
	for i := 0; i < workers; i++ {
		go r.runFanoutWorker(ctx, jobs, errs, method, operation)
	}
	for _, target := range targets {
		jobs <- target
	}
	close(jobs)

	var joined error
	for range targets {
		joined = errors.Join(joined, <-errs)
	}
	return joined
}

func (r *ToolRegistry) runFanoutWorker(
	ctx context.Context,
	jobs <-chan sendTarget,
	errs chan<- error,
	method string,
	operation fanoutOperation,
) {
	defer func() {
		if rec := recover(); rec != nil {
			pkglogger.Error("mcp "+operation.name+" worker goroutine panic",
				"method", method,
				"panic", rec,
			)
		}
	}()
	for target := range jobs {
		var err error
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					err = r.recoverWorkerPanic(ctx, operation.name, method, target, rec)
				}
			}()
			err = r.invokeFanoutTarget(ctx, target, operation)
		}()
		errs <- err
	}
}

func (r *ToolRegistry) invokeFanoutTarget(ctx context.Context, target sendTarget, operation fanoutOperation) error {
	callCtx, cancel := withTimeoutContext(ctx, r.notifyTimeout)
	defer cancel()
	if err := operation.invoke(callCtx, target.peer); err != nil {
		peer, evicted := r.notePeerFailure(target.key)
		if evicted {
			_ = r.disconnectLease(target.key, disconnectLeaseOptions{
				ctx:     ctx,
				peer:    peer,
				timeout: true,
			})
		} else {
			closePeer(peer)
		}
		return fmt.Errorf("%s/%d: %w", target.key.InstanceID, target.key.Generation, err)
	}
	r.resetPeerFailure(target.key)
	return nil
}

func baseConfigPayload(eventType string, header shareddto.AgentSessionHeader) map[string]any {
	payload := map[string]any{
		"event": eventType,
	}
	setPayloadString(payload, "threadId", header.ThreadID)
	setPayloadString(payload, "agentId", header.AgentID)
	setPayloadString(payload, "sessionId", header.SessionID)
	return payload
}

func configPayloadHeader(agentID, threadID string) shareddto.AgentSessionHeader {
	return shareddto.AgentSessionHeader{
		AgentHeader: shareddto.AgentHeader{
			ThreadHeader: shareddto.ThreadHeader{
				ThreadID: threadID,
			},
			AgentID: agentID,
		},
	}
}

// mapHookHandlerError 映射hook处理器错误。
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
	var invalid *hookInvalidParamsError
	if errors.As(err, &invalid) {
		return errInvalidParams("%s", invalid.Error())
	}
	if errors.Is(err, contract.ErrHookReviewPermissionDenied) {
		return errAuthFailed("%v", err)
	}
	return errInternal("hook %s failed: %v", operation, err)
}
