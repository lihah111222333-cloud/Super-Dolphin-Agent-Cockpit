package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/testutil/golden"
)

func TestLaunchHandlerReturnsExistingByLaunchIDAfterRemoteRekey(t *testing.T) {
	handler := HandleLaunchAgent(&golden.OrchestrationStub{
		ListAgentsFunc: func(context.Context) ([]contract.AgentSnapshot, error) {
			return []contract.AgentSnapshot{{ID: "remote-final", AgentID: "remote-final", LaunchID: "agent-requested", ThreadID: "thread-final", State: "turn_running"}}, nil
		},
		LaunchAgentSnapshotFunc: func(context.Context, contract.LaunchRequest) (contract.AgentSnapshot, error) {
			t.Fatal("LaunchAgentSnapshot should not be called for active launch_id alias")
			return contract.AgentSnapshot{}, nil
		},
	})

	result, err := handler(context.Background(), json.RawMessage(`{"agent_id":"agent-requested","name":"worker","cwd":"/tmp/work","prompt":"retry"}`))
	if err != nil {
		t.Fatalf("HandleLaunchAgent() error = %v", err)
	}
	got, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("HandleLaunchAgent() result type = %T, want map[string]any", result)
	}
	if got["agent_id"] != "remote-final" || got["status"] != "existing" || got["thread_id"] != "thread-final" {
		t.Fatalf("HandleLaunchAgent() result = %#v, want existing remote-final by launch alias", got)
	}
}

func TestLaunchHandlerRejectsRelaunchWhileExistingAgentStopping(t *testing.T) {
	handler := HandleLaunchAgent(&golden.OrchestrationStub{
		ListAgentsFunc: func(context.Context) ([]contract.AgentSnapshot, error) {
			return []contract.AgentSnapshot{{ID: "agent-stopping", AgentID: "agent-stopping", ThreadID: "thread-stopping", State: "stopping"}}, nil
		},
		LaunchAgentSnapshotFunc: func(context.Context, contract.LaunchRequest) (contract.AgentSnapshot, error) {
			t.Fatal("LaunchAgentSnapshot should not be called while an explicit agent_id is stopping")
			return contract.AgentSnapshot{}, nil
		},
	})

	_, err := handler(context.Background(), json.RawMessage(`{"agent_id":"agent-stopping","name":"worker","cwd":"/tmp/work","prompt":"retry"}`))
	if err == nil || !strings.Contains(err.Error(), "stopping") {
		t.Fatalf("HandleLaunchAgent() error = %v, want explicit stopping rejection", err)
	}
}

func TestLaunchHandlerRejectsRelaunchWhenExplicitAgentIDIsArchived(t *testing.T) {
	handler := HandleLaunchAgent(&golden.OrchestrationStub{
		ListAgentsFunc: func(context.Context) ([]contract.AgentSnapshot, error) {
			return []contract.AgentSnapshot{{ID: "agent-archived", AgentID: "agent-archived", ThreadID: "thread-archived", State: "stopped"}}, nil
		},
		LaunchAgentSnapshotFunc: func(context.Context, contract.LaunchRequest) (contract.AgentSnapshot, error) {
			t.Fatal("LaunchAgentSnapshot should not be called while an explicit agent_id is archived and recoverable")
			return contract.AgentSnapshot{}, nil
		},
	})

	_, err := handler(context.Background(), json.RawMessage(`{"agent_id":"agent-archived","name":"worker","cwd":"/tmp/work","prompt":"retry"}`))
	if err == nil || !strings.Contains(err.Error(), "restore") {
		t.Fatalf("HandleLaunchAgent() error = %v, want restore/unarchive rejection", err)
	}
}

func TestLaunchHandlerFailsFastWhenDuplicateCheckListAgentsFails(t *testing.T) {
	listErr := errors.New("list unavailable")
	handler := HandleLaunchAgent(&golden.OrchestrationStub{
		ListAgentsFunc: func(context.Context) ([]contract.AgentSnapshot, error) {
			return nil, listErr
		},
		LaunchAgentSnapshotFunc: func(context.Context, contract.LaunchRequest) (contract.AgentSnapshot, error) {
			t.Fatal("LaunchAgentSnapshot should not be called when duplicate check cannot list agents")
			return contract.AgentSnapshot{}, nil
		},
	})

	_, err := handler(context.Background(), json.RawMessage(`{"agent_id":"agent-requested","name":"worker","cwd":"/tmp/work","prompt":"retry"}`))
	if !errors.Is(err, listErr) {
		t.Fatalf("HandleLaunchAgent() error = %v, want list unavailable", err)
	}
}

func TestLaunchHandlerDoesNotLaunchWhenAliasAppearsDuringReservation(t *testing.T) {
	var listCalls atomic.Int64
	handler := HandleLaunchAgent(&golden.OrchestrationStub{
		ListAgentsFunc: func(context.Context) ([]contract.AgentSnapshot, error) {
			if listCalls.Add(1) == 1 {
				return nil, nil
			}
			return []contract.AgentSnapshot{{ID: "remote-final", AgentID: "remote-final", LaunchID: "agent-requested", State: "turn_running"}}, nil
		},
		LaunchAgentSnapshotFunc: func(context.Context, contract.LaunchRequest) (contract.AgentSnapshot, error) {
			t.Fatal("LaunchAgentSnapshot should not be called after active launch_id alias appears")
			return contract.AgentSnapshot{}, nil
		},
	})

	_, err := handler(context.Background(), json.RawMessage(`{"agent_id":"agent-requested","name":"worker","cwd":"/tmp/work","prompt":"retry"}`))
	if err == nil || !strings.Contains(err.Error(), "launch already in progress") {
		t.Fatalf("HandleLaunchAgent() error = %v, want launch already in progress", err)
	}
}

func TestLaunchHandlerConcurrentExplicitAgentIDLaunchesOnce(t *testing.T) {
	var launchCalls atomic.Int64
	launchStarted := make(chan struct{}, 2)
	releaseLaunch := make(chan struct{})
	releaseBlockedLaunch := closeTestSignalOnce(releaseLaunch)
	defer releaseBlockedLaunch()
	handler := HandleLaunchAgent(&golden.OrchestrationStub{
		ListAgentsFunc: func(context.Context) ([]contract.AgentSnapshot, error) {
			return nil, nil
		},
		LaunchAgentSnapshotFunc: func(context.Context, contract.LaunchRequest) (contract.AgentSnapshot, error) {
			launchCalls.Add(1)
			launchStarted <- struct{}{}
			<-releaseLaunch
			return contract.AgentSnapshot{ID: "agent-race", AgentID: "agent-race", State: "idle"}, nil
		},
	})
	input := json.RawMessage(`{"agent_id":"agent-race","name":"worker","cwd":"/tmp/work","prompt":"start"}`)

	firstDone := make(chan error, 1)
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() {
		_, err := handler(context.Background(), input)
		firstDone <- err
	})
	select {
	case <-launchStarted:
	case <-time.After(time.Second):
		t.Fatal("first launch did not start")
	}

	secondDone := make(chan error, 1)
	goroutines.Go(func() {
		_, err := handler(context.Background(), input)
		secondDone <- err
	})
	select {
	case <-launchStarted:
		releaseBlockedLaunch()
		t.Fatal("second concurrent explicit agent_id request started a duplicate launch")
	case err := <-secondDone:
		if err == nil || !strings.Contains(err.Error(), "launch already in progress") {
			releaseBlockedLaunch()
			t.Fatalf("second HandleLaunchAgent() error = %v, want launch already in progress", err)
		}
	case <-time.After(time.Second):
		releaseBlockedLaunch()
		t.Fatal("second concurrent explicit agent_id request did not return")
	}
	releaseBlockedLaunch()
	if err := <-firstDone; err != nil {
		t.Fatalf("first HandleLaunchAgent() error = %v", err)
	}
	if got := launchCalls.Load(); got != 1 {
		t.Fatalf("LaunchAgentSnapshot calls = %d, want 1", got)
	}
}
