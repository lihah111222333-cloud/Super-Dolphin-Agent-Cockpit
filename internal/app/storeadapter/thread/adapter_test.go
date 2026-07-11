package threadadapter

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"go.uber.org/fx"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/thread"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/threadprompt"
	bindingstore "github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/binding"
	promptstore "github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/prompt"
	threadstore "github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/thread"
	storeadaptertest "github.com/lihah111222333-cloud/super-dolphin-agent/internal/testutil/storeadapter"
)

// promptSnapshotMappingFixture 只归一化两侧命名不同但结构相同的 Boundary，避免放宽共享 helper。
type promptSnapshotMappingFixture struct {
	DisplayName           string
	BaseInstructions      string
	Boundary              *promptBoundaryMappingFixture
	DeveloperInstructions string
	Provider              string
	Version               int
	Hash                  string
	SectionSnapshot       map[string]string
	Generation            uint64
}

type promptBoundaryMappingFixture struct {
	CachedPrefix string
	UncachedTail string
}

func TestThreadStoreAdapterFieldCoverage(t *testing.T) {
	t.Parallel()

	storeadaptertest.AssertFieldsMap(t, func(row threadstore.Thread) thread.ThreadRecord { return *threadRecordFromStore(&row) })
	storeadaptertest.AssertFieldsMap(t, threadUpsertToStore)
	storeadaptertest.AssertFieldsMap(t, threadStatusUpdateToStore)
	storeadaptertest.AssertFieldsMap(t, mapPromptSnapshotFixtureToStore)
	storeadaptertest.AssertFieldsMap(t, mapPromptSnapshotFixtureFromStore)
	finishedAt := int64(41)
	promptVersionID := int64(42)
	record := threadstore.Thread{
		ConfigOverride:  []byte{1, 2},
		FinishedAt:      &finishedAt,
		PromptVersionID: &promptVersionID,
	}
	snapshot := thread.PromptSnapshotRecord{SectionSnapshot: map[string]string{"existing": "value"}}
	if _, err := provideThreadStoreAdapter(nil); err == nil || !strings.Contains(err.Error(), "thread store") {
		t.Fatalf("provideThreadStoreAdapter(nil) error = %v, want thread store context", err)
	}
	assertThreadAdapterClonesMutableFields(t, record, snapshot)
}

func mapPromptSnapshotFixtureToStore(row promptSnapshotMappingFixture) promptSnapshotMappingFixture {
	stored := promptSnapshotToStore(thread.PromptSnapshotRecord{
		DisplayName:           row.DisplayName,
		BaseInstructions:      row.BaseInstructions,
		Boundary:              promptBoundaryRecordFromFixture(row.Boundary),
		DeveloperInstructions: row.DeveloperInstructions,
		Provider:              row.Provider,
		Version:               row.Version,
		Hash:                  row.Hash,
		SectionSnapshot:       row.SectionSnapshot,
		Generation:            row.Generation,
	})
	return promptSnapshotFixtureFromStore(stored)
}

func mapPromptSnapshotFixtureFromStore(row promptSnapshotMappingFixture) promptSnapshotMappingFixture {
	domain := promptSnapshotFromStore(&threadstore.PromptSnapshot{
		DisplayName:           row.DisplayName,
		BaseInstructions:      row.BaseInstructions,
		Boundary:              promptBoundaryStoreFromFixture(row.Boundary),
		DeveloperInstructions: row.DeveloperInstructions,
		Provider:              row.Provider,
		Version:               row.Version,
		Hash:                  row.Hash,
		SectionSnapshot:       row.SectionSnapshot,
		Generation:            row.Generation,
	})
	return promptSnapshotFixtureFromDomain(*domain)
}

func promptSnapshotFixtureFromStore(row threadstore.PromptSnapshot) promptSnapshotMappingFixture {
	return promptSnapshotMappingFixture{
		DisplayName:           row.DisplayName,
		BaseInstructions:      row.BaseInstructions,
		Boundary:              promptBoundaryFixtureFromStore(row.Boundary),
		DeveloperInstructions: row.DeveloperInstructions,
		Provider:              row.Provider,
		Version:               row.Version,
		Hash:                  row.Hash,
		SectionSnapshot:       row.SectionSnapshot,
		Generation:            row.Generation,
	}
}

func promptSnapshotFixtureFromDomain(row thread.PromptSnapshotRecord) promptSnapshotMappingFixture {
	return promptSnapshotMappingFixture{
		DisplayName:           row.DisplayName,
		BaseInstructions:      row.BaseInstructions,
		Boundary:              promptBoundaryFixtureFromRecord(row.Boundary),
		DeveloperInstructions: row.DeveloperInstructions,
		Provider:              row.Provider,
		Version:               row.Version,
		Hash:                  row.Hash,
		SectionSnapshot:       row.SectionSnapshot,
		Generation:            row.Generation,
	}
}

func promptBoundaryRecordFromFixture(row *promptBoundaryMappingFixture) *thread.PromptBoundaryRecord {
	if row == nil {
		return nil
	}
	return &thread.PromptBoundaryRecord{CachedPrefix: row.CachedPrefix, UncachedTail: row.UncachedTail}
}

func promptBoundaryStoreFromFixture(row *promptBoundaryMappingFixture) *threadstore.PromptBoundary {
	if row == nil {
		return nil
	}
	return &threadstore.PromptBoundary{CachedPrefix: row.CachedPrefix, UncachedTail: row.UncachedTail}
}

func promptBoundaryFixtureFromRecord(row *thread.PromptBoundaryRecord) *promptBoundaryMappingFixture {
	if row == nil {
		return nil
	}
	return &promptBoundaryMappingFixture{CachedPrefix: row.CachedPrefix, UncachedTail: row.UncachedTail}
}

func promptBoundaryFixtureFromStore(row *threadstore.PromptBoundary) *promptBoundaryMappingFixture {
	if row == nil {
		return nil
	}
	return &promptBoundaryMappingFixture{CachedPrefix: row.CachedPrefix, UncachedTail: row.UncachedTail}
}

func TestThreadBindingStoreAdapterFieldCoverage(t *testing.T) {
	t.Parallel()

	storeadaptertest.AssertFieldsMap(t, func(row bindingstore.Binding) thread.BindingRecord { return *bindingRecordFromStore(&row) })
	storeadaptertest.AssertFieldsMap(t, bindingUpsertToStore)
	storeadaptertest.AssertFieldsMap(t, bindingSessionUUIDUpdateToStore)
	storeadaptertest.AssertFieldsMap(t, bindingProviderThreadIDUpdateToStore)
	storeadaptertest.AssertFieldsMap(t, bindingArchiveUpdateToStore)
	storeadaptertest.AssertFieldsMap(t, bindingCWDUpdateToStore)
	if _, err := provideThreadBindingStoreAdapter(nil); err == nil || !strings.Contains(err.Error(), "binding store") {
		t.Fatalf("provideThreadBindingStoreAdapter(nil) error = %v, want binding store context", err)
	}
}

func TestThreadPromptStoreAdapterImplementsPort(t *testing.T) {
	t.Parallel()

	storeadaptertest.AssertFieldsMap(t, func(row promptstore.PromptTemplate) threadprompt.PromptTemplate {
		return *threadPromptTemplateFromStore(&row)
	})
	storeadaptertest.AssertFieldsMap(t, threadPromptSectionFromStore)
	storeadaptertest.AssertFieldsMap(t, threadPromptVersionToStore)
	storeadaptertest.AssertFieldsMap(t, threadPromptListFilterToStore)
	template := promptstore.PromptTemplate{Tags: []byte{1, 2}}
	convertedTemplate := threadPromptTemplateFromStore(&template)
	port, err := provideThreadPromptStoreAdapter(threadPromptStoreParams{})
	if err != nil || port != nil {
		t.Fatalf("provideThreadPromptStoreAdapter(empty) = (%#v, %v), want optional nil", port, err)
	}
	convertedTemplate.Tags[0] ^= 0xff
	if reflect.DeepEqual(convertedTemplate.Tags, template.Tags) {
		t.Fatal("prompt template JSON shares Store backing bytes")
	}
}

func TestThreadPromptCatalogAdapterImplementsPort(t *testing.T) {
	t.Parallel()

	storeadaptertest.AssertFieldsMap(t, threadPromptTemplateToThread)
	storeadaptertest.AssertFieldsMap(t, threadPromptSectionToThread)
	storeadaptertest.AssertFieldsMap(t, threadPromptVersionFromThread)
	storeadaptertest.AssertFieldsMap(t, threadPromptListFilterFromThread)
	template := threadprompt.PromptTemplate{PromptKey: "template", Tags: []byte{1, 2}}
	section := threadprompt.PromptTemplateSection{SectionKey: "section"}
	fake := &runtimePromptCatalogStub{templates: []threadprompt.PromptTemplate{template}, sections: []threadprompt.PromptTemplateSection{section}, canInsert: true}
	port, err := provideThreadPromptCatalog(fake)
	if err != nil {
		t.Fatalf("provideThreadPromptCatalog() error = %v", err)
	}
	assertPromptCatalogReadsAndWrites(t, port, fake)
	assertBuiltinOnlyPromptCatalog(t)
}

func TestThreadStoreOptionalCapabilities(t *testing.T) {
	t.Parallel()

	plain, err := provideThreadStoreAdapter(&plainThreadStore{})
	if err != nil {
		t.Fatalf("provide plain adapter: %v", err)
	}
	if _, ok := plain.(thread.ThreadPageReader); ok {
		t.Fatal("plain Store unexpectedly exposes optional page capability")
	}
	full, err := provideThreadStoreAdapter(&capableThreadStore{})
	if err != nil {
		t.Fatalf("provide capable adapter: %v", err)
	}
	assertThreadOptionalCapabilities(t, full)
	if _, err := provideThreadStoreAdapter(&partialThreadStore{}); err == nil || !strings.Contains(err.Error(), "provided together") {
		t.Fatalf("partial optional capabilities error = %v, want fail-fast", err)
	}
}

func TestModuleOwnsPortsAndRegistersPromptProviders(t *testing.T) {
	t.Parallel()

	registrar := &capturingDynamicRegistrar{}
	var threadPort thread.ThreadStore
	var bindingPort thread.BindingStore
	var promptPort thread.PromptCatalog
	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			func() contract.DynamicSectionRegistrar { return registrar },
			func() contract.BuiltinPromptRegistry { return builtinPromptRegistryStub{} },
			func() threadstore.Store { return &plainThreadStore{} },
			func() bindingstore.Store { return &bindingStoreStub{} },
		),
		Module,
		fx.Populate(&threadPort, &bindingPort, &promptPort),
	)
	if err := app.Err(); err != nil {
		t.Fatalf("fx.New() error = %v", err)
	}
	ctx := context.Background()
	if err := app.Start(ctx); err != nil {
		t.Fatalf("fx.Start() error = %v", err)
	}
	if err := app.Stop(ctx); err != nil {
		t.Fatalf("fx.Stop() error = %v", err)
	}
	if threadPort == nil || bindingPort == nil || promptPort == nil {
		t.Fatalf("Module ports = (%#v, %#v, %#v), want all nonnil", threadPort, bindingPort, promptPort)
	}
	want := []string{
		contract.DynamicSectionProjectDefaultRules,
		contract.DynamicSectionAvailableExperts,
		contract.DynamicSectionRecallCatalog,
	}
	if !reflect.DeepEqual(registrar.names, want) {
		t.Fatalf("registered provider order = %#v, want %#v", registrar.names, want)
	}
}

func assertPromptCatalogReadsAndWrites(t *testing.T, port thread.PromptCatalog, fake *runtimePromptCatalogStub) {
	t.Helper()
	rows, err := port.ListTemplates(context.Background(), thread.PromptListFilter{Keyword: "template"})
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListTemplates() = (%#v, %v)", rows, err)
	}
	if want := threadPromptTemplateToThread(fake.templates[0]); !reflect.DeepEqual(rows[0], want) {
		t.Fatalf("ListTemplates()[0] = %#v, want %#v", rows[0], want)
	}
	sections, err := port.ListSectionsByTemplateID(context.Background(), 1)
	if err != nil || len(sections) != 1 {
		t.Fatalf("ListSectionsByTemplateID() = (%#v, %v)", sections, err)
	}
	if want := threadPromptSectionToThread(fake.sections[0]); !reflect.DeepEqual(sections[0], want) {
		t.Fatalf("ListSectionsByTemplateID()[0] = %#v, want %#v", sections[0], want)
	}
	version := thread.PromptTemplateVersion{PromptKey: "version", Tags: []byte{3, 4}}
	if _, err := port.InsertVersion(context.Background(), version); err != nil {
		t.Fatalf("InsertVersion() error = %v", err)
	}
	if want := threadPromptVersionFromThread(version); !reflect.DeepEqual(fake.inserted, want) {
		t.Fatalf("InsertVersion() stored %#v, want %#v", fake.inserted, want)
	}
	if !port.CanInsertPromptVersion() {
		t.Fatal("writable runtime catalog reported read-only")
	}
}

func assertBuiltinOnlyPromptCatalog(t *testing.T) {
	t.Helper()
	runtime := provideThreadPromptRuntimeCatalog(threadPromptRuntimeCatalogParams{Builtin: builtinPromptRegistryStub{}})
	if runtime == nil || runtime.CanInsertPromptVersion() {
		t.Fatalf("builtin-only runtime catalog = %#v, want nonnil read-only", runtime)
	}
	port, err := provideThreadPromptCatalog(runtime)
	if err != nil || port == nil || port.CanInsertPromptVersion() {
		t.Fatalf("builtin-only Thread catalog = (%#v, %v), want nonnil read-only", port, err)
	}
	_, err = port.InsertVersion(context.Background(), thread.PromptTemplateVersion{PromptKey: "builtin"})
	if err == nil || !strings.Contains(err.Error(), "prompt store is required") {
		t.Fatalf("builtin-only InsertVersion() error = %v, want explicit capability error", err)
	}
}

func assertThreadOptionalCapabilities(t *testing.T, store thread.ThreadStore) {
	t.Helper()
	page, ok := store.(thread.ThreadPageReader)
	if !ok {
		t.Fatal("capable Store missing ThreadPageReader")
	}
	loaded, ok := store.(thread.LoadedThreadPageReader)
	if !ok {
		t.Fatal("capable Store missing LoadedThreadPageReader")
	}
	active, ok := store.(thread.ActiveThreadCounter)
	if !ok {
		t.Fatal("capable Store missing ActiveThreadCounter")
	}
	gotPage, _ := page.ListPage(context.Background(), contract.ThreadListPageParams{})
	gotLoaded, _ := loaded.ListLoadedPage(context.Background(), contract.ThreadListPageParams{})
	gotActive, _ := active.CountActive(context.Background())
	if gotPage.NextCursorThreadID != "all" || gotLoaded.NextCursorThreadID != "loaded" || gotActive != 7 {
		t.Fatalf("optional capability results = (%#v, %#v, %d)", gotPage, gotLoaded, gotActive)
	}
}

func assertThreadAdapterClonesMutableFields(t *testing.T, record threadstore.Thread, snapshot thread.PromptSnapshotRecord) {
	t.Helper()
	mappedRecord := threadRecordFromStore(&record)
	mappedRecord.ConfigOverride[0] ^= 0xff
	if reflect.DeepEqual(mappedRecord.ConfigOverride, record.ConfigOverride) {
		t.Fatal("thread ConfigOverride shares Store backing bytes")
	}
	assertAdapterInt64PointerClone(t, "FinishedAt", record.FinishedAt, mappedRecord.FinishedAt)
	assertAdapterInt64PointerClone(t, "PromptVersionID", record.PromptVersionID, mappedRecord.PromptVersionID)
	mappedSnapshot := promptSnapshotToStore(snapshot)
	mappedSnapshot.SectionSnapshot["new"] = "value"
	if _, ok := snapshot.SectionSnapshot["new"]; ok {
		t.Fatal("prompt snapshot section map shares domain backing map")
	}
}

func assertAdapterInt64PointerClone(t *testing.T, field string, source, target *int64) {
	t.Helper()
	if source == nil || target == nil || source == target || *source != *target {
		t.Fatalf("%s pointer clone = (%p:%v, %p:%v), want equal values at distinct addresses", field, source, source, target, target)
	}
	original := *source
	*target++
	if *source != original {
		t.Fatalf("mutating mapped %s changed Store source to %d", field, *source)
	}
}

type plainThreadStore struct{ threadstore.Store }

type bindingStoreStub struct{ bindingstore.Store }

type capableThreadStore struct{ threadstore.Store }

func (*capableThreadStore) ListPage(context.Context, contract.ThreadListPageParams) (contract.ThreadListPage, error) {
	return contract.ThreadListPage{NextCursorThreadID: "all"}, nil
}

func (*capableThreadStore) ListLoadedPage(context.Context, contract.ThreadListPageParams) (contract.ThreadListPage, error) {
	return contract.ThreadListPage{NextCursorThreadID: "loaded"}, nil
}

func (*capableThreadStore) CountActive(context.Context) (int64, error) {
	return 7, nil
}

type partialThreadStore struct{ threadstore.Store }

func (*partialThreadStore) ListPage(context.Context, contract.ThreadListPageParams) (contract.ThreadListPage, error) {
	return contract.ThreadListPage{}, nil
}

type runtimePromptCatalogStub struct {
	templates []threadprompt.PromptTemplate
	sections  []threadprompt.PromptTemplateSection
	inserted  threadprompt.PromptTemplateVersion
	canInsert bool
}

func (a *runtimePromptCatalogStub) ListTemplates(context.Context, threadprompt.RuntimeListFilter) ([]threadprompt.PromptTemplate, error) {
	return append([]threadprompt.PromptTemplate(nil), a.templates...), nil
}

func (*runtimePromptCatalogStub) GetTemplate(context.Context, string, string) (*threadprompt.PromptTemplate, error) {
	return nil, errors.New("unused")
}

func (a *runtimePromptCatalogStub) ListSectionsByTemplateID(context.Context, int64) ([]threadprompt.PromptTemplateSection, error) {
	return append([]threadprompt.PromptTemplateSection(nil), a.sections...), nil
}

func (*runtimePromptCatalogStub) ListRecallSections(context.Context, string) ([]threadprompt.PromptTemplateSection, error) {
	return nil, nil
}

func (*runtimePromptCatalogStub) ListDefaultRuleSections(context.Context, string) ([]threadprompt.PromptTemplateSection, error) {
	return nil, nil
}

func (a *runtimePromptCatalogStub) InsertVersion(_ context.Context, version threadprompt.PromptTemplateVersion) (int64, error) {
	a.inserted = version
	return 9, nil
}

func (a *runtimePromptCatalogStub) CanInsertPromptVersion() bool {
	return a.canInsert
}

type builtinPromptRegistryStub struct{}

func (builtinPromptRegistryStub) ListTemplates() []contract.BuiltinPromptTemplate {
	return []contract.BuiltinPromptTemplate{{ID: -1, PromptKey: "builtin", AgentKey: "main", Enabled: true}}
}

func (builtinPromptRegistryStub) GetTemplate(promptKey string) (contract.BuiltinPromptTemplate, bool) {
	return contract.BuiltinPromptTemplate{ID: -1, PromptKey: promptKey, AgentKey: "main", Enabled: true}, true
}

func (builtinPromptRegistryStub) SectionsByTemplateID(int64) []contract.BuiltinPromptSection {
	return nil
}

type capturingDynamicRegistrar struct {
	names []string
}

func (r *capturingDynamicRegistrar) RegisterDynamicProvider(provider contract.DynamicSectionProvider) error {
	r.names = append(r.names, provider.SectionName())
	return nil
}

func (*capturingDynamicRegistrar) UnregisterDynamicProvider(string) bool {
	return false
}

var _ threadprompt.RuntimePromptCatalog = (*runtimePromptCatalogStub)(nil)
