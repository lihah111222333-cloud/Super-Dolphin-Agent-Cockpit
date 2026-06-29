package unified

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/pkg/dreammetrics"
)

type fakeDreamExecutor struct {
	name   string
	result string
	err    error
	sleep  time.Duration
	calls  *[]string
	mu     *sync.Mutex
}

func (f *fakeDreamExecutor) ExecuteDream(ctx context.Context, prompt string) (string, error) {
	_ = prompt
	if f.calls != nil && f.mu != nil {
		f.mu.Lock()
		*f.calls = append(*f.calls, f.name)
		f.mu.Unlock()
	}
	if f.sleep > 0 {
		select {
		case <-time.After(f.sleep):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return f.result, f.err
}

type fakePolicyDreamExecutor struct {
	result  string
	options []contract.DreamOptions
}

func (f *fakePolicyDreamExecutor) ExecuteDream(context.Context, string) (string, error) {
	return f.result, nil
}

func (f *fakePolicyDreamExecutor) ExecuteDreamWithOptions(_ context.Context, _ string, options contract.DreamOptions) (string, error) {
	f.options = append(f.options, options)
	return f.result, nil
}

// newDreamExecutorWithTimeout 构造 dispatcher 后用反射式断言注入 timeout，
// 仅供测试调小 dispatcher deadline。生产构造走 NewDreamExecutor + defaultDreamTimeout。
func newDreamExecutorWithTimeout(t *testing.T, providers []contract.DreamExecutorProvider, timeout time.Duration) contract.DreamExecutor {
	t.Helper()
	d := NewDreamExecutor(providers, newSilentLogger())
	impl, ok := d.(*dreamExecutor)
	if !ok {
		t.Fatalf("expected *dreamExecutor, got %T", d)
	}
	impl.timeout = timeout
	return impl
}

// newDreamExecutorWithMaxPromptBytes 仅供测试调小 prompt size cap。
func newDreamExecutorWithMaxPromptBytes(t *testing.T, providers []contract.DreamExecutorProvider, maxBytes int, logger *slog.Logger) contract.DreamExecutor {
	t.Helper()
	if logger == nil {
		logger = newSilentLogger()
	}
	d := NewDreamExecutor(providers, logger)
	impl, ok := d.(*dreamExecutor)
	if !ok {
		t.Fatalf("expected *dreamExecutor, got %T", d)
	}
	impl.maxPromptBytes = maxBytes
	return impl
}

func newSilentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestDreamExecutor_EmptyPromptReturnsError(t *testing.T) {
	d := NewDreamExecutor(nil, newSilentLogger())
	_, err := d.ExecuteDream(context.Background(), "   ")
	if err == nil || !strings.Contains(err.Error(), "dream prompt is empty") {
		t.Fatalf("expected empty prompt error, got %v", err)
	}
}

func TestDreamExecutor_CanceledContextReturnsCtxErr(t *testing.T) {
	d := NewDreamExecutor(nil, newSilentLogger())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := d.ExecuteDream(ctx, "hello")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestDreamExecutor_NoProvidersReturnsNotConfigured(t *testing.T) {
	d := NewDreamExecutor(nil, newSilentLogger())
	_, err := d.ExecuteDream(context.Background(), "hello")
	if !errors.Is(err, contract.ErrDreamExecutorNotConfigured) {
		t.Fatalf("expected ErrDreamExecutorNotConfigured, got %v", err)
	}
	if !strings.Contains(err.Error(), "no provider dream executors registered") {
		t.Fatalf("expected 'no provider' message, got %v", err)
	}
}

func TestDreamExecutor_SkipsBlankNameAndNilExecutor(t *testing.T) {
	calls := []string{}
	mu := &sync.Mutex{}
	providers := []contract.DreamExecutorProvider{
		{Name: "  ", Executor: &fakeDreamExecutor{name: "blank", calls: &calls, mu: mu}},
		{Name: "alpha", Executor: nil},
		{Name: "beta", Executor: &fakeDreamExecutor{name: "beta", result: "ok", calls: &calls, mu: mu}},
	}
	d := NewDreamExecutor(providers, newSilentLogger())
	got, err := d.ExecuteDream(context.Background(), "hello")
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if got != "ok" {
		t.Fatalf("expected 'ok', got %q", got)
	}
	if !equalStrings(calls, []string{"beta"}) {
		t.Fatalf("expected only beta called, got %v", calls)
	}
}

func TestDreamExecutor_SingleProviderSuccess(t *testing.T) {
	providers := []contract.DreamExecutorProvider{
		{Name: "claude", Executor: &fakeDreamExecutor{result: "consolidated"}},
	}
	d := NewDreamExecutor(providers, newSilentLogger())
	got, err := d.ExecuteDream(context.Background(), "p")
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if got != "consolidated" {
		t.Fatalf("expected 'consolidated', got %q", got)
	}
}

func TestDreamExecutor_AppliesStrictRuntimePolicy(t *testing.T) {
	provider := &fakePolicyDreamExecutor{result: "consolidated"}
	d := NewDreamExecutor([]contract.DreamExecutorProvider{{Name: "codex", Executor: provider}}, newSilentLogger())

	got, err := d.ExecuteDream(context.Background(), "p")
	if err != nil {
		t.Fatalf("ExecuteDream() error = %v", err)
	}
	if got != "consolidated" {
		t.Fatalf("ExecuteDream() = %q, want consolidated", got)
	}
	if len(provider.options) != 1 {
		t.Fatalf("recorded options = %d, want 1", len(provider.options))
	}
	if got := provider.options[0].RuntimePolicy; !got.ToolsDisabled || !got.ReadOnlySandbox || !got.MinEnv {
		t.Fatalf("RuntimePolicy = %+v, want strict dream policy", got)
	}
}

func TestDreamExecutor_SingleProviderNotConfiguredPropagates(t *testing.T) {
	notCfg := fmt.Errorf("%w: claude not configured", contract.ErrDreamExecutorNotConfigured)
	providers := []contract.DreamExecutorProvider{
		{Name: "claude", Executor: &fakeDreamExecutor{err: notCfg}},
	}
	d := NewDreamExecutor(providers, newSilentLogger())
	_, err := d.ExecuteDream(context.Background(), "p")
	if !errors.Is(err, contract.ErrDreamExecutorNotConfigured) {
		t.Fatalf("expected ErrDreamExecutorNotConfigured, got %v", err)
	}
	if !strings.Contains(err.Error(), "claude not configured") {
		t.Fatalf("expected propagated wrapper, got %v", err)
	}
}

func TestDreamExecutor_NonNotConfiguredErrorShortCircuits(t *testing.T) {
	calls := []string{}
	mu := &sync.Mutex{}
	boom := errors.New("provider boom")
	providers := []contract.DreamExecutorProvider{
		{Name: "claude", Executor: &fakeDreamExecutor{name: "claude", err: boom, calls: &calls, mu: mu}},
		{Name: "codex", Executor: &fakeDreamExecutor{name: "codex", result: "would not run", calls: &calls, mu: mu}},
	}
	d := NewDreamExecutor(providers, newSilentLogger())
	_, err := d.ExecuteDream(context.Background(), "p")
	if !errors.Is(err, boom) {
		t.Fatalf("expected boom error, got %v", err)
	}
	if !equalStrings(calls, []string{"claude"}) {
		t.Fatalf("expected only claude called, got %v", calls)
	}
}

func TestDreamExecutor_FirstWinsByAlphabeticalOrder(t *testing.T) {
	calls := []string{}
	mu := &sync.Mutex{}
	providers := []contract.DreamExecutorProvider{
		{Name: "codex", Executor: &fakeDreamExecutor{name: "codex", result: "codex-out", calls: &calls, mu: mu}},
		{Name: "claude", Executor: &fakeDreamExecutor{name: "claude", result: "claude-out", calls: &calls, mu: mu}},
	}
	d := NewDreamExecutor(providers, newSilentLogger())
	got, err := d.ExecuteDream(context.Background(), "p")
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if got != "claude-out" {
		t.Fatalf("expected claude (sorted first) wins, got %q", got)
	}
	if !equalStrings(calls, []string{"claude"}) {
		t.Fatalf("expected only claude called, got %v", calls)
	}
}

func TestDreamExecutor_FailoverFromNotConfiguredToNext(t *testing.T) {
	calls := []string{}
	mu := &sync.Mutex{}
	notCfg := fmt.Errorf("%w: claude not configured", contract.ErrDreamExecutorNotConfigured)
	providers := []contract.DreamExecutorProvider{
		{Name: "claude", Executor: &fakeDreamExecutor{name: "claude", err: notCfg, calls: &calls, mu: mu}},
		{Name: "codex", Executor: &fakeDreamExecutor{name: "codex", result: "codex-out", calls: &calls, mu: mu}},
	}
	d := NewDreamExecutor(providers, newSilentLogger())
	got, err := d.ExecuteDream(context.Background(), "p")
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if got != "codex-out" {
		t.Fatalf("expected codex-out, got %q", got)
	}
	if !equalStrings(calls, []string{"claude", "codex"}) {
		t.Fatalf("expected calls [claude codex], got %v", calls)
	}
}

func TestDreamExecutor_AllNotConfiguredReturnsLast(t *testing.T) {
	calls := []string{}
	mu := &sync.Mutex{}
	cliErr := fmt.Errorf("%w: claude not configured", contract.ErrDreamExecutorNotConfigured)
	codexErr := fmt.Errorf("%w: codex not configured", contract.ErrDreamExecutorNotConfigured)
	providers := []contract.DreamExecutorProvider{
		{Name: "claude", Executor: &fakeDreamExecutor{name: "claude", err: cliErr, calls: &calls, mu: mu}},
		{Name: "codex", Executor: &fakeDreamExecutor{name: "codex", err: codexErr, calls: &calls, mu: mu}},
	}
	d := NewDreamExecutor(providers, newSilentLogger())
	_, err := d.ExecuteDream(context.Background(), "p")
	if !errors.Is(err, contract.ErrDreamExecutorNotConfigured) {
		t.Fatalf("expected ErrDreamExecutorNotConfigured, got %v", err)
	}
	if !strings.Contains(err.Error(), "codex not configured") {
		t.Fatalf("expected last NotConfigured (codex) propagated, got %v", err)
	}
	if !equalStrings(calls, []string{"claude", "codex"}) {
		t.Fatalf("expected calls [claude codex], got %v", calls)
	}
}

func TestDreamExecutor_MidChainNonNotConfiguredShortCircuits(t *testing.T) {
	calls := []string{}
	mu := &sync.Mutex{}
	notCfg := fmt.Errorf("%w: claude", contract.ErrDreamExecutorNotConfigured)
	boom := errors.New("codex boom")
	providers := []contract.DreamExecutorProvider{
		{Name: "claude", Executor: &fakeDreamExecutor{name: "claude", err: notCfg, calls: &calls, mu: mu}},
		{Name: "codex", Executor: &fakeDreamExecutor{name: "codex", err: boom, calls: &calls, mu: mu}},
		{Name: "delta", Executor: &fakeDreamExecutor{name: "delta", result: "would-not-run", calls: &calls, mu: mu}},
	}
	d := NewDreamExecutor(providers, newSilentLogger())
	_, err := d.ExecuteDream(context.Background(), "p")
	if !errors.Is(err, boom) {
		t.Fatalf("expected boom, got %v", err)
	}
	if !equalStrings(calls, []string{"claude", "codex"}) {
		t.Fatalf("expected calls [claude codex] (delta skipped), got %v", calls)
	}
}

func TestDreamExecutor_DuplicateNameLastWinsOrderUnique(t *testing.T) {
	calls := []string{}
	mu := &sync.Mutex{}
	earlier := &fakeDreamExecutor{name: "claude-earlier", result: "earlier", calls: &calls, mu: mu}
	later := &fakeDreamExecutor{name: "claude-later", result: "later", calls: &calls, mu: mu}
	providers := []contract.DreamExecutorProvider{
		{Name: "claude", Executor: earlier},
		{Name: "claude", Executor: later},
	}
	d := NewDreamExecutor(providers, newSilentLogger())
	got, err := d.ExecuteDream(context.Background(), "p")
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if got != "later" {
		t.Fatalf("expected last registered to win, got %q", got)
	}
	if !equalStrings(calls, []string{"claude-later"}) {
		t.Fatalf("expected only claude-later called, got %v", calls)
	}
}

func TestDreamExecutor_NilLoggerFallsBackToDefault(t *testing.T) {
	d := NewDreamExecutor(nil, nil)
	_, err := d.ExecuteDream(context.Background(), "p")
	if !errors.Is(err, contract.ErrDreamExecutorNotConfigured) {
		t.Fatalf("expected ErrDreamExecutorNotConfigured, got %v", err)
	}
}

// newCapturingLogger returns an slog.Logger that writes Debug+ records into
// a buffer so tests can assert log invariants. Tests sharing the buffer must
// not run in parallel.
func newCapturingLogger() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	h := slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(h), buf
}

func assertLogContains(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Fatalf("expected log to contain %q, got:\n%s", w, out)
		}
	}
}

func TestDreamExecutor_DispatcherTimeoutCancelsLongRunningProvider(t *testing.T) {
	slow := &fakeDreamExecutor{sleep: 500 * time.Millisecond, result: "should-not-finish"}
	d := newDreamExecutorWithTimeout(t,
		[]contract.DreamExecutorProvider{{Name: "claude", Executor: slow}},
		30*time.Millisecond,
	)

	start := time.Now()
	_, err := d.ExecuteDream(context.Background(), "p")
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("expected dispatcher timeout ~30ms, took %v", elapsed)
	}
}

func TestDreamExecutor_RespectsCallerDeadlineWhenShorter(t *testing.T) {
	slow := &fakeDreamExecutor{sleep: 500 * time.Millisecond, result: "should-not-finish"}
	d := newDreamExecutorWithTimeout(t,
		[]contract.DreamExecutorProvider{{Name: "claude", Executor: slow}},
		200*time.Millisecond,
	)

	// 上层 ctx 已有更短 deadline，应该胜出（context.WithTimeout 取较近者）
	callerCtx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := d.ExecuteDream(callerCtx, "p")
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
	if elapsed > 150*time.Millisecond {
		t.Fatalf("expected caller deadline ~30ms to win, took %v", elapsed)
	}
}

func TestResolveProviderOrder(t *testing.T) {
	registered := []string{"claude", "codex"}
	cases := []struct {
		name     string
		override string
		want     []string
		wantErr  bool
	}{
		{name: "empty falls back to alphabetical", override: "", want: []string{"claude", "codex"}},
		{name: "whitespace-only falls back to alphabetical", override: "   ", want: []string{"claude", "codex"}},
		{name: "explicit reverses default", override: "codex,claude", want: []string{"codex", "claude"}},
		{name: "partial puts listed first then alphabetical", override: "codex", want: []string{"codex", "claude"}},
		{name: "unknown names rejected", override: "ghost,codex,phantom", wantErr: true},
		{name: "duplicates collapsed first-wins", override: "codex,claude,codex", want: []string{"codex", "claude"}},
		{name: "whitespace trimmed around tokens", override: "  codex  ,  claude  ", want: []string{"codex", "claude"}},
		{name: "empty tokens skipped", override: "codex,,,claude", want: []string{"codex", "claude"}},
		{name: "all unknown rejected", override: "ghost,phantom", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveProviderOrder(registered, tc.override)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("resolveProviderOrder(%q) error = nil, want unknown provider error", tc.override)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveProviderOrder(%q) error = %v", tc.override, err)
			}
			if !equalStrings(got, tc.want) {
				t.Fatalf("override=%q: got %v, want %v", tc.override, got, tc.want)
			}
		})
	}
}

func TestResolveProviderOrder_NewOrderDoesNotMutateInput(t *testing.T) {
	registered := []string{"claude", "codex"}
	orig := append([]string(nil), registered...)
	if _, err := resolveProviderOrder(registered, "codex,claude"); err != nil {
		t.Fatalf("resolveProviderOrder() error = %v", err)
	}
	if !equalStrings(registered, orig) {
		t.Fatalf("resolveProviderOrder mutated input: got %v, want %v", registered, orig)
	}
}

func TestDreamExecutor_EnvOverridesAlphabeticalOrder(t *testing.T) {
	t.Setenv(dreamProviderOrderEnv, "codex,claude")
	calls := []string{}
	mu := &sync.Mutex{}
	providers := []contract.DreamExecutorProvider{
		{Name: "claude", Executor: &fakeDreamExecutor{name: "claude", result: "claude-out", calls: &calls, mu: mu}},
		{Name: "codex", Executor: &fakeDreamExecutor{name: "codex", result: "codex-out", calls: &calls, mu: mu}},
	}
	d := NewDreamExecutor(providers, newSilentLogger())
	got, err := d.ExecuteDream(context.Background(), "p")
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if got != "codex-out" {
		t.Fatalf("expected env-ordered codex to win, got %q", got)
	}
	if !equalStrings(calls, []string{"codex"}) {
		t.Fatalf("expected only codex called (env order), got %v", calls)
	}
}

func TestDreamExecutor_PromptExceedsSizeLimit(t *testing.T) {
	calls := []string{}
	mu := &sync.Mutex{}
	providers := []contract.DreamExecutorProvider{
		{Name: "claude", Executor: &fakeDreamExecutor{name: "claude", result: "unreachable", calls: &calls, mu: mu}},
	}
	d := newDreamExecutorWithMaxPromptBytes(t, providers, 100, nil)
	oversized := strings.Repeat("x", 101)

	_, err := d.ExecuteDream(context.Background(), oversized)
	if err == nil || !strings.Contains(err.Error(), "dream prompt too large") {
		t.Fatalf("expected size cap error, got %v", err)
	}
	if !strings.Contains(err.Error(), "101") || !strings.Contains(err.Error(), "100") {
		t.Fatalf("expected error to include sizes (101/100), got %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("expected provider not called when size cap exceeded, got %v", calls)
	}
}

func TestDreamExecutor_PromptAtSizeLimitAccepts(t *testing.T) {
	providers := []contract.DreamExecutorProvider{
		{Name: "claude", Executor: &fakeDreamExecutor{result: "ok"}},
	}
	d := newDreamExecutorWithMaxPromptBytes(t, providers, 100, nil)
	atLimit := strings.Repeat("x", 100)

	got, err := d.ExecuteDream(context.Background(), atLimit)
	if err != nil {
		t.Fatalf("expected boundary value to pass, got %v", err)
	}
	if got != "ok" {
		t.Fatalf("expected 'ok', got %q", got)
	}
}

func TestDreamExecutor_ZeroMaxPromptBytesSkipsCheck(t *testing.T) {
	providers := []contract.DreamExecutorProvider{
		{Name: "claude", Executor: &fakeDreamExecutor{result: "ok"}},
	}
	d := newDreamExecutorWithMaxPromptBytes(t, providers, 0, nil)
	huge := strings.Repeat("x", 1024*1024) // 1MB 远超默认 256KB

	got, err := d.ExecuteDream(context.Background(), huge)
	if err != nil {
		t.Fatalf("expected size cap disabled to pass, got %v", err)
	}
	if got != "ok" {
		t.Fatalf("expected 'ok', got %q", got)
	}
}

func TestDreamExecutorLogsSuccessEvent(t *testing.T) {
	logger, buf := newCapturingLogger()
	providers := []contract.DreamExecutorProvider{
		{Name: "claude", Executor: &fakeDreamExecutor{result: "ok"}},
	}
	d := NewDreamExecutor(providers, logger)
	if _, err := d.ExecuteDream(context.Background(), "p"); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	assertLogContains(t, buf.String(),
		"level=INFO",
		"dream executor succeeded",
		"provider=claude",
		"size_bytes=2",
	)
}

func TestDreamExecutorLogsAllNotConfiguredEvent(t *testing.T) {
	logger, buf := newCapturingLogger()
	notCfg := fmt.Errorf("%w: claude", contract.ErrDreamExecutorNotConfigured)
	providers := []contract.DreamExecutorProvider{
		{Name: "claude", Executor: &fakeDreamExecutor{err: notCfg}},
	}
	d := NewDreamExecutor(providers, logger)
	if _, err := d.ExecuteDream(context.Background(), "p"); !errors.Is(err, contract.ErrDreamExecutorNotConfigured) {
		t.Fatalf("expected ErrDreamExecutorNotConfigured, got %v", err)
	}
	assertLogContains(t, buf.String(),
		"level=WARN",
		"all dream executors not configured",
	)
}

func TestDreamExecutorLogsProviderFailureEvent(t *testing.T) {
	logger, buf := newCapturingLogger()
	boom := errors.New("provider boom")
	providers := []contract.DreamExecutorProvider{
		{Name: "claude", Executor: &fakeDreamExecutor{err: boom}},
	}
	d := NewDreamExecutor(providers, logger)
	if _, err := d.ExecuteDream(context.Background(), "p"); !errors.Is(err, boom) {
		t.Fatalf("expected boom, got %v", err)
	}
	assertLogContains(t, buf.String(),
		"level=WARN",
		"dream executor failed",
		"provider=claude",
		"provider boom",
	)
}

func TestDreamExecutorLogsPromptSizeCapEvent(t *testing.T) {
	logger, buf := newCapturingLogger()
	providers := []contract.DreamExecutorProvider{
		{Name: "claude", Executor: &fakeDreamExecutor{result: "unreachable"}},
	}
	d := newDreamExecutorWithMaxPromptBytes(t, providers, 50, logger)
	oversized := strings.Repeat("x", 60)
	if _, err := d.ExecuteDream(context.Background(), oversized); err == nil {
		t.Fatalf("expected size cap error, got nil")
	}
	assertLogContains(t, buf.String(),
		"level=WARN",
		"dream prompt exceeds size limit",
		"size_bytes=60",
		"max_bytes=50",
	)
}

func TestDreamExecutorLogsEnvOverriddenOrderEvent(t *testing.T) {
	t.Setenv(dreamProviderOrderEnv, "codex,claude")
	logger, buf := newCapturingLogger()
	providers := []contract.DreamExecutorProvider{
		{Name: "claude", Executor: &fakeDreamExecutor{result: "unused"}},
		{Name: "codex", Executor: &fakeDreamExecutor{result: "unused"}},
	}
	_ = NewDreamExecutor(providers, logger)
	assertLogContains(t, buf.String(),
		"level=INFO",
		"dream provider order overridden",
		"env=codex,claude",
	)
}

func TestDreamExecutorMetricSuccessTotal(t *testing.T) {
	dreammetrics.ResetForTesting()
	t.Cleanup(dreammetrics.ResetForTesting)
	providers := []contract.DreamExecutorProvider{
		{Name: "claude", Executor: &fakeDreamExecutor{result: "ok"}},
	}
	d := NewDreamExecutor(providers, newSilentLogger())
	if _, err := d.ExecuteDream(context.Background(), "p"); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if got := dreammetrics.Success(); got != 1 {
		t.Errorf("SuccessTotal: got %d, want 1", got)
	}
}

func TestDreamExecutorMetricProviderSkippedTotal(t *testing.T) {
	dreammetrics.ResetForTesting()
	t.Cleanup(dreammetrics.ResetForTesting)
	notCfg := fmt.Errorf("%w: claude", contract.ErrDreamExecutorNotConfigured)
	providers := []contract.DreamExecutorProvider{
		{Name: "claude", Executor: &fakeDreamExecutor{err: notCfg}},
		{Name: "codex", Executor: &fakeDreamExecutor{result: "codex-ok"}},
	}
	d := NewDreamExecutor(providers, newSilentLogger())
	if _, err := d.ExecuteDream(context.Background(), "p"); err != nil {
		t.Fatalf("expected success after failover, got %v", err)
	}
	if got := dreammetrics.ProviderSkipped(); got != 1 {
		t.Errorf("ProviderSkippedTotal: got %d, want 1", got)
	}
	if got := dreammetrics.Success(); got != 1 {
		t.Errorf("SuccessTotal: got %d, want 1", got)
	}
}

func TestDreamExecutorMetricProviderFailedTotal(t *testing.T) {
	dreammetrics.ResetForTesting()
	t.Cleanup(dreammetrics.ResetForTesting)
	boom := errors.New("provider boom")
	providers := []contract.DreamExecutorProvider{
		{Name: "claude", Executor: &fakeDreamExecutor{err: boom}},
	}
	d := NewDreamExecutor(providers, newSilentLogger())
	if _, err := d.ExecuteDream(context.Background(), "p"); !errors.Is(err, boom) {
		t.Fatalf("expected boom, got %v", err)
	}
	if got := dreammetrics.ProviderFailed(); got != 1 {
		t.Errorf("ProviderFailedTotal: got %d, want 1", got)
	}
}

func TestDreamExecutorMetricAllNotConfiguredTotal(t *testing.T) {
	dreammetrics.ResetForTesting()
	t.Cleanup(dreammetrics.ResetForTesting)
	notCfg := fmt.Errorf("%w: claude", contract.ErrDreamExecutorNotConfigured)
	providers := []contract.DreamExecutorProvider{
		{Name: "claude", Executor: &fakeDreamExecutor{err: notCfg}},
	}
	d := NewDreamExecutor(providers, newSilentLogger())
	if _, err := d.ExecuteDream(context.Background(), "p"); !errors.Is(err, contract.ErrDreamExecutorNotConfigured) {
		t.Fatalf("expected ErrDreamExecutorNotConfigured, got %v", err)
	}
	if got := dreammetrics.AllNotConfigured(); got != 1 {
		t.Errorf("AllNotConfiguredTotal: got %d, want 1", got)
	}
	if got := dreammetrics.ProviderSkipped(); got != 1 {
		t.Errorf("ProviderSkippedTotal: got %d, want 1", got)
	}
}

func TestDreamExecutorMetricPromptOversizeTotal(t *testing.T) {
	dreammetrics.ResetForTesting()
	t.Cleanup(dreammetrics.ResetForTesting)
	providers := []contract.DreamExecutorProvider{
		{Name: "claude", Executor: &fakeDreamExecutor{result: "unreachable"}},
	}
	d := newDreamExecutorWithMaxPromptBytes(t, providers, 10, nil)
	oversized := strings.Repeat("x", 20)
	if _, err := d.ExecuteDream(context.Background(), oversized); err == nil {
		t.Fatalf("expected size cap error, got nil")
	}
	if got := dreammetrics.PromptOversize(); got != 1 {
		t.Errorf("PromptOversizeTotal: got %d, want 1", got)
	}
	if got := dreammetrics.Success(); got != 0 {
		t.Errorf("SuccessTotal should not increment when size cap fails fast: got %d", got)
	}
}
