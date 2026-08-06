package threadprompt

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

func TestAvailableExpertsProviderFailsFastWithoutStore(t *testing.T) {
	t.Parallel()

	provider := newAvailableExpertsProvider(nil)
	if text, err := provider.Resolve(context.Background(), contract.SectionContext{
		Start: &contract.StartInput{Prompt: "帮我写测试"},
	}); err == nil || text != nil || !contract.IsCriticalPromptSectionError(err) ||
		!strings.Contains(err.Error(), "runtime prompt catalog is required") {
		t.Fatalf("Resolve() without store = (%v, %v), want critical error", text, err)
	}
}

func TestNewAvailableExpertsProviderOwnsRenderPolicy(t *testing.T) {
	first := newAvailableExpertsProvider(nil)
	second := newAvailableExpertsProvider(nil)

	first.policy.fullKeywords[0] = "mutated"
	first.policy.splitKeywords[0] = "mutated"
	first.policy.delegationTargetKeywords[0] = "mutated"

	if second.policy.fullKeywords[0] == "mutated" {
		t.Fatal("full keywords are shared between providers")
	}
	if second.policy.splitKeywords[0] == "mutated" {
		t.Fatal("split keywords are shared between providers")
	}
	if second.policy.delegationTargetKeywords[0] == "mutated" {
		t.Fatal("delegation target keywords are shared between providers")
	}
}

func TestAvailableExpertsProviderReturnsNilWithoutPrompt(t *testing.T) {
	t.Parallel()

	provider := newAvailableExpertsProvider(newRuntimeCatalog(&fakePromptStore{}, nil))
	if text, err := provider.Resolve(context.Background(), contract.SectionContext{}); err != nil || text != nil {
		t.Fatalf("Resolve() without user prompt = (%v, %v), want nil, nil", text, err)
	}
}

func TestAvailableExpertsProviderRendersShortListSortedAndScoped(t *testing.T) {
	t.Parallel()

	store := &fakePromptStore{
		templates: []PromptTemplate{
			expertTemplate("low/priority", 1, "低优先级任务"),
			expertTemplate("main/default", 100, "当前模板不应出现"),
			{PromptKey: "disabled/expert", Priority: 99, WhenToUse: "禁用模板", Enabled: false},
			{PromptKey: "empty/when", Priority: 98, WhenToUse: "  ", Enabled: true},
			expertTemplate("high/priority", 50, "高优先级任务"),
		},
	}
	provider := newAvailableExpertsProvider(newRuntimeCatalog(store, nil))

	text, err := provider.Resolve(context.Background(), contract.SectionContext{
		Start:    &contract.StartInput{Prompt: "你好", PromptKey: "main/default"},
		BuildCtx: contract.BuildCtx{CWD: "/repo-a"},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text == nil {
		t.Fatal("Resolve() = nil, want expert list")
	}
	requireContainsInOrder(t, *text, "high/priority", "low/priority")
	if !strings.Contains(*text, "prompt_key='high/priority'") {
		t.Fatalf("Resolve() = %q, want explicit prompt_key instruction", *text)
	}
	for _, absent := range []string{"main/default", "disabled/expert", "empty/when", "可用专家详细说明"} {
		if strings.Contains(*text, absent) {
			t.Fatalf("Resolve() = %q, want %q absent", *text, absent)
		}
	}
	if len(store.listFilters) != 1 || store.listFilters[0].Limit != 200 || store.listFilters[0].CWD != "/repo-a" {
		t.Fatalf("List() filter = %#v, want limit=200 cwd=/repo-a", store.listFilters)
	}
}

func TestAvailableExpertsProviderPrefersProjectTemplateOverGlobalTemplate(t *testing.T) {
	t.Parallel()

	store := &fakePromptStore{
		templates: []PromptTemplate{
			{
				PromptKey: "main/expert/sql-global",
				Title:     "SQL Expert",
				WhenToUse: "Global SQL guidance.",
				Enabled:   true,
				Tags:      mustJSONTags("scope.global", "intent:expert"),
			},
			{
				PromptKey: "main/expert/sql-project",
				Title:     "SQL Expert",
				WhenToUse: "Project SQL guidance.",
				Enabled:   true,
				Tags:      mustJSONTags("scope.cwd:/repo-a", "intent:expert"),
			},
		},
	}
	provider := newAvailableExpertsProvider(newRuntimeCatalog(store, nil))

	text, err := provider.Resolve(context.Background(), contract.SectionContext{
		Start:    &contract.StartInput{Prompt: "帮我看 SQL"},
		BuildCtx: contract.BuildCtx{CWD: "/repo-a"},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text == nil {
		t.Fatal("Resolve() = nil, want expert list")
	}
	if !strings.Contains(*text, "main/expert/sql-project") || strings.Contains(*text, "main/expert/sql-global") {
		t.Fatalf("Resolve() = %q, want project expert only", *text)
	}
}

func TestAvailableExpertsProviderDoesNotRenderFullForOrdinaryMultiTaskPrompt(t *testing.T) {
	t.Parallel()

	provider := newAvailableExpertsProvider(newRuntimeCatalog(&fakePromptStore{
		templates: []PromptTemplate{
			expertTemplate("coder/prompt", 20, "代码任务、bug 修复、测试编写"),
			expertTemplate("main/sql", 10, "数据库 schema 设计、migration、复杂 SQL 查询"),
		},
	}, nil))

	text, err := provider.Resolve(context.Background(), contract.SectionContext{
		Turn:     &contract.TurnInput{UserText: "加 SQL 查询以及 Vue 组件"},
		BuildCtx: contract.BuildCtx{CWD: "/repo-a"},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text == nil {
		t.Fatal("Resolve() = nil, want short expert list")
	}
	if !strings.Contains(*text, "可用专家（通过 launch_agent 调用）：") {
		t.Fatalf("Resolve() = %q, want short expert list", *text)
	}
	if strings.Contains(*text, "可用专家详细说明：") {
		t.Fatalf("Resolve() = %q, want short list only for ordinary multitask prompt", *text)
	}
}

func TestAvailableExpertsProviderRendersFullForExplicitDelegationPrompt(t *testing.T) {
	t.Parallel()

	provider := newAvailableExpertsProvider(newRuntimeCatalog(&fakePromptStore{
		templates: []PromptTemplate{
			expertTemplate("coder/prompt", 20, "代码任务、bug 修复、测试编写"),
			expertTemplate("main/sql", 10, "数据库 schema 设计、migration、复杂 SQL 查询"),
		},
	}, nil))

	text, err := provider.Resolve(context.Background(), contract.SectionContext{
		Turn:     &contract.TurnInput{UserText: "请拆分给多个专家并行处理 SQL 查询和 Vue 组件"},
		BuildCtx: contract.BuildCtx{CWD: "/repo-a"},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text == nil {
		t.Fatal("Resolve() = nil, want full expert list")
	}
	for _, want := range []string{
		"可用专家（通过 launch_agent 调用）：",
		"可用专家详细说明：",
		"调用：launch_agent(name='coder/prompt', prompt_key='coder/prompt'",
		"调用：launch_agent(name='main/sql', prompt_key='main/sql'",
		"判断准则：",
	} {
		if !strings.Contains(*text, want) {
			t.Fatalf("Resolve() = %q, want substring %q", *text, want)
		}
	}
}

func TestAvailableExpertsProviderAndCacheDependencyUseSameCWDResolver(t *testing.T) {
	t.Parallel()

	store := &fakePromptStore{templates: []PromptTemplate{expertTemplate("coder/prompt", 20, "代码任务")}}
	provider := newAvailableExpertsProvider(newRuntimeCatalog(store, nil))
	input := contract.SectionContext{
		Start: &contract.StartInput{Prompt: "帮我写测试", CWD: "/repo/start"},
	}
	if got := availableExpertsCWD(input); got != "/repo/start" {
		t.Fatalf("availableExpertsCWD() = %q, want /repo/start", got)
	}
	text, err := provider.Resolve(context.Background(), input)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text == nil {
		t.Fatal("Resolve() = nil, want expert list")
	}
	if len(store.listFilters) != 1 || store.listFilters[0].CWD != "/repo/start" {
		t.Fatalf("List() filter = %#v, want cwd=/repo/start", store.listFilters)
	}
}

func TestAvailableExpertsProviderMissingCWDFailsCritical(t *testing.T) {
	t.Parallel()

	store := &fakePromptStore{templates: []PromptTemplate{expertTemplate("coder/prompt", 20, "代码任务")}}
	provider := newAvailableExpertsProvider(newRuntimeCatalog(store, nil))
	text, err := provider.Resolve(context.Background(), contract.SectionContext{
		Start: &contract.StartInput{Prompt: "帮我写测试"},
	})
	if err == nil || text != nil || !contract.IsCriticalPromptSectionError(err) {
		t.Fatalf("Resolve() missing cwd = (%v, %v), want critical error", text, err)
	}
	if len(store.listFilters) != 0 {
		t.Fatalf("List() filters = %#v, want no query without trusted cwd", store.listFilters)
	}
}

func TestAvailableExpertsProviderExcludesCurrentPromptOnTurn(t *testing.T) {
	t.Parallel()

	provider := newAvailableExpertsProvider(newRuntimeCatalog(&fakePromptStore{
		templates: []PromptTemplate{
			expertTemplate("coder/prompt", 20, "代码任务、bug 修复、测试编写"),
			expertTemplate("main/sql", 10, "数据库 schema 设计、migration、复杂 SQL 查询"),
		},
	}, nil))

	text, err := provider.Resolve(context.Background(), contract.SectionContext{
		Turn: &contract.TurnInput{UserText: "继续写测试", PromptKey: "coder/prompt", CWD: "/repo/a"},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text == nil {
		t.Fatal("Resolve() = nil, want remaining expert")
	}
	if strings.Contains(*text, "coder/prompt") {
		t.Fatalf("Resolve() = %q, want current turn prompt excluded", *text)
	}
	if !strings.Contains(*text, "main/sql") {
		t.Fatalf("Resolve() = %q, want other experts retained", *text)
	}
}

func TestAvailableExpertsProviderDoesNotRenderFullForSingleCharacterTriggers(t *testing.T) {
	t.Parallel()

	provider := newAvailableExpertsProvider(newRuntimeCatalog(&fakePromptStore{
		templates: []PromptTemplate{
			expertTemplate("coder/prompt", 20, "代码任务、bug 修复、测试编写"),
		},
	}, nil))

	text, err := provider.Resolve(context.Background(), contract.SectionContext{
		Turn: &contract.TurnInput{UserText: "修 bug 和测试，顺手吗？", CWD: "/repo/a"},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text == nil {
		t.Fatal("Resolve() = nil, want short expert list")
	}
	if strings.Contains(*text, "可用专家详细说明：") {
		t.Fatalf("Resolve() = %q, want short list only for single-character/punctuation triggers", *text)
	}
}

func TestAvailableExpertsProviderReturnsNilWhenNoUsableExperts(t *testing.T) {
	t.Parallel()

	provider := newAvailableExpertsProvider(newRuntimeCatalog(&fakePromptStore{
		templates: []PromptTemplate{
			{PromptKey: "empty/when", Enabled: true, WhenToUse: ""},
			{PromptKey: "disabled/expert", Enabled: false, WhenToUse: "禁用"},
		},
	}, nil))
	if text, err := provider.Resolve(context.Background(), contract.SectionContext{
		Start: &contract.StartInput{Prompt: "帮我写测试", CWD: "/repo/a"},
	}); err != nil || text != nil {
		t.Fatalf("Resolve() unusable experts = (%v, %v), want nil, nil", text, err)
	}
}

func TestAvailableExpertsProviderFailsFastWhenListFails(t *testing.T) {
	t.Parallel()

	provider := newAvailableExpertsProvider(newRuntimeCatalog(&fakePromptStore{listErr: errors.New("db down")}, nil))
	if text, err := provider.Resolve(context.Background(), contract.SectionContext{
		Start: &contract.StartInput{Prompt: "帮我写测试", CWD: "/repo/a"},
	}); err == nil || text != nil || !contract.IsCriticalPromptSectionError(err) {
		t.Fatalf("Resolve() list failure = (%v, %v), want critical error", text, err)
	}
}

func TestAvailableExpertsExcludesRecallAndDefaultRuleAssets(t *testing.T) {
	t.Parallel()

	templates := []PromptTemplate{
		{PromptKey: "main/expert", AgentKey: "main", WhenToUse: "Use for expert work.", Enabled: true},
		{PromptKey: "main/knowledge/sqlc", AgentKey: "main", WhenToUse: "Knowledge asset.", Tags: mustJSONTags("intent:recall"), Enabled: true},
		{PromptKey: "main/default-rule/scope", AgentKey: "default_rule", WhenToUse: "Project rule.", Tags: mustJSONTags("intent:default_rule"), Enabled: true},
	}
	got := availableExpertsFromTemplates(templates, "")
	if len(got) != 1 || got[0].PromptKey != "main/expert" {
		t.Fatalf("availableExpertsFromTemplates() = %#v, want only main/expert", got)
	}
}

func TestRecallCatalogProviderFailsFastWithoutStore(t *testing.T) {
	t.Parallel()

	provider := RecallCatalogProvider{}
	if text, err := provider.Resolve(context.Background(), contract.SectionContext{}); err == nil || text != nil ||
		!contract.IsCriticalPromptSectionError(err) || !strings.Contains(err.Error(), "runtime prompt catalog is required") {
		t.Fatalf("Resolve() without store = (%v, %v), want critical error", text, err)
	}
}

func TestRecallCatalogProviderReturnsNilOnEmpty(t *testing.T) {
	t.Parallel()

	provider := RecallCatalogProvider{catalog: newRuntimeCatalog(&fakePromptStore{}, nil)}
	if text, err := provider.Resolve(context.Background(), contract.SectionContext{BuildCtx: contract.BuildCtx{CWD: "/repo/a"}}); err != nil || text != nil {
		t.Fatalf("Resolve() empty catalog = (%v, %v), want nil, nil", text, err)
	}
}

func TestRecallCatalogProviderFailsFastWhenListFails(t *testing.T) {
	t.Parallel()

	provider := RecallCatalogProvider{catalog: newRuntimeCatalog(&fakePromptStore{recallErr: errors.New("db down")}, nil)}
	if text, err := provider.Resolve(context.Background(), contract.SectionContext{BuildCtx: contract.BuildCtx{CWD: "/repo/a"}}); err == nil || text != nil || !contract.IsCriticalPromptSectionError(err) {
		t.Fatalf("Resolve() list failure = (%v, %v), want critical error", text, err)
	}
}

func TestRecallCatalogProviderMissingCWDFailsCritical(t *testing.T) {
	t.Parallel()

	store := &fakePromptStore{}
	provider := RecallCatalogProvider{catalog: newRuntimeCatalog(store, nil)}
	text, err := provider.Resolve(context.Background(), contract.SectionContext{})
	if err == nil || text != nil || !contract.IsCriticalPromptSectionError(err) {
		t.Fatalf("Resolve() missing cwd = (%v, %v), want critical error", text, err)
	}
	if len(store.recallCWDs) != 0 {
		t.Fatalf("ListRecallSections() cwd calls = %#v, want none", store.recallCWDs)
	}
}

func TestRecallCatalogProviderFiltersByCWD(t *testing.T) {
	t.Parallel()

	store := &fakePromptStore{
		recallSectionsByCWD: map[string][]PromptTemplateSection{
			"/repo/a": {{RecallTopic: "repo-a", TemplateDescription: "Repo A only.", TriggerType: "recall", Enabled: true}},
			"/repo/b": {{RecallTopic: "repo-b", TemplateDescription: "Repo B only.", TriggerType: "recall", Enabled: true}},
		},
	}
	provider := RecallCatalogProvider{catalog: newRuntimeCatalog(store, nil)}
	text, err := provider.Resolve(context.Background(), contract.SectionContext{BuildCtx: contract.BuildCtx{CWD: "/repo/a"}})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text == nil {
		t.Fatal("Resolve() = nil, want catalog")
	}
	if !strings.Contains(*text, `topic="repo-a"`) || strings.Contains(*text, `topic="repo-b"`) {
		t.Fatalf("Resolve() = %q, want only repo-a topic", *text)
	}
	if len(store.recallCWDs) != 1 || store.recallCWDs[0] != "/repo/a" {
		t.Fatalf("ListRecallSections() cwd calls = %#v, want /repo/a", store.recallCWDs)
	}
}

func TestRecallCatalogProviderRendersTopicCatalog(t *testing.T) {
	t.Parallel()

	const bodyMarker = "PROMPT_INTENT_RECALL_BODY_MARKER"
	provider := RecallCatalogProvider{catalog: newRuntimeCatalog(&fakePromptStore{
		recallSections: []PromptTemplateSection{
			{
				RecallTopic:         "sqlc-workflow",
				Body:                bodyMarker + " SQLC workflow body must stay behind prompt_recall.",
				TemplateDescription: "Knowledge material: SQLC 变更先改 sql/queries 并生成代码。第二句不应进入摘要。",
				TemplateWhenToUse:   "Use when editing SQLC queries.",
				TemplateTitle:       "SQLC workflow",
				Enabled:             true,
			},
			{RecallTopic: "guard-rules", Body: bodyMarker, TemplateWhenToUse: "Guard 失败必须先定位根因，再决定是否扩大修复面。"},
			{RecallTopic: "title-only", Body: bodyMarker, TemplateTitle: "标题兜底摘要。"},
			{RecallTopic: "topic-only", Body: bodyMarker},
			{RecallTopic: "  ", TemplateDescription: "missing topic"},
		},
	}, nil)}

	text, err := provider.Resolve(context.Background(), contract.SectionContext{BuildCtx: contract.BuildCtx{CWD: "/repo/a"}})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text == nil {
		t.Fatal("Resolve() = nil, want catalog")
	}
	requireContainsInOrder(t, *text, `topic="guard-rules"`, `topic="sqlc-workflow"`)
	for _, want := range []string{
		"可回忆知识目录：",
		`topic="guard-rules" — Guard 失败必须先定位根因，再决定是否扩大修复面。`,
		`topic="sqlc-workflow" — SQLC 变更先改 sql/queries 并生成代码。`,
		`topic="title-only" — 标题兜底摘要。`,
		`topic="topic-only" — topic-only`,
		`prompt_recall(topic="<topic>")`,
		"判断准则：",
	} {
		if !strings.Contains(*text, want) {
			t.Fatalf("Resolve() = %q, want substring %q", *text, want)
		}
	}
	for _, absent := range []string{bodyMarker, "Knowledge material:", "第二句不应进入摘要", "missing topic"} {
		if strings.Contains(*text, absent) {
			t.Fatalf("Resolve() = %q, want %q absent", *text, absent)
		}
	}
}

func TestRecallCatalogProviderPrefersProjectTopicOverGlobalTopic(t *testing.T) {
	t.Parallel()

	provider := RecallCatalogProvider{catalog: newRuntimeCatalog(&fakePromptStore{
		recallSections: []PromptTemplateSection{
			{
				RecallTopic:         "sqlc-workflow",
				TemplateDescription: "Global SQLC workflow.",
				TemplateTags:        mustJSONTags("scope.global"),
				Enabled:             true,
			},
			{
				RecallTopic:         "sqlc-workflow",
				TemplateDescription: "Project SQLC workflow.",
				TemplateTags:        mustJSONTags("scope.cwd:/repo-a"),
				Enabled:             true,
			},
		},
	}, nil)}

	text, err := provider.Resolve(context.Background(), contract.SectionContext{BuildCtx: contract.BuildCtx{CWD: "/repo-a"}})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text == nil {
		t.Fatal("Resolve() = nil, want catalog")
	}
	if !strings.Contains(*text, "Project SQLC workflow.") || strings.Contains(*text, "Global SQLC workflow.") {
		t.Fatalf("Resolve() = %q, want project topic only", *text)
	}
}

func TestPromptDynamicProvidersLogResolveMetrics(t *testing.T) {
	var logs bytes.Buffer
	previous := pkglogger.Get()
	pkglogger.SetForTest(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { pkglogger.SetForTest(previous) })

	available := newAvailableExpertsProvider(newRuntimeCatalog(&fakePromptStore{
		templates: []PromptTemplate{
			expertTemplate("coder/prompt", 20, "代码任务、bug 修复、测试编写"),
			expertTemplate("main/sql", 10, "数据库 schema 设计、migration、复杂 SQL 查询"),
		},
	}, nil))
	if text, err := available.Resolve(context.Background(), contract.SectionContext{
		Start: &contract.StartInput{Prompt: "写 SQL 和测试", CWD: "/repo/a"},
	}); err != nil || text == nil {
		t.Fatalf("AvailableExpertsProvider.Resolve() = (%v, %v), want rendered text", text, err)
	}
	availableLogs := logs.String()
	for _, want := range []string{
		`"msg":"thread: dynamic section resolved"`,
		`"section":"available_experts"`,
		`"rendered":true`,
		`"template_count":2`,
		`"candidate_count":2`,
		`"render_mode":"short"`,
		`"body_len":`,
		`"latency_ms":`,
	} {
		if !strings.Contains(availableLogs, want) {
			t.Fatalf("available_experts logs missing %s in:\n%s", want, availableLogs)
		}
	}

	logs.Reset()
	recall := RecallCatalogProvider{catalog: newRuntimeCatalog(&fakePromptStore{
		recallSections: []PromptTemplateSection{
			{RecallTopic: "sqlc-workflow", TemplateDescription: "SQLC 变更先改 sql/queries 并生成代码。", Enabled: true},
		},
	}, nil)}
	if text, err := recall.Resolve(context.Background(), contract.SectionContext{BuildCtx: contract.BuildCtx{CWD: "/repo/a"}}); err != nil || text == nil {
		t.Fatalf("RecallCatalogProvider.Resolve() = (%v, %v), want rendered text", text, err)
	}
	recallLogs := logs.String()
	for _, want := range []string{
		`"msg":"thread: dynamic section resolved"`,
		`"section":"recall_catalog"`,
		`"rendered":true`,
		`"topic_count":1`,
		`"body_len":`,
		`"latency_ms":`,
	} {
		if !strings.Contains(recallLogs, want) {
			t.Fatalf("recall_catalog logs missing %s in:\n%s", want, recallLogs)
		}
	}
}

func TestRegisterProvidersRegistersProjectDefaultRulesAvailableExpertsAndRecallCatalog(t *testing.T) {
	t.Parallel()

	registrar := &capturingDynamicRegistrar{}
	if err := RegisterProviders(registrar, NewRuntimeCatalog(&fakePromptStore{}, nil)); err != nil {
		t.Fatalf("RegisterProviders() error = %v", err)
	}
	if len(registrar.names) != 3 ||
		registrar.names[0] != contract.DynamicSectionProjectDefaultRules ||
		registrar.names[1] != contract.DynamicSectionAvailableExperts ||
		registrar.names[2] != contract.DynamicSectionRecallCatalog {
		t.Fatalf("registered names = %#v, want project_default_rules, available_experts, recall_catalog", registrar.names)
	}
}

func TestRegisterProvidersRequiresRuntimePromptCatalog(t *testing.T) {
	t.Parallel()

	err := RegisterProviders(&capturingDynamicRegistrar{}, nil)
	if err == nil || !strings.Contains(err.Error(), "runtime prompt catalog is required") {
		t.Fatalf("RegisterProviders() error = %v, want runtime prompt catalog required", err)
	}
}
