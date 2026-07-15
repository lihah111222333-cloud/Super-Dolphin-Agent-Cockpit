package nodeexec

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestStructJSONTagsPreserveZeroValueFieldPresence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		value    any
		required []string
	}{
		{name: "agent", value: AgentNodeConfig{}, required: []string{"execution", "inputs", "outputs"}},
		{name: "automation", value: AutomationNodeConfig{}, required: []string{"execution", "inputs", "outputs"}},
		{name: "hybrid", value: HybridNodeConfig{}, required: []string{"execution", "inputs", "outputs"}},
		{name: "timeout envelope", value: executionTimeoutEnvelope{}, required: []string{"execution", "schedule"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw, err := json.Marshal(test.value)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			for _, name := range test.required {
				if _, ok := fields[name]; !ok {
					t.Fatalf("json = %s, want zero-value field %q present", raw, name)
				}
			}
		})
	}
}

// agentConfigFixture 提供一份完整 agent config 用于 round-trip 测试。
func agentConfigFixture() AgentNodeConfig {
	return AgentNodeConfig{
		Exec: AgentExecConfig{
			Provider:      "codex",
			Model:         "opus",
			AgentKey:      "architect",
			PromptKey:     "main/architect",
			Effort:        "high",
			Language:      "zh",
			Isolation:     "worktree",
			AllowedTools:  []string{"Read", "Bash"},
			DisabledTools: []string{},
			BudgetTokens:  50000,
			OnFailure: &OnFailureConfig{
				Default: OnFailureRetry,
				ByClass: map[FailureClass]OnFailureStrategy{
					FailureClassCapability: OnFailureEscalateModel,
					FailureClassValidation: OnFailureAppendError,
					FailureClassHard:       OnFailureFailFast,
				},
				MaxAttempts:     3,
				EscalationChain: []string{"sonnet", "opus"},
			},
		},
		Inputs: InputsConfig{
			FromNodes:       []string{"prev"},
			FromSharedfiles: []string{"plan.md"},
			Summarization: &SummarizationConfig{
				Strategy:  "last_n",
				MaxTokens: 4000,
			},
		},
		Outputs: OutputsConfig{
			ToSharedfile: &SharedfileTarget{
				Path:     "report.md",
				LockMode: "exclusive",
			},
			ToNodeResult: true,
			Schema:       json.RawMessage(`{"type":"object","required":["summary"]}`),
		},
		FirstTurn: "请按规范输出",
	}
}

func TestParseAgentConfig_RoundTrip(t *testing.T) {
	t.Parallel()
	original := agentConfigFixture()
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := ParseAgentConfig(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	assertAgentExecRoundTrip(t, got.Exec)
	assertAgentInputsRoundTrip(t, got.Inputs)
	assertAgentOutputsRoundTrip(t, got.Outputs)
	if got.FirstTurn != "请按规范输出" {
		t.Errorf("FirstTurn lost: %q", got.FirstTurn)
	}
}

func TestParseNodeConfigsRejectUnknownFieldsAndTrailingJSON(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		parse func(json.RawMessage) error
		raw   string
	}{
		{name: "agent unknown cwd typo", parse: func(raw json.RawMessage) error { _, err := ParseAgentConfig(raw); return err }, raw: `{"exec":{"cwd":"/tmp/ok","cwdd":"/tmp/typo"}}`},
		{name: "automation unknown timeout typo", parse: func(raw json.RawMessage) error { _, err := ParseAutomationConfig(raw); return err }, raw: `{"exec":{"command_ref":"build"},"execution":{"timeout_secc":30}}`},
		{name: "agent negative retry", parse: func(raw json.RawMessage) error { _, err := ParseAgentConfig(raw); return err }, raw: `{"execution":{"retry":-1}}`},
		{name: "automation negative retry", parse: func(raw json.RawMessage) error { _, err := ParseAutomationConfig(raw); return err }, raw: `{"execution":{"retry":-1}}`},
		{name: "hybrid unknown verifier cwd typo", parse: func(raw json.RawMessage) error { _, err := ParseHybridConfig(raw); return err }, raw: `{"exec":{"verifier":{"cwd":"/tmp/ok","cwdd":"/tmp/typo"}}}`},
		{name: "agent trailing document", parse: func(raw json.RawMessage) error { _, err := ParseAgentConfig(raw); return err }, raw: `{} {}`},
		{name: "automation trailing document", parse: func(raw json.RawMessage) error { _, err := ParseAutomationConfig(raw); return err }, raw: `{} {}`},
		{name: "hybrid trailing document", parse: func(raw json.RawMessage) error { _, err := ParseHybridConfig(raw); return err }, raw: `{} {}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.parse(json.RawMessage(test.raw)); err == nil {
				t.Fatalf("parse(%s) error = nil, want strict JSON rejection", test.raw)
			}
		})
	}
}

func assertAgentExecRoundTrip(t *testing.T, exec AgentExecConfig) {
	t.Helper()
	if exec.Provider != "codex" || exec.Model != "opus" {
		t.Errorf("Exec lost fields: %+v", exec)
	}
	if exec.PromptKey != "main/architect" {
		t.Errorf("PromptKey = %q, want main/architect", exec.PromptKey)
	}
	if exec.Isolation != "worktree" {
		t.Errorf("isolation = %q, want worktree", exec.Isolation)
	}
	if exec.OnFailure == nil || exec.OnFailure.ByClass[FailureClassCapability] != OnFailureEscalateModel {
		t.Errorf("OnFailure.ByClass round-trip lost: %+v", exec.OnFailure)
	}
	if len(exec.OnFailure.EscalationChain) != 2 {
		t.Errorf("EscalationChain = %v, want [sonnet opus]", exec.OnFailure.EscalationChain)
	}
}

func assertAgentInputsRoundTrip(t *testing.T, inputs InputsConfig) {
	t.Helper()
	if inputs.Summarization == nil || inputs.Summarization.Strategy != "last_n" {
		t.Errorf("Summarization round-trip lost: %+v", inputs.Summarization)
	}
}

func assertAgentOutputsRoundTrip(t *testing.T, outputs OutputsConfig) {
	t.Helper()
	if outputs.ToSharedfile == nil || outputs.ToSharedfile.LockMode != "exclusive" {
		t.Errorf("Outputs.ToSharedfile round-trip lost: %+v", outputs.ToSharedfile)
	}
	if string(outputs.Schema) != `{"type":"object","required":["summary"]}` {
		t.Errorf("Schema round-trip lost: %s", outputs.Schema)
	}
}

func TestParseAutomationConfig_RoundTrip(t *testing.T) {
	t.Parallel()
	original := AutomationNodeConfig{
		Exec: AutomationExecConfig{
			CommandRef:   "build_app",
			Args:         json.RawMessage(`{"target":"linux"}`),
			BudgetTokens: 0,
			OnFailure: &OnFailureConfig{
				Default:     OnFailureRetry,
				MaxAttempts: 2,
			},
		},
		Execution: ExecutionConfig{Timeout: "45s"},
		Inputs:    InputsConfig{FromNodes: []string{"prep"}},
		Outputs:   OutputsConfig{ToNodeResult: true},
	}
	data, _ := json.Marshal(original)
	got, err := ParseAutomationConfig(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.Exec.CommandRef != "build_app" {
		t.Errorf("CommandRef lost: %q", got.Exec.CommandRef)
	}
	if string(got.Exec.Args) != `{"target":"linux"}` {
		t.Errorf("Args lost: %s", got.Exec.Args)
	}
	if got.Execution.Timeout != "45s" {
		t.Errorf("Execution.Timeout lost: %q", got.Execution.Timeout)
	}
}

// TestParseAutomationConfig_KindEmptyDefaultsToCommandCard 验证旧配置缺省 kind 时仍映射到 command_card。
func TestParseAutomationConfig_KindEmptyDefaultsToCommandCard(t *testing.T) {
	t.Parallel()
	cases := []string{
		`{"exec":{"command_ref":"build"}}`,           // kind 缺失
		`{"exec":{"kind":"","command_ref":"build"}}`, // kind 空字符串
	}
	for _, raw := range cases {
		got, err := ParseAutomationConfig(json.RawMessage(raw))
		if err != nil {
			t.Fatalf("parse %s: %v", raw, err)
		}
		if got.Exec.Kind != AutomationKindCommandCard {
			t.Errorf("raw=%s: Exec.Kind = %q, want %q", raw, got.Exec.Kind, AutomationKindCommandCard)
		}
	}
}

// TestParseAutomationConfig_KindCommandCardRoundTrip验证显式 kind=command_card round-trip 不丢。
func TestParseAutomationConfig_KindCommandCardRoundTrip(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"exec":{"kind":"command_card","command_ref":"build"}}`)
	got, err := ParseAutomationConfig(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.Exec.Kind != AutomationKindCommandCard {
		t.Errorf("Exec.Kind = %q, want %q", got.Exec.Kind, AutomationKindCommandCard)
	}
	if got.Exec.CommandRef != "build" {
		t.Errorf("CommandRef lost: %q", got.Exec.CommandRef)
	}
}

// TestParseAutomationConfig_UnknownKindRejected 验证未实装 kind 会被 fail-fast 拒绝。
func TestParseAutomationConfig_UnknownKindRejected(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"exec":{"kind":"webhook","command_ref":"x"}}`)
	_, err := ParseAutomationConfig(raw)
	if err == nil {
		t.Fatalf("expected error for unknown kind")
	}
	if !errors.Is(err, ErrUnsupportedAutomationKind) {
		t.Errorf("err = %v, want errors.Is(ErrUnsupportedAutomationKind)", err)
	}
}

func TestParseHybridConfig_RoundTrip(t *testing.T) {
	t.Parallel()
	original := HybridNodeConfig{
		Exec: HybridExecConfig{
			Automation: &AutomationExecConfig{CommandRef: "run_tests"},
			Verifier: &AgentExecConfig{
				Provider: "codex",
				Model:    "sonnet",
				AgentKey: "verifier",
			},
		},
	}
	data, _ := json.Marshal(original)
	got, err := ParseHybridConfig(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.Exec.Automation == nil || got.Exec.Automation.CommandRef != "run_tests" {
		t.Errorf("Automation round-trip lost: %+v", got.Exec.Automation)
	}
	if got.Exec.Verifier == nil || got.Exec.Verifier.AgentKey != "verifier" {
		t.Errorf("Verifier round-trip lost: %+v", got.Exec.Verifier)
	}
}

func TestParseNodeConfig_DispatchByNodeType(t *testing.T) {
	t.Parallel()
	cases := []struct {
		nodeType string
		raw      string
		check    func(*testing.T, *ParsedNodeConfig)
	}{
		{
			"agent",
			`{"exec":{"provider":"codex","model":"opus"}}`,
			assertAgentDispatchConfig,
		},
		{
			"automation",
			`{"exec":{"command_ref":"build"}}`,
			assertAutomationDispatchConfig,
		},
		{
			"hybrid",
			`{"exec":{"automation":{"command_ref":"x"}}}`,
			assertHybridDispatchConfig,
		},
	}
	for _, tc := range cases {
		t.Run(tc.nodeType, func(t *testing.T) {
			got, err := ParseNodeConfig(tc.nodeType, json.RawMessage(tc.raw))
			if err != nil {
				t.Fatalf("ParseNodeConfig: %v", err)
			}
			if got.NodeType != tc.nodeType {
				t.Errorf("NodeType = %q, want %q", got.NodeType, tc.nodeType)
			}
			tc.check(t, got)
		})
	}
}

func assertAgentDispatchConfig(t *testing.T, p *ParsedNodeConfig) {
	t.Helper()
	if p.Agent == nil || p.Automation != nil || p.Hybrid != nil {
		t.Fatalf("agent dispatch wrong: %+v", p)
	}
	if p.Agent.Exec.Model != "opus" {
		t.Errorf("Agent.Exec.Model = %q", p.Agent.Exec.Model)
	}
}

func assertAutomationDispatchConfig(t *testing.T, p *ParsedNodeConfig) {
	t.Helper()
	if p.Automation == nil || p.Agent != nil || p.Hybrid != nil {
		t.Fatalf("automation dispatch wrong: %+v", p)
	}
	if p.Automation.Exec.CommandRef != "build" {
		t.Errorf("Automation.Exec.CommandRef = %q", p.Automation.Exec.CommandRef)
	}
}

func assertHybridDispatchConfig(t *testing.T, p *ParsedNodeConfig) {
	t.Helper()
	if p.Hybrid == nil || p.Agent != nil || p.Automation != nil {
		t.Fatalf("hybrid dispatch wrong: %+v", p)
	}
	if p.Hybrid.Exec.Automation == nil || p.Hybrid.Exec.Automation.CommandRef != "x" {
		t.Errorf("Hybrid.Exec.Automation.CommandRef = %+v", p.Hybrid.Exec.Automation)
	}
}

func TestParseNodeConfig_UnknownNodeType(t *testing.T) {
	t.Parallel()
	_, err := ParseNodeConfig("bogus", json.RawMessage(`{}`))
	if !errors.Is(err, ErrUnknownNodeType) {
		t.Fatalf("err = %v, want ErrUnknownNodeType", err)
	}
}

func TestParseNodeConfig_EmptyRawReturnsZero(t *testing.T) {
	t.Parallel()
	// 空 raw 应返回 zero-value config，不报错（旧 DAG 兼容）
	for _, nodeType := range []string{"agent", "automation", "hybrid"} {
		got, err := ParseNodeConfig(nodeType, nil)
		if err != nil {
			t.Errorf("%s: empty raw err = %v", nodeType, err)
		}
		if got == nil {
			t.Errorf("%s: returned nil parsed config", nodeType)
		}
	}
}

func TestParseNodeConfig_InvalidJSONReturnsError(t *testing.T) {
	t.Parallel()
	_, err := ParseAgentConfig(json.RawMessage(`{not json`))
	if err == nil {
		t.Fatalf("expected JSON parse error")
	}
}

// TestSharedfileTarget_AlwaysObjectShape: lock_mode 是 object 不是 string.
func TestSharedfileTarget_AlwaysObjectShape(t *testing.T) {
	t.Parallel()
	cfg := AgentNodeConfig{
		Outputs: OutputsConfig{
			ToSharedfile: &SharedfileTarget{Path: "x.md", LockMode: "append"},
		},
	}
	data, _ := json.Marshal(cfg)
	// 关键：to_sharedfile 必须是 object，不是 string 路径。
	expected := `"to_sharedfile":{"path":"x.md","lock_mode":"append"}`
	if !contains(string(data), expected) {
		t.Errorf("ToSharedfile not object shape: %s", data)
	}
}

// TestOnFailureConfig_ByClassMapKeys: by_class 的 key 是 FailureClass 字符串.
func TestOnFailureConfig_ByClassMapKeys(t *testing.T) {
	t.Parallel()
	cfg := OnFailureConfig{
		ByClass: map[FailureClass]OnFailureStrategy{
			FailureClassCapability: OnFailureEscalateModel,
			FailureClassQuota:      OnFailureFailFast,
		},
	}
	data, _ := json.Marshal(cfg)
	// key 应该是 "capability" / "quota" 而不是 Go 类型名
	if !contains(string(data), `"capability":"escalate_model"`) {
		t.Errorf("by_class key not FailureClass string: %s", data)
	}
	if !contains(string(data), `"quota":"fail_fast"`) {
		t.Errorf("by_class round-trip lost: %s", data)
	}

	// 反向 unmarshal 验证 typed key 还原
	var got OnFailureConfig
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ByClass[FailureClassCapability] != OnFailureEscalateModel {
		t.Errorf("ByClass roundtrip lost: %+v", got.ByClass)
	}
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Automation result 物化回归测试放在本文件，复用同包 stubs 并避免 executor_automation_test.go 继续膨胀。
func TestAutomationExecutor_EmptyInputsOutputs_KeepsF21Behaviour(t *testing.T) {
	t.Parallel()
	getter := &stubAutomationGetter{card: AutomationCommandCard{CardKey: "k", CommandTemplate: "x", Enabled: true}}
	runner := &captureRunner{result: AutomationCommandResult{CardKey: "k", Stdout: "out"}}
	exec := NewAutomationExecutor(getter, runner)
	node := makeAutomationNode(t, AutomationNodeConfig{
		Exec: AutomationExecConfig{CommandRef: "k", Args: json.RawMessage(`{"x":1}`)},
	})

	out := executeAutomationNode(t, exec, node, RunContext{})
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
	if _, hasInputs := got["__inputs"]; hasInputs {
		t.Fatalf("lastArgs should not contain injected __inputs key on empty Inputs config: %#v", got)
	}
}

func TestClassifyAutomationError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want FailureClass
	}{
		{"not_found_hard", errors.New("command missing not found"), FailureClassHard},
		{"timeout_transient", errors.New("i/o timeout"), FailureClassTransient},
		{"network_transient", errors.New("connection refused"), FailureClassTransient},
		{"infra", errors.New("sqlite database unavailable"), FailureClassInfrastructure},
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

// TestEnforceNodeResultSizeCap_Boundary 验证 node.result 大小边界：等于 cap 通过，超过即 validation 失败。
func TestEnforceNodeResultSizeCap_Boundary(t *testing.T) {
	if NodeResultSizeCapBytes != 4096 {
		t.Fatalf("ADR-006 cap drift; NodeResultSizeCapBytes = %d, want 4096", NodeResultSizeCapBytes)
	}
	// 恰好 4096 bytes 是允许写入 node.result 的上限。
	if out := enforceNodeResultSizeCap(make([]byte, 4096)); out != nil {
		t.Fatalf("4096-byte payload must pass; got outcome=%+v", out)
	}
	// 4097 bytes 已超过上限，必须拒绝写入。
	out := enforceNodeResultSizeCap(make([]byte, 4097))
	if out == nil {
		t.Fatalf("4097-byte payload must be rejected; got nil")
	}
	if out.Status != NodeStatusFailed || out.FailureClass != FailureClassValidation {
		t.Fatalf("got status=%q class=%q, want failed/validation", out.Status, out.FailureClass)
	}
	if !strings.Contains(out.ErrorSummary, "outputs.to_sharedfile") {
		t.Fatalf("ErrorSummary must hint outputs.to_sharedfile fix; got %q", out.ErrorSummary)
	}
}

// TestEnforceNodeResultSizeCap_FivekBytes 5000 byte 拒绝（典型超阈案例）。
func TestEnforceNodeResultSizeCap_FivekBytes(t *testing.T) {
	out := enforceNodeResultSizeCap(make([]byte, 5000))
	if out == nil {
		t.Fatalf("5000-byte payload must be rejected; got nil")
	}
	if out.FailureClass != FailureClassValidation {
		t.Fatalf("FailureClass = %q, want validation", out.FailureClass)
	}
	if !strings.Contains(out.ErrorSummary, "5000") || !strings.Contains(out.ErrorSummary, "4096") {
		t.Fatalf("ErrorSummary should report both actual and cap; got %q", out.ErrorSummary)
	}
}

// TestAutomationExecutor_Outputs_OversizeResultRejected 端到端：runner 返回的 stdout
// marshal 后超 4KB → finalizeAutomationOutcome 拒绝，不写 NodeOutcome.Result。
func TestAutomationExecutor_Outputs_OversizeResultRejected(t *testing.T) {
	// Stdout 撑爆 4KB；marshal 后整个 AutomationCommandResult JSON 也大于 4KB。
	bigStdout := strings.Repeat("x", 5000)
	getter := &stubAutomationGetter{card: AutomationCommandCard{CardKey: "k", CommandTemplate: "x", Enabled: true}}
	runner := &captureRunner{result: AutomationCommandResult{CardKey: "k", Stdout: bigStdout}}
	exec := NewAutomationExecutor(getter, runner)
	node := makeAutomationNode(t, AutomationNodeConfig{
		Exec:    AutomationExecConfig{CommandRef: "k"},
		Outputs: OutputsConfig{ToNodeResult: true},
	})

	out := executeAutomationNode(t, exec, node, RunContext{})
	if out.Status != NodeStatusFailed || out.FailureClass != FailureClassValidation {
		t.Fatalf("got status=%q class=%q, want failed/validation", out.Status, out.FailureClass)
	}
	if !strings.Contains(out.ErrorSummary, "ADR-006") {
		t.Fatalf("ErrorSummary should cite ADR-006; got %q", out.ErrorSummary)
	}
	if out.Result != nil {
		t.Fatalf("Result must NOT be set when size cap rejection fires; got %d bytes", len(out.Result))
	}
}

// TestAutomationExecutor_Outputs_OversizeViaSharedfile_OK 验证 sharedfile 旁路：
// stdout > 4KB 但 to_node_result=false + to_sharedfile=path → node.result 只写小 envelope
// → 不触发 size cap rejection，这是大输出场景的推荐修复路径。
func TestAutomationExecutor_Outputs_OversizeViaSharedfile_OK(t *testing.T) {
	bigStdout := strings.Repeat("y", 5000)
	getter := &stubAutomationGetter{card: AutomationCommandCard{CardKey: "k", CommandTemplate: "x", Enabled: true}}
	runner := &captureRunner{result: AutomationCommandResult{CardKey: "k", Stdout: bigStdout}}
	writer := &stubAutomationSharedFileWriter{}
	exec := NewAutomationExecutor(getter, runner)
	node := makeAutomationNode(t, AutomationNodeConfig{
		Exec:    AutomationExecConfig{CommandRef: "k"},
		Outputs: OutputsConfig{ToSharedfile: &SharedfileTarget{Path: "reports/big.log"}},
	})

	out := executeAutomationNode(t, exec, node, RunContext{
		DagKey:           "dag-x",
		NodeKey:          "node-auto",
		RunID:            7,
		SharedFileWriter: writer,
	})
	if out.Status != NodeStatusDone {
		t.Fatalf("Status = %q, want done; ErrorSummary=%q", out.Status, out.ErrorSummary)
	}
	if len(writer.writes) != 1 || writer.writes[0].Content != bigStdout {
		t.Fatalf("writer should hold the big stdout; writes=%#v", writer.writes)
	}
	if len(out.Result) == 0 || strings.Contains(string(out.Result), bigStdout[:128]) {
		t.Fatalf("Result = %s, want small sharedfile envelope without large stdout", out.Result)
	}
	if !strings.Contains(string(out.Result), "reports/big.log") {
		t.Fatalf("Result = %s, want sharedfile path", out.Result)
	}
}

func TestAutomationExecutor_SharedfileEnvelopeFeedsDownstreamAutomation(t *testing.T) {
	t.Parallel()
	getter := &stubAutomationGetter{card: AutomationCommandCard{CardKey: "k", CommandTemplate: "x", Enabled: true}}
	upstreamRunner := &captureRunner{result: AutomationCommandResult{CardKey: "k", Stdout: "artifact payload"}}
	writer := &stubAutomationSharedFileWriter{}
	upstreamExec := NewAutomationExecutor(getter, upstreamRunner)
	upstreamNode := makeAutomationNode(t, AutomationNodeConfig{
		Exec:    AutomationExecConfig{CommandRef: "produce"},
		Outputs: OutputsConfig{ToSharedfile: &SharedfileTarget{Path: "reports/artifact.log"}},
	})

	upstream := executeAutomationNode(t, upstreamExec, upstreamNode, RunContext{
		DagKey:           "dag-x",
		NodeKey:          "producer",
		RunID:            42,
		SharedFileWriter: writer,
	})
	if upstream.Status != NodeStatusDone {
		t.Fatalf("upstream status = %q, want done; summary=%q", upstream.Status, upstream.ErrorSummary)
	}

	downstreamRunner := &captureRunner{result: AutomationCommandResult{CardKey: "k", Stdout: "consumed"}}
	downstreamExec := NewAutomationExecutor(getter, downstreamRunner)
	downstreamNode := makeAutomationNode(t, AutomationNodeConfig{
		Exec:   AutomationExecConfig{CommandRef: "consume", Args: json.RawMessage(`{"mode":"consume"}`)},
		Inputs: InputsConfig{FromNodes: []string{"producer"}},
	})
	downstream := executeAutomationNode(t, downstreamExec, downstreamNode, RunContext{
		PrevResults: map[string]json.RawMessage{"producer": upstream.Result},
	})
	if downstream.Status != NodeStatusDone {
		t.Fatalf("downstream status = %q, want done; summary=%q", downstream.Status, downstream.ErrorSummary)
	}
	var args map[string]any
	if err := json.Unmarshal(downstreamRunner.lastArgs, &args); err != nil {
		t.Fatalf("unmarshal downstream args: %v; raw=%s", err, downstreamRunner.lastArgs)
	}
	inputs, ok := args["__inputs"].(map[string]any)
	if !ok {
		t.Fatalf("__inputs missing in downstream args: %#v", args)
	}
	fromNodes, ok := inputs["from_nodes"].(map[string]any)
	if !ok {
		t.Fatalf("__inputs.from_nodes missing: %#v", inputs)
	}
	producer, ok := fromNodes["producer"].(map[string]any)
	if !ok {
		t.Fatalf("producer envelope missing: %#v", fromNodes)
	}
	if producer["kind"] != "sharedfile" || producer["path"] != "reports/artifact.log" {
		t.Fatalf("producer envelope = %#v, want sharedfile reports/artifact.log", producer)
	}
}
