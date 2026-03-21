package orchestration

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/kelindar/event"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
)

func TestUpdateRuntimePrefersReportedValues(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	svc := &service{
		eventBus: dispatcher,
		agents: map[string]*agentRuntime{
			"agent-1": {
				id:             "agent-1",
				port:           8080,
				portSource:     "inferred",
				provider:       "codex",
				providerSource: "inferred",
			},
		},
	}
	reported := make(chan agentdto.AgentRuntimeReported, 1)
	cancel := event.Subscribe(dispatcher, func(ev agentdto.AgentRuntimeReported) {
		reported <- ev
	})
	defer cancel()

	err := svc.UpdateRuntime(context.Background(), RuntimeReport{
		AgentID:  "agent-1",
		Port:     9090,
		Provider: "claude",
	})
	if err != nil {
		t.Fatalf("UpdateRuntime() error = %v", err)
	}

	snapshot, err := svc.Snapshot(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.Port != 9090 || snapshot.PortSource != "runtime" {
		t.Fatalf("snapshot port = (%d, %q), want (9090, runtime)", snapshot.Port, snapshot.PortSource)
	}
	if snapshot.Provider != "claude" || snapshot.ProviderSource != "runtime" {
		t.Fatalf("snapshot provider = (%q, %q), want (claude, runtime)", snapshot.Provider, snapshot.ProviderSource)
	}

	select {
	case ev := <-reported:
		if ev.AgentID != "agent-1" || ev.Port != 9090 || ev.Provider != "claude" {
			t.Fatalf("runtime event = %#v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("expected AgentRuntimeReported event")
	}
}

func TestPrepareLaunchStateLockedClearsStaleRuntimeValues(t *testing.T) {
	t.Parallel()

	svc := NewService(nil, nil, nil, nil, nil)
	req := LaunchRequest{
		AgentID: "agent-1",
		Command: []string{"agent"},
		Env:     []string{"PORT=8080", "AGENT_PROVIDER=codex"},
	}
	agent := svc.agentForLaunchLocked(req)
	agent.runtimePort = 9090
	agent.runtimeProvider = "claude"
	agent.portSource = "runtime"
	agent.providerSource = "runtime"

	agent = svc.agentForLaunchLocked(req)
	if err := svc.prepareLaunchStateLocked(context.Background(), agent); err != nil {
		t.Fatalf("prepareLaunchStateLocked() error = %v", err)
	}

	snapshot := svc.snapshotLocked(context.Background(), agent)
	if snapshot.Port != 8080 || snapshot.PortSource != "inferred" {
		t.Fatalf("snapshot port after relaunch prep = (%d, %q), want (8080, inferred)", snapshot.Port, snapshot.PortSource)
	}
	if snapshot.Provider != "codex" || snapshot.ProviderSource != "inferred" {
		t.Fatalf("snapshot provider after relaunch prep = (%q, %q), want (codex, inferred)", snapshot.Provider, snapshot.ProviderSource)
	}
}

func TestRuntimeReportParamsCompatibility(t *testing.T) {
	t.Parallel()

	var snake runtimeReportParams
	if err := json.Unmarshal([]byte(`{"agent_id":"agent-1","port":9000,"provider":"codex"}`), &snake); err != nil {
		t.Fatalf("runtimeReportParams snake_case err = %v", err)
	}
	if snake.AgentID != "agent-1" || snake.Port != 9000 || snake.Provider != "codex" {
		t.Fatalf("runtimeReportParams snake_case = %#v", snake)
	}

	var camel runtimeReportParams
	if err := json.Unmarshal([]byte(`{"agentId":"agent-2","port":9001,"provider":"claude"}`), &camel); err != nil {
		t.Fatalf("runtimeReportParams camelCase err = %v", err)
	}
	if camel.AgentID != "agent-2" || camel.Port != 9001 || camel.Provider != "claude" {
		t.Fatalf("runtimeReportParams camelCase = %#v", camel)
	}
}
