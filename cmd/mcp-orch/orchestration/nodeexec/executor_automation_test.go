package nodeexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type stubAutomationGetter struct {
	called  int
	lastKey string
	card    AutomationCommandCard
	err     error
}

func (s *stubAutomationGetter) GetCommandCard(_ context.Context, cardKey string) (AutomationCommandCard, error) {
	s.called++
	s.lastKey = cardKey
	return s.card, s.err
}

type stubAutomationRunner struct {
	called   int
	lastCard AutomationCommandCard
	lastArgs json.RawMessage
	result   AutomationCommandResult
	err      error
}

func (s *stubAutomationRunner) RunCommandCard(_ context.Context, card AutomationCommandCard, args json.RawMessage) (AutomationCommandResult, error) {
	s.called++
	s.lastCard = card
	s.lastArgs = append(json.RawMessage(nil), args...)
	return s.result, s.err
}

func makeAutomationNode(t *testing.T, cfg AutomationNodeConfig) Node {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal AutomationNodeConfig: %v", err)
	}
	return Node{
		DagKey:   "dag-x",
		NodeKey:  "node-auto",
		NodeType: "automation",
		Title:    "automation node",
		Config:   raw,
	}
}

func TestAutomationExecutor_Happy(t *testing.T) {
	getter := &stubAutomationGetter{card: AutomationCommandCard{
		CardKey:         "build_app",
		CommandTemplate: "printf 'hello %s' '{{.name}}'",
		Enabled:         true,
	}}
	exec := NewAutomationExecutor(getter, NewShellCommandRunner())
	node := makeAutomationNode(t, AutomationNodeConfig{Exec: AutomationExecConfig{
		Kind:       AutomationKindCommandCard,
		CommandRef: " build_app ",
		Args:       json.RawMessage(`{"name":"dolphin"}`),
	}})

	out, err := exec.Execute(context.Background(), node, RunContext{DagKey: "dag-x", NodeKey: "node-auto", RunID: 7})
	if err != nil {
		t.Fatalf("Execute() framework error = %v, want nil", err)
	}
	if out.Status != NodeStatusDone {
		t.Fatalf("Status = %q, want %q", out.Status, NodeStatusDone)
	}
	if out.FailureClass != "" {
		t.Fatalf("FailureClass = %q, want empty on success", out.FailureClass)
	}
	if getter.called != 1 || getter.lastKey != "build_app" {
		t.Fatalf("command_get called (%d, %q), want (1, build_app)", getter.called, getter.lastKey)
	}
	var result AutomationCommandResult
	if err := json.Unmarshal(out.Result, &result); err != nil {
		t.Fatalf("unmarshal Result: %v; payload=%s", err, out.Result)
	}
	if result.CardKey != "build_app" || result.ExitCode != 0 || result.Stdout != "hello dolphin" {
		t.Fatalf("Result = %#v, want card_key build_app exit 0 stdout hello dolphin", result)
	}
}

func TestAutomationExecutor_UnsupportedKind(t *testing.T) {
	getter := &stubAutomationGetter{}
	runner := &stubAutomationRunner{}
	exec := NewAutomationExecutor(getter, runner)
	node := Node{NodeType: "automation", Config: json.RawMessage(`{"exec":{"kind":"webhook","command_ref":"x"}}`)}

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
	if !strings.Contains(out.ErrorSummary, "unsupported automation.kind") {
		t.Fatalf("ErrorSummary = %q, want unsupported automation.kind", out.ErrorSummary)
	}
	if getter.called != 0 || runner.called != 0 {
		t.Fatalf("getter/runner called on unsupported kind: getter=%d runner=%d", getter.called, runner.called)
	}
}

func TestAutomationExecutor_CommandNotFound(t *testing.T) {
	getter := &stubAutomationGetter{err: errors.New("command missing not found")}
	runner := &stubAutomationRunner{}
	exec := NewAutomationExecutor(getter, runner)
	node := makeAutomationNode(t, AutomationNodeConfig{Exec: AutomationExecConfig{CommandRef: "missing"}})

	out, err := exec.Execute(context.Background(), node, RunContext{})
	if err != nil {
		t.Fatalf("Execute() framework error = %v, want nil", err)
	}
	if out.Status != NodeStatusFailed {
		t.Fatalf("Status = %q, want %q", out.Status, NodeStatusFailed)
	}
	if out.FailureClass != FailureClassHard {
		t.Fatalf("FailureClass = %q, want %q", out.FailureClass, FailureClassHard)
	}
	if runner.called != 0 {
		t.Fatalf("runner called on command_get failure: %d", runner.called)
	}
}

func TestAutomationExecutor_Timeout(t *testing.T) {
	getter := &stubAutomationGetter{card: AutomationCommandCard{CardKey: "slow", CommandTemplate: "sleep 10", Enabled: true}}
	runner := &stubAutomationRunner{err: context.DeadlineExceeded}
	exec := NewAutomationExecutor(getter, runner)
	node := makeAutomationNode(t, AutomationNodeConfig{Exec: AutomationExecConfig{CommandRef: "slow"}})

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
}

func TestAutomationExecutor_NilLauncher(t *testing.T) {
	exec := NewAutomationExecutor(nil, &stubAutomationRunner{})
	node := makeAutomationNode(t, AutomationNodeConfig{Exec: AutomationExecConfig{CommandRef: "build_app"}})

	out, err := exec.Execute(context.Background(), node, RunContext{})
	if err != nil {
		t.Fatalf("Execute() framework error = %v, want nil", err)
	}
	if out.Status != NodeStatusFailed {
		t.Fatalf("Status = %q, want %q on nil command_get client", out.Status, NodeStatusFailed)
	}
	if out.FailureClass != FailureClassValidation {
		t.Fatalf("FailureClass = %q, want %q", out.FailureClass, FailureClassValidation)
	}
	if !strings.Contains(out.ErrorSummary, "command_get client not wired") {
		t.Fatalf("ErrorSummary = %q, want command_get client not wired", out.ErrorSummary)
	}
}

func TestAutomationExecutor_ImplementsNodeExecutor(t *testing.T) {
	var _ NodeExecutor = (*AutomationExecutor)(nil)
}

// ---------------------------------------------------------------------------
// F2.2 下的 inputs/outputs 测例
// ---------------------------------------------------------------------------

type stubAutomationSharedFileReader struct {
	content map[string]string
	err     error
	calls   []string
}

// 端口收敛 batch 后 SharedFileReader 统一为 (content, exists, err) 三态。
// 本测试 stub 用 ok 表达 exists；not-found 不再走 err 路径，避免与基础设施 err 混淆。
func (s *stubAutomationSharedFileReader) ReadSharedFile(_ context.Context, path string) (string, bool, error) {
	s.calls = append(s.calls, path)
	if s.err != nil {
		return "", false, s.err
	}
	if v, ok := s.content[path]; ok {
		return v, true, nil
	}
	return "", false, nil
}

type stubAutomationSharedFileWriter struct {
	writes []struct{ Path, Content string }
	err    error
}

func (s *stubAutomationSharedFileWriter) WriteSharedFile(_ context.Context, path, content string) error {
	if s.err != nil {
		return s.err
	}
	s.writes = append(s.writes, struct{ Path, Content string }{path, content})
	return nil
}

// captureRunner 记录 RunCommandCard 被调用时的 args，同时返回可定制 result。
type captureRunner struct {
	lastArgs json.RawMessage
	result   AutomationCommandResult
}

func (c *captureRunner) RunCommandCard(_ context.Context, _ AutomationCommandCard, args json.RawMessage) (AutomationCommandResult, error) {
	c.lastArgs = append(json.RawMessage(nil), args...)
	return c.result, nil
}

func TestAutomationExecutor_Inputs_InjectsFromNodes(t *testing.T) {
	getter := &stubAutomationGetter{card: AutomationCommandCard{CardKey: "build_app", CommandTemplate: "noop", Enabled: true}}
	runner := &captureRunner{result: AutomationCommandResult{CardKey: "build_app", Stdout: "ok"}}
	exec := NewAutomationExecutor(getter, runner)
	node := makeAutomationNode(t, AutomationNodeConfig{
		Exec: AutomationExecConfig{
			CommandRef: "build_app",
			Args:       json.RawMessage(`{"target":"prod"}`),
		},
		Inputs: InputsConfig{FromNodes: []string{"plan"}},
	})
	prev := map[string]json.RawMessage{
		"plan": json.RawMessage(`{"summary":"build prod"}`),
	}

	out, err := exec.Execute(context.Background(), node, RunContext{PrevResults: prev})
	if err != nil {
		t.Fatalf("Execute() framework error = %v", err)
	}
	if out.Status != NodeStatusDone {
		t.Fatalf("Status = %q, want %q", out.Status, NodeStatusDone)
	}
	var merged map[string]any
	if err := json.Unmarshal(runner.lastArgs, &merged); err != nil {
		t.Fatalf("unmarshal lastArgs: %v; raw=%s", err, runner.lastArgs)
	}
	if merged["target"] != "prod" {
		t.Fatalf("original arg target lost: %#v", merged)
	}
	inputs, ok := merged["inputs"].(map[string]any)
	if !ok {
		t.Fatalf("inputs subobject missing: %#v", merged)
	}
	fromNodes, ok := inputs["from_nodes"].(map[string]any)
	if !ok {
		t.Fatalf("inputs.from_nodes missing: %#v", inputs)
	}
	plan, ok := fromNodes["plan"].(map[string]any)
	if !ok {
		t.Fatalf("inputs.from_nodes.plan not an object: %#v", fromNodes["plan"])
	}
	if plan["summary"] != "build prod" {
		t.Fatalf("plan.summary = %v, want 'build prod'", plan["summary"])
	}
	if out.Result == nil {
		t.Fatalf("Result nil but ToNodeResult default path expected to write payload")
	}
}

func TestAutomationExecutor_Inputs_MissingPrevResult_Validation(t *testing.T) {
	getter := &stubAutomationGetter{card: AutomationCommandCard{CardKey: "k", CommandTemplate: "x", Enabled: true}}
	runner := &captureRunner{}
	exec := NewAutomationExecutor(getter, runner)
	node := makeAutomationNode(t, AutomationNodeConfig{
		Exec:   AutomationExecConfig{CommandRef: "k"},
		Inputs: InputsConfig{FromNodes: []string{"missing"}},
	})

	out, err := exec.Execute(context.Background(), node, RunContext{PrevResults: map[string]json.RawMessage{}})
	if err != nil {
		t.Fatalf("Execute() framework error = %v", err)
	}
	if out.Status != NodeStatusFailed || out.FailureClass != FailureClassValidation {
		t.Fatalf("got status=%q class=%q, want failed/validation", out.Status, out.FailureClass)
	}
	if !strings.Contains(out.ErrorSummary, "missing prev result") {
		t.Fatalf("ErrorSummary = %q, want missing prev result", out.ErrorSummary)
	}
	if runner.lastArgs != nil {
		t.Fatalf("runner should not be called on validation failure; got args=%s", runner.lastArgs)
	}
}

func TestAutomationExecutor_Inputs_ReservedKeyConflict_Validation(t *testing.T) {
	getter := &stubAutomationGetter{card: AutomationCommandCard{CardKey: "k", CommandTemplate: "x", Enabled: true}}
	runner := &captureRunner{}
	exec := NewAutomationExecutor(getter, runner)
	node := makeAutomationNode(t, AutomationNodeConfig{
		Exec: AutomationExecConfig{
			CommandRef: "k",
			Args:       json.RawMessage(`{"inputs":"already-there"}`),
		},
		Inputs: InputsConfig{FromNodes: []string{"a"}},
	})

	out, err := exec.Execute(context.Background(), node, RunContext{PrevResults: map[string]json.RawMessage{"a": json.RawMessage(`{}`)}})
	if err != nil {
		t.Fatalf("Execute() framework error = %v", err)
	}
	if out.Status != NodeStatusFailed || out.FailureClass != FailureClassValidation {
		t.Fatalf("got status=%q class=%q, want failed/validation", out.Status, out.FailureClass)
	}
	if !strings.Contains(out.ErrorSummary, "reserved key") {
		t.Fatalf("ErrorSummary = %q, want reserved key", out.ErrorSummary)
	}
}

func TestAutomationExecutor_Inputs_SharedfileNoReader_Validation(t *testing.T) {
	getter := &stubAutomationGetter{card: AutomationCommandCard{CardKey: "k", CommandTemplate: "x", Enabled: true}}
	runner := &captureRunner{}
	exec := NewAutomationExecutor(getter, runner)
	node := makeAutomationNode(t, AutomationNodeConfig{
		Exec:   AutomationExecConfig{CommandRef: "k"},
		Inputs: InputsConfig{FromSharedfiles: []string{"plan.md"}},
	})

	out, err := exec.Execute(context.Background(), node, RunContext{})
	if err != nil {
		t.Fatalf("Execute() framework error = %v", err)
	}
	if out.Status != NodeStatusFailed || out.FailureClass != FailureClassValidation {
		t.Fatalf("got status=%q class=%q, want failed/validation", out.Status, out.FailureClass)
	}
	if !strings.Contains(out.ErrorSummary, "SharedFileReader not wired") {
		t.Fatalf("ErrorSummary = %q, want SharedFileReader not wired", out.ErrorSummary)
	}
}

func TestAutomationExecutor_Inputs_SharedfileReadError_Classified(t *testing.T) {
	getter := &stubAutomationGetter{card: AutomationCommandCard{CardKey: "k", CommandTemplate: "x", Enabled: true}}
	runner := &captureRunner{}
	reader := &stubAutomationSharedFileReader{err: errors.New("i/o timeout reading plan.md")}
	exec := NewAutomationExecutor(getter, runner)
	node := makeAutomationNode(t, AutomationNodeConfig{
		Exec:   AutomationExecConfig{CommandRef: "k"},
		Inputs: InputsConfig{FromSharedfiles: []string{"plan.md"}},
	})

	out, err := exec.Execute(context.Background(), node, RunContext{SharedFileReader: reader})
	if err != nil {
		t.Fatalf("Execute() framework error = %v", err)
	}
	if out.Status != NodeStatusFailed {
		t.Fatalf("Status = %q, want failed", out.Status)
	}
	if out.FailureClass != FailureClassTransient {
		t.Fatalf("FailureClass = %q, want transient (timeout)", out.FailureClass)
	}
}

func TestAutomationExecutor_Outputs_WritesSharedfile(t *testing.T) {
	getter := &stubAutomationGetter{card: AutomationCommandCard{CardKey: "k", CommandTemplate: "x", Enabled: true}}
	runner := &captureRunner{result: AutomationCommandResult{CardKey: "k", Stdout: "hello world", ExitCode: 0}}
	writer := &stubAutomationSharedFileWriter{}
	exec := NewAutomationExecutor(getter, runner)
	node := makeAutomationNode(t, AutomationNodeConfig{
		Exec:    AutomationExecConfig{CommandRef: "k"},
		Outputs: OutputsConfig{ToSharedfile: &SharedfileTarget{Path: "reports/build.log", LockMode: "exclusive"}},
	})

	out, err := exec.Execute(context.Background(), node, RunContext{SharedFileWriter: writer})
	if err != nil {
		t.Fatalf("Execute() framework error = %v", err)
	}
	if out.Status != NodeStatusDone {
		t.Fatalf("Status = %q, want done; summary=%q", out.Status, out.ErrorSummary)
	}
	if len(writer.writes) != 1 {
		t.Fatalf("writer called %d times, want 1", len(writer.writes))
	}
	if writer.writes[0].Path != "reports/build.log" || writer.writes[0].Content != "hello world" {
		t.Fatalf("write = %#v, want path=reports/build.log content='hello world'", writer.writes[0])
	}
	// to_node_result 未勾选，且 to_sharedfile 启用 → NodeOutcome.Result 不重复写。
	if out.Result != nil {
		t.Fatalf("Result should be nil when to_sharedfile set and to_node_result unset; got %s", out.Result)
	}
}

func TestAutomationExecutor_Outputs_BothChannels(t *testing.T) {
	getter := &stubAutomationGetter{card: AutomationCommandCard{CardKey: "k", CommandTemplate: "x", Enabled: true}}
	runner := &captureRunner{result: AutomationCommandResult{CardKey: "k", Stdout: "payload"}}
	writer := &stubAutomationSharedFileWriter{}
	exec := NewAutomationExecutor(getter, runner)
	node := makeAutomationNode(t, AutomationNodeConfig{
		Exec: AutomationExecConfig{CommandRef: "k"},
		Outputs: OutputsConfig{
			ToSharedfile: &SharedfileTarget{Path: "reports/build.log"},
			ToNodeResult: true,
		},
	})

	out, err := exec.Execute(context.Background(), node, RunContext{SharedFileWriter: writer})
	if err != nil {
		t.Fatalf("Execute() framework error = %v", err)
	}
	if out.Status != NodeStatusDone {
		t.Fatalf("Status = %q, want done", out.Status)
	}
	if len(writer.writes) != 1 || writer.writes[0].Content != "payload" {
		t.Fatalf("writer state = %#v, want 1 write of payload", writer.writes)
	}
	if out.Result == nil {
		t.Fatalf("Result must be present when to_node_result=true")
	}
}

func TestAutomationExecutor_Outputs_SharedfileNoWriter_Validation(t *testing.T) {
	getter := &stubAutomationGetter{card: AutomationCommandCard{CardKey: "k", CommandTemplate: "x", Enabled: true}}
	runner := &captureRunner{result: AutomationCommandResult{CardKey: "k"}}
	exec := NewAutomationExecutor(getter, runner)
	node := makeAutomationNode(t, AutomationNodeConfig{
		Exec:    AutomationExecConfig{CommandRef: "k"},
		Outputs: OutputsConfig{ToSharedfile: &SharedfileTarget{Path: "reports/build.log"}},
	})

	out, err := exec.Execute(context.Background(), node, RunContext{})
	if err != nil {
		t.Fatalf("Execute() framework error = %v", err)
	}
	if out.Status != NodeStatusFailed || out.FailureClass != FailureClassValidation {
		t.Fatalf("got status=%q class=%q, want failed/validation", out.Status, out.FailureClass)
	}
	if !strings.Contains(out.ErrorSummary, "SharedFileWriter not wired") {
		t.Fatalf("ErrorSummary = %q, want SharedFileWriter not wired", out.ErrorSummary)
	}
}

func TestAutomationExecutor_Outputs_SharedfileWriteFails_Validation(t *testing.T) {
	getter := &stubAutomationGetter{card: AutomationCommandCard{CardKey: "k", CommandTemplate: "x", Enabled: true}}
	runner := &captureRunner{result: AutomationCommandResult{CardKey: "k", Stdout: "data"}}
	writer := &stubAutomationSharedFileWriter{err: errors.New("disk full")}
	exec := NewAutomationExecutor(getter, runner)
	node := makeAutomationNode(t, AutomationNodeConfig{
		Exec:    AutomationExecConfig{CommandRef: "k"},
		Outputs: OutputsConfig{ToSharedfile: &SharedfileTarget{Path: "x.log"}},
	})

	out, err := exec.Execute(context.Background(), node, RunContext{SharedFileWriter: writer})
	if err != nil {
		t.Fatalf("Execute() framework error = %v", err)
	}
	if out.Status != NodeStatusFailed || out.FailureClass != FailureClassValidation {
		t.Fatalf("got status=%q class=%q, want failed/validation", out.Status, out.FailureClass)
	}
	if !strings.Contains(out.ErrorSummary, "disk full") {
		t.Fatalf("ErrorSummary = %q, want disk full", out.ErrorSummary)
	}
}

func TestAutomationExecutor_Outputs_RejectsAgentPromptField(t *testing.T) {
	getter := &stubAutomationGetter{}
	runner := &captureRunner{}
	exec := NewAutomationExecutor(getter, runner)
	// raw config 中 outputs 手动推 "prompt" 字段（typed OutputsConfig 不认，但原始 json 看得到）。
	node := Node{
		NodeType: "automation",
		Config: json.RawMessage(`{
			"exec":{"kind":"command_card","command_ref":"k"},
			"outputs":{"prompt":"please summarize"}
		}`),
	}

	out, err := exec.Execute(context.Background(), node, RunContext{})
	if err != nil {
		t.Fatalf("Execute() framework error = %v", err)
	}
	if out.Status != NodeStatusFailed || out.FailureClass != FailureClassValidation {
		t.Fatalf("got status=%q class=%q, want failed/validation", out.Status, out.FailureClass)
	}
	if !strings.Contains(out.ErrorSummary, "agent-prompt or agent-routing field") {
		t.Fatalf("ErrorSummary = %q, want 'agent-prompt or agent-routing field'", out.ErrorSummary)
	}
	if getter.called != 0 || runner.lastArgs != nil {
		t.Fatalf("getter/runner should not be reached on outputs-validation failure; getter=%d runnerArgs=%s", getter.called, runner.lastArgs)
	}
}

// TestAutomationExecutor_Outputs_RejectsAllBannedKeys 表驱动覆盖全 11 个 banned key。
//
// R1 P1 #3 + R2 P0 gap：原测试只覆盖了 "prompt" 一个；扩展为 prompt-injection 5 +
// agent-routing 6 = 11 个，确保任何路由字段从 automation outputs 注入下游 agent 节点
// 都被 validation 拦截，不至于让 automation 隐式驱动下游 agent 路由 / 升级 model。
func TestAutomationExecutor_Outputs_RejectsAllBannedKeys(t *testing.T) {
	// keep in sync with executor_automation.go::automationOutputsForbiddenKeys
	bannedKeys := []string{
		// prompt-injection family
		"prompt", "first_turn", "agent_prompt", "system_prompt", "append_error",
		// agent-routing family
		"agent_key", "model", "provider", "language", "tool_choice", "tools",
	}
	if len(bannedKeys) != 11 {
		t.Fatalf("test table out of sync; want 11 banned keys, got %d", len(bannedKeys))
	}

	for _, key := range bannedKeys {
		t.Run(key, func(t *testing.T) {
			getter := &stubAutomationGetter{}
			runner := &captureRunner{}
			exec := NewAutomationExecutor(getter, runner)
			cfg := fmt.Sprintf(`{
				"exec":{"kind":"command_card","command_ref":"k"},
				"outputs":{%q:"x"}
			}`, key)
			node := Node{NodeType: "automation", Config: json.RawMessage(cfg)}

			out, err := exec.Execute(context.Background(), node, RunContext{})
			if err != nil {
				t.Fatalf("Execute() framework error = %v", err)
			}
			if out.Status != NodeStatusFailed || out.FailureClass != FailureClassValidation {
				t.Fatalf("banned key %q not rejected; got status=%q class=%q", key, out.Status, out.FailureClass)
			}
			if !strings.Contains(out.ErrorSummary, key) {
				t.Fatalf("ErrorSummary should mention offending key %q; got %q", key, out.ErrorSummary)
			}
			if getter.called != 0 || runner.lastArgs != nil {
				t.Fatalf("getter/runner should not be reached for banned key %q", key)
			}
		})
	}
}

func TestAutomationExecutor_EmptyInputsOutputs_KeepsF21Behaviour(t *testing.T) {
	getter := &stubAutomationGetter{card: AutomationCommandCard{CardKey: "k", CommandTemplate: "x", Enabled: true}}
	runner := &captureRunner{result: AutomationCommandResult{CardKey: "k", Stdout: "out"}}
	exec := NewAutomationExecutor(getter, runner)
	node := makeAutomationNode(t, AutomationNodeConfig{
		Exec: AutomationExecConfig{CommandRef: "k", Args: json.RawMessage(`{"x":1}`)},
	})

	out, err := exec.Execute(context.Background(), node, RunContext{})
	if err != nil {
		t.Fatalf("Execute() framework error = %v", err)
	}
	if out.Status != NodeStatusDone {
		t.Fatalf("Status = %q, want done", out.Status)
	}
	if out.Result == nil {
		t.Fatalf("Result must be set on F2.1 fallback path")
	}
	// runner 拿到的 args 应与原 cfg.Exec.Args 一致（未被 merge 路径动过）。
	var got map[string]any
	if err := json.Unmarshal(runner.lastArgs, &got); err != nil {
		t.Fatalf("unmarshal lastArgs: %v", err)
	}
	if _, hasInputs := got["inputs"]; hasInputs {
		t.Fatalf("lastArgs should not contain injected inputs key on empty Inputs config: %#v", got)
	}
}

func TestClassifyAutomationError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want FailureClass
	}{
		{"not_found_hard", errors.New("command missing not found"), FailureClassHard},
		{"timeout_transient", errors.New("i/o timeout"), FailureClassTransient},
		{"network_transient", errors.New("connection refused"), FailureClassTransient},
		{"infra", errors.New("postgres service unavailable"), FailureClassInfrastructure},
		{"parse_validation", errors.New("parse command args: invalid json"), FailureClassValidation},
		{"nonzero_hard", CommandExitError{ExitCode: 2, Err: errors.New("exit status 2")}, FailureClassHard},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyAutomationError(tc.err); got != tc.want {
				t.Fatalf("classifyAutomationError(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}
