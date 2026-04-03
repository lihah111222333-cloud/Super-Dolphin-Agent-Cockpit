package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	transportReadyTimeout        = 15 * time.Second
	transportShutdownGracePeriod = 3 * time.Second
	transportKillWaitTimeout     = 5 * time.Second
	transportStderrLimitBytes    = 8 * 1024
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
	startupCtx, cancel := withTimeout(normalizeTransportContext(ctx), transportReadyTimeout)
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
	if t.closed.Load() {
		return nil, errors.New("codexapp: transport closed")
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
	if t.closed.Load() {
		return errors.New("codexapp: transport closed")
	}
	return t.writeJSON(struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{JSONRPC: "2.0", Method: method, Params: params})
}

func (t *transport) ReadLoop(ctx context.Context, handler func(method string, params json.RawMessage)) {
	if !t.looping.CompareAndSwap(false, true) {
		return
	}
	defer t.looping.Store(false)
	for t.readLoopStep(ctx, handler) {
	}
}

func (t *transport) Close() error {
	if t.closed.Load() {
		return nil
	}
	_ = t.Notify("shutdown", nil)
	t.closed.Store(true)
	err := t.stopProcess(true)
	t.closeSocket()
	return err
}

func (t *transport) Kill() error {
	t.closed.Store(true)
	err := t.stopProcess(false)
	t.closeSocket()
	return err
}

func (t *transport) Running() bool {
	if t.closed.Load() || t.currentWS() == nil {
		return false
	}
	return !t.local || t.processRunning()
}

func (t *transport) reconnect(ctx context.Context) error {
	if t.closed.Load() {
		return errors.New("codexapp: transport closed")
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
	ctx = normalizeTransportContext(ctx)
	if err := t.connect(ctx); err != nil {
		return err
	}
	return t.initialize(ctx)
}

func (t *transport) connect(ctx context.Context) error {
	var lastErr error
	for attempt := 0; attempt < 6; attempt++ {
		if err := t.localProcessReady(); err != nil {
			if t.local && t.processFailure() != nil {
				return err
			}
			lastErr = err
		} else {
			lastErr = t.connectOnce(ctx)
			if lastErr == nil {
				return nil
			}
		}
		if attempt == 5 {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(150 * time.Millisecond << attempt):
		}
	}
	return lastErr
}

func (t *transport) readLoopStep(ctx context.Context, handler func(string, json.RawMessage)) bool {
	if err := checkCtx(ctx); err != nil {
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
	return t.dispatchReadMessage(data, handler)
}
