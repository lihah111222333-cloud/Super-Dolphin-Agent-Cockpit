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
//
// 端口收敛 batch：inputs 数据源由构造器端口改为 RunContext 字段。
// AgentExecutor 现统一从 RunContext.PrevResults / RunContext.SharedFileReader 拿。
// ====================================================================

// stubSharedFileReader 是 SharedFileReader 的测试假实现（端口收敛后统一三态）。
// contents 按 path 索引；未命中 → exists=false；注入 err → 走基础设施错分类路径。
type stubSharedFileReader struct {
	contents map[string]string
	err      error
	calls    int
}

func (s *stubSharedFileReader) ReadSharedFile(_ context.Context, path string) (string, bool, error) {
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
	t.Parallel()
	launcher := &stubAgentLauncher{}
	exec := NewAgentExecutor(launcher)

	cfg := AgentNodeConfig{
		Exec:   AgentExecConfig{AgentKey: "implementer"},
		Inputs: InputsConfig{FromNodes: []string{"node-a"}},
	}
	node := makeAgentNode(t, cfg)
	node.NodeKey = "node-b" // 当前节点是 B

	prev := map[string]json.RawMessage{
		"node-a": json.RawMessage(`{"summary":"hello from A"}`),
	}
	out, err := exec.Execute(context.Background(), node, RunContext{
		DagKey: "dag-x", NodeKey: "node-b",
		PrevResults: prev,
	})
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
	t.Parallel()
	launcher := &stubAgentLauncher{}
	exec := NewAgentExecutor(launcher)

	cfg := AgentNodeConfig{
		Exec:   AgentExecConfig{AgentKey: "implementer"},
		Inputs: InputsConfig{FromNodes: []string{"node-a", "node-c"}},
	}
	node := makeAgentNode(t, cfg)

	prev := map[string]json.RawMessage{
		"node-a": json.RawMessage(`{"a":1}`),
		"node-c": json.RawMessage(`{"c":3}`),
	}
	if _, err := exec.Execute(context.Background(), node, RunContext{DagKey: "dag-x", PrevResults: prev}); err != nil {
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
	t.Parallel()
	launcher := &stubAgentLauncher{}
	sf := &stubSharedFileReader{contents: map[string]string{
		"plan.md": "# Plan\n- step1\n- step2",
	}}
	exec := NewAgentExecutor(launcher)

	cfg := AgentNodeConfig{
		Exec:   AgentExecConfig{AgentKey: "implementer"},
		Inputs: InputsConfig{FromSharedfiles: []string{"plan.md"}},
	}
	node := makeAgentNode(t, cfg)

	if _, err := exec.Execute(context.Background(), node, RunContext{DagKey: "dag-x", SharedFileReader: sf}); err != nil {
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

// TestAgentExecutorInputsMixedSources 验证两类来源混合 + first_turn 拼接。
func TestAgentExecutorInputsMixedSources(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{}
	sf := &stubSharedFileReader{contents: map[string]string{
		"plan.md": "PLAN-CONTENT",
	}}
	exec := NewAgentExecutor(launcher)

	cfg := AgentNodeConfig{
		Exec: AgentExecConfig{AgentKey: "implementer"},
		Inputs: InputsConfig{
			FromNodes:       []string{"node-a"},
			FromSharedfiles: []string{"plan.md"},
		},
		FirstTurn: "do task X",
	}
	node := makeAgentNode(t, cfg)

	prev := map[string]json.RawMessage{
		"node-a": json.RawMessage(`"result-A"`),
	}
	if _, err := exec.Execute(context.Background(), node, RunContext{DagKey: "dag-x", PrevResults: prev, SharedFileReader: sf}); err != nil {
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

func TestAgentExecutorInjectsArtifactOutputContract(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{}
	exec := NewAgentExecutor(launcher)

	cfg := AgentNodeConfig{
		Exec: AgentExecConfig{AgentKey: "video"},
		Outputs: OutputsConfig{ToArtifact: &ArtifactTarget{
			SourceTool:      "video_with_audio",
			SourcePathField: "output_path",
			PathTemplate:    "dag/douyin/daily-video/{{run_id}}/final.mp4",
		}},
		FirstTurn: "调用 video_with_audio 生成 MP4。",
	}
	node := makeAgentNode(t, cfg)

	out, err := exec.Execute(context.Background(), node, RunContext{DagKey: "dag-video", RunID: 42})
	if err != nil {
		t.Fatalf("Execute() = %v", err)
	}
	if out.Status != NodeStatusDone {
		t.Fatalf("Status = %q, want done (out=%+v)", out.Status, out)
	}
	prompt := launcher.lastReq.Prompt
	for _, want := range []string{
		"[outputs.to_artifact]",
		`"source_tool":"video_with_audio"`,
		`"output_path":"<path>"`,
		"Do not return only natural language",
		"调用 video_with_audio 生成 MP4。",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("Prompt missing artifact contract %q; got: %q", want, prompt)
		}
	}
	if strings.Index(prompt, "[outputs.to_artifact]") > strings.Index(prompt, "[first_turn]") {
		t.Fatalf("artifact contract must appear before first_turn; got: %q", prompt)
	}
}

// TestAgentExecutor_Inputs_EmptyInputs_BackwardsCompat 验证 cfg.Inputs 为空时
// LaunchRequest.Prompt 与 F1.1 保持一致（仅 first_turn）。
func TestAgentExecutor_Inputs_EmptyInputs_BackwardsCompat(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{}
	// 即使 prev/sharedfile readers 注入了，但 inputs 为空也不应被消费。
	sf := &stubSharedFileReader{}
	exec := NewAgentExecutor(launcher)

	cfg := AgentNodeConfig{
		Exec:      AgentExecConfig{AgentKey: "implementer"},
		FirstTurn: "do task Z",
	}
	node := makeAgentNode(t, cfg)

	prev := map[string]json.RawMessage{"node-a": json.RawMessage(`{}`)}
	out, err := exec.Execute(context.Background(), node, RunContext{DagKey: "dag-x", PrevResults: prev, SharedFileReader: sf})
	if err != nil {
		t.Fatalf("Execute() = %v", err)
	}
	if out.Status != NodeStatusDone {
		t.Fatalf("Status = %q, want done", out.Status)
	}
	if launcher.lastReq.Prompt != "do task Z" {
		t.Fatalf("Prompt = %q, want %q (cfg.Inputs empty → identical to F1.1)", launcher.lastReq.Prompt, "do task Z")
	}
	if sf.calls != 0 {
		t.Fatalf("sharedfile reader should not be invoked when inputs empty (sf=%d)", sf.calls)
	}
}

// TestAgentExecutorInputsFromNodesUnknownKey 验证 from_nodes 引用
// 不存在的 node_key → validation 失败、不调 launcher。
func TestAgentExecutorInputsFromNodesUnknownKey(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{}
	exec := NewAgentExecutor(launcher)

	cfg := AgentNodeConfig{
		Exec:   AgentExecConfig{AgentKey: "implementer"},
		Inputs: InputsConfig{FromNodes: []string{"node-does-not-exist"}},
	}
	node := makeAgentNode(t, cfg)

	prev := map[string]json.RawMessage{"node-a": json.RawMessage(`{}`)}
	out, err := exec.Execute(context.Background(), node, RunContext{DagKey: "dag-x", PrevResults: prev})
	if err != nil {
		t.Fatalf("Execute() framework error = %v, want classified inputs validation outcome", err)
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

// TestAgentExecutorInputsFromSharedfilesMissing 验证 sharedfile
// 不存在 → validation 失败。
func TestAgentExecutorInputsFromSharedfilesMissing(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{}
	sf := &stubSharedFileReader{contents: map[string]string{}} // 空
	exec := NewAgentExecutor(launcher)

	cfg := AgentNodeConfig{
		Exec:   AgentExecConfig{AgentKey: "implementer"},
		Inputs: InputsConfig{FromSharedfiles: []string{"missing.md"}},
	}
	node := makeAgentNode(t, cfg)

	out, err := exec.Execute(context.Background(), node, RunContext{DagKey: "dag-x", SharedFileReader: sf})
	if err != nil {
		t.Fatalf("Execute() framework error = %v, want classified sharedfile validation outcome", err)
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

// TestAgentExecutorInputsFromNodesNilReader 验证 inputs.from_nodes
// 非空但 RunContext.PrevResults 未填 → validation（不静默吞掉注入需求）。
func TestAgentExecutorInputsFromNodesNilReader(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{}
	exec := NewAgentExecutor(launcher)

	cfg := AgentNodeConfig{
		Exec:   AgentExecConfig{AgentKey: "implementer"},
		Inputs: InputsConfig{FromNodes: []string{"node-a"}},
	}
	node := makeAgentNode(t, cfg)

	out, err := exec.Execute(context.Background(), node, RunContext{DagKey: "dag-x"})
	if err != nil {
		t.Fatalf("Execute() framework error = %v, want classified inputs validation outcome", err)
	}
	if out.Status != NodeStatusFailed || out.FailureClass != FailureClassValidation {
		t.Fatalf("got (%q,%q), want (failed,validation); ErrorSummary=%q", out.Status, out.FailureClass, out.ErrorSummary)
	}
}

// TestAgentExecutorInputsFromSharedfilesNilReader 验证
// inputs.from_sharedfiles 非空但 reader 未接通 → validation。
func TestAgentExecutorInputsFromSharedfilesNilReader(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{}
	exec := NewAgentExecutor(launcher)

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

// TestAgentExecutorInputsFromNodesEmptyResult 验证 prev node
// 存在但 result 列为空时，注入「(empty)」占位且继续 launch（不阻塞 child）。
// 上游节点合法配置 outputs.to_node_result=false 时此路径自然发生。
func TestAgentExecutorInputsFromNodesEmptyResult(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{}
	exec := NewAgentExecutor(launcher)

	cfg := AgentNodeConfig{
		Exec:   AgentExecConfig{AgentKey: "implementer"},
		Inputs: InputsConfig{FromNodes: []string{"node-a"}},
	}
	node := makeAgentNode(t, cfg)

	// exists 但 payload 为 nil
	prev := map[string]json.RawMessage{"node-a": nil}
	out, err := exec.Execute(context.Background(), node, RunContext{DagKey: "dag-x", PrevResults: prev})
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

// TestAgentExecutor_Inputs_FallsBackToNodeDagKey 验证 runCtx.DagKey 为空时
// 从 node.DagKey 取 dag_key（与 F1.5 resolveSpawnKeys 一致的回退）。
func TestAgentExecutor_Inputs_FallsBackToNodeDagKey(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{}
	exec := NewAgentExecutor(launcher)

	cfg := AgentNodeConfig{
		Exec:   AgentExecConfig{AgentKey: "implementer"},
		Inputs: InputsConfig{FromNodes: []string{"node-a"}},
	}
	node := makeAgentNode(t, cfg) // node.DagKey="dag-x"

	prev := map[string]json.RawMessage{"node-a": json.RawMessage(`"fallback-result"`)}
	out, _ := exec.Execute(context.Background(), node, RunContext{PrevResults: prev}) // runCtx empty dag_key
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
	t.Parallel()
	launcher := &stubAgentLauncher{threadID: "thread-1"}
	recorder := &stubNodeSpawnRecorder{}
	exec := NewAgentExecutor(launcher, WithRecorder(recorder))

	cfg := AgentNodeConfig{
		Exec:   AgentExecConfig{AgentKey: "implementer"},
		Inputs: InputsConfig{FromNodes: []string{"node-a"}},
	}
	node := makeAgentNode(t, cfg)

	prev := map[string]json.RawMessage{"node-a": json.RawMessage(`{"ok":true}`)}
	out, err := exec.Execute(context.Background(), node, RunContext{DagKey: "dag-x", NodeKey: "node-b", RunID: 3003, PrevResults: prev})
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
	if recorder.lastRunID != 3003 {
		t.Fatalf("recorder.lastRunID = %d, want 3003", recorder.lastRunID)
	}
}

// TestErrInputsValidation_IsSentinel 锁住 errors.Is 可见 ErrInputsValidation，
// 便于上层（F1.4 dispatcher / 测试）按哨兵识别 inputs-stage 验证失败。
//
// 收敛 batch 第 4 项后：loadFromNodes 返 *InputsError；errors.Is 通过 Unwrap 链
// 仍可命中 ErrInputsValidation；errors.As 一次拿 FailureClass。
func TestErrInputsValidation_IsSentinel(t *testing.T) {
	t.Parallel()
	_, ierr := loadFromNodes("dag-x", []string{"missing"}, map[string]json.RawMessage{})
	if ierr == nil {
		t.Fatalf("expected validation error for missing node_key")
	}
	if !errors.Is(ierr, ErrInputsValidation) {
		t.Fatalf("errors.Is(ierr, ErrInputsValidation) = false; err=%v", ierr)
	}
	if ierr.Class != FailureClassValidation {
		t.Fatalf("Class = %q, want validation", ierr.Class)
	}

	// errors.As also resolves to *InputsError type for callers that want the
	// full struct (Class + wrapped Err) without the variable-binding shortcut.
	var via *InputsError
	if !errors.As(error(ierr), &via) {
		t.Fatalf("errors.As(ierr, &*InputsError) = false")
	}
	if via.Class != FailureClassValidation {
		t.Fatalf("via.Class = %q, want validation", via.Class)
	}
}
