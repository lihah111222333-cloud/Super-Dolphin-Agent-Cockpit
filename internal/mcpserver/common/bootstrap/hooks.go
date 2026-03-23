package bootstrap

import (
	"context"
	"log"
	"strings"
	"sync"

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

// hookState stores the last subscribe parameters so reconnect can replay them.
type hookState struct {
	mu             sync.Mutex
	subscriptionID string
	topics         []string
	scope          mcp.Selector
	mode           string
}

func (hs *hookState) store(subID string, topics []string, scope mcp.Selector, mode string) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	hs.subscriptionID = subID
	hs.topics = cloneStrings(topics)
	hs.scope = cloneSelector(scope)
	hs.mode = mode
}

func (hs *hookState) load() (subID string, topics []string, scope mcp.Selector, mode string, ok bool) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	if len(hs.topics) == 0 {
		return "", nil, mcp.Selector{}, "", false
	}
	return hs.subscriptionID, cloneStrings(hs.topics), cloneSelector(hs.scope), hs.mode, true
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
func (c *Client) SubscribeHooks(ctx context.Context, subscriptionID string, topics []string, scope mcp.Selector, mode string) (*mcp.HookSubscribeResponse, error) {
	conn, degraded := c.currentConn()
	if conn == nil || degraded {
		// Store for later replay even if we cannot send right now.
		c.hooks.store(subscriptionID, topics, scope, mode)
		return nil, errHookSubscribeUnavailable()
	}

	req := mcp.HookSubscribeRequest{
		SubscriptionID: strings.TrimSpace(subscriptionID),
		Topics:         cloneStrings(topics),
		Scope:          scope,
		Mode:           strings.TrimSpace(mode),
	}
	callCtx, cancel := withTimeoutIfNone(ctx, c.currentSendTimeout())
	defer cancel()

	var resp mcp.HookSubscribeResponse
	if err := conn.CallResult(callCtx, mcp.MethodHookSubscribe, req, &resp); err != nil {
		return nil, err
	}

	// Persist on success so reconnect can replay.
	c.hooks.store(subscriptionID, topics, scope, mode)
	return &resp, nil
}

// replayHookSubscriptions re-sends the last ctl/hook/subscribe after a
// successful reconnect. Safe to call when no prior subscription exists.
func (c *Client) replayHookSubscriptions(ctx context.Context) {
	subID, topics, scope, mode, ok := c.hooks.load()
	if !ok {
		return
	}
	conn, _ := c.currentConn()
	if conn == nil {
		return
	}

	req := mcp.HookSubscribeRequest{
		SubscriptionID: subID,
		Topics:         topics,
		Scope:          scope,
		Mode:           mode,
	}
	callCtx, cancel := withTimeoutIfNone(ctx, c.currentSendTimeout())
	defer cancel()

	var resp mcp.HookSubscribeResponse
	if err := conn.CallResult(callCtx, mcp.MethodHookSubscribe, req, &resp); err != nil {
		log.Printf("bootstrap hook subscribe replay failed: instance=%s err=%v", c.instanceID, err)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func errHookSubscribeUnavailable() error {
	return &hookUnavailableError{msg: "bootstrap: hook subscribe unavailable (disconnected)"}
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
