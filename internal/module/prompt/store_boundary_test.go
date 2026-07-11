package prompt

import (
	"context"

	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
)

// promptStoreForTest 把真实 Store 测试夹具集中适配为 prompt 领域端口。
func promptStoreForTest(store promptstore.Store) Store {
	return newTestPromptStoreAdapter(store)
}

type testPromptStoreAdapter struct {
	store promptstore.Store
	*testPromptTemplateStoreAdapter
	*testPromptSectionStoreAdapter
	*testPromptIntentDraftStoreAdapter
}

type testPromptTemplateStoreAdapter struct{ store promptstore.Store }
type testPromptSectionStoreAdapter struct{ store promptstore.Store }
type testPromptIntentDraftStoreAdapter struct{ store promptstore.Store }

func newTestPromptStoreAdapter(store promptstore.Store) testPromptStoreAdapter {
	return testPromptStoreAdapter{
		store:                             store,
		testPromptTemplateStoreAdapter:    &testPromptTemplateStoreAdapter{store: store},
		testPromptSectionStoreAdapter:     &testPromptSectionStoreAdapter{store: store},
		testPromptIntentDraftStoreAdapter: &testPromptIntentDraftStoreAdapter{store: store},
	}
}

var _ Store = testPromptStoreAdapter{}

func promptIntentDraftToStoreForTest(value IntentDraft) promptstore.PromptIntentDraft {
	return promptConvertStruct[promptstore.PromptIntentDraft](value)
}

func isRuntimeAssetTemplateForTest(value Template) bool {
	return promptstore.IsRuntimeAssetTemplate(promptConvertStruct[promptstore.PromptTemplate](value))
}

func (a testPromptTemplateStoreAdapter) List(ctx context.Context, filter ListFilter) ([]Template, error) {
	rows, err := a.store.List(ctx, promptConvertStruct[promptstore.ListFilter](filter))
	return promptConvertSlice[Template](rows), err
}

func (a testPromptStoreAdapter) WithTx(ctx context.Context, fn func(Store) error) error {
	return a.store.WithTx(ctx, func(store promptstore.Store) error { return fn(newTestPromptStoreAdapter(store)) })
}

func (a testPromptTemplateStoreAdapter) Get(ctx context.Context, key string) (*Template, error) {
	row, err := a.store.Get(ctx, key)
	return promptConvertPtr[Template](row), err
}

func (a testPromptTemplateStoreAdapter) Delete(ctx context.Context, key string) error {
	return a.store.Delete(ctx, key)
}

func (a testPromptTemplateStoreAdapter) InsertVersion(ctx context.Context, version TemplateVersion) (int64, error) {
	return a.store.InsertVersion(ctx, promptConvertStruct[promptstore.PromptTemplateVersion](version))
}

func (a testPromptTemplateStoreAdapter) CreatePromptTemplate(ctx context.Context, value Template) (*Template, error) {
	row, err := a.store.CreatePromptTemplate(ctx, promptConvertStruct[promptstore.PromptTemplate](value))
	return promptConvertPtr[Template](row), err
}

func (a testPromptTemplateStoreAdapter) Upsert(ctx context.Context, value Template) (*Template, error) {
	row, err := a.store.Upsert(ctx, promptConvertStruct[promptstore.PromptTemplate](value))
	return promptConvertPtr[Template](row), err
}

func (a testPromptSectionStoreAdapter) ListSectionsByTemplateID(ctx context.Context, id int64) ([]TemplateSection, error) {
	rows, err := a.store.ListSectionsByTemplateID(ctx, id)
	return promptConvertSlice[TemplateSection](rows), err
}

func (a testPromptSectionStoreAdapter) ListSectionsByTemplateIDs(ctx context.Context, ids []int64) ([]TemplateSection, error) {
	rows, err := a.store.ListSectionsByTemplateIDs(ctx, ids)
	return promptConvertSlice[TemplateSection](rows), err
}

func (a testPromptSectionStoreAdapter) ListRecallSections(ctx context.Context, cwd string) ([]TemplateSection, error) {
	rows, err := a.store.ListRecallSections(ctx, cwd)
	return promptConvertSlice[TemplateSection](rows), err
}

func (a testPromptSectionStoreAdapter) ListDefaultRuleSections(ctx context.Context, cwd string) ([]TemplateSection, error) {
	rows, err := a.store.ListDefaultRuleSections(ctx, cwd)
	return promptConvertSlice[TemplateSection](rows), err
}

func (a testPromptSectionStoreAdapter) UpsertSection(ctx context.Context, value TemplateSection) (*TemplateSection, error) {
	row, err := a.store.UpsertSection(ctx, promptConvertStruct[promptstore.PromptTemplateSection](value))
	return promptConvertPtr[TemplateSection](row), err
}

func (a testPromptSectionStoreAdapter) DeleteSection(ctx context.Context, id int64, key string) error {
	return a.store.DeleteSection(ctx, id, key)
}

func (a testPromptSectionStoreAdapter) UpsertRecallTopicTargetInCWD(
	ctx context.Context,
	cwd, topic string,
	id int64,
	key string,
) error {
	return a.store.UpsertRecallTopicTargetInCWD(ctx, cwd, topic, id, key)
}

func (a testPromptIntentDraftStoreAdapter) UpsertIntentDraft(ctx context.Context, value IntentDraft) (*IntentDraft, error) {
	row, err := a.store.UpsertIntentDraft(ctx, promptConvertStruct[promptstore.PromptIntentDraft](value))
	return promptConvertPtr[IntentDraft](row), err
}

func (a testPromptIntentDraftStoreAdapter) GetIntentDraft(ctx context.Context, cwd, key string) (*IntentDraft, error) {
	row, err := a.store.GetIntentDraft(ctx, cwd, key)
	return promptConvertPtr[IntentDraft](row), err
}

func (a testPromptIntentDraftStoreAdapter) ListIntentDrafts(ctx context.Context, filter IntentDraftListFilter) ([]IntentDraft, error) {
	rows, err := a.store.ListIntentDrafts(ctx, promptConvertStruct[promptstore.PromptIntentDraftListFilter](filter))
	return promptConvertSlice[IntentDraft](rows), err
}

func (a testPromptIntentDraftStoreAdapter) UpdateIntentDraftStatus(
	ctx context.Context,
	cwd, key, status string,
) (*IntentDraft, error) {
	row, err := a.store.UpdateIntentDraftStatus(ctx, cwd, key, status)
	return promptConvertPtr[IntentDraft](row), err
}

func (a testPromptSectionStoreAdapter) LockRecallTopicInCWD(ctx context.Context, cwd, topic string) error {
	return a.store.LockRecallTopicInCWD(ctx, cwd, topic)
}
