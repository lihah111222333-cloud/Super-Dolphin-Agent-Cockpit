package rpc

import (
	"testing"

	"github.com/creachadair/jrpc2"
)

func TestOnConnectReplaysActiveServers(t *testing.T) {
	server := &Server{active: make(map[*jrpc2.Server]struct{})}
	active := new(jrpc2.Server)
	server.addActive(active)

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

func TestNotifyConnectedInvokesOnConnectHooks(t *testing.T) {
	server := &Server{active: make(map[*jrpc2.Server]struct{})}
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
