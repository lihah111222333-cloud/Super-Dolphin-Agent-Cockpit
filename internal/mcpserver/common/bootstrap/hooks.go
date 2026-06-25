package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/creachadair/jrpc2"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

// ---------------------------------------------------------------------------
// Handler types
// ---------------------------------------------------------------------------

// HookBeforeHandler handles a ctl/hook/before callback from the core layer.
// It receives the HookPayload and returns a BeforeDecision.
type HookBeforeHandler func(ctx context.Context, payload mcp.HookPayload) (mcp.BeforeDecision, error)

// HookCheckHandler handles a ctl/hook/check callback from the core layer.
type HookCheckHandler func(ctx context.Context, payload mcp.HookPayload) (mcp.CheckDecision, error)

// HookAfterHandler handles a ctl/hook/after callback from the core layer.
type HookAfterHandler func(ctx context.Context, payload mcp.HookPayload) (mcp.AfterDecision, error)

// ---------------------------------------------------------------------------
// HookConfig — tool-side hook configuration
// ---------------------------------------------------------------------------

// HookConfig holds handler functions for hooks.
type HookConfig struct {
	OnBefore HookBeforeHandler
	OnCheck  HookCheckHandler
	OnAfter  HookAfterHandler
}

// hookState 存储最近一次 SubscribeHooks 的参数，供断线重连后重放。
type hookState struct {
	mu             sync.Mutex
	subscriptionID string
	topics         []string
	scope          mcp.Selector
	filters        json.RawMessage
	mode           string
	replayState    string
	replayAttempts int
	lastReplayErr  string
}

// store 保存订阅参数，重置重放状态，供后续断线重连时使用。
func (hs *hookState) store(subID string, topics []string, scope mcp.Selector, filters json.RawMessage, mode string) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	hs.subscriptionID = subID
	hs.topics = shared.CloneStrings(topics)
	hs.scope = shared.CloneSelector(scope)
	hs.filters = shared.CloneRawMessage(filters)
	hs.mode = mode
	hs.replayState = ""
	hs.replayAttempts = 0
	hs.lastReplayErr = ""
}

// load 读取上次保存的订阅参数；topics 为空时返回 ok=false。
func (hs *hookState) load() (subID string, topics []string, scope mcp.Selector, filters json.RawMessage, mode string, ok bool) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	if len(hs.topics) == 0 {
		return "", nil, mcp.Selector{}, nil, "", false
	}
	return hs.subscriptionID, shared.CloneStrings(hs.topics), shared.CloneSelector(hs.scope), shared.CloneRawMessage(hs.filters), hs.mode, true
}

// markReplayFailure 记录重放失败的尝试次数和错误。
func (hs *hookState) markReplayFailure(attempts int, err error) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	hs.replayState = "failed"
	hs.replayAttempts = attempts
	if err != nil {
		hs.lastReplayErr = err.Error()
	} else {
		hs.lastReplayErr = ""
	}
}

// markReplayPending records that a live SubscribeHooks call failed
// and the desired state has been persisted so the reconnect path can
// retry it. Carries the initial failure so operators can surface it
// via the usual replay diagnostics without waiting for the first
// reconnect attempt.
func (hs *hookState) markReplayPending(err error) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	hs.replayState = "pending"
	hs.replayAttempts = 0
	if err != nil {
		hs.lastReplayErr = err.Error()
	} else {
		hs.lastReplayErr = ""
	}
}

// clearReplayFailure 清除重放失败状态，订阅成功后调用。
func (hs *hookState) clearReplayFailure() {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	hs.replayState = ""
	hs.replayAttempts = 0
	hs.lastReplayErr = ""
}

// ---------------------------------------------------------------------------
// Callback dispatch — invoked from dispatchRequest
// ---------------------------------------------------------------------------

// dispatchHookCallback routes a hook callback to the appropriate handler.
// It returns (response, handled, error). If handled is false the method was
// not a hook method and the caller should continue normal dispatch.
func (c *Client) dispatchHookCallback(ctx context.Context, req *jrpc2.Request) (any, bool, error) {
	switch req.Method() {
	case mcp.MethodHookBefore:
		return c.handleHookBefore(ctx, req)
	case mcp.MethodHookCheck:
		return c.handleHookCheck(ctx, req)
	case mcp.MethodHookAfter:
		return c.handleHookAfter(ctx, req)
	default:
		return nil, false, nil
	}
}

// handleHookBefore 处理 ctl/hook/before 回调，未注册时拒绝。
func (c *Client) handleHookBefore(ctx context.Context, req *jrpc2.Request) (any, bool, error) {
	handler := c.cfg.Hooks.OnBefore
	if handler == nil {
		return mcp.BeforeDecision{Decision: mcp.HookDecisionDeny}, true, nil
	}
	var payload mcp.HookPayload
	if err := req.UnmarshalParams(&payload); err != nil {
		return nil, true, err
	}
	payload.Context = shared.CloneRawMessage(payload.Context)
	dec, err := handler(ctx, payload)
	return dec, true, err
}

// handleHookCheck 处理 ctl/hook/check 回调，未注册时继续。
func (c *Client) handleHookCheck(ctx context.Context, req *jrpc2.Request) (any, bool, error) {
	handler := c.cfg.Hooks.OnCheck
	if handler == nil {
		return mcp.CheckDecision{Decision: mcp.HookDecisionContinue}, true, nil
	}
	var payload mcp.HookPayload
	if err := req.UnmarshalParams(&payload); err != nil {
		return nil, true, err
	}
	payload.Context = shared.CloneRawMessage(payload.Context)
	dec, err := handler(ctx, payload)
	return dec, true, err
}

// handleHookAfter 处理 ctl/hook/after 回调，未注册时拒绝。
func (c *Client) handleHookAfter(ctx context.Context, req *jrpc2.Request) (any, bool, error) {
	handler := c.cfg.Hooks.OnAfter
	if handler == nil {
		return mcp.AfterDecision{Decision: mcp.HookDecisionReject}, true, nil
	}
	var payload mcp.HookPayload
	if err := req.UnmarshalParams(&payload); err != nil {
		return nil, true, err
	}
	payload.Context = shared.CloneRawMessage(payload.Context)
	dec, err := handler(ctx, payload)
	return dec, true, err
}

// ---------------------------------------------------------------------------
// SubscribeHooks — outgoing RPC to core layer
// ---------------------------------------------------------------------------

// SubscribeHooks sends a ctl/hook/subscribe request to the core layer
// and freezes the desired-state contract for required topics (P22 P2
// bootstrap-S2 / plan §498 / §504):
//
//   - conn nil / degraded: persist the desired state so reconnect can
//     replay, then return errHookSubscribeUnavailable. Unchanged.
//   - live CallResult succeeds: persist on success. Unchanged.
//   - live CallResult fails (new): persist the desired state before
//     returning the error and mark the replay state as pending so the
//     reconnect path picks it up. Pre-P22 P2 bootstrap-S2 this path
//     dropped the subscription on the floor, so a first-call failure
//     required manual resubscribe to self-heal.
//
// SubscribeHooks 处理subscribehooks。
func (c *Client) SubscribeHooks(ctx context.Context, subscriptionID string, topics []string, scope mcp.Selector, filters json.RawMessage, mode string) (*mcp.HookSubscribeResponse, error) {
	conn, degraded := c.currentConn()
	if conn == nil || degraded {
		// Store for later replay even if we cannot send right now.
		c.hooks.store(subscriptionID, topics, scope, filters, mode)
		return nil, errHookSubscribeUnavailable()
	}

	req := mcp.HookSubscribeRequest{
		SubscriptionID: strings.TrimSpace(subscriptionID),
		Topics:         shared.CloneStrings(topics),
		Scope:          scope,
		Filters:        shared.CloneRawMessage(filters),
		Mode:           strings.TrimSpace(mode),
	}
	callCtx, cancel := withTimeoutIfNone(ctx, c.currentSendTimeout())
	defer cancel()

	var resp mcp.HookSubscribeResponse
	if err := conn.CallResult(callCtx, mcp.MethodHookSubscribe, req, &resp); err != nil {
		// Persist desired state so replayHookSubscriptions can retry
		// on the next successful reconnect. markReplayPending lets
		// diagnostics distinguish this path from a reconnect-time
		// retry failure.
		c.hooks.store(subscriptionID, topics, scope, filters, mode)
		c.hooks.markReplayPending(err)
		return nil, err
	}

	// Persist on success so reconnect can replay.
	c.hooks.store(subscriptionID, topics, scope, filters, mode)
	return &resp, nil
}

// ResolveHook sends a ctl/hook/resolve request to the core layer.
// ResolveHook 解析hook。
func (c *Client) ResolveHook(ctx context.Context, req mcp.HookResolveRequest) (*mcp.HookResolveResponse, error) {
	conn, degraded := c.currentConn()
	if conn == nil || degraded {
		return nil, errHookResolveUnavailable()
	}

	callCtx, cancel := withTimeoutIfNone(ctx, c.currentSendTimeout())
	defer cancel()

	var resp mcp.HookResolveResponse
	if err := conn.CallResult(callCtx, mcp.MethodHookResolve, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// PendingHooks fetches the current agent's pending hook reviews from the core layer.
// PendingHooks 处理待处理hooks。
func (c *Client) PendingHooks(ctx context.Context) ([]mcp.PendingHookReview, error) {
	conn, degraded := c.currentConn()
	if conn == nil || degraded {
		return nil, errHookPendingUnavailable()
	}
	// P22 P4 S5b / plan §316: the authoritative agent identity is
	// c.cfg.AgentID; c.boot.AgentID is a startup snapshot for
	// diagnostics only, not an identity source, and the legacy
	// FirstNonEmpty-style fallback between them has been removed. If
	// cfg.AgentID is empty we fail closed so a peer cannot silently
	// read pending reviews under a different identity.
	agentID := strings.TrimSpace(c.cfg.AgentID)
	if agentID == "" {
		return nil, errHookPendingAgentIDRequired()
	}

	callCtx, cancel := withTimeoutIfNone(ctx, c.currentSendTimeout())
	defer cancel()

	var resp mcp.HookPendingResponse
	if err := conn.CallResult(callCtx, mcp.MethodHookPending, mcp.HookPendingRequest{AgentID: agentID}, &resp); err != nil {
		return nil, err
	}
	if len(resp.Reviews) == 0 {
		return []mcp.PendingHookReview{}, nil
	}
	return append([]mcp.PendingHookReview(nil), resp.Reviews...), nil
}

// replayHookSubscriptions re-sends the last ctl/hook/subscribe after a
// successful reconnect. Safe to call when no prior subscription exists.
// replayHookSubscriptions 处理replayhooksubscriptions。
func (c *Client) replayHookSubscriptions(ctx context.Context) error {
	subID, topics, scope, filters, mode, ok := c.hooks.load()
	if !ok {
		return nil
	}
	// P22 P4 S6a / plan §321: stable log anchor for hook-replay start.
	// Ops dashboards group by `event` to count replay attempts without
	// pattern-matching free-text descriptions.
	pkglogger.Info("bootstrap hook replay begin",
		"event", "bootstrap.hook_replay.begin",
		"instance_id", c.instanceID,
		"subscription_id", subID,
		"lease_key", c.currentLease(),
	)

	req := mcp.HookSubscribeRequest{
		SubscriptionID: subID,
		Topics:         topics,
		Scope:          scope,
		Filters:        filters,
		Mode:           mode,
	}
	ctx = defaultContext(ctx)
	delay := time.Second
	var lastErr error
	attempts := 0
	for attempt := 1; attempt <= 3; attempt++ {
		attempts = attempt
		conn, degraded := c.currentConn()
		if conn == nil || degraded {
			lastErr = errHookSubscribeUnavailable()
		} else {
			callCtx, cancel := withTimeoutIfNone(ctx, c.currentSendTimeout())
			var resp mcp.HookSubscribeResponse
			lastErr = conn.CallResult(callCtx, mcp.MethodHookSubscribe, req, &resp)
			cancel()
			if lastErr == nil {
				c.hooks.clearReplayFailure()
				// P22 P4 S6a / plan §321: stable log anchor for
				// hook-replay successful completion.
				pkglogger.Info("bootstrap hook subscription replayed",
					"event", "bootstrap.hook_replay.end",
					"outcome", "success",
					"instance_id", c.instanceID,
					"subscription_id", subID,
					"lease_key", c.currentLease(),
					"attempts", attempt,
				)
				return nil
			}
		}
		if attempt == 3 || ctx.Err() != nil {
			break
		}
		pkglogger.Warn("bootstrap hook subscription replay failed; retrying",
			"instance_id", c.instanceID,
			"subscription_id", subID,
			"lease_key", c.currentLease(),
			"attempt", attempt,
			"retry_in", delay,
			"error", lastErr,
		)
		if !sleepContext(ctx, delay) {
			lastErr = ctx.Err()
			break
		}
		delay *= 2
	}
	c.hooks.markReplayFailure(attempts, lastErr)
	// P22 P4 S6a / plan §321: stable log anchor for hook-replay
	// terminal failure (paired with bootstrap.hook_replay.begin at
	// function entry).
	pkglogger.Error("bootstrap hook subscription replay failed",
		"event", "bootstrap.hook_replay.end",
		"outcome", "failed",
		"instance_id", c.instanceID,
		"subscription_id", subID,
		"lease_key", c.currentLease(),
		"attempts", attempts,
		"replay_state", "failed",
		"error", lastErr,
	)
	return lastErr
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func errHookSubscribeUnavailable() error {
	return &hookUnavailableError{msg: "bootstrap: hook subscribe unavailable (disconnected)"}
}

func errHookResolveUnavailable() error {
	return &hookUnavailableError{msg: "bootstrap: hook resolve unavailable (disconnected)"}
}

func errHookPendingUnavailable() error {
	return &hookUnavailableError{msg: "bootstrap: hook pending unavailable (disconnected)"}
}

func errHookPendingAgentIDRequired() error {
	return errors.New("bootstrap: hook pending requires agent_id")
}

// hookUnavailableError 标记 hook 连接不可用的非致命错误。
type hookUnavailableError struct{ msg string }

// Error 返回错误文本。
func (e *hookUnavailableError) Error() string { return e.msg }
