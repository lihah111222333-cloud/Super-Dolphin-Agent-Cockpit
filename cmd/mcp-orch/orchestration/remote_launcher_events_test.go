package orchestration

import (
	"context"
	"errors"
	"maps"
	"sync/atomic"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	"github.com/kelindar/event"
	"github.com/stretchr/testify/require"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/launcherwire"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/eventsurface"
)

func TestRemoteLauncher_RegistersBeforeThreadStart(t *testing.T) {
	t.Setenv("GO_AGENT_CTL_SESSION_TOKEN", "session-secret")
	var registered atomic.Int32
	registerReq := make(chan mcpdto.RegisterRequest, 1)
	addr, _ := startRPCServer(t, handler.Map{
		mcpdto.MethodRegister: handler.New(func(_ context.Context, req mcpdto.RegisterRequest) (mcpdto.RegisterResponse, error) {
			registerReq <- req
			if req.SessionToken != "session-secret" {
				return mcpdto.RegisterResponse{}, jrpc2.Errorf(jrpc2.Code(-31002), "control rpc unauthorized: invalid session token")
			}
			registered.Store(1)
			return launcherRegisterResponse(req, 60000), nil
		}),
		launcherwire.MethodThreadStart: handler.New(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			if registered.Load() == 0 {
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

	svc.registry.mu.Lock()
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateIdle
	agent.remoteThreadID = "thread-1"
	svc.registry.agents[agent.id] = agent
	decoy := svc.newAgentLocked("thread-1")
	decoy.state = agentdto.StateTurnRunning
	decoy.remoteThreadID = "decoy-thread"
	decoy.activeTurnID = "decoy-turn"
	svc.registry.agents[decoy.id] = decoy
	svc.registry.mu.Unlock()

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
	require.NoError(t, server.Notify(context.Background(), eventsurface.MethodTurnTerminal, turndto.TurnTerminalV2{
		SchemaVersion: 2,
		EventID:       "11111111-2222-4333-8444-555555555555",
		ThreadID:      "thread-1",
		TurnID:        "remote-turn-1",
		Outcome:       "success",
		OccurredAt:    "2026-07-16T10:11:12.123Z",
	}))

	require.Eventually(t, func() bool {
		snapshot, err := svc.Snapshot(context.Background(), "agent-1")
		return err == nil &&
			snapshot.State == string(agentdto.StateIdle) &&
			snapshot.ActiveTurnID == ""
	}, time.Second, 10*time.Millisecond)
	decoySnapshot, err := svc.Snapshot(context.Background(), "thread-1")
	require.NoError(t, err)
	require.Equal(t, string(agentdto.StateTurnRunning), decoySnapshot.State)
	require.Equal(t, "decoy-turn", decoySnapshot.ActiveTurnID)
}

func TestRemoteTerminalRequiresTurnID(t *testing.T) {
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-1"
	agent.remoteThreadID = "thread-1"
	agent.activeTurnID = "remote-turn-1"
	svc.registry.agents[agent.id] = agent
	svc.handleRemoteTurnCompleted(context.Background(), turndto.TurnCompleted{
		TurnHeader: shareddto.TurnHeader{
			AgentHeader: shareddto.AgentHeader{
				ThreadHeader: shareddto.ThreadHeader{ThreadID: "thread-1"},
				AgentID:      "agent-1",
			},
		},
		Success: true,
		Result:  "done without turn id",
	})
	snapshot, err := svc.Snapshot(context.Background(), "agent-1")
	require.NoError(t, err)
	require.Equal(t, string(agentdto.StateTurnRunning), snapshot.State)
	require.Equal(t, "remote-turn-1", snapshot.ActiveTurnID)
	require.NotContains(t, snapshot.LastReport, "done without turn id")
}

func TestRemoteTerminalFirstCanonicalTruthWins(t *testing.T) {
	t.Parallel()
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-1"
	agent.remoteThreadID = "thread-1"
	agent.activeTurnID = "remote-turn-1"
	agent.lastReport = "report before terminal"
	svc.registry.agents[agent.id] = agent

	success := turndto.TurnTerminalV2{
		SchemaVersion: 2,
		EventID:       "event-success",
		ThreadID:      "thread-1",
		TurnID:        "remote-turn-1",
		Outcome:       "success",
		OccurredAt:    "2026-07-17T01:02:03Z",
	}
	svc.handleRemoteTurnTerminal(context.Background(), success)
	first, err := svc.Snapshot(context.Background(), "agent-1")
	require.NoError(t, err)
	require.Equal(t, string(agentdto.StateIdle), first.State)
	require.Equal(t, "", first.ActiveTurnID)
	require.Equal(t, "report before terminal", first.LastReport)

	svc.handleRemoteTurnTerminal(context.Background(), success)
	duplicate, err := svc.Snapshot(context.Background(), "agent-1")
	require.NoError(t, err)
	require.Equal(t, first.State, duplicate.State)
	require.Equal(t, first.LastReport, duplicate.LastReport)

	conflict := turndto.TurnTerminalV2{
		SchemaVersion: 2,
		EventID:       "event-conflicting-failure",
		ThreadID:      "thread-1",
		TurnID:        "remote-turn-1",
		Outcome:       "failed",
		PublicError: &turndto.PublicErrorV1{
			Code:            "PERMANENT_FAILURE",
			Title:           "Permanent failure",
			Message:         "must not replace success",
			DiagnosticID:    "diag-conflict",
			Retryable:       false,
			RecoveryActions: []string{},
		},
		OccurredAt: "2026-07-17T01:02:04Z",
	}
	svc.handleRemoteTurnTerminal(context.Background(), conflict)
	afterConflict, err := svc.Snapshot(context.Background(), "agent-1")
	require.NoError(t, err)
	require.Equal(t, string(agentdto.StateIdle), afterConflict.State)
	require.Equal(t, "", afterConflict.ActiveTurnID)
	require.Equal(t, first.LastReport, afterConflict.LastReport)
}

func TestService_SubmitTurnRemoteModeDeadlineFailureClearsBusyState(t *testing.T) {
	svc := NewService(silentLogger(), event.NewDispatcher(), remoteLocalLauncher(t, handler.Map{
		"turn/start": handler.New(func(ctx context.Context, _ map[string]any) (map[string]any, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}),
	}), nil, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.state, agent.remoteThreadID, agent.name = agentdto.StateIdle, "thread-1", "worker-agent"
	svc.registry.agents[agent.id] = agent

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := svc.SubmitTurn(ctx, TurnSubmission{
		AgentID: agent.id,
		Inputs:  []shareddto.InputItem{{Type: "text", Content: "work"}},
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("SubmitTurn() error = %v, want deadline exceeded", err)
	}
	if agent.activeTurnID != "" || agent.state != agentdto.StateIdle {
		t.Fatalf("agent after SubmitTurn timeout = state:%q active:%q, want idle with no active turn", agent.state, agent.activeTurnID)
	}
}

func withLauncherControlMethods(methods handler.Map) handler.Map {
	out := make(handler.Map, len(methods)+2)
	maps.Copy(out, methods)
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
