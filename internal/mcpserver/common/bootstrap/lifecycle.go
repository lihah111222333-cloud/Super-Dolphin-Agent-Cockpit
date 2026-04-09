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

	"github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
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

func (c *Client) handleCallback(ctx context.Context, req *jrpc2.Request) (any, error) {
	// P15: route tools/list and tools/call to registered handlers.
	switch req.Method() {
	case "tools/list":
		if c.cfg.OnToolsList != nil {
			return c.cfg.OnToolsList(ctx)
		}
	case "tools/call":
		if c.cfg.OnToolsCall != nil {
			return c.cfg.OnToolsCall(ctx, json.RawMessage(req.ParamString()))
		}
	}
	if resp, handled, err := c.dispatchHookCallback(ctx, req); handled {
		return resp, err
	}
	if err := c.dispatchRequest(req); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

func (c *Client) dispatchRequest(req *jrpc2.Request) error {
	switch req.Method() {
	case mcp.MethodShutdown:
		var payload mcp.ShutdownRequest
		if err := req.UnmarshalParams(&payload); err != nil {
			return err
		}
		c.fireShutdown(payload)
	case mcp.MethodConfigChanged:
		var payload mcp.ConfigChangedNotify
		if err := req.UnmarshalParams(&payload); err != nil {
			return err
		}
		c.fireConfigChanged(payload)
	}
	return nil
}

func (c *Client) fireShutdown(req mcp.ShutdownRequest) {
	if c.cfg.OnShutdown == nil {
		return
	}
	go c.cfg.OnShutdown(req)
}

func (c *Client) fireConfigChanged(notify mcp.ConfigChangedNotify) {
	if c.cfg.OnConfigChanged == nil {
		return
	}
	notify.Payload = shared.CloneRawMessage(notify.Payload)
	go c.cfg.OnConfigChanged(notify)
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
	c.lease = reg.Lease
	c.resumeGeneration = reg.Lease.Generation
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

func (c *Client) localLogFallback(entry mcp.LogNotify, sendErr error) {
	level := pkglogger.LevelInfo
	if sendErr != nil {
		level = pkglogger.LevelWarn
	}
	pkglogger.Get().Log(context.Background(), level, "bootstrap local log fallback",
		"instance_id", c.instanceID,
		"callback_method", mcp.MethodLog,
		"level", entry.Level,
		"message", entry.Message,
		"fields", entry.Fields,
		"error", sendErr,
	)
}
