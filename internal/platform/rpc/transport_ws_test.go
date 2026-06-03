package rpc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	"github.com/gorilla/websocket"
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
	server := NewServer(Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}, Observability: svc})
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
