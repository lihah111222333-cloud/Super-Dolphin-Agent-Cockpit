package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

const (
	defaultHeartbeatInterval = 10 * time.Second
	defaultHeartbeatTimeout  = 5 * time.Second
	defaultRPCTimeout        = 5 * time.Second
	defaultReportQueueLimit  = 128
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
}

func New(cfg Config) *Client {
	cfg, boot := normalizeConfig(cfg)
	return &Client{
		instanceID:        cfg.InstanceID,
		cfg:               cfg,
		heartbeatInterval: defaultHeartbeatInterval,
		heartbeatTimeout:  defaultHeartbeatTimeout,
		sendTimeout:       defaultRPCTimeout,
		reportQueueLimit:  normalizeQueueLimit(cfg.ReportQueueLimit),
		boot:              boot,
	}
}

func (c *Client) Start(ctx context.Context) error {
	if strings.TrimSpace(c.cfg.RPCAddr) == "" {
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
		_ = c.Close()
		return err
	}
	c.mu.Lock()
	c.activateLocked(conn, reg)
	c.mu.Unlock()
	go c.watchRoot(rootCtx)
	c.flushQueuedReports(context.Background())
	return nil
}

func (c *Client) Context(ctx context.Context, scope string, keys []string) (*mcp.ContextResponse, error) {
	conn, degraded := c.currentConn()
	if conn == nil || degraded {
		return c.envContext(scope, keys)
	}
	req := mcp.ContextRequest{
		Lease: c.currentLease(),
		Scope: strings.TrimSpace(scope),
		Keys:  cloneStrings(keys),
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
	req := mcp.EventNotify{
		Lease:      c.currentLease(),
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
			return nil
		}
		return err
	}
	return nil
}

func (c *Client) Log(ctx context.Context, level, message string, fields map[string]string) error {
	entry := mcp.LogNotify{
		Lease:   c.currentLease(),
		Seq:     c.nextLogSeq(),
		Level:   strings.TrimSpace(level),
		Message: message,
		Fields:  cloneStringMapAny(fields),
		TS:      time.Now().UnixMilli(),
	}
	conn, degraded := c.currentConn()
	if conn == nil || degraded {
		c.localLogFallback(entry, nil)
		return nil
	}
	noteCtx, cancel := withTimeoutIfNone(ctx, c.currentSendTimeout())
	defer cancel()
	if err := conn.Notify(noteCtx, mcp.MethodLog, entry); err != nil {
		if isTransportErr(err) {
			c.localLogFallback(entry, err)
			return nil
		}
		return err
	}
	return nil
}

func (c *Client) RequestApproval(ctx context.Context, req mcp.ApprovalRequest) (*mcp.ApprovalResponse, error) {
	conn, degraded := c.currentConn()
	if conn == nil || degraded {
		return nil, approvalUnavailableErr("live lifecycle RPC is unavailable")
	}
	req.Lease = c.currentLease()
	var resp mcp.ApprovalResponse
	if err := conn.CallResult(defaultContext(ctx), mcp.MethodApproval, req, &resp); err != nil {
		if isTransportErr(err) {
			return nil, approvalUnavailableErr("live lifecycle RPC disconnected during approval")
		}
		return nil, err
	}
	resp.Detail = cloneRaw(resp.Detail)
	return &resp, nil
}

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
				log.Printf("bootstrap final report failed: instance=%s report_id=%s err=%v", c.instanceID, finalReq.ReportID, err)
			}
		}
	}
	if stop != nil {
		stop()
	}
	if conn != nil {
		return conn.Close()
	}
	return nil
}
