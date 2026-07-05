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

// defaultWSUpgrader 是 WebSocket 升级器，测试可替换其包级配置。
var defaultWSUpgrader = websocket.Upgrader{}

const (
	// wailsWSMaxMessageBytes 限制单条 UI WebSocket 消息大小。
	wailsWSMaxMessageBytes      int64 = 16 * 1024 * 1024
	wailsWSMaxActiveConnections       = 32
)

var _ channel.Channel = (*wsChannel)(nil)

// WSHandler 将 WebSocket 连接桥接为 jrpc2 channel。
// UI WebSocket 会计入并发槽位，并在连接建立后触发 Server 的 UI 连接回调。
func WSHandler(server *Server, opts *jrpc2.ServerOptions) http.Handler {
	var mux jrpc2.Assigner = wsDispatchAssigner{server: server}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		releaseSlot, err := reserveWailsWSConnectionSlot(server)
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer func() {
			if err := releaseSlot(); err != nil {
				pkglogger.Get().Error("rpc: failed to release UI websocket slot", "error", err)
			}
		}()
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

// reserveWailsWSConnectionSlot 为 Wails UI WebSocket 预留槽位，server 为空时返回空释放函数。
func reserveWailsWSConnectionSlot(server *Server) (func() error, error) {
	if server == nil {
		return noopWailsWSConnectionSlot, nil
	}
	if err := server.reserveUIWebSocketSlot(); err != nil {
		return nil, err
	}
	return server.releaseUIWebSocketSlot, nil
}

// noopWailsWSConnectionSlot 是无 server 场景的空释放函数。
func noopWailsWSConnectionSlot() error {
	_ = struct{}{}
	return nil
}

// wsDispatchAssigner 把 WebSocket RPC 调用转给 Server.Dispatch。
type wsDispatchAssigner struct {
	server *Server
}

// Assign 为 WebSocket 请求创建调用本地 Dispatch 的 handler。
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

// prepareWSDispatchParams 提取前端 trace context，并移除内部 _ao 元数据字段。
func prepareWSDispatchParams(ctx context.Context, method string, raw json.RawMessage) (context.Context, json.RawMessage, error) {
	traceCtx, err := wsFrontendTraceContext(ctx, raw)
	if err != nil {
		return nil, nil, err
	}
	return traceCtx, stripWSFrontendMeta(method, raw), nil
}

// stripWSFrontendMeta 删除前端传入的内部 trace 元数据，避免流入业务 handler。
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

// stripWSJSONFields 从 JSON object 中删除匹配字段，非 object 或编码失败时返回原始值。
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

// wsFrontendTraceContext 从前端 _aoTraceparent 构造后端 trace context。
// metadata 不一致会作为 invalid params 返回，避免伪造 trace 关联。
func wsFrontendTraceContext(ctx context.Context, raw json.RawMessage) (context.Context, error) {
	if !wsIsJSONObject(raw) {
		return ctx, nil
	}
	obj, err := wsDecodeFrontendMetaObject(raw)
	if err != nil {
		return nil, err
	}
	trace, ok, err := pkglogger.ExtractAOTraceCarrierJSON(obj)
	if err != nil {
		return nil, jrpc2.Errorf(jrpc2.Code(CodeInvalidParams), "%v", err)
	}
	if !ok {
		return ctx, nil
	}
	return pkglogger.WithTraceContext(ctx, trace.TraceID, trace.SpanID, ""), nil
}

// wsIsJSONObject 快速判断 raw params 是否可能是 JSON object。
func wsIsJSONObject(raw json.RawMessage) bool {
	return strings.HasPrefix(strings.TrimSpace(string(raw)), "{")
}

// wsDecodeFrontendMetaObject 解码前端 trace metadata 对象。
func wsDecodeFrontendMetaObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, jrpc2.Errorf(jrpc2.Code(CodeInvalidParams), "decode frontend metadata: %v", err)
	}
	return obj, nil
}

// wsChannel 把 gorilla websocket 适配为 jrpc2 channel.Channel。
// sendMu 保证并发 Send 不会同时写同一个 websocket 连接。
type wsChannel struct {
	conn           *websocket.Conn
	readLimitBytes int64
	sendMu         sync.Mutex
	closeOnce      sync.Once
}

// newWSChannel 使用默认消息大小限制创建 WebSocket channel。
func newWSChannel(conn *websocket.Conn) *wsChannel {
	return newWSChannelWithReadLimit(conn, wailsWSMaxMessageBytes)
}

// newWSChannelWithReadLimit 创建带自定义读限制的 WebSocket channel。
func newWSChannelWithReadLimit(conn *websocket.Conn, readLimitBytes int64) *wsChannel {
	conn.SetReadLimit(readLimitBytes)
	return &wsChannel{conn: conn, readLimitBytes: readLimitBytes}
}

// Send 向 WebSocket 写入一条 jrpc2 消息，并把正常关闭映射为 channel.ErrClosed。
func (c *wsChannel) Send(msg []byte) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
		return sendWSError(err)
	}
	return nil
}

// Recv 读取下一条文本或二进制 WebSocket 消息。
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

// Close 幂等关闭底层 WebSocket 连接。
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

// recvWSError 把 WebSocket 读取错误映射为 jrpc2/channel 期望的错误形态。
func recvWSError(err error, readLimitBytes int64) error {
	if errors.Is(err, websocket.ErrReadLimit) {
		return jrpc2.Errorf(jrpc2.Code(CodeInvalidParams), "wails websocket message size limit exceeded: max %d bytes: %v", readLimitBytes, err)
	}
	if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) || isExpectedCloseErr(err) {
		return io.EOF
	}
	return err
}

// sendWSError 把 WebSocket 发送错误映射为 channel.ErrClosed 或原始错误。
func sendWSError(err error) error {
	if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) || isExpectedCloseErr(err) {
		return channel.ErrClosed
	}
	return err
}
