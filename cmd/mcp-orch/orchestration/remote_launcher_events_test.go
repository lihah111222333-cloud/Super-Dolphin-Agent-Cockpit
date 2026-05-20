package orchestration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	"github.com/kelindar/event"
	"github.com/stretchr/testify/require"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
)

func TestRemoteLauncher_TurnCompletedNotificationClearsRemoteBusyState(t *testing.T) {
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
