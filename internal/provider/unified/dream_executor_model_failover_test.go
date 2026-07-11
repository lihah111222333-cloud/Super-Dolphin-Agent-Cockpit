package unified

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestDreamExecutor_FailoverFromProviderModelUnavailableToNext(t *testing.T) {
	calls := []string{}
	mu := &sync.Mutex{}
	claudeModelErr := fmt.Errorf("%w: claude model unavailable", contract.ErrDreamExecutorNotConfigured)
	providers := []contract.DreamExecutorProvider{
		{Name: "claude", Executor: &fakeDreamExecutor{name: "claude", err: claudeModelErr, calls: &calls, mu: mu}},
		{Name: "codex", Executor: &fakeDreamExecutor{name: "codex", result: "codex-out", calls: &calls, mu: mu}},
	}
	d := NewDreamExecutor(providers, newSilentLogger())
	got, err := d.ExecuteDream(context.Background(), "p")
	if err != nil {
		t.Fatalf("expected success after model-unavailable failover, got %v", err)
	}
	if got != "codex-out" {
		t.Fatalf("expected codex-out, got %q", got)
	}
	if !equalStrings(calls, []string{"claude", "codex"}) {
		t.Fatalf("expected calls [claude codex], got %v", calls)
	}
}
