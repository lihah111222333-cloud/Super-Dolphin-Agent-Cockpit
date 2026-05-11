package nodeexec

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// ====================================================================
// F1.2 / 蓝图 v2 §7 / 实施计划 F1.2 行：
// AgentExecutor 处理 cfg.Inputs：注入 prev nodes result + sharedfile。
// 集成测试：节点 B 看到节点 A.result。
// ====================================================================

// stubPrevNodeResultReader 是 PrevNodeResultReader 的测试假实现。
// results 按 (dagKey|nodeKey) 索引，未命中 → exists=false。
// err 注入 → 任何调用都返回该错误（覆盖 missing-not-found 之上）。
type stubPrevNodeResultReader struct {
	results map[string]json.RawMessage
	err     error
	calls   int
}

func (s *stubPrevNodeResultReader) GetNodeResult(_ context.Context, dagKey, nodeKey string) (json.RawMessage, bool, error) {
	s.calls++
	if s.err != nil {
		return nil, false, s.err
	}
	v, ok := s.results[dagKey+"|"+nodeKey]
	return v, ok, nil
}

// stubSharedfileReader 是 SharedfileReader 的测试假实现。
type stubSharedfileReader struct {
	contents map[string]string
	err      error
	calls    int
}

func (s *stubSharedfileReader) ReadSharedfile(_ context.Context, path string) (string, bool, error) {
	s.calls++
	if s.err != nil {
		return "", false, s.err
	}
	v, ok := s.contents[path]
	return v, ok, nil
}

// TestAgentExecutor_Inputs_FromNodes_Single 验证「节点 B 看到节点 A.result」核心场景：
// cfg.Inputs.FromNodes 引用一个上游节点，结果被注入到 LaunchRequest.Prompt。
func TestAgentExecutor_Inputs_FromNodes_Single(t *testing.T) {
	launcher := &stubAgentLauncher{}
	prev := &stubPrevNodeResultReader{results: map[string]json.RawMessage{
		"dag-x|node-a": json.RawMessage(`{"summary":"hello from A"}`),
	}}
	exec := NewAgentExecutorWithInputs(launcher, nil, prev, nil)

	cfg := AgentNodeConfig{
		Exec:   AgentExecConfig{AgentKey: "implementer"},
		Inputs: InputsConfig{FromNodes: []string{"node-a"}},
	}
	node := makeAgentNode(t, cfg)
	node.NodeKey = "node-b" // 当前节点是 B

	out, err := exec.Execute(context.Background(), node, RunContext{DagKey: "dag-x", NodeKey: "node-b"})
	if err != nil {
		t.Fatalf("Execute() = %v", err)
	}
	if out.Status != NodeStatusDone {
		t.Fatalf("Status = %q, want %q (out=%+v)", out.Status, NodeStatusDone, out)
	}
	if launcher.called != 1 {
		t.Fatalf("launcher.called = %d, want 1", launcher.called)
	}
	prompt := launcher.lastReq.Prompt
	if !strings.Contains(prompt, "[inputs.from_nodes]") {
		t.Fatalf("Prompt missing [inputs.from_nodes] header; got: %q", prompt)
	}
	if !strings.Contains(prompt, "## node:node-a") {
		t.Fatalf("Prompt missing node:node-a label; got: %q", prompt)
	}
	if !strings.Contains(prompt, `"summary":"hello from A"`) {
		t.Fatalf("Prompt missing node-a.result content; got: %q", prompt)
	}
}

// TestAgentExecutor_Inputs_FromNodes_Multiple 验证多个 from_nodes 顺序保持配置顺序。
func TestAgentExecutor_Inputs_FromNodes_Multiple(t *testing.T) {
	launcher := &stubAgentLauncher{}
	prev := &stubPrevNodeResultReader{results: map[string]json.RawMessage{
		"dag-x|node-a": json.RawMessage(`{"a":1}`),
		"dag-x|node-c": json.RawMessage(`{"c":3}`),
	}}
	exec := NewAgentExecutorWithInputs(launcher, nil, prev, nil)

	cfg := AgentNodeConfig{
		Exec:   AgentExecConfig{AgentKey: "implementer"},
		Inputs: InputsConfig{FromNodes: []string{"node-a", "node-c"}},
	}
	node := makeAgentNode(t, cfg)

	if _, err := exec.Execute(context.Background(), node, RunContext{DagKey: "dag-x"}); err != nil {
		t.Fatalf("Execute() = %v", err)
	}
	prompt := launcher.lastReq.Prompt
	idxA := strings.Index(prompt, "## node:node-a")
	idxC := strings.Index(prompt, "## node:node-c")
	if idxA < 0 || idxC < 0 {
		t.Fatalf("Prompt missing one of node labels; got: %q", prompt)
	}
	if idxA > idxC {
		t.Fatalf("Prompt order wrong: node-a should come before node-c; got: %q", prompt)
	}
}

// TestAgentExecutor_Inputs_FromSharedfiles_Single 验证 sharedfile 注入。
func TestAgentExecutor_Inputs_FromSharedfiles_Single(t *testing.T) {
	launcher := &stubAgentLauncher{}
	sf := &stubSharedfileReader{contents: map[string]string{
		"plan.md": "# Plan\n- step1\n- step2",
	}}
	exec := NewAgentExecutorWithInputs(launcher, nil, nil, sf)

	cfg := AgentNodeConfig{
		Exec:   AgentExecConfig{AgentKey: "implementer"},
		Inputs: InputsConfig{FromSharedfiles: []string{"plan.md"}},
	}
	node := makeAgentNode(t, cfg)

	if _, err := exec.Execute(context.Background(), node, RunContext{DagKey: "dag-x"}); err != nil {
		t.Fatalf("Execute() = %v", err)
	}
	prompt := launcher.lastReq.Prompt
	if !strings.Contains(prompt, "[inputs.from_sharedfiles]") {
		t.Fatalf("Prompt missing [inputs.from_sharedfiles] header; got: %q", prompt)
	}
	if !strings.Contains(prompt, "## sharedfile:plan.md") {
		t.Fatalf("Prompt missing sharedfile label; got: %q", prompt)
	}
	if !strings.Contains(prompt, "step1") {
		t.Fatalf("Prompt missing sharedfile content; got: %q", prompt)
	}
}

// TestAgentExecutor_Inputs_Mixed_FromNodes_AndSharedfiles 验证两类来源混合 + first_turn 拼接。
func TestAgentExecutor_Inputs_Mixed_FromNodes_AndSharedfiles(t *testing.T) {
	launcher := &stubAgentLauncher{}
	prev := &stubPrevNodeResultReader{results: map[string]json.RawMessage{
		"dag-x|node-a": json.RawMessage(`"result-A"`),
	}}
	sf := &stubSharedfileReader{contents: map[string]string{
		"plan.md": "PLAN-CONTENT",
	}}
	exec := NewAgentExecutorWithInputs(launcher, nil, prev, sf)

	cfg := AgentNodeConfig{
		Exec: AgentExecConfig{AgentKey: "implementer"},
		Inputs: InputsConfig{
			FromNodes:       []string{"node-a"},
			FromSharedfiles: []string{"plan.md"},
		},
		FirstTurn: "do task X",
	}
	node := makeAgentNode(t, cfg)

	if _, err := exec.Execute(context.Background(), node, RunContext{DagKey: "dag-x"}); err != nil {
		t.Fatalf("Execute() = %v", err)
	}
	prompt := launcher.lastReq.Prompt

	// 顺序：from_nodes 节先于 from_sharedfiles 节先于 first_turn。
	idxFromNodes := strings.Index(prompt, "[inputs.from_nodes]")
	idxFromShared := strings.Index(prompt, "[inputs.from_sharedfiles]")
	idxFirstTurn := strings.Index(prompt, "[first_turn]")
	if idxFromNodes < 0 || idxFromShared < 0 || idxFirstTurn < 0 {
		t.Fatalf("Prompt missing one of section headers; got: %q", prompt)
	}
	if !(idxFromNodes < idxFromShared && idxFromShared < idxFirstTurn) {
		t.Fatalf("section order wrong (from_nodes < from_sharedfiles < first_turn expected); got: %q", prompt)
	}
	if !strings.Contains(prompt, "result-A") || !strings.Contains(prompt, "PLAN-CONTENT") || !strings.Contains(prompt, "do task X") {
		t.Fatalf("Prompt missing expected content; got: %q", prompt)
	}
}

// TestAgentExecutor_Inputs_EmptyInputs_BackwardsCompat 验证 cfg.Inputs 为空时
// LaunchRequest.Prompt 与 F1.1 保持一致（仅 first_turn）。
func TestAgentExecutor_Inputs_EmptyInputs_BackwardsCompat(t *testing.T) {
	launcher := &stubAgentLauncher{}
	// 即使 prev/sharedfile readers 注入了，但 inputs 为空也不应被调用。
	prev := &stubPrevNodeResultReader{}
	sf := &stubSharedfileReader{}
	exec := NewAgentExecutorWithInputs(launcher, nil, prev, sf)

	cfg := AgentNodeConfig{
		Exec:      AgentExecConfig{AgentKey: "implementer"},
		FirstTurn: "do task Z",
	}
	node := makeAgentNode(t, cfg)

	out, err := exec.Execute(context.Background(), node, RunContext{DagKey: "dag-x"})
	if err != nil {
		t.Fatalf("Execute() = %v", err)
	}
	if out.Status != NodeStatusDone {
		t.Fatalf("Status = %q, want done", out.Status)
	}
	if launcher.lastReq.Prompt != "do task Z" {
		t.Fatalf("Prompt = %q, want %q (cfg.Inputs empty → identical to F1.1)", launcher.lastReq.Prompt, "do task Z")
	}
	if prev.calls != 0 || sf.calls != 0 {
		t.Fatalf("readers should not be invoked when inputs empty (prev=%d, sf=%d)", prev.calls, sf.calls)
	}
}

// TestAgentExecutor_Inputs_FromNodes_UnknownKey_Validation 验证 from_nodes 引用
// 不存在的 node_key → validation 失败、不调 launcher。
func TestAgentExecutor_Inputs_FromNodes_UnknownKey_Validation(t *testing.T) {
	launcher := &stubAgentLauncher{}
	prev := &stubPrevNodeResultReader{results: map[string]json.RawMessage{
		"dag-x|node-a": json.RawMessage(`{}`),
	}}
	exec := NewAgentExecutorWithInputs(launcher, nil, prev, nil)

	cfg := AgentNodeConfig{
		Exec:   AgentExecConfig{AgentKey: "implementer"},
		Inputs: InputsConfig{FromNodes: []string{"node-does-not-exist"}},
	}
	node := makeAgentNode(t, cfg)

	out, err := exec.Execute(context.Background(), node, RunContext{DagKey: "dag-x"})
	if err != nil {
		t.Fatalf("Execute() = %v", err)
	}
	if out.Status != NodeStatusFailed {
		t.Fatalf("Status = %q, want failed", out.Status)
	}
	if out.FailureClass != FailureClassValidation {
		t.Fatalf("FailureClass = %q, want validation", out.FailureClass)
	}
	if launcher.called != 0 {
		t.Fatalf("launcher should not be called on validation failure, got %d", launcher.called)
	}
	if !strings.Contains(out.ErrorSummary, "node-does-not-exist") {
		t.Fatalf("ErrorSummary missing offending node_key; got %q", out.ErrorSummary)
	}
}

// TestAgentExecutor_Inputs_FromSharedfiles_Missing_Validation 验证 sharedfile
// 不存在 → validation 失败。
func TestAgentExecutor_Inputs_FromSharedfiles_Missing_Validation(t *testing.T) {
	launcher := &stubAgentLauncher{}
	sf := &stubSharedfileReader{contents: map[string]string{}} // 空
	exec := NewAgentExecutorWithInputs(launcher, nil, nil, sf)

	cfg := AgentNodeConfig{
		Exec:   AgentExecConfig{AgentKey: "implementer"},
		Inputs: InputsConfig{FromSharedfiles: []string{"missing.md"}},
	}
	node := makeAgentNode(t, cfg)

	out, err := exec.Execute(context.Background(), node, RunContext{DagKey: "dag-x"})
	if err != nil {
		t.Fatalf("Execute() = %v", err)
	}
	if out.Status != NodeStatusFailed {
		t.Fatalf("Status = %q, want failed", out.Status)
	}
	if out.FailureClass != FailureClassValidation {
		t.Fatalf("FailureClass = %q, want validation", out.FailureClass)
	}
	if launcher.called != 0 {
		t.Fatalf("launcher should not be called, got %d", launcher.called)
	}
	if !strings.Contains(out.ErrorSummary, "missing.md") {
		t.Fatalf("ErrorSummary missing offending path; got %q", out.ErrorSummary)
	}
}

// TestAgentExecutor_Inputs_FromNodes_NilReader_Validation 验证 inputs.from_nodes
// 非空但 reader 未接通 → validation（不静默吞掉注入需求）。
func TestAgentExecutor_Inputs_FromNodes_NilReader_Validation(t *testing.T) {
	launcher := &stubAgentLauncher{}
	exec := NewAgentExecutorWithInputs(launcher, nil, nil /*prev*/, nil /*sf*/)

	cfg := AgentNodeConfig{
		Exec:   AgentExecConfig{AgentKey: "implementer"},
		Inputs: InputsConfig{FromNodes: []string{"node-a"}},
	}
	node := makeAgentNode(t, cfg)

	out, err := exec.Execute(context.Background(), node, RunContext{DagKey: "dag-x"})
	if err != nil {
		t.Fatalf("Execute() = %v", err)
	}
	if out.Status != NodeStatusFailed || out.FailureClass != FailureClassValidation {
		t.Fatalf("got (%q,%q), want (failed,validation); ErrorSummary=%q", out.Status, out.FailureClass, out.ErrorSummary)
	}
}

// TestAgentExecutor_Inputs_FromSharedfiles_NilReader_Validation 验证
// inputs.from_sharedfiles 非空但 reader 未接通 → validation。
func TestAgentExecutor_Inputs_FromSharedfiles_NilReader_Validation(t *testing.T) {
	launcher := &stubAgentLauncher{}
	exec := NewAgentExecutorWithInputs(launcher, nil, nil, nil)

	cfg := AgentNodeConfig{
		Exec:   AgentExecConfig{AgentKey: "implementer"},
		Inputs: InputsConfig{FromSharedfiles: []string{"plan.md"}},
	}
	node := makeAgentNode(t, cfg)

	out, _ := exec.Execute(context.Background(), node, RunContext{DagKey: "dag-x"})
	if out.Status != NodeStatusFailed || out.FailureClass != FailureClassValidation {
		t.Fatalf("got (%q,%q), want (failed,validation); ErrorSummary=%q", out.Status, out.FailureClass, out.ErrorSummary)
	}
}

// TestAgentExecutor_Inputs_FromNodes_EmptyResult_StillSucceeds 验证 prev node
// 存在但 result 列为空时，注入「(empty)」占位且继续 launch（不阻塞 child）。
// 上游节点合法配置 outputs.to_node_result=false 时此路径自然发生。
func TestAgentExecutor_Inputs_FromNodes_EmptyResult_StillSucceeds(t *testing.T) {
	launcher := &stubAgentLauncher{}
	prev := &stubPrevNodeResultReader{results: map[string]json.RawMessage{
		"dag-x|node-a": nil, // exists but no result payload
	}}
	exec := NewAgentExecutorWithInputs(launcher, nil, prev, nil)

	cfg := AgentNodeConfig{
		Exec:   AgentExecConfig{AgentKey: "implementer"},
		Inputs: InputsConfig{FromNodes: []string{"node-a"}},
	}
	node := makeAgentNode(t, cfg)

	out, err := exec.Execute(context.Background(), node, RunContext{DagKey: "dag-x"})
	if err != nil {
		t.Fatalf("Execute() = %v", err)
	}
	if out.Status != NodeStatusDone {
		t.Fatalf("Status = %q, want done", out.Status)
	}
	if !strings.Contains(launcher.lastReq.Prompt, "(empty)") {
		t.Fatalf("Prompt missing (empty) placeholder; got: %q", launcher.lastReq.Prompt)
	}
}

// TestAgentExecutor_Inputs_FromNodes_ReaderInfraError_Transient 验证 reader
// 返回的非 not-found 错误（如 DB 失联）走 classifyAgentLaunchError → transient。
func TestAgentExecutor_Inputs_FromNodes_ReaderInfraError_Transient(t *testing.T) {
	launcher := &stubAgentLauncher{}
	prev := &stubPrevNodeResultReader{err: errors.New("connection refused: prev store")}
	exec := NewAgentExecutorWithInputs(launcher, nil, prev, nil)

	cfg := AgentNodeConfig{
		Exec:   AgentExecConfig{AgentKey: "implementer"},
		Inputs: InputsConfig{FromNodes: []string{"node-a"}},
	}
	node := makeAgentNode(t, cfg)

	out, _ := exec.Execute(context.Background(), node, RunContext{DagKey: "dag-x"})
	if out.Status != NodeStatusFailed {
		t.Fatalf("Status = %q, want failed", out.Status)
	}
	if out.FailureClass != FailureClassTransient {
		t.Fatalf("FailureClass = %q, want transient (infra error must NOT be validation)", out.FailureClass)
	}
}

// TestAgentExecutor_Inputs_FallsBackToNodeDagKey 验证 runCtx.DagKey 为空时
// 从 node.DagKey 取 dag_key（与 F1.5 resolveSpawnKeys 一致的回退）。
func TestAgentExecutor_Inputs_FallsBackToNodeDagKey(t *testing.T) {
	launcher := &stubAgentLauncher{}
	prev := &stubPrevNodeResultReader{results: map[string]json.RawMessage{
		"dag-x|node-a": json.RawMessage(`"fallback-result"`),
	}}
	exec := NewAgentExecutorWithInputs(launcher, nil, prev, nil)

	cfg := AgentNodeConfig{
		Exec:   AgentExecConfig{AgentKey: "implementer"},
		Inputs: InputsConfig{FromNodes: []string{"node-a"}},
	}
	node := makeAgentNode(t, cfg) // node.DagKey="dag-x"

	out, _ := exec.Execute(context.Background(), node, RunContext{}) // runCtx empty
	if out.Status != NodeStatusDone {
		t.Fatalf("Status = %q, want done; ErrorSummary=%q", out.Status, out.ErrorSummary)
	}
	if !strings.Contains(launcher.lastReq.Prompt, "fallback-result") {
		t.Fatalf("Prompt missing fallback content; got: %q", launcher.lastReq.Prompt)
	}
}

// TestAgentExecutor_Inputs_PreservesF15_ThreadIDWriteback 验证 F1.2 inputs 注入
// 与 F1.5 spawning_thread_id 写回共存：注入成功 + launch 成功后，recorder 仍被调用。
func TestAgentExecutor_Inputs_PreservesF15_ThreadIDWriteback(t *testing.T) {
	launcher := &stubAgentLauncher{threadID: "thread-1"}
	recorder := &stubNodeSpawnRecorder{}
	prev := &stubPrevNodeResultReader{results: map[string]json.RawMessage{
		"dag-x|node-a": json.RawMessage(`{"ok":true}`),
	}}
	exec := NewAgentExecutorWithInputs(launcher, recorder, prev, nil)

	cfg := AgentNodeConfig{
		Exec:   AgentExecConfig{AgentKey: "implementer"},
		Inputs: InputsConfig{FromNodes: []string{"node-a"}},
	}
	node := makeAgentNode(t, cfg)

	out, err := exec.Execute(context.Background(), node, RunContext{DagKey: "dag-x", NodeKey: "node-b"})
	if err != nil {
		t.Fatalf("Execute() = %v", err)
	}
	if out.Status != NodeStatusDone {
		t.Fatalf("Status = %q, want done", out.Status)
	}
	if recorder.called != 1 {
		t.Fatalf("recorder.called = %d, want 1", recorder.called)
	}
	if recorder.lastThreadID != "thread-1" {
		t.Fatalf("recorder.lastThreadID = %q, want thread-1", recorder.lastThreadID)
	}
}

// TestErrInputsValidation_IsSentinel 锁住 errors.Is 可见 ErrInputsValidation，
// 便于上层（F1.4 dispatcher / 测试）按哨兵识别 inputs-stage 验证失败。
func TestErrInputsValidation_IsSentinel(t *testing.T) {
	exec := NewAgentExecutorWithInputs(&stubAgentLauncher{}, nil, &stubPrevNodeResultReader{}, nil)
	_, err, class := exec.loadFromNodes(context.Background(), "dag-x", []string{"missing"})
	if err == nil {
		t.Fatalf("expected validation error for missing node_key")
	}
	if !errors.Is(err, ErrInputsValidation) {
		t.Fatalf("errors.Is(err, ErrInputsValidation) = false; err=%v", err)
	}
	if class != FailureClassValidation {
		t.Fatalf("class = %q, want validation", class)
	}
}
