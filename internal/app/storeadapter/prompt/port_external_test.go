package promptadapter_test

import (
	"context"
	"encoding/json"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/module/prompt"
)

type externalPromptPreferenceReader struct{}

func (externalPromptPreferenceReader) GetValue(context.Context, string, string) (json.RawMessage, error) {
	return nil, nil
}

type externalPromptSharedFileReader struct{}

func (externalPromptSharedFileReader) GetContent(context.Context, string) (string, error) {
	return "", nil
}

type externalPromptTemplateStore struct{}

func (externalPromptTemplateStore) List(context.Context, prompt.ListFilter) ([]prompt.Template, error) {
	return nil, nil
}

func (externalPromptTemplateStore) Get(context.Context, string) (*prompt.Template, error) {
	return nil, nil
}

func (externalPromptTemplateStore) Delete(context.Context, string) error { return nil }

func (externalPromptTemplateStore) InsertVersion(context.Context, prompt.TemplateVersion) (int64, error) {
	return 0, nil
}

func (externalPromptTemplateStore) CreatePromptTemplate(
	context.Context,
	prompt.Template,
) (*prompt.Template, error) {
	return nil, nil
}

func (externalPromptTemplateStore) Upsert(context.Context, prompt.Template) (*prompt.Template, error) {
	return nil, nil
}

type externalPromptSectionStore struct{}

func (externalPromptSectionStore) ListSectionsByTemplateID(
	context.Context,
	int64,
) ([]prompt.TemplateSection, error) {
	return nil, nil
}

func (externalPromptSectionStore) ListSectionsByTemplateIDs(
	context.Context,
	[]int64,
) ([]prompt.TemplateSection, error) {
	return nil, nil
}

func (externalPromptSectionStore) ListRecallSections(context.Context, string) ([]prompt.TemplateSection, error) {
	return nil, nil
}

func (externalPromptSectionStore) ListDefaultRuleSections(
	context.Context,
	string,
) ([]prompt.TemplateSection, error) {
	return nil, nil
}

func (externalPromptSectionStore) UpsertSection(
	context.Context,
	prompt.TemplateSection,
) (*prompt.TemplateSection, error) {
	return nil, nil
}

func (externalPromptSectionStore) DeleteSection(context.Context, int64, string) error { return nil }

func (externalPromptSectionStore) UpsertRecallTopicTargetInCWD(
	context.Context,
	string,
	string,
	int64,
	string,
) error {
	return nil
}

type externalPromptIntentDraftStore struct{}

func (externalPromptIntentDraftStore) UpsertIntentDraft(
	context.Context,
	prompt.IntentDraft,
) (*prompt.IntentDraft, error) {
	return nil, nil
}

func (externalPromptIntentDraftStore) GetIntentDraft(
	context.Context,
	string,
	string,
) (*prompt.IntentDraft, error) {
	return nil, nil
}

func (externalPromptIntentDraftStore) ListIntentDrafts(
	context.Context,
	prompt.IntentDraftListFilter,
) ([]prompt.IntentDraft, error) {
	return nil, nil
}

func (externalPromptIntentDraftStore) UpdateIntentDraftStatus(
	context.Context,
	string,
	string,
	string,
) (*prompt.IntentDraft, error) {
	return nil, nil
}

func (externalPromptIntentDraftStore) LockRecallTopicInCWD(context.Context, string, string) error {
	return nil
}

type externalPromptStore struct {
	externalPromptTemplateStore
	externalPromptSectionStore
	externalPromptIntentDraftStore
}

func (s externalPromptStore) WithTx(ctx context.Context, fn func(prompt.Store) error) error {
	return fn(s)
}

var _ prompt.PreferenceReader = externalPromptPreferenceReader{}
var _ prompt.SharedFileReader = externalPromptSharedFileReader{}
var _ prompt.Store = externalPromptStore{}

var _ = prompt.ListFilter{AgentKey: "agent", Keyword: "keyword", CWD: "/repo", Limit: 41}
var _ = prompt.Template{
	ID: 1, PromptKey: "key", Title: "title", AgentKey: "agent", ToolName: "tool", PromptText: "text",
	WhenToUse: "when", Variables: json.RawMessage(`{}`), Tags: json.RawMessage(`[]`), Enabled: true,
	ManuallyEdited: true, MatchWhen: json.RawMessage(`{}`), Priority: 41, CreatedBy: "creator", UpdatedBy: "updater",
	CreatedAt: time.Time{}, UpdatedAt: time.Time{}, Description: "description",
}
var _ = prompt.TemplateSection{
	ID: 1, TemplateID: 2, SectionKey: "section", Region: "static", Ordinal: 3, Body: "body",
	EnableWhen: json.RawMessage(`{}`), Enabled: true, TriggerType: "always", RecallTopic: "topic",
	TemplatePromptKey: "key", TemplateTitle: "title", TemplateDescription: "description",
	TemplateWhenToUse: "when", TemplateTags: json.RawMessage(`[]`), CreatedAt: time.Time{}, UpdatedAt: time.Time{},
}
var _ = prompt.TemplateVersion{
	ID: 1, PromptKey: "key", Title: "title", AgentKey: "agent", ToolName: "tool", PromptText: "text",
	Variables: json.RawMessage(`{}`), Tags: json.RawMessage(`[]`), Description: "description", Enabled: true,
	CreatedBy: "creator", UpdatedBy: "updater", SourceUpdatedAt: new(time.Time), CreatedAt: time.Time{}, ArchivedAt: time.Time{},
}
var _ = prompt.IntentDraft{
	ID: 1, DraftKey: "draft", CWD: "/repo", Kind: "expert", RawInput: "input", SourceType: "text",
	SourceURL: "url", OriginHash: "hash", LicenseHint: "license", GeneratedCard: json.RawMessage(`{}`),
	Confidence: 0.9, Status: "draft", Scope: "cwd", Issues: json.RawMessage(`[]`),
	CreatedAt: time.Time{}, UpdatedAt: time.Time{},
}
var _ = prompt.IntentDraftListFilter{CWD: "/repo", Status: "draft", Limit: 41}
