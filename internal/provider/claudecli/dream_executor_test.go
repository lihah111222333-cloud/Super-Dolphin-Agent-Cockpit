package claudecli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/dreamexec"
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
	if len(c.lastArgs) != 1 || c.lastArgs[0] != "-p" {
		t.Errorf("args without model: got %v, want [-p]", c.lastArgs)
	}
}

func TestClaudeDreamExecutor_ModelEnvAddsArgs(t *testing.T) {
	c := &capturingCommander{outputs: []string{`{"memories":[]}`}}
	exec := newDreamExecutor(c, "claude", "claude-sonnet-4-5")
	if _, err := exec.ExecuteDream(context.Background(), "p"); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	want := []string{"-p", "--model", "claude-sonnet-4-5"}
	if len(c.lastArgs) != len(want) {
		t.Fatalf("args: got %v, want %v", c.lastArgs, want)
	}
	for i, a := range want {
		if c.lastArgs[i] != a {
			t.Fatalf("args[%d]: got %q, want %q", i, c.lastArgs[i], a)
		}
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
