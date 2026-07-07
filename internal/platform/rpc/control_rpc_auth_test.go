package rpc

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimesafe"
	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"
	"github.com/creachadair/jrpc2/handler"
)

func TestControlRPCPeerIsNotActiveBeforeRegisterAuth(t *testing.T) {
	server := newTestServer()
	server.setControlRPCAuthToken("secret")
	server.Register(handler.Map{
		mcpdto.MethodRegister: StrictHandler(func(context.Context, mcpdto.RegisterRequest) (map[string]bool, error) {
			return map[string]bool{"ok": true}, nil
		}),
	})

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	runtimesafe.SafeGo(context.Background(), nil, "rpc.controlAuth.testServeConn", func(context.Context) {
		defer close(done)
		server.serveConn(context.Background(), channel.Line(serverConn, serverConn), &wg)
	})
	t.Cleanup(func() {
		_ = clientConn.Close()
		<-done
	})

	client := jrpc2.NewClient(channel.Line(clientConn, clientConn), nil)
	defer client.Close()

	deadline := time.After(100 * time.Millisecond)
	for {
		if got := len(server.snapshotActive()); got != 0 {
			t.Fatalf("active peers before ctl/register = %d, want 0", got)
		}
		select {
		case <-deadline:
			goto register
		case <-time.After(10 * time.Millisecond):
		}
	}

register:
	var out map[string]bool
	if err := client.CallResult(context.Background(), mcpdto.MethodRegister, mcpdto.RegisterRequest{SessionToken: "secret"}, &out); err != nil {
		t.Fatalf("ctl/register error = %v", err)
	}
	if got := len(server.snapshotActive()); got != 1 {
		t.Fatalf("active peers after ctl/register = %d, want 1", got)
	}
}
