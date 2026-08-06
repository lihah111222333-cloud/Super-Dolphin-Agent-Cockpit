// Package bootstrap 提供 MCP peer 进程向控制平面注册、心跳、日志中继和生命周期管理的客户端能力。
package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/creachadair/jrpc2"
	platformmetrics "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/metrics"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
)

// 默认时序与队列容量，控制心跳、RPC 超时、报告排队和回调 drain 的上限。
const (
	defaultHeartbeatInterval = 10 * time.Second
	defaultHeartbeatTimeout  = 5 * time.Second
	defaultRPCTimeout        = 5 * time.Second
	defaultReportQueueLimit  = 128
	// defaultCallbackDrainTimeout 限制 Close() 等待 OnShutdown/OnConfigChanged 回调退出的时间。
	// 必须低于整体退出预算，避免应用层回调卡死 bootstrap shutdown。
	defaultCallbackDrainTimeout = 2 * time.Second
)

// Client 是 MCP peer 进程的控制平面客户端，负责注册、心跳、报告队列和回调分发。
type Client struct {
	instanceID string
	lease      mcp.LeaseKey
	conn       *jrpc2.Client
	hbCancel   context.CancelFunc

	mu sync.RWMutex

	cfg                    Config
	rootCtx                context.Context
	stop                   context.CancelFunc
	reconnecting           bool
	closed                 bool
	heartbeatInterval      time.Duration
	heartbeatTimeout       time.Duration
	sendTimeout            time.Duration
	sweeperInterval        time.Duration
	configVersion          int64
	serverProtocolVersion  string
	managedToken           string
	managedRequestID       string
	capabilitiesNegotiated []string
	capabilitiesRejected   []string
	resumeGeneration       uint64
	heartbeatSeq           uint64
	logSeq                 uint64
	reportQueue            []mcp.ReportRequest
	reportQueueLimit       int
	boot                   bootSnapshot
	hooks                  hookState

	// callbackWG 跟踪 OnShutdown/OnConfigChanged 回调 goroutine。
	// 生命周期回调必须由 Client 拥有；Close() 通过 drainCallbacks 做有界等待，避免回调泄漏到 client 关闭之后。
	callbackWG sync.WaitGroup
	metrics    *platformmetrics.BootstrapMetrics
}

// Config 是创建 Client 时必须提供的静态配置。
type Config struct {
	RPCAddr                string
	InstanceID             string
	BootID                 string
	BinaryName             string
	ClientKind             string
	AgentID                string // optional; empty means this MCP process is a shared service
	ThreadID               string
	SessionToken           string
	ManagedToken           string
	ManagedProtocolVersion string
	Capabilities           []string
	CapabilitiesOffered    []string
	CapabilitiesRequired   []string
	Subscriptions          []string
	BootSnapshot           json.RawMessage
	ReportQueueLimit       int
	Metrics                *platformmetrics.BootstrapMetrics
	FinalReport            func() *mcp.ReportRequest
	OnShutdown             func(mcp.ShutdownRequest)
	OnConfigChanged        func(mcp.ConfigChangedNotify)
	OnLSPReleaseScope      func(context.Context, mcp.LSPReleaseScopeRequest) (mcp.LSPReleaseScopeResult, error)
	Hooks                  HookConfig
	OnToolsList            func(context.Context) (any, error)                             // tools/list 回调入口
	OnToolsCall            func(ctx context.Context, params json.RawMessage) (any, error) // tools/call 回调入口
}

// New 创建控制平面 Client，并在构造阶段严格校验和归一化配置。
func New(cfg Config) (*Client, error) {
	if cfg.Metrics == nil {
		return nil, errors.New("bootstrap: metrics owner is required")
	}
	cfg, boot, err := normalizeConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &Client{
		instanceID:        cfg.InstanceID,
		cfg:               cfg,
		heartbeatInterval: defaultHeartbeatInterval,
		heartbeatTimeout:  defaultHeartbeatTimeout,
		sendTimeout:       defaultRPCTimeout,
		reportQueueLimit:  shared.ClampLimit(cfg.ReportQueueLimit, 1, 0, defaultReportQueueLimit),
		boot:              boot,
		managedToken:      cfg.ManagedToken,
		metrics:           cfg.Metrics,
	}, nil
}

// Start 连接控制平面、完成 register，并启动心跳和离线报告 flush 流程。
func (c *Client) Start(ctx context.Context) error {
	if strings.TrimSpace(c.cfg.RPCAddr) == "" {
		pkglogger.Warn("bootstrap start skipped: GO_AGENT_CTL_RPC_ADDR missing",
			"binary_name", c.cfg.BinaryName,
			"instance_id", c.instanceID,
			"thread_id", c.cfg.ThreadID,
			"capabilities_offered", c.offeredCapabilities(),
		)
		return errors.New("bootstrap: GO_AGENT_CTL_RPC_ADDR is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	rootCtx, cancel := context.WithCancel(ctx)
	if err := c.beginStart(rootCtx, cancel); err != nil {
		cancel()
		return err
	}
	conn, reg, err := c.connectAndRegister(rootCtx)
	if err != nil {
		pkglogger.Warn("bootstrap connect/register failed",
			"binary_name", c.cfg.BinaryName,
			"instance_id", c.instanceID,
			"rpc_addr", c.cfg.RPCAddr,
			"thread_id", c.cfg.ThreadID,
			"error", err,
		)
		_ = c.Close()
		return err
	}
	c.mu.Lock()
	c.activateLocked(conn, reg)
	c.mu.Unlock()
	pkglogger.Info("bootstrap registered",
		"binary_name", c.cfg.BinaryName,
		"instance_id", c.instanceID,
		"rpc_addr", c.cfg.RPCAddr,
		"thread_id", c.cfg.ThreadID,
		"lease_generation", reg.Generation,
		"capabilities_negotiated", reg.CapabilitiesNegotiated,
		"subscriptions", c.cfg.Subscriptions,
		"config_version", reg.ConfigVersion,
	)
	var rootWG sync.WaitGroup
	rootWG.Go(func() { c.watchRoot(rootCtx) })
	c.flushQueuedReports(context.Background())
	return nil
}

// Context 优先通过控制平面读取上下文；传输断开时退回 bootSnapshot 快照。
func (c *Client) Context(ctx context.Context, scope string, keys []string) (*mcp.ContextResponse, error) {
	conn, degraded := c.currentConn()
	if conn == nil || degraded {
		return c.envContext(scope, keys)
	}
	lease := c.currentLease()
	req := mcp.ContextRequest{
		InstanceID: lease.InstanceID,
		Generation: lease.Generation,
		Scope:      strings.TrimSpace(scope),
		Keys:       shared.CloneStrings(keys),
	}
	if agentID := strings.TrimSpace(c.cfg.AgentID); agentID != "" {
		req.AgentID = agentID
	}
	callCtx, cancel := withTimeoutIfNone(ctx, c.currentSendTimeout())
	defer cancel()
	var resp mcp.ContextResponse
	if err := conn.CallResult(callCtx, mcp.MethodContext, req, &resp); err != nil {
		if isTransportErr(err) {
			return c.envContext(scope, keys)
		}
		return nil, err
	}
	return normalizeContextResponse(scope, &resp), nil
}

// EmitEvent 向控制平面发送生命周期事件；传输断开时写本地审计 fallback。
func (c *Client) EmitEvent(ctx context.Context, eventType string, payload any) error {
	raw, err := marshalRaw(payload)
	if err != nil {
		return err
	}
	conn, degraded := c.currentConn()
	if conn == nil || degraded {
		c.auditEventFallback(eventType, raw, nil)
		return nil
	}
	lease := c.currentLease()
	req := mcp.EventNotify{
		InstanceID: lease.InstanceID,
		Generation: lease.Generation,
		EventID:    generateID("ctl_event"),
		EventType:  strings.TrimSpace(eventType),
		AuditClass: "tool.lifecycle",
		Payload:    raw,
	}
	noteCtx, cancel := withTimeoutIfNone(ctx, c.currentSendTimeout())
	defer cancel()
	if err := conn.Notify(noteCtx, mcp.MethodEvent, req); err != nil {
		if isTransportErr(err) {
			c.auditEventFallback(eventType, raw, err)
			return fmt.Errorf("emit event notify failed after audit fallback: %w", err)
		}
		return err
	}
	return nil
}

// RequestApproval 发送审批请求，live RPC 不可用时返回明确的 approval unavailable 错误。
func (c *Client) RequestApproval(ctx context.Context, req mcp.ApprovalRequest) (*mcp.ApprovalResponse, error) {
	conn, degraded := c.currentConn()
	if conn == nil || degraded {
		return nil, approvalUnavailableErr("live lifecycle RPC is unavailable")
	}
	lease := c.currentLease()
	req.InstanceID = lease.InstanceID
	req.Generation = lease.Generation
	var resp mcp.ApprovalResponse
	if err := conn.CallResult(defaultContext(ctx), mcp.MethodApproval, req, &resp); err != nil {
		if isTransportErr(err) {
			return nil, approvalUnavailableErr("live lifecycle RPC disconnected during approval")
		}
		return nil, err
	}
	resp.Detail = shared.CloneRawMessage(resp.Detail)
	return &resp, nil
}

// Report 发送运行报告；传输断开时进入有界离线队列，队列满则返回错误。
func (c *Client) Report(ctx context.Context, req mcp.ReportRequest) (*mcp.ReportResponse, error) {
	normalized := c.normalizeReportRequest(req)
	conn, degraded := c.currentConn()
	if conn == nil || degraded {
		if err := c.enqueueReport(normalized); err != nil {
			return nil, err
		}
		return queuedReportResponse(normalized), nil
	}
	resp, err := c.sendReportWithConn(ctx, conn, c.currentLease(), normalized)
	if err == nil {
		return resp, nil
	}
	if !isTransportErr(err) {
		return nil, err
	}
	if qerr := c.enqueueReport(normalized); qerr != nil {
		return nil, qerr
	}
	return queuedReportResponse(normalized), nil
}

// Close 关闭MCP 服务资源。
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	hbCancel := c.hbCancel
	stop := c.stop
	conn := c.conn
	lease := c.lease
	c.hbCancel = nil
	c.stop = nil
	c.rootCtx = nil
	c.conn = nil
	c.lease = mcp.LeaseKey{}
	c.reconnecting = false
	c.mu.Unlock()

	if hbCancel != nil {
		hbCancel()
	}
	if conn != nil {
		c.flushQueuedReportsWithConn(context.Background(), conn, lease)
		if finalReq := c.finalReportRequest(); finalReq != nil {
			if _, err := c.sendReportWithConn(context.Background(), conn, lease, *finalReq); err != nil && !isTransportErr(err) {
				pkglogger.Warn("bootstrap final report failed",
					"instance_id", c.instanceID,
					"lease_key", lease,
					"report_id", finalReq.ReportID,
					"error", err,
				)
			}
		}
	}
	if stop != nil {
		stop()
	}
	drainErr := c.drainCallbacks(defaultCallbackDrainTimeout)
	if drainErr != nil {
		pkglogger.Warn("bootstrap callback drain timed out",
			"instance_id", c.instanceID,
			"error", drainErr,
		)
	}
	if conn != nil {
		return conn.Close()
	}
	return nil
}

// drainCallbacks 等待由 spawnCallback 启动的 OnShutdown/OnConfigChanged goroutine 退出。
// 超时返回错误但调用方仍继续关闭流程，避免应用回调卡住 bootstrap shutdown。
func (c *Client) drainCallbacks(timeout time.Duration) error {
	done := make(chan struct{})
	var drainWG sync.WaitGroup
	drainWG.Go(func() {
		defer func() { _ = recover() }()
		c.callbackWG.Wait()
		close(done)
	})
	select {
	case <-done:
		return nil
	case <-time.After(timeout):
		return errors.New("bootstrap: OnShutdown/OnConfigChanged callbacks did not drain within " + timeout.String())
	}
}
