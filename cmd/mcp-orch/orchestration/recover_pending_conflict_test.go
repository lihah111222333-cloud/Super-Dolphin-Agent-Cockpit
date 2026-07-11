package orchestration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kelindar/event"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	sharedto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	threaddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/thread"
)

func TestRecoveringAgentAcceptsRecoveredStoppedAfterConflictingStartedHooks(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	svc := NewService(silentLogger(), dispatcher, nil, nil, nil, nil)
	agent := launcherRecoveryAgent(svc, "agent-remote")
	agent.state = agentdto.StateRecovering
	agent.updatedAt = time.Now()
	agent.reportRequesters = []string{"agent-parent"}
	consumer := newHookConsumer(svc, silentLogger())

	consumer.handleThreadStarted(context.Background(), threaddto.Started{
		EventHeader: sharedto.EventHeader{Timestamp: agent.updatedAt.Add(time.Second)},
		ThreadID:    "thread-stale-third",
		AgentID:     "agent-remote",
	})
	consumer.handleThreadStarted(context.Background(), threaddto.Started{
		EventHeader: sharedto.EventHeader{Timestamp: agent.updatedAt.Add(2 * time.Second)},
		ThreadID:    "thread-recovered",
		AgentID:     "agent-remote",
	})
	if agent.pendingLaunchThreadID != pendingLaunchThreadConflict {
		t.Fatalf("pendingLaunchThreadID = %q, want conflict marker", agent.pendingLaunchThreadID)
	}

	consumer.handleThreadStopped(context.Background(), threaddto.Stopped{
		EventHeader: sharedto.EventHeader{Timestamp: agent.updatedAt.Add(3 * time.Second)},
		ThreadID:    "thread-recovered",
		AgentID:     "agent-remote",
		Reason:      "recovered_thread_crashed",
	})
	if agent.state != agentdto.StateStopped || agent.remoteThreadID != "thread-recovered" {
		t.Fatalf("agent after recovered stopped hook = state:%q thread:%q, want stopped recovered thread", agent.state, agent.remoteThreadID)
	}
	if !strings.Contains(agent.lastReport, "without producing a turn report") || len(agent.reportRequesters) != 0 {
		t.Fatalf("fallback/report requesters = report:%q requesters:%v, want drained fallback", agent.lastReport, agent.reportRequesters)
	}
}
