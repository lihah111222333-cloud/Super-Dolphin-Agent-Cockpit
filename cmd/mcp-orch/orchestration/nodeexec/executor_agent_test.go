package nodeexec

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// stubAgentLauncher 是 AgentLauncher 接口的测试假实现。
// 记录最近一次 LaunchAgent 调用入参 + 注入返回错误 + 返回 threadID，
// 便于断言。F1.5 后 LaunchAgent 返回值是 (threadID, error)。
type stubAgentLauncher struct {
	called   int
	lastReq  contract.LaunchRequest
	threadID string
	err      error
}

func (s *stubAgentLauncher) LaunchAgent(_ context.Context, req contract.LaunchRequest) (string, error) {
	s.called++
	s.lastReq = req
	return s.threadID, s.err
}

// stubNodeSpawnRecorder 是 NodeSpawnRecorder 接口的测试假实现。
// 记录最近一次 RecordNodeSpawn 入参 + 返回注入错误。
type stubNodeSpawnRecorder struct {
	called       int
	lastDagKey   string
	lastNodeKey  string
	lastRunID    int64
	lastThreadID string
	err          error
}

func (r *stubNodeSpawnRecorder) RecordNodeSpawn(_ context.Context, dagKey, nodeKey string, runID int64, threadID string) error {
	r.called++
	r.lastDagKey = dagKey
	r.lastNodeKey = nodeKey
	r.lastRunID = runID
	r.lastThreadID = threadID
	return r.err
}

func launchEnvValue(env []string, key string) string {
	prefix := key + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return strings.TrimPrefix(item, prefix)
		}
	}
	return ""
}

// makeAgentNode 构造一个 agent 类型节点 + json 编码的 AgentNodeConfig。
func makeAgentNode(t *testing.T, cfg AgentNodeConfig) Node {
	t.Helper()
	if strings.TrimSpace(cfg.Exec.CWD) == "" {
		cfg.Exec.CWD = "/tmp/node-cwd"
	}
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

func TestBuildLaunchRequestFromAgentConfigFillsAgentIDAndName(t *testing.T) {
	t.Parallel()
	cfg := AgentNodeConfig{
		Exec: AgentExecConfig{
			AgentKey: "implementer",
			CWD:      "/repo/agent",
			Language: "zh",
		},
		FirstTurn: "do the thing",
	}
	node := Node{
		NodeType: "agent",
		Title:    "Validate DAG node",
	}

	req := buildLaunchRequestFromAgentConfig(&cfg, node, RunContext{})
	again := buildLaunchRequestFromAgentConfig(&cfg, node, RunContext{})

	if !strings.HasPrefix(req.AgentID, "agent_") {
		t.Fatalf("AgentID = %q, want agent_*", req.AgentID)
	}
	if req.AgentID == again.AgentID {
		t.Fatalf("AgentID repeated across calls: %q", req.AgentID)
	}
	if req.Name != node.Title {
		t.Fatalf("Name = %q, want node title %q", req.Name, node.Title)
	}
	if req.AgentKey != cfg.Exec.AgentKey {
		t.Fatalf("AgentKey = %q, want %q", req.AgentKey, cfg.Exec.AgentKey)
	}
	if req.Cwd != cfg.Exec.CWD {
		t.Fatalf("Cwd = %q, want %q", req.Cwd, cfg.Exec.CWD)
	}
	if req.Prompt != cfg.FirstTurn {
		t.Fatalf("Prompt = %q, want first turn %q", req.Prompt, cfg.FirstTurn)
	}
}

func TestBuildLaunchRequestFromAgentConfigForwardsRuntimeHints(t *testing.T) {
	t.Parallel()
	cfg := AgentNodeConfig{
		Exec: AgentExecConfig{
			Provider:      " CLAUDE ",
			Model:         " opus ",
			Effort:        " high ",
			AgentKey:      "implementer",
			CWD:           "/repo/agent",
			DisabledTools: []string{"shell", " browser "},
		},
	}

	req := buildLaunchRequestFromAgentConfig(&cfg, Node{NodeType: "agent", Title: "node"}, RunContext{})

	if got := launchEnvValue(req.Env, "AGENT_PROVIDER"); got != "claude" {
		t.Fatalf("AGENT_PROVIDER = %q, want claude", got)
	}
	if got := launchEnvValue(req.Env, "AGENT_MODEL"); got != "opus" {
		t.Fatalf("AGENT_MODEL = %q, want opus", got)
	}
	if got := launchEnvValue(req.Env, "AGENT_EFFORT"); got != "high" {
		t.Fatalf("AGENT_EFFORT = %q, want high", got)
	}
	if got := launchEnvValue(req.Env, "AGENT_DISABLED_TOOLS"); got != "shell,browser" {
		t.Fatalf("AGENT_DISABLED_TOOLS = %q, want shell,browser", got)
	}
	if req.Cwd != cfg.Exec.CWD {
		t.Fatalf("Cwd = %q, want %q", req.Cwd, cfg.Exec.CWD)
	}
}

func TestBuildLaunchRequestFromAgentConfigDoesNotInventCWD(t *testing.T) {
	t.Parallel()
	cfg := AgentNodeConfig{Exec: AgentExecConfig{AgentKey: "implementer"}}
	req := buildLaunchRequestFromAgentConfig(&cfg, Node{NodeType: "agent", Title: "node"}, RunContext{})
	if req.Cwd != "" {
		t.Fatalf("Cwd = %q, want empty until exec.cwd is explicit", req.Cwd)
	}
}

func TestAgentExecutor_Execute_HappyPath(t *testing.T) {
	t.Parallel()
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
	if !strings.HasPrefix(launcher.lastReq.AgentID, "agent_") {
		t.Fatalf("LaunchAgent.AgentID = %q, want agent_*", launcher.lastReq.AgentID)
	}
	if launcher.lastReq.Name != node.Title {
		t.Fatalf("LaunchAgent.Name = %q, want %q", launcher.lastReq.Name, node.Title)
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

func TestAgentExecutor_Execute_NilLauncher(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	// 节点 config 为空（旧 DAG）也得是 validation 失败而非 panic：
	// ParseAgentConfig 返回 zero-value，但 agent_key 缺失 → validation。
	launcher := &stubAgentLauncher{}
	exec := NewAgentExecutor(launcher)

	node := Node{NodeType: "agent", Config: nil}
	out, err := exec.Execute(context.Background(), node, RunContext{})
	if err != nil {
		t.Fatalf("Execute() framework error = %v, want classified validation outcome", err)
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
	t.Parallel()
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
	t.Parallel()
	exec := NewAgentExecutor(&stubAgentLauncher{})
	if h := exec.Hooks(); h != nil {
		t.Fatalf("Hooks() = %v, want nil (F13 留位)", h)
	}
}

func TestAgentExecutor_ImplementsNodeExecutor(t *testing.T) {
	t.Parallel()
	// 编译期检查：保证 *AgentExecutor 满足 NodeExecutor 接口。
	var _ NodeExecutor = (*AgentExecutor)(nil)
}

func TestClassifyAgentLaunchError(t *testing.T) {
	t.Parallel()
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
		{"capability", errors.New("model lacks capability for this task"), FailureClassCapability},
		{"unauthorized_validation", errors.New("401 unauthorized"), FailureClassValidation},
		{"forbidden_validation", errors.New("403 forbidden"), FailureClassValidation},
		{"root_task_missing_validation", errors.New(`root task id missing on thread "agent-parent"`), FailureClassValidation},
		{"task_handoff_title_validation", errors.New(`task handoff title is required for task "task-demo"`), FailureClassValidation},
		{"task_handoff_file_validation", errors.New(`task handoff file is required for task "task-demo"`), FailureClassValidation},
		{"task_handoff_config_validation", errors.New(`task handoff config "taskId" must be a string`), FailureClassValidation},
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

// ====================================================================
// F1.5 / ADR-009: spawning_thread_id 写回单测。
// ====================================================================

// TestAgentExecutor_Execute_Spawn_WritesBackThreadID 验证成功 launch 后
// AgentExecutor 调用 NodeSpawnRecorder.RecordNodeSpawn 传入正确的 dagKey /
// nodeKey / threadID。该用例覃盖 ADR-009 §3 「写入时机」核心约定。
func TestAgentExecutor_Execute_Spawn_WritesBackThreadID(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{threadID: "thread-success"}
	recorder := &stubNodeSpawnRecorder{}
	exec := NewAgentExecutor(launcher, WithRecorder(recorder))

	cfg := AgentNodeConfig{Exec: AgentExecConfig{AgentKey: "implementer"}}
	node := makeAgentNode(t, cfg)

	out, err := exec.Execute(context.Background(), node, RunContext{
		DagKey: "dag-x", NodeKey: "node-a", RunID: 1001,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out.Status != NodeStatusDone {
		t.Fatalf("Status = %q, want %q", out.Status, NodeStatusDone)
	}
	if out.ErrorSummary != "" {
		t.Fatalf("ErrorSummary = %q, want empty on successful writeback", out.ErrorSummary)
	}
	if recorder.called != 1 {
		t.Fatalf("RecordNodeSpawn called %d times, want 1", recorder.called)
	}
	if recorder.lastDagKey != "dag-x" || recorder.lastNodeKey != "node-a" {
		t.Fatalf("RecordNodeSpawn keys = (%q,%q), want (dag-x,node-a)",
			recorder.lastDagKey, recorder.lastNodeKey)
	}
	if recorder.lastRunID != 1001 {
		t.Fatalf("RecordNodeSpawn runID = %d, want 1001", recorder.lastRunID)
	}
	if recorder.lastThreadID != "thread-success" {
		t.Fatalf("RecordNodeSpawn threadID = %q, want thread-success", recorder.lastThreadID)
	}
}

// TestAgentExecutor_Execute_Spawn_FallsBackToNodeKeys 验证 RunContext.DagKey /
// NodeKey 为空时，AgentExecutor 会 fallback 到 node.DagKey / node.NodeKey。
// dispatcher 未填 RunContext 时不会失去写回能力。
func TestAgentExecutor_Execute_Spawn_FallsBackToNodeKeys(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{threadID: "thread-fallback"}
	recorder := &stubNodeSpawnRecorder{}
	exec := NewAgentExecutor(launcher, WithRecorder(recorder))

	cfg := AgentNodeConfig{Exec: AgentExecConfig{AgentKey: "implementer"}}
	node := makeAgentNode(t, cfg) // node has DagKey=dag-x NodeKey=node-a

	_, err := exec.Execute(context.Background(), node, RunContext{RunID: 2002})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if recorder.called != 1 {
		t.Fatalf("RecordNodeSpawn called %d times, want 1", recorder.called)
	}
	if recorder.lastDagKey != "dag-x" || recorder.lastNodeKey != "node-a" {
		t.Fatalf("RecordNodeSpawn keys = (%q,%q), want fallback (dag-x,node-a)",
			recorder.lastDagKey, recorder.lastNodeKey)
	}
	if recorder.lastRunID != 2002 {
		t.Fatalf("RecordNodeSpawn runID = %d, want 2002", recorder.lastRunID)
	}
}

// TestAgentExecutorExecuteSpawnNilRecorderSkipsWriteback 验证 recorder=nil
// 时 AgentExecutor 仍能正常 launch + 返回 done。保证 F1.5 之前的 wiring 不被破坏。
func TestAgentExecutorExecuteSpawnNilRecorderSkipsWriteback(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{threadID: "thread-nil-recorder"}
	exec := NewAgentExecutor(launcher)
	cfg := AgentNodeConfig{Exec: AgentExecConfig{AgentKey: "implementer"}}
	node := makeAgentNode(t, cfg)

	out, err := exec.Execute(context.Background(), node, RunContext{
		DagKey: "dag-x", NodeKey: "node-a",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out.Status != NodeStatusDone || out.ErrorSummary != "" {
		t.Fatalf("Execute() = %+v, want Status=done & empty summary", out)
	}
}

// TestAgentExecutorExecuteSpawnEmptyThreadIDIsHardFailure 验证 launcher
// 返回 threadID="" 时不能当作成功：没有可持久化的 spawning_thread_id，下游
// ready→running 写入失败后的 retry 会重复 launch child。
func TestAgentExecutorExecuteSpawnEmptyThreadIDIsHardFailure(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{threadID: ""} // launch 成功但拿不到 thread_id
	recorder := &stubNodeSpawnRecorder{}
	exec := NewAgentExecutor(launcher, WithRecorder(recorder))

	cfg := AgentNodeConfig{Exec: AgentExecConfig{AgentKey: "implementer"}}
	node := makeAgentNode(t, cfg)

	out, err := exec.Execute(context.Background(), node, RunContext{
		DagKey: "dag-x", NodeKey: "node-a",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out.Status != NodeStatusFailed {
		t.Fatalf("Status = %q, want %q", out.Status, NodeStatusFailed)
	}
	if out.FailureClass != FailureClassHard {
		t.Fatalf("FailureClass = %q, want %q", out.FailureClass, FailureClassHard)
	}
	if !strings.Contains(out.ErrorSummary, "spawning_thread_id write-back skipped") {
		t.Fatalf("ErrorSummary = %q, want skipped write-back context", out.ErrorSummary)
	}
	if recorder.called != 0 {
		t.Fatalf("RecordNodeSpawn called %d times, want 0 when threadID empty", recorder.called)
	}
}

func TestAgentExecutorExecuteSpawnMissingKeysIsHardFailure(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{threadID: "thread-launched"}
	recorder := &stubNodeSpawnRecorder{}
	exec := NewAgentExecutor(launcher, WithRecorder(recorder))

	cfg := AgentNodeConfig{Exec: AgentExecConfig{AgentKey: "implementer"}}
	node := makeAgentNode(t, cfg)
	node.DagKey = ""
	node.NodeKey = ""

	out, err := exec.Execute(context.Background(), node, RunContext{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out.Status != NodeStatusFailed {
		t.Fatalf("Status = %q, want %q", out.Status, NodeStatusFailed)
	}
	if out.FailureClass != FailureClassHard {
		t.Fatalf("FailureClass = %q, want %q", out.FailureClass, FailureClassHard)
	}
	if !strings.Contains(out.ErrorSummary, "spawning_thread_id write-back skipped") {
		t.Fatalf("ErrorSummary = %q, want skipped write-back context", out.ErrorSummary)
	}
	if recorder.called != 0 {
		t.Fatalf("RecordNodeSpawn called %d times, want 0 without keys", recorder.called)
	}
}

// TestAgentExecutor_Execute_Spawn_RecorderErrorIsHardFailure 验证 recorder
// 写回失败不会被当成软成功。child 已经启动但 spawning_thread_id 不可持久化时，
// 下游 subscriber 无法可靠反查节点；必须 fail-fast，避免 dispatcher 重试时再
// 启动第二个 child。
func TestAgentExecutor_Execute_Spawn_RecorderErrorIsHardFailure(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{threadID: "thread-err"}
	recorder := &stubNodeSpawnRecorder{err: errors.New("db connection refused")}
	exec := NewAgentExecutor(launcher, WithRecorder(recorder))

	cfg := AgentNodeConfig{Exec: AgentExecConfig{AgentKey: "implementer"}}
	node := makeAgentNode(t, cfg)

	out, err := exec.Execute(context.Background(), node, RunContext{
		DagKey: "dag-x", NodeKey: "node-a",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out.Status != NodeStatusFailed {
		t.Fatalf("Status = %q, want %q", out.Status, NodeStatusFailed)
	}
	if out.FailureClass != FailureClassHard {
		t.Fatalf("FailureClass = %q, want %q", out.FailureClass, FailureClassHard)
	}
	if out.ErrorSummary == "" {
		t.Fatalf("ErrorSummary empty, want substring 'spawning_thread_id write-back failed'")
	}
}

// TestAgentExecutor_Execute_Spawn_LaunchErrorSkipsWriteback 验证 launch 本身
// 失败时 recorder 不被调用。避免走到一个 launch 失败但路径上又去写 thread id
// 的错乱局面。
func TestAgentExecutor_Execute_Spawn_LaunchErrorSkipsWriteback(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{
		threadID: "thread-should-not-be-used",
		err:      errors.New("connection refused"),
	}
	recorder := &stubNodeSpawnRecorder{}
	exec := NewAgentExecutor(launcher, WithRecorder(recorder))

	cfg := AgentNodeConfig{Exec: AgentExecConfig{AgentKey: "implementer"}}
	node := makeAgentNode(t, cfg)

	out, err := exec.Execute(context.Background(), node, RunContext{
		DagKey: "dag-x", NodeKey: "node-a",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out.Status != NodeStatusFailed {
		t.Fatalf("Status = %q, want %q", out.Status, NodeStatusFailed)
	}
	if recorder.called != 0 {
		t.Fatalf("RecordNodeSpawn called %d times on launch error, want 0", recorder.called)
	}
}
