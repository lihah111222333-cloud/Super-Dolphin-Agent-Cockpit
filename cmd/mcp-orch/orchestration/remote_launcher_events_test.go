package orchestration

import (
	"context"
	"errors"
	"fmt"
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

func TestRemoteLauncher_RejectsInvalidRegisterProtocolBeforeHeartbeatOrThreadStart(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version string
		want    string
	}{
		{name: "missing", want: "missing server protocol version"},
		{name: "mismatch", version: "ctl/v0", want: "incompatible server protocol version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GO_AGENT_CTL_SESSION_TOKEN", "session-secret")
			var heartbeats atomic.Int32
			var threadStarts atomic.Int32
			addr, _ := startRPCServer(t, handler.Map{
				mcpdto.MethodRegister: handler.New(func(_ context.Context, req mcpdto.RegisterRequest) (mcpdto.RegisterResponse, error) {
					resp := launcherRegisterResponse(req, 5)
					resp.ServerProtocolVersion = tc.version
					return resp, nil
				}),
				mcpdto.MethodHeartbeat: handler.New(func(_ context.Context, _ mcpdto.HeartbeatRequest) (mcpdto.HeartbeatResponse, error) {
					heartbeats.Add(1)
					return mcpdto.HeartbeatResponse{OK: true}, nil
				}),
				launcherwire.MethodThreadStart: handler.New(func(_ context.Context, _ map[string]any) (map[string]any, error) {
					threadStarts.Add(1)
					return map[string]any{"thread": map[string]any{"id": "thread-1"}}, nil
				}),
			})
			launcher := NewRemoteLauncher(addr).(*remoteLauncher)
			t.Cleanup(func() { _ = launcher.Close() })

			_, err := launcher.Launch(context.Background(), &agentRuntime{id: "agent-1"}, LaunchRequest{Command: []string{"ignored"}})
			require.ErrorContains(t, err, tc.want)
			require.Zero(t, heartbeats.Load(), "invalid registration must not start heartbeat")
			require.Zero(t, threadStarts.Load(), "invalid registration must not start thread RPC")
		})
	}
}

func TestRemoteLauncher_NormalizesRegisterProtocolVersion(t *testing.T) {
	t.Setenv("GO_AGENT_CTL_SESSION_TOKEN", "session-secret")
	var threadStarts atomic.Int32
	addr, _ := startRPCServer(t, handler.Map{
		mcpdto.MethodRegister: handler.New(func(_ context.Context, req mcpdto.RegisterRequest) (mcpdto.RegisterResponse, error) {
			resp := launcherRegisterResponse(req, 60000)
			resp.ServerProtocolVersion = "  ctl/v1  "
			return resp, nil
		}),
		launcherwire.MethodThreadStart: handler.New(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			threadStarts.Add(1)
			return map[string]any{"thread": map[string]any{"id": "thread-1"}}, nil
		}),
	})
	launcher := NewRemoteLauncher(addr).(*remoteLauncher)
	t.Cleanup(func() { _ = launcher.Close() })

	_, err := launcher.Launch(context.Background(), &agentRuntime{id: "agent-1"}, LaunchRequest{Command: []string{"ignored"}})
	require.NoError(t, err)
	require.Equal(t, int32(1), threadStarts.Load())
}

func TestRemoteLauncher_RegistrationNotificationDoesNotDeadlock(t *testing.T) {
	t.Setenv("GO_AGENT_CTL_SESSION_TOKEN", "session-secret")
	notified := make(chan struct{}, 1)
	addr, _ := startPushRPCServer(t, handler.Map{
		mcpdto.MethodRegister: handler.New(func(ctx context.Context, req mcpdto.RegisterRequest) (mcpdto.RegisterResponse, error) {
			server := jrpc2.ServerFromContext(ctx)
			require.NoError(t, server.Notify(context.Background(), eventsurface.MethodTurnTerminal, turndto.TurnTerminalV2{
				SchemaVersion: 2,
				EventID:       "00000000-1111-4222-8333-444444444444",
				ThreadID:      "thread-1",
				TurnID:        "turn-1",
				Outcome:       "success",
				PublicSummary: "completed",
				OccurredAt:    "2026-07-21T00:00:00Z",
			}))
			return launcherRegisterResponse(req, 60000), nil
		}),
		launcherwire.MethodThreadStart: handler.New(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return map[string]any{"thread": map[string]any{"id": "thread-1"}}, nil
		}),
	})
	launcher := NewRemoteLauncher(addr).(*remoteLauncher)
	launcher.bindRemoteEventSink(remoteLauncherEventSinkFunc(func(context.Context, turndto.TurnTerminalV2) {
		notified <- struct{}{}
	}))
	t.Cleanup(func() { _ = launcher.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := launcher.Launch(ctx, &agentRuntime{id: "agent-1"}, LaunchRequest{Command: []string{"ignored"}})
	require.NoError(t, err)
	select {
	case <-notified:
	case <-time.After(time.Second):
		t.Fatal("registration notification was not delivered")
	}
}

func TestRemoteLauncher_CloseDoesNotDeadlockWithInFlightNotification(t *testing.T) {
	t.Setenv("GO_AGENT_CTL_SESSION_TOKEN", "session-secret")
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	addr, _ := startPushRPCServer(t, handler.Map{
		launcherwire.MethodThreadStart: handler.New(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return map[string]any{"thread": map[string]any{"id": "thread-1"}}, nil
		}),
		launcherwire.MethodTurnStart: handler.New(func(ctx context.Context, _ map[string]any) (map[string]any, error) {
			server := jrpc2.ServerFromContext(ctx)
			require.NoError(t, server.Notify(context.Background(), eventsurface.MethodTurnTerminal, turndto.TurnTerminalV2{
				SchemaVersion: 2,
				EventID:       "11111111-2222-4333-8444-555555555555",
				ThreadID:      "thread-1",
				TurnID:        "turn-1",
				Outcome:       "success",
				PublicSummary: "completed",
				OccurredAt:    "2026-07-21T00:00:00Z",
			}))
			return map[string]any{"turn_id": "turn-1"}, nil
		}),
	})
	launcher := NewRemoteLauncher(addr).(*remoteLauncher)
	launcher.bindRemoteEventSink(remoteLauncherEventSinkFunc(func(context.Context, turndto.TurnTerminalV2) {
		entered <- struct{}{}
		<-release
	}))
	t.Cleanup(func() { _ = launcher.Close() })
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()

	_, err := launcher.Launch(context.Background(), &agentRuntime{id: "agent-1"}, LaunchRequest{Command: []string{"ignored"}})
	require.NoError(t, err)
	goroutines := newTestGoroutineGroup(t)
	submitDone := make(chan error, 1)
	goroutines.Go(func() {
		_, err := launcher.SubmitTurn(context.Background(), &agentRuntime{remoteThreadID: "thread-1"}, TurnSubmission{})
		submitDone <- err
	})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("notification did not enter event sink")
	}
	closeDone := make(chan error, 1)
	goroutines.Go(func() { closeDone <- launcher.Close() })
	close(release)
	released = true
	select {
	case err := <-closeDone:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Close deadlocked with in-flight notification")
	}
	select {
	case <-submitDone:
	case <-time.After(time.Second):
		t.Fatal("SubmitTurn did not return after notification release")
	}
}

func TestRemoteLauncher_TurnCompletedNotificationClearsRemoteBusyState(t *testing.T) {
	t.Setenv("GO_AGENT_CTL_SESSION_TOKEN", "session-secret")
	addr, _ := startPushRPCServer(t, handler.Map{
		"turn/start": handler.New(func(ctx context.Context, _ map[string]any) (map[string]any, error) {
			server := jrpc2.ServerFromContext(ctx)
			require.NoError(t, server.Notify(context.Background(), eventsurface.MethodTurnTerminal, turndto.TurnTerminalV2{
				SchemaVersion: 2,
				EventID:       "00000000-1111-4222-8333-444444444444",
				ThreadID:      "thread-1",
				TurnID:        "stale-remote-turn",
				Outcome:       "success",
				PublicSummary: "completed",
				OccurredAt:    "2026-07-16T10:11:11.123Z",
			}))
			require.NoError(t, server.Notify(context.Background(), eventsurface.MethodTurnTerminal, turndto.TurnTerminalV2{
				SchemaVersion: 2,
				EventID:       "11111111-2222-4333-8444-555555555555",
				ThreadID:      "thread-1",
				TurnID:        "remote-turn-1",
				Outcome:       "success",
				PublicSummary: "completed",
				OccurredAt:    "2026-07-16T10:11:12.123Z",
			}))
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
		PublicSummary: "completed",
		OccurredAt:    "2026-07-17T01:02:03Z",
	}
	svc.handleRemoteTurnTerminal(context.Background(), success)
	first, err := svc.Snapshot(context.Background(), "agent-1")
	require.NoError(t, err)
	require.Equal(t, string(agentdto.StateIdle), first.State)
	require.Equal(t, "", first.ActiveTurnID)
	require.Equal(t, "completed", first.LastReport)

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

func TestRemoteTerminalSealCapacityKeepsTargetChangedProtection(t *testing.T) {
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	svc.turns.remoteTerminalCapacity = 1
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-1"
	agent.remoteThreadID = "thread-1"
	agent.activeTurnID = "remote-turn-1"
	svc.registry.agents[agent.id] = agent

	first := remoteTerminalFixture("thread-1", "remote-turn-1")
	svc.handleRemoteTurnTerminal(context.Background(), first)
	agent.state = agentdto.StateTurnRunning
	agent.activeTurnID = "remote-turn-2"

	svc.handleRemoteTurnTerminal(context.Background(), remoteTerminalFixture("thread-1", "remote-turn-2"))
	agent.state = agentdto.StateTurnRunning
	agent.activeTurnID = "remote-turn-3"

	conflict := first
	conflict.EventID = "event-conflicting-old-turn"
	conflict.Outcome = "failed"
	conflict.PublicError = &turndto.PublicErrorV1{
		Code:            "PERMANENT_FAILURE",
		Title:           "Permanent failure",
		Message:         "old terminal must not finish the replacement turn",
		DiagnosticID:    "diag-old-turn",
		Retryable:       false,
		RecoveryActions: []string{},
	}
	svc.handleRemoteTurnTerminal(context.Background(), conflict)

	snapshot, err := svc.Snapshot(context.Background(), agent.id)
	require.NoError(t, err)
	require.Equal(t, string(agentdto.StateTurnRunning), snapshot.State)
	require.Equal(t, "remote-turn-3", snapshot.ActiveTurnID)
	stats := svc.turns.remoteTerminalSealStats()
	require.LessOrEqual(t, stats.Entries, 1)
	require.Equal(t, uint64(1), stats.CapacityEvictions)
}

func TestRemoteTerminalSealBoundedPressureAndLifecycleCleanup(t *testing.T) {
	controller := &turnController{remoteTerminalCapacity: 8}
	for index := range 128 {
		acceptance, err := controller.acceptRemoteTurnTerminal(remoteTerminalFixture("thread-pressure", fmt.Sprintf("turn-%03d", index)))
		require.NoError(t, err)
		require.True(t, acceptance.accepted)
		controller.releaseRemoteTurnTerminal(acceptance)
	}
	stats := controller.remoteTerminalSealStats()
	require.Equal(t, 8, stats.Capacity)
	require.Equal(t, 8, stats.Entries)
	require.Zero(t, stats.InFlight)
	require.Equal(t, uint64(120), stats.CapacityEvictions)

	duplicate, err := controller.acceptRemoteTurnTerminal(remoteTerminalFixture("thread-pressure", "turn-127"))
	require.NoError(t, err)
	require.False(t, duplicate.accepted)

	inFlight, err := controller.acceptRemoteTurnTerminal(remoteTerminalFixture("thread-cleanup", "turn-in-flight"))
	require.NoError(t, err)
	cleared := controller.clearRemoteTerminalsForThread("thread-cleanup")
	require.Equal(t, 1, cleared.InFlight)
	require.Equal(t, uint64(1), cleared.LifecycleDeferred)
	controller.releaseRemoteTurnTerminal(inFlight)
	stats = controller.remoteTerminalSealStats()
	require.Zero(t, stats.InFlight)
	require.Equal(t, uint64(1), stats.LifecycleClears)
}

func TestRemoteTerminalSealConcurrentAccessRemainsBounded(t *testing.T) {
	controller := &turnController{remoteTerminalCapacity: 64}
	const workers = 16
	const terminalsPerWorker = 64
	errs := make(chan error, workers)
	group := newTestGoroutineGroup(t)
	for worker := range workers {
		group.Go(func() {
			for index := range terminalsPerWorker {
				acceptance, err := controller.acceptRemoteTurnTerminal(remoteTerminalFixture(fmt.Sprintf("thread-%02d", worker), fmt.Sprintf("turn-%03d", index)))
				if err != nil {
					errs <- err
					return
				}
				if !acceptance.accepted {
					errs <- errors.New("unique terminal was not accepted")
					return
				}
				controller.releaseRemoteTurnTerminal(acceptance)
			}
		})
	}
	group.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	stats := controller.remoteTerminalSealStats()
	require.Equal(t, 64, stats.Capacity)
	require.LessOrEqual(t, stats.Entries, stats.Capacity)
	require.Zero(t, stats.InFlight)
	require.Equal(t, uint64(workers*terminalsPerWorker-64), stats.CapacityEvictions)
}

func TestRemoteTerminalSealRejectsCapacityExhaustedByInFlightTerminals(t *testing.T) {
	controller := &turnController{remoteTerminalCapacity: 2}
	first, err := controller.acceptRemoteTurnTerminal(remoteTerminalFixture("thread-1", "turn-1"))
	require.NoError(t, err)
	second, err := controller.acceptRemoteTurnTerminal(remoteTerminalFixture("thread-2", "turn-2"))
	require.NoError(t, err)
	_, err = controller.acceptRemoteTurnTerminal(remoteTerminalFixture("thread-3", "turn-3"))
	require.EqualError(t, err, "remote terminal seal capacity exhausted by in-flight terminals")
	controller.releaseRemoteTurnTerminal(first)
	controller.releaseRemoteTurnTerminal(second)
}

func TestRemoteTerminalIdleDoesNotAcceptUnownedTerminal(t *testing.T) {
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateIdle
	agent.remoteThreadID = "thread-1"
	svc.registry.agents[agent.id] = agent

	svc.handleRemoteTurnTerminal(context.Background(), remoteTerminalFixture("thread-1", "old-turn"))

	snapshot, err := svc.Snapshot(context.Background(), agent.id)
	require.NoError(t, err)
	require.Equal(t, string(agentdto.StateIdle), snapshot.State)
	require.Empty(t, snapshot.ActiveTurnID)
	require.Zero(t, svc.turns.remoteTerminalSealStats().Entries)
}

func TestSubmitRemoteTurnDriftInterruptsImmutableOrphanSnapshot(t *testing.T) {
	launcher := &remoteTurnFenceLauncher{turnID: "remote-accepted"}
	svc := NewService(silentLogger(), event.NewDispatcher(), launcher, nil, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateIdle
	agent.remoteThreadID = "thread-old"
	agent.launchSeq = 7
	svc.registry.agents[agent.id] = agent
	launcher.onSubmit = func(snapshot *agentRuntime) {
		svc.registry.withAgentLocked(agent.id, func(current *agentRuntime) error {
			current.remoteThreadID = "thread-replacement"
			current.launchSeq++
			current.activeTurnID = "replacement-turn"
			current.state = agentdto.StateTurnRunning
			return nil
		})
		launcher.submitThread = snapshot.remoteThreadID
		launcher.submitLaunchSeq = snapshot.launchSeq
	}

	err := svc.SubmitTurn(context.Background(), TurnSubmission{AgentID: agent.id, Inputs: []shareddto.InputItem{{Type: "text", Content: "work"}}})
	require.ErrorContains(t, err, "remote turn submit drift")
	require.Equal(t, "thread-old", launcher.submitThread)
	require.Equal(t, uint64(7), launcher.submitLaunchSeq)
	require.Equal(t, "thread-old", launcher.interruptThread)
	require.Equal(t, "remote-accepted", launcher.interruptTurn)
	snapshot, snapErr := svc.Snapshot(context.Background(), agent.id)
	require.NoError(t, snapErr)
	require.Equal(t, "thread-replacement", snapshot.ThreadID)
	require.Equal(t, "replacement-turn", snapshot.ActiveTurnID)
}

func TestSubmitRemoteTurnEarlyTerminalCapacityFailsAndInterrupts(t *testing.T) {
	launcher := &remoteTurnFenceLauncher{turnID: "remote-accepted"}
	svc := NewService(silentLogger(), event.NewDispatcher(), launcher, nil, nil, nil)
	svc.turns.pendingRemoteTerminalCapacity = 1
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateIdle
	agent.remoteThreadID = "thread-1"
	agent.launchSeq = 1
	svc.registry.agents[agent.id] = agent
	launcher.onSubmit = func(_ *agentRuntime) {
		first := remoteTerminalFixture("thread-1", "remote-accepted")
		_, buffered, routeErr := svc.turns.routeRemoteTurnTerminal(agent.id, first)
		require.NoError(t, routeErr)
		require.True(t, buffered)
		_, buffered, routeErr = svc.turns.routeRemoteTurnTerminal(agent.id, first)
		require.NoError(t, routeErr)
		require.True(t, buffered)
		second := remoteTerminalFixture("thread-1", "remote-other")
		second.EventID = "event-second"
		_, _, routeErr = svc.turns.routeRemoteTurnTerminal(agent.id, second)
		require.EqualError(t, routeErr, "pending remote terminal reconciliation capacity exhausted")
	}

	err := svc.SubmitTurn(context.Background(), TurnSubmission{AgentID: agent.id, Inputs: []shareddto.InputItem{{Type: "text", Content: "work"}}})
	require.ErrorContains(t, err, "pending remote terminal reconciliation capacity exhausted")
	require.Equal(t, "thread-1", launcher.interruptThread)
	require.Equal(t, "remote-accepted", launcher.interruptTurn)
	snapshot, snapErr := svc.Snapshot(context.Background(), agent.id)
	require.NoError(t, snapErr)
	require.Equal(t, string(agentdto.StateIdle), snapshot.State)
	require.Empty(t, snapshot.ActiveTurnID)
	require.Zero(t, svc.turns.pendingRemoteTerminalCount)
}

func TestSubmitRemoteTurnBuffersMatchingProvisionalTerminalUntilRPCReturns(t *testing.T) {
	launcher := &remoteTurnFenceLauncher{}
	svc := NewService(silentLogger(), event.NewDispatcher(), launcher, nil, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateIdle
	agent.remoteThreadID = "thread-1"
	agent.launchSeq = 1
	svc.registry.agents[agent.id] = agent
	launcher.onSubmit = func(snapshot *agentRuntime) {
		launcher.turnID = snapshot.activeTurnID
		terminal := remoteTerminalFixture(snapshot.remoteThreadID, snapshot.activeTurnID)
		deliver, buffered, routeErr := svc.turns.routeRemoteTurnTerminal(agent.id, terminal)
		require.NoError(t, routeErr)
		require.False(t, deliver)
		require.True(t, buffered)
	}

	err := svc.SubmitTurn(context.Background(), TurnSubmission{
		AgentID: agent.id,
		Inputs:  []shareddto.InputItem{{Type: "text", Content: "work"}},
	})
	require.NoError(t, err)
	snapshot, snapErr := svc.Snapshot(context.Background(), agent.id)
	require.NoError(t, snapErr)
	require.Equal(t, string(agentdto.StateIdle), snapshot.State)
	require.Empty(t, snapshot.ActiveTurnID)
	require.Empty(t, launcher.interruptTurn)
}

type remoteTurnFenceLauncher struct {
	turnID          string
	submitThread    string
	submitLaunchSeq uint64
	interruptThread string
	interruptTurn   string
	onSubmit        func(*agentRuntime)
}

func (l *remoteTurnFenceLauncher) Launch(context.Context, *agentRuntime, LaunchRequest) (LaunchResult, error) {
	return LaunchResult{}, nil
}

func (l *remoteTurnFenceLauncher) Fork(context.Context, *agentRuntime, *agentRuntime, LaunchRequest) (LaunchResult, error) {
	return LaunchResult{}, nil
}

func (l *remoteTurnFenceLauncher) Stop(context.Context, *agentRuntime) error { return nil }

func (l *remoteTurnFenceLauncher) Archive(context.Context, *agentRuntime) error { return nil }

func (l *remoteTurnFenceLauncher) Interrupt(_ context.Context, agent *agentRuntime, _ string) error {
	l.interruptThread = agent.remoteThreadID
	l.interruptTurn = agent.activeTurnID
	return nil
}

func (l *remoteTurnFenceLauncher) SubmitTurn(_ context.Context, agent *agentRuntime, _ TurnSubmission) (string, error) {
	if l.onSubmit != nil {
		l.onSubmit(agent)
	}
	return l.turnID, nil
}

func (l *remoteTurnFenceLauncher) IsRunning(context.Context, *agentRuntime) bool { return true }

func remoteTerminalFixture(threadID, turnID string) turndto.TurnTerminalV2 {
	return turndto.TurnTerminalV2{
		SchemaVersion: 2,
		EventID:       "event-" + threadID + "-" + turnID,
		ThreadID:      threadID,
		TurnID:        turnID,
		Outcome:       "success",
		PublicSummary: "completed",
		OccurredAt:    "2026-07-19T01:02:03Z",
	}
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

type remoteLauncherEventSinkFunc func(context.Context, turndto.TurnTerminalV2)

func (f remoteLauncherEventSinkFunc) handleRemoteTurnTerminal(ctx context.Context, terminal turndto.TurnTerminalV2) {
	f(ctx, terminal)
}
