package prompt

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	promptintent "github.com/anthropic-ai/super-agent-v3/internal/module/prompt/intent"
	"github.com/anthropic-ai/super-agent-v3/internal/module/threadprompt"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	"github.com/stretchr/testify/require"
)

func TestPromptsRPCListUsesSectionPreviewWithoutRecallBody(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	sectioned := scopedPromptTemplate("main/sectioned", "")
	sectioned.ID = 7
	sectioned.PromptText = "legacy sectioned fallback"
	plain := scopedPromptTemplate("main/plain", "")
	plain.ID = 8
	plain.PromptText = "plain fallback"
	store.templates[sectioned.PromptKey] = sectioned
	store.templates[plain.PromptKey] = plain
	store.sections[7] = sectionPreviewTestSections()

	server := platformrpc.NewServer(platformrpc.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(buildPromptHandlersWithService(newPromptService(store)).Handlers)

	raw, err := server.Dispatch(context.Background(), "prompts/list", json.RawMessage(`{"cwd":"/repo/a"}`))
	require.NoError(t, err)
	require.Equal(t, 1, store.listSectionsByTemplateIDsCalls)
	require.ElementsMatch(t, []int64{7, 8}, store.listSectionsByTemplateIDsCaptured)

	byID := decodePromptListByID(t, raw)
	require.Equal(t, "Identity body\n\nWorkflow body", byID["main/sectioned"].Content)
	require.NotContains(t, byID["main/sectioned"].Content, "Recall pack body")
	require.Equal(t, "plain fallback", byID["main/plain"].Content)
}

func TestPromptsRPCListPassesCWDToStore(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	store.templates["main/project"] = scopedPromptTemplate("main/project", "/repo/a")
	store.templates["main/other"] = scopedPromptTemplate("main/other", "/repo/b")

	server := platformrpc.NewServer(platformrpc.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(buildPromptHandlersWithService(newPromptService(store)).Handlers)

	_, err := server.Dispatch(context.Background(), "prompts/list", json.RawMessage(`{"cwd":"/repo/a"}`))
	require.NoError(t, err)
	require.Len(t, store.listFilters, 1)
	require.Equal(t, "/repo/a", store.listFilters[0].CWD)
	require.Equal(t, int32(promptRPCLimit), store.listFilters[0].Limit)
}

func TestPromptAssetsRPCListShowsOnlyUserAssetsAndReadyDrafts(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	seedPromptAssetListScopeFixtures(store)

	server := platformrpc.NewServer(platformrpc.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(buildPromptHandlersWithService(newPromptService(store), store).Handlers)

	raw, err := server.Dispatch(context.Background(), "prompt-assets/list", json.RawMessage(`{"cwd":"/repo/a"}`))
	require.NoError(t, err)
	assets := decodePromptAssetsByID(t, raw)

	require.Contains(t, assets, "main/expert/review")
	require.Contains(t, assets, "main/knowledge/sqlc")
	require.Contains(t, assets, "main/expert/global-review")
	require.Contains(t, assets, "main/expert/shared-project")
	require.Contains(t, assets, "main/system-edited")
	require.NotContains(t, assets, "main/expert/shared-global")
	require.NotContains(t, assets, "main/system-seed")
	require.NotContains(t, assets, "main/system-global")
	require.NotContains(t, assets, "main/expert/other")
	require.NotContains(t, assets, "intent/expert/enabled")
	require.Equal(t, "project", assets["main/expert/review"]["scope"])
	require.Equal(t, "global", assets["main/expert/global-review"]["scope"])

	draft := assets["intent/recall/ready"]
	require.Equal(t, "待确认 SQLC 资料", draft["name"])
	require.Equal(t, "pending_confirm", draft["state"])
	require.Equal(t, "intent/recall/ready", draft["draft_key"])
	require.Equal(t, "ready_to_save", draft["draft_status"])
	require.Equal(t, "project", draft["scope"])
	card, ok := draft["card"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, []any{"SQLC 价格是多少"}, card["hit_examples"])
	require.Equal(t, []any{"普通闲聊"}, card["miss_examples"])
	require.Equal(t, "SQLC pricing details", card["recall_body"])
	require.NotContains(t, card, "suggested_alternative")
}

func TestPromptAssetsRPCListHidesBuiltinSystemTag(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	builtinTagged := scopedPromptTemplate("main/default", "/repo/a")
	builtinTagged.ID = 7
	builtinTagged.Title = "Builtin Default"
	builtinTagged.CreatedBy = promptUpdatedBy
	builtinTagged.UpdatedBy = promptUpdatedBy
	builtinTagged.Tags = withPromptScopeTag(json.RawMessage(`["intent:expert","builtin:system"]`), "/repo/a")
	store.templates[builtinTagged.PromptKey] = builtinTagged
	userAsset := scopedPromptTemplate("main/expert/review", "/repo/a")
	userAsset.ID = 8
	userAsset.Title = "Review Expert"
	userAsset.CreatedBy = promptUpdatedBy
	userAsset.UpdatedBy = promptUpdatedBy
	userAsset.Tags = withPromptScopeTag(json.RawMessage(`["intent:expert"]`), "/repo/a")
	store.templates[userAsset.PromptKey] = userAsset

	server := platformrpc.NewServer(platformrpc.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(buildPromptHandlersWithService(newPromptService(store), store, fakeBuiltinRegistryWithKeys("main/default")).Handlers)

	raw, err := server.Dispatch(context.Background(), "prompt-assets/list", json.RawMessage(`{"cwd":"/repo/a"}`))
	require.NoError(t, err)
	assets := decodePromptAssetsByID(t, raw)
	require.NotContains(t, assets, "main/default")
	require.Contains(t, assets, "main/expert/review")
}

func TestPromptAssetsRPCListPreservesGlobalPendingDraftScope(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	dream := &fakePromptIntentDream{output: `{
		"kind":"recall",
		"title":"Global Price List",
		"summary":"Shared price-list reference.",
		"recall_topic":"global-price-list",
		"recall_body":"Plan A costs 100.",
		"hit_examples":["How much is plan A?"],
		"miss_examples":["Write a Vue component"]
	}`}
	rawDraft, err := promptintent.HandleDraft(context.Background(), promptIntentStoreForTest(store), dream, nil, promptintent.DraftParams{
		Kind:         "recall",
		RawInput:     "Save this price list for all projects.",
		Cwd:          "/repo/a",
		EnableGlobal: true,
	})
	require.NoError(t, err)
	draftBytes, err := json.Marshal(rawDraft)
	require.NoError(t, err)
	var draft struct {
		DraftKey string `json:"draft_key"`
		Scope    string `json:"scope"`
	}
	require.NoError(t, json.Unmarshal(draftBytes, &draft))
	require.Equal(t, "global", draft.Scope)

	server := platformrpc.NewServer(platformrpc.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(buildPromptHandlersWithService(newPromptService(store), store).Handlers)

	raw, err := server.Dispatch(context.Background(), "prompt-assets/list", json.RawMessage(`{"cwd":"/repo/a"}`))
	require.NoError(t, err)
	assets := decodePromptAssetsByID(t, raw)
	require.Equal(t, "global", assets[draft.DraftKey]["scope"])
}

func TestPromptAssetsRPCListReturnsFullEditableAssetContent(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	expert := scopedPromptTemplate("main/expert/review", "/repo/a")
	expert.ID = 7
	expert.PromptText = ""
	expert.CreatedBy = promptUpdatedBy
	expert.UpdatedBy = promptUpdatedBy
	expert.Tags = withPromptScopeTag(json.RawMessage(`["intent:expert"]`), "/repo/a")
	recall := scopedPromptTemplate("main/knowledge/sqlc", "/repo/a")
	recall.ID = 8
	recall.PromptText = ""
	recall.CreatedBy = promptUpdatedBy
	recall.UpdatedBy = promptUpdatedBy
	recall.Tags = withPromptScopeTag(json.RawMessage(`["intent:recall"]`), "/repo/a")
	store.templates[expert.PromptKey] = expert
	store.templates[recall.PromptKey] = recall
	longWorkflow := strings.Repeat("W", promptListContentPreviewMaxRunes+25)
	store.sections[7] = map[string]promptstore.PromptTemplateSection{
		"workflow": {ID: 1, TemplateID: 7, SectionKey: "workflow", Region: "dynamic", Ordinal: 20, Body: longWorkflow, Enabled: true, TriggerType: "always"},
	}
	store.sections[8] = map[string]promptstore.PromptTemplateSection{
		"recall_sqlc": {ID: 2, TemplateID: 8, SectionKey: "recall_sqlc", Region: "dynamic", Ordinal: 100, Body: "Read source SQL before generated sqlc output.", Enabled: true, TriggerType: "recall", RecallTopic: "sqlc-workflow"},
	}

	server := platformrpc.NewServer(platformrpc.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(buildPromptHandlersWithService(newPromptService(store), store).Handlers)

	raw, err := server.Dispatch(context.Background(), "prompt-assets/list", json.RawMessage(`{"cwd":"/repo/a"}`))
	require.NoError(t, err)
	assets := decodePromptAssetsByID(t, raw)
	require.Equal(t, longWorkflow, assets["main/expert/review"]["content"])
	require.Equal(t, "Read source SQL before generated sqlc output.", assets["main/knowledge/sqlc"]["content"])
}

func seedPromptAssetListScopeFixtures(store *inMemoryPromptStore) {
	systemTemplate := scopedPromptTemplate("main/system-seed", "/repo/a")
	systemTemplate.ID = 1
	systemTemplate.Title = "System Seed"
	systemTemplate.CreatedBy = "system.seed"
	systemTemplate.UpdatedBy = "migration"
	systemTemplate.Tags = withPromptScopeTag(json.RawMessage(`["intent:expert","internal"]`), "/repo/a")
	store.templates[systemTemplate.PromptKey] = systemTemplate

	systemGlobal := scopedPromptTemplate("main/system-global", "")
	systemGlobal.ID = 11
	systemGlobal.Title = "System Global Seed"
	systemGlobal.CreatedBy = "system.seed"
	systemGlobal.UpdatedBy = "migration"
	systemGlobal.Tags = json.RawMessage(`["intent:expert","scope.global"]`)
	store.templates[systemGlobal.PromptKey] = systemGlobal

	seedPromptAssetListEditedSystemFixture(store)

	expert := scopedPromptTemplate("main/expert/review", "/repo/a")
	expert.ID = 2
	expert.Title = "Review Expert"
	expert.CreatedBy = promptUpdatedBy
	expert.UpdatedBy = promptUpdatedBy
	expert.Tags = withPromptScopeTag(json.RawMessage(`["intent:expert","review"]`), "/repo/a")
	store.templates[expert.PromptKey] = expert

	recall := scopedPromptTemplate("main/knowledge/sqlc", "/repo/a")
	recall.ID = 3
	recall.Title = "SQLC Knowledge"
	recall.CreatedBy = "importer"
	recall.UpdatedBy = promptUpdatedBy
	recall.Tags = withPromptScopeTag(json.RawMessage(`["intent:recall","sqlc"]`), "/repo/a")
	store.templates[recall.PromptKey] = recall

	globalExpert := scopedPromptTemplate("main/expert/global-review", "")
	globalExpert.ID = 12
	globalExpert.Title = "Global Review Expert"
	globalExpert.CreatedBy = promptUpdatedBy
	globalExpert.UpdatedBy = promptUpdatedBy
	globalExpert.Tags = json.RawMessage(`["intent:expert","review","scope.global"]`)
	store.templates[globalExpert.PromptKey] = globalExpert

	sharedGlobal := scopedPromptTemplate("main/expert/shared-global", "")
	sharedGlobal.ID = 14
	sharedGlobal.Title = "Shared SQL Expert"
	sharedGlobal.CreatedBy = promptUpdatedBy
	sharedGlobal.UpdatedBy = promptUpdatedBy
	sharedGlobal.Tags = json.RawMessage(`["intent:expert","sql","scope.global"]`)
	store.templates[sharedGlobal.PromptKey] = sharedGlobal

	sharedProject := scopedPromptTemplate("main/expert/shared-project", "/repo/a")
	sharedProject.ID = 15
	sharedProject.Title = "Shared SQL Expert"
	sharedProject.CreatedBy = promptUpdatedBy
	sharedProject.UpdatedBy = promptUpdatedBy
	sharedProject.Tags = withPromptScopeTag(json.RawMessage(`["intent:expert","sql"]`), "/repo/a")
	store.templates[sharedProject.PromptKey] = sharedProject

	otherCWD := scopedPromptTemplate("main/expert/other", "/repo/b")
	otherCWD.ID = 4
	otherCWD.Title = "Other Project Expert"
	otherCWD.CreatedBy = promptUpdatedBy
	otherCWD.UpdatedBy = promptUpdatedBy
	otherCWD.Tags = withPromptScopeTag(json.RawMessage(`["intent:expert"]`), "/repo/b")
	store.templates[otherCWD.PromptKey] = otherCWD

	store.drafts["intent/recall/ready"] = promptAssetDraftForTest("intent/recall/ready", "/repo/a", "recall", "ready_to_save", `{
		"kind":"recall",
		"title":"待确认 SQLC 资料",
		"summary":"SQLC 价格表解析资料",
		"when_to_use":"Use when SQLC pricing is discussed.",
		"recall_body":"SQLC pricing details",
		"hit_examples":["SQLC 价格是多少"],
		"miss_examples":["普通闲聊"],
		"suggested_alternative":{"kind":"recall","reason":"更适合做参考资料"}
	}`)
	store.drafts["intent/expert/enabled"] = promptAssetDraftForTest("intent/expert/enabled", "/repo/a", "expert", "enabled", `{
		"kind":"expert",
		"title":"已保存专家草稿",
		"summary":"Should not be shown as pending",
		"when_to_use":"Use when already enabled.",
		"hit_examples":["实现功能"],
		"miss_examples":["问天气"]
	}`)
}

func seedPromptAssetListEditedSystemFixture(store *inMemoryPromptStore) {
	editedSystem := scopedPromptTemplate("main/system-edited", "/repo/a")
	editedSystem.ID = 16
	editedSystem.Title = "User Edited System Seed"
	editedSystem.CreatedBy = "system.seed"
	editedSystem.UpdatedBy = "system.seed"
	editedSystem.ManuallyEdited = true
	editedSystem.Tags = withPromptScopeTag(json.RawMessage(`["intent:expert","edited"]`), "/repo/a")
	store.templates[editedSystem.PromptKey] = editedSystem
}

func TestPromptsRPCGetReturnsFullSectionContent(t *testing.T) {
	t.Parallel()

	const promptKey = "main/sectioned"
	store := newInMemoryPromptStore()
	template := scopedPromptTemplate(promptKey, "/repo/a")
	template.ID = 7
	template.PromptText = "legacy monolith"
	store.templates[promptKey] = template
	store.sections[7] = map[string]promptstore.PromptTemplateSection{
		"identity": {ID: 1, TemplateID: 7, SectionKey: "identity", Region: "static", Ordinal: 0, Body: strings.Repeat("A", 220), Enabled: true},
		"recall":   {ID: 2, TemplateID: 7, SectionKey: "recall", Region: "dynamic", Ordinal: 1, Body: "hidden recall", TriggerType: "recall", Enabled: true},
		"workflow": {ID: 3, TemplateID: 7, SectionKey: "workflow", Region: "dynamic", Ordinal: 2, Body: "workflow body", Enabled: true},
	}
	server := platformrpc.NewServer(platformrpc.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(buildPromptHandlersWithService(newPromptService(store)).Handlers)

	raw, err := server.Dispatch(context.Background(), "prompts/get", json.RawMessage(`{
		"id":"main/sectioned",
		"cwd":"/repo/a"
	}`))
	require.NoError(t, err)
	prompt := decodePromptGet(t, raw)
	require.Contains(t, prompt.Content, strings.Repeat("A", 220))
	require.Contains(t, prompt.Content, "workflow body")
	require.NotContains(t, prompt.Content, "hidden recall")
}

func TestPromptsRPCGetReturnsRecallAssetContent(t *testing.T) {
	t.Parallel()

	const promptKey = "main/knowledge/sqlc"
	store := newInMemoryPromptStore()
	template := scopedPromptTemplate(promptKey, "/repo/a")
	template.ID = 7
	template.PromptText = ""
	template.Tags = withPromptScopeTag(json.RawMessage(`["intent:recall"]`), "/repo/a")
	store.templates[promptKey] = template
	store.sections[7] = map[string]promptstore.PromptTemplateSection{
		"recall_sqlc": {ID: 1, TemplateID: 7, SectionKey: "recall_sqlc", Region: "dynamic", Ordinal: 100, Body: "Recall body copied from asset manager.", Enabled: true, TriggerType: "recall", RecallTopic: "sqlc-workflow"},
	}
	server := platformrpc.NewServer(platformrpc.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(buildPromptHandlersWithService(newPromptService(store)).Handlers)

	raw, err := server.Dispatch(context.Background(), "prompts/get", json.RawMessage(`{
		"id":"main/knowledge/sqlc",
		"cwd":"/repo/a"
	}`))
	require.NoError(t, err)
	prompt := decodePromptGet(t, raw)
	require.Equal(t, "Recall body copied from asset manager.", prompt.Content)
}

func TestPromptsRPCWriteClearsMatchWhenWithNull(t *testing.T) {
	t.Parallel()

	const promptKey = "main/scoped"
	store := newInMemoryPromptStore()
	template := scopedPromptTemplate(promptKey, "/repo/a")
	template.ID = 7
	template.MatchWhen = json.RawMessage(`{"cwd_prefix":"/repo/a"}`)
	store.templates[promptKey] = template
	server := platformrpc.NewServer(platformrpc.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(buildPromptHandlersWithService(newPromptService(store)).Handlers)

	_, err := server.Dispatch(context.Background(), "prompts/write", json.RawMessage(`{
		"id":"main/scoped",
		"name":"Scoped Prompt",
		"content":"updated by user",
		"cwd":"/repo/a",
		"match_when":null
	}`))
	require.NoError(t, err)
	require.Empty(t, store.templates[promptKey].MatchWhen)
}

func TestPromptsRPCWritePreservesPromptTextWhenContentOmitted(t *testing.T) {
	t.Parallel()

	const promptKey = "main/scoped"
	store := newInMemoryPromptStore()
	template := scopedPromptTemplate(promptKey, "/repo/a")
	template.ID = 7
	template.PromptText = "original body"
	store.templates[promptKey] = template
	server := platformrpc.NewServer(platformrpc.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(buildPromptHandlersWithService(newPromptService(store)).Handlers)

	_, err := server.Dispatch(context.Background(), "prompts/write", json.RawMessage(`{
		"id":"main/scoped",
		"name":"Scoped Prompt",
		"cwd":"/repo/a",
		"when_to_use":"Use after metadata edits."
	}`))
	require.NoError(t, err)
	require.Equal(t, "original body", store.templates[promptKey].PromptText)
	require.Equal(t, "Use after metadata edits.", store.templates[promptKey].WhenToUse)
}

func TestPromptsRPCWriteUpdatesSectionedExpertExecutionContent(t *testing.T) {
	t.Parallel()

	const promptKey = "main/expert/review"
	store := newInMemoryPromptStore()
	template := scopedPromptTemplate(promptKey, "/repo/a")
	template.ID = 7
	template.PromptText = ""
	template.Tags = withPromptScopeTag(json.RawMessage(`["intent:expert"]`), "/repo/a")
	store.templates[promptKey] = template
	store.sections[7] = map[string]promptstore.PromptTemplateSection{
		"workflow": {TemplateID: 7, SectionKey: "workflow", Region: "dynamic", Ordinal: 20, Body: "old workflow", Enabled: true, TriggerType: "always"},
		"output":   {TemplateID: 7, SectionKey: "output", Region: "dynamic", Ordinal: 40, Body: "old output", Enabled: true, TriggerType: "always"},
	}
	server := platformrpc.NewServer(platformrpc.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(buildPromptHandlersWithService(newPromptService(store)).Handlers)

	_, err := server.Dispatch(context.Background(), "prompts/write", json.RawMessage(`{
		"id":"main/expert/review",
		"name":"Review Expert",
		"content":"Inspect changed files, list blocking findings first, then summarize residual risk.",
		"cwd":"/repo/a"
	}`))
	require.NoError(t, err)
	require.Equal(t, "", store.templates[promptKey].PromptText)
	require.Equal(t, "Inspect changed files, list blocking findings first, then summarize residual risk.", store.sections[7]["workflow"].Body)
	require.Equal(t, "old output", store.sections[7]["output"].Body)
}

func TestPromptsRPCWriteUpdatesRecallRuntimeContentSection(t *testing.T) {
	t.Parallel()

	const promptKey = "main/knowledge/sqlc"
	store := newInMemoryPromptStore()
	template := scopedPromptTemplate(promptKey, "/repo/a")
	template.ID = 7
	template.PromptText = ""
	template.Tags = withPromptScopeTag(json.RawMessage(`["intent:recall"]`), "/repo/a")
	store.templates[promptKey] = template
	store.sections[7] = map[string]promptstore.PromptTemplateSection{
		"recall_sqlc_workflow": {TemplateID: 7, SectionKey: "recall_sqlc_workflow", Region: "dynamic", Ordinal: 100, Body: "old recall body", Enabled: true, TriggerType: "recall", RecallTopic: "sqlc-workflow"},
	}
	server := platformrpc.NewServer(platformrpc.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(buildPromptHandlersWithService(newPromptService(store)).Handlers)

	_, err := server.Dispatch(context.Background(), "prompts/write", json.RawMessage(`{
		"id":"main/knowledge/sqlc",
		"name":"SQLC Knowledge",
		"content":"Read sql/queries before generated sqlc output.",
		"cwd":"/repo/a"
	}`))
	require.NoError(t, err)
	require.Equal(t, "", store.templates[promptKey].PromptText)
	section := store.sections[7]["recall_sqlc_workflow"]
	require.Equal(t, "Read sql/queries before generated sqlc output.", section.Body)
	require.Equal(t, "recall", section.TriggerType)
	require.Equal(t, "sqlc-workflow", section.RecallTopic)
}

func TestPromptsRPCWriteUpdatesDefaultRuleRuntimeContentSection(t *testing.T) {
	t.Parallel()

	const promptKey = "main/default-rule"
	store := newInMemoryPromptStore()
	template := scopedPromptTemplate(promptKey, "/repo/a")
	template.ID = 7
	template.PromptText = ""
	template.AgentKey = "default_rule"
	template.Tags = withPromptScopeTag(json.RawMessage(`["intent:default_rule"]`), "/repo/a")
	store.templates[promptKey] = template
	store.sections[7] = map[string]promptstore.PromptTemplateSection{
		"project_rule": {TemplateID: 7, SectionKey: "project_rule", Region: "dynamic", Ordinal: 100, Body: "old project rule", Enabled: true, TriggerType: "always"},
	}
	server := platformrpc.NewServer(platformrpc.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(buildPromptHandlersWithService(newPromptService(store)).Handlers)

	_, err := server.Dispatch(context.Background(), "prompts/write", json.RawMessage(`{
		"id":"main/default-rule",
		"name":"Project Rule",
		"content":"Before reporting completion, run the relevant focused verification.",
		"cwd":"/repo/a"
	}`))
	require.NoError(t, err)
	require.Equal(t, "", store.templates[promptKey].PromptText)
	require.Equal(t, "Before reporting completion, run the relevant focused verification.", store.sections[7]["project_rule"].Body)
}

func TestPromptsRPCWriteFeedsLLMDiscoverySections(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	for key, id := range map[string]int64{"main/expert/review": 7, "main/knowledge/sqlc": 8, "main/default-rule": 9} {
		tpl := scopedPromptTemplate(key, "/repo/a")
		tpl.ID = id
		tpl.PromptText = ""
		if strings.Contains(key, "knowledge") {
			tpl.Tags = withPromptScopeTag(json.RawMessage(`["intent:recall"]`), "/repo/a")
		} else if strings.Contains(key, "default-rule") {
			tpl.AgentKey = "default_rule"
			tpl.Tags = withPromptScopeTag(json.RawMessage(`["intent:default_rule"]`), "/repo/a")
		} else {
			tpl.Tags = withPromptScopeTag(json.RawMessage(`["intent:expert"]`), "/repo/a")
		}
		store.templates[key] = tpl
	}
	store.sections[7] = map[string]promptstore.PromptTemplateSection{"workflow": {TemplateID: 7, SectionKey: "workflow", Region: "dynamic", Ordinal: 20, Body: "old workflow", Enabled: true, TriggerType: "always"}}
	store.sections[8] = map[string]promptstore.PromptTemplateSection{"recall_sqlc": {TemplateID: 8, SectionKey: "recall_sqlc", Region: "dynamic", Ordinal: 100, Body: "old recall", Enabled: true, TriggerType: "recall", RecallTopic: "sqlc-workflow"}}
	store.sections[9] = map[string]promptstore.PromptTemplateSection{"project_rule": {TemplateID: 9, SectionKey: "project_rule", Region: "dynamic", Ordinal: 100, Body: "old rule", Enabled: true, TriggerType: "always"}}
	server := platformrpc.NewServer(platformrpc.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(buildPromptHandlersWithService(newPromptService(store), store).Handlers)

	writePromptJSON(t, server, `{"id":"main/expert/review","name":"Review Expert","description":"Code review expert","when_to_use":"Use when code review needs blocking findings.","content":"Inspect the diff and report blocking findings first.","cwd":"/repo/a"}`)
	writePromptJSON(t, server, `{"id":"main/knowledge/sqlc","name":"SQLC Knowledge","description":"SQLC workflow reference.","when_to_use":"Use when sqlc workflow details are needed.","content":"Read sql/queries before generated sqlc output.","cwd":"/repo/a"}`)
	writePromptJSON(t, server, `{"id":"main/default-rule","name":"Focused Verification","when_to_use":"Apply before completion reports.","content":"Before reporting completion, run focused verification.","cwd":"/repo/a"}`)
	require.Equal(t, "Inspect the diff and report blocking findings first.", store.sections[7]["workflow"].Body)
	require.Equal(t, "Read sql/queries before generated sqlc output.", store.sections[8]["recall_sqlc"].Body)
	require.Equal(t, "recall", store.sections[8]["recall_sqlc"].TriggerType)
	require.True(t, store.sections[8]["recall_sqlc"].Enabled)
	require.Equal(t, "Before reporting completion, run focused verification.", store.sections[9]["project_rule"].Body)
	require.JSONEq(t, `["intent:recall","scope.cwd:/repo/a"]`, string(store.templates["main/knowledge/sqlc"].Tags))
	recallSections, err := store.ListRecallSections(context.Background(), "/repo/a")
	require.NoError(t, err)
	require.Len(t, recallSections, 1)
	require.Equal(t, "SQLC workflow reference.", recallSections[0].TemplateDescription)

	providers := registeredThreadPromptProviders(t, store)
	input := contract.SectionContext{BuildCtx: contract.BuildCtx{CWD: "/repo/a"}, Turn: &contract.TurnInput{UserText: "Please delegate code review and SQLC workflow to multiple agents in parallel."}}
	experts, err := providers[contract.DynamicSectionAvailableExperts].Resolve(context.Background(), input)
	require.NoError(t, err)
	require.Contains(t, *experts, "Use when code review needs blocking findings.")
	require.Contains(t, *experts, "launch_agent(name='main/expert/review', prompt_key='main/expert/review'")
	recall, err := providers[contract.DynamicSectionRecallCatalog].Resolve(context.Background(), input)
	require.NoError(t, err)
	require.Contains(t, *recall, `topic="sqlc-workflow"`)
	require.Contains(t, *recall, `prompt_recall(topic="<topic>")`)
	rules, err := providers[contract.DynamicSectionProjectDefaultRules].Resolve(context.Background(), input)
	require.NoError(t, err)
	require.Contains(t, *rules, "Before reporting completion, run focused verification.")
}

func TestPromptsRPCWriteAppliesEnabledFalse(t *testing.T) {
	t.Parallel()

	const promptKey = "main/scoped"
	store := newInMemoryPromptStore()
	template := scopedPromptTemplate(promptKey, "/repo/a")
	template.ID = 7
	store.templates[promptKey] = template
	server := platformrpc.NewServer(platformrpc.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(buildPromptHandlersWithService(newPromptService(store)).Handlers)

	raw, err := server.Dispatch(context.Background(), "prompts/write", json.RawMessage(`{
		"id":"main/scoped",
		"name":"Scoped Prompt",
		"content":"updated by user",
		"cwd":"/repo/a",
		"enabled":false
	}`))
	require.NoError(t, err)
	written := decodePromptWrite(t, raw)
	require.False(t, written.Enabled)
	require.False(t, store.templates[promptKey].Enabled)

	raw, err = server.Dispatch(context.Background(), "prompts/get", json.RawMessage(`{
		"id":"main/scoped",
		"cwd":"/repo/a"
	}`))
	require.NoError(t, err)
	got := decodePromptGet(t, raw)
	require.False(t, got.Enabled)

	raw, err = server.Dispatch(context.Background(), "prompts/list", json.RawMessage(`{"cwd":"/repo/a"}`))
	require.NoError(t, err)
	listed := decodePromptListByID(t, raw)
	require.False(t, listed[promptKey].Enabled)
}

func sectionPreviewTestSections() map[string]promptstore.PromptTemplateSection {
	return map[string]promptstore.PromptTemplateSection{
		"identity":    {ID: 1, TemplateID: 7, SectionKey: "identity", Region: "static", Ordinal: 0, Body: "Identity body", Enabled: true, TriggerType: "always"},
		"workflow":    {ID: 2, TemplateID: 7, SectionKey: "workflow", Region: "dynamic", Ordinal: 10, Body: "Workflow body", Enabled: true, TriggerType: "keyword"},
		"recall_sqlc": {ID: 3, TemplateID: 7, SectionKey: "recall_sqlc", Region: "dynamic", Ordinal: 20, Body: "Recall pack body must stay hidden", Enabled: true, TriggerType: "recall", RecallTopic: "sqlc-workflow"},
	}
}

func decodePromptListByID(t *testing.T, raw []byte) map[string]promptRPCItem {
	t.Helper()
	var decoded struct {
		Prompts []promptRPCItem `json:"prompts"`
	}
	require.NoError(t, json.Unmarshal(raw, &decoded))
	byID := map[string]promptRPCItem{}
	for _, item := range decoded.Prompts {
		byID[item.ID] = item
	}
	return byID
}

func decodePromptWrite(t *testing.T, raw []byte) promptRPCItem {
	t.Helper()
	var decoded struct {
		Prompt promptRPCItem `json:"prompt"`
	}
	require.NoError(t, json.Unmarshal(raw, &decoded))
	return decoded.Prompt
}

func decodePromptGet(t *testing.T, raw []byte) promptRPCItem {
	t.Helper()
	var decoded struct {
		Prompt promptRPCItem `json:"prompt"`
	}
	require.NoError(t, json.Unmarshal(raw, &decoded))
	return decoded.Prompt
}

func decodePromptAssetsByID(t *testing.T, raw []byte) map[string]map[string]any {
	t.Helper()
	var decoded struct {
		Prompts []map[string]any `json:"prompts"`
	}
	require.NoError(t, json.Unmarshal(raw, &decoded))
	byID := map[string]map[string]any{}
	for _, item := range decoded.Prompts {
		id, _ := item["id"].(string)
		if id != "" {
			byID[id] = item
		}
	}
	return byID
}

func writePromptJSON(t *testing.T, server *platformrpc.Server, raw string) {
	t.Helper()
	_, err := server.Dispatch(context.Background(), "prompts/write", json.RawMessage(raw))
	require.NoError(t, err)
}

func registeredThreadPromptProviders(t *testing.T, store promptstore.Store) map[string]contract.DynamicSectionProvider {
	t.Helper()
	reg := &captureDynamicProviders{items: map[string]contract.DynamicSectionProvider{}}
	require.NoError(t, threadprompt.RegisterProviders(reg, threadprompt.NewRuntimeCatalog(store, nil)))
	return reg.items
}

type captureDynamicProviders struct {
	items map[string]contract.DynamicSectionProvider
}

func (c *captureDynamicProviders) RegisterDynamicProvider(provider contract.DynamicSectionProvider) error {
	c.items[provider.SectionName()] = provider
	return nil
}

func promptAssetDraftForTest(draftKey, cwd, kind, status, card string) promptstore.PromptIntentDraft {
	now := time.Unix(1_700_000_000, 0).UTC()
	return promptstore.PromptIntentDraft{
		DraftKey:      draftKey,
		CWD:           cwd,
		Kind:          kind,
		RawInput:      "raw " + kind,
		SourceType:    "user_input",
		GeneratedCard: json.RawMessage(card),
		Confidence:    0.85,
		Status:        status,
		Scope:         "project",
		Issues:        json.RawMessage("[]"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}
