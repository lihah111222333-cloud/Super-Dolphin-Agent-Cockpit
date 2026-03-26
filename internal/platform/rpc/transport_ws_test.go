package rpc

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/creachadair/jrpc2"
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
