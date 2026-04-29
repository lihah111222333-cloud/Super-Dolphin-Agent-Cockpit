package codexapp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/pkg/skillmetrics"
)

// rawParams 帮助构造 RawMessage with given params.
func rawParams(t *testing.T, m map[string]any) RawMessage {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return RawMessage{Method: "item/tool/call", ID: json.RawMessage(`1`), Params: raw}
}

// TestEnrichToolCallParams_InjectsAgentID 锁核心契约：codex 发的 params 不含 agentId 时，
// 本函数把 session.agentID 注入。
func TestEnrichToolCallParams_InjectsAgentID(t *testing.T) {
	msg := rawParams(t, map[string]any{"name": "skill_expand_body", "arguments": map[string]any{"name": "demo"}})
	out := enrichToolCallParams(msg, "agent-42")
	var got map[string]any
	if err := json.Unmarshal(out.Params, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v, _ := got["agentId"].(string); v != "agent-42" {
		t.Fatalf("agentId = %q, want agent-42", v)
	}
	if v, _ := got["name"].(string); v != "skill_expand_body" {
		t.Fatalf("name lost: %q", v)
	}
}

// TestEnrichToolCallParams_OverridesExisting 锁安全边界：params 已含 agentId 时，
// 仍以 session.agentID 为准，避免信任 Codex payload / fixture 里的外部 agentId。
func TestEnrichToolCallParams_OverridesExisting(t *testing.T) {
	msg := rawParams(t, map[string]any{"name": "x", "agentId": "from-codex", "agent_id": "snake"})
	out := enrichToolCallParams(msg, "agent-override")
	var got map[string]any
	_ = json.Unmarshal(out.Params, &got)
	if v, _ := got["agentId"].(string); v != "agent-override" {
		t.Fatalf("agentId = %q, want agent-override", v)
	}
	if _, ok := got["agent_id"]; ok {
		t.Fatalf("agent_id alias must be removed after canonical overwrite: %v", got)
	}
}

// TestEnrichToolCallParams_EmptyAgentID 空 agentID（session 还没初始化）→ 原样返回。
func TestEnrichToolCallParams_EmptyAgentID(t *testing.T) {
	msg := rawParams(t, map[string]any{"name": "x"})
	out := enrichToolCallParams(msg, "")
	if string(out.Params) != string(msg.Params) {
		t.Fatalf("params mutated when agentID empty: %s", out.Params)
	}
	out2 := enrichToolCallParams(msg, "   ")
	if string(out2.Params) != string(msg.Params) {
		t.Fatalf("params mutated when agentID whitespace: %s", out2.Params)
	}
}

// TestEnrichToolCallParams_EmptyParams 空 params → 原样返回，不构造新对象。
// 防御 nil/空 params 路径。
func TestEnrichToolCallParams_EmptyParams(t *testing.T) {
	msg := RawMessage{Method: "item/tool/call", ID: json.RawMessage(`1`)}
	out := enrichToolCallParams(msg, "agent-1")
	if len(out.Params) != 0 {
		t.Fatalf("empty params should stay empty, got %s", out.Params)
	}
}

// TestEnrichToolCallParams_BadJSON params 不是合法 JSON object → 原样返回，不报错。
// fail-soft 契约：本函数不应让一个 bad payload 升级成 panic。
func TestEnrichToolCallParams_BadJSON(t *testing.T) {
	skillmetrics.ResetForTesting()
	t.Cleanup(skillmetrics.ResetForTesting)
	msg := RawMessage{Method: "item/tool/call", ID: json.RawMessage(`1`), Params: json.RawMessage(`not-json`)}
	out := enrichToolCallParams(msg, "agent-1")
	if string(out.Params) != "not-json" {
		t.Fatalf("bad json should be passed through, got %s", out.Params)
	}
	if got := skillmetrics.EnrichFailures(); got != 1 {
		t.Fatalf("EnrichFailures = %d, want 1", got)
	}
}

// TestEnrichToolCallParams_PreservesOtherFields 不破坏 arguments / 其他字段。
func TestEnrichToolCallParams_PreservesOtherFields(t *testing.T) {
	args := map[string]any{"name": "demo", "anchor": "Usage"}
	msg := rawParams(t, map[string]any{"name": "skill_expand_body", "arguments": args})
	out := enrichToolCallParams(msg, "agent-99")
	if !strings.Contains(string(out.Params), `"anchor":"Usage"`) {
		t.Fatalf("arguments lost: %s", out.Params)
	}
}
