package dashboard

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

func TestListAgentStatusesUsesStoreAndTrimsStatus(t *testing.T) {
	t.Parallel()

	store := &stubAgentStatusStore{
		listResult: []AgentStatus{{AgentID: "agent-1", Status: "running"}},
	}
	svc := NewService(nil, nil, store, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	got, err := svc.ListAgentStatuses(context.Background(), " running ")
	if err != nil {
		t.Fatalf("ListAgentStatuses() error = %v", err)
	}
	if store.listCalls != 1 || store.listStatus != "running" {
		t.Fatalf("List() calls/status = (%d, %q)", store.listCalls, store.listStatus)
	}
	if len(got) != 1 || got[0].AgentID != "agent-1" {
		t.Fatalf("ListAgentStatuses() = %#v", got)
	}
}

func TestListAgentStatusesWithoutStoreReturnsEmpty(t *testing.T) {
	t.Parallel()

	svc := NewService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	got, err := svc.ListAgentStatuses(context.Background(), "running")
	if err != nil {
		t.Fatalf("ListAgentStatuses() error = %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("ListAgentStatuses() = %#v, want empty slice", got)
	}
}

func TestDashboardAgentStatusHandlerSupportsStatusFilter(t *testing.T) {
	t.Parallel()

	store := &stubAgentStatusStore{
		listResult: []AgentStatus{{AgentID: "agent-1", Status: "running"}},
	}
	svc := NewService(nil, nil, store, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	server := platformrpc.NewServer(platformrpc.Params{Config: &contract.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewDashboardHandlers(svc).Handlers)

	result, err := server.Dispatch(context.Background(), "dashboard/agentStatus", json.RawMessage(`{"status":"running"}`))
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	var response struct {
		Agents []AgentStatus `json:"agents"`
	}
	if err := json.Unmarshal(result, &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if store.listCalls != 1 || store.listStatus != "running" {
		t.Fatalf("List() calls/status = (%d, %q)", store.listCalls, store.listStatus)
	}
	if len(response.Agents) != 1 || response.Agents[0].AgentID != "agent-1" {
		t.Fatalf("Dispatch() response = %#v", response)
	}
}

type stubAgentStatusStore struct {
	listResult []AgentStatus
	listErr    error
	listStatus string
	listCalls  int
}

func (s *stubAgentStatusStore) List(_ context.Context, status string) ([]AgentStatus, error) {
	s.listCalls++
	s.listStatus = status
	if s.listErr != nil {
		return nil, s.listErr
	}
	return append([]AgentStatus(nil), s.listResult...), nil
}
