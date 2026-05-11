package nodeexec

import (
	"encoding/json"
	"errors"
	"testing"
)

// agentConfigFixture 提供一份完整 agent config 用于 round-trip 测试。
func agentConfigFixture() AgentNodeConfig {
	return AgentNodeConfig{
		Exec: AgentExecConfig{
			Provider:      "claude",
			Model:         "opus",
			AgentKey:      "architect",
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
	if got.Exec.Provider != "claude" || got.Exec.Model != "opus" {
		t.Errorf("Exec lost fields: %+v", got.Exec)
	}
	if got.Exec.Isolation != "worktree" {
		t.Errorf("isolation = %q, want worktree", got.Exec.Isolation)
	}
	if got.Exec.OnFailure == nil || got.Exec.OnFailure.ByClass[FailureClassCapability] != OnFailureEscalateModel {
		t.Errorf("OnFailure.ByClass round-trip lost: %+v", got.Exec.OnFailure)
	}
	if len(got.Exec.OnFailure.EscalationChain) != 2 {
		t.Errorf("EscalationChain = %v, want [sonnet opus]", got.Exec.OnFailure.EscalationChain)
	}
	if got.Outputs.ToSharedfile == nil || got.Outputs.ToSharedfile.LockMode != "exclusive" {
		t.Errorf("Outputs.ToSharedfile round-trip lost: %+v", got.Outputs.ToSharedfile)
	}
	if string(got.Outputs.Schema) != `{"type":"object","required":["summary"]}` {
		t.Errorf("Schema round-trip lost: %s", got.Outputs.Schema)
	}
	if got.Inputs.Summarization == nil || got.Inputs.Summarization.Strategy != "last_n" {
		t.Errorf("Summarization round-trip lost: %+v", got.Inputs.Summarization)
	}
	if got.FirstTurn != "请按规范输出" {
		t.Errorf("FirstTurn lost: %q", got.FirstTurn)
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
		Inputs:  InputsConfig{FromNodes: []string{"prep"}},
		Outputs: OutputsConfig{ToNodeResult: true},
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
}

// TestParseAutomationConfig_KindEmptyDefaultsToCommandCard验证空 kind 默认填 command_card（向下兼容 ADR-007）。
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

// TestParseAutomationConfig_UnknownKindRejected验证未实装 kind 被 fail-fast 拒绝（ADR-007 §4）。
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
				Provider: "claude",
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
			`{"exec":{"provider":"claude","model":"opus"}}`,
			func(t *testing.T, p *ParsedNodeConfig) {
				if p.Agent == nil || p.Automation != nil || p.Hybrid != nil {
					t.Fatalf("agent dispatch wrong: %+v", p)
				}
				if p.Agent.Exec.Model != "opus" {
					t.Errorf("Agent.Exec.Model = %q", p.Agent.Exec.Model)
				}
			},
		},
		{
			"automation",
			`{"exec":{"command_ref":"build"}}`,
			func(t *testing.T, p *ParsedNodeConfig) {
				if p.Automation == nil || p.Agent != nil || p.Hybrid != nil {
					t.Fatalf("automation dispatch wrong: %+v", p)
				}
				if p.Automation.Exec.CommandRef != "build" {
					t.Errorf("Automation.Exec.CommandRef = %q", p.Automation.Exec.CommandRef)
				}
			},
		},
		{
			"hybrid",
			`{"exec":{"automation":{"command_ref":"x"}}}`,
			func(t *testing.T, p *ParsedNodeConfig) {
				if p.Hybrid == nil || p.Agent != nil || p.Automation != nil {
					t.Fatalf("hybrid dispatch wrong: %+v", p)
				}
				if p.Hybrid.Exec.Automation == nil || p.Hybrid.Exec.Automation.CommandRef != "x" {
					t.Errorf("Hybrid.Exec.Automation.CommandRef = %+v", p.Hybrid.Exec.Automation)
				}
			},
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
