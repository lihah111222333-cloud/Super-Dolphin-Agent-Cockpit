package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"
	"github.com/creachadair/jrpc2/handler"
	"github.com/gorilla/websocket"
)

var defaultWSUpgrader = websocket.Upgrader{}

const (
	wailsWSMaxMessageBytes      int64 = 16 * 1024 * 1024
	wailsWSMaxActiveConnections       = 32
)

var _ channel.Channel = (*wsChannel)(nil)

// WSHandler bridges a websocket connection into a jrpc2 channel.
// WSHandler 处理ws处理器。
func WSHandler(server *Server, opts *jrpc2.ServerOptions) http.Handler {
	var mux jrpc2.Assigner = handler.Map{}
	if server != nil && server.methods != nil {
		mux = wsDispatchAssigner{server: server}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		releaseSlot, err := reserveWailsWSConnectionSlot(server)
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer releaseSlot()
		conn, err := defaultWSUpgrader.Upgrade(w, r, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ch := newWSChannel(conn)
		var rpcLog jrpc2.RPCLogger
		tracker := (*rpcRequestTracker)(nil)
		if server != nil {
			tracker = newRPCRequestTracker(server.logger)
			rpcLog = tracker
		}
		srv := jrpc2.NewServer(mux, prepareServerOptions(rpcLog, opts)).Start(ch)
		if server != nil {
			server.addActive(srv, dto.PeerKindUI)
			defer server.removeActive(srv)
			server.notifyConnected(srv)
		}
		defer srv.Stop()
		defer ch.Close()
		if err := srv.Wait(); err != nil && !isExpectedCloseErr(err) {
			if tracker != nil {
				tracker.logConnectionExit(err)
			}
			deadline := time.Now().Add(time.Second)
			msg := websocket.FormatCloseMessage(websocket.CloseInternalServerErr, http.StatusText(http.StatusInternalServerError))
			_ = conn.WriteControl(websocket.CloseMessage, msg, deadline)
		}
	})
}

func reserveWailsWSConnectionSlot(server *Server) (func(), error) {
	if server == nil {
		return noopWailsWSConnectionSlot, nil
	}
	if err := server.reserveUIWebSocketSlot(); err != nil {
		return nil, err
	}
	return server.releaseUIWebSocketSlot, nil
}

func noopWailsWSConnectionSlot() {}

type wsDispatchAssigner struct {
	server *Server
}

// Assign 处理assign。
func (a wsDispatchAssigner) Assign(_ context.Context, method string) jrpc2.Handler {
	if a.server == nil || a.server.methods == nil || a.server.methods[method] == nil {
		return nil
	}
	return handler.Func(func(ctx context.Context, req *jrpc2.Request) (any, error) {
		raw := json.RawMessage(req.ParamString())
		traceCtx, params, err := prepareWSDispatchParams(ctx, method, raw)
		if err != nil {
			return nil, jrpc2.Errorf(jrpc2.Code(CodeInvalidParams), "%s", err.Error())
		}
		out, err := a.server.Dispatch(traceCtx, method, params)
		if err != nil {
			return nil, err
		}
		var value any
		if len(out) == 0 || string(out) == "null" {
			return nil, nil
		}
		if err := json.Unmarshal(out, &value); err != nil {
			return nil, err
		}
		return value, nil
	})
}

func prepareWSDispatchParams(ctx context.Context, method string, raw json.RawMessage) (context.Context, json.RawMessage, error) {
	traceCtx, err := wsFrontendTraceContext(ctx, raw)
	if err != nil {
		return nil, nil, err
	}
	return traceCtx, stripWSFrontendMeta(method, raw), nil
}

func stripWSFrontendMeta(method string, raw json.RawMessage) json.RawMessage {
	if strings.TrimSpace(method) == "ui/log" {
		return stripWSJSONFields(raw, func(key string) bool {
			return key == "_aoTraceparent" || key == "_aoTraceId" || key == "_aoSpanId"
		})
	}
	return stripWSJSONFields(raw, func(key string) bool {
		return strings.HasPrefix(key, "_ao")
	})
}

// stripWSJSONFields 处理stripwsjson字段。
func stripWSJSONFields(raw json.RawMessage, shouldStrip func(string) bool) json.RawMessage {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return raw
	}
	changed := false
	for key := range obj {
		if shouldStrip(key) {
			delete(obj, key)
			changed = true
		}
	}
	if !changed {
		return raw
	}
	cleaned, err := json.Marshal(obj)
	if err != nil {
		return raw
	}
	return cleaned
}

// wsFrontendTraceContext 处理ws前端trace上下文。
func wsFrontendTraceContext(ctx context.Context, raw json.RawMessage) (context.Context, error) {
	if !wsIsJSONObject(raw) {
		return ctx, nil
	}
	obj, err := wsDecodeFrontendMetaObject(raw)
	if err != nil {
		return nil, err
	}
	traceparent, ok, err := wsFrontendStringField(obj, "_aoTraceparent")
	if err != nil {
		return nil, err
	}
	if !ok {
		return ctx, nil
	}
	traceID, spanID, err := wsParseFrontendTraceparent(traceparent)
	if err != nil {
		return nil, jrpc2.Errorf(jrpc2.Code(CodeInvalidParams), "invalid _aoTraceparent: %v", err)
	}
	if err := wsValidateFrontendTraceMetadata(obj, traceID, spanID); err != nil {
		return nil, err
	}
	return pkglogger.WithTraceContext(ctx, traceID, spanID, ""), nil
}

func wsIsJSONObject(raw json.RawMessage) bool {
	return strings.HasPrefix(strings.TrimSpace(string(raw)), "{")
}

func wsDecodeFrontendMetaObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, jrpc2.Errorf(jrpc2.Code(CodeInvalidParams), "decode frontend metadata: %v", err)
	}
	return obj, nil
}

// wsValidateFrontendTraceMetadata 处理wsvalidate前端trace元数据。
func wsValidateFrontendTraceMetadata(obj map[string]json.RawMessage, traceID, spanID string) error {
	if metadataTraceID, ok, err := wsFrontendStringField(obj, "_aoTraceId"); err != nil {
		return err
	} else if ok && metadataTraceID != traceID {
		return jrpc2.Errorf(jrpc2.Code(CodeInvalidParams), "mismatched _aoTraceId")
	}
	if metadataSpanID, ok, err := wsFrontendStringField(obj, "_aoSpanId"); err != nil {
		return err
	} else if ok && metadataSpanID != spanID {
		return jrpc2.Errorf(jrpc2.Code(CodeInvalidParams), "mismatched _aoSpanId")
	}
	return nil
}

func wsFrontendStringField(obj map[string]json.RawMessage, key string) (string, bool, error) {
	raw, ok := obj[key]
	if !ok {
		return "", false, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", true, jrpc2.Errorf(jrpc2.Code(CodeInvalidParams), "%s must be a string", key)
	}
	return value, true, nil
}

// wsParseFrontendTraceparent 处理wsparse前端traceparent。
func wsParseFrontendTraceparent(value string) (string, string, error) {
	parts := strings.Split(value, "-")
	if len(parts) != 4 {
		return "", "", jrpc2.Errorf(jrpc2.Code(CodeInvalidParams), "expected 4 dash-separated fields")
	}
	if parts[0] != "00" {
		return "", "", jrpc2.Errorf(jrpc2.Code(CodeInvalidParams), "unsupported version %q", parts[0])
	}
	traceID, spanID, flags := parts[1], parts[2], parts[3]
	if err := wsValidateTraceID(traceID); err != nil {
		return "", "", err
	}
	if err := wsValidateSpanID(spanID); err != nil {
		return "", "", err
	}
	if err := wsValidateTraceFlags(flags); err != nil {
		return "", "", err
	}
	return traceID, spanID, nil
}

func wsValidateTraceID(value string) error {
	if len(value) != 32 || !wsIsLowerHex(value) || wsAllZeroHex(value) {
		return jrpc2.Errorf(jrpc2.Code(CodeInvalidParams), "invalid trace id")
	}
	return nil
}

func wsValidateSpanID(value string) error {
	if len(value) != 16 || !wsIsLowerHex(value) || wsAllZeroHex(value) {
		return jrpc2.Errorf(jrpc2.Code(CodeInvalidParams), "invalid span id")
	}
	return nil
}

func wsValidateTraceFlags(value string) error {
	if len(value) != 2 || !wsIsLowerHex(value) {
		return jrpc2.Errorf(jrpc2.Code(CodeInvalidParams), "invalid flags")
	}
	return nil
}

// wsIsLowerHex 处理wsislowerhex。
func wsIsLowerHex(value string) bool {
	for _, ch := range value {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

func wsAllZeroHex(value string) bool {
	for _, ch := range value {
		if ch != '0' {
			return false
		}
	}
	return true
}

type wsChannel struct {
	conn           *websocket.Conn
	readLimitBytes int64
	sendMu         sync.Mutex
	closeOnce      sync.Once
}

func newWSChannel(conn *websocket.Conn) *wsChannel {
	return newWSChannelWithReadLimit(conn, wailsWSMaxMessageBytes)
}

func newWSChannelWithReadLimit(conn *websocket.Conn, readLimitBytes int64) *wsChannel {
	conn.SetReadLimit(readLimitBytes)
	return &wsChannel{conn: conn, readLimitBytes: readLimitBytes}
}

// Send 向底层传输写入请求。
func (c *wsChannel) Send(msg []byte) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
		return sendWSError(err)
	}
	return nil
}

// Recv 处理recv。
func (c *wsChannel) Recv() ([]byte, error) {
	for {
		msgType, msg, err := c.conn.ReadMessage()
		if err != nil {
			return nil, recvWSError(err, c.readLimitBytes)
		}
		if msgType == websocket.TextMessage || msgType == websocket.BinaryMessage {
			return msg, nil
		}
	}
}

// Close 关闭平台RPC资源。
func (c *wsChannel) Close() error {
	var err error
	c.closeOnce.Do(func() {
		err = c.conn.Close()
	})
	if err != nil {
		return sendWSError(err)
	}
	return nil
}

func recvWSError(err error, readLimitBytes int64) error {
	if errors.Is(err, websocket.ErrReadLimit) {
		return jrpc2.Errorf(jrpc2.Code(CodeInvalidParams), "wails websocket message size limit exceeded: max %d bytes: %v", readLimitBytes, err)
	}
	if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) || isExpectedCloseErr(err) {
		return io.EOF
	}
	return err
}

func sendWSError(err error) error {
	if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) || isExpectedCloseErr(err) {
		return channel.ErrClosed
	}
	return err
}
