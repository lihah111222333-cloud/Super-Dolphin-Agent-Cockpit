package nodeexec

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// stubAgentLauncher 是 AgentLauncher 接口的测试假实现。
// 记录最近一次 LaunchAgent 调用入参 + 注入返回错误，便于断言。
type stubAgentLauncher struct {
	called  int
	lastReq contract.LaunchRequest
	err     error
}

func (s *stubAgentLauncher) LaunchAgent(_ context.Context, req contract.LaunchRequest) error {
	s.called++
	s.lastReq = req
	return s.err
}

// makeAgentNode 构造一个 agent 类型节点 + json 编码的 AgentNodeConfig。
func makeAgentNode(t *testing.T, cfg AgentNodeConfig) Node {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal AgentNodeConfig: %v", err)
	}
	return Node{
		DagKey:   "dag-x",
		NodeKey:  "node-a",
		NodeType: "agent",
		Title:    "test node",
		Config:   raw,
	}
}

func TestAgentExecutor_Execute_HappyPath(t *testing.T) {
	launcher := &stubAgentLauncher{}
	exec := NewAgentExecutor(launcher)

	cfg := AgentNodeConfig{
		Exec: AgentExecConfig{
			Provider: "claude",
			Model:    "sonnet",
			AgentKey: "implementer",
			Language: "zh",
		},
		FirstTurn: "do the thing",
	}
	node := makeAgentNode(t, cfg)

	out, err := exec.Execute(context.Background(), node, RunContext{
		DagKey: "dag-x", NodeKey: "node-a", RunID: 42,
	})
	if err != nil {
		t.Fatalf("Execute() framework error = %v, want nil", err)
	}
	if out.Status != NodeStatusDone {
		t.Fatalf("Status = %q, want %q", out.Status, NodeStatusDone)
	}
	if out.FailureClass != "" {
		t.Fatalf("FailureClass = %q, want empty on success", out.FailureClass)
	}
	if launcher.called != 1 {
		t.Fatalf("LaunchAgent called %d times, want 1", launcher.called)
	}
	if launcher.lastReq.AgentKey != "implementer" {
		t.Fatalf("LaunchAgent.AgentKey = %q, want %q", launcher.lastReq.AgentKey, "implementer")
	}
	if launcher.lastReq.Language != "zh" {
		t.Fatalf("LaunchAgent.Language = %q, want %q", launcher.lastReq.Language, "zh")
	}
	if launcher.lastReq.Prompt != "do the thing" {
		t.Fatalf("LaunchAgent.Prompt = %q, want first_turn", launcher.lastReq.Prompt)
	}
}

func TestAgentExecutor_Execute_InvalidConfig_BadJSON(t *testing.T) {
	launcher := &stubAgentLauncher{}
	exec := NewAgentExecutor(launcher)

	node := Node{
		NodeType: "agent",
		Config:   json.RawMessage(`{"exec": "not-an-object"`), // 截断 + 类型错
	}
	out, err := exec.Execute(context.Background(), node, RunContext{})
	if err != nil {
		t.Fatalf("Execute() framework error = %v, want nil (failure should be NodeOutcome)", err)
	}
	if out.Status != NodeStatusFailed {
		t.Fatalf("Status = %q, want %q", out.Status, NodeStatusFailed)
	}
	if out.FailureClass != FailureClassValidation {
		t.Fatalf("FailureClass = %q, want %q", out.FailureClass, FailureClassValidation)
	}
	if launcher.called != 0 {
		t.Fatalf("LaunchAgent should not be called on invalid config, got %d", launcher.called)
	}
}

func TestAgentExecutor_Execute_InvalidConfig_MissingAgentKey(t *testing.T) {
	launcher := &stubAgentLauncher{}
	exec := NewAgentExecutor(launcher)

	cfg := AgentNodeConfig{
		Exec: AgentExecConfig{Provider: "claude"}, // 缺 agent_key
	}
	node := makeAgentNode(t, cfg)

	out, err := exec.Execute(context.Background(), node, RunContext{})
	if err != nil {
		t.Fatalf("Execute() framework error = %v, want nil", err)
	}
	if out.Status != NodeStatusFailed {
		t.Fatalf("Status = %q, want %q", out.Status, NodeStatusFailed)
	}
	if out.FailureClass != FailureClassValidation {
		t.Fatalf("FailureClass = %q, want %q", out.FailureClass, FailureClassValidation)
	}
	if launcher.called != 0 {
		t.Fatalf("LaunchAgent should not be called, got %d", launcher.called)
	}
}

func TestAgentExecutor_Execute_LaunchTransientErr(t *testing.T) {
	launcher := &stubAgentLauncher{err: errors.New("connection refused: provider not up")}
	exec := NewAgentExecutor(launcher)

	cfg := AgentNodeConfig{Exec: AgentExecConfig{AgentKey: "implementer"}}
	node := makeAgentNode(t, cfg)

	out, err := exec.Execute(context.Background(), node, RunContext{})
	if err != nil {
		t.Fatalf("Execute() framework error = %v, want nil", err)
	}
	if out.Status != NodeStatusFailed {
		t.Fatalf("Status = %q, want %q", out.Status, NodeStatusFailed)
	}
	if out.FailureClass != FailureClassTransient {
		t.Fatalf("FailureClass = %q, want %q", out.FailureClass, FailureClassTransient)
	}
	if out.ErrorSummary == "" {
		t.Fatalf("ErrorSummary should be populated on failure")
	}
}

func TestAgentExecutor_Execute_LaunchQuotaErr(t *testing.T) {
	launcher := &stubAgentLauncher{err: errors.New("quota_exhausted: out of credits")}
	exec := NewAgentExecutor(launcher)

	cfg := AgentNodeConfig{Exec: AgentExecConfig{AgentKey: "implementer"}}
	node := makeAgentNode(t, cfg)

	out, err := exec.Execute(context.Background(), node, RunContext{})
	if err != nil {
		t.Fatalf("Execute() framework error = %v, want nil", err)
	}
	if out.Status != NodeStatusFailed {
		t.Fatalf("Status = %q, want %q", out.Status, NodeStatusFailed)
	}
	if out.FailureClass != FailureClassQuota {
		t.Fatalf("FailureClass = %q, want %q", out.FailureClass, FailureClassQuota)
	}
}

func TestAgentExecutor_Execute_LaunchPermanentErr(t *testing.T) {
	launcher := &stubAgentLauncher{err: errors.New("401 unauthorized: invalid api key")}
	exec := NewAgentExecutor(launcher)

	cfg := AgentNodeConfig{Exec: AgentExecConfig{AgentKey: "implementer"}}
	node := makeAgentNode(t, cfg)

	out, err := exec.Execute(context.Background(), node, RunContext{})
	if err != nil {
		t.Fatalf("Execute() framework error = %v, want nil", err)
	}
	if out.Status != NodeStatusFailed {
		t.Fatalf("Status = %q, want %q", out.Status, NodeStatusFailed)
	}
	if out.FailureClass != FailureClassValidation {
		t.Fatalf("FailureClass = %q, want %q (permanent err maps to validation per F1.4 spec)",
			out.FailureClass, FailureClassValidation)
	}
}

func TestAgentExecutor_Execute_NilLauncher(t *testing.T) {
	// nil launcher 应在构造期失败而非 Execute 期 panic。
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("NewAgentExecutor(nil) should not panic, got %v", r)
		}
	}()
	exec := NewAgentExecutor(nil)
	if exec == nil {
		// 允许返回 nil 给 nil launcher
		return
	}
	cfg := AgentNodeConfig{Exec: AgentExecConfig{AgentKey: "implementer"}}
	node := makeAgentNode(t, cfg)
	out, err := exec.Execute(context.Background(), node, RunContext{})
	if err != nil {
		t.Fatalf("Execute() framework error = %v, want nil", err)
	}
	if out.Status != NodeStatusFailed {
		t.Fatalf("Status = %q, want %q on nil launcher", out.Status, NodeStatusFailed)
	}
}

func TestAgentExecutor_Execute_NilNodeConfig(t *testing.T) {
	// 节点 config 为空（旧 DAG）也得是 validation 失败而非 panic：
	// ParseAgentConfig 返回 zero-value，但 agent_key 缺失 → validation。
	launcher := &stubAgentLauncher{}
	exec := NewAgentExecutor(launcher)

	node := Node{NodeType: "agent", Config: nil}
	out, err := exec.Execute(context.Background(), node, RunContext{})
	if err != nil {
		t.Fatalf("Execute() framework error = %v, want nil", err)
	}
	if out.Status != NodeStatusFailed {
		t.Fatalf("Status = %q, want %q", out.Status, NodeStatusFailed)
	}
	if out.FailureClass != FailureClassValidation {
		t.Fatalf("FailureClass = %q, want %q", out.FailureClass, FailureClassValidation)
	}
	if launcher.called != 0 {
		t.Fatalf("LaunchAgent should not be called when config invalid")
	}
}

func TestAgentExecutor_Execute_NilContextDefaultsToBackground(t *testing.T) {
	// nil ctx 不应 panic：Execute 应内部兜底 context.Background()。
	launcher := &stubAgentLauncher{}
	exec := NewAgentExecutor(launcher)
	cfg := AgentNodeConfig{Exec: AgentExecConfig{AgentKey: "implementer"}}
	node := makeAgentNode(t, cfg)
	//nolint:staticcheck // 故意传 nil ctx 测兜底
	out, err := exec.Execute(nil, node, RunContext{})
	if err != nil {
		t.Fatalf("Execute(nil ctx) framework error = %v, want nil", err)
	}
	if out.Status != NodeStatusDone {
		t.Fatalf("Status = %q, want %q", out.Status, NodeStatusDone)
	}
}

func TestAgentExecutor_Hooks_Nil(t *testing.T) {
	exec := NewAgentExecutor(&stubAgentLauncher{})
	if h := exec.Hooks(); h != nil {
		t.Fatalf("Hooks() = %v, want nil (F13 留位)", h)
	}
}

func TestAgentExecutor_ImplementsNodeExecutor(t *testing.T) {
	// 编译期检查：保证 *AgentExecutor 满足 NodeExecutor 接口。
	var _ NodeExecutor = (*AgentExecutor)(nil)
}

func TestClassifyAgentLaunchError(t *testing.T) {
	// classifyAgentLaunchError 把 launcher 返回的 error 映射到 FailureClass。
	// 与 service_launcher_errors.go::classifyLaunchError 模式对齐，
	// 但目标空间是 nodeexec.FailureClass。
	cases := []struct {
		name string
		err  error
		want FailureClass
	}{
		{"nil_treated_as_transient", nil, FailureClassTransient},
		{"connection_refused_transient", errors.New("connection refused"), FailureClassTransient},
		{"timeout_transient", errors.New("i/o timeout"), FailureClassTransient},
		{"rate_limit_transient", errors.New("HTTP 429 too many requests"), FailureClassTransient},
		{"context_length_quota", errors.New("context_length_exceeded"), FailureClassQuota},
		{"prompt_too_long_quota", errors.New("prompt is too long"), FailureClassQuota},
		{"usage_limit_quota", errors.New("usage limit"), FailureClassQuota},
		{"out_of_credits_quota", errors.New("out of credits"), FailureClassQuota},
		{"unauthorized_validation", errors.New("401 unauthorized"), FailureClassValidation},
		{"forbidden_validation", errors.New("403 forbidden"), FailureClassValidation},
		{"unknown_default_transient", errors.New("strange new failure"), FailureClassTransient},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyAgentLaunchError(tc.err); got != tc.want {
				t.Fatalf("classifyAgentLaunchError(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}
