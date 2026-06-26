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
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// beginStart 在锁内初始化 rootCtx 和 stop，防止重复 Start。
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

// connectAndRegister 建立 TCP 连接并完成 register 握手，任一步失败时关闭连接。
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

// dial 建立到控制平面的 TCP 连接并返回 jrpc2.Client。
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

// registerConn 在已有连接上执行 register RPC，校验响应并返回规范化结果。
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

// handleNotify 分发服务端推送的通知消息。
func (c *Client) handleNotify(req *jrpc2.Request) {
	if err := c.dispatchRequest(req); err != nil {
		pkglogger.Warn("bootstrap notify dispatch failed",
			"instance_id", c.instanceID,
			"callback_method", req.Method(),
			"error", err,
		)
	}
}

// handleCallback 分发控制平面主动发起的 callback。
// 未显式注册的 callback 一律返回 MethodNotFound，避免 peer 拼写错误或新方法被静默 ACK。
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

// dispatchToolCallback 路由 tools/list 和 tools/call 回调；未注册 handler 时 fail-closed。
func (c *Client) dispatchToolCallback(ctx context.Context, req *jrpc2.Request) (any, bool, error) {
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

// dispatchLSPAdminCallback 路由 LSP releaseScope 回调，未注册时返回错误。
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

// dispatchRequest 是通知路径的入口，分发生命周期请求；未知方法只记日志不返回错误。
func (c *Client) dispatchRequest(req *jrpc2.Request) error {
	_, err := c.dispatchLifecycleRequest(req)
	return err
}

// dispatchLifecycleRequest 路由 shutdown/config_changed 方法，返回是否已处理及错误。
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

// errBootstrapUnknownMethod 返回服务端回调未知方法的线协议错误。
// 使用 contract.CodeMethodNotFound，确保 peer typo 或未接入的新方法不会被静默 ACK。
func errBootstrapUnknownMethod(method string) error {
	return jrpc2.Errorf(jrpc2.Code(contract.CodeMethodNotFound), "bootstrap: unknown callback method: %s", strings.TrimSpace(method))
}

// fireShutdown 在独立 goroutine 中调用 OnShutdown 回调，受 callbackWG 追踪。
func (c *Client) fireShutdown(req mcp.ShutdownRequest) {
	if c.cfg.OnShutdown == nil {
		return
	}
	c.spawnCallback(func() { c.cfg.OnShutdown(req) })
}

// fireConfigChanged 在独立 goroutine 中调用 OnConfigChanged 回调，受 callbackWG 追踪。
func (c *Client) fireConfigChanged(notify mcp.ConfigChangedNotify) {
	if c.cfg.OnConfigChanged == nil {
		return
	}
	notify.Payload = shared.CloneRawMessage(notify.Payload)
	c.spawnCallback(func() { c.cfg.OnConfigChanged(notify) })
}

// spawnCallback 启动应用侧生命周期回调，并把 goroutine 纳入 Client 的 WaitGroup 管理。
// 如果与 Close() 竞争时发现 client 已关闭，则不再启动新 goroutine，避免无限延长 drain。
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

// watchRoot 等待 rootCtx 取消后自动调用 Close，确保上下文退出时客户端被清理。
func (c *Client) watchRoot(ctx context.Context) {
	<-ctx.Done()
	_ = c.Close()
}

// activateLocked 在锁内设置新连接和租约并重启心跳，reconnect 和首次 Start 共用此路径。
func (c *Client) activateLocked(conn *jrpc2.Client, reg *mcp.RegisterResponse) {
	c.conn = conn
	c.reconnecting = false
	c.closed = false
	c.applyRegisterLocked(reg)
	c.startHeartbeatLocked()
}

// applyRegisterLocked 将注册响应中的 lease、config 和心跳参数写入 Client 字段（需持有写锁）。
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

// currentConn 在读锁下返回当前 jrpc2 连接和重连标志。
func (c *Client) currentConn() (*jrpc2.Client, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.conn, c.reconnecting
}

// currentLease 在读锁下返回当前 LeaseKey。
func (c *Client) currentLease() mcp.LeaseKey {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lease
}

// callTarget 在读锁下同时返回连接和 LeaseKey，供心跳等需要原子读取两者的路径使用。
func (c *Client) callTarget() (*jrpc2.Client, mcp.LeaseKey) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.conn, c.lease
}

// currentSendTimeout 在读锁下返回当前 RPC 超时，非正值时回退到默认值。
func (c *Client) currentSendTimeout() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.sendTimeout <= 0 {
		return defaultRPCTimeout
	}
	return c.sendTimeout
}

// currentResumeGeneration 在读锁下返回 resumeGeneration，用于断线重连时携带上一代。
func (c *Client) currentResumeGeneration() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.resumeGeneration
}

// offeredCapabilities 返回实际对外声明的能力列表，优先使用 CapabilitiesOffered。
func (c *Client) offeredCapabilities() []string {
	if len(c.cfg.CapabilitiesOffered) != 0 {
		return c.cfg.CapabilitiesOffered
	}
	return c.cfg.Capabilities
}

// nextHeartbeatSeq 在写锁下自增并返回心跳序列号，防止乱序响应。
func (c *Client) nextHeartbeatSeq() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.heartbeatSeq++
	return c.heartbeatSeq
}

// nextLogSeq 在写锁下自增并返回日志序列号，保证日志有序。
func (c *Client) nextLogSeq() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logSeq++
	return c.logSeq
}

// auditEventFallback 在事件发送失败时将事件写入本地日志作为审计兜底。
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
