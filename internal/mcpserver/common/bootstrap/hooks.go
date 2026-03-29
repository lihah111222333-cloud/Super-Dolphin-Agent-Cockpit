package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/creachadair/jrpc2"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"

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

// hookState stores the last subscribe parameters so reconnect can replay them.
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

func (hs *hookState) store(subID string, topics []string, scope mcp.Selector, filters json.RawMessage, mode string) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	hs.subscriptionID = subID
	hs.topics = cloneStrings(topics)
	hs.scope = cloneSelector(scope)
	hs.filters = cloneRaw(filters)
	hs.mode = mode
	hs.replayState = ""
	hs.replayAttempts = 0
	hs.lastReplayErr = ""
}

func (hs *hookState) load() (subID string, topics []string, scope mcp.Selector, filters json.RawMessage, mode string, ok bool) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	if len(hs.topics) == 0 {
		return "", nil, mcp.Selector{}, nil, "", false
	}
	return hs.subscriptionID, cloneStrings(hs.topics), cloneSelector(hs.scope), cloneRaw(hs.filters), hs.mode, true
}

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

func (c *Client) handleHookBefore(ctx context.Context, req *jrpc2.Request) (any, bool, error) {
	handler := c.cfg.Hooks.OnBefore
	if handler == nil {
		return mcp.BeforeDecision{Decision: mcp.HookDecisionDeny}, true, nil
	}
	var payload mcp.HookPayload
	if err := req.UnmarshalParams(&payload); err != nil {
		return nil, true, err
	}
	payload.Context = cloneRaw(payload.Context)
	dec, err := handler(ctx, payload)
	return dec, true, err
}

func (c *Client) handleHookCheck(ctx context.Context, req *jrpc2.Request) (any, bool, error) {
	handler := c.cfg.Hooks.OnCheck
	if handler == nil {
		return mcp.CheckDecision{Decision: mcp.HookDecisionContinue}, true, nil
	}
	var payload mcp.HookPayload
	if err := req.UnmarshalParams(&payload); err != nil {
		return nil, true, err
	}
	payload.Context = cloneRaw(payload.Context)
	dec, err := handler(ctx, payload)
	return dec, true, err
}

func (c *Client) handleHookAfter(ctx context.Context, req *jrpc2.Request) (any, bool, error) {
	handler := c.cfg.Hooks.OnAfter
	if handler == nil {
		return mcp.AfterDecision{Decision: mcp.HookDecisionReject}, true, nil
	}
	var payload mcp.HookPayload
	if err := req.UnmarshalParams(&payload); err != nil {
		return nil, true, err
	}
	payload.Context = cloneRaw(payload.Context)
	dec, err := handler(ctx, payload)
	return dec, true, err
}

// ---------------------------------------------------------------------------
// SubscribeHooks — outgoing RPC to core layer
// ---------------------------------------------------------------------------

// SubscribeHooks sends a ctl/hook/subscribe request to the core layer.
// It stores the parameters so they can be replayed on reconnect.
func (c *Client) SubscribeHooks(ctx context.Context, subscriptionID string, topics []string, scope mcp.Selector, filters json.RawMessage, mode string) (*mcp.HookSubscribeResponse, error) {
	conn, degraded := c.currentConn()
	if conn == nil || degraded {
		// Store for later replay even if we cannot send right now.
		c.hooks.store(subscriptionID, topics, scope, filters, mode)
		return nil, errHookSubscribeUnavailable()
	}

	req := mcp.HookSubscribeRequest{
		SubscriptionID: strings.TrimSpace(subscriptionID),
		Topics:         cloneStrings(topics),
		Scope:          scope,
		Filters:        cloneRaw(filters),
		Mode:           strings.TrimSpace(mode),
	}
	callCtx, cancel := withTimeoutIfNone(ctx, c.currentSendTimeout())
	defer cancel()

	var resp mcp.HookSubscribeResponse
	if err := conn.CallResult(callCtx, mcp.MethodHookSubscribe, req, &resp); err != nil {
		return nil, err
	}

	// Persist on success so reconnect can replay.
	c.hooks.store(subscriptionID, topics, scope, filters, mode)
	return &resp, nil
}

// ResolveHook sends a ctl/hook/resolve request to the core layer.
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
func (c *Client) PendingHooks(ctx context.Context) ([]mcp.PendingHookReview, error) {
	conn, degraded := c.currentConn()
	if conn == nil || degraded {
		return nil, errHookPendingUnavailable()
	}
	agentID := strings.TrimSpace(firstNonEmpty(c.cfg.AgentID, c.boot.AgentID))
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
func (c *Client) replayHookSubscriptions(ctx context.Context) error {
	subID, topics, scope, filters, mode, ok := c.hooks.load()
	if !ok {
		return nil
	}

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
				pkglogger.Info("bootstrap hook subscription replayed",
					"instance_id", c.instanceID,
					"subscription_id", subID,
					"lease_key", c.currentLease(),
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
	pkglogger.Error("bootstrap hook subscription replay failed",
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

// hookUnavailableError marks a non-fatal connectivity error for hook subscribe.
type hookUnavailableError struct{ msg string }

func (e *hookUnavailableError) Error() string { return e.msg }

func cloneSelector(s mcp.Selector) mcp.Selector {
	out := s
	if s.Scope != nil {
		cp := *s.Scope
		out.Scope = &cp
	}
	return out
}
