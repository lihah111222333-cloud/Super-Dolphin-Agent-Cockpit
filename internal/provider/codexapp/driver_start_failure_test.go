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

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	codexprotocol "github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp/protocol"
	"github.com/gorilla/websocket"
)

func TestStartSessionFailsFastAndCleansUpOnStartupPermanentError(t *testing.T) {
	serverURL := startStartupPermanentErrorServer(t)
	var released atomic.Int32
	d := &driver{
		pool:    newSingleURLPoolForTest(t, serverURL),
		mirror:  &recordingSkillMirrorReconciler{},
		manager: &ServerManager{},
		prepareTools: func(context.Context, contract.CodexToolSurfaceScope) ([]codexprotocol.DynamicToolSchema, error) {
			return []codexprotocol.DynamicToolSchema{{Name: "grep", InputSchema: json.RawMessage(`{"type":"object"}`)}}, nil
		},
		releaseTools: func(contract.CodexToolSurfaceScope) error {
			released.Add(1)
			return nil
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	startedAt := time.Now()
	session, err := d.StartSession(ctx, dto.StartSessionRequest{
		Provider:      "codex",
		AgentID:       "agent-startup-auth-fail",
		CWD:           t.TempDir(),
		StartAssembly: validStartAssemblyForTest(),
	})
	elapsed := time.Since(startedAt)

	if err == nil || !strings.Contains(err.Error(), "API Error: Unable to connect to API") {
		t.Fatalf("StartSession() error = %v, want startup API error", err)
	}
	if elapsed > time.Second {
		t.Fatalf("StartSession() returned after %s, want fail-fast before context deadline", elapsed)
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
			notification := mustJSON(map[string]any{
				"jsonrpc": "2.0",
				"method":  "connection.dead",
				"params": mustJSON(map[string]any{
					"error": "API Error: Unable to connect to API (ConnectionRefused)",
				}),
			})
			if err := conn.WriteMessage(websocket.TextMessage, notification); err != nil {
				return
			}
		default:
			if !writeStartupPermanentErrorResponse(t, conn, msg.ID, mustJSON(map[string]any{"ok": true})) {
				return
			}
		}
	}
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
