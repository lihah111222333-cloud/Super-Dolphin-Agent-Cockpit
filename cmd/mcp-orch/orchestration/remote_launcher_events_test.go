package orchestration

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	"github.com/kelindar/event"
	"github.com/stretchr/testify/require"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/launcherwire"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
)

func TestRemoteLauncher_RegistersBeforeThreadStart(t *testing.T) {
	t.Setenv("GO_AGENT_CTL_SESSION_TOKEN", "session-secret")
	var registered int32
	registerReq := make(chan mcpdto.RegisterRequest, 1)
	addr, _ := startRPCServer(t, handler.Map{
		mcpdto.MethodRegister: handler.New(func(_ context.Context, req mcpdto.RegisterRequest) (mcpdto.RegisterResponse, error) {
			registerReq <- req
			if req.SessionToken != "session-secret" {
				return mcpdto.RegisterResponse{}, jrpc2.Errorf(jrpc2.Code(-31002), "control rpc unauthorized: invalid session token")
			}
			atomic.StoreInt32(&registered, 1)
			return launcherRegisterResponse(req, 60000), nil
		}),
		launcherwire.MethodThreadStart: handler.New(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			if atomic.LoadInt32(&registered) == 0 {
				return nil, jrpc2.Errorf(jrpc2.Code(-31002), "control rpc unauthorized: register with a valid session token first")
			}
			return map[string]any{"thread": map[string]any{"id": "thread-1"}}, nil
		}),
	})
	launcher := NewRemoteLauncher(addr).(*remoteLauncher)
	t.Cleanup(func() { _ = launcher.Close() })

	if _, err := launcher.Launch(context.Background(), &agentRuntime{id: "agent-1"}, LaunchRequest{Command: []string{"ignored"}}); err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	got := <-registerReq
	if got.ClientKind != mcpdto.ClientKindCustom || got.PeerKind != mcpdto.PeerKindTool || got.BinaryName != remoteLauncherBinaryName {
		t.Fatalf("register request = %+v", got)
	}
}

func TestRemoteLauncher_HeartbeatKeepsControlLeaseAlive(t *testing.T) {
	t.Setenv("GO_AGENT_CTL_SESSION_TOKEN", "session-secret")
	heartbeat := make(chan mcpdto.HeartbeatRequest, 1)
	addr, _ := startRPCServer(t, handler.Map{
		mcpdto.MethodRegister: handler.New(func(_ context.Context, req mcpdto.RegisterRequest) (mcpdto.RegisterResponse, error) {
			if req.SessionToken != "session-secret" {
				return mcpdto.RegisterResponse{}, jrpc2.Errorf(jrpc2.Code(-31002), "control rpc unauthorized: invalid session token")
			}
			return launcherRegisterResponse(req, 5), nil
		}),
		mcpdto.MethodHeartbeat: handler.New(func(_ context.Context, req mcpdto.HeartbeatRequest) (mcpdto.HeartbeatResponse, error) {
			heartbeat <- req
			return mcpdto.HeartbeatResponse{OK: true, ConfigVersion: 1, NextHeartbeatMs: 1000}, nil
		}),
		launcherwire.MethodThreadStart: handler.New(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return map[string]any{"thread": map[string]any{"id": "thread-1"}}, nil
		}),
	})
	launcher := NewRemoteLauncher(addr).(*remoteLauncher)
	t.Cleanup(func() { _ = launcher.Close() })

	if _, err := launcher.Launch(context.Background(), &agentRuntime{id: "agent-1"}, LaunchRequest{Command: []string{"ignored"}}); err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	select {
	case got := <-heartbeat:
		if got.Status != mcpdto.StatusActive || got.Generation != 1 || got.HeartbeatSeq == 0 {
			t.Fatalf("heartbeat = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("remote launcher did not send heartbeat")
	}
}

func TestRemoteLauncher_TurnCompletedNotificationClearsRemoteBusyState(t *testing.T) {
	t.Setenv("GO_AGENT_CTL_SESSION_TOKEN", "session-secret")
	notify := make(chan *jrpc2.Server, 1)
	addr, _ := startPushRPCServer(t, handler.Map{
		"turn/start": handler.New(func(ctx context.Context, _ map[string]any) (map[string]any, error) {
			notify <- jrpc2.ServerFromContext(ctx)
			return map[string]any{"turn_id": "remote-turn-1"}, nil
		}),
	})
	launcher := NewRemoteLauncher(addr)
	svc := NewService(silentLogger(), event.NewDispatcher(), launcher, nil, nil, nil)
	remote, ok := launcher.(*remoteLauncher)
	require.True(t, ok)
	t.Cleanup(func() { _ = remote.Close() })

	svc.mu.Lock()
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateIdle
	agent.remoteThreadID = "thread-1"
	svc.agents[agent.id] = agent
	svc.mu.Unlock()

	err := svc.SubmitTurn(context.Background(), TurnSubmission{
		AgentID: "agent-1",
		Inputs:  []shareddto.InputItem{{Type: "text", Content: "hi"}},
	})
	require.NoError(t, err)
	require.Equal(t, agentdto.StateTurnRunning, agent.state)
	require.Equal(t, "remote-turn-1", agent.activeTurnID)

	var server *jrpc2.Server
	select {
	case server = <-notify:
	case <-time.After(time.Second):
		t.Fatal("turn/start handler did not capture server")
	}
	require.NoError(t, server.Notify(context.Background(), "turn/completed", turndto.TurnCompleted{
		TurnHeader: shareddto.TurnHeader{
			AgentHeader: shareddto.AgentHeader{
				ThreadHeader: shareddto.ThreadHeader{ThreadID: "thread-1"},
				AgentID:      "agent-1",
			},
			TurnIDHeader: shareddto.TurnIDHeader{TurnID: "provider-turn-uuid"},
		},
		Success: true,
		Result:  "done from provider turn",
	}))

	require.Eventually(t, func() bool {
		snapshot, err := svc.Snapshot(context.Background(), "agent-1")
		return err == nil &&
			snapshot.State == string(agentdto.StateIdle) &&
			strings.Contains(snapshot.LastReport, "done from provider turn")
	}, time.Second, 10*time.Millisecond)
}

func withLauncherControlMethods(methods handler.Map) handler.Map {
	out := make(handler.Map, len(methods)+2)
	for name, method := range methods {
		out[name] = method
	}
	if _, ok := out[mcpdto.MethodRegister]; !ok {
		out[mcpdto.MethodRegister] = handler.New(func(_ context.Context, req mcpdto.RegisterRequest) (mcpdto.RegisterResponse, error) {
			return launcherRegisterResponse(req, 60000), nil
		})
	}
	if _, ok := out[mcpdto.MethodHeartbeat]; !ok {
		out[mcpdto.MethodHeartbeat] = handler.New(func(_ context.Context, _ mcpdto.HeartbeatRequest) (mcpdto.HeartbeatResponse, error) {
			return mcpdto.HeartbeatResponse{OK: true, ConfigVersion: 1, NextHeartbeatMs: 60000}, nil
		})
	}
	return out
}

func launcherRegisterResponse(req mcpdto.RegisterRequest, heartbeatIntervalMs int) mcpdto.RegisterResponse {
	return mcpdto.RegisterResponse{
		InstanceID:            req.InstanceID,
		Generation:            1,
		AcceptedGeneration:    1,
		CapabilitiesRejected:  []string{},
		HeartbeatIntervalMs:   heartbeatIntervalMs,
		HeartbeatTimeoutMs:    5000,
		SendTimeoutMs:         5000,
		SweeperIntervalMs:     5000,
		ServerProtocolVersion: mcpdto.ProtocolVersion,
		ConfigVersion:         1,
	}
}
