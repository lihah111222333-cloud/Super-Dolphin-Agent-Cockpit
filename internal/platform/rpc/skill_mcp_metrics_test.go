package rpc

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/pkg/skillmetrics"
)

func TestSkillMCPMetricReportHandlerIncrementsParentCounters(t *testing.T) {
	skillmetrics.ResetForTesting()
	t.Cleanup(skillmetrics.ResetForTesting)

	server := newTestServer()
	server.Register(NewSkillMCPMetricHandlers().Handlers)

	for i, payload := range []string{
		`{"tool":"skill_expand_body","outcome":"success"}`,
		`{"tool":"skill_expand_body","outcome":"approval_required"}`,
		`{"tool":"skill_expand_body","outcome":"host_tool_error"}`,
	} {
		if _, err := server.Dispatch(context.Background(), skillmetrics.SkillMCPReportMethod, json.RawMessage(payload)); err != nil {
			t.Fatalf("Dispatch report %d error = %v", i, err)
		}
	}

	snap := skillmetrics.Read()
	if snap.SkillMCPToolCallTotal != 3 || snap.SkillMCPToolSuccessTotal != 1 || snap.SkillMCPApprovalRequiredTotal != 1 || snap.SkillMCPToolErrorTotal != 1 {
		t.Fatalf("skill MCP parent counters mismatch: %+v", snap)
	}
}
