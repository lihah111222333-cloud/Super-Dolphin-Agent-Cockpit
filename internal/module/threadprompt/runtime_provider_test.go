package threadprompt

import (
	"context"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestAvailableExpertsProviderRendersBuiltinAndUserExpertsOnly(t *testing.T) {
	t.Parallel()

	provider := newAvailableExpertsProvider(newRuntimeCatalog(
		runtimeProviderUserStore(),
		runtimeProviderBuiltinRegistry(),
	))
	text, err := provider.Resolve(context.Background(), contract.SectionContext{
		Start:    &contract.StartInput{Prompt: "帮我做 SQL 和测试"},
		BuildCtx: contract.BuildCtx{CWD: "/repo/a"},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text == nil {
		t.Fatal("Resolve() = nil, want available experts")
	}
	assertRuntimeProviderExpertVisibility(t, *text)
}

func runtimeProviderBuiltinRegistry() *fakeBuiltinPromptRegistry {
	return &fakeBuiltinPromptRegistry{templates: []contract.BuiltinPromptTemplate{
		{ID: -701, PromptKey: "main/expert/builtin", Title: "Builtin Expert", AgentKey: "main", WhenToUse: "Use builtin expert.", Tags: []string{"scope.global", "intent:expert"}, Enabled: true, Scope: "global", Priority: 180},
		{ID: -702, PromptKey: "main/knowledge/builtin", Title: "Builtin Knowledge", AgentKey: "main", WhenToUse: "Builtin recall asset.", Tags: []string{"scope.global", "intent:recall"}, Enabled: true, Scope: "global"},
		{ID: -703, PromptKey: "main/default-rule/builtin", Title: "Builtin Rule", AgentKey: "default_rule", WhenToUse: "Builtin default rule.", Tags: []string{"scope.global", "intent:default_rule"}, Enabled: true, Scope: "global"},
	}}
}

func runtimeProviderUserStore() *fakePromptStore {
	return &fakePromptStore{templates: []PromptTemplate{
		{PromptKey: "user/expert/sql", Title: "SQL Expert", AgentKey: "main", WhenToUse: "Use for SQL work.", Tags: mustJSONTags("scope.cwd:/repo/a", "intent:expert"), Enabled: true, Priority: 160},
		{PromptKey: "user/knowledge/sqlc", Title: "SQLC Knowledge", AgentKey: "main", WhenToUse: "Recall SQLC workflow.", Tags: mustJSONTags("scope.cwd:/repo/a", "intent:recall"), Enabled: true},
		{PromptKey: "user/default/rule", Title: "Project Rule", AgentKey: "default_rule", WhenToUse: "Project default.", Tags: mustJSONTags("scope.cwd:/repo/a", "intent:default_rule"), Enabled: true},
		{PromptKey: "user/expert/disabled", Title: "Disabled Expert", AgentKey: "main", WhenToUse: "Must stay hidden.", Tags: mustJSONTags("scope.cwd:/repo/a", "intent:expert"), Enabled: false},
	}}
}

func assertRuntimeProviderExpertVisibility(t *testing.T, text string) {
	t.Helper()
	for _, want := range []string{"main/expert/builtin", "user/expert/sql"} {
		if !strings.Contains(text, want) {
			t.Fatalf("Resolve() = %q, want substring %q", text, want)
		}
	}
	for _, absent := range []string{"main/knowledge/builtin", "main/default-rule/builtin", "user/knowledge/sqlc", "user/default/rule", "user/expert/disabled"} {
		if strings.Contains(text, absent) {
			t.Fatalf("Resolve() = %q, want %q absent", text, absent)
		}
	}
}
