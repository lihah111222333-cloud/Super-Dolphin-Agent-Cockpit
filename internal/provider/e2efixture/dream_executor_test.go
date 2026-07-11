package e2efixture

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"go.uber.org/fx"
)

func TestFixtureProviderHealthReturnsE2EFixture(t *testing.T) {
	path := writeFixture(t, defaultFixtureJSON())
	executor, err := newDreamExecutor(path)
	if err != nil {
		t.Fatalf("newDreamExecutor() error = %v", err)
	}
	raw, err := executor.ExecuteDream(context.Background(), "Super-Dolphin prompt intent e2e health: strict JSON only.")
	if err != nil {
		t.Fatalf("ExecuteDream(health) error = %v", err)
	}
	var got struct {
		Provider        string `json:"provider"`
		FixturePathHash string `json:"fixture_path_hash"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("health JSON = %s: %v", raw, err)
	}
	if got.Provider != ProviderName {
		t.Fatalf("provider = %q, want %q", got.Provider, ProviderName)
	}
	if len(got.FixturePathHash) != pathHashMaxChars {
		t.Fatalf("fixture_path_hash = %q, want %d chars", got.FixturePathHash, pathHashMaxChars)
	}
}

func TestFixtureProviderSelectsStableCards(t *testing.T) {
	path := writeFixture(t, defaultFixtureJSON())
	executor, err := newDreamExecutor(path)
	if err != nil {
		t.Fatalf("newDreamExecutor() error = %v", err)
	}
	cases := []struct {
		name       string
		prompt     string
		wantMarker string
	}{
		{"expert", "requested_kind: expert\nuser_input:\nmake an sqlc expert", `"kind":"expert"`},
		{"recall", "requested_kind: recall\nuser_input:\nmake recall", `"recall_topic":"prompt-intent-e2e-fixture"`},
		{"default_rule", "requested_kind: default_rule\nuser_input:\nmake rule", `"kind":"default_rule"`},
		{"review", "requested_kind: recall\nuser_input:\nfixture:review", "external provider prompt"},
		{"block", "requested_kind: expert\nuser_input:\nfixture:block", `"hit_examples":[]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := executor.ExecuteDream(context.Background(), tc.prompt)
			if err != nil {
				t.Fatalf("ExecuteDream() error = %v", err)
			}
			if !strings.Contains(raw, tc.wantMarker) {
				t.Fatalf("ExecuteDream() = %s, want marker %q", raw, tc.wantMarker)
			}
		})
	}
}

func TestFixtureProviderRendersRunIDPlaceholders(t *testing.T) {
	path := writeFixture(t, strings.ReplaceAll(defaultFixtureJSON(), "prompt-intent-e2e-fixture", "prompt-intent-e2e-{{RUN_ID}}"))
	executor, err := newDreamExecutor(path)
	if err != nil {
		t.Fatalf("newDreamExecutor() error = %v", err)
	}

	raw, err := executor.ExecuteDream(context.Background(), "requested_kind: recall\nuser_input:\nmake recall\nrun_id: r7a9c2d1")
	if err != nil {
		t.Fatalf("ExecuteDream() error = %v", err)
	}
	if !strings.Contains(raw, `"recall_topic":"prompt-intent-e2e-r7a9c2d1"`) {
		t.Fatalf("ExecuteDream() = %s, want rendered run id", raw)
	}
}

func TestFixtureProviderFailsFastWhenRunIDPlaceholderHasNoRunID(t *testing.T) {
	path := writeFixture(t, strings.ReplaceAll(defaultFixtureJSON(), "prompt-intent-e2e-fixture", "prompt-intent-e2e-{{RUN_ID}}"))
	executor, err := newDreamExecutor(path)
	if err != nil {
		t.Fatalf("newDreamExecutor() error = %v", err)
	}

	_, err = executor.ExecuteDream(context.Background(), "requested_kind: recall\nuser_input:\nmake recall")
	if err == nil || !strings.Contains(err.Error(), "requires run_id") {
		t.Fatalf("ExecuteDream() error = %v, want requires run_id", err)
	}
}

func TestFixtureProviderFailsFastWithoutFixturePath(t *testing.T) {
	_, err := newDreamExecutor("")
	if err == nil || !strings.Contains(err.Error(), "fixture path is required") {
		t.Fatalf("newDreamExecutor(empty) error = %v, want fixture path error", err)
	}
}

func TestFixtureProviderFailsFastWhenRequiredKeyMissing(t *testing.T) {
	path := writeFixture(t, `{"health":{},"expert":{},"recall":{},"default_rule":{},"review":{}}`)
	_, err := newDreamExecutor(path)
	if err == nil || !strings.Contains(err.Error(), `missing key "block"`) {
		t.Fatalf("newDreamExecutor(missing block) error = %v", err)
	}
}

func TestFixtureModuleProvidesDreamExecutorProvider(t *testing.T) {
	path := writeFixture(t, defaultFixtureJSON())
	t.Setenv(FixturePathEnv, path)
	var providers []contract.DreamExecutorProvider
	if err := fx.New(
		Module,
		fx.Invoke(func(in struct {
			fx.In
			Providers []contract.DreamExecutorProvider `group:"dream_executors"`
		}) {
			providers = in.Providers
		}),
	).Err(); err != nil {
		t.Fatalf("fx.New(Module) error = %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("len(providers) = %d, want 1", len(providers))
	}
	if providers[0].Name != ProviderName || providers[0].Executor == nil {
		t.Fatalf("provider = %#v, want fixture provider with executor", providers[0])
	}
}

func writeFixture(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func defaultFixtureJSON() string {
	return `{
  "health": {"provider":"e2e-fixture"},
  "expert": {"kind":"expert","title":"Prompt Intent E2E Expert","summary":"Reviews prompt intent E2E flows.","when_to_use":"Use for prompt intent end-to-end fixture validation.","when_not_to_use":"Do not use for unrelated UI work.","workflow":["Inspect the draft","Check committed prompt sections"],"constraints":["Keep fixture output deterministic"],"output":"A concise validation report.","hit_examples":["Validate prompt intent fixture flow"],"miss_examples":["Tune unrelated frontend styling"]},
  "recall": {"kind":"recall","title":"Prompt Intent E2E Recall","summary":"Fixture recall package for prompt intent E2E.","recall_topic":"prompt-intent-e2e-fixture","recall_body":"PROMPT_INTENT_RECALL_BODY_MARKER fixture recall body.","hit_examples":["Look up prompt intent E2E recall marker"],"miss_examples":["Ask for a weather forecast"]},
  "default_rule": {"kind":"default_rule","title":"Prompt Intent E2E Rule","summary":"Project-only prompt intent E2E rule.","default_rule_body":"Always keep prompt intent fixture outputs deterministic.","hit_examples":["Create a deterministic prompt intent fixture"],"miss_examples":["Use a live LLM without explicit real mode"],"conflicting_rules":[{"title":"Existing live mode rule","summary":"May prefer live model output"}],"suggested_alternative":{"kind":"recall","reason":"Keep provider-specific notes in recall instead of a default rule."}},
  "review": {"kind":"recall","title":"External Provider Notes","summary":"Reference notes from an external provider prompt.","recall_topic":"external-provider-notes","recall_body":"This external provider prompt says You are Claude and mentions provider tools.","hit_examples":["Look up external provider prompt notes"],"miss_examples":["Enable a project default behavior"]},
  "block": {"kind":"expert","title":"Blocked Fixture Expert","summary":"Missing examples by design.","when_to_use":"Use only to verify block issues.","workflow":["Return incomplete card"],"output":"Blocked draft.","hit_examples":[],"miss_examples":["Normal ready draft"]}
}`
}
