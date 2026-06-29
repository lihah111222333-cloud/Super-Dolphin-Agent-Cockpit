package claudecli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/dreamexec"
	"github.com/anthropic-ai/super-agent-v3/pkg/dreammetrics"
)

// capturingCommander 记录最后一次调用的 binary/args/input，并按预设序列返回。
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

func TestClaudeDreamExecutor_SuccessReturnsExtractedJSON(t *testing.T) {
	c := &capturingCommander{outputs: []string{
		"```json\n{\"memories\":[{\"content\":\"x\",\"type\":\"user\"}]}\n```\n",
	}}
	exec := newDreamExecutor(c, "claude-test-bin", "")
	got, err := exec.ExecuteDream(context.Background(), "consolidate")
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if !strings.Contains(got, `"memories"`) {
		t.Fatalf("expected JSON with memories, got %q", got)
	}
	if c.lastBinary != "claude-test-bin" {
		t.Errorf("binary: got %q, want claude-test-bin", c.lastBinary)
	}
	if c.lastInput != "consolidate" {
		t.Errorf("input: got %q, want 'consolidate'", c.lastInput)
	}
	wantArgs := []string{"-p", "--output-format", "json", "--tools", "", "--permission-mode", "default"}
	if len(c.lastArgs) != len(wantArgs) {
		t.Fatalf("args without model: got %v, want %v", c.lastArgs, wantArgs)
	}
	for i, want := range wantArgs {
		if c.lastArgs[i] != want {
			t.Errorf("args[%d]: got %q, want %q", i, c.lastArgs[i], want)
		}
	}
}

func TestClaudeDreamExecutor_ModelEnvAddsArgs(t *testing.T) {
	c := &capturingCommander{outputs: []string{`{"memories":[]}`}}
	exec := newDreamExecutor(c, "claude", "claude-sonnet-4-5")
	if _, err := exec.ExecuteDream(context.Background(), "p"); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	want := []string{"-p", "--output-format", "json", "--tools", "", "--permission-mode", "default", "--model", "claude-sonnet-4-5"}
	if len(c.lastArgs) != len(want) {
		t.Fatalf("args: got %v, want %v", c.lastArgs, want)
	}
	for i, a := range want {
		if c.lastArgs[i] != a {
			t.Fatalf("args[%d]: got %q, want %q", i, c.lastArgs[i], a)
		}
	}
}

func TestClaudeDreamExecutor_RequestModelOverridesEnvModel(t *testing.T) {
	c := &capturingCommander{outputs: []string{`{"memories":[]}`}}
	exec := newDreamExecutor(c, "claude", "env-model")
	if _, err := exec.ExecuteDreamWithOptions(context.Background(), "p", contract.DreamOptions{Model: "request-model"}); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	want := []string{"-p", "--output-format", "json", "--tools", "", "--permission-mode", "default", "--model", "request-model"}
	if len(c.lastArgs) != len(want) {
		t.Fatalf("args: got %v, want %v", c.lastArgs, want)
	}
	for i, a := range want {
		if c.lastArgs[i] != a {
			t.Fatalf("args[%d]: got %q, want %q", i, c.lastArgs[i], a)
		}
	}
}

func TestClaudeDreamExecutorEnforcesNoToolsReadOnlyRuntime(t *testing.T) {
	c := &capturingCommander{outputs: []string{`{"memories":[]}`}}
	exec := newDreamExecutor(c, "claude", "")
	if _, err := exec.ExecuteDream(context.Background(), "p"); err != nil {
		t.Fatalf("ExecuteDream() error = %v", err)
	}
	assertArgPair(t, c.lastArgs, "--tools", "")
	assertArgPair(t, c.lastArgs, "--permission-mode", "default")
	if hasArgValue(c.lastArgs, "--permission-mode", "bypassPermissions") {
		t.Fatalf("dream args must not bypass permissions: %v", c.lastArgs)
	}
}

// TestClaudeDreamExecutor_EnvelopeUsageIncrementsDreamMetrics 验证完整流路：
// envelope JSON 输出 -> dreamexec 探测 -> ExtractClaudeEnvelope -> OnUsage 路由 -> dreammetrics.AddTokens。
// fixture 使用小数量便于验证：input=1+2+3=6、output=4、cacheRead=3。
func TestClaudeDreamExecutor_EnvelopeUsageIncrementsDreamMetrics(t *testing.T) {
	dreammetrics.ResetForTesting()
	t.Cleanup(dreammetrics.ResetForTesting)
	c := &capturingCommander{outputs: []string{`{"type":"result","is_error":false,"result":"{\"memories\":[]}","usage":{"input_tokens":1,"cache_creation_input_tokens":2,"cache_read_input_tokens":3,"output_tokens":4}}`}}
	exec := newDreamExecutor(c, "claude", "")
	if _, err := exec.ExecuteDream(context.Background(), "p"); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	snap := dreammetrics.Read()
	if snap.TokensInputTotal != 6 || snap.TokensOutputTotal != 4 || snap.TokensCacheReadTotal != 3 {
		t.Fatalf("dream token metrics = %+v, want input=6 output=4 cacheRead=3", snap)
	}
}

func TestClaudeDreamExecutor_BinaryNotAvailableMapsToNotConfigured(t *testing.T) {
	// dreamexec.realCommander 在 binary 不可用时包裹为 ErrBinaryNotAvailable。
	// fake 模拟包裹后的哨兵 error。
	notAvail := fmt.Errorf("%w: claude: fork/exec /nonexistent/claude: no such file or directory", dreamexec.ErrBinaryNotAvailable)
	c := &capturingCommander{errs: []error{notAvail}}
	exec := newDreamExecutor(c, "claude", "")
	_, err := exec.ExecuteDream(context.Background(), "p")
	if !errors.Is(err, contract.ErrDreamExecutorNotConfigured) {
		t.Fatalf("expected ErrDreamExecutorNotConfigured, got %v", err)
	}
	if !strings.Contains(err.Error(), "binary") {
		t.Errorf("expected error to mention binary, got %v", err)
	}
}

func TestClaudeDreamExecutor_ModelUnavailableMapsToNotConfigured(t *testing.T) {
	modelErr := fmt.Errorf("%w: claude exited with error: exit status 1 (stdout: {\"type\":\"result\",\"is_error\":true,\"api_error_status\":404,\"result\":\"There's an issue with the selected model (gpt-5.5). It may not exist or you may not have access to it.\"})", dreamexec.ErrModelUnavailable)
	c := &capturingCommander{errs: []error{modelErr}}
	exec := newDreamExecutor(c, "claude", "")
	_, err := exec.ExecuteDream(context.Background(), "p")
	if !errors.Is(err, contract.ErrDreamExecutorNotConfigured) {
		t.Fatalf("expected ErrDreamExecutorNotConfigured, got %v", err)
	}
	if !strings.Contains(err.Error(), "model") {
		t.Errorf("expected error to mention model, got %v", err)
	}
}

func TestClaudeDreamExecutor_OtherErrorTransparent(t *testing.T) {
	boom := errors.New("auth expired")
	c := &capturingCommander{errs: []error{boom}}
	exec := newDreamExecutor(c, "claude", "")
	_, err := exec.ExecuteDream(context.Background(), "p")
	if !errors.Is(err, boom) {
		t.Fatalf("expected boom transparent, got %v", err)
	}
	// 不应映射为 NotConfigured
	if errors.Is(err, contract.ErrDreamExecutorNotConfigured) {
		t.Fatalf("auth error should NOT map to NotConfigured: %v", err)
	}
}

func TestClaudeDreamExecutor_CanceledContext(t *testing.T) {
	c := &capturingCommander{outputs: []string{`{"memories":[]}`}}
	exec := newDreamExecutor(c, "claude", "")
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

func TestClaudeDreamExecutor_NewDreamExecutorDefaultsBinary(t *testing.T) {
	// 不传 binary 时走 resolveBinaryPath()（默认 "claude" 或 CLAUDE_CLI_BIN）
	exec := newDreamExecutor(nil, "", "")
	if exec.binary == "" {
		t.Fatalf("expected non-empty default binary, got empty")
	}
	if exec.commander == nil {
		t.Fatalf("expected non-nil default commander")
	}
}

func TestClaudeDreamExecutor_NewDreamExecutorRespectsCLAUDE_CLI_BIN(t *testing.T) {
	t.Setenv("CLAUDE_CLI_BIN", "/usr/local/bin/claude-test")
	exec := newDreamExecutor(nil, "", "")
	if exec.binary != "/usr/local/bin/claude-test" {
		t.Fatalf("expected env-overridden binary, got %q", exec.binary)
	}
}

func TestClaudeDreamExecutor_ProviderProviderUsesClaudeName(t *testing.T) {
	p := provideDreamExecutorProvider()
	if p.Name != "claude" {
		t.Fatalf("expected provider Name=claude, got %q", p.Name)
	}
	if p.Executor == nil {
		t.Fatalf("expected non-nil Executor")
	}
}

func assertArgPair(t *testing.T, args []string, flag, value string) {
	t.Helper()
	if !hasArgValue(args, flag, value) {
		t.Fatalf("args missing %s %q: %v", flag, value, args)
	}
}

func hasArgValue(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}
