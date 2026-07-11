package promptadapter

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/module/prompt"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
	"github.com/anthropic-ai/super-agent-v3/internal/store/uipreference"
	storeadaptertest "github.com/anthropic-ai/super-agent-v3/internal/testutil/storeadapter"
)

type promptStoreTestState struct {
	err             error
	withTx          func(context.Context, func(promptstore.Store) error) error
	templates       []promptstore.PromptTemplate
	template        *promptstore.PromptTemplate
	sections        []promptstore.PromptTemplateSection
	section         *promptstore.PromptTemplateSection
	drafts          []promptstore.PromptIntentDraft
	draft           *promptstore.PromptIntentDraft
	listFilter      promptstore.ListFilter
	templateInput   promptstore.PromptTemplate
	versionInput    promptstore.PromptTemplateVersion
	sectionInput    promptstore.PromptTemplateSection
	draftInput      promptstore.PromptIntentDraft
	draftListFilter promptstore.PromptIntentDraftListFilter
	templateIDs     []int64
	deleteCalls     int
}

type promptTemplateStoreTestDouble struct{ state *promptStoreTestState }

func (s *promptTemplateStoreTestDouble) List(
	_ context.Context,
	filter promptstore.ListFilter,
) ([]promptstore.PromptTemplate, error) {
	s.state.listFilter = filter
	return s.state.templates, s.state.err
}

func (s *promptTemplateStoreTestDouble) Get(context.Context, string) (*promptstore.PromptTemplate, error) {
	return s.state.template, s.state.err
}

func (s *promptTemplateStoreTestDouble) Delete(context.Context, string) error {
	s.state.deleteCalls++
	return s.state.err
}

func (s *promptTemplateStoreTestDouble) InsertVersion(
	_ context.Context,
	version promptstore.PromptTemplateVersion,
) (int64, error) {
	s.state.versionInput = version
	return 41, s.state.err
}

func (s *promptTemplateStoreTestDouble) CreatePromptTemplate(
	_ context.Context,
	template promptstore.PromptTemplate,
) (*promptstore.PromptTemplate, error) {
	s.state.templateInput = template
	return s.state.template, s.state.err
}

func (s *promptTemplateStoreTestDouble) Upsert(
	_ context.Context,
	template promptstore.PromptTemplate,
) (*promptstore.PromptTemplate, error) {
	s.state.templateInput = template
	return s.state.template, s.state.err
}

type promptSectionStoreTestDouble struct{ state *promptStoreTestState }

func (s *promptSectionStoreTestDouble) ListSectionsByTemplateID(
	context.Context,
	int64,
) ([]promptstore.PromptTemplateSection, error) {
	return s.state.sections, s.state.err
}

func (s *promptSectionStoreTestDouble) ListSectionsByTemplateIDs(
	_ context.Context,
	templateIDs []int64,
) ([]promptstore.PromptTemplateSection, error) {
	s.state.templateIDs = templateIDs
	return s.state.sections, s.state.err
}

func (s *promptSectionStoreTestDouble) ListRecallSections(
	context.Context,
	string,
) ([]promptstore.PromptTemplateSection, error) {
	return s.state.sections, s.state.err
}

func (s *promptSectionStoreTestDouble) ListDefaultRuleSections(
	context.Context,
	string,
) ([]promptstore.PromptTemplateSection, error) {
	return s.state.sections, s.state.err
}

func (s *promptSectionStoreTestDouble) UpsertSection(
	_ context.Context,
	section promptstore.PromptTemplateSection,
) (*promptstore.PromptTemplateSection, error) {
	s.state.sectionInput = section
	return s.state.section, s.state.err
}

func (s *promptSectionStoreTestDouble) DeleteSection(context.Context, int64, string) error {
	s.state.deleteCalls++
	return s.state.err
}

func (s *promptSectionStoreTestDouble) UpsertRecallTopicTargetInCWD(
	context.Context,
	string,
	string,
	int64,
	string,
) error {
	return s.state.err
}

type promptIntentDraftStoreTestDouble struct{ state *promptStoreTestState }

func (s *promptIntentDraftStoreTestDouble) UpsertIntentDraft(
	_ context.Context,
	draft promptstore.PromptIntentDraft,
) (*promptstore.PromptIntentDraft, error) {
	s.state.draftInput = draft
	return s.state.draft, s.state.err
}

func (s *promptIntentDraftStoreTestDouble) GetIntentDraft(
	context.Context,
	string,
	string,
) (*promptstore.PromptIntentDraft, error) {
	return s.state.draft, s.state.err
}

func (s *promptIntentDraftStoreTestDouble) ListIntentDrafts(
	_ context.Context,
	filter promptstore.PromptIntentDraftListFilter,
) ([]promptstore.PromptIntentDraft, error) {
	s.state.draftListFilter = filter
	return s.state.drafts, s.state.err
}

func (s *promptIntentDraftStoreTestDouble) UpdateIntentDraftStatus(
	context.Context,
	string,
	string,
	string,
) (*promptstore.PromptIntentDraft, error) {
	return s.state.draft, s.state.err
}

func (s *promptIntentDraftStoreTestDouble) LockRecallTopicInCWD(context.Context, string, string) error {
	return s.state.err
}

type promptStoreTestDouble struct {
	*promptTemplateStoreTestDouble
	*promptSectionStoreTestDouble
	*promptIntentDraftStoreTestDouble
	state *promptStoreTestState
}

func newPromptStoreTestDouble(state *promptStoreTestState) *promptStoreTestDouble {
	if state == nil {
		state = &promptStoreTestState{}
	}
	return &promptStoreTestDouble{
		promptTemplateStoreTestDouble:    &promptTemplateStoreTestDouble{state: state},
		promptSectionStoreTestDouble:     &promptSectionStoreTestDouble{state: state},
		promptIntentDraftStoreTestDouble: &promptIntentDraftStoreTestDouble{state: state},
		state:                            state,
	}
}

func (s *promptStoreTestDouble) WithTx(ctx context.Context, fn func(promptstore.Store) error) error {
	if s.state.withTx != nil {
		return s.state.withTx(ctx, fn)
	}
	if s.state.err != nil {
		return s.state.err
	}
	return fn(s)
}

var _ promptstore.Store = (*promptStoreTestDouble)(nil)

type promptPreferenceStoreTestDouble struct {
	value json.RawMessage
	err   error
}

func (s *promptPreferenceStoreTestDouble) GetValue(context.Context, string, string) (json.RawMessage, error) {
	return s.value, s.err
}

func (*promptPreferenceStoreTestDouble) Upsert(context.Context, uipreference.UpsertParams) error {
	return nil
}
func (*promptPreferenceStoreTestDouble) List(context.Context, string) ([]uipreference.UIPreference, error) {
	return nil, nil
}

type promptSharedFileStoreTestDouble struct {
	file *sharedfilestore.SharedFile
	err  error
}

func (s *promptSharedFileStoreTestDouble) Get(context.Context, string) (*sharedfilestore.SharedFile, error) {
	return s.file, s.err
}

func (*promptSharedFileStoreTestDouble) List(
	context.Context,
	sharedfilestore.ListFilter,
) ([]sharedfilestore.SharedFile, error) {
	return nil, nil
}

// TestPromptStoreAdapterProvidersRequireRoots 固定三个 required Store 输入都拒绝 nil/typed nil。
func TestPromptStoreAdapterProvidersRequireRoots(t *testing.T) {
	if _, err := providePromptStore(nil); !errors.Is(err, prompt.ErrStoreNotConfigured) {
		t.Fatalf("prompt Store nil error = %v", err)
	}
	var typedPrompt *promptStoreTestDouble
	if _, err := providePromptStore(typedPrompt); !errors.Is(err, prompt.ErrStoreNotConfigured) {
		t.Fatalf("prompt Store typed nil error = %v", err)
	}
	if _, err := providePromptPreferenceReader(nil); err == nil {
		t.Fatal("preference nil error = nil")
	}
	var typedPrefs *promptPreferenceStoreTestDouble
	if _, err := providePromptPreferenceReader(typedPrefs); err == nil {
		t.Fatal("preference typed nil error = nil")
	}
	if _, err := providePromptSharedFileReader(nil); err == nil {
		t.Fatal("shared file nil error = nil")
	}
	var typedShared *promptSharedFileStoreTestDouble
	if _, err := providePromptSharedFileReader(typedShared); err == nil {
		t.Fatal("shared file typed nil error = nil")
	}
}

// TestModuleOwnsPromptPorts 通过真实 Fx lifecycle 证明 prompt adapter module 提供三个端口。
func TestModuleOwnsPromptPorts(t *testing.T) {
	root := newPromptStoreTestDouble(nil)
	preferences := &promptPreferenceStoreTestDouble{}
	sharedFiles := &promptSharedFileStoreTestDouble{}
	var storePort prompt.Store
	var preferencePort prompt.PreferenceReader
	var sharedFilePort prompt.SharedFileReader
	app := fx.New(
		fx.NopLogger,
		fx.Provide(func() promptstore.Store { return root }),
		fx.Provide(func() uipreference.Store { return preferences }),
		fx.Provide(func() sharedfilestore.Reader { return sharedFiles }),
		Module,
		fx.Populate(&storePort, &preferencePort, &sharedFilePort),
	)
	if err := app.Err(); err != nil {
		t.Fatalf("fx.New: %v", err)
	}
	ctx := context.Background()
	if err := app.Start(ctx); err != nil {
		t.Fatalf("fx.Start: %v", err)
	}
	if err := app.Stop(ctx); err != nil {
		t.Fatalf("fx.Stop: %v", err)
	}
	if storePort == nil || preferencePort == nil || sharedFilePort == nil {
		t.Fatalf("prompt ports = (%T, %T, %T), want all non-nil", storePort, preferencePort, sharedFilePort)
	}
}

// TestPromptStoreAdapterRejectsNilTxCallback 固定 nil callback 在调用底层 Store 前失败。
func TestPromptStoreAdapterRejectsNilTxCallback(t *testing.T) {
	called := false
	root := newPromptStoreTestDouble(&promptStoreTestState{
		withTx: func(context.Context, func(promptstore.Store) error) error {
			called = true
			return nil
		},
	})
	port := requirePromptStorePort(t, root)
	err := port.WithTx(context.Background(), nil)
	if !errors.Is(err, prompt.ErrStoreTxCallbackRequired) || called {
		t.Fatalf("WithTx(nil) = %v, Store called=%v", err, called)
	}
}

// TestPromptStoreAdapterRejectsNilTxStore 固定 nil/typed nil tx Store 不进入领域 callback。
func TestPromptStoreAdapterRejectsNilTxStore(t *testing.T) {
	stores := map[string]promptstore.Store{"nil": nil}
	var typedNil *promptStoreTestDouble
	stores["typed_nil"] = typedNil
	for name, txStore := range stores {
		t.Run(name, func(t *testing.T) {
			callbackCalled := false
			root := newPromptStoreTestDouble(&promptStoreTestState{
				withTx: func(_ context.Context, fn func(promptstore.Store) error) error { return fn(txStore) },
			})
			err := requirePromptStorePort(t, root).WithTx(context.Background(), func(prompt.Store) error {
				callbackCalled = true
				return nil
			})
			if !errors.Is(err, prompt.ErrStoreNotConfigured) || callbackCalled {
				t.Fatalf("WithTx = %v, callback called=%v", err, callbackCalled)
			}
		})
	}
}

// TestPromptStoreAdapterUsesSameTxStore 固定领域 callback 操作底层传入的同一 tx Store。
func TestPromptStoreAdapterUsesSameTxStore(t *testing.T) {
	txState := &promptStoreTestState{}
	txStore := newPromptStoreTestDouble(txState)
	root := newPromptStoreTestDouble(&promptStoreTestState{
		withTx: func(_ context.Context, fn func(promptstore.Store) error) error { return fn(txStore) },
	})
	err := requirePromptStorePort(t, root).WithTx(context.Background(), func(tx prompt.Store) error {
		return tx.Delete(context.Background(), "prompt/key")
	})
	if err != nil || txState.deleteCalls != 1 {
		t.Fatalf("WithTx = %v, tx delete calls=%d", err, txState.deleteCalls)
	}
}

func requirePromptStorePort(t *testing.T, store promptstore.Store) prompt.Store {
	t.Helper()
	port, err := providePromptStore(store)
	if err != nil {
		t.Fatalf("provide prompt Store: %v", err)
	}
	return port
}

// TestPromptStoreAdapterFieldCoverage 用 one-hot 输入覆盖全部领域与 Store DTO 字段。
func TestPromptStoreAdapterFieldCoverage(t *testing.T) {
	t.Run("list_filter", func(t *testing.T) { assertPromptFieldsMap(t, toStorePromptListFilter) })
	t.Run("template_to_store", func(t *testing.T) { assertPromptFieldsMap(t, toStorePromptTemplate) })
	t.Run("template_from_store", func(t *testing.T) { assertPromptFieldsMap(t, fromStorePromptTemplate) })
	t.Run("section_to_store", func(t *testing.T) { assertPromptFieldsMap(t, toStorePromptTemplateSection) })
	t.Run("section_from_store", func(t *testing.T) { assertPromptFieldsMap(t, fromStorePromptTemplateSection) })
	t.Run("version_to_store", func(t *testing.T) { assertPromptFieldsMap(t, toStorePromptTemplateVersion) })
	t.Run("version_from_store", func(t *testing.T) { assertPromptFieldsMap(t, fromStorePromptTemplateVersion) })
	t.Run("draft_to_store", func(t *testing.T) { assertPromptFieldsMap(t, toStorePromptIntentDraft) })
	t.Run("draft_from_store", func(t *testing.T) { assertPromptFieldsMap(t, fromStorePromptIntentDraft) })
	t.Run("draft_filter", func(t *testing.T) { assertPromptFieldsMap(t, toStorePromptIntentDraftListFilter) })
}

func assertPromptFieldsMap[Source, Target any](t *testing.T, mapper func(Source) Target) {
	t.Helper()
	storeadaptertest.AssertFieldsMapE(t, func(source Source) (Target, error) { return mapper(source), nil })
}

// TestPromptStoreAdapterCopiesMutableFields 固定 RawMessage 与时间指针双向隔离。
func TestPromptStoreAdapterCopiesMutableFields(t *testing.T) {
	t.Run("template", testPromptTemplateCopies)
	t.Run("section", testPromptSectionCopies)
	t.Run("version", testPromptVersionCopies)
	t.Run("intent_draft", testPromptIntentDraftCopies)
}

func testPromptTemplateCopies(t *testing.T) {
	domain := prompt.Template{Variables: json.RawMessage(`{"a":1}`), Tags: json.RawMessage(`[]`), MatchWhen: json.RawMessage(`{}`)}
	stored := toStorePromptTemplate(domain)
	stored.Variables[0], stored.Tags[0], stored.MatchWhen[0] = '[', '{', '['
	if string(domain.Variables) != `{"a":1}` || string(domain.Tags) != `[]` || string(domain.MatchWhen) != `{}` {
		t.Fatalf("domain template shared with Store: %#v", domain)
	}
	source := promptstore.PromptTemplate{Variables: json.RawMessage(`{"a":1}`), Tags: json.RawMessage(`[]`), MatchWhen: json.RawMessage(`{}`)}
	mapped := fromStorePromptTemplate(source)
	mapped.Variables[0], mapped.Tags[0], mapped.MatchWhen[0] = '[', '{', '['
	if string(source.Variables) != `{"a":1}` || string(source.Tags) != `[]` || string(source.MatchWhen) != `{}` {
		t.Fatalf("Store template shared with domain: %#v", source)
	}
}

func testPromptSectionCopies(t *testing.T) {
	domain := prompt.TemplateSection{EnableWhen: json.RawMessage(`{}`), TemplateTags: json.RawMessage(`[]`)}
	stored := toStorePromptTemplateSection(domain)
	stored.EnableWhen[0], stored.TemplateTags[0] = '[', '{'
	if string(domain.EnableWhen) != `{}` || string(domain.TemplateTags) != `[]` {
		t.Fatalf("domain section shared with Store: %#v", domain)
	}
	source := promptstore.PromptTemplateSection{EnableWhen: json.RawMessage(`{}`), TemplateTags: json.RawMessage(`[]`)}
	mapped := fromStorePromptTemplateSection(source)
	mapped.EnableWhen[0], mapped.TemplateTags[0] = '[', '{'
	if string(source.EnableWhen) != `{}` || string(source.TemplateTags) != `[]` {
		t.Fatalf("Store section shared with domain: %#v", source)
	}
}

func testPromptVersionCopies(t *testing.T) {
	updatedAt := time.Date(2026, time.July, 11, 1, 2, 3, 0, time.UTC)
	domain := prompt.TemplateVersion{Variables: json.RawMessage(`{}`), Tags: json.RawMessage(`[]`), SourceUpdatedAt: &updatedAt}
	stored := toStorePromptTemplateVersion(domain)
	stored.Variables[0], stored.Tags[0], *stored.SourceUpdatedAt = '[', '{', updatedAt.Add(time.Hour)
	if string(domain.Variables) != `{}` || string(domain.Tags) != `[]` || !domain.SourceUpdatedAt.Equal(updatedAt) {
		t.Fatalf("domain version shared with Store: %#v", domain)
	}
	source := promptstore.PromptTemplateVersion{Variables: json.RawMessage(`{}`), Tags: json.RawMessage(`[]`), SourceUpdatedAt: &updatedAt}
	mapped := fromStorePromptTemplateVersion(source)
	mapped.Variables[0], mapped.Tags[0], *mapped.SourceUpdatedAt = '[', '{', updatedAt.Add(time.Hour)
	if string(source.Variables) != `{}` || string(source.Tags) != `[]` || !source.SourceUpdatedAt.Equal(updatedAt) {
		t.Fatalf("Store version shared with domain: %#v", source)
	}
}

func testPromptIntentDraftCopies(t *testing.T) {
	domain := prompt.IntentDraft{GeneratedCard: json.RawMessage(`{}`), Issues: json.RawMessage(`[]`)}
	stored := toStorePromptIntentDraft(domain)
	stored.GeneratedCard[0], stored.Issues[0] = '[', '{'
	if string(domain.GeneratedCard) != `{}` || string(domain.Issues) != `[]` {
		t.Fatalf("domain draft shared with Store: %#v", domain)
	}
	source := promptstore.PromptIntentDraft{GeneratedCard: json.RawMessage(`{}`), Issues: json.RawMessage(`[]`)}
	mapped := fromStorePromptIntentDraft(source)
	mapped.GeneratedCard[0], mapped.Issues[0] = '[', '{'
	if string(source.GeneratedCard) != `{}` || string(source.Issues) != `[]` {
		t.Fatalf("Store draft shared with domain: %#v", source)
	}
}

// TestPromptStoreAdapterCopiesTemplateIDs 固定批量输入切片不与 Store 共享 backing array。
func TestPromptStoreAdapterCopiesTemplateIDs(t *testing.T) {
	state := &promptStoreTestState{}
	port := requirePromptStorePort(t, newPromptStoreTestDouble(state))
	ids := []int64{1, 2, 3}
	if _, err := port.ListSectionsByTemplateIDs(context.Background(), ids); err != nil {
		t.Fatalf("ListSectionsByTemplateIDs: %v", err)
	}
	state.templateIDs[0] = 9
	if ids[0] != 1 {
		t.Fatalf("template IDs shared with Store: %v", ids)
	}
}

// TestPromptStoreAdapterReturnsIndependentLists 固定三类返回列表及其 RawMessage 不共享 Store 所有权。
func TestPromptStoreAdapterReturnsIndependentLists(t *testing.T) {
	state := &promptStoreTestState{
		templates: []promptstore.PromptTemplate{{ID: 1, Tags: json.RawMessage(`[]`)}},
		sections:  []promptstore.PromptTemplateSection{{ID: 2, EnableWhen: json.RawMessage(`{}`)}},
		drafts:    []promptstore.PromptIntentDraft{{ID: 3, Issues: json.RawMessage(`[]`)}},
	}
	port := requirePromptStorePort(t, newPromptStoreTestDouble(state))
	templates, err := port.List(context.Background(), prompt.ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	sections, err := port.ListSectionsByTemplateID(context.Background(), 1)
	if err != nil {
		t.Fatalf("ListSectionsByTemplateID: %v", err)
	}
	drafts, err := port.ListIntentDrafts(context.Background(), prompt.IntentDraftListFilter{})
	if err != nil {
		t.Fatalf("ListIntentDrafts: %v", err)
	}
	templates[0].ID, sections[0].ID, drafts[0].ID = 9, 9, 9
	templates[0].Tags[0], sections[0].EnableWhen[0], drafts[0].Issues[0] = '{', '[', '{'
	if state.templates[0].ID != 1 || state.sections[0].ID != 2 || state.drafts[0].ID != 3 ||
		string(state.templates[0].Tags) != `[]` || string(state.sections[0].EnableWhen) != `{}` ||
		string(state.drafts[0].Issues) != `[]` {
		t.Fatalf("Store lists mutated: %#v %#v %#v", state.templates, state.sections, state.drafts)
	}
}

// TestPromptStoreAdapterPreservesNilResults 固定现有 nil,nil 指针结果语义不变。
func TestPromptStoreAdapterPreservesNilResults(t *testing.T) {
	port := requirePromptStorePort(t, newPromptStoreTestDouble(nil))
	tests := map[string]func() (bool, error){
		"get": func() (bool, error) {
			value, err := port.Get(context.Background(), "key")
			return value == nil, err
		},
		"create": func() (bool, error) {
			value, err := port.CreatePromptTemplate(context.Background(), prompt.Template{})
			return value == nil, err
		},
		"upsert": func() (bool, error) {
			value, err := port.Upsert(context.Background(), prompt.Template{})
			return value == nil, err
		},
		"section": func() (bool, error) {
			value, err := port.UpsertSection(context.Background(), prompt.TemplateSection{})
			return value == nil, err
		},
		"draft_upsert": func() (bool, error) {
			value, err := port.UpsertIntentDraft(context.Background(), prompt.IntentDraft{})
			return value == nil, err
		},
		"draft_get": func() (bool, error) {
			value, err := port.GetIntentDraft(context.Background(), "/repo", "draft")
			return value == nil, err
		},
		"draft_status": func() (bool, error) {
			value, err := port.UpdateIntentDraftStatus(context.Background(), "/repo", "draft", "ready")
			return value == nil, err
		},
	}
	for name, run := range tests {
		t.Run(name, func(t *testing.T) {
			isNil, err := run()
			if !isNil || err != nil {
				t.Fatalf("result nil = %v, error = %v, want (true, nil)", isNil, err)
			}
		})
	}
}

// TestPromptAuxiliaryAdaptersPreserveSemantics 固定偏好复制和 shared nil-file 行为。
func TestPromptAuxiliaryAdaptersPreserveSemantics(t *testing.T) {
	value := json.RawMessage(`{"theme":"dark"}`)
	prefs, err := providePromptPreferenceReader(&promptPreferenceStoreTestDouble{value: value})
	if err != nil {
		t.Fatalf("provide preference reader: %v", err)
	}
	got, err := prefs.GetValue(context.Background(), "/repo", "theme")
	if err != nil {
		t.Fatalf("GetValue: %v", err)
	}
	got[0] = '['
	if string(value) != `{"theme":"dark"}` {
		t.Fatalf("preference RawMessage shared: %s", value)
	}
	shared, err := providePromptSharedFileReader(&promptSharedFileStoreTestDouble{})
	if err != nil {
		t.Fatalf("provide shared reader: %v", err)
	}
	content, err := shared.GetContent(context.Background(), "missing.md")
	if err != nil || content != "" {
		t.Fatalf("GetContent nil file = (%q, %v)", content, err)
	}
}

// TestPromptAdaptersPreserveErrors 固定 Store、Preference 与 SharedFile 错误对象身份。
func TestPromptAdaptersPreserveErrors(t *testing.T) {
	wantErr := errors.New("prompt adapter Store failed")
	store := requirePromptStorePort(t, newPromptStoreTestDouble(&promptStoreTestState{err: wantErr}))
	for name, run := range promptFailingOperations(store) {
		t.Run(name, func(t *testing.T) {
			gotErr := run()
			if gotErr != wantErr || !errors.Is(gotErr, wantErr) {
				t.Fatalf("error = %v, want identical %v", gotErr, wantErr)
			}
		})
	}
	prefs, err := providePromptPreferenceReader(&promptPreferenceStoreTestDouble{err: wantErr})
	if err != nil {
		t.Fatalf("provide preference reader: %v", err)
	}
	if _, err := prefs.GetValue(context.Background(), "/repo", "key"); err != wantErr || !errors.Is(err, wantErr) {
		t.Fatalf("preference error = %v", err)
	}
	shared, err := providePromptSharedFileReader(&promptSharedFileStoreTestDouble{err: wantErr})
	if err != nil {
		t.Fatalf("provide shared reader: %v", err)
	}
	if _, err := shared.GetContent(context.Background(), "file"); err != wantErr || !errors.Is(err, wantErr) {
		t.Fatalf("shared error = %v", err)
	}
}

func promptFailingOperations(store prompt.Store) map[string]func() error {
	ctx := context.Background()
	return map[string]func() error{
		"with_tx":        func() error { return store.WithTx(ctx, func(prompt.Store) error { return nil }) },
		"list":           func() error { _, err := store.List(ctx, prompt.ListFilter{}); return err },
		"get":            func() error { _, err := store.Get(ctx, "key"); return err },
		"delete":         func() error { return store.Delete(ctx, "key") },
		"version":        func() error { _, err := store.InsertVersion(ctx, prompt.TemplateVersion{}); return err },
		"create":         func() error { _, err := store.CreatePromptTemplate(ctx, prompt.Template{}); return err },
		"upsert":         func() error { _, err := store.Upsert(ctx, prompt.Template{}); return err },
		"list_section":   func() error { _, err := store.ListSectionsByTemplateID(ctx, 1); return err },
		"list_sections":  func() error { _, err := store.ListSectionsByTemplateIDs(ctx, []int64{1}); return err },
		"recall":         func() error { _, err := store.ListRecallSections(ctx, "/repo"); return err },
		"default_rule":   func() error { _, err := store.ListDefaultRuleSections(ctx, "/repo"); return err },
		"upsert_section": func() error { _, err := store.UpsertSection(ctx, prompt.TemplateSection{}); return err },
		"delete_section": func() error { return store.DeleteSection(ctx, 1, "key") },
		"recall_target":  func() error { return store.UpsertRecallTopicTargetInCWD(ctx, "/repo", "topic", 1, "key") },
		"upsert_draft":   func() error { _, err := store.UpsertIntentDraft(ctx, prompt.IntentDraft{}); return err },
		"get_draft":      func() error { _, err := store.GetIntentDraft(ctx, "/repo", "draft"); return err },
		"list_drafts":    func() error { _, err := store.ListIntentDrafts(ctx, prompt.IntentDraftListFilter{}); return err },
		"draft_status":   func() error { _, err := store.UpdateIntentDraftStatus(ctx, "/repo", "draft", "ready"); return err },
		"lock_recall":    func() error { return store.LockRecallTopicInCWD(ctx, "/repo", "topic") },
	}
}
