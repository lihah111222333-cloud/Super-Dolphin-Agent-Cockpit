package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/creachadair/jrpc2"

	"github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/metrics"
)

// heartbeatWarnAfter 控制连续心跳失败达到多少次后升级为 warn 日志。
const heartbeatWarnAfter = 3

// startHeartbeatLocked 在已持有 mu 锁的情况下启动心跳 goroutine，替换旧的 hbCancel。
func (c *Client) startHeartbeatLocked() {
	if c.rootCtx == nil {
		return
	}
	if c.hbCancel != nil {
		c.hbCancel()
	}
	hbCtx, cancel := context.WithCancel(c.rootCtx)
	c.hbCancel = cancel
	var heartbeatWG sync.WaitGroup
	heartbeatWG.Go(func() {
		defer func() {
			if rec := recover(); rec != nil {
				pkglogger.Error("bootstrap: recovered heartbeat panic",
					"instance_id", c.instanceID, "panic", rec)
			}
		}()
		c.runHeartbeat(hbCtx)
	})
}

// runHeartbeat 按协商间隔发送心跳；租约被拒绝时触发重新 register。
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
			// 每次心跳失败都计数；warn 阈值只是日志降噪，指标仍要反映短暂抖动。
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

// heartbeatTiming 在读锁下读取心跳间隔和超时，保证并发安全。
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

// waitForHeartbeat 等待带抖动的心跳间隔，ctx 取消时返回 false。
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

// sendHeartbeat 发送一次心跳，并返回租约是否被拒绝以及服务端建议的下一次间隔。
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

// refreshLease 在租约被拒绝后重新 register 以获取新租约，然后重启心跳。
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

// currentConfigVersion 在读锁下返回当前已知的配置版本号。
func (c *Client) currentConfigVersion() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.configVersion
}

// setHeartbeatInterval 更新心跳间隔，非正值时不做修改。
func (c *Client) setHeartbeatInterval(interval time.Duration) {
	if interval <= 0 {
		return
	}
	c.mu.Lock()
	c.heartbeatInterval = interval
	c.mu.Unlock()
}

// heartbeatMetrics 收集并序列化心跳指标载荷，供 HeartbeatRequest 使用。
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

// isLeaseRejectedErr 判断 jrpc2 错误是否表示租约不存在或已过期。
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
