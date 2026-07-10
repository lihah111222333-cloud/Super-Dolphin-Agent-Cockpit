package thread

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	promptpkg "github.com/anthropic-ai/super-agent-v3/internal/module/prompt"
	"github.com/anthropic-ai/super-agent-v3/internal/module/threadprompt"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
)

func TestStartAssemblyMergesBuiltinBaseAndUserRuntimeAssets(t *testing.T) {
	t.Setenv("PROMPT_START_CURRENT_DATE", "2026-05-22")

	store := newRuntimeChainPromptStore()
	catalog := threadprompt.NewRuntimeCatalog(store, runtimeChainBuiltinRegistry())
	promptAssembly := promptpkg.NewService(&promptpkg.Config{}, nil)
	if err := registerThreadPromptProviders(threadPromptProviderParams{
		Registrar:     promptAssembly,
		PromptCatalog: catalog,
	}); err != nil {
		t.Fatalf("registerThreadPromptProviders() error = %v", err)
	}

	sessions := &stubSessionProvider{}
	svc := newRuntimeChainService(catalog, promptAssembly, sessions, runtimeChainStarter(t, sessions))
	if _, err := svc.Start(context.Background(), StartRequest{
		AgentID:  "agent-runtime-chain",
		Provider: "codex",
		CWD:      resolvePromptCWD("/repo/a"),
		Prompt:   "帮我实现 SQLC 改动，并规划相关测试。",
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if store.lastInsertVersion.PromptKey != "main/default" {
		t.Fatalf("inserted prompt version key = %q, want main/default", store.lastInsertVersion.PromptKey)
	}
}

func runtimeChainBuiltinRegistry() *threadBuiltinPromptRegistry {
	return &threadBuiltinPromptRegistry{
		templates: []contract.BuiltinPromptTemplate{
			{ID: -700, PromptKey: "main/default", Title: "Builtin Main Default", AgentKey: "main", Enabled: true, Scope: "global", Priority: 200},
			{ID: -701, PromptKey: "main/expert/builtin", Title: "Builtin Expert", AgentKey: "main", WhenToUse: "Use builtin expert for runtime chain checks.", Tags: []string{"scope.global", "intent:expert"}, Enabled: true, Scope: "global", Priority: 180},
		},
		sections: map[int64][]contract.BuiltinPromptSection{
			-700: {{
				TemplateID: -700,
				SectionKey: "identity",
				Region:     "static",
				Ordinal:    0,
				Body:       "Builtin identity",
				Enabled:    true,
			}},
		},
	}
}

func newRuntimeChainPromptStore() *fakePromptStore {
	cwd := resolvePromptCWD("/repo/a")
	otherCWD := resolvePromptCWD("/repo/b")
	return &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			{ID: 10, PromptKey: "user/expert/sql", Title: "SQL Expert", AgentKey: "main", WhenToUse: "Use for SQL work.", Tags: runtimeChainJSONTags("scope.cwd:"+cwd, "intent:expert"), Enabled: true, Priority: 160},
			{ID: 11, PromptKey: "user/knowledge/sqlc", Title: "SQLC Knowledge", AgentKey: "main", WhenToUse: "Recall SQLC workflow.", Tags: runtimeChainJSONTags("scope.cwd:"+cwd, "intent:recall"), Enabled: true},
			{ID: 12, PromptKey: "user/default/rule", Title: "Project Rule", AgentKey: "default_rule", WhenToUse: "Project default.", Tags: runtimeChainJSONTags("scope.cwd:"+cwd, "intent:default_rule"), Enabled: true},
			{ID: 13, PromptKey: "user/expert/disabled", Title: "Disabled Expert", AgentKey: "main", WhenToUse: "Must stay hidden.", Tags: runtimeChainJSONTags("scope.cwd:"+cwd, "intent:expert"), Enabled: false},
		},
		recallSections: []promptstore.PromptTemplateSection{
			{TemplateID: 11, SectionKey: "recall_sqlc", TriggerType: "recall", RecallTopic: "sqlc-workflow", TemplateDescription: "Read source SQL first.", TemplateTags: runtimeChainJSONTags("scope.cwd:" + cwd), Enabled: true},
			{TemplateID: 11, SectionKey: "recall_other", TriggerType: "recall", RecallTopic: "other-repo-topic", TemplateDescription: "Other repo only.", TemplateTags: runtimeChainJSONTags("scope.cwd:" + otherCWD), Enabled: true},
			{TemplateID: 11, SectionKey: "recall_disabled", TriggerType: "recall", RecallTopic: "disabled-topic", TemplateDescription: "Disabled topic.", TemplateTags: runtimeChainJSONTags("scope.cwd:" + cwd), Enabled: false},
		},
		defaultRuleSections: []promptstore.PromptTemplateSection{
			{TemplateID: 12, SectionKey: "focused_tests", Body: "Always run focused tests before reporting done.", TemplateTags: runtimeChainJSONTags("scope.cwd:" + cwd), Enabled: true},
			{TemplateID: 12, SectionKey: "global_rule", Body: "Global runtime rule.", TemplateTags: runtimeChainJSONTags("scope.global"), Enabled: true},
			{TemplateID: 12, SectionKey: "other_repo_rule", Body: "Other repo rule.", TemplateTags: runtimeChainJSONTags("scope.cwd:" + otherCWD), Enabled: true},
			{TemplateID: 12, SectionKey: "disabled_rule", Body: "Disabled rule.", TemplateTags: runtimeChainJSONTags("scope.cwd:" + cwd), Enabled: false},
		},
	}
}

func runtimeChainStarter(t *testing.T, sessions *stubSessionProvider) *startOnlySessionStarter {
	t.Helper()
	return &startOnlySessionStarter{onStart: func(_ context.Context, req dto.StartSessionRequest) (contract.Session, error) {
		assertRuntimeChainStartAssembly(t, req)
		session := &stubSession{threadID: "019d5f6b-fb3c-7760-9d6f-54005553f609"}
		sessions.session = session
		return session, nil
	}}
}

func assertRuntimeChainStartAssembly(t *testing.T, req dto.StartSessionRequest) {
	t.Helper()
	if _, ok := req.StartAssembly.Snapshot.SectionSnapshot[contract.DynamicSectionRecallCatalog]; !ok {
		t.Fatalf("SectionSnapshot missing %q: %#v", contract.DynamicSectionRecallCatalog, req.StartAssembly.Snapshot.SectionSnapshot)
	}
	body := strings.Join([]string{
		req.Instructions,
		req.StartAssembly.BaseInstructions,
		req.StartAssembly.UserContext["runtimeExtras"],
		req.StartAssembly.UserContextText,
		req.StartAssembly.Snapshot.BaseInstructions,
		req.StartAssembly.Snapshot.SectionSnapshot[contract.DynamicSectionAvailableExperts],
		req.StartAssembly.Snapshot.SectionSnapshot[contract.DynamicSectionRecallCatalog],
		req.StartAssembly.Snapshot.SectionSnapshot[contract.DynamicSectionProjectDefaultRules],
	}, "\n")
	for _, want := range runtimeChainExpectedSubstrings() {
		if !strings.Contains(body, want) {
			t.Fatalf("start assembly body missing %q:\n%s", want, body)
		}
	}
	for _, absent := range runtimeChainHiddenSubstrings() {
		if strings.Contains(body, absent) {
			t.Fatalf("start assembly body contains hidden asset %q:\n%s", absent, body)
		}
	}
}

func runtimeChainExpectedSubstrings() []string {
	return []string{"Builtin identity", "可用专家", "main/expert/builtin", "user/expert/sql", "可回忆知识目录", "sqlc-workflow", "Always run focused tests", "Global runtime rule."}
}

func runtimeChainHiddenSubstrings() []string {
	return []string{"user/expert/disabled", "other-repo-topic", "disabled-topic", "Other repo rule.", "Disabled rule."}
}

func newRuntimeChainService(
	catalog promptstore.RuntimePromptCatalog,
	promptAssembly contract.PromptAssemblyService,
	sessions *stubSessionProvider,
	starter contract.SessionStarter,
) *service {
	return NewServiceWithPromptAssemblyAndSharedFiles(
		silentLogger(),
		&stubThreadStore{},
		nil,
		sessions,
		starter,
		nil,
		&stubThreadOrchestration{},
		nil,
		promptAssembly,
		testThreadDependencyConfig(),
		nil,
		nil,
		catalog,
		promptpkg.EvaluateMatchWhen,
		promptpkg.EvaluateEnableWhen,
	).(*service)
}

func runtimeChainJSONTags(tags ...string) json.RawMessage {
	raw, _ := json.Marshal(tags)
	return raw
}
