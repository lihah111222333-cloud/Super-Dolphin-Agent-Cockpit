package orchestration

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/kelindar/event"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	rpcpkg "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

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

func TestUpdateRuntimeDualAliaseSameValue(t *testing.T) {
	t.Parallel()

	var params runtimeReportParams
	err := json.Unmarshal([]byte(`{"agent_id":"agent-1","agentId":"agent-1","port":9000,"provider":"codex"}`), &params)
	if err != nil {
		t.Fatalf("runtimeReportParams dual-alias err = %v", err)
	}
	if params.AgentID != "agent-1" || params.Port != 9000 || params.Provider != "codex" {
		t.Fatalf("runtimeReportParams dual-alias = %#v", params)
	}
}

func TestRuntimeReportParamsConflictingAliases(t *testing.T) {
	t.Parallel()

	var params runtimeReportParams
	err := json.Unmarshal([]byte(`{"agent_id":"agent-1","agentId":"agent-2","port":9000}`), &params)
	if err == nil || !strings.Contains(err.Error(), "aliases conflict") {
		t.Fatalf("runtimeReportParams conflicting-aliases err = %v", err)
	}
}

func TestRuntimeReportParamsRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	var params runtimeReportParams
	err := json.Unmarshal([]byte(`{"agentId":"agent-1","port":9000,"provider":"codex","extra":true}`), &params)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("runtimeReportParams unknown-field err = %v", err)
	}
}

func TestRuntimeReportParamsRejectsTrailingData(t *testing.T) {
	t.Parallel()

	var params runtimeReportParams
	err := json.Unmarshal([]byte(`{"agentId":"agent-1","port":9000,"provider":"codex"} {}`), &params)
	if err == nil {
		t.Fatal("expected trailing-data error")
	}
}

func TestReportRuntimeRPCHandler(t *testing.T) {
	t.Parallel()

	svc, reported, cancel := newRuntimeTestService(silentLogger(), runtimeTestAgent())
	defer cancel()

	server := rpcpkg.NewServer(rpcpkg.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewOrchestrationHandlers(svc).Handlers)

	raw, err := server.Dispatch(context.Background(), "orchestration.reportRuntime", json.RawMessage(`{"agent_id":"agent-1","provider":"claude"}`))
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}

	var resp struct {
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("json.Unmarshal(response) error = %v", err)
	}
	if !resp.Success {
		t.Fatalf("response = %s, want success=true", raw)
	}

	snapshot, err := svc.Snapshot(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.Port != 8080 || snapshot.PortSource != "inferred" {
		t.Fatalf("snapshot port = (%d, %q), want (8080, inferred)", snapshot.Port, snapshot.PortSource)
	}
	if snapshot.Provider != "claude" || snapshot.ProviderSource != "runtime" {
		t.Fatalf("snapshot provider = (%q, %q), want (claude, runtime)", snapshot.Provider, snapshot.ProviderSource)
	}

	ev := expectRuntimeEvent(t, reported)
	if ev.AgentID != "agent-1" || ev.Port != 8080 || ev.Provider != "claude" {
		t.Fatalf("runtime event = %#v", ev)
	}
}

func newRuntimeTestService(logger *slog.Logger, agent *agentRuntime) (*service, <-chan agentdto.AgentRuntimeReported, func()) {
	dispatcher := event.NewDispatcher()
	svc := NewService(logger, dispatcher, nil, nil, nil, nil)
	svc.agents[agent.id] = agent
	reported := make(chan agentdto.AgentRuntimeReported, 4)
	cancel := event.Subscribe(dispatcher, func(ev agentdto.AgentRuntimeReported) {
		reported <- ev
	})
	return svc, reported, cancel
}

func runtimeTestAgent() *agentRuntime {
	return &agentRuntime{
		id:             "agent-1",
		port:           8080,
		portSource:     "inferred",
		provider:       "codex",
		providerSource: "inferred",
	}
}

func expectRuntimeEvent(t *testing.T, reported <-chan agentdto.AgentRuntimeReported) agentdto.AgentRuntimeReported {
	t.Helper()

	select {
	case ev := <-reported:
		return ev
	case <-time.After(time.Second):
		t.Fatal("expected AgentRuntimeReported event")
	}
	return agentdto.AgentRuntimeReported{}
}

func expectNoRuntimeEvent(t *testing.T, reported <-chan agentdto.AgentRuntimeReported) {
	t.Helper()

	select {
	case ev := <-reported:
		t.Fatalf("unexpected AgentRuntimeReported event: %#v", ev)
	case <-time.After(200 * time.Millisecond):
	}
}
