package nodeexec

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/idgen"
)

func TestBuildLaunchRequestFromAgentConfigFillsAgentIDAndName(t *testing.T) {
	t.Parallel()
	cfg := AgentNodeConfig{
		Exec: AgentExecConfig{
			AgentKey:  "implementer",
			PromptKey: "user/implementer",
			CWD:       "/repo/agent",
			Language:  "zh",
		},
		FirstTurn: "do the thing",
	}
	node := Node{
		NodeType: "agent",
		Title:    "Validate DAG node",
	}

	req := buildLaunchRequestFromAgentConfig(idgen.NewGenerator(), &cfg, node, RunContext{})
	again := buildLaunchRequestFromAgentConfig(idgen.NewGenerator(), &cfg, node, RunContext{})

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
	if req.PromptKey != cfg.Exec.PromptKey {
		t.Fatalf("PromptKey = %q, want %q", req.PromptKey, cfg.Exec.PromptKey)
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
			Provider:      " CODEX ",
			Model:         " opus ",
			Effort:        " high ",
			AgentKey:      "implementer",
			CWD:           "/repo/agent",
			DisabledTools: []string{"shell", " browser "},
		},
	}

	req := buildLaunchRequestFromAgentConfig(idgen.NewGenerator(), &cfg, Node{NodeType: "agent", Title: "node"}, RunContext{})

	if got := launchEnvValue(req.Env, "AGENT_PROVIDER"); got != "codex" {
		t.Fatalf("AGENT_PROVIDER = %q, want codex", got)
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

func TestBuildLaunchRequestFromAgentConfigForwardsCodexModelProvider(t *testing.T) {
	t.Parallel()
	var cfg AgentNodeConfig
	err := json.Unmarshal([]byte(`{
		"exec": {
			"provider": "codex",
			"codex_home": " /Users/mac/.codex ",
			"codex_instance_key": " default ",
			"codex_model_provider": " openai "
		}
	}`), &cfg)
	if err != nil {
		t.Fatalf("unmarshal agent config: %v", err)
	}

	req := buildLaunchRequestFromAgentConfig(idgen.NewGenerator(), &cfg, Node{NodeType: "agent", Title: "node"}, RunContext{})

	if got := launchEnvValue(req.Env, "AGENT_CODEX_HOME"); got != "/Users/mac/.codex" {
		t.Fatalf("AGENT_CODEX_HOME = %q, want /Users/mac/.codex; env=%#v", got, req.Env)
	}
	if got := launchEnvValue(req.Env, "AGENT_CODEX_INSTANCE_KEY"); got != "default" {
		t.Fatalf("AGENT_CODEX_INSTANCE_KEY = %q, want default; env=%#v", got, req.Env)
	}
	if got := launchEnvValue(req.Env, "AGENT_CODEX_MODEL_PROVIDER"); got != "openai" {
		t.Fatalf("AGENT_CODEX_MODEL_PROVIDER = %q, want openai; env=%#v", got, req.Env)
	}
}

func TestAgentExecutorExecuteCodexProviderRequiresCompleteIdentityWhenOverridePresent(t *testing.T) {
	t.Parallel()
	exec := NewAgentExecutor(&stubAgentLauncher{})
	node := makeAgentNode(t, AgentNodeConfig{Exec: AgentExecConfig{
		Provider:           "codex",
		AgentKey:           "implementer",
		CodexModelProvider: "openai",
	}})

	out, err := exec.Execute(context.Background(), node, RunContext{})
	if err != nil {
		t.Fatalf("Execute() framework error = %v, want nil", err)
	}
	if out.Status != NodeStatusFailed || out.FailureClass != FailureClassValidation {
		t.Fatalf("outcome = (%q, %q), want failed validation", out.Status, out.FailureClass)
	}
	if !strings.Contains(out.ErrorSummary, "codex_home") || !strings.Contains(out.ErrorSummary, "codex_instance_key") {
		t.Fatalf("ErrorSummary = %q, want complete codex identity guidance", out.ErrorSummary)
	}
}

func TestAgentExecutorExecuteRequiresExplicitProvider(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{}
	exec := NewAgentExecutor(launcher)
	raw, err := json.Marshal(AgentNodeConfig{Exec: AgentExecConfig{
		AgentKey: "implementer",
		CWD:      testCWD(t, "node-cwd"),
	}})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	node := Node{NodeType: "agent", Title: "test node", Config: raw}

	out, err := exec.Execute(context.Background(), node, RunContext{})
	if err != nil {
		t.Fatalf("Execute() framework error = %v, want nil", err)
	}
	if out.Status != NodeStatusFailed || out.FailureClass != FailureClassValidation {
		t.Fatalf("outcome = (%q, %q), want failed validation", out.Status, out.FailureClass)
	}
	if !strings.Contains(out.ErrorSummary, "provider") {
		t.Fatalf("ErrorSummary = %q, want provider guidance", out.ErrorSummary)
	}
	if launcher.called != 0 {
		t.Fatalf("launcher called %d times, want 0 for missing provider", launcher.called)
	}
}

func TestAgentExecutorExecuteCodexProviderAllowsEmptyIdentity(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{}
	exec := NewAgentExecutor(launcher)
	node := makeAgentNode(t, AgentNodeConfig{Exec: AgentExecConfig{
		Provider: "codex",
		AgentKey: "implementer",
	}})

	out, err := exec.Execute(context.Background(), node, RunContext{})
	if err != nil {
		t.Fatalf("Execute() framework error = %v, want nil", err)
	}
	if out.Status != NodeStatusDone {
		t.Fatalf("outcome = %q, want Done", out.Status)
	}
	if launcher.called != 1 {
		t.Fatalf("launcher called %d times, want 1", launcher.called)
	}
}

func TestBuildLaunchRequestFromAgentConfigDoesNotInventCWD(t *testing.T) {
	t.Parallel()
	cfg := AgentNodeConfig{Exec: AgentExecConfig{AgentKey: "implementer"}}
	req := buildLaunchRequestFromAgentConfig(idgen.NewGenerator(), &cfg, Node{NodeType: "agent", Title: "node"}, RunContext{})
	if req.Cwd != "" {
		t.Fatalf("Cwd = %q, want empty until exec.cwd is explicit", req.Cwd)
	}
}

func TestAgentExecutor_Execute_MissingCWDDoesNotCallLauncher(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{err: contract.ErrLaunchCWDRequired}
	exec := NewAgentExecutor(launcher)
	raw, err := json.Marshal(AgentNodeConfig{Exec: AgentExecConfig{Provider: "codex", AgentKey: "implementer"}})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	out, err := exec.Execute(context.Background(), Node{NodeType: "agent", Title: "node", Config: raw}, RunContext{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out.Status != NodeStatusFailed || out.FailureClass != FailureClassValidation {
		t.Fatalf("outcome = %#v, want validation failure", out)
	}
	if launcher.called != 0 {
		t.Fatalf("launcher called %d times, want 0 for missing cwd contract failure", launcher.called)
	}
	if strings.HasPrefix(out.ErrorSummary, "launch agent:") {
		t.Fatalf("ErrorSummary = %q, must not be retryable launch-agent validation", out.ErrorSummary)
	}
}

func TestAgentExecutor_ExecuteClassifiesLaunchCWDContractError(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{err: contract.ErrLaunchCWDRequired}
	exec := NewAgentExecutor(launcher)
	raw, err := json.Marshal(AgentNodeConfig{Exec: AgentExecConfig{Provider: "codex", AgentKey: "implementer", CWD: testCWD(t, "node-cwd")}})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	out, err := exec.Execute(context.Background(), Node{NodeType: "agent", Title: "node", Config: raw}, RunContext{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out.Status != NodeStatusFailed || out.FailureClass != FailureClassValidation {
		t.Fatalf("outcome = %#v, want validation failure", out)
	}
	if launcher.called != 1 {
		t.Fatalf("launcher called %d times, want 1", launcher.called)
	}
	if strings.HasPrefix(out.ErrorSummary, "launch agent:") {
		t.Fatalf("ErrorSummary = %q, must not be retryable launch-agent validation", out.ErrorSummary)
	}
}

func TestAgentExecutor_Execute_InvalidConfig_BadJSON(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{}
	exec := NewAgentExecutor(launcher)

	node := Node{
		NodeType: "agent",
		Config:   json.RawMessage(`{"exec": "not-an-object"`), // 截断 + 类型错
	}
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
		t.Fatalf("LaunchAgent should not be called on invalid config, got %d", launcher.called)
	}
}

func TestAgentExecutor_Execute_InvalidConfig_MissingAgentKey(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{}
	exec := NewAgentExecutor(launcher)

	cfg := AgentNodeConfig{
		Exec: AgentExecConfig{Provider: "codex"}, // 缺 agent_key
	}
	node := makeAgentNode(t, cfg)

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
		t.Fatalf("LaunchAgent should not be called, got %d", launcher.called)
	}
}

func TestAgentExecutor_Execute_InvalidProvider(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{}
	exec := NewAgentExecutor(launcher)

	cfg := AgentNodeConfig{
		Exec: AgentExecConfig{
			AgentKey: "implementer",
			Provider: "openai",
		},
	}
	node := makeAgentNode(t, cfg)

	out, err := exec.Execute(context.Background(), node, RunContext{})
	if err != nil {
		t.Fatalf("Execute() framework error = %v, want classified provider validation outcome", err)
	}
	if out.Status != NodeStatusFailed {
		t.Fatalf("Status = %q, want %q", out.Status, NodeStatusFailed)
	}
	if out.FailureClass != FailureClassValidation {
		t.Fatalf("FailureClass = %q, want %q", out.FailureClass, FailureClassValidation)
	}
	if !strings.Contains(out.ErrorSummary, "invalid provider") {
		t.Fatalf("ErrorSummary = %q, want invalid provider", out.ErrorSummary)
	}
	if launcher.called != 0 {
		t.Fatalf("LaunchAgent should not be called, got %d", launcher.called)
	}
}

func TestAgentExecutor_Execute_LaunchTransientErr(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
