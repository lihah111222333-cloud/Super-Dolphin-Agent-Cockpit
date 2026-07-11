package codexapp_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/dreamexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/unified"
)

type modelFailoverFakeExecutor struct {
	name   string
	result string
	err    error
	calls  *[]string
	mu     *sync.Mutex
}

func (f *modelFailoverFakeExecutor) ExecuteDream(context.Context, string) (string, error) {
	if f.calls != nil && f.mu != nil {
		f.mu.Lock()
		*f.calls = append(*f.calls, f.name)
		f.mu.Unlock()
	}
	return f.result, f.err
}

func TestDreamExecutorProviderNeutralFallbackAfterModelUnavailable(t *testing.T) {
	calls := []string{}
	mu := &sync.Mutex{}
	modelErr := fmt.Errorf("%w: claude model unavailable: %w", contract.ErrDreamExecutorNotConfigured, dreamexec.ErrModelUnavailable)
	providers := []contract.DreamExecutorProvider{
		{Name: "claude", Executor: &modelFailoverFakeExecutor{name: "claude", err: modelErr, calls: &calls, mu: mu}},
		{Name: "codex", Executor: &modelFailoverFakeExecutor{name: "codex", result: `{"description":"当你需要调试 MCP Server 或接入 MCP 工具资源时使用。"}`, calls: &calls, mu: mu}},
	}
	dispatcher := unified.NewDreamExecutor(providers, nil)
	got, err := dispatcher.ExecuteDream(context.Background(), "generate skill description")
	if err != nil {
		t.Fatalf("expected provider-neutral fallback to succeed, got %v", err)
	}
	if !strings.Contains(got, `"description"`) {
		t.Fatalf("expected description JSON from fallback provider, got %q", got)
	}
	if !equalStringSlices(calls, []string{"claude", "codex"}) {
		t.Fatalf("expected calls [claude codex], got %v", calls)
	}
}

func TestDreamExecutorProviderNeutralFallbackStillStopsOnRealProviderError(t *testing.T) {
	calls := []string{}
	mu := &sync.Mutex{}
	boom := errors.New("provider rate limit")
	providers := []contract.DreamExecutorProvider{
		{Name: "claude", Executor: &modelFailoverFakeExecutor{name: "claude", err: boom, calls: &calls, mu: mu}},
		{Name: "codex", Executor: &modelFailoverFakeExecutor{name: "codex", result: `{"description":"unused"}`, calls: &calls, mu: mu}},
	}
	dispatcher := unified.NewDreamExecutor(providers, nil)
	_, err := dispatcher.ExecuteDream(context.Background(), "generate skill description")
	if !errors.Is(err, boom) {
		t.Fatalf("expected real provider error to stop fallback, got %v", err)
	}
	if !equalStringSlices(calls, []string{"claude"}) {
		t.Fatalf("expected only claude called, got %v", calls)
	}
}

func equalStringSlices(a, b []string) bool {
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
