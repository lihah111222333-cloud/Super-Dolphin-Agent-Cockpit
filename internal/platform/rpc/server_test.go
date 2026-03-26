package rpc

import (
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/creachadair/jrpc2"
)

func TestOnConnectReplaysActiveServers(t *testing.T) {
	server := &Server{active: make(map[*jrpc2.Server]string)}
	active := new(jrpc2.Server)
	server.addActive(active, dto.PeerKindTool)

	got := make(chan *jrpc2.Server, 1)
	server.OnConnect(func(current *jrpc2.Server) {
		got <- current
	})

	select {
	case current := <-got:
		if current != active {
			t.Fatalf("OnConnect replayed %p, want %p", current, active)
		}
	default:
		t.Fatal("OnConnect did not replay active server")
	}
}

func TestOnConnectUIReplaysOnlyUIActiveServers(t *testing.T) {
	server := &Server{active: make(map[*jrpc2.Server]string)}
	server.addActive(new(jrpc2.Server), dto.PeerKindTool)
	active := new(jrpc2.Server)
	server.addActive(active, dto.PeerKindUI)

	got := make(chan *jrpc2.Server, 2)
	server.OnConnectUI(func(current *jrpc2.Server) {
		got <- current
	})

	select {
	case current := <-got:
		if current != active {
			t.Fatalf("OnConnectUI replayed %p, want %p", current, active)
		}
	default:
		t.Fatal("OnConnectUI did not replay active UI server")
	}

	select {
	case current := <-got:
		t.Fatalf("OnConnectUI replayed unexpected server %p", current)
	default:
	}
}

func TestNotifyConnectedInvokesOnConnectHooks(t *testing.T) {
	server := &Server{active: make(map[*jrpc2.Server]string)}
	connected := new(jrpc2.Server)

	got := make(chan *jrpc2.Server, 1)
	server.OnConnect(func(current *jrpc2.Server) {
		got <- current
	})
	server.notifyConnected(connected)

	select {
	case current := <-got:
		if current != connected {
			t.Fatalf("notifyConnected passed %p, want %p", current, connected)
		}
	default:
		t.Fatal("notifyConnected did not invoke hook")
	}
}
