package unified

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

type fakeOptionsDreamExecutor struct {
	name    string
	result  string
	calls   *[]string
	options *[]contract.DreamOptions
	mu      *sync.Mutex
}

func (f *fakeOptionsDreamExecutor) ExecuteDream(context.Context, string) (string, error) {
	return f.result, nil
}

func (f *fakeOptionsDreamExecutor) ExecuteDreamWithOptions(_ context.Context, _ string, options contract.DreamOptions) (string, error) {
	if f.calls != nil && f.mu != nil {
		f.mu.Lock()
		*f.calls = append(*f.calls, f.name)
		f.mu.Unlock()
	}
	if f.options != nil && f.mu != nil {
		f.mu.Lock()
		*f.options = append(*f.options, options)
		f.mu.Unlock()
	}
	return f.result, nil
}

func TestDreamExecutor_ExecuteDreamWithOptionsUsesRequestedProviderOnly(t *testing.T) {
	calls := []string{}
	options := []contract.DreamOptions{}
	mu := &sync.Mutex{}
	providers := []contract.DreamExecutorProvider{
		{Name: "claude", Executor: &fakeOptionsDreamExecutor{name: "claude", result: "claude-out", calls: &calls, mu: mu}},
		{Name: "codex", Executor: &fakeOptionsDreamExecutor{name: "codex", result: "codex-out", calls: &calls, options: &options, mu: mu}},
	}
	d := NewDreamExecutor(providers, newSilentLogger())
	withOptions, ok := d.(contract.DreamExecutorWithOptions)
	if !ok {
		t.Fatalf("dream executor should support request-scoped options")
	}

	got, err := withOptions.ExecuteDreamWithOptions(context.Background(), "p", contract.DreamOptions{
		Provider: " Codex ",
		Model:    "gpt-5.5",
	})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if got != "codex-out" {
		t.Fatalf("expected codex-out, got %q", got)
	}
	if !equalStrings(calls, []string{"codex"}) {
		t.Fatalf("expected only requested codex provider called, got %v", calls)
	}
	if len(options) != 1 || options[0].Provider != " Codex " || options[0].Model != "gpt-5.5" {
		t.Fatalf("requested options not forwarded to provider: %#v", options)
	}
}

func TestDreamExecutor_ExecuteDreamWithOptionsRejectsUnknownProvider(t *testing.T) {
	calls := []string{}
	mu := &sync.Mutex{}
	providers := []contract.DreamExecutorProvider{
		{Name: "claude", Executor: &fakeOptionsDreamExecutor{name: "claude", result: "claude-out", calls: &calls, mu: mu}},
	}
	d := NewDreamExecutor(providers, newSilentLogger())
	withOptions, ok := d.(contract.DreamExecutorWithOptions)
	if !ok {
		t.Fatalf("dream executor should support request-scoped options")
	}

	_, err := withOptions.ExecuteDreamWithOptions(context.Background(), "p", contract.DreamOptions{Provider: "openrouter"})
	if err == nil || !strings.Contains(err.Error(), `provider "openrouter"`) {
		t.Fatalf("expected unknown provider error, got %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("unknown provider should not call registered executors, got %v", calls)
	}
}
