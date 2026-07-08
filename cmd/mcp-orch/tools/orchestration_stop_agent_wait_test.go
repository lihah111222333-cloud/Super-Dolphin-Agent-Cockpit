package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	orch "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestStopAgentWaitFalsePreservesExistingResult(t *testing.T) {
	svc := &stopWaitService{snapshots: []contract.AgentSnapshot{{AgentID: "agent-1", State: "stopped"}}}
	handler := HandleStopAgent(svc)

	result, err := handler(context.Background(), json.RawMessage(`{"agent_id":"agent-1"}`))
	if err != nil {
		t.Fatalf("HandleStopAgent() error = %v", err)
	}
	got := requireStopWaitMap(t, result)
	if got["success"] != true || got["agent_id"] != "agent-1" || got["archived"] != true {
		t.Fatalf("HandleStopAgent() = %#v, want legacy archive result", got)
	}
	if _, ok := got["stopped"]; ok {
		t.Fatalf("HandleStopAgent() wait=false unexpectedly returned stopped: %#v", got)
	}
	if svc.listCalls != 0 {
		t.Fatalf("ListAgents calls = %d, want 0 when wait is omitted", svc.listCalls)
	}
}

func TestStopAgentWaitTrueReturnsStoppedWhenStateSettles(t *testing.T) {
	svc := &stopWaitService{snapshots: []contract.AgentSnapshot{
		{AgentID: "agent-1", State: "stopping"},
		{AgentID: "agent-1", State: "stopped"},
	}}
	handler := HandleStopAgent(svc)

	result, err := handler(context.Background(), json.RawMessage(`{"agent_id":"agent-1","wait":true,"timeout_ms":100}`))
	if err != nil {
		t.Fatalf("HandleStopAgent() error = %v", err)
	}
	got := requireStopWaitMap(t, result)
	if got["stopped"] != true || got["state"] != "stopped" || got["agent_id"] != "agent-1" || got["archived"] != true {
		t.Fatalf("HandleStopAgent() = %#v, want stopped archive result", got)
	}
	if svc.archiveCalls != 1 || svc.listCalls < 2 {
		t.Fatalf("archiveCalls=%d listCalls=%d, want archive once and poll", svc.archiveCalls, svc.listCalls)
	}
}

func TestStopAgentWaitTrueReturnsFailedWhenStateSettles(t *testing.T) {
	svc := &stopWaitService{snapshots: []contract.AgentSnapshot{{AgentID: "agent-1", State: "failed"}}}
	handler := HandleStopAgent(svc)

	result, err := handler(context.Background(), json.RawMessage(`{"agent_id":"agent-1","wait":true,"timeout_ms":100}`))
	if err != nil {
		t.Fatalf("HandleStopAgent() error = %v", err)
	}
	got := requireStopWaitMap(t, result)
	if got["stopped"] != true || got["state"] != "failed" {
		t.Fatalf("HandleStopAgent() = %#v, want failed settlement", got)
	}
}

func TestStopAgentWaitTrueTreatsMissingAgentAsStopped(t *testing.T) {
	svc := &stopWaitService{snapshots: []contract.AgentSnapshot{}}
	handler := HandleStopAgent(svc)

	result, err := handler(context.Background(), json.RawMessage(`{"agent_id":"agent-1","wait":true,"timeout_ms":100}`))
	if err != nil {
		t.Fatalf("HandleStopAgent() error = %v", err)
	}
	got := requireStopWaitMap(t, result)
	if got["stopped"] != true || got["state"] != "archived" {
		t.Fatalf("HandleStopAgent() = %#v, want missing archived agent treated as settled", got)
	}
}

func TestStopAgentWaitTrueTimeoutMentionsAgent(t *testing.T) {
	svc := &stopWaitService{snapshots: []contract.AgentSnapshot{{AgentID: "agent-1", State: "stopping"}}}
	handler := HandleStopAgent(svc)

	_, err := handler(context.Background(), json.RawMessage(`{"agent_id":"agent-1","wait":true,"timeout_ms":1}`))
	if err == nil || !strings.Contains(err.Error(), "agent-1") {
		t.Fatalf("HandleStopAgent() error = %v, want timeout mentioning agent", err)
	}
}

func TestStopAgentWaitTrueRejectsNegativeTimeoutBeforeArchive(t *testing.T) {
	svc := &stopWaitService{}
	handler := HandleStopAgent(svc)

	_, err := handler(context.Background(), json.RawMessage(`{"agent_id":"agent-1","wait":true,"timeout_ms":-1}`))
	if err == nil || !strings.Contains(err.Error(), "timeout_ms") {
		t.Fatalf("HandleStopAgent() error = %v, want timeout_ms validation error", err)
	}
	if svc.archiveCalls != 0 || svc.listCalls != 0 {
		t.Fatalf("archiveCalls=%d listCalls=%d, want validation before side effects", svc.archiveCalls, svc.listCalls)
	}
}

func TestStopAgentSchemaDocumentsWaitSettlement(t *testing.T) {
	props := stopAgentSchemaProperties(t)
	if _, ok := props["wait"]; !ok {
		t.Fatalf("stop_agent schema missing wait property: %#v", props)
	}
	if _, ok := props["timeout_ms"]; !ok {
		t.Fatalf("stop_agent schema missing timeout_ms property: %#v", props)
	}
	waitSchema, ok := props["wait"].(map[string]any)
	if !ok {
		t.Fatalf("wait schema type = %T, want map[string]any", props["wait"])
	}
	description, _ := waitSchema["description"].(string)
	if !strings.Contains(description, "state settlement") || strings.Contains(description, "interrupt") {
		t.Fatalf("wait description = %q, want settlement wording without interrupt", description)
	}
}

func requireStopWaitMap(t *testing.T, result any) map[string]any {
	t.Helper()
	got, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("HandleStopAgent() result type = %T, want map[string]any", result)
	}
	return got
}

func stopAgentSchemaProperties(t *testing.T) map[string]any {
	t.Helper()
	def := stopAgentToolDefinition(&stopWaitService{})
	props, ok := def.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties schema type = %T, want map[string]any", def.InputSchema["properties"])
	}
	return props
}

type stopWaitService struct {
	contract.AgentLifecyclePort
	snapshots    []contract.AgentSnapshot
	archiveCalls int
	listCalls    int
}

func (s *stopWaitService) ArchiveAgent(context.Context, string) (orch.ArchiveOutcome, error) {
	s.archiveCalls++
	return orch.ArchiveOutcome{RuntimeStopped: true}, nil
}

func (s *stopWaitService) StopAgent(context.Context, string) error {
	return fmt.Errorf("StopAgent should not be called when ArchiveAgent is available")
}

func (s *stopWaitService) ListAgents(context.Context) ([]contract.AgentSnapshot, error) {
	s.listCalls++
	if len(s.snapshots) == 0 {
		return nil, nil
	}
	if s.listCalls <= len(s.snapshots) {
		return []contract.AgentSnapshot{s.snapshots[s.listCalls-1]}, nil
	}
	return []contract.AgentSnapshot{s.snapshots[len(s.snapshots)-1]}, nil
}
