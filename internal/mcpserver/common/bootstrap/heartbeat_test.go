package bootstrap

import (
	"context"
	"testing"

	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	jrpcserver "github.com/creachadair/jrpc2/server"
)

func TestSendHeartbeatTreatsLeaseStaleAsRefreshSignal(t *testing.T) {
	local := jrpcserver.NewLocal(handler.Map{
		mcpdto.MethodHeartbeat: platformrpc.StrictHandler(func(context.Context, mcpdto.HeartbeatRequest) (mcpdto.HeartbeatResponse, error) {
			return mcpdto.HeartbeatResponse{}, jrpc2.Errorf(jrpc2.Code(mcpdto.ErrCodeLeaseStale), "lease stale")
		}),
	}, &jrpcserver.LocalOptions{Server: &jrpc2.ServerOptions{}})
	defer local.Close()

	client := New(Config{InstanceID: "inst-stale", BinaryName: "mcp-lsp", ClientKind: mcpdto.ClientKindLSP})
	client.conn = local.Client
	client.lease = mcpdto.LeaseKey{InstanceID: "inst-stale", Generation: 7}

	rejected, next, err := client.sendHeartbeat(context.Background(), 0)
	if err != nil {
		t.Fatalf("sendHeartbeat() error = %v, want nil so runHeartbeat refreshes the lease", err)
	}
	if !rejected {
		t.Fatal("sendHeartbeat() rejected = false, want true")
	}
	if next != 0 {
		t.Fatalf("next heartbeat = %v, want 0", next)
	}
}
