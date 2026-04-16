package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/gorilla/websocket"
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

type RawMessage struct {
	ID     json.RawMessage
	Method string
	Params json.RawMessage
}

func (m RawMessage) ThreadID() string {
	if len(m.Params) == 0 {
		return ""
	}
	return payloadThreadID(decodeEventPayload(m.Params))
}

var _ Responder = (*transport)(nil)

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

func (p *pendingCall) resolve(result json.RawMessage, err error) {
	p.once.Do(func() {
		p.result = result
		p.err = err
		close(p.done)
	})
}

func normalizeServerURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "ws://") || strings.HasPrefix(raw, "wss://") {
		return raw
	}
	return "ws://" + raw
}

func localSpawnListenURL() string {
	return "ws://127.0.0.1:0"
}

func reserveServerURL() (string, func(), error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	addr := listener.Addr().(*net.TCPAddr)
	release := func() {
		_ = listener.Close()
	}
	return fmt.Sprintf("ws://127.0.0.1:%d", addr.Port), release, nil
}

func jsonRPCIDKey(raw json.RawMessage) string {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var num int64
	if err := json.Unmarshal(raw, &num); err == nil {
		return strconv.FormatInt(num, 10)
	}
	return strings.TrimSpace(string(raw))
}

func (t *transport) connectOnce(ctx context.Context) error {
	dialer := websocket.Dialer{
		HandshakeTimeout: 5 * time.Second,
		// Bypass local proxy for 127.0.0.1 connections. Without this,
		// HTTP_PROXY / HTTPS_PROXY env vars cause localhost WS dials
		// to route through the proxy and timeout or fail.
		Proxy: nil,
	}
	conn, _, err := dialer.DialContext(ctx, t.serverURL, nil)
	if err != nil {
		return err
	}
	t.setWS(conn)
	return nil
}

func (t *transport) initialize(ctx context.Context) error {
	ctx = shared.NonNilContext(ctx)
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

func (t *transport) initializeSocket(ctx context.Context) (*websocket.Conn, error) {
	if err := shared.CheckCtx(ctx); err != nil {
		return nil, err
	}
	return t.currentWSOrErr()
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

func (t *transport) awaitInitialize(ctx context.Context, ws *websocket.Conn, pc *pendingCall) error {
	defer func() { _ = ws.SetReadDeadline(time.Time{}) }()
	for {
		if done, err := initializeDone(pc); done {
			return err
		}
		if err := shared.CheckCtx(ctx); err != nil {
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

func (t *transport) readInitializeMessage(ctx context.Context, ws *websocket.Conn) error {
	_ = ws.SetReadDeadline(initializeReadDeadline(ctx))
	_, data, err := ws.ReadMessage()
	if err != nil {
		return err
	}
	// P15 debug: log the initialize response to verify experimentalApi accepted
	if len(data) < 2000 {
		pkglogger.Info("codexapp: initialize response", "data", string(data))
	} else {
		pkglogger.Info("codexapp: initialize response", "data_len", len(data), "preview", string(data[:500]))
	}
	t.dispatchReadMessage(ctx, data, nil)
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

func (t *transport) writeJSON(v any) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	ws, err := t.currentWSOrErr()
	if err != nil {
		return err
	}
	_ = ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return ws.WriteJSON(v)
}

func (t *transport) endReadLoop(ctx context.Context, handler any, err error, message string) bool {
	pkglogger.Warn("codexapp: transport read loop ending",
		"server_url", t.serverURL, "local", t.local, "closed", t.closed.Load(),
		"error", err, "message", message)
	if err != nil {
		t.failPending(err)
	}
	if handler != nil && !t.closed.Load() && shared.CheckCtx(ctx) == nil {
		invokeReadHandler(ctx, t, RawMessage{Method: "connection.dead", Params: mustJSON(map[string]any{"error": message})}, handler)
	}
	return false
}

func (t *transport) dispatchReadMessage(ctx context.Context, data []byte, handler any) bool {
	var rpcMsg jsonRPCMessage
	if err := json.Unmarshal(data, &rpcMsg); err != nil {
		return true
	}
	if t.handleResponse(rpcMsg) {
		return true
	}
	msg := RawMessage{ID: rpcMsg.ID, Method: rpcMsg.Method, Params: rpcMsg.Params}
	if strings.TrimSpace(msg.Method) != "" {
		invokeReadHandler(ctx, t, msg, handler)
	}
	return true
}

func invokeReadHandler(ctx context.Context, resp Responder, msg RawMessage, handler any) {
	switch h := handler.(type) {
	case func(context.Context, Responder, RawMessage):
		h(ctx, resp, msg)
	case func(string, json.RawMessage):
		if strings.TrimSpace(msg.Method) != "" {
			h(msg.Method, msg.Params)
		}
	}
}

func (t *transport) RespondWithID(id json.RawMessage, result any, callErr error) error {
	if len(id) == 0 {
		return errors.New("codexapp: response id is required")
	}
	payload := map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
	if callErr != nil {
		code, message := -32000, strings.ToLower(strings.TrimSpace(callErr.Error()))
		if strings.Contains(message, "method not supported") || strings.Contains(message, "method not found") {
			code = -32601
		}
		delete(payload, "result")
		payload["error"] = jsonRPCError{Code: code, Message: callErr.Error()}
	}
	return t.writeJSON(payload)
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

func (t *transport) currentWS() *websocket.Conn {
	t.stateMu.RLock()
	defer t.stateMu.RUnlock()
	return t.ws
}

func (t *transport) setWS(ws *websocket.Conn) {
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
