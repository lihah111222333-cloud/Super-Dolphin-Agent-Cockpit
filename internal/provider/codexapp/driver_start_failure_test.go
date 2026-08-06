package codexapp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	codexprotocol "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/codexapp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/pkg/skillmetrics"
)

func TestStartSessionFailsFastAndCleansUpOnStartupPermanentError(t *testing.T) {
	const startupPermanentErrorTimeout = 2 * time.Minute
	const startupPermanentErrorFailFastMax = 5 * time.Second
	serverURL := startStartupPermanentErrorServer(t)
	var released atomic.Int32
	d := &driver{
		approvals:    testApprovalManager(),
		pool:         newSingleURLPoolForTest(t, serverURL),
		mirror:       &recordingSkillMirrorReconciler{},
		manager:      &ServerManager{},
		skillMetrics: skillmetrics.NewRegistry(),
		prepareTools: func(context.Context, contract.CodexToolSurfaceScope) ([]codexprotocol.DynamicToolSchema, error) {
			return []codexprotocol.DynamicToolSchema{{Name: "grep", InputSchema: json.RawMessage(`{"type":"object"}`)}}, nil
		},
		releaseTools: func(contract.CodexToolSurfaceScope) error {
			released.Add(1)
			return nil
		},
	}
	req := dto.StartSessionRequest{
		Provider:      "codex",
		AgentID:       "agent-startup-auth-fail",
		CWD:           t.TempDir(),
		Model:         "gpt-5",
		StartAssembly: validStartAssemblyForTest(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), startupPermanentErrorTimeout)
	defer cancel()

	startedAt := time.Now()
	session, err := d.StartSession(ctx, req)
	elapsed := time.Since(startedAt)
	if err == nil || !strings.Contains(err.Error(), "API Error: Unable to connect to API") {
		t.Fatalf("StartSession() error = %v, want startup API error", err)
	}
	if elapsed > startupPermanentErrorFailFastMax {
		t.Fatalf("StartSession() returned after %s, want fail-fast before RPC timeout", elapsed)
	}
	if session != nil {
		t.Fatalf("StartSession() session = %#v, want nil after startup failure", session)
	}
	if got := released.Load(); got != 1 {
		t.Fatalf("releaseTools calls = %d, want cleanupFailedSession/ForceStop release once", got)
	}
}

func startStartupPermanentErrorServer(t *testing.T) string {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		serveStartupPermanentErrorConn(t, conn)
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

func serveStartupPermanentErrorConn(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	for {
		_, rawBytes, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var msg jsonRPCMessage
		if err := json.Unmarshal(rawBytes, &msg); err != nil || len(msg.ID) == 0 {
			continue
		}
		switch msg.Method {
		case "initialize":
			if !writeStartupPermanentErrorResponse(t, conn, msg.ID, mustJSON(map[string]any{"codexHome": t.TempDir()})) {
				return
			}
		case "thread/start":
			if !writeStartupPermanentErrorRPCError(t, conn, msg.ID, -32000, "API Error: Unable to connect to API (ConnectionRefused)") {
				return
			}
			return
		default:
			if !writeStartupPermanentErrorResponse(t, conn, msg.ID, mustJSON(map[string]any{"ok": true})) {
				return
			}
		}
	}
}

func writeStartupPermanentErrorRPCError(t *testing.T, conn *websocket.Conn, id json.RawMessage, code int, message string) bool {
	t.Helper()
	resp, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(append([]byte(nil), id...)),
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
	if err != nil {
		t.Fatalf("marshal error response: %v", err)
	}
	return conn.WriteMessage(websocket.TextMessage, resp) == nil
}

func writeStartupPermanentErrorResponse(t *testing.T, conn *websocket.Conn, id json.RawMessage, result json.RawMessage) bool {
	t.Helper()
	resp, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(append([]byte(nil), id...)),
		"result":  json.RawMessage(append([]byte(nil), result...)),
	})
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	return conn.WriteMessage(websocket.TextMessage, resp) == nil
}
