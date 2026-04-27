package dreamexec

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ---------- realCommander 测试（依赖 sh / cat） ----------

func skipIfWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("realCommander 测试依赖 sh/cat，跳过 windows")
	}
}

func TestRealCommander_StdinEchoedToStdout(t *testing.T) {
	skipIfWindows(t)
	c := NewRealCommander()
	out, err := c.Run(context.Background(), "cat", nil, "hello dream", 1024)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if string(out) != "hello dream" {
		t.Fatalf("expected stdin echoed, got %q", string(out))
	}
}

func TestRealCommander_NonZeroExitCarriesStderr(t *testing.T) {
	skipIfWindows(t)
	c := NewRealCommander()
	_, err := c.Run(context.Background(), "sh", []string{"-c", "echo boom >&2; exit 7"}, "", 1024)
	if err == nil {
		t.Fatalf("expected non-zero exit error, got nil")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected stderr 'boom' in error, got %v", err)
	}
}

func TestRealCommander_ContextCancellationReturnsCtxErr(t *testing.T) {
	skipIfWindows(t)
	c := NewRealCommander()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	// sleep 远超 ctx timeout
	_, err := c.Run(ctx, "sh", []string{"-c", "sleep 5"}, "", 1024)
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("expected ctx cancel ~30ms, took %v", elapsed)
	}
}

func TestRealCommander_StdoutExceedsMaxReportsError(t *testing.T) {
	skipIfWindows(t)
	c := NewRealCommander()
	// 输出 200 字节，max=10
	_, err := c.Run(context.Background(), "sh", []string{"-c", "printf 'x%.0s' {1..200}"}, "", 10)
	if err == nil {
		t.Fatalf("expected stdout overflow error, got nil")
	}
	if !strings.Contains(err.Error(), "stdout exceeded") {
		t.Fatalf("expected 'stdout exceeded' message, got %v", err)
	}
}

func TestRealCommander_BinaryNotFoundReportsError(t *testing.T) {
	c := NewRealCommander()
	_, err := c.Run(context.Background(), "this-binary-does-not-exist-dream-test", nil, "", 1024)
	if err == nil {
		t.Fatalf("expected binary-not-found error, got nil")
	}
}

func TestRealCommander_EmptyBinaryRejected(t *testing.T) {
	c := NewRealCommander()
	_, err := c.Run(context.Background(), "  ", nil, "x", 1024)
	if err == nil || !strings.Contains(err.Error(), "binary is empty") {
		t.Fatalf("expected empty binary error, got %v", err)
	}
}

func TestRealCommander_NonPositiveMaxRejected(t *testing.T) {
	c := NewRealCommander()
	_, err := c.Run(context.Background(), "cat", nil, "x", 0)
	if err == nil || !strings.Contains(err.Error(), "maxStdoutBytes must be positive") {
		t.Fatalf("expected non-positive max error, got %v", err)
	}
}

// ---------- fakeCommander + Run 集成测试 ----------

type fakeCommander struct {
	outputs []string // 顺序返回，每次调用消耗一项
	errs    []error
	calls   int
}

func (f *fakeCommander) Run(ctx context.Context, binary string, args []string, input string, maxStdoutBytes int64) ([]byte, error) {
	idx := f.calls
	f.calls++
	if idx >= len(f.outputs) && idx >= len(f.errs) {
		return nil, errors.New("fakeCommander: no more responses configured")
	}
	if idx < len(f.errs) && f.errs[idx] != nil {
		return nil, f.errs[idx]
	}
	if idx < len(f.outputs) {
		return []byte(f.outputs[idx]), nil
	}
	return nil, nil
}

func TestRun_SuccessWithFenceAndProse(t *testing.T) {
	c := &fakeCommander{outputs: []string{"Sure here:\n```json\n{\"memories\":[{\"content\":\"x\"}]}\n```\n"}}
	got, err := Run(context.Background(), c, RunOptions{
		Binary:         "claude",
		Args:           []string{"-p"},
		Prompt:         "consolidate memory",
		MaxStdoutBytes: 4096,
		MaxRetries:     0,
	})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if !strings.Contains(got, `"memories"`) {
		t.Fatalf("expected JSON object with 'memories', got %q", got)
	}
	if c.calls != 1 {
		t.Fatalf("expected 1 commander call, got %d", c.calls)
	}
}

func TestRun_CommanderErrorTransparentNoRetry(t *testing.T) {
	boom := errors.New("commander boom")
	c := &fakeCommander{errs: []error{boom}, outputs: []string{"never used"}}
	_, err := Run(context.Background(), c, RunOptions{
		Binary:         "claude",
		Prompt:         "p",
		MaxStdoutBytes: 1024,
		MaxRetries:     2, // 即使 retry 配置 2，commander 错误也不 retry
	})
	if !errors.Is(err, boom) {
		t.Fatalf("expected boom, got %v", err)
	}
	if c.calls != 1 {
		t.Fatalf("expected exactly 1 commander call (no retry on cmd error), got %d", c.calls)
	}
}

func TestRun_JSONParseFailureRetries(t *testing.T) {
	// 第 1 次：垃圾输出（无 JSON），第 2 次：有效 JSON
	c := &fakeCommander{outputs: []string{"not json at all", `{"memories":[]}`}}
	got, err := Run(context.Background(), c, RunOptions{
		Binary:         "claude",
		Prompt:         "p",
		MaxStdoutBytes: 1024,
		MaxRetries:     1,
	})
	if err != nil {
		t.Fatalf("expected retry to succeed, got %v", err)
	}
	if got != `{"memories":[]}` {
		t.Fatalf("expected retry payload, got %q", got)
	}
	if c.calls != 2 {
		t.Fatalf("expected 2 commander calls (1 fail + 1 retry), got %d", c.calls)
	}
}

func TestRun_RetryExhaustedReturnsParseError(t *testing.T) {
	c := &fakeCommander{outputs: []string{"junk1", "junk2", "junk3"}}
	_, err := Run(context.Background(), c, RunOptions{
		Binary:         "claude",
		Prompt:         "p",
		MaxStdoutBytes: 1024,
		MaxRetries:     2,
	})
	if err == nil || !strings.Contains(err.Error(), "failed to extract JSON") {
		t.Fatalf("expected exhausted parse error, got %v", err)
	}
	if c.calls != 3 {
		t.Fatalf("expected 3 commander calls (1 + 2 retries), got %d", c.calls)
	}
}

func TestRun_RejectsInvalidOptions(t *testing.T) {
	c := &fakeCommander{}
	cases := []struct {
		name string
		opts RunOptions
		want string
	}{
		{"empty prompt", RunOptions{Binary: "x", MaxStdoutBytes: 1024}, "prompt is empty"},
		{"zero max bytes", RunOptions{Binary: "x", Prompt: "p", MaxStdoutBytes: 0}, "MaxStdoutBytes must be positive"},
		{"negative retries", RunOptions{Binary: "x", Prompt: "p", MaxStdoutBytes: 1024, MaxRetries: -1}, "MaxRetries must be non-negative"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Run(context.Background(), c, tc.opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q error, got %v", tc.want, err)
			}
		})
	}
}

func TestRun_NilCommanderRejected(t *testing.T) {
	_, err := Run(context.Background(), nil, RunOptions{Binary: "x", Prompt: "p", MaxStdoutBytes: 1024})
	if err == nil || !strings.Contains(err.Error(), "commander is nil") {
		t.Fatalf("expected nil commander error, got %v", err)
	}
}

func TestRun_ContextCanceledBeforeFirstCallReturnsCtxErr(t *testing.T) {
	c := &fakeCommander{outputs: []string{"never used"}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Run(ctx, c, RunOptions{Binary: "x", Prompt: "p", MaxStdoutBytes: 1024})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if c.calls != 0 {
		t.Fatalf("expected 0 calls when ctx canceled before run, got %d", c.calls)
	}
}
