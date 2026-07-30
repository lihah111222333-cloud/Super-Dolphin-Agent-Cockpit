package archtest

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	rpcpkg "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
	goldentest "github.com/lihah111222333-cloud/super-dolphin-agent/internal/testutil/golden"
)

func TestCrossDomainGoldenAgentListDispatch(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.March, 25, 12, 0, 0, 0, time.UTC)
	svc := &goldentest.OrchestrationStub{
		ListAgentsFunc: func(context.Context) ([]contract.AgentSnapshot, error) {
			return listAgentSnapshots(at), nil
		},
	}
	server := rpcpkg.NewServer(rpcpkg.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(orchestration.ProvideRPCFacade(orchestration.RPCFacadeParams{State: svc}).Handlers)

	request := map[string]any{}
	params, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("json.Marshal(agent.list request) error = %v", err)
	}
	raw, err := server.Dispatch(context.Background(), "agent/list", params)
	if err != nil {
		t.Fatalf("Dispatch(agent.list) error = %v", err)
	}

	var response any
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("json.Unmarshal(agent.list response) error = %v", err)
	}

	goldentest.AssertJSON(t, goldentest.Case{
		BaseDir: "testdata/golden",
		Domain:  goldentest.DomainIntegration,
		Name:    "agent_list_dispatch",
	}, map[string]any{
		"method":       "agent/list",
		"request":      request,
		"response":     response,
		"v2_reference": agentListV2Reference(),
	})
}

func listAgentSnapshots(at time.Time) []contract.AgentSnapshot {
	return []contract.AgentSnapshot{
		{
			AgentID:    "agent-list-1",
			ID:         "agent-list-1",
			Name:       "list-alpha",
			ThreadID:   "thread-list-1",
			Cwd:        "/tmp/agent-list/alpha",
			State:      "idle",
			Provider:   "codex",
			LastReport: "alpha finished",
			CreatedAt:  at,
			UpdatedAt:  at,
			Assignment: &agentdto.Assignment{Title: "list-alpha", Description: "run alpha", AssignedAt: at},
			Progress:   agentdto.Progress{Status: "idle", UpdatedAt: at},
			Outcome:    &agentdto.Outcome{Kind: agentdto.OutcomeKindSuccess, Summary: "alpha finished", CompletedAt: at},
		},
		{
			AgentID:    "agent-list-2",
			ID:         "agent-list-2",
			Name:       "list-beta",
			ParentID:   "agent-list-1",
			ThreadID:   "thread-list-2",
			Cwd:        "/tmp/agent-list/beta",
			State:      "running",
			Provider:   "codex",
			CreatedAt:  at,
			UpdatedAt:  at,
			Assignment: &agentdto.Assignment{Title: "list-beta", Description: "run beta", AssignedAt: at},
			Progress:   agentdto.Progress{Status: "running", UpdatedAt: at},
			Outcome:    nil,
		},
	}
}

func agentListV2Reference() map[string]any {
	return map[string]any{
		"path": "internal/guards/golden/rpc_response/agent_list.golden.json",
		"result": []map[string]any{
			{
				"cwd":         "/tmp/agent-list/alpha",
				"id":          "agent-list-1",
				"last_report": "alpha finished",
				"name":        "list-alpha",
				"port":        0,
				"provider":    "codex",
				"state":       "idle",
				"thread_id":   "__THREAD_ID__",
			},
			{
				"cwd":       "/tmp/agent-list/beta",
				"id":        "agent-list-2",
				"name":      "list-beta",
				"parent_id": "agent-list-1",
				"port":      0,
				"provider":  "codex",
				"state":     "running",
				"thread_id": "__THREAD_ID__",
			},
		},
	}
}
