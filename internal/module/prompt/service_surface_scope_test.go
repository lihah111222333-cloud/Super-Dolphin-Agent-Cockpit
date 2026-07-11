package prompt

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/stretchr/testify/require"
)

func TestPromptsRPCListRequiresCWD(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	store.templates["main/other"] = scopedPromptTemplate("main/other", "/repo/b")

	server := platformrpc.NewServer(platformrpc.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(buildPromptHandlersWithService(newPromptService(store)).Handlers)

	_, err := server.Dispatch(context.Background(), "prompts/list", json.RawMessage(`{}`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "cwd is required")
	require.Empty(t, store.listFilters)
}

func TestPromptsRPCGetRequiresCWD(t *testing.T) {
	t.Parallel()

	const promptKey = "main/other-project"
	store := newInMemoryPromptStore()
	store.templates[promptKey] = scopedPromptTemplate(promptKey, "/repo/b")

	server := platformrpc.NewServer(platformrpc.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(buildPromptHandlersWithService(newPromptService(store)).Handlers)

	_, err := server.Dispatch(context.Background(), "prompts/get", json.RawMessage(`{"id":"main/other-project"}`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "cwd is required")
	require.Zero(t, store.getCalls)
}

func TestPromptWritePreservesIntentRecallTagFromOrdinaryEdit(t *testing.T) {
	t.Parallel()

	const promptKey = "main/knowledge/pricing"
	store := newInMemoryPromptStore()
	template := scopedPromptTemplate(promptKey, "/repo/a")
	template.ID = 7
	template.Tags = withPromptScopeTag(json.RawMessage(`["intent:recall","pricing"]`), "/repo/a")
	template.WhenToUse = "Use when answering pricing questions."
	store.templates[promptKey] = template
	store.sections[7] = map[string]TemplateSection{
		"recall_pricing": {TemplateID: 7, SectionKey: "recall_pricing", TriggerType: "recall", RecallTopic: "pricing", Body: "old pricing", Enabled: true},
	}
	svc := newPromptService(store)

	got, err := svc.WritePrompt(context.Background(), "/repo/a", PromptWriteRequest{
		ID:         promptKey,
		Name:       "Pricing Knowledge",
		Content:    "new pricing",
		ContentSet: true,
		Tags:       json.RawMessage(`[]`),
	})
	require.NoError(t, err)
	require.Contains(t, promptTags(got.Tags), "intent:recall")
	require.Contains(t, promptTags(store.templates[promptKey].Tags), "intent:recall")
	require.Equal(t, "new pricing", store.sections[7]["recall_pricing"].Body)
}

func TestPromptWriteInfersRecallIntentFromExistingRecallSection(t *testing.T) {
	t.Parallel()

	const promptKey = "main/knowledge/pricing"
	store := newInMemoryPromptStore()
	template := scopedPromptTemplate(promptKey, "/repo/a")
	template.ID = 7
	template.PromptText = ""
	template.Tags = withPromptScopeTag(json.RawMessage(`["pricing"]`), "/repo/a")
	template.WhenToUse = "Use when answering pricing questions."
	store.templates[promptKey] = template
	store.sections[7] = map[string]TemplateSection{
		"recall_pricing": {TemplateID: 7, SectionKey: "recall_pricing", TriggerType: "recall", RecallTopic: "pricing", Body: "old pricing", Enabled: true},
	}
	server := platformrpc.NewServer(platformrpc.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(buildPromptHandlersWithService(newPromptService(store), store).Handlers)

	_, err := server.Dispatch(context.Background(), "prompts/write", json.RawMessage(`{
		"id":"main/knowledge/pricing",
		"name":"Pricing Knowledge",
		"content":"new pricing",
		"cwd":"/repo/a",
		"tags":["pricing"]
	}`))
	require.NoError(t, err)
	require.Equal(t, "", store.templates[promptKey].PromptText)
	require.Equal(t, "new pricing", store.sections[7]["recall_pricing"].Body)
	require.Contains(t, promptTags(store.templates[promptKey].Tags), "intent:recall")

	raw, err := server.Dispatch(context.Background(), "prompt-assets/list", json.RawMessage(`{"cwd":"/repo/a"}`))
	require.NoError(t, err)
	assets := decodePromptAssetsByID(t, raw)
	require.Equal(t, "new pricing", assets[promptKey]["content"])
	require.Contains(t, assets[promptKey]["tags"], "intent:recall")
}

func TestPromptWriteInfersRecallIntentIgnoringDisabledDirectSections(t *testing.T) {
	t.Parallel()

	const promptKey = "main/knowledge/pricing"
	store := newInMemoryPromptStore()
	template := scopedPromptTemplate(promptKey, "/repo/a")
	template.ID = 7
	template.PromptText = ""
	template.Tags = withPromptScopeTag(json.RawMessage(`["pricing"]`), "/repo/a")
	store.templates[promptKey] = template
	store.sections[7] = map[string]TemplateSection{
		"workflow":       {TemplateID: 7, SectionKey: "workflow", TriggerType: "always", Body: "old direct body", Enabled: false},
		"recall_pricing": {TemplateID: 7, SectionKey: "recall_pricing", TriggerType: "recall", RecallTopic: "pricing", Body: "old pricing", Enabled: true},
	}
	server := platformrpc.NewServer(platformrpc.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(buildPromptHandlersWithService(newPromptService(store), store).Handlers)

	_, err := server.Dispatch(context.Background(), "prompts/write", json.RawMessage(`{
		"id":"main/knowledge/pricing",
		"name":"Pricing Knowledge",
		"content":"new pricing",
		"cwd":"/repo/a",
		"tags":["pricing"]
	}`))
	require.NoError(t, err)
	require.Contains(t, promptTags(store.templates[promptKey].Tags), "intent:recall")
	require.Equal(t, "new pricing", store.sections[7]["recall_pricing"].Body)
}

func TestPromptSectionsForTemplatesRejectsOutsideCWD(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	other := scopedPromptTemplate("main/other", "/repo/b")
	other.ID = 9
	store.templates[other.PromptKey] = other
	store.sections[9] = map[string]TemplateSection{
		"workflow": {TemplateID: 9, SectionKey: "workflow", Body: "other project content", Enabled: true},
	}
	svc := newPromptService(store)

	_, err := svc.ListPromptSectionsByTemplates(context.Background(), "/repo/a", []Template{other})
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside cwd scope")
	require.Zero(t, store.listSectionsByTemplateIDsCalls)
}

func TestPromptAssetsListHidesBuiltinRegistryAuthorWithoutSystemTag(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	registry := scopedPromptTemplate("main/registry", "/repo/a")
	registry.ID = 7
	registry.CreatedBy = "builtin.registry"
	registry.UpdatedBy = "builtin.registry"
	registry.Tags = withPromptScopeTag(json.RawMessage(`["intent:expert"]`), "/repo/a")
	store.templates[registry.PromptKey] = registry
	user := scopedPromptTemplate("main/user", "/repo/a")
	user.ID = 8
	user.CreatedBy = promptUpdatedBy
	user.UpdatedBy = promptUpdatedBy
	user.Tags = withPromptScopeTag(json.RawMessage(`["intent:expert"]`), "/repo/a")
	store.templates[user.PromptKey] = user

	server := platformrpc.NewServer(platformrpc.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(buildPromptHandlersWithService(newPromptService(store), store).Handlers)

	raw, err := server.Dispatch(context.Background(), "prompt-assets/list", json.RawMessage(`{"cwd":"/repo/a"}`))
	require.NoError(t, err)
	assets := decodePromptAssetsByID(t, raw)
	require.NotContains(t, assets, "main/registry")
	require.Contains(t, assets, "main/user")
}

func TestPromptAssetsRPCListHidesUserAssetWithoutExplicitScope(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	legacy := scopedPromptTemplate("main/legacy-noscope", "")
	legacy.ID = 7
	legacy.CreatedBy = promptUpdatedBy
	legacy.UpdatedBy = promptUpdatedBy
	legacy.Tags = json.RawMessage(`["intent:expert"]`)
	store.templates[legacy.PromptKey] = legacy
	scoped := scopedPromptTemplate("main/scoped", "/repo/a")
	scoped.ID = 8
	scoped.CreatedBy = promptUpdatedBy
	scoped.UpdatedBy = promptUpdatedBy
	scoped.Tags = withPromptScopeTag(json.RawMessage(`["intent:expert"]`), "/repo/a")
	store.templates[scoped.PromptKey] = scoped

	server := platformrpc.NewServer(platformrpc.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(buildPromptHandlersWithService(newPromptService(store), store).Handlers)

	raw, err := server.Dispatch(context.Background(), "prompt-assets/list", json.RawMessage(`{"cwd":"/repo/a"}`))
	require.NoError(t, err)
	assets := decodePromptAssetsByID(t, raw)
	require.NotContains(t, assets, "main/legacy-noscope")
	require.Contains(t, assets, "main/scoped")
}
