package codexapp

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	"github.com/gorilla/websocket"
)

const (
	transportReadyTimeout        = 15 * time.Second
	transportShutdownGracePeriod = 3 * time.Second
	transportKillWaitTimeout     = 5 * time.Second
	transportStderrLimitBytes    = 8 * 1024
	transportConnectRetryDelay   = 150 * time.Millisecond
	transportConnectRetryMaxWait = time.Second
)

type transport struct {
	serverURL  string
	local      bool
	ws         *websocket.Conn
	process    *localProcess
	processErr error
	stateMu    sync.RWMutex
	writeMu    sync.Mutex
	pending    sync.Map
	nextID     atomic.Int64
	looping    atomic.Bool
	closed     atomic.Bool
}

func newTransport(ctx context.Context, serverURL string) (*transport, error) {
	startupCtx, cancel := withTimeout(shared.NonNilContext(ctx), transportReadyTimeout)
	defer cancel()
	t := &transport{serverURL: normalizeServerURL(serverURL)}
	if t.serverURL == "" {
		if err := t.spawnLocal(); err != nil {
			return nil, err
		}
	}
	if err := t.establish(startupCtx); err != nil {
		_ = t.Kill()
		return nil, err
	}
	return t, nil
}

func (t *transport) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if err := t.ensureOpen(); err != nil {
		return nil, err
	}
	callCtx, cancel := withTimeout(ctx, 30*time.Second)
	defer cancel()
	id := t.nextID.Add(1)
	key := strconv.FormatInt(id, 10)
	pc := &pendingCall{done: make(chan struct{})}
	t.pending.Store(key, pc)
	defer t.pending.Delete(key)
	if err := t.writeJSON(jsonRPCRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		return nil, err
	}
	select {
	case <-callCtx.Done():
		return nil, callCtx.Err()
	case <-pc.done:
		return pc.result, pc.err
	}
}

func (t *transport) Notify(method string, params any) error {
	if err := t.ensureOpen(); err != nil {
		return err
	}
	return t.writeJSON(struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{JSONRPC: "2.0", Method: method, Params: params})
}

func (t *transport) ReadLoop(ctx context.Context, handler any) {
	if !t.looping.CompareAndSwap(false, true) {
		return
	}
	defer t.looping.Store(false)
	for t.readLoopStep(ctx, handler) {
	}
}

func (t *transport) Close() error {
	return t.shutdownTransport(true)
}

func (t *transport) Kill() error {
	return t.shutdownTransport(false)
}

func (t *transport) Running() bool {
	if t.closed.Load() || t.currentWS() == nil {
		return false
	}
	return !t.local || t.processRunning()
}

func (t *transport) reconnect(ctx context.Context) error {
	if err := t.ensureOpen(); err != nil {
		return err
	}
	t.closeSocket()
	if t.local && !t.processRunning() {
		if err := t.spawnLocal(); err != nil {
			return err
		}
	}
	return t.establish(ctx)
}

func (t *transport) establish(ctx context.Context) error {
	ctx = shared.NonNilContext(ctx)
	if err := t.connect(ctx); err != nil {
		return err
	}
	return t.initialize(ctx)
}

func (t *transport) connect(ctx context.Context) error {
	retryDelay := transportConnectRetryDelay
	for {
		connected, err := t.connectAttempt(ctx)
		if err != nil {
			return err
		}
		if connected {
			return nil
		}
		if err := t.waitForConnectRetry(ctx, retryDelay); err != nil {
			return err
		}
		retryDelay = nextConnectRetryDelay(retryDelay)
	}
}

func (t *transport) connectAttempt(ctx context.Context) (bool, error) {
	if err := t.localProcessReady(); err != nil {
		if procErr := t.localProcessFailure(); procErr != nil {
			return false, procErr
		}
		return false, nil
	}
	if err := t.connectOnce(ctx); err != nil {
		if procErr := t.localProcessFailure(); procErr != nil {
			return false, procErr
		}
		return false, nil
	}
	return true, nil
}

func (t *transport) waitForConnectRetry(ctx context.Context, delay time.Duration) error {
	select {
	case <-ctx.Done():
		if procErr := t.localProcessFailure(); procErr != nil {
			return procErr
		}
		return ctx.Err()
	case <-time.After(delay):
		return nil
	}
}

func nextConnectRetryDelay(delay time.Duration) time.Duration {
	if delay < transportConnectRetryMaxWait {
		delay *= 2
		if delay > transportConnectRetryMaxWait {
			return transportConnectRetryMaxWait
		}
	}
	return delay
}

func (t *transport) localProcessFailure() error {
	if !t.local {
		return nil
	}
	return t.processFailure()
}

func (t *transport) readLoopStep(ctx context.Context, handler any) bool {
	if err := shared.CheckCtx(ctx); err != nil {
		return false
	}
	if t.closed.Load() {
		return false
	}
	ws := t.currentWS()
	if ws == nil {
		return t.endReadLoop(ctx, handler, nil, "connection unavailable")
	}
	_, data, err := ws.ReadMessage()
	if err != nil {
		return t.endReadLoop(ctx, handler, err, err.Error())
	}
	return t.dispatchReadMessage(ctx, data, handler)
}
