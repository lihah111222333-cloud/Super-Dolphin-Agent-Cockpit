package bootstrap

import (
	"context"
	"log"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

const reconnectMaxDelay = 30 * time.Second

func (c *Client) handleStop(stopped *jrpc2.Client, err error) {
	rootCtx, shouldReconnect := c.markDisconnected(stopped)
	if !shouldReconnect {
		return
	}
	log.Printf("bootstrap disconnected: instance=%s err=%v", c.instanceID, err)
	go c.reconnectLoop(rootCtx)
}

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

func (c *Client) reconnectLoop(ctx context.Context) {
	delay := time.Second
	for {
		if ctx == nil || ctx.Err() != nil {
			return
		}
		conn, reg, err := c.reconnectAttempt(ctx)
		if err == nil {
			c.mu.Lock()
			if c.closed || c.rootCtx != ctx {
				c.mu.Unlock()
				_ = conn.Close()
				return
			}
			c.activateLocked(conn, reg)
			c.mu.Unlock()
			c.flushQueuedReports(context.Background())
			log.Printf("bootstrap reconnected: instance=%s generation=%d", c.instanceID, reg.Lease.Generation)
			return
		}
		log.Printf("bootstrap reconnect failed: instance=%s retry_in=%s err=%v", c.instanceID, delay, err)
		if !sleepContext(ctx, delay) {
			return
		}
		delay = nextReconnectDelay(delay)
	}
}

func (c *Client) reconnectAttempt(ctx context.Context) (*jrpc2.Client, *mcp.RegisterResponse, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, defaultHeartbeatInterval)
	defer cancel()
	return c.connectAndRegister(attemptCtx)
}

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

func nextReconnectDelay(current time.Duration) time.Duration {
	next := current * 2
	if next > reconnectMaxDelay {
		return reconnectMaxDelay
	}
	return next
}
