package nodeexec

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// stubAgentLauncher 是 AgentLauncher 接口的测试假实现。
// 记录最近一次 LaunchAgent 调用入参 + 注入返回错误 + 返回 threadID，
// 便于断言当前 LaunchAgent 的 (threadID, error) 返回边界。
type stubAgentLauncher struct {
	called         int
	lastReq        contract.LaunchRequest
	threadID       string
	err            error
	stoppedThreads []string
	stopErr        error
}

func (s *stubAgentLauncher) LaunchAgent(_ context.Context, req contract.LaunchRequest) (string, error) {
	s.called++
	s.lastReq = req
	return s.threadID, s.err
}

func (s *stubAgentLauncher) StopLaunchedThread(_ context.Context, threadID string) error {
	s.stoppedThreads = append(s.stoppedThreads, strings.TrimSpace(threadID))
	return s.stopErr
}

type stubPrePromptAgentLauncher struct {
	events         []string
	threadID       string
	err            error
	legacyCalled   bool
	stoppedThreads []string
}

func (s *stubPrePromptAgentLauncher) LaunchAgent(context.Context, contract.LaunchRequest) (string, error) {
	s.legacyCalled = true
	return "", errors.New("legacy LaunchAgent should not be called")
}

func (s *stubPrePromptAgentLauncher) LaunchAgentWithSpawnRecord(
	_ context.Context,
	_ contract.LaunchRequest,
	record func(threadID string) error,
) (string, error) {
	s.events = append(s.events, "launch")
	if s.err != nil {
		return "", s.err
	}
	if record != nil {
		if err := record(s.threadID); err != nil {
			return "", err
		}
		s.events = append(s.events, "record")
	}
	s.events = append(s.events, "submit")
	return s.threadID, nil
}

func (s *stubPrePromptAgentLauncher) StopLaunchedThread(_ context.Context, threadID string) error {
	s.stoppedThreads = append(s.stoppedThreads, strings.TrimSpace(threadID))
	return nil
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
	if strings.TrimSpace(cfg.Exec.Provider) == "" {
		cfg.Exec.Provider = "codex"
	}
	if strings.TrimSpace(cfg.Exec.CWD) == "" {
		cfg.Exec.CWD = testCWD(t, "node-cwd")
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

func TestAgentExecutor_Execute_HappyPath(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{}
	exec := NewAgentExecutor(launcher)

	cfg := AgentNodeConfig{
		Exec: AgentExecConfig{
			Provider: "codex",
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

func TestAgentExecutor_Execute_PromptKeyOnlyLaunches(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{}
	exec := NewAgentExecutor(launcher)

	cfg := AgentNodeConfig{
		Exec: AgentExecConfig{
			Provider:  "codex",
			PromptKey: "user/custom-sql",
			Language:  "zh",
		},
		FirstTurn: "do the sql task",
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
	if launcher.called != 1 {
		t.Fatalf("LaunchAgent called %d times, want 1", launcher.called)
	}
	if launcher.lastReq.PromptKey != "user/custom-sql" {
		t.Fatalf("LaunchAgent.PromptKey = %q, want user/custom-sql", launcher.lastReq.PromptKey)
	}
	if launcher.lastReq.AgentKey != "" {
		t.Fatalf("LaunchAgent.AgentKey = %q, want empty when config pins prompt_key only", launcher.lastReq.AgentKey)
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
	var nilCtx context.Context
	out, err := exec.Execute(nilCtx, node, RunContext{})
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
	// 与 launcherrors/errors.go::Classify 模式对齐，
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
		{"claude_model_unavailable_validation", errors.New("There's an issue with the selected model (gpt-5.5). It may not exist or you may not have access to it. Run --model to pick a different model."), FailureClassValidation},
		{"config_load_failure_validation", errors.New("failed to load configuration: Model provider 'codex' not found"), FailureClassValidation},
		{"model_provider_not_found_validation", errors.New("Model provider 'codex' not found"), FailureClassValidation},
		{"codex_home_required_validation", errors.New("[-32098] codexHome is required"), FailureClassValidation},
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

// 下方用例覆盖 launch 成功后的 spawning_thread_id 写回路径。

// TestAgentExecutor_Execute_Spawn_WritesBackThreadID 验证成功 launch 后
// AgentExecutor 调用 NodeSpawnRecorder.RecordNodeSpawn 传入正确的 dagKey /
// nodeKey / threadID，覆盖“线程创建成功后再写回”的持久化时机。
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

func TestAgentExecutor_Execute_Spawn_RecordsBeforeInitialPromptWhenLauncherSupportsIt(t *testing.T) {
	t.Parallel()
	launcher := &stubPrePromptAgentLauncher{threadID: "thread-pre-prompt"}
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
	if launcher.legacyCalled {
		t.Fatal("legacy LaunchAgent was called; want pre-prompt recording path")
	}
	if strings.Join(launcher.events, ",") != "launch,record,submit" {
		t.Fatalf("events = %v, want launch,record,submit", launcher.events)
	}
	if recorder.called != 1 || recorder.lastThreadID != "thread-pre-prompt" {
		t.Fatalf("RecordNodeSpawn = (%d,%q), want once with thread-pre-prompt", recorder.called, recorder.lastThreadID)
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
// 时 AgentExecutor 仍能正常 launch + 返回 done，保持无写回依赖的兼容启动路径。
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

func TestAgentExecutor_Execute_Spawn_RecorderErrorStopsLaunchedThread(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{threadID: "thread-late"}
	recorder := &stubNodeSpawnRecorder{err: errors.New("node already cancelled")}
	exec := NewAgentExecutor(launcher, WithRecorder(recorder))

	cfg := AgentNodeConfig{Exec: AgentExecConfig{AgentKey: "implementer"}}
	node := makeAgentNode(t, cfg)

	out, err := exec.Execute(context.Background(), node, RunContext{
		DagKey: "dag-x", NodeKey: "node-a", RunID: 1001,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out.Status != NodeStatusFailed {
		t.Fatalf("Status = %q, want %q", out.Status, NodeStatusFailed)
	}
	if got := launcher.stoppedThreads; len(got) != 1 || got[0] != "thread-late" {
		t.Fatalf("stoppedThreads = %v, want [thread-late]", got)
	}
}

// TestRunningWriteConflictStopsSpawnedChild covers cleanup after a post-launch running write conflict.
func TestRunningWriteConflictStopsSpawnedChild(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{threadID: "thread-running-cas"}
	recorder := &stubNodeSpawnRecorder{}
	exec := NewAgentExecutor(launcher, WithRecorder(recorder))

	cfg := AgentNodeConfig{Exec: AgentExecConfig{AgentKey: "implementer"}}
	node := makeAgentNode(t, cfg)
	var launchedThreadID string
	ctx := WithLaunchedThreadCapture(context.Background(), &launchedThreadID)

	out, err := exec.Execute(ctx, node, RunContext{
		DagKey: "dag-x", NodeKey: "node-a", RunID: 1001,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out.Status != NodeStatusDone {
		t.Fatalf("Status = %q, want %q", out.Status, NodeStatusDone)
	}
	if launchedThreadID != "thread-running-cas" {
		t.Fatalf("captured launched thread = %q, want thread-running-cas", launchedThreadID)
	}
	summary := exec.StopLaunchedThreadAfterWritebackFailure(ctx, launchedThreadID, errors.New("running wakeup fence lost"))
	if !strings.Contains(summary, "launched thread stopped") {
		t.Fatalf("stop summary = %q, want launched thread stopped", summary)
	}
	if got := launcher.stoppedThreads; len(got) != 1 || got[0] != "thread-running-cas" {
		t.Fatalf("stoppedThreads = %v, want [thread-running-cas]", got)
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
