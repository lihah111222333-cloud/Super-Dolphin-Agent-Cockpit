package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	xwebsocket "golang.org/x/net/websocket"
)

type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type jsonRPCMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type pendingCall struct {
	result json.RawMessage
	err    error
	done   chan struct{}
	once   sync.Once
}

type transport struct {
	serverURL string
	local     bool
	cmd       *exec.Cmd
	ws        *xwebsocket.Conn
	stateMu   sync.RWMutex
	writeMu   sync.Mutex
	pending   sync.Map
	nextID    atomic.Int64
	looping   atomic.Bool
	closed    atomic.Bool
}

func newTransport(serverURL string) (*transport, error) {
	t := &transport{serverURL: normalizeServerURL(serverURL)}
	if t.serverURL == "" {
		if err := t.spawnLocal(); err != nil {
			return nil, err
		}
	}
	ctx, cancel := withTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := t.connect(ctx); err != nil {
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
	t.closeSocket()
	return t.stopProcess(true)
}

func (t *transport) Kill() error {
	t.closed.Store(true)
	t.closeSocket()
	return t.stopProcess(false)
}

func (t *transport) Running() bool {
	return !t.closed.Load() && t.currentWS() != nil
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
	if err := t.connect(ctx); err != nil {
		return err
	}
	return t.initialize(ctx)
}

func (t *transport) connect(ctx context.Context) error {
	return shared.Retry(ctx, 6, 150*time.Millisecond, func() error {
		config, err := xwebsocket.NewConfig(t.serverURL, websocketOrigin(t.serverURL))
		if err != nil {
			return err
		}
		config.Dialer = &net.Dialer{Timeout: 5 * time.Second}
		conn, err := config.DialContext(ctx)
		if err != nil {
			return err
		}
		t.setWS(conn)
		return nil
	})
}

func (t *transport) initialize(ctx context.Context) error {
	ctx = normalizeTransportContext(ctx)
	ws, err := t.initializeSocket(ctx)
	if err != nil {
		return err
	}
	id, key, pc := t.registerInitializeCall()
	defer t.pending.Delete(key)
	if err := t.sendInitializeRequest(id); err != nil {
		return err
	}
	return t.awaitInitialize(ctx, ws, pc)
}

func normalizeTransportContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func (t *transport) initializeSocket(ctx context.Context) (*xwebsocket.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ws := t.currentWS()
	if ws == nil {
		return nil, errors.New("codexapp: websocket not connected")
	}
	return ws, nil
}

func (t *transport) registerInitializeCall() (int64, string, *pendingCall) {
	id := t.nextID.Add(1)
	key := strconv.FormatInt(id, 10)
	pc := &pendingCall{done: make(chan struct{})}
	t.pending.Store(key, pc)
	return id, key, pc
}

func (t *transport) sendInitializeRequest(id int64) error {
	return t.writeJSON(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  "initialize",
		Params:  initializeParams(),
	})
}

func (t *transport) awaitInitialize(ctx context.Context, ws *xwebsocket.Conn, pc *pendingCall) error {
	defer func() { _ = ws.SetReadDeadline(time.Time{}) }()
	for {
		if done, err := initializeDone(pc); done {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := t.readInitializeMessage(ctx, ws); err != nil {
			if isTimeoutNetError(err) {
				continue
			}
			return err
		}
	}
}

func initializeDone(pc *pendingCall) (bool, error) {
	select {
	case <-pc.done:
		return true, pc.err
	default:
		return false, nil
	}
}

func (t *transport) readInitializeMessage(ctx context.Context, ws *xwebsocket.Conn) error {
	_ = ws.SetReadDeadline(initializeReadDeadline(ctx))
	var data string
	if err := xwebsocket.Message.Receive(ws, &data); err != nil {
		return err
	}
	t.dispatchReadMessage([]byte(data), nil)
	return nil
}

func initializeReadDeadline(ctx context.Context) time.Time {
	deadline := time.Now().Add(200 * time.Millisecond)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	return deadline
}

func isTimeoutNetError(err error) bool {
	netErr, ok := err.(net.Error)
	return ok && netErr.Timeout()
}

func (t *transport) spawnLocal() error {
	if t.processRunning() {
		return nil
	}
	serverURL, err := reserveServerURL()
	if err != nil {
		return err
	}
	cmd := exec.Command("codex", "app-server", "--listen", serverURL)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return err
	}
	t.stateMu.Lock()
	t.cmd = cmd
	t.local = true
	t.serverURL = serverURL
	t.stateMu.Unlock()
	return nil
}

func (t *transport) writeJSON(v any) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	ws := t.currentWS()
	if ws == nil {
		return errors.New("codexapp: websocket not connected")
	}
	_ = ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return xwebsocket.JSON.Send(ws, v)
}

func (t *transport) readLoopStep(ctx context.Context, handler func(string, json.RawMessage)) bool {
	if ctx.Err() != nil || t.closed.Load() {
		return false
	}
	ws := t.currentWS()
	if ws == nil {
		return t.endReadLoop(ctx, handler, nil, "connection unavailable")
	}
	var data string
	err := xwebsocket.Message.Receive(ws, &data)
	if err != nil {
		return t.endReadLoop(ctx, handler, err, err.Error())
	}
	return t.dispatchReadMessage([]byte(data), handler)
}

func (t *transport) endReadLoop(ctx context.Context, handler func(string, json.RawMessage), err error, message string) bool {
	if err != nil {
		t.failPending(err)
	}
	if handler != nil && !t.closed.Load() && ctx.Err() == nil {
		handler("connection.dead", mustJSON(map[string]any{"error": message}))
	}
	return false
}

func (t *transport) dispatchReadMessage(data []byte, handler func(string, json.RawMessage)) bool {
	var msg jsonRPCMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return true
	}
	if t.handleResponse(msg) {
		return true
	}
	if handler != nil && strings.TrimSpace(msg.Method) != "" {
		handler(msg.Method, msg.Params)
	}
	return true
}

func (t *transport) handleResponse(msg jsonRPCMessage) bool {
	if strings.TrimSpace(msg.Method) != "" || len(msg.ID) == 0 {
		return false
	}
	key := jsonRPCIDKey(msg.ID)
	value, ok := t.pending.Load(key)
	if !ok {
		return true
	}
	pc := value.(*pendingCall)
	if msg.Error != nil {
		pc.resolve(nil, fmt.Errorf("rpc error %d: %s", msg.Error.Code, msg.Error.Message))
		return true
	}
	pc.resolve(msg.Result, nil)
	return true
}

func (t *transport) failPending(err error) {
	if err == nil {
		err = errors.New("codexapp: transport unavailable")
	}
	t.pending.Range(func(_, value any) bool {
		value.(*pendingCall).resolve(nil, err)
		return true
	})
}

func (t *transport) currentWS() *xwebsocket.Conn {
	t.stateMu.RLock()
	defer t.stateMu.RUnlock()
	return t.ws
}

func (t *transport) setWS(ws *xwebsocket.Conn) {
	t.stateMu.Lock()
	defer t.stateMu.Unlock()
	if t.ws != nil && t.ws != ws {
		_ = t.ws.Close()
	}
	t.ws = ws
}

func (t *transport) closeSocket() {
	t.stateMu.Lock()
	defer t.stateMu.Unlock()
	if t.ws != nil {
		_ = t.ws.Close()
	}
	t.ws = nil
}

func (t *transport) stopProcess(graceful bool) error {
	t.stateMu.Lock()
	cmd := t.cmd
	t.cmd = nil
	t.stateMu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if graceful {
		_ = cmd.Process.Signal(os.Interrupt)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-time.After(time.Second):
		_ = cmd.Process.Kill()
		<-done
		return nil
	case <-done:
		return nil
	}
}

func (t *transport) processRunning() bool {
	t.stateMu.RLock()
	defer t.stateMu.RUnlock()
	return t.cmd != nil && t.cmd.Process != nil && (t.cmd.ProcessState == nil || !t.cmd.ProcessState.Exited())
}
