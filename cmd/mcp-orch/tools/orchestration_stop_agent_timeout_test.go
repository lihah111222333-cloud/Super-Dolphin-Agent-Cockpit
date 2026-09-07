package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	orch "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpcommon "github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

type stopTimeoutService struct {
	stopWaitService
	archiveErr, listErr error
}

// ArchiveAgent 注入生命周期边界错误而不启动真实 provider。
func (s *stopTimeoutService) ArchiveAgent(ctx context.Context, id string) (orch.ArchiveOutcome, error) {
	if s.archiveErr != nil {
		return orch.ArchiveOutcome{}, s.archiveErr
	}
	return s.stopWaitService.ArchiveAgent(ctx, id)
}

// ListAgents 注入快照错误并复用停止状态 fixture。
func (s *stopTimeoutService) ListAgents(ctx context.Context) ([]contract.AgentSnapshot, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.stopWaitService.ListAgents(ctx)
}

// TestStopAgentTimeoutContract 锁定生命周期与等待超时的领域错误，取消不得冒充超时。
func TestStopAgentTimeoutContract(t *testing.T) {
	for _, tc := range []struct {
		name, boundary string
		cause          error
		code           string
	}{
		{"archive-timeout", "archive", context.DeadlineExceeded, "stop_agent_timeout"},
		{"archive-cancel", "archive", context.Canceled, "stop_agent_canceled"},
		{"snapshot-timeout", "snapshot", context.DeadlineExceeded, "stop_agent_timeout"},
		{"snapshot-cancel", "snapshot", context.Canceled, "stop_agent_canceled"},
		{"wait-timeout", "wait", context.DeadlineExceeded, "stop_agent_timeout"},
		{"parent-cancel", "parent", context.Canceled, "stop_agent_canceled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &stopTimeoutService{stopWaitService: stopWaitService{snapshots: []contract.AgentSnapshot{{AgentID: "agent-1", State: "stopping"}}}}
			wrapped := fmt.Errorf("synthetic-provider-token: %w", tc.cause)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch tc.boundary {
			case "archive":
				svc.archiveErr = wrapped
			case "snapshot":
				svc.listErr = wrapped
			case "parent":
				cancel()
			}
			_, err := HandleStopAgent(svc)(ctx, json.RawMessage(`{"agent_id":"agent-1","wait":true,"timeout_ms":1}`))
			if !errors.Is(err, tc.cause) {
				t.Fatalf("error=%v, want cause %v", err, tc.cause)
			}
			assertStopTimeoutEnvelope(t, err, tc.code)
		})
	}
	t.Run("settled", TestStopAgentWaitTrueReturnsStoppedWhenStateSettles)
	t.Run("no-wait", TestStopAgentWaitFalsePreservesExistingResult)
}

// assertStopTimeoutEnvelope 验证公共 MCP envelope 的码、操作提示与脱敏。
func assertStopTimeoutEnvelope(t *testing.T, err error, code string) {
	t.Helper()
	envelope := mcpcommon.NewToolErrorEnvelope("stop_agent", err)
	if envelope.Code != code || envelope.Retryable {
		t.Fatalf("wrong stop classification: %+v", envelope)
	}
	if !strings.Contains(envelope.Hint, "list_agents") || !strings.Contains(envelope.Error, "agent-1") {
		t.Fatalf("missing stop diagnostics: %+v", envelope)
	}
	raw := envelope.Error + envelope.Hint
	for _, forbidden := range []string{"synthetic-provider-token", "language server", "indexing", "lsp_timeout"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("unrelated or private detail: %s", forbidden)
		}
	}
}
