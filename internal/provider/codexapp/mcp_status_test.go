package codexapp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestPollMCPStatus_FindsAllServers(t *testing.T) {
	t.Parallel()

	transport := newMCPStatusTestTransport(t, []json.RawMessage{
		mcpStatusResult("mcp-lsp", "mcp-orch"),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := pollMCPStatus(ctx, transport, []string{"mcp-lsp", "mcp-orch"}, 5*time.Millisecond)
	if err != nil {
		t.Fatalf("pollMCPStatus() error = %v, want nil", err)
	}
}

func TestPollMCPStatus_Timeout(t *testing.T) {
	t.Parallel()

	transport := newMCPStatusTestTransport(t, []json.RawMessage{
		mcpStatusResult(),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	err := pollMCPStatus(ctx, transport, []string{"mcp-lsp", "mcp-orch"}, 5*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "mcp status poll timeout, still waiting for: [mcp-lsp mcp-orch]") {
		t.Fatalf("pollMCPStatus() error = %v, want timeout with pending server list", err)
	}
}

func TestPollMCPStatus_PartialThenComplete(t *testing.T) {
	t.Parallel()

	transport := newMCPStatusTestTransport(t, []json.RawMessage{
		mcpStatusResult("mcp-lsp"),
		mcpStatusResult("mcp-lsp", "mcp-orch"),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := pollMCPStatus(ctx, transport, []string{"mcp-lsp", "mcp-orch"}, 5*time.Millisecond)
	if err != nil {
		t.Fatalf("pollMCPStatus() error = %v, want nil", err)
	}
}

func newMCPStatusTestTransport(t *testing.T, results []json.RawMessage) *transport {
	t.Helper()

	var mu sync.Mutex
	index := 0
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, rawBytes, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg jsonRPCMessage
			if err := json.Unmarshal(rawBytes, &msg); err != nil || len(msg.ID) == 0 {
				continue
			}
			result := mustJSON(map[string]any{"ok": true})
			if msg.Method == "mcpServerStatus/list" {
				mu.Lock()
				if len(results) > 0 {
					at := index
					if at >= len(results) {
						at = len(results) - 1
					}
					result = results[at]
					index++
				}
				mu.Unlock()
			}
			resp, err := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      json.RawMessage(append([]byte(nil), msg.ID...)),
				"result":  json.RawMessage(append([]byte(nil), result...)),
			})
			if err != nil {
				t.Fatalf("marshal response: %v", err)
			}
			if err := conn.WriteMessage(websocket.TextMessage, resp); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	transport, err := newTransport(ctx, "ws"+strings.TrimPrefix(server.URL, "http"))
	if err != nil {
		t.Fatalf("newTransport() error = %v", err)
	}
	readCtx, readCancel := context.WithCancel(context.Background())
	go transport.ReadLoop(readCtx, func(string, json.RawMessage) {})
	t.Cleanup(readCancel)
	t.Cleanup(func() { _ = transport.Kill() })
	return transport
}

func mcpStatusResult(names ...string) json.RawMessage {
	data := make([]map[string]any, 0, len(names))
	for _, name := range names {
		data = append(data, map[string]any{"name": name})
	}
	return mustJSON(map[string]any{"data": data})
}
