package prompt

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	"github.com/stretchr/testify/require"
)

func TestPromptWriteRejectsGlobalSeedWithoutCurrentCWD(t *testing.T) {
	t.Parallel()

	const promptKey = "main/global-seed"
	store := newInMemoryPromptStore()
	template := scopedPromptTemplate(promptKey, "")
	template.Tags = json.RawMessage(`["scope.global"]`)
	store.templates[promptKey] = template
	svc := newPromptService(store)

	_, err := svc.WritePrompt(context.Background(), "/repo/a", PromptWriteRequest{
		ID:           promptKey,
		Name:         "Global Seed",
		Content:      "updated",
		WhenToUse:    "Use for edits.",
		WhenToUseSet: true,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside cwd scope")
	require.Zero(t, store.upsertCalls)
	require.Equal(t, json.RawMessage(`["scope.global"]`), store.templates[promptKey].Tags)
}

func TestPromptWriteStripsGlobalAndStaleScopeTags(t *testing.T) {
	t.Parallel()

	const promptKey = "main/scoped"
	store := newInMemoryPromptStore()
	template := scopedPromptTemplate(promptKey, "/repo/a")
	template.Tags = json.RawMessage(`["scope.global","scope.cwd:/repo/a","scope.cwd:/repo/old","team"]`)
	store.templates[promptKey] = template
	svc := newPromptService(store)

	got, err := svc.WritePrompt(context.Background(), "/repo/a", PromptWriteRequest{
		ID:           promptKey,
		Name:         "Scoped Prompt",
		Content:      "updated",
		WhenToUse:    "Use for scoped edits.",
		WhenToUseSet: true,
		Tags:         json.RawMessage(`["scope.global","scope.cwd:/repo/old","team"]`),
	})
	require.NoError(t, err)
	require.JSONEq(t, `["team","scope.cwd:/repo/a"]`, string(got.Tags))
}

func TestPromptWriteGlobalRequiresExplicitScope(t *testing.T) {
	t.Parallel()

	const promptKey = "main/global-user"
	store := newInMemoryPromptStore()
	template := scopedPromptTemplate(promptKey, "")
	template.Tags = json.RawMessage(`["scope.global","intent:expert"]`)
	template.CreatedBy = promptUpdatedBy
	template.UpdatedBy = promptUpdatedBy
	store.templates[promptKey] = template
	svc := newPromptService(store)

	_, err := svc.WritePrompt(context.Background(), "/repo/a", PromptWriteRequest{
		ID:           promptKey,
		Name:         "Global User",
		Content:      "implicit project edit",
		WhenToUse:    "Use for global edits.",
		WhenToUseSet: true,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside cwd scope")

	got, err := svc.WritePrompt(context.Background(), "/repo/a", PromptWriteRequest{
		ID:           promptKey,
		Name:         "Global User",
		Content:      "explicit global edit",
		WhenToUse:    "Use for global edits.",
		WhenToUseSet: true,
		Scope:        "global",
		ScopeSet:     true,
	})
	require.NoError(t, err)
	require.Equal(t, "explicit global edit", got.PromptText)
	require.JSONEq(t, `["intent:expert","scope.global"]`, string(got.Tags))
}

func TestPromptWriteGlobalAllowsExplicitProjectScope(t *testing.T) {
	t.Parallel()

	const promptKey = "main/global-user"
	store := newInMemoryPromptStore()
	template := scopedPromptTemplate(promptKey, "")
	template.Tags = json.RawMessage(`["scope.global","intent:expert"]`)
	template.PromptText = "global original"
	template.CreatedBy = promptUpdatedBy
	template.UpdatedBy = promptUpdatedBy
	store.templates[promptKey] = template
	svc := newPromptService(store)

	got, err := svc.WritePrompt(context.Background(), "/repo/a", PromptWriteRequest{
		ID:           promptKey,
		Name:         "Global User",
		Content:      "project scope edit",
		WhenToUse:    "Use for project edits.",
		WhenToUseSet: true,
		Scope:        "project",
		ScopeSet:     true,
	})
	require.NoError(t, err)
	require.Equal(t, "project scope edit", got.PromptText)
	require.JSONEq(t, `["intent:expert","scope.cwd:/repo/a"]`, string(got.Tags))
	require.JSONEq(t, `["intent:expert","scope.cwd:/repo/a"]`, string(store.templates[promptKey].Tags))
}

func TestPromptWriteCreatesGlobalWhenScopeExplicit(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	svc := newPromptService(store)

	got, err := svc.WritePrompt(context.Background(), "/repo/a", PromptWriteRequest{
		Name:         "Global Expert",
		Content:      "global body",
		WhenToUse:    "Use across projects.",
		WhenToUseSet: true,
		Scope:        "global",
		ScopeSet:     true,
		Tags:         json.RawMessage(`["intent:expert"]`),
	})
	require.NoError(t, err)
	require.JSONEq(t, `["intent:expert","scope.global"]`, string(got.Tags))
}

func TestPromptSectionWriteRejectsGlobalSeedWithoutCurrentCWD(t *testing.T) {
	t.Parallel()

	const promptKey = "main/global-seed"
	store := newInMemoryPromptStore()
	template := scopedPromptTemplate(promptKey, "")
	template.ID = 7
	template.Tags = json.RawMessage(`["scope.global"]`)
	store.templates[promptKey] = template
	svc := newPromptService(store)

	_, err := svc.WriteSection(context.Background(), "/repo/a", PromptSectionWriteRequest{
		PromptKey:  promptKey,
		SectionKey: "body",
		Region:     "dynamic",
		Body:       "updated",
		Enabled:    true,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside cwd scope")
	require.Empty(t, store.sections[7])
}

func TestPromptDeleteGlobalUserAssetRequiresExplicitGlobalScope(t *testing.T) {
	t.Parallel()

	const promptKey = "main/global-user"
	store := newInMemoryPromptStore()
	template := scopedPromptTemplate(promptKey, "")
	template.Tags = json.RawMessage(`["scope.global","intent:expert"]`)
	template.CreatedBy = promptUpdatedBy
	template.UpdatedBy = promptUpdatedBy
	store.templates[promptKey] = template
	svc := newPromptService(store)

	err := svc.DeletePrompt(context.Background(), "/repo/a", promptKey)
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside cwd scope")
	require.Contains(t, store.templates, promptKey)

	err = svc.DeletePrompt(context.Background(), "/repo/a", promptKey, "global")
	require.NoError(t, err)
	require.NotContains(t, store.templates, promptKey)
}

func TestPromptSectionWriteGlobalUserAssetRequiresExplicitGlobalScope(t *testing.T) {
	t.Parallel()

	const promptKey = "main/global-user"
	store := newInMemoryPromptStore()
	template := scopedPromptTemplate(promptKey, "")
	template.ID = 7
	template.Tags = json.RawMessage(`["scope.global","intent:expert"]`)
	template.CreatedBy = promptUpdatedBy
	template.UpdatedBy = promptUpdatedBy
	store.templates[promptKey] = template
	svc := newPromptService(store)

	_, err := svc.WriteSection(context.Background(), "/repo/a", PromptSectionWriteRequest{
		PromptKey:   promptKey,
		SectionKey:  "body",
		Region:      "dynamic",
		Body:        "implicit global edit",
		Enabled:     true,
		TriggerType: "always",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside cwd scope")
	require.Empty(t, store.sections[7])

	_, err = svc.WriteSection(context.Background(), "/repo/a", PromptSectionWriteRequest{
		PromptKey:   promptKey,
		SectionKey:  "body",
		Region:      "dynamic",
		Body:        "explicit global edit",
		Enabled:     true,
		TriggerType: "always",
		Scope:       "global",
		ScopeSet:    true,
	})
	require.NoError(t, err)
	require.Equal(t, "explicit global edit", store.sections[7]["body"].Body)
}

func TestPromptSectionWriteProjectRecallCanOverrideGlobalTopic(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	globalTemplate := scopedPromptTemplate("main/knowledge/sqlc-global", "")
	globalTemplate.ID = 7
	globalTemplate.Tags = json.RawMessage(`["intent:recall","scope.global"]`)
	store.templates[globalTemplate.PromptKey] = globalTemplate
	store.sections[globalTemplate.ID] = map[string]promptstore.PromptTemplateSection{
		"recall_sqlc_workflow": {
			TemplateID:  globalTemplate.ID,
			SectionKey:  "recall_sqlc_workflow",
			Enabled:     true,
			TriggerType: "recall",
			RecallTopic: "sqlc-workflow",
			Body:        "Global SQLC workflow.",
		},
	}
	projectTemplate := scopedPromptTemplate("main/knowledge/sqlc-project", "/repo/a")
	projectTemplate.ID = 8
	projectTemplate.Tags = withPromptScopeTag(json.RawMessage(`["intent:recall"]`), "/repo/a")
	store.templates[projectTemplate.PromptKey] = projectTemplate
	svc := newPromptService(store)

	_, err := svc.WriteSection(context.Background(), "/repo/a", PromptSectionWriteRequest{
		PromptKey:   projectTemplate.PromptKey,
		SectionKey:  "recall_sqlc_workflow",
		Region:      "dynamic",
		Body:        "Project SQLC workflow.",
		Enabled:     true,
		TriggerType: "recall",
		RecallTopic: "sqlc-workflow",
	})
	require.NoError(t, err)
	require.Equal(t, "Project SQLC workflow.", store.sections[projectTemplate.ID]["recall_sqlc_workflow"].Body)
}

func TestPromptSectionWriteGlobalRecallCanCoexistWithProjectTopic(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	projectTemplate := scopedPromptTemplate("main/knowledge/sqlc-project", "/repo/a")
	projectTemplate.ID = 7
	projectTemplate.Tags = withPromptScopeTag(json.RawMessage(`["intent:recall"]`), "/repo/a")
	store.templates[projectTemplate.PromptKey] = projectTemplate
	store.sections[projectTemplate.ID] = map[string]promptstore.PromptTemplateSection{
		"recall_sqlc_workflow": {
			TemplateID:  projectTemplate.ID,
			SectionKey:  "recall_sqlc_workflow",
			Enabled:     true,
			TriggerType: "recall",
			RecallTopic: "sqlc-workflow",
			Body:        "Project SQLC workflow.",
		},
	}
	globalTemplate := scopedPromptTemplate("main/knowledge/sqlc-global", "")
	globalTemplate.ID = 8
	globalTemplate.Tags = json.RawMessage(`["intent:recall","scope.global"]`)
	store.templates[globalTemplate.PromptKey] = globalTemplate
	svc := newPromptService(store)

	_, err := svc.WriteSection(context.Background(), "/repo/a", PromptSectionWriteRequest{
		PromptKey:   globalTemplate.PromptKey,
		SectionKey:  "recall_sqlc_workflow",
		Region:      "dynamic",
		Body:        "Global SQLC workflow.",
		Enabled:     true,
		TriggerType: "recall",
		RecallTopic: "sqlc-workflow",
		Scope:       "global",
		ScopeSet:    true,
	})
	require.NoError(t, err)
	require.Equal(t, "Global SQLC workflow.", store.sections[globalTemplate.ID]["recall_sqlc_workflow"].Body)
}

func TestDefaultRuleSectionWriteInvalidatesProjectDefaultRules(t *testing.T) {
	t.Parallel()

	const promptKey = "main/default-rule"
	store := newInMemoryPromptStore()
	template := scopedPromptTemplate(promptKey, "/repo/a")
	template.ID = 7
	template.AgentKey = "default_rule"
	store.templates[promptKey] = template
	rec := &recordingSectionInvalidator{}
	svc := newPromptService(store, rec)

	_, err := svc.WriteSection(context.Background(), "/repo/a", PromptSectionWriteRequest{
		PromptKey:   promptKey,
		SectionKey:  "default_rule_body",
		Region:      "dynamic",
		Body:        "提交前跑测试。",
		Enabled:     true,
		TriggerType: "always",
	})
	require.NoError(t, err)
	require.Equal(t, contract.InvalidateClear, rec.reason)
	require.Equal(t, []string{contract.DynamicSectionRecallCatalog, contract.DynamicSectionProjectDefaultRules}, rec.names)
}

func TestDefaultRuleSectionDeleteInvalidatesProjectDefaultRules(t *testing.T) {
	t.Parallel()

	const promptKey = "main/default-rule"
	store := newInMemoryPromptStore()
	template := scopedPromptTemplate(promptKey, "/repo/a")
	template.ID = 7
	template.AgentKey = "default_rule"
	store.templates[promptKey] = template
	rec := &recordingSectionInvalidator{}
	svc := newPromptService(store, rec)

	err := svc.DeleteSection(context.Background(), "/repo/a", promptKey, "default_rule_body")
	require.NoError(t, err)
	require.Equal(t, contract.InvalidateClear, rec.reason)
	require.Equal(t, []string{contract.DynamicSectionRecallCatalog, contract.DynamicSectionProjectDefaultRules}, rec.names)
}

func TestPromptWritePreservesMatchWhenWhenOmitted(t *testing.T) {
	t.Parallel()

	const promptKey = "main/scoped"
	store := newInMemoryPromptStore()
	template := scopedPromptTemplate(promptKey, "/repo/a")
	template.MatchWhen = json.RawMessage(`{"cwd_prefix":"/repo/a"}`)
	store.templates[promptKey] = template
	svc := newPromptService(store)

	got, err := svc.WritePrompt(context.Background(), "/repo/a", PromptWriteRequest{
		ID:      promptKey,
		Name:    "Scoped Prompt",
		Content: "updated by user",
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"cwd_prefix":"/repo/a"}`, string(got.MatchWhen))
	require.JSONEq(t, `{"cwd_prefix":"/repo/a"}`, string(store.templates[promptKey].MatchWhen))
}

func TestPromptWriteStripsRetiredTemplateTagsHasMatchWhen(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	svc := newPromptService(store)

	got, err := svc.WritePrompt(context.Background(), "/repo/a", PromptWriteRequest{
		Name:         "Scoped Prompt",
		Content:      "new body",
		WhenToUse:    "Use when routing by cwd.",
		WhenToUseSet: true,
		MatchWhen:    json.RawMessage(`{"tags_has":["review","bug"],"cwd_prefix":"/repo"}`),
		MatchWhenSet: true,
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"cwd_prefix":"/repo"}`, string(got.MatchWhen))
}

func TestPromptWriteStripsTagsHasOnlyMatchWhenToNil(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	svc := newPromptService(store)

	got, err := svc.WritePrompt(context.Background(), "/repo/a", PromptWriteRequest{
		Name:         "Scoped Prompt",
		Content:      "new body",
		WhenToUse:    "Use when routing by match_when.",
		WhenToUseSet: true,
		MatchWhen:    json.RawMessage(`{"tags_has":"review"}`),
		MatchWhenSet: true,
	})
	require.NoError(t, err)
	require.Empty(t, got.MatchWhen)
}

func TestPromptItemFromTemplateIncludesWhenToUse(t *testing.T) {
	t.Parallel()

	template := scopedPromptTemplate("main/scoped", "/repo/a")
	template.WhenToUse = "Use when routing to scoped prompt edits."

	got := promptItemFromTemplate(promptTemplateFromStore(template))

	require.Equal(t, "Use when routing to scoped prompt edits.", got.WhenToUse)
}

func TestPromptItemFromTemplateIncludesScopeAndHidesInternalScopeTags(t *testing.T) {
	t.Parallel()

	template := scopedPromptTemplate("main/global", "")
	template.Tags = json.RawMessage(`["scope.global","intent:expert","review"]`)

	got := promptItemFromTemplate(promptTemplateFromStore(template))

	require.Equal(t, "global", got.Scope)
	require.JSONEq(t, `["intent:expert","review"]`, string(got.Tags))
}

func TestPromptSectionWriteRoundTripsTriggerTypeAndRecallTopic(t *testing.T) {
	t.Parallel()

	const promptKey = "main/scoped"
	store := newInMemoryPromptStore()
	template := scopedPromptTemplate(promptKey, "/repo/a")
	template.ID = 7
	store.templates[promptKey] = template
	svc := newPromptService(store)

	saved, err := svc.WriteSection(context.Background(), "/repo/a", PromptSectionWriteRequest{
		PromptKey:   promptKey,
		SectionKey:  "project_memory",
		Region:      "dynamic",
		Ordinal:     30,
		Body:        "remember the project",
		Enabled:     true,
		TriggerType: "recall",
		RecallTopic: "project-memory",
	})
	require.NoError(t, err)
	require.Equal(t, "recall", saved.TriggerType)
	require.Equal(t, "project-memory", saved.RecallTopic)

	listed, err := svc.ListSections(context.Background(), "/repo/a", promptKey)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, "recall", listed[0].TriggerType)
	require.Equal(t, "project-memory", listed[0].RecallTopic)
}

func TestPromptSectionWriteRejectsInvalidRecallTopic(t *testing.T) {
	t.Parallel()

	const promptKey = "main/scoped"
	store := newInMemoryPromptStore()
	template := scopedPromptTemplate(promptKey, "/repo/a")
	template.ID = 7
	store.templates[promptKey] = template
	svc := newPromptService(store)

	_, err := svc.WriteSection(context.Background(), "/repo/a", PromptSectionWriteRequest{
		PromptKey:   promptKey,
		SectionKey:  "project_memory",
		Region:      "dynamic",
		Body:        "remember the project",
		Enabled:     true,
		TriggerType: "recall",
		RecallTopic: "project.memory",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "recall_topic")
}

func TestPromptSectionWriteRejectsDuplicateRecallTopicInCWD(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	existing := scopedPromptTemplate("main/existing", "/repo/a")
	existing.ID = 7
	current := scopedPromptTemplate("main/current", "/repo/a")
	current.ID = 8
	store.templates[existing.PromptKey] = existing
	store.templates[current.PromptKey] = current
	store.sections[7] = map[string]promptstore.PromptTemplateSection{
		"recall_existing": {TemplateID: 7, SectionKey: "recall_existing", TriggerType: "recall", RecallTopic: "project-memory", Body: "existing", Enabled: true},
	}
	svc := newPromptService(store)

	_, err := svc.WriteSection(context.Background(), "/repo/a", PromptSectionWriteRequest{
		PromptKey:   current.PromptKey,
		SectionKey:  "recall_new",
		Region:      "dynamic",
		Body:        "new",
		Enabled:     true,
		TriggerType: "recall",
		RecallTopic: "project-memory",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate recall_topic")
	require.Equal(t, []struct {
		cwd   string
		topic string
	}{{cwd: "/repo/a", topic: "project-memory"}}, store.lockRecallCalls)
	require.Empty(t, store.sections[8])
}

func TestPromptSectionWriteAllowsSameRecallTopicInAnotherCWD(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	existing := scopedPromptTemplate("main/existing", "/repo/b")
	existing.ID = 7
	current := scopedPromptTemplate("main/current", "/repo/a")
	current.ID = 8
	store.templates[existing.PromptKey] = existing
	store.templates[current.PromptKey] = current
	store.sections[7] = map[string]promptstore.PromptTemplateSection{
		"recall_existing": {TemplateID: 7, SectionKey: "recall_existing", TriggerType: "recall", RecallTopic: "project-memory", Body: "existing", Enabled: true},
	}
	svc := newPromptService(store)

	saved, err := svc.WriteSection(context.Background(), "/repo/a", PromptSectionWriteRequest{
		PromptKey:   current.PromptKey,
		SectionKey:  "recall_new",
		Region:      "dynamic",
		Body:        "new",
		Enabled:     true,
		TriggerType: "recall",
		RecallTopic: "project-memory",
	})
	require.NoError(t, err)
	require.Equal(t, "project-memory", saved.RecallTopic)
	require.Len(t, store.sections[8], 1)
}

func TestPromptSectionWriteAllowsDuplicateRecallTopicWhenUpdatingSameSection(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	template := scopedPromptTemplate("main/current", "/repo/a")
	template.ID = 7
	store.templates[template.PromptKey] = template
	store.sections[7] = map[string]promptstore.PromptTemplateSection{
		"recall_existing": {TemplateID: 7, SectionKey: "recall_existing", TriggerType: "recall", RecallTopic: "project-memory", Body: "existing", Enabled: true},
	}
	svc := newPromptService(store)

	saved, err := svc.WriteSection(context.Background(), "/repo/a", PromptSectionWriteRequest{
		PromptKey:   template.PromptKey,
		SectionKey:  "recall_existing",
		Region:      "dynamic",
		Body:        "updated",
		Enabled:     true,
		TriggerType: "recall",
		RecallTopic: "project-memory",
	})
	require.NoError(t, err)
	require.Equal(t, "updated", saved.Body)
	require.Equal(t, "project-memory", saved.RecallTopic)
}

func TestPromptSectionItemFromStoreIncludesTriggerTypeAndRecallTopic(t *testing.T) {
	t.Parallel()

	got := promptSectionItemFromStore(promptTemplateSectionFromStore(promptstore.PromptTemplateSection{
		ID:          11,
		TemplateID:  7,
		SectionKey:  "project_memory",
		Region:      "dynamic",
		Body:        "remember the project",
		Enabled:     true,
		TriggerType: "recall",
		RecallTopic: "project-memory",
	}), "main/scoped")

	require.Equal(t, "recall", got.TriggerType)
	require.Equal(t, "project-memory", got.RecallTopic)
}

func TestPromptSectionsRPCWriteRoundTripsTriggerTypeAndRecallTopic(t *testing.T) {
	t.Parallel()

	const promptKey = "main/scoped"
	store := newInMemoryPromptStore()
	template := scopedPromptTemplate(promptKey, "/repo/a")
	template.ID = 7
	store.templates[promptKey] = template
	server := platformrpc.NewServer(platformrpc.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(buildPromptHandlersWithService(newPromptService(store)).Handlers)

	raw, err := server.Dispatch(context.Background(), "prompt-sections/write", json.RawMessage(`{
		"prompt_id":"main/scoped",
		"section_key":"project_memory",
		"region":"dynamic",
		"ordinal":30,
		"body":"remember the project",
		"enabled":true,
		"cwd":"/repo/a",
		"trigger_type":"recall",
		"recall_topic":"project-memory"
	}`))
	require.NoError(t, err)
	require.JSONEq(t, `{
		"section":{
			"id":1,
			"prompt_id":"main/scoped",
			"section_key":"project_memory",
			"region":"dynamic",
			"ordinal":30,
			"body":"remember the project",
			"enabled":true,
			"created_at":"0001-01-01T00:00:00Z",
			"updated_at":"0001-01-01T00:00:00Z",
			"trigger_type":"recall",
			"recall_topic":"project-memory"
		}
	}`, string(raw))
}

func TestPromptSectionsRPCWriteRejectsInvalidRecallTopic(t *testing.T) {
	t.Parallel()

	const promptKey = "main/scoped"
	store := newInMemoryPromptStore()
	template := scopedPromptTemplate(promptKey, "/repo/a")
	template.ID = 7
	store.templates[promptKey] = template
	server := platformrpc.NewServer(platformrpc.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(buildPromptHandlersWithService(newPromptService(store)).Handlers)

	_, err := server.Dispatch(context.Background(), "prompt-sections/write", json.RawMessage(`{
		"prompt_id":"main/scoped",
		"section_key":"project_memory",
		"region":"dynamic",
		"body":"remember the project",
		"enabled":true,
		"cwd":"/repo/a",
		"trigger_type":"recall",
		"recall_topic":"project.memory"
	}`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "recall_topic")
}
