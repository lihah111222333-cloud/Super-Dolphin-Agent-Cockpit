package unified

import (
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestDreamProviderOrderRejectsUnknownProvider(t *testing.T) {
	t.Setenv(dreamProviderOrderEnv, "ghost,codex")
	executor, err := provideDreamExecutor(dreamExecutorParams{
		Providers: []contract.DreamExecutorProvider{
			{Name: "claude", Executor: &fakeDreamExecutor{name: "claude", result: "claude-out"}},
			{Name: "codex", Executor: &fakeDreamExecutor{name: "codex", result: "codex-out"}},
		},
		Logger: newSilentLogger(),
	})

	if err == nil {
		t.Fatal("provideDreamExecutor() error = nil, want startup config error")
	}
	if executor != nil {
		t.Fatalf("provideDreamExecutor() executor = %#v, want nil on config error", executor)
	}
	if !strings.Contains(err.Error(), `unknown DREAM_PROVIDER_ORDER provider "ghost"`) {
		t.Fatalf("provideDreamExecutor() error = %v, want unknown provider error", err)
	}
}
