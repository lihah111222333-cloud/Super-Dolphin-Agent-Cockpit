package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/e2efixture"
	"go.uber.org/fx"
)

func TestPromptIntentE2EFixtureModuleDisabledByDefault(t *testing.T) {
	t.Setenv(e2efixture.FixturePathEnv, "")
	t.Setenv("DREAM_PROVIDER_ORDER", "")
	var providers []contract.DreamExecutorProvider
	if err := fx.New(
		promptIntentE2EFixtureModule(),
		fx.Invoke(func(in struct {
			fx.In
			Providers []contract.DreamExecutorProvider `group:"dream_executors"`
		}) {
			providers = in.Providers
		}),
	).Err(); err != nil {
		t.Fatalf("fx.New(disabled fixture module) error = %v", err)
	}
	if len(providers) != 0 {
		t.Fatalf("len(providers) = %d, want 0", len(providers))
	}
	if got := os.Getenv("DREAM_PROVIDER_ORDER"); got != "" {
		t.Fatalf("DREAM_PROVIDER_ORDER = %q, want empty when fixture is disabled", got)
	}
}

func TestPromptIntentE2EFixtureModuleEnabledProvidesProvider(t *testing.T) {
	path := writePromptIntentE2EFixture(t)
	t.Setenv(e2efixture.FixturePathEnv, path)
	t.Setenv("PROMPT_INTENT_E2E_FIXTURE_HARNESS", "1")
	t.Setenv("DREAM_PROVIDER_ORDER", "")
	var providers []contract.DreamExecutorProvider
	if err := fx.New(
		promptIntentE2EFixtureModule(),
		fx.Invoke(func(in struct {
			fx.In
			Providers []contract.DreamExecutorProvider `group:"dream_executors"`
		}) {
			providers = in.Providers
		}),
	).Err(); err != nil {
		t.Fatalf("fx.New(enabled fixture module) error = %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("len(providers) = %d, want 1", len(providers))
	}
	if providers[0].Name != e2efixture.ProviderName || providers[0].Executor == nil {
		t.Fatalf("provider = %#v, want e2e fixture provider", providers[0])
	}
	if got := os.Getenv("DREAM_PROVIDER_ORDER"); got != e2efixture.ProviderName {
		t.Fatalf("DREAM_PROVIDER_ORDER = %q, want %q", got, e2efixture.ProviderName)
	}
}

func TestPromptIntentE2EFixtureModuleRejectsProductionEnvFixture(t *testing.T) {
	path := writePromptIntentE2EFixture(t)
	t.Setenv(e2efixture.FixturePathEnv, path)
	t.Setenv("PROMPT_INTENT_E2E_FIXTURE_HARNESS", "")
	t.Setenv("DREAM_PROVIDER_ORDER", "")
	var providers []contract.DreamExecutorProvider
	err := fx.New(
		promptIntentE2EFixtureModule(),
		fx.Invoke(func(in struct {
			fx.In
			Providers []contract.DreamExecutorProvider `group:"dream_executors"`
		}) {
			providers = in.Providers
		}),
	).Err()
	if err == nil {
		t.Fatal("fx.New(production fixture env) succeeded, want fail-fast error")
	}
	if len(providers) != 0 {
		t.Fatalf("len(providers) = %d, want 0 when fixture harness is disabled", len(providers))
	}
	if got := os.Getenv("DREAM_PROVIDER_ORDER"); got != "" {
		t.Fatalf("DREAM_PROVIDER_ORDER = %q, want unchanged when fixture is rejected", got)
	}
}

func TestPromptIntentE2EFixtureModulePreservesExplicitDreamProviderOrder(t *testing.T) {
	path := writePromptIntentE2EFixture(t)
	t.Setenv(e2efixture.FixturePathEnv, path)
	t.Setenv("PROMPT_INTENT_E2E_FIXTURE_HARNESS", "1")
	t.Setenv("DREAM_PROVIDER_ORDER", "codex,claude")
	if err := fx.New(promptIntentE2EFixtureModule()).Err(); err != nil {
		t.Fatalf("fx.New(enabled fixture module) error = %v", err)
	}
	if got := os.Getenv("DREAM_PROVIDER_ORDER"); got != "codex,claude" {
		t.Fatalf("DREAM_PROVIDER_ORDER = %q, want explicit order preserved", got)
	}
}

func writePromptIntentE2EFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "prompt_intent_dream_fixture.json")
	body := `{
  "health": {"provider":"e2e-fixture"},
  "expert": {"kind":"expert"},
  "recall": {"kind":"recall"},
  "default_rule": {"kind":"default_rule"},
  "review": {"kind":"recall"},
  "block": {"kind":"expert"}
}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}
