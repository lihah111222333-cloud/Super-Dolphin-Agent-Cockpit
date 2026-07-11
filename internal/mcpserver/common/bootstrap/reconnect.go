package bootstrap

import (
	"context"
	"sync"
	"time"

	"github.com/creachadair/jrpc2"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/metrics"
)

// reconnectMaxDelay 是重连指数退避的上限。
const reconnectMaxDelay = 30 * time.Second

// handleStop 在 jrpc2 连接断开时触发，判断是否需要重连并启动重连 goroutine。
func (c *Client) handleStop(stopped *jrpc2.Client, err error) {
	rootCtx, shouldReconnect := c.markDisconnected(stopped)
	if !shouldReconnect {
		return
	}
	pkglogger.Warn("bootstrap disconnected",
		"instance_id", c.instanceID,
		"lease_key", c.currentLease(),
		"error", err,
	)
	var reconnectWG sync.WaitGroup
	reconnectWG.Go(func() {
		defer func() {
			if rec := recover(); rec != nil {
				pkglogger.Error("bootstrap: recovered reconnectLoop panic",
					"instance_id", c.instanceID, "panic", rec)
			}
		}()
		c.reconnectLoop(rootCtx)
	})
}

// markDisconnected 在锁内清空连接和租约，并决定是否启动唯一的重连循环。
func (c *Client) markDisconnected(stopped *jrpc2.Client) (context.Context, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != stopped {
		return nil, false
	}
	if c.hbCancel != nil {
		c.hbCancel()
		c.hbCancel = nil
	}
	c.conn = nil
	c.lease = mcp.LeaseKey{}
	if c.closed || c.rootCtx == nil || c.rootCtx.Err() != nil || c.reconnecting {
		return c.rootCtx, false
	}
	c.reconnecting = true
	return c.rootCtx, true
}

// reconnectLoop 按指数退避重连；成功后恢复心跳、flush 报告并重放 hook 订阅。
func (c *Client) reconnectLoop(ctx context.Context) {
	delay := time.Second
	for {
		if ctx == nil || ctx.Err() != nil {
			return
		}
		conn, reg, err := c.reconnectAttempt(ctx)
		if err == nil {
			// 先记录 reconnectAttempt 成功，再激活连接，指标只反映本次尝试结果。
			metrics.BootstrapReconnectAttempts.WithLabelValues("success").Inc()
			c.mu.Lock()
			if c.closed || c.rootCtx != ctx {
				c.mu.Unlock()
				_ = conn.Close()
				return
			}
			c.activateLocked(conn, reg)
			c.mu.Unlock()
			c.flushQueuedReports(context.Background())
			replayErr := c.replayHookSubscriptions(ctx)
			pkglogger.Info("bootstrap reconnected",
				"instance_id", c.instanceID,
				"lease_key", mcp.LeaseKey{InstanceID: reg.InstanceID, Generation: reg.Generation},
				"hook_replay_pending", replayErr != nil,
			)
			return
		}
		// 失败指标与 warn 日志同步记录，便于观测侧按同一时间点关联。
		metrics.BootstrapReconnectAttempts.WithLabelValues("fail").Inc()
		pkglogger.Warn("bootstrap reconnect failed",
			"instance_id", c.instanceID,
			"retry_in", delay,
			"error", err,
		)
		if !sleepContext(ctx, delay) {
			return
		}
		delay = nextReconnectDelay(delay)
	}
}

// reconnectAttempt 在超时上下文内执行一次 connect+register 尝试。
func (c *Client) reconnectAttempt(ctx context.Context) (*jrpc2.Client, *mcp.RegisterResponse, error) {
	attemptCtx, cancel := platformconfig.WithPeerTimeout(ctx, defaultHeartbeatInterval)
	defer cancel()
	return c.connectAndRegister(attemptCtx)
}

// sleepContext 等待 delay 后返回 true，若 ctx 先取消则返回 false。
func sleepContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// nextReconnectDelay 按指数退避计算下一次重连等待时间，上限为 reconnectMaxDelay。
func nextReconnectDelay(current time.Duration) time.Duration {
	next := current * 2
	if next > reconnectMaxDelay {
		return reconnectMaxDelay
	}
	return next
}
