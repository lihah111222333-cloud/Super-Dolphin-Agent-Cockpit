package codexapp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/dreamexec"
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
	if len(c.lastArgs) != 1 || c.lastArgs[0] != "exec" {
		t.Errorf("args without model: got %v, want [exec]", c.lastArgs)
	}
}

func TestCodexDreamExecutor_ModelEnvAddsArgs(t *testing.T) {
	c := &capturingCommander{outputs: []string{`{"memories":[]}`}}
	exec := newDreamExecutor(c, "codex", "gpt-5-codex")
	if _, err := exec.ExecuteDream(context.Background(), "p"); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	want := []string{"exec", "--model", "gpt-5-codex"}
	if len(c.lastArgs) != len(want) {
		t.Fatalf("args: got %v, want %v", c.lastArgs, want)
	}
	for i, a := range want {
		if c.lastArgs[i] != a {
			t.Fatalf("args[%d]: got %q, want %q", i, c.lastArgs[i], a)
		}
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
