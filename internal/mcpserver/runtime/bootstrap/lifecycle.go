package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"strings"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	shared "github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

func (c *Client) beginStart(rootCtx context.Context, cancel context.CancelFunc) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rootCtx != nil {
		return errors.New("bootstrap: client already started")
	}
	c.closed = false
	c.rootCtx = rootCtx
	c.stop = cancel
	return nil
}

func (c *Client) connectAndRegister(ctx context.Context) (*jrpc2.Client, *mcp.RegisterResponse, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, nil, err
	}
	reg, err := c.registerConn(ctx, conn)
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	return conn, reg, nil
}

func (c *Client) dial(ctx context.Context) (*jrpc2.Client, error) {
	var d net.Dialer
	raw, err := d.DialContext(ctx, "tcp", c.cfg.RPCAddr)
	if err != nil {
		return nil, err
	}
	return jrpc2.NewClient(channel.Line(raw, raw), &jrpc2.ClientOptions{
		OnNotify:   c.handleNotify,
		OnCallback: c.handleCallback,
		OnStop:     c.handleStop,
	}), nil
}

func (c *Client) registerConn(ctx context.Context, conn *jrpc2.Client) (*mcp.RegisterResponse, error) {
	if conn == nil {
		return nil, errors.New("bootstrap: nil rpc client")
	}
	req := mcp.RegisterRequest{
		InstanceID:           c.instanceID,
		BinaryName:           c.cfg.BinaryName,
		AgentID:              "",
		ThreadID:             c.cfg.ThreadID,
		PID:                  os.Getpid(),
		SessionToken:         c.cfg.SessionToken,
		BootID:               c.cfg.BootID,
		ClientKind:           c.cfg.ClientKind,
		PeerKind:             mcp.PeerKindTool,
		CapabilitiesOffered:  shared.CloneStrings(c.offeredCapabilities()),
		CapabilitiesRequired: shared.CloneStrings(c.cfg.CapabilitiesRequired),
		Subscriptions:        shared.CloneStrings(c.cfg.Subscriptions),
	}
	if resume := c.currentResumeGeneration(); resume != 0 {
		req.ResumeFromGeneration = &resume
	}
	callCtx, cancel := withTimeoutIfNone(ctx, defaultRPCTimeout)
	defer cancel()
	var resp mcp.RegisterResponse
	if err := conn.CallResult(callCtx, mcp.MethodRegister, req, &resp); err != nil {
		return nil, err
	}
	return normalizeRegisterResponse(&resp, c.instanceID)
}

func (c *Client) handleNotify(req *jrpc2.Request) {
	if err := c.dispatchRequest(req); err != nil {
		pkglogger.Warn("bootstrap notify dispatch failed",
			"instance_id", c.instanceID,
			"callback_method", req.Method(),
			"error", err,
		)
	}
}

// handleCallback dispatches server-initiated callbacks to the
// appropriate registered handler. P22 P4 S5b / plan §315: the
// previous trailing `return map[string]bool{"ok": true}, nil`
// silently ACK'd any unknown method, meaning a peer typo or a new
// control-plane method we did not opt into would look like a
// success. That contract is now fail-closed: dispatchRequest owns
// shutdown/config_changed, and handleCallback returns a JSON-RPC
// MethodNotFound error for anything else unless a handler is
// explicitly registered.
// handleCallback 处理callback。
func (c *Client) handleCallback(ctx context.Context, req *jrpc2.Request) (any, error) {
	if resp, handled, err := c.dispatchToolCallback(ctx, req); handled {
		return resp, err
	}
	if resp, handled, err := c.dispatchLSPAdminCallback(ctx, req); handled {
		return resp, err
	}
	if resp, handled, err := c.dispatchHookCallback(ctx, req); handled {
		return resp, err
	}
	handled, err := c.dispatchLifecycleRequest(req)
	if err != nil {
		return nil, err
	}
	if !handled {
		return nil, errBootstrapUnknownMethod(req.Method())
	}
	return map[string]bool{"ok": true}, nil
}

func (c *Client) dispatchToolCallback(ctx context.Context, req *jrpc2.Request) (any, bool, error) {
	// P15: route tools/list and tools/call to registered handlers.
	switch req.Method() {
	case "tools/list":
		if c.cfg.OnToolsList == nil {
			return nil, true, errBootstrapUnknownMethod(req.Method())
		}
		resp, err := c.cfg.OnToolsList(ctx)
		return resp, true, err
	case "tools/call":
		if c.cfg.OnToolsCall == nil {
			return nil, true, errBootstrapUnknownMethod(req.Method())
		}
		resp, err := c.cfg.OnToolsCall(ctx, json.RawMessage(req.ParamString()))
		return resp, true, err
	default:
		return nil, false, nil
	}
}

func (c *Client) dispatchLSPAdminCallback(ctx context.Context, req *jrpc2.Request) (any, bool, error) {
	if req.Method() != mcp.MethodLSPReleaseScope {
		return nil, false, nil
	}
	if c.cfg.OnLSPReleaseScope == nil {
		return nil, true, errBootstrapUnknownMethod(req.Method())
	}
	var payload mcp.LSPReleaseScopeRequest
	if err := req.UnmarshalParams(&payload); err != nil {
		return nil, true, err
	}
	resp, err := c.cfg.OnLSPReleaseScope(ctx, payload)
	return resp, true, err
}

// dispatchRequest is the notification-path entry point
// (handleNotify). Notifications have no response, so an unknown
// method only warrants a warning log at the handleNotify caller
// rather than the fail-closed error surface used for requests.
func (c *Client) dispatchRequest(req *jrpc2.Request) error {
	_, err := c.dispatchLifecycleRequest(req)
	return err
}

// dispatchLifecycleRequest routes the bootstrap lifecycle methods
// (shutdown / config_changed) and reports whether the method was
// recognised. Callers that require fail-closed semantics (e.g.
// handleCallback on the request path) treat !handled as an unknown
// method error; handleNotify, which has no response surface, just
// logs and moves on.
func (c *Client) dispatchLifecycleRequest(req *jrpc2.Request) (handled bool, err error) {
	switch req.Method() {
	case mcp.MethodShutdown:
		var payload mcp.ShutdownRequest
		if err := req.UnmarshalParams(&payload); err != nil {
			return true, err
		}
		c.fireShutdown(payload)
		return true, nil
	case mcp.MethodConfigChanged:
		var payload mcp.ConfigChangedNotify
		if err := req.UnmarshalParams(&payload); err != nil {
			return true, err
		}
		c.fireConfigChanged(payload)
		return true, nil
	}
	return false, nil
}

// errBootstrapUnknownMethod is the wire error surfaced when a
// server-initiated callback uses a method this client has not opted
// into. Uses contract.CodeMethodNotFound (-31008).
// See P22 P4 S5b / plan §315.
func errBootstrapUnknownMethod(method string) error {
	return jrpc2.Errorf(jrpc2.Code(contract.CodeMethodNotFound), "bootstrap: unknown callback method: %s", strings.TrimSpace(method))
}

func (c *Client) fireShutdown(req mcp.ShutdownRequest) {
	if c.cfg.OnShutdown == nil {
		return
	}
	c.spawnCallback(func() { c.cfg.OnShutdown(req) })
}

func (c *Client) fireConfigChanged(notify mcp.ConfigChangedNotify) {
	if c.cfg.OnConfigChanged == nil {
		return
	}
	notify.Payload = shared.CloneRawMessage(notify.Payload)
	c.spawnCallback(func() { c.cfg.OnConfigChanged(notify) })
}

// spawnCallback launches an application-supplied callback (OnShutdown
// or OnConfigChanged) while keeping its lifecycle owned by the
// bootstrap client. Pre-P22 P2 bootstrap-S1 these were fire-and-forget
// `go callback(...)` statements; the wait group + closed-check replace
// that with a bounded drain contract honoured by Close() via
// drainCallbacks. A spawn that races past Close() returns without
// launching a goroutine so the drain is not extended indefinitely.
func (c *Client) spawnCallback(fn func()) {
	if fn == nil {
		return
	}
	c.mu.RLock()
	closed := c.closed
	c.mu.RUnlock()
	if closed {
		return
	}
	c.callbackWG.Add(1)
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				pkglogger.Error("bootstrap: recovered callback panic",
					"instance_id", c.instanceID, "panic", rec)
			}
		}()
		defer c.callbackWG.Done()
		fn()
	}()
}

func (c *Client) watchRoot(ctx context.Context) {
	<-ctx.Done()
	_ = c.Close()
}

func (c *Client) activateLocked(conn *jrpc2.Client, reg *mcp.RegisterResponse) {
	c.conn = conn
	c.reconnecting = false
	c.closed = false
	c.applyRegisterLocked(reg)
	c.startHeartbeatLocked()
}

func (c *Client) applyRegisterLocked(reg *mcp.RegisterResponse) {
	if reg == nil {
		return
	}
	c.lease = mcp.LeaseKey{InstanceID: reg.InstanceID, Generation: reg.Generation}
	c.resumeGeneration = reg.Generation
	c.configVersion = reg.ConfigVersion
	c.serverProtocolVersion = strings.TrimSpace(reg.ServerProtocolVersion)
	c.capabilitiesNegotiated = shared.CloneStrings(reg.CapabilitiesNegotiated)
	c.capabilitiesRejected = shared.CloneStrings(reg.CapabilitiesRejected)
	c.heartbeatInterval = durationOrDefault(reg.HeartbeatIntervalMs, defaultHeartbeatInterval)
	c.heartbeatTimeout = durationOrDefault(reg.HeartbeatTimeoutMs, defaultHeartbeatTimeout)
	c.sendTimeout = durationOrDefault(reg.SendTimeoutMs, defaultRPCTimeout)
	c.sweeperInterval = durationOrDefault(reg.SweeperIntervalMs, c.heartbeatInterval)
	if c.heartbeatTimeout >= c.heartbeatInterval {
		c.heartbeatTimeout = maxDuration(time.Second, c.heartbeatInterval/2)
	}
	if c.sendTimeout >= c.heartbeatInterval {
		c.sendTimeout = maxDuration(time.Second, c.heartbeatInterval/2)
	}
}

func (c *Client) currentConn() (*jrpc2.Client, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.conn, c.reconnecting
}

func (c *Client) currentLease() mcp.LeaseKey {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lease
}

func (c *Client) callTarget() (*jrpc2.Client, mcp.LeaseKey) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.conn, c.lease
}

func (c *Client) currentSendTimeout() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.sendTimeout <= 0 {
		return defaultRPCTimeout
	}
	return c.sendTimeout
}

func (c *Client) currentResumeGeneration() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.resumeGeneration
}

func (c *Client) offeredCapabilities() []string {
	if len(c.cfg.CapabilitiesOffered) != 0 {
		return c.cfg.CapabilitiesOffered
	}
	return c.cfg.Capabilities
}

func (c *Client) nextHeartbeatSeq() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.heartbeatSeq++
	return c.heartbeatSeq
}

func (c *Client) nextLogSeq() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logSeq++
	return c.logSeq
}

func (c *Client) auditEventFallback(eventType string, payload json.RawMessage, sendErr error) {
	level := pkglogger.LevelInfo
	if sendErr != nil {
		level = pkglogger.LevelWarn
	}
	pkglogger.Get().Log(context.Background(), level, "bootstrap audit fallback",
		"instance_id", c.instanceID,
		"callback_method", mcp.MethodEvent,
		"event_type", strings.TrimSpace(eventType),
		"payload", string(payload),
		"error", sendErr,
	)
}
