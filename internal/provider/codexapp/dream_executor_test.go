package codexapp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/dreamexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/pkg/dreammetrics"
)

// capturingCommander 记录最后一次调用的 binary/args/input。
type capturingCommander struct {
	outputs    []string
	errs       []error
	calls      int
	lastBinary string
	lastArgs   []string
	lastInput  string
}

func (c *capturingCommander) Run(ctx context.Context, binary string, args []string, input string, maxStdoutBytes int64) ([]byte, error) {
	idx := c.calls
	c.calls++
	c.lastBinary = binary
	c.lastArgs = append([]string(nil), args...)
	c.lastInput = input
	if idx < len(c.errs) && c.errs[idx] != nil {
		return nil, c.errs[idx]
	}
	if idx < len(c.outputs) {
		return []byte(c.outputs[idx]), nil
	}
	return nil, errors.New("capturingCommander: no more responses configured")
}

func TestCodexDreamExecutor_SuccessReturnsExtractedJSON(t *testing.T) {
	c := &capturingCommander{outputs: []string{
		"```json\n{\"memories\":[{\"content\":\"y\",\"type\":\"reference\"}]}\n```\n",
	}}
	exec := newDreamExecutor(c, "codex-test-bin", "")
	got, err := exec.ExecuteDream(context.Background(), "consolidate")
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if !strings.Contains(got, `"memories"`) {
		t.Fatalf("expected JSON with memories, got %q", got)
	}
	if c.lastBinary != "codex-test-bin" {
		t.Errorf("binary: got %q, want codex-test-bin", c.lastBinary)
	}
	if c.lastInput != "consolidate" {
		t.Errorf("input: got %q, want 'consolidate'", c.lastInput)
	}
	wantArgs := []string{"exec", "--json", "--skip-git-repo-check", "--sandbox", "read-only", "--ephemeral", "--ignore-user-config", "--ignore-rules", "-c", `shell_environment_policy.inherit="none"`}
	if len(c.lastArgs) != len(wantArgs) {
		t.Fatalf("args without model: got %v, want %v", c.lastArgs, wantArgs)
	}
	for i, want := range wantArgs {
		if c.lastArgs[i] != want {
			t.Errorf("args[%d]: got %q, want %q", i, c.lastArgs[i], want)
		}
	}
}

func TestCodexDreamExecutor_ModelEnvAddsArgs(t *testing.T) {
	c := &capturingCommander{outputs: []string{`{"memories":[]}`}}
	exec := newDreamExecutor(c, "codex", "gpt-5-codex")
	if _, err := exec.ExecuteDream(context.Background(), "p"); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	want := []string{"exec", "--json", "--skip-git-repo-check", "--sandbox", "read-only", "--ephemeral", "--ignore-user-config", "--ignore-rules", "-c", `shell_environment_policy.inherit="none"`, "--model", "gpt-5-codex"}
	if len(c.lastArgs) != len(want) {
		t.Fatalf("args: got %v, want %v", c.lastArgs, want)
	}
	for i, a := range want {
		if c.lastArgs[i] != a {
			t.Fatalf("args[%d]: got %q, want %q", i, c.lastArgs[i], a)
		}
	}
}

func TestCodexDreamExecutor_RequestModelOverridesEnvModel(t *testing.T) {
	c := &capturingCommander{outputs: []string{`{"memories":[]}`}}
	exec := newDreamExecutor(c, "codex", "env-model")
	if _, err := exec.ExecuteDreamWithOptions(context.Background(), "p", contract.DreamOptions{Model: "request-model"}); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	want := []string{"exec", "--json", "--skip-git-repo-check", "--sandbox", "read-only", "--ephemeral", "--ignore-user-config", "--ignore-rules", "-c", `shell_environment_policy.inherit="none"`, "--model", "request-model"}
	if len(c.lastArgs) != len(want) {
		t.Fatalf("args: got %v, want %v", c.lastArgs, want)
	}
	for i, a := range want {
		if c.lastArgs[i] != a {
			t.Fatalf("args[%d]: got %q, want %q", i, c.lastArgs[i], a)
		}
	}
}

func TestCodexDreamExecutor_RequestModelProviderAddsConfigArg(t *testing.T) {
	c := &capturingCommander{outputs: []string{`{"memories":[]}`}}
	exec := newDreamExecutor(c, "codex", "")
	if _, err := exec.ExecuteDreamWithOptions(context.Background(), "p", contract.DreamOptions{ModelProvider: "openrouter"}); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	want := []string{"exec", "--json", "--skip-git-repo-check", "--sandbox", "read-only", "--ephemeral", "--ignore-user-config", "--ignore-rules", "-c", `shell_environment_policy.inherit="none"`, "-c", `model_provider="openrouter"`}
	if len(c.lastArgs) != len(want) {
		t.Fatalf("args: got %v, want %v", c.lastArgs, want)
	}
	for i, a := range want {
		if c.lastArgs[i] != a {
			t.Fatalf("args[%d]: got %q, want %q", i, c.lastArgs[i], a)
		}
	}
}

func TestCodexDreamExecutorEnforcesNoToolsReadOnlyMinEnvRuntime(t *testing.T) {
	c := &capturingCommander{outputs: []string{`{"memories":[]}`}}
	exec := newDreamExecutor(c, "codex", "")
	if _, err := exec.ExecuteDream(context.Background(), "p"); err != nil {
		t.Fatalf("ExecuteDream() error = %v", err)
	}
	assertArgPair(t, c.lastArgs, "--sandbox", "read-only")
	assertArgPresent(t, c.lastArgs, "--ephemeral")
	assertArgPresent(t, c.lastArgs, "--ignore-user-config")
	assertArgPresent(t, c.lastArgs, "--ignore-rules")
	assertArgPair(t, c.lastArgs, "-c", `shell_environment_policy.inherit="none"`)
}

// TestCodexDreamExecutor_JSONLUsageIncrementsDreamMetrics 验证完整流路：
// JSONL stream -> dreamexec 探测 -> ExtractCodexJSONL -> OnUsage -> dreammetrics.AddTokens。
// codex usage 语义：input_tokens 已含 cached（OpenAI 惯例），无 cache_creation。
func TestCodexDreamExecutor_JSONLUsageIncrementsDreamMetrics(t *testing.T) {
	dreammetrics.ResetForTesting()
	t.Cleanup(dreammetrics.ResetForTesting)
	jsonl := "{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"{\\\"memories\\\":[]}\"}}\n" +
		"{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":10,\"cached_input_tokens\":3,\"output_tokens\":5}}\n"
	c := &capturingCommander{outputs: []string{jsonl}}
	exec := newDreamExecutor(c, "codex", "")
	if _, err := exec.ExecuteDream(context.Background(), "p"); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	// TokensInput = input_tokens (=10)、TokensOutput = output_tokens (=5)、TokensCacheRead = cached_input_tokens (=3)
	snap := dreammetrics.Read()
	if snap.TokensInputTotal != 10 || snap.TokensOutputTotal != 5 || snap.TokensCacheReadTotal != 3 {
		t.Fatalf("dream token metrics = %+v, want input=10 output=5 cacheRead=3", snap)
	}
}

func TestCodexDreamExecutor_BinaryNotAvailableMapsToNotConfigured(t *testing.T) {
	// dreamexec.realCommander 在 binary 不可用时包裹为 ErrBinaryNotAvailable。
	notAvail := fmt.Errorf("%w: codex: fork/exec /nonexistent/codex: no such file or directory", dreamexec.ErrBinaryNotAvailable)
	c := &capturingCommander{errs: []error{notAvail}}
	exec := newDreamExecutor(c, "codex", "")
	_, err := exec.ExecuteDream(context.Background(), "p")
	if !errors.Is(err, contract.ErrDreamExecutorNotConfigured) {
		t.Fatalf("expected ErrDreamExecutorNotConfigured, got %v", err)
	}
	if !strings.Contains(err.Error(), "binary") {
		t.Errorf("expected error to mention binary, got %v", err)
	}
}

func TestCodexDreamExecutor_ModelUnavailableMapsToNotConfigured(t *testing.T) {
	modelErr := fmt.Errorf("%w: codex exited with error: exit status 1 (stdout: model not found)", dreamexec.ErrModelUnavailable)
	c := &capturingCommander{errs: []error{modelErr}}
	exec := newDreamExecutor(c, "codex", "")
	_, err := exec.ExecuteDream(context.Background(), "p")
	if !errors.Is(err, contract.ErrDreamExecutorNotConfigured) {
		t.Fatalf("expected ErrDreamExecutorNotConfigured, got %v", err)
	}
	if !strings.Contains(err.Error(), "model") {
		t.Errorf("expected error to mention model, got %v", err)
	}
}

func TestCodexDreamExecutor_OtherErrorTransparent(t *testing.T) {
	boom := errors.New("openai rate limit")
	c := &capturingCommander{errs: []error{boom}}
	exec := newDreamExecutor(c, "codex", "")
	_, err := exec.ExecuteDream(context.Background(), "p")
	if !errors.Is(err, boom) {
		t.Fatalf("expected boom transparent, got %v", err)
	}
	if errors.Is(err, contract.ErrDreamExecutorNotConfigured) {
		t.Fatalf("rate limit should NOT map to NotConfigured: %v", err)
	}
}

func TestCodexDreamExecutor_CanceledContext(t *testing.T) {
	c := &capturingCommander{outputs: []string{`{"memories":[]}`}}
	exec := newDreamExecutor(c, "codex", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := exec.ExecuteDream(ctx, "p")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if c.calls != 0 {
		t.Fatalf("expected 0 commander calls when ctx canceled, got %d", c.calls)
	}
}

func TestCodexDreamExecutor_ResolveDreamBinaryDefault(t *testing.T) {
	t.Setenv(dreamBinaryEnv, "")
	if got := resolveDreamBinary(); got != defaultCodexBin {
		t.Fatalf("default: got %q, want %q", got, defaultCodexBin)
	}
}

func TestCodexDreamExecutor_ResolveDreamBinaryRespectsEnv(t *testing.T) {
	t.Setenv(dreamBinaryEnv, "/usr/local/bin/codex-test")
	if got := resolveDreamBinary(); got != "/usr/local/bin/codex-test" {
		t.Fatalf("env override: got %q, want /usr/local/bin/codex-test", got)
	}
}

func TestCodexDreamExecutor_ProviderProviderUsesCodexName(t *testing.T) {
	p := provideDreamExecutorProvider()
	if p.Name != "codex" {
		t.Fatalf("expected provider Name=codex, got %q", p.Name)
	}
	if p.Executor == nil {
		t.Fatalf("expected non-nil Executor")
	}
}

func assertArgPresent(t *testing.T, args []string, flag string) {
	t.Helper()
	for _, arg := range args {
		if arg == flag {
			return
		}
	}
	t.Fatalf("args missing %s: %v", flag, args)
}

func assertArgPair(t *testing.T, args []string, flag, value string) {
	t.Helper()
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return
		}
	}
	t.Fatalf("args missing %s %q: %v", flag, value, args)
}
