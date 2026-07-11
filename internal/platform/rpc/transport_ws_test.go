package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	"github.com/gorilla/websocket"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/observability"
)

func TestWSHandlerNotifiesUIConnectHooks(t *testing.T) {
	server := newTestServer()
	got := make(chan string, 1)
	server.OnConnectUI(func(current *jrpc2.Server) {
		got <- server.PeerKind(current)
	})

	httpServer := httptest.NewServer(WSHandler(server, nil))
	defer httpServer.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(httpServer.URL, "http"), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	select {
	case peerKind := <-got:
		if peerKind != dto.PeerKindUI {
			t.Fatalf("PeerKind = %q, want %q", peerKind, dto.PeerKindUI)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for UI connect hook")
	}
}

func TestWSHandlerRejectsMessagesOverReadLimit(t *testing.T) {
	server := newTestServer()
	conn := dialWSTestServer(t, WSHandler(server, nil))
	defer conn.Close()

	oversized := strings.Repeat("x", int(wailsWSMaxMessageBytes)+1)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(oversized)); err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}

	_, _, err := readWSMessageWithDeadline(conn)
	if err == nil {
		t.Fatal("server accepted oversized websocket message; want close")
	}
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) {
		t.Fatalf("ReadMessage() error = %v, want websocket close error", err)
	}
	if closeErr.Code != websocket.CloseMessageTooBig {
		t.Fatalf("close code = %d (%q), want %d", closeErr.Code, closeErr.Text, websocket.CloseMessageTooBig)
	}
}

func TestWSChannelReadLimitErrorIsExplicit(t *testing.T) {
	err := recvWSError(websocket.ErrReadLimit, wailsWSMaxMessageBytes)
	if err == nil {
		t.Fatal("recvWSError() = nil, want explicit read limit error")
	}
	if !strings.Contains(err.Error(), "wails websocket message size limit exceeded") {
		t.Fatalf("recvWSError() = %q, want explicit wails websocket limit message", err.Error())
	}
}

func TestWSHandlerRejectsConnectionsOverLimit(t *testing.T) {
	server := newTestServer()
	connected := make(chan struct{}, wailsWSMaxActiveConnections)
	server.OnConnectUI(func(*jrpc2.Server) {
		connected <- struct{}{}
	})

	httpServer := httptest.NewServer(WSHandler(server, nil))
	defer httpServer.Close()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")

	conns := dialActiveWSTestConnections(t, wsURL, wailsWSMaxActiveConnections)
	defer closeWSTestConnections(conns)
	waitForWSTestConnections(t, connected, wailsWSMaxActiveConnections)

	extraConn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		extraConn.Close()
		t.Fatal("Dial() over limit succeeded; want fail-fast rejection")
	}
	assertWSConnectionLimitResponse(t, resp, err)
}

func dialActiveWSTestConnections(t *testing.T, wsURL string, count int) []*websocket.Conn {
	t.Helper()

	conns := make([]*websocket.Conn, 0, count)
	for range count {
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			closeWSTestConnections(conns)
			t.Fatalf("Dial() within limit error = %v", err)
		}
		conns = append(conns, conn)
	}
	return conns
}

func closeWSTestConnections(conns []*websocket.Conn) {
	for _, conn := range conns {
		_ = conn.Close()
	}
}

func waitForWSTestConnections(t *testing.T, connected <-chan struct{}, count int) {
	t.Helper()

	for range count {
		select {
		case <-connected:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for UI websocket connections to become active")
		}
	}
}

func assertWSConnectionLimitResponse(t *testing.T, resp *http.Response, dialErr error) {
	t.Helper()

	if resp == nil {
		t.Fatalf("Dial() over limit response is nil, error = %v", dialErr)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("Dial() over limit status = %d, want %d", resp.StatusCode, http.StatusServiceUnavailable)
	}
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		t.Fatalf("ReadAll(over limit response) error = %v", readErr)
	}
	if !strings.Contains(string(body), "wails websocket connection limit reached") {
		t.Fatalf("Dial() over limit body = %q, want clear connection limit error", string(body))
	}
}

func TestWSHandlerCorrelatesFrontendTraceThroughDispatch(t *testing.T) {
	svc := newWSTraceService(t)
	var sawTrace observability.TraceContext
	server := newWSTraceServer(t, svc, &sawTrace)
	conn := dialWSTestServer(t, WSHandler(server, nil))
	defer conn.Close()

	rsp := writeTraceWSRequest(t, conn)
	assertWSRPCResponseOK(t, rsp)
	assertWSHandlerTrace(t, sawTrace)
	assertWSTraceEvents(t, svc)
}

func newWSTraceService(t *testing.T) *observability.Service {
	t.Helper()
	cfg, err := observability.ParseConfig(observability.EnvMap{"OBS_TRACING_ENABLED": "true"})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	return observability.NewService(cfg)
}

func newWSTraceServer(t *testing.T, svc *observability.Service, sawTrace *observability.TraceContext) *Server {
	t.Helper()
	server := NewServer(Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}, TraceRecorder: testRPCTraceRecorder{svc}})
	server.Register(handler.Map{"thread/echo": StrictHandler(func(ctx context.Context, req struct {
		ThreadID string `json:"threadId"`
	}) (map[string]string, error) {
		if req.ThreadID != "thread-1" {
			t.Fatalf("ThreadID = %q, want thread-1", req.ThreadID)
		}
		var ok bool
		*sawTrace, ok = observability.TraceFromContext(ctx)
		if !ok {
			t.Fatal("handler observability trace context missing")
		}
		return map[string]string{"threadId": req.ThreadID}, nil
	})})
	return server
}

func dialWSTestServer(t *testing.T, h http.Handler) *websocket.Conn {
	t.Helper()
	httpServer := httptest.NewServer(h)
	t.Cleanup(httpServer.Close)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(httpServer.URL, "http"), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	return conn
}

func readWSMessageWithDeadline(conn *websocket.Conn) (int, []byte, error) {
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		return 0, nil, err
	}
	return conn.ReadMessage()
}

func writeTraceWSRequest(t *testing.T, conn *websocket.Conn) wsRPCResponse {
	t.Helper()
	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	const frontendSpanID = "00f067aa0ba902b7"
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "thread/echo",
		"params": map[string]any{
			"threadId":       "thread-1",
			"_aoTraceparent": "00-" + traceID + "-" + frontendSpanID + "-01",
			"_aoTraceId":     traceID,
			"_aoSpanId":      frontendSpanID,
			"_aoClientKind":  "web-debug-shim",
			"_aoClientRoute": "/",
		},
	}
	if err := conn.WriteJSON(req); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}
	var rsp wsRPCResponse
	if err := conn.ReadJSON(&rsp); err != nil {
		t.Fatalf("ReadJSON() error = %v", err)
	}
	return rsp
}

func assertWSRPCResponseOK(t *testing.T, rsp wsRPCResponse) {
	t.Helper()
	if rsp.Error != nil {
		t.Fatalf("rpc error = %+v", *rsp.Error)
	}
	if string(rsp.Result) != `{"threadId":"thread-1"}` {
		t.Fatalf("result = %s, want thread id response", rsp.Result)
	}
}

func assertWSHandlerTrace(t *testing.T, sawTrace observability.TraceContext) {
	t.Helper()
	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	const frontendSpanID = "00f067aa0ba902b7"
	if sawTrace.TraceID != traceID || sawTrace.ParentSpanID != frontendSpanID || sawTrace.SpanID == "" || sawTrace.SpanID == frontendSpanID {
		t.Fatalf("handler trace = %#v, want backend child of frontend span", sawTrace)
	}
}

func assertWSTraceEvents(t *testing.T, svc *observability.Service) {
	t.Helper()
	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	events := svc.Query(context.Background(), observability.Query{TraceID: traceID}).Events
	if len(events) != 2 {
		t.Fatalf("trace event count = %d, want dispatch start/done: %#v", len(events), events)
	}
	assertWSTraceEventKinds(t, events)
	for _, event := range events {
		assertWSTraceEvent(t, event)
	}
}

func assertWSTraceEventKinds(t *testing.T, events []observability.TraceEvent) {
	t.Helper()
	if events[0].Kind != "backend.rpc.dispatch.start" || events[1].Kind != "backend.rpc.dispatch.done" {
		t.Fatalf("event kinds = %q, %q; want backend dispatch start/done", events[0].Kind, events[1].Kind)
	}
}

func assertWSTraceEvent(t *testing.T, event observability.TraceEvent) {
	t.Helper()
	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	const frontendSpanID = "00f067aa0ba902b7"
	if event.TraceID != traceID || event.ParentSpanID != frontendSpanID || event.SpanID == "" || event.SpanID == frontendSpanID {
		t.Fatalf("event trace = %#v, want backend child of frontend span", event)
	}
	if event.Method != "thread/echo" {
		t.Fatalf("event method = %q, want thread/echo", event.Method)
	}
	assertWSParamKeysExcludeFrontendMeta(t, event)
}

func assertWSParamKeysExcludeFrontendMeta(t *testing.T, event observability.TraceEvent) {
	t.Helper()
	keys, _ := event.Metadata["param_keys"].([]string)
	for _, key := range keys {
		if strings.HasPrefix(key, "_ao") {
			t.Fatalf("event param keys leaked frontend metadata: %#v", keys)
		}
	}
}

type wsRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}
