package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/creachadair/jrpc2"

	"github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/metrics"
)

const heartbeatWarnAfter = 3

func (c *Client) startHeartbeatLocked() {
	if c.rootCtx == nil {
		return
	}
	if c.hbCancel != nil {
		c.hbCancel()
	}
	hbCtx, cancel := context.WithCancel(c.rootCtx)
	c.hbCancel = cancel
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				pkglogger.Error("bootstrap: recovered heartbeat panic",
					"instance_id", c.instanceID, "panic", rec)
			}
		}()
		c.runHeartbeat(hbCtx)
	}()
}

// runHeartbeat 运行heartbeat。
func (c *Client) runHeartbeat(ctx context.Context) {
	failures := 0
	for {
		interval, timeout := c.heartbeatTiming()
		if !waitForHeartbeat(ctx, interval) {
			return
		}
		rejected, next, err := c.sendHeartbeat(ctx, timeout)
		if err != nil {
			failures++
			// P22 P4 S6b / plan §322: count every heartbeat failure,
			// not just warn-level ones, so operators can see churn
			// even under the 3-strike warn threshold.
			metrics.BootstrapHeartbeatFailures.WithLabelValues(c.cfg.BinaryName, c.cfg.ClientKind).Inc()
			if failures >= heartbeatWarnAfter {
				pkglogger.Warn("bootstrap heartbeat failed",
					"instance_id", c.instanceID,
					"lease_key", c.currentLease(),
					"failures", failures,
					"interval", interval,
					"error", err,
				)
			}
			continue
		}
		failures = 0
		if next > 0 {
			c.setHeartbeatInterval(next)
		}
		if rejected {
			if err := c.refreshLease(ctx); err != nil {
				pkglogger.Warn("bootstrap heartbeat lease refresh failed",
					"instance_id", c.instanceID,
					"lease_key", c.currentLease(),
					"interval", interval,
					"error", err,
				)
			}
		}
	}
}

func (c *Client) heartbeatTiming() (time.Duration, time.Duration) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	interval := c.heartbeatInterval
	timeout := c.heartbeatTimeout
	if interval <= 0 {
		interval = defaultHeartbeatInterval
	}
	if timeout <= 0 {
		timeout = defaultHeartbeatTimeout
	}
	return interval, timeout
}

func waitForHeartbeat(ctx context.Context, interval time.Duration) bool {
	timer := time.NewTimer(jitterDuration(interval))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// sendHeartbeat 处理sendheartbeat。
func (c *Client) sendHeartbeat(ctx context.Context, timeout time.Duration) (bool, time.Duration, error) {
	conn, lease := c.callTarget()
	if conn == nil || lease.Generation == 0 {
		return false, 0, errors.New("bootstrap: heartbeat without active lease")
	}
	callCtx, cancel := withTimeoutIfNone(ctx, timeout)
	defer cancel()
	req := mcp.HeartbeatRequest{
		InstanceID:            lease.InstanceID,
		Generation:            lease.Generation,
		HeartbeatSeq:          c.nextHeartbeatSeq(),
		Status:                mcp.StatusActive,
		Metrics:               c.heartbeatMetrics(),
		ObservedConfigVersion: c.currentConfigVersion(),
	}
	var resp mcp.HeartbeatResponse
	leaseRejected := false
	if err := conn.CallResult(callCtx, mcp.MethodHeartbeat, req, &resp); err != nil {
		if isLeaseRejectedErr(err) {
			leaseRejected = true
		} else {
			return false, 0, err
		}
	}
	if leaseRejected {
		return true, 0, nil
	}
	c.mu.Lock()
	c.configVersion = resp.ConfigVersion
	c.mu.Unlock()
	if !resp.OK {
		return true, durationOrDefault(resp.NextHeartbeatMs, 0), nil
	}
	return false, durationOrDefault(resp.NextHeartbeatMs, 0), nil
}

func (c *Client) refreshLease(ctx context.Context) error {
	conn, _ := c.currentConn()
	if conn == nil {
		return errors.New("bootstrap: reconnect required")
	}
	reg, err := c.registerConn(ctx, conn)
	if err != nil {
		return err
	}
	c.mu.Lock()
	if c.conn != conn {
		c.mu.Unlock()
		return nil
	}
	c.applyRegisterLocked(reg)
	c.startHeartbeatLocked()
	c.mu.Unlock()
	c.flushQueuedReports(context.Background())
	return nil
}

func (c *Client) currentConfigVersion() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.configVersion
}

func (c *Client) setHeartbeatInterval(interval time.Duration) {
	if interval <= 0 {
		return
	}
	c.mu.Lock()
	c.heartbeatInterval = interval
	c.mu.Unlock()
}

func (c *Client) heartbeatMetrics() json.RawMessage {
	c.mu.RLock()
	payload := map[string]any{
		"queued_reports": len(c.reportQueue),
		"client_kind":    c.cfg.ClientKind,
	}
	c.mu.RUnlock()
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return raw
}

func isLeaseRejectedErr(err error) bool {
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) {
		return false
	}
	switch rpcErr.Code {
	case jrpc2.Code(mcp.ErrCodeLeaseNotFound),
		jrpc2.Code(mcp.ErrCodeLeaseStale):
		return true
	default:
		return false
	}
}
