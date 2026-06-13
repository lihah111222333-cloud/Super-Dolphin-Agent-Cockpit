package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/creachadair/jrpc2"

	"github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

const (
	defaultHeartbeatInterval = 10 * time.Second
	defaultHeartbeatTimeout  = 5 * time.Second
	defaultRPCTimeout        = 5 * time.Second
	defaultReportQueueLimit  = 128
	// defaultCallbackDrainTimeout bounds how long Close() waits for
	// in-flight OnShutdown / OnConfigChanged callback goroutines to
	// drain. Keep it below the overall shutdown budget so a stuck
	// application callback cannot pin bootstrap shutdown. P22 P2
	// bootstrap-S1 / plan §499 / §505.
	defaultCallbackDrainTimeout = 2 * time.Second
)

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
	capabilitiesNegotiated []string
	capabilitiesRejected   []string
	resumeGeneration       uint64
	heartbeatSeq           uint64
	logSeq                 uint64
	reportQueue            []mcp.ReportRequest
	reportQueueLimit       int
	boot                   bootSnapshot
	hooks                  hookState

	// callbackWG tracks in-flight OnShutdown / OnConfigChanged
	// goroutines. Pre-P22 P2 bootstrap-S1 fireShutdown /
	// fireConfigChanged launched the application callback as a bare
	// goroutine with no owner, so those callbacks could outlive the
	// bootstrap client and Close() had no join/drain point. The
	// WaitGroup plus drainCallbacks() give Close() a bounded,
	// observable drain (plan §499 / §505).
	callbackWG sync.WaitGroup
}

type Config struct {
	RPCAddr              string
	InstanceID           string
	BootID               string
	BinaryName           string
	ClientKind           string
	AgentID              string // optional; empty means this MCP process is a shared service
	ThreadID             string
	SessionToken         string
	Capabilities         []string
	CapabilitiesOffered  []string
	CapabilitiesRequired []string
	Subscriptions        []string
	BootSnapshot         json.RawMessage
	ReportQueueLimit     int
	FinalReport          func() *mcp.ReportRequest
	OnShutdown           func(mcp.ShutdownRequest)
	OnConfigChanged      func(mcp.ConfigChangedNotify)
	OnLSPReleaseScope    func(context.Context, mcp.LSPReleaseScopeRequest) (mcp.LSPReleaseScopeResult, error)
	Hooks                HookConfig
	OnToolsList          func(context.Context) (any, error)                             // P15: tools/list callback
	OnToolsCall          func(ctx context.Context, params json.RawMessage) (any, error) // P15: tools/call callback
}

// New 创建MCP 服务。
func New(cfg Config) *Client {
	cfg, boot := normalizeConfig(cfg)
	return &Client{
		instanceID:        cfg.InstanceID,
		cfg:               cfg,
		heartbeatInterval: defaultHeartbeatInterval,
		heartbeatTimeout:  defaultHeartbeatTimeout,
		sendTimeout:       defaultRPCTimeout,
		reportQueueLimit:  shared.ClampLimit(cfg.ReportQueueLimit, 1, 0, defaultReportQueueLimit),
		boot:              boot,
	}
}

// Start 启动MCP 服务流程。
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
	go c.watchRoot(rootCtx)
	c.flushQueuedReports(context.Background())
	return nil
}

// Context 处理上下文。
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

// EmitEvent 处理emit事件。
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

// RequestApproval 处理请求审批。
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

// Report 报告MCP 服务。
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

// drainCallbacks waits up to timeout for every in-flight OnShutdown /
// OnConfigChanged goroutine launched via spawnCallback to return. A
// non-nil error indicates some callback outlived the drain budget;
// the caller still proceeds so a stuck application handler cannot
// pin bootstrap shutdown. P22 P2 bootstrap-S1 (plan §499 / §505).
func (c *Client) drainCallbacks(timeout time.Duration) error {
	done := make(chan struct{})
	go func() {
		defer func() { _ = recover() }()
		c.callbackWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-time.After(timeout):
		return errors.New("bootstrap: OnShutdown/OnConfigChanged callbacks did not drain within " + timeout.String())
	}
}
