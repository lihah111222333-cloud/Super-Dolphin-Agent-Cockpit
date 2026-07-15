package bootstrap

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	jrpcserver "github.com/creachadair/jrpc2/server"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
)

func TestNormalizeConfig_DoesNotReviveAgentIDFromBootSnapshot(t *testing.T) {
	cfg, _, err := normalizeConfig(Config{
		BootSnapshot: json.RawMessage(`{"agent_id":"boot-agent","thread_id":"thread-1"}`),
	})
	if err != nil {
		t.Fatalf("normalizeConfig() error = %v", err)
	}
	if cfg.AgentID != "" {
		t.Fatalf("AgentID = %q, want empty", cfg.AgentID)
	}
	if cfg.ThreadID != "thread-1" {
		t.Fatalf("ThreadID = %q, want thread-1", cfg.ThreadID)
	}
}

func TestContextPayloadFromSnapshot_IgnoresBootSnapshotAgentID(t *testing.T) {
	client := &Client{
		cfg: Config{AgentID: "", ThreadID: "thread-cfg"},
		boot: bootSnapshot{
			AgentID:  "boot-agent",
			ThreadID: "thread-boot",
		},
		instanceID: "instance-1",
	}
	payload := contextPayloadFromSnapshot(client, mcpdto.ScopeThreadBinding)
	if got := payload["agent_id"]; got != "" {
		t.Fatalf("payload.agent_id = %#v, want empty", got)
	}
	if got := payload["thread_id"]; got != "thread-boot" {
		t.Fatalf("payload.thread_id = %#v, want thread-boot", got)
	}
}

func TestRegisterConn_ClearsAgentIDForSharedService(t *testing.T) {
	var captured mcpdto.RegisterRequest
	local := jrpcserver.NewLocal(handler.Map{
		mcpdto.MethodRegister: platformrpc.StrictHandler(func(_ context.Context, req mcpdto.RegisterRequest) (mcpdto.RegisterResponse, error) {
			captured = req
			return mcpdto.RegisterResponse{
				InstanceID:            req.InstanceID,
				Generation:            1,
				HeartbeatIntervalMs:   1000,
				HeartbeatTimeoutMs:    500,
				ServerProtocolVersion: mcpdto.ProtocolVersion,
			}, nil
		}),
	}, &jrpcserver.LocalOptions{Server: &jrpc2.ServerOptions{}})
	defer local.Close()

	client := &Client{
		cfg: Config{
			InstanceID:   "instance-1",
			BinaryName:   "mcp-orch",
			AgentID:      "agent-42",
			ThreadID:     "thread-42",
			ClientKind:   "orch",
			SessionToken: "token",
			BootID:       "boot-1",
		},
		instanceID: "instance-1",
	}
	if _, err := client.registerConn(context.Background(), local.Client); err != nil {
		t.Fatalf("registerConn() error = %v", err)
	}
	if captured.AgentID != "" {
		t.Fatalf("register request agent_id = %q, want empty", captured.AgentID)
	}
	if captured.ThreadID != "thread-42" {
		t.Fatalf("register request thread_id = %q, want thread-42", captured.ThreadID)
	}
}
