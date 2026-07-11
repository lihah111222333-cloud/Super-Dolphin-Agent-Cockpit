package codexapp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kelindar/event"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/unified"
	"github.com/lihah111222333-cloud/super-dolphin-agent/pkg/skillmetrics"
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
	msg := rawParams(t, map[string]any{"name": "test_dynamic_echo", "arguments": map[string]any{"name": "demo"}})
	out := enrichToolCallParams(msg, "agent-42", "")
	var got map[string]any
	if err := json.Unmarshal(out.Params, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v, _ := got["agentId"].(string); v != "agent-42" {
		t.Fatalf("agentId = %q, want agent-42", v)
	}
	if v, _ := got["name"].(string); v != "test_dynamic_echo" {
		t.Fatalf("name lost: %q", v)
	}
}

// TestEnrichToolCallParams_OverridesExisting 锁安全边界：params 已含 agentId 时，
// 仍以 session.agentID 为准，避免信任 Codex payload / fixture 里的外部 agentId。
func TestEnrichToolCallParams_OverridesExisting(t *testing.T) {
	msg := rawParams(t, map[string]any{"name": "x", "agentId": "from-codex", "agent_id": "snake"})
	out := enrichToolCallParams(msg, "agent-override", "")
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
	out := enrichToolCallParams(msg, "", "")
	if string(out.Params) != string(msg.Params) {
		t.Fatalf("params mutated when agentID empty: %s", out.Params)
	}
	out2 := enrichToolCallParams(msg, "   ", "")
	if string(out2.Params) != string(msg.Params) {
		t.Fatalf("params mutated when agentID whitespace: %s", out2.Params)
	}
}

// TestEnrichToolCallParams_EmptyParams 空 params → 原样返回，不构造新对象。
// 防御 nil/空 params 路径。
func TestEnrichToolCallParams_EmptyParams(t *testing.T) {
	msg := RawMessage{Method: "item/tool/call", ID: json.RawMessage(`1`)}
	out := enrichToolCallParams(msg, "agent-1", "")
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
	out := enrichToolCallParams(msg, "agent-1", "")
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
	msg := rawParams(t, map[string]any{"name": "test_dynamic_echo", "arguments": args})
	out := enrichToolCallParams(msg, "agent-99", "")
	if !strings.Contains(string(out.Params), `"anchor":"Usage"`) {
		t.Fatalf("arguments lost: %s", out.Params)
	}
}

func TestShouldWarnToolCWDTraceUsesOrchestrationContractRegistry(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{name: "file", want: true},
		{name: "code_execute", want: true},
		{name: "launch_agent", want: true},
		{name: "send_message", want: false},
	} {
		if got := shouldWarnToolCWDTrace(tc.name); got != tc.want {
			t.Fatalf("shouldWarnToolCWDTrace(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestPublishToolCallEnd_FileReadEmptySuccessResultFailsWithPathGuidance(t *testing.T) {
	bus := event.NewDispatcher()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	endCh := make(chan tooldto.ToolCallEnd, 1)
	cancelEnd := event.Subscribe(bus, func(ev tooldto.ToolCallEnd) { endCh <- ev })
	defer cancelEnd()
	defer func() { _ = bus.Close() }()

	s := newInboundTestSession(context.Background(), nil, &ServerManager{})
	s.dispatcher = dispatcher

	msg := rawParams(t, map[string]any{
		"name": "file",
		"arguments": map[string]any{
			"action":    "read_file",
			"file_path": "/home/user/Downloads/missing.md",
		},
	})
	call := preparedToolCall{
		header:  toolCallHeader("agent-1", "turn-1", "call-file-empty", "file", time.Now()),
		params:  msg,
		started: time.Now(),
	}
	result := map[string]any{
		"contentItems":      []map[string]any{{"type": "inputText"}},
		"structuredContent": map[string]any{"value": ""},
		"success":           true,
	}

	s.publishToolCallEnd(call, result, nil)

	end := waitToolCallEnd(t, endCh)
	if end.Success {
		t.Fatalf("ToolCallEnd.Success = true, want false for empty file read result: %+v", end)
	}
	for _, want := range []string{"missing.md", "does not exist", "outside workspace"} {
		if !strings.Contains(end.Error, want) {
			t.Fatalf("ToolCallEnd.Error = %q, want %q", end.Error, want)
		}
	}
	if strings.Contains(end.Result, `"success":true`) {
		t.Fatalf("ToolCallEnd.Result = %q, want path guidance instead of empty success envelope", end.Result)
	}
}

func TestEnrichToolCallParams_InjectsTrustedCWD(t *testing.T) {
	msg := rawParams(t, map[string]any{
		"name":      "file",
		"cwd":       "/untrusted/model/cwd",
		"arguments": map[string]any{"action": "read_file", "file_path": "go.mod"},
	})
	out := enrichToolCallParams(msg, "agent-42", "/repo/worktree")
	var got map[string]any
	if err := json.Unmarshal(out.Params, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v, _ := got["_cwd"].(string); v != "/repo/worktree" {
		t.Fatalf("_cwd = %q, want /repo/worktree", v)
	}
	if _, ok := got["cwd"]; ok {
		t.Fatalf("public cwd alias must be removed after trusted _cwd injection: %v", got)
	}
	if v, _ := got["agentId"].(string); v != "agent-42" {
		t.Fatalf("agentId = %q, want agent-42", v)
	}
}

func TestEnrichToolCallParamsStrictInjectsTrustedWorkspaceRootsAndRemovesPublicAliases(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	api := filepath.Join(repo, "packages", "api")
	msg := rawParams(t, map[string]any{
		"name":           "file",
		"cwd":            "/forged/cwd",
		"workspaceRoots": []string{"/forged/camel"},
		"workspace_roots": []string{
			"/forged/snake",
		},
		"_workspace_roots": []string{"/forged/private-snake"},
		"arguments": map[string]any{
			"file_path":       "go.mod",
			"_workspaceRoots": []string{"/forged/arguments"},
		},
	})

	out, err := enrichToolCallParamsStrict(msg, "agent-42", "thread-42", "call-42", repo, []string{repo, api})
	if err != nil {
		t.Fatalf("enrichToolCallParamsStrict() error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Params, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	assertToolParamKeysAbsent(t, got, "cwd", "workspaceRoots", "workspace_roots", "_workspace_roots")
	roots, ok := got["_workspaceRoots"].([]any)
	if !ok {
		t.Fatalf("_workspaceRoots = %#v, want array", got["_workspaceRoots"])
	}
	want := []string{repo, api}
	if len(roots) != len(want) {
		t.Fatalf("_workspaceRoots length = %d, want %d: %#v", len(roots), len(want), roots)
	}
	for i, wantRoot := range want {
		if roots[i] != wantRoot {
			t.Fatalf("_workspaceRoots[%d] = %#v, want %q", i, roots[i], wantRoot)
		}
	}
}

func TestEnrichToolCallParamsStrictResolvesRelativeWorkspaceRootsAgainstCWD(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	msg := rawParams(t, map[string]any{
		"name":      "file",
		"arguments": map[string]any{"file_path": "go.mod"},
	})

	out, err := enrichToolCallParamsStrict(msg, "agent-42", "thread-42", "call-42", repo, []string{"packages/api"})
	if err != nil {
		t.Fatalf("enrichToolCallParamsStrict() error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Params, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	roots, ok := got["_workspaceRoots"].([]any)
	if !ok {
		t.Fatalf("_workspaceRoots = %#v, want array", got["_workspaceRoots"])
	}
	want := []string{repo, filepath.Join(repo, "packages", "api")}
	if len(roots) != len(want) {
		t.Fatalf("_workspaceRoots length = %d, want %d: %#v", len(roots), len(want), roots)
	}
	for i, wantRoot := range want {
		if roots[i] != wantRoot {
			t.Fatalf("_workspaceRoots[%d] = %#v, want %q", i, roots[i], wantRoot)
		}
	}
}

func TestTrustedWorkspaceRootsDropsRelativeAdditionalRootsWithoutCWD(t *testing.T) {
	got := trustedWorkspaceRoots("", []string{"packages/api"})
	if len(got) != 0 {
		t.Fatalf("trustedWorkspaceRoots() = %#v, want empty without trusted cwd", got)
	}
}

func TestTrustedWorkspaceRootsDropsAdditionalRootsWithoutTrustedCWD(t *testing.T) {
	for name, cwd := range map[string]string{
		"missing cwd":  "",
		"relative cwd": ".",
	} {
		t.Run(name, func(t *testing.T) {
			got := trustedWorkspaceRoots(cwd, []string{"/repo/packages/api"})
			if len(got) != 0 {
				t.Fatalf("trustedWorkspaceRoots() = %#v, want empty without trusted cwd", got)
			}
		})
	}
}

func TestEnrichToolCallParamsStrictDoesNotPromoteAdditionalRootWithoutTrustedCWD(t *testing.T) {
	msg := rawParams(t, map[string]any{
		"name":      "file",
		"arguments": map[string]any{"file_path": "go.mod"},
	})

	out, err := enrichToolCallParamsStrict(msg, "agent-42", "thread-42", "call-42", ".", []string{"/repo/packages/api"})
	if err != nil {
		t.Fatalf("enrichToolCallParamsStrict() error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Params, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	rawRoots, ok := got["_workspaceRoots"]
	if !ok || rawRoots == nil {
		return
	}
	roots, ok := rawRoots.([]any)
	if !ok {
		t.Fatalf("_workspaceRoots = %#v, want array or null", rawRoots)
	}
	if len(roots) != 0 {
		t.Fatalf("_workspaceRoots = %#v, want empty without trusted cwd", roots)
	}
}

func assertToolParamKeysAbsent(t *testing.T, got map[string]any, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if _, ok := got[key]; ok {
			t.Fatalf("%s alias must be removed: %v", key, got)
		}
	}
}
