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

func (s *stubAutomationRunner) RunCommandCard(_ context.Context, card AutomationCommandCard, args json.RawMessage, _ ...AutomationCommandRunOptions) (AutomationCommandResult, error) {
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

func executeAutomationNode(t *testing.T, exec NodeExecutor, node Node, runCtx RunContext) NodeOutcome {
	t.Helper()
	out, err := exec.Execute(context.Background(), node, runCtx)
	if err != nil {
		t.Fatalf("Execute() framework error = %v", err)
	}
	return out
}

func TestShellCommandRunnerRejectsRenderedShellInjection(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	runner := NewShellCommandRunner()

	result, err := runner.RunCommandCard(context.Background(), AutomationCommandCard{
		CardKey:         "greet",
		CommandTemplate: "printf 'hello %s' '{{.name}}'",
		RiskLevel:       "high",
		Enabled:         true,
	}, json.RawMessage(`{"name":"dolphin'; printf 'pwned"}`), AutomationCommandRunOptions{
		CWD:            root,
		WorkspaceRoots: []string{root},
	})

	if err == nil || !strings.Contains(err.Error(), "unsafe shell metacharacter") {
		t.Fatalf("RunCommandCard() error = %v, want unsafe shell metacharacter rejection", err)
	}
	if strings.Contains(result.Stdout, "pwned") {
		t.Fatalf("RunCommandCard() stdout = %q, want injected command not executed", result.Stdout)
	}
}

func TestAutomationExecutor_Happy(t *testing.T) {
	t.Parallel()
	getter := &stubAutomationGetter{card: AutomationCommandCard{
		CardKey:         "build_app",
		CommandTemplate: "printf 'hello %s' '{{.name}}'",
		RiskLevel:       "high",
		Enabled:         true,
	}}
	exec := NewAutomationExecutor(getter, NewShellCommandRunner())
	root := t.TempDir()
	node := makeAutomationNode(t, AutomationNodeConfig{Exec: AutomationExecConfig{
		Kind:           AutomationKindCommandCard,
		CommandRef:     " build_app ",
		Args:           json.RawMessage(`{"name":"dolphin"}`),
		CWD:            root,
		WorkspaceRoots: []string{root},
	}})

	out := executeAutomationNode(t, exec, node, RunContext{DagKey: "dag-x", NodeKey: "node-auto", RunID: 7})
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
	t.Parallel()
	getter := &stubAutomationGetter{}
	runner := &stubAutomationRunner{}
	exec := NewAutomationExecutor(getter, runner)
	node := Node{NodeType: "automation", Config: json.RawMessage(`{"exec":{"kind":"webhook","command_ref":"x"}}`)}

	out := executeAutomationNode(t, exec, node, RunContext{DagKey: "dag-x", NodeKey: "node-auto", RunID: 7})
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
	t.Parallel()
	getter := &stubAutomationGetter{err: errors.New("command missing not found")}
	runner := &stubAutomationRunner{}
	exec := NewAutomationExecutor(getter, runner)
	node := makeAutomationNode(t, AutomationNodeConfig{Exec: AutomationExecConfig{CommandRef: "missing"}})

	out := executeAutomationNode(t, exec, node, RunContext{DagKey: "dag-x", NodeKey: "node-auto", RunID: 7})
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
	t.Parallel()
	getter := &stubAutomationGetter{card: AutomationCommandCard{CardKey: "slow", CommandTemplate: "sleep 10", Enabled: true}}
	runner := &stubAutomationRunner{err: context.DeadlineExceeded}
	exec := NewAutomationExecutor(getter, runner)
	node := makeAutomationNode(t, AutomationNodeConfig{Exec: AutomationExecConfig{CommandRef: "slow"}})

	out := executeAutomationNode(t, exec, node, RunContext{
		DagKey:  "dag-x",
		NodeKey: "node-auto",
		RunID:   7,
	})
	if out.Status != NodeStatusFailed {
		t.Fatalf("Status = %q, want %q", out.Status, NodeStatusFailed)
	}
	if out.FailureClass != FailureClassTransient {
		t.Fatalf("FailureClass = %q, want %q", out.FailureClass, FailureClassTransient)
	}
}

func TestAutomationExecutor_NilLauncher(t *testing.T) {
	t.Parallel()
	exec := NewAutomationExecutor(nil, &stubAutomationRunner{})
	node := makeAutomationNode(t, AutomationNodeConfig{Exec: AutomationExecConfig{CommandRef: "build_app"}})

	out := executeAutomationNode(t, exec, node, RunContext{})
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
	t.Parallel()
	var _ NodeExecutor = (*AutomationExecutor)(nil)
}

func TestCommandOutputBufferTruncatesAndReportsDroppedBytes(t *testing.T) {
	t.Parallel()
	buf := newCommandOutputBuffer("stdout", 5)

	n, err := buf.Write([]byte("hello world"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if n != len("hello world") {
		t.Fatalf("Write() n = %d, want %d", n, len("hello world"))
	}

	got := buf.String()
	if !strings.HasPrefix(got, "hello\n[super-dolphin: stdout truncated after 5 bytes; dropped 6 bytes]") {
		t.Fatalf("buffer output = %q, want retained prefix and truncation marker", got)
	}
	if strings.Contains(got, "world") {
		t.Fatalf("buffer output = %q, want overflow bytes dropped", got)
	}
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
	lastOpts []AutomationCommandRunOptions
	result   AutomationCommandResult
}

func (c *captureRunner) RunCommandCard(_ context.Context, _ AutomationCommandCard, args json.RawMessage, opts ...AutomationCommandRunOptions) (AutomationCommandResult, error) {
	c.lastArgs = append(json.RawMessage(nil), args...)
	c.lastOpts = append([]AutomationCommandRunOptions(nil), opts...)
	return c.result, nil
}

type echoArgsAutomationRunner struct{}

func (echoArgsAutomationRunner) RunCommandCard(_ context.Context, card AutomationCommandCard, args json.RawMessage, _ ...AutomationCommandRunOptions) (AutomationCommandResult, error) {
	return AutomationCommandResult{
		CardKey:  card.CardKey,
		ExitCode: 0,
		Stdout:   "ok",
		Args:     append(json.RawMessage(nil), args...),
	}, nil
}

func TestAutomationExecutor_Inputs_InjectsFromNodes(t *testing.T) {
	t.Parallel()
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
		"plan": json.RawMessage(`{"summary":"build prod","path":"reports/plan.log","kind":"sharedfile"}`),
	}

	out := executeAutomationNode(t, exec, node, RunContext{PrevResults: prev})
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
	inputs, ok := merged["__inputs"].(map[string]any)
	if !ok {
		t.Fatalf("__inputs subobject missing: %#v", merged)
	}
	fromNodes, ok := inputs["from_nodes"].(map[string]any)
	if !ok {
		t.Fatalf("__inputs.from_nodes missing: %#v", inputs)
	}
	plan, ok := fromNodes["plan"].(map[string]any)
	if !ok {
		t.Fatalf("__inputs.from_nodes.plan not an object: %#v", fromNodes["plan"])
	}
	if plan["summary"] != "build prod" {
		t.Fatalf("plan.summary = %v, want 'build prod'", plan["summary"])
	}
	if plan["path"] != "reports/plan.log" {
		t.Fatalf("plan.path = %v, want reports/plan.log", plan["path"])
	}
	if out.Result == nil {
		t.Fatalf("Result nil but ToNodeResult default path expected to write payload")
	}
}

func TestAutomationCommandResultRedactsSharedFileInputs(t *testing.T) {
	t.Parallel()
	const secret = "SHAREDFILE_RAW_TOKEN_123"
	getter := &stubAutomationGetter{card: AutomationCommandCard{
		CardKey:         "k",
		CommandTemplate: "noop",
		Enabled:         true,
	}}
	exec := NewAutomationExecutor(getter, echoArgsAutomationRunner{})
	node := makeAutomationNode(t, AutomationNodeConfig{
		Exec:    AutomationExecConfig{CommandRef: "k"},
		Inputs:  InputsConfig{FromSharedfiles: []string{"handoff/plan.md"}},
		Outputs: OutputsConfig{ToNodeResult: true},
	})

	out := executeAutomationNode(t, exec, node, RunContext{
		DagKey:  "dag-x",
		NodeKey: "node-auto",
		RunID:   7,
		SharedFileReader: &stubAutomationSharedFileReader{content: map[string]string{
			"handoff/plan.md": secret,
		}},
	})

	if out.Status != NodeStatusDone {
		t.Fatalf("Status = %q, want done; summary=%q", out.Status, out.ErrorSummary)
	}
	if strings.Contains(string(out.Result), secret) {
		t.Fatalf("Result leaked raw sharedfile input token under args/__inputs: %s", out.Result)
	}
	if strings.Contains(string(out.Result), "__inputs") || strings.Contains(string(out.Result), "from_sharedfiles") {
		t.Fatalf("Result exposed injected input topology, want scrubbed metadata-only result: %s", out.Result)
	}
}

func TestAutomationExecutor_Inputs_MissingPrevResult_Validation(t *testing.T) {
	t.Parallel()
	getter := &stubAutomationGetter{card: AutomationCommandCard{CardKey: "k", CommandTemplate: "x", Enabled: true}}
	runner := &captureRunner{}
	exec := NewAutomationExecutor(getter, runner)
	node := makeAutomationNode(t, AutomationNodeConfig{
		Exec:   AutomationExecConfig{CommandRef: "k"},
		Inputs: InputsConfig{FromNodes: []string{"missing"}},
	})

	out := executeAutomationNode(t, exec, node, RunContext{PrevResults: map[string]json.RawMessage{}})
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
	t.Parallel()
	getter := &stubAutomationGetter{card: AutomationCommandCard{CardKey: "k", CommandTemplate: "x", Enabled: true}}
	runner := &captureRunner{}
	exec := NewAutomationExecutor(getter, runner)
	node := makeAutomationNode(t, AutomationNodeConfig{
		Exec: AutomationExecConfig{
			CommandRef: "k",
			Args:       json.RawMessage(`{"__inputs":"already-there"}`),
		},
		Inputs: InputsConfig{FromNodes: []string{"a"}},
	})

	out := executeAutomationNode(t, exec, node, RunContext{PrevResults: map[string]json.RawMessage{"a": json.RawMessage(`{}`)}})
	if out.Status != NodeStatusFailed || out.FailureClass != FailureClassValidation {
		t.Fatalf("got status=%q class=%q, want failed/validation", out.Status, out.FailureClass)
	}
	if !strings.Contains(out.ErrorSummary, "reserved key") {
		t.Fatalf("ErrorSummary = %q, want reserved key", out.ErrorSummary)
	}
}

func TestAutomationExecutor_Inputs_SharedfileNoReader_Validation(t *testing.T) {
	t.Parallel()
	getter := &stubAutomationGetter{card: AutomationCommandCard{CardKey: "k", CommandTemplate: "x", Enabled: true}}
	runner := &captureRunner{}
	exec := NewAutomationExecutor(getter, runner)
	node := makeAutomationNode(t, AutomationNodeConfig{
		Exec:   AutomationExecConfig{CommandRef: "k"},
		Inputs: InputsConfig{FromSharedfiles: []string{"plan.md"}},
	})

	out := executeAutomationNode(t, exec, node, RunContext{})
	if out.Status != NodeStatusFailed || out.FailureClass != FailureClassValidation {
		t.Fatalf("got status=%q class=%q, want failed/validation", out.Status, out.FailureClass)
	}
	if !strings.Contains(out.ErrorSummary, "SharedFileReader not wired") {
		t.Fatalf("ErrorSummary = %q, want SharedFileReader not wired", out.ErrorSummary)
	}
}

func TestAutomationExecutor_Inputs_SharedfileReadError_Classified(t *testing.T) {
	t.Parallel()
	getter := &stubAutomationGetter{card: AutomationCommandCard{CardKey: "k", CommandTemplate: "x", Enabled: true}}
	runner := &captureRunner{}
	reader := &stubAutomationSharedFileReader{err: errors.New("i/o timeout reading plan.md")}
	exec := NewAutomationExecutor(getter, runner)
	node := makeAutomationNode(t, AutomationNodeConfig{
		Exec:   AutomationExecConfig{CommandRef: "k"},
		Inputs: InputsConfig{FromSharedfiles: []string{"plan.md"}},
	})

	out := executeAutomationNode(t, exec, node, RunContext{SharedFileReader: reader})
	if out.Status != NodeStatusFailed {
		t.Fatalf("Status = %q, want failed", out.Status)
	}
	if out.FailureClass != FailureClassTransient {
		t.Fatalf("FailureClass = %q, want transient (timeout)", out.FailureClass)
	}
}

func TestAutomationExecutor_Outputs_WritesSharedfile(t *testing.T) {
	t.Parallel()
	getter := &stubAutomationGetter{card: AutomationCommandCard{CardKey: "k", CommandTemplate: "x", Enabled: true}}
	runner := &captureRunner{result: AutomationCommandResult{CardKey: "k", Stdout: "hello world", ExitCode: 0}}
	writer := &stubAutomationSharedFileWriter{}
	exec := NewAutomationExecutor(getter, runner)
	node := makeAutomationNode(t, AutomationNodeConfig{
		Exec:    AutomationExecConfig{CommandRef: "k"},
		Outputs: OutputsConfig{ToSharedfile: &SharedfileTarget{Path: "reports/build.log", LockMode: "exclusive"}},
	})

	out := executeAutomationNode(t, exec, node, RunContext{
		DagKey:           "dag-x",
		NodeKey:          "node-auto",
		RunID:            7,
		SharedFileWriter: writer,
	})
	if out.Status != NodeStatusDone {
		t.Fatalf("Status = %q, want done; summary=%q", out.Status, out.ErrorSummary)
	}
	if len(writer.writes) != 1 {
		t.Fatalf("writer called %d times, want 1", len(writer.writes))
	}
	if writer.writes[0].Path != "reports/build.log" || writer.writes[0].Content != "hello world" {
		t.Fatalf("write = %#v, want path=reports/build.log content='hello world'", writer.writes[0])
	}
	// to_node_result 未勾选，且 to_sharedfile 启用 → NodeOutcome.Result 写轻量 path envelope。
	if !strings.Contains(string(out.Result), "reports/build.log") {
		t.Fatalf("Result = %s, want sharedfile path envelope", out.Result)
	}
}

func TestAutomationExecutor_Outputs_SharedfileOnlyReturnsContextEnvelope(t *testing.T) {
	t.Parallel()
	getter := &stubAutomationGetter{card: AutomationCommandCard{CardKey: "k", CommandTemplate: "x", Enabled: true}}
	runner := &captureRunner{result: AutomationCommandResult{CardKey: "k", Stdout: "hello world", ExitCode: 0}}
	writer := &stubAutomationSharedFileWriter{}
	exec := NewAutomationExecutor(getter, runner)
	node := makeAutomationNode(t, AutomationNodeConfig{
		Exec:    AutomationExecConfig{CommandRef: "k"},
		Outputs: OutputsConfig{ToSharedfile: &SharedfileTarget{Path: "reports/build.log", LockMode: "exclusive"}},
	})

	out := executeAutomationNode(t, exec, node, RunContext{
		DagKey:           "dag-x",
		NodeKey:          "node-auto",
		RunID:            99,
		SharedFileWriter: writer,
	})
	if out.Status != NodeStatusDone {
		t.Fatalf("Status = %q, want done; summary=%q", out.Status, out.ErrorSummary)
	}
	var envelope struct {
		Kind       string `json:"kind"`
		Path       string `json:"path"`
		Dag        string `json:"dag"`
		Run        int64  `json:"run"`
		Node       string `json:"node"`
		Sharedfile struct {
			Path string `json:"path"`
		} `json:"sharedfile"`
	}
	if err := json.Unmarshal(out.Result, &envelope); err != nil {
		t.Fatalf("unmarshal sharedfile envelope: %v; raw=%s", err, out.Result)
	}
	if envelope.Kind != "sharedfile" || envelope.Path != "reports/build.log" || envelope.Sharedfile.Path != "reports/build.log" {
		t.Fatalf("sharedfile fields = %+v, want kind/path/sharedfile.path", envelope)
	}
	if envelope.Dag != "dag-x" || envelope.Run != 99 || envelope.Node != "node-auto" {
		t.Fatalf("context fields = dag=%q run=%d node=%q, want dag-x/99/node-auto", envelope.Dag, envelope.Run, envelope.Node)
	}
}

func TestAutomationExecutor_Outputs_BothChannels(t *testing.T) {
	t.Parallel()
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

	out := executeAutomationNode(t, exec, node, RunContext{
		DagKey:           "dag-x",
		NodeKey:          "node-auto",
		RunID:            7,
		SharedFileWriter: writer,
	})
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

func TestAutomationExecutor_Outputs_SharedfileEnvelopeCanBeDisabled(t *testing.T) {
	t.Parallel()
	getter := &stubAutomationGetter{card: AutomationCommandCard{CardKey: "k", CommandTemplate: "x", Enabled: true}}
	runner := &captureRunner{result: AutomationCommandResult{CardKey: "k", Stdout: "payload"}}
	writer := &stubAutomationSharedFileWriter{}
	emitEnvelope := false
	exec := NewAutomationExecutor(getter, runner)
	node := makeAutomationNode(t, AutomationNodeConfig{
		Exec: AutomationExecConfig{CommandRef: "k"},
		Outputs: OutputsConfig{
			ToSharedfile:       &SharedfileTarget{Path: "reports/build.log"},
			NodeResultEnvelope: &emitEnvelope,
		},
	})

	out := executeAutomationNode(t, exec, node, RunContext{
		DagKey:           "dag-x",
		NodeKey:          "node-auto",
		RunID:            7,
		SharedFileWriter: writer,
	})
	if out.Status != NodeStatusDone {
		t.Fatalf("Status = %q, want done", out.Status)
	}
	if out.Result != nil {
		t.Fatalf("Result = %s, want nil when node_result_envelope=false", out.Result)
	}
}

func TestAutomationExecutor_Outputs_SharedfileNoWriter_Validation(t *testing.T) {
	t.Parallel()
	getter := &stubAutomationGetter{card: AutomationCommandCard{CardKey: "k", CommandTemplate: "x", Enabled: true}}
	runner := &captureRunner{result: AutomationCommandResult{CardKey: "k"}}
	exec := NewAutomationExecutor(getter, runner)
	node := makeAutomationNode(t, AutomationNodeConfig{
		Exec:    AutomationExecConfig{CommandRef: "k"},
		Outputs: OutputsConfig{ToSharedfile: &SharedfileTarget{Path: "reports/build.log"}},
	})

	out := executeAutomationNode(t, exec, node, RunContext{DagKey: "dag-x", NodeKey: "node-auto", RunID: 7})
	if out.Status != NodeStatusFailed || out.FailureClass != FailureClassValidation {
		t.Fatalf("got status=%q class=%q, want failed/validation", out.Status, out.FailureClass)
	}
	if !strings.Contains(out.ErrorSummary, "SharedFileWriter not wired") {
		t.Fatalf("ErrorSummary = %q, want SharedFileWriter not wired", out.ErrorSummary)
	}
}

func TestAutomationExecutor_Outputs_SharedfileWriteFails_Validation(t *testing.T) {
	t.Parallel()
	getter := &stubAutomationGetter{card: AutomationCommandCard{CardKey: "k", CommandTemplate: "x", Enabled: true}}
	runner := &captureRunner{result: AutomationCommandResult{CardKey: "k", Stdout: "data"}}
	writer := &stubAutomationSharedFileWriter{err: errors.New("disk full")}
	exec := NewAutomationExecutor(getter, runner)
	node := makeAutomationNode(t, AutomationNodeConfig{
		Exec:    AutomationExecConfig{CommandRef: "k"},
		Outputs: OutputsConfig{ToSharedfile: &SharedfileTarget{Path: "x.log"}},
	})

	out := executeAutomationNode(t, exec, node, RunContext{
		DagKey:           "dag-x",
		NodeKey:          "node-auto",
		RunID:            7,
		SharedFileWriter: writer,
	})
	if out.Status != NodeStatusFailed || out.FailureClass != FailureClassValidation {
		t.Fatalf("got status=%q class=%q, want failed/validation", out.Status, out.FailureClass)
	}
	if !strings.Contains(out.ErrorSummary, "disk full") {
		t.Fatalf("ErrorSummary = %q, want disk full", out.ErrorSummary)
	}
}

// TestAutomationExecutor_Outputs_RejectsAllBannedKeys 表驱动覆盖所有禁止注入的 output key。
// 自动化输出不能把 prompt 或 agent-routing 字段注入下游 agent 节点，否则会绕过显式路由和模型选择。
func TestAutomationExecutor_Outputs_RejectsAllBannedKeys(t *testing.T) {
	bannedKeys := automationOutputsForbiddenKeys

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

			out := executeAutomationNode(t, exec, node, RunContext{})
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
