package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/testutil/golden"
)

func TestListAgentsHandlerSkipsReportHydrationWhenReportsOmitted(t *testing.T) {
	deadlineSeen := false
	handler := HandleListAgents(&golden.OrchestrationStub{
		ListAgentsFunc: func(ctx context.Context) ([]contract.AgentSnapshot, error) {
			_, deadlineSeen = ctx.Deadline()
			return []contract.AgentSnapshot{{ID: "agent-idle", AgentID: "agent-idle", State: "idle", LastReport: "must be stripped"}}, nil
		},
		GetReportFunc: func(context.Context, string) (contract.AgentReportResult, error) {
			t.Fatalf("GetReport should not be called when include_reports=false")
			return contract.AgentReportResult{}, nil
		},
	})

	result, err := handler(context.Background(), json.RawMessage(`{"include_reports":false}`))
	if err != nil {
		t.Fatalf("HandleListAgents() error = %v", err)
	}
	got, ok := result.([]contract.AgentSnapshot)
	if !ok || !deadlineSeen {
		t.Fatalf("HandleListAgents() result type = %T, want []contract.AgentSnapshot with deadline", result)
	}
	if len(got) != 1 || got[0].AgentID != "agent-idle" || got[0].LastReport != "" {
		t.Fatalf("HandleListAgents() = %#v, want compact snapshot without report", got)
	}
}

func TestListAgentsHandlerCanIncludeInactiveReportsAndLimit(t *testing.T) {
	handler := HandleListAgents(&golden.OrchestrationStub{
		ListAgentsFunc: func(context.Context) ([]contract.AgentSnapshot, error) {
			return []contract.AgentSnapshot{
				{ID: "agent-stopped", AgentID: "agent-stopped", State: "stopped"},
				{ID: "agent-idle", AgentID: "agent-idle", State: "idle"},
			}, nil
		},
		GetReportFunc: func(ctx context.Context, agentID string) (contract.AgentReportResult, error) {
			if _, ok := ctx.Deadline(); !ok {
				t.Fatalf("GetReport context had no deadline")
			}
			if agentID != "agent-stopped" {
				t.Fatalf("GetReport agentID = %q, want only limited first agent", agentID)
			}
			return contract.AgentReportResult{AgentID: agentID, Report: "old report", State: "stopped"}, nil
		},
	})

	result, err := handler(context.Background(), json.RawMessage(`{"include_inactive":true,"include_reports":true,"limit":1}`))
	if err != nil {
		t.Fatalf("HandleListAgents() error = %v", err)
	}
	got, ok := result.([]contract.AgentSnapshot)
	if !ok || len(got) != 1 || got[0].AgentID != "agent-stopped" || got[0].LastReport != "old report" {
		t.Fatalf("HandleListAgents() = %#v, want stopped with report", result)
	}
}

func TestListAgentsHandlerIncludeReportsKeepsAgentsWithoutReport(t *testing.T) {
	handler := HandleListAgents(&golden.OrchestrationStub{
		ListAgentsFunc: func(context.Context) ([]contract.AgentSnapshot, error) {
			return []contract.AgentSnapshot{
				{ID: "agent-provisioning", AgentID: "agent-provisioning", State: "provisioning"},
				{ID: "agent-idle", AgentID: "agent-idle", State: "idle"},
			}, nil
		},
		GetReportFunc: func(_ context.Context, agentID string) (contract.AgentReportResult, error) {
			if agentID == "agent-provisioning" {
				return contract.AgentReportResult{AgentID: agentID, State: "provisioning"}, fmt.Errorf("%w: persisted report missing for %s", contract.ErrAgentNotFound, agentID)
			}
			return contract.AgentReportResult{AgentID: agentID, Report: "ready report", State: "idle"}, nil
		},
	})

	result, err := handler(context.Background(), json.RawMessage(`{"include_reports":true}`))
	if err != nil {
		t.Fatalf("HandleListAgents() error = %v", err)
	}
	got, ok := result.([]contract.AgentSnapshot)
	if !ok || len(got) != 2 {
		t.Fatalf("HandleListAgents() = %#v, want two snapshots", result)
	}
	if got[0].AgentID != "agent-provisioning" || got[0].LastReport != "" {
		t.Fatalf("HandleListAgents() first snapshot = %#v, want provisioning agent without report", got[0])
	}
	if got[1].AgentID != "agent-idle" || got[1].LastReport != "ready report" {
		t.Fatalf("HandleListAgents() second snapshot = %#v, want hydrated idle report", got[1])
	}
}

func TestListAgentsHandlerIncludeReportsFailsOnReportError(t *testing.T) {
	handler := HandleListAgents(&golden.OrchestrationStub{
		ListAgentsFunc: func(context.Context) ([]contract.AgentSnapshot, error) {
			return []contract.AgentSnapshot{{ID: "agent-1", AgentID: "agent-1", State: "idle"}}, nil
		},
		GetReportFunc: func(context.Context, string) (contract.AgentReportResult, error) {
			return contract.AgentReportResult{}, fmt.Errorf("report store down")
		},
	})

	_, err := handler(context.Background(), json.RawMessage(`{"include_reports":true}`))
	if err == nil || !strings.Contains(err.Error(), "hydrate agent report") {
		t.Fatalf("HandleListAgents() error = %v, want report hydration failure", err)
	}
}

func TestListAgentsEnvelopeGuidesSingleAndBatchReportReads(t *testing.T) {
	handler := HandleListAgents(&golden.OrchestrationStub{
		ListAgentsFunc: func(context.Context) ([]contract.AgentSnapshot, error) {
			return []contract.AgentSnapshot{{ID: "agent-1", AgentID: "agent-1", State: "idle"}}, nil
		},
	})

	result, err := handler(context.Background(), json.RawMessage(`{"envelope":true}`))
	if err != nil {
		t.Fatalf("HandleListAgents() error = %v", err)
	}
	got, ok := result.(ListAgentsOutput)
	if !ok {
		t.Fatalf("HandleListAgents() result type = %T, want ListAgentsOutput", result)
	}
	requireContains(t, got.Hint, "get_agent_report")
	requireContains(t, got.Hint, "get_agent_reports")
	requireNotContains(t, got.Hint, "next: use get_agent_report")
}
