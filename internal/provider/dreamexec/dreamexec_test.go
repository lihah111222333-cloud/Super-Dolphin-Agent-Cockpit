package dreamexec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// ---------- realCommander 测试（依赖 sh / cat） ----------

func skipIfWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("realCommander 测试依赖 sh/cat，跳过 windows")
	}
}

func TestRealCommander_ScrubsDatabaseEnvFromParent(t *testing.T) {
	if os.Getenv("DREAMEXEC_ENV_HELPER") == "1" {
		writeDreamExecEnvHelperFile()
		os.Exit(0)
	}

	setDreamExecDatabaseEnvForTest(t)
	t.Setenv("DREAMEXEC_SAFE_PARENT_ENV", "keep-parent")
	t.Setenv("DREAMEXEC_ENV_HELPER", "1")
	envPath := filepath.Join(t.TempDir(), "dream-env.txt")
	t.Setenv("DREAMEXEC_ENV_FILE", envPath)

	c := NewRealCommander()
	_, err := c.Run(context.Background(), os.Args[0], []string{"-test.run=^TestRealCommander_ScrubsDatabaseEnvFromParent$"}, "", 1024)
	if err != nil {
		t.Fatalf("realCommander.Run() error = %v", err)
	}

	env := readDreamExecEnvFile(t, envPath)
	for _, key := range contract.ForbiddenDatabaseEnvKeyNames() {
		requireDreamExecEnvKeyAbsent(t, env, key)
	}
	requireDreamExecEnvValue(t, env, "DREAMEXEC_SAFE_PARENT_ENV", "keep-parent")
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

func TestRealCommander_NonZeroExitWithModelUnavailableEnvelope(t *testing.T) {
	skipIfWindows(t)
	c := NewRealCommander()
	script := `printf '%s' '{"type":"result","is_error":true,"api_error_status":404,"result":"There'\''s an issue with the selected model (gpt-5.5). It may not exist or you may not have access to it."}'; exit 1`
	_, err := c.Run(context.Background(), "sh", []string{"-c", script}, "", 4096)
	if !errors.Is(err, ErrModelUnavailable) {
		t.Fatalf("expected ErrModelUnavailable, got %v", err)
	}
}

func TestRealCommander_NonZeroExitWithModelUnavailableStderr(t *testing.T) {
	skipIfWindows(t)
	c := NewRealCommander()
	_, err := c.Run(context.Background(), "sh", []string{"-c", "echo 'model not found: gpt-5.5' >&2; exit 1"}, "", 4096)
	if !errors.Is(err, ErrModelUnavailable) {
		t.Fatalf("expected ErrModelUnavailable, got %v", err)
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

func TestRealCommander_BinaryNotFoundReportsErrBinaryNotAvailable(t *testing.T) {
	c := NewRealCommander()
	_, err := c.Run(context.Background(), "this-binary-does-not-exist-dream-test", nil, "", 1024)
	if err == nil {
		t.Fatalf("expected binary-not-found error, got nil")
	}
	if !errors.Is(err, ErrBinaryNotAvailable) {
		t.Fatalf("expected ErrBinaryNotAvailable (PATH lookup), got %v", err)
	}
}

func TestRealCommander_AbsolutePathNotExistReportsErrBinaryNotAvailable(t *testing.T) {
	skipIfWindows(t)
	c := NewRealCommander()
	_, err := c.Run(context.Background(), "/nonexistent/dream/binary/path", nil, "", 1024)
	if err == nil {
		t.Fatalf("expected binary-not-found error, got nil")
	}
	if !errors.Is(err, ErrBinaryNotAvailable) {
		t.Fatalf("expected ErrBinaryNotAvailable (absolute path missing), got %v", err)
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

// ---------- 自动 dispatch + OnUsage 集成测试 ----------

// claudeEnvelopeIntegrationFixture 是带 ```json fence 包裹的 result 字段。路径：
// envelope -> ExtractClaudeEnvelope -> result text -> StripJSONFences -> ExtractFirstJSONObject。
const claudeEnvelopeIntegrationFixture = `{"type":"result","is_error":false,"result":"` +
	"```json\\n{\\\"memories\\\":[{\\\"content\\\":\\\"x\\\"}]}\\n```" +
	`","usage":{"input_tokens":6,"cache_creation_input_tokens":7409,"cache_read_input_tokens":16153,"output_tokens":8}}`

func TestRun_AutoDispatchClaudeEnvelopeAndOnUsage(t *testing.T) {
	var captured TokenUsage
	c := &fakeCommander{outputs: []string{claudeEnvelopeIntegrationFixture}}
	got, err := Run(context.Background(), c, RunOptions{
		Binary: "claude", Prompt: "p", MaxStdoutBytes: 4096,
		OnUsage: func(u TokenUsage) { captured = u },
	})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if !strings.Contains(got, `"memories"`) {
		t.Fatalf("expected JSON object with 'memories', got %q", got)
	}
	// 6 + 7409 + 16153 = 23568
	if captured.InputTokens != 23568 || captured.OutputTokens != 8 || captured.CacheReadTokens != 16153 {
		t.Fatalf("OnUsage capture mismatch: %+v", captured)
	}
}

func TestRun_AutoDispatchCodexJSONLAndOnUsage(t *testing.T) {
	jsonl := "{\"type\":\"thread.started\"}\n" +
		"{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"{\\\"memories\\\":[{\\\"content\\\":\\\"y\\\"}]}\"}}\n" +
		"{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":11920,\"cached_input_tokens\":5504,\"output_tokens\":92}}\n"
	var captured TokenUsage
	c := &fakeCommander{outputs: []string{jsonl}}
	got, err := Run(context.Background(), c, RunOptions{
		Binary: "codex", Prompt: "p", MaxStdoutBytes: 4096,
		OnUsage: func(u TokenUsage) { captured = u },
	})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if !strings.Contains(got, `"memories"`) {
		t.Fatalf("expected JSON object with 'memories', got %q", got)
	}
	if captured.InputTokens != 11920 || captured.OutputTokens != 92 || captured.CacheReadTokens != 5504 {
		t.Fatalf("OnUsage capture mismatch: %+v", captured)
	}
}

func TestRun_OnUsageNotCalledForPlainTextFallback(t *testing.T) {
	// 既不像 envelope 也不像 JSONL，走 fallback。OnUsage 不该被调用。
	called := false
	c := &fakeCommander{outputs: []string{`{"memories":[{"content":"plain"}]}`}}
	got, err := Run(context.Background(), c, RunOptions{
		Binary: "any", Prompt: "p", MaxStdoutBytes: 1024,
		OnUsage: func(TokenUsage) { called = true },
	})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if !strings.Contains(got, `"memories"`) {
		t.Fatalf("expected fallback to extract JSON, got %q", got)
	}
	if called {
		t.Errorf("OnUsage should not be called for plain-text fallback")
	}
}

func TestRun_AutoDispatchClaudeEnvelopeIsErrorRetries(t *testing.T) {
	// is_error=true 的 envelope 走 structuredErr 分支，触发 retry。
	bad := `{"type":"result","is_error":true,"result":""}`
	good := `{"type":"result","is_error":false,"result":"{\"memories\":[]}","usage":{"input_tokens":1,"output_tokens":2}}`
	c := &fakeCommander{outputs: []string{bad, good}}
	got, err := Run(context.Background(), c, RunOptions{
		Binary: "claude", Prompt: "p", MaxStdoutBytes: 1024, MaxRetries: 1,
	})
	if err != nil {
		t.Fatalf("expected retry to succeed, got %v", err)
	}
	if got != `{"memories":[]}` {
		t.Fatalf("got %q", got)
	}
	if c.calls != 2 {
		t.Fatalf("expected 2 calls (1 fail + 1 retry), got %d", c.calls)
	}
}

func TestLooksLikeClaudeEnvelope(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"real envelope", `{"type":"result","is_error":false,"result":"x"}`, true},
		{"only type=result", `{"type":"result"}`, true},
		{"only is_error key", `{"is_error":false}`, true},
		{"only result key", `{"result":"x"}`, true},
		{"plain memories JSON", `{"memories":[]}`, false},
		{"jsonl line", `{"type":"thread.started"}`, false},
		{"not JSON", `hello`, false},
		{"empty", ``, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := looksLikeClaudeEnvelope([]byte(tc.in)); got != tc.want {
				t.Errorf("got %v, want %v for %q", got, tc.want, tc.in)
			}
		})
	}
}

func TestLooksLikeCodexJSONL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"thread.started line", `{"type":"thread.started"}`, true},
		{"item.completed line", `{"type":"item.completed","item":{"type":"agent_message","text":"x"}}`, true},
		{"turn.completed only", `{"type":"turn.completed"}`, true},
		{"with stdin prefix", "Reading prompt from stdin...\n{\"type\":\"thread.started\"}\n", true},
		{"plain memories JSON", `{"memories":[]}`, false},
		{"claude envelope", `{"type":"result","is_error":false,"result":"x"}`, false},
		{"non JSON", `not json`, false},
		{"empty", ``, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := looksLikeCodexJSONL([]byte(tc.in)); got != tc.want {
				t.Errorf("got %v, want %v for %q", got, tc.want, tc.in)
			}
		})
	}
}

func setDreamExecDatabaseEnvForTest(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://parent@localhost/super_dolphin")
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://compat@localhost/super_dolphin")
	t.Setenv("SUPER_DOLPHIN_SQLITE_PATH", filepath.Join(t.TempDir(), "parent.db"))
	t.Setenv("SUPER_DOLPHIN_INTERNAL_SQLITE_PATH", filepath.Join(t.TempDir(), "parent-internal.db"))
}

func writeDreamExecEnvHelperFile() {
	path := strings.TrimSpace(os.Getenv("DREAMEXEC_ENV_FILE"))
	if path == "" {
		os.Exit(2)
	}
	if err := os.WriteFile(path, []byte(strings.Join(os.Environ(), "\n")), 0o600); err != nil {
		os.Exit(2)
	}
}

func readDreamExecEnvFile(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read dream env file %s: %v", path, err)
	}
	text := strings.TrimSpace(strings.ReplaceAll(string(raw), "\r\n", "\n"))
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func requireDreamExecEnvKeyAbsent(t *testing.T, env []string, key string) {
	t.Helper()
	if value, ok := dreamExecEnvValue(env, key); ok {
		t.Fatalf("%s leaked with value %q in env %#v", key, value, env)
	}
}

func requireDreamExecEnvValue(t *testing.T, env []string, key, want string) {
	t.Helper()
	got, ok := dreamExecEnvValue(env, key)
	if !ok {
		t.Fatalf("%s missing from env %#v", key, env)
	}
	if got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}

func dreamExecEnvValue(env []string, key string) (string, bool) {
	for _, item := range env {
		name, value, ok := strings.Cut(item, "=")
		if ok && strings.EqualFold(name, key) {
			return strings.TrimSpace(value), true
		}
	}
	return "", false
}
