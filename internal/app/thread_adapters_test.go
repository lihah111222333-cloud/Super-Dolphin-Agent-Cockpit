package app

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/module/threadprompt"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
	"go.uber.org/fx"
)

func TestThreadStoreAdapterFieldCoverage(t *testing.T) {
	t.Parallel()

	assertAdapterMappingByField(t, func(row threadstore.Thread) thread.ThreadRecord { return *threadRecordFromStore(&row) })
	assertAdapterMappingByField(t, threadUpsertToStore)
	assertAdapterMappingByField(t, threadStatusUpdateToStore)
	assertAdapterMappingByField(t, promptSnapshotToStore)
	assertAdapterMappingByField(t, func(row threadstore.PromptSnapshot) thread.PromptSnapshotRecord {
		return *promptSnapshotFromStore(&row)
	})
	record := populatedAdapterValue[threadstore.Thread]()
	assertAdapterFieldsEqual(t, record, *threadRecordFromStore(&record))
	upsert := populatedAdapterValue[thread.ThreadUpsert]()
	assertAdapterFieldsEqual(t, upsert, threadUpsertToStore(upsert))
	status := populatedAdapterValue[thread.ThreadStatusUpdate]()
	assertAdapterFieldsEqual(t, status, threadStatusUpdateToStore(status))
	snapshot := populatedAdapterValue[thread.PromptSnapshotRecord]()
	storedSnapshot := promptSnapshotToStore(snapshot)
	assertAdapterFieldsEqual(t, snapshot, storedSnapshot)
	assertAdapterFieldsEqual(t, storedSnapshot, *promptSnapshotFromStore(&storedSnapshot))
	if _, err := provideThreadStoreAdapter(nil); err == nil || !strings.Contains(err.Error(), "thread store") {
		t.Fatalf("provideThreadStoreAdapter(nil) error = %v, want thread store context", err)
	}
	assertThreadAdapterClonesMutableFields(t, record, snapshot)
}

func TestThreadBindingStoreAdapterFieldCoverage(t *testing.T) {
	t.Parallel()

	assertAdapterMappingByField(t, func(row bindingstore.Binding) thread.BindingRecord { return *bindingRecordFromStore(&row) })
	assertAdapterMappingByField(t, bindingUpsertToStore)
	assertAdapterMappingByField(t, bindingSessionUUIDUpdateToStore)
	assertAdapterMappingByField(t, bindingProviderThreadIDUpdateToStore)
	assertAdapterMappingByField(t, bindingArchiveUpdateToStore)
	assertAdapterMappingByField(t, bindingCWDUpdateToStore)
	record := populatedAdapterValue[bindingstore.Binding]()
	assertAdapterFieldsEqual(t, record, *bindingRecordFromStore(&record))
	assertAdapterFieldsEqual(t, populatedAdapterValue[thread.BindingUpsert](), bindingUpsertToStore(populatedAdapterValue[thread.BindingUpsert]()))
	assertAdapterFieldsEqual(t, populatedAdapterValue[thread.BindingSessionUUIDUpdate](), bindingSessionUUIDUpdateToStore(populatedAdapterValue[thread.BindingSessionUUIDUpdate]()))
	assertAdapterFieldsEqual(t, populatedAdapterValue[thread.BindingProviderThreadIDUpdate](), bindingProviderThreadIDUpdateToStore(populatedAdapterValue[thread.BindingProviderThreadIDUpdate]()))
	assertAdapterFieldsEqual(t, populatedAdapterValue[thread.BindingArchiveUpdate](), bindingArchiveUpdateToStore(populatedAdapterValue[thread.BindingArchiveUpdate]()))
	assertAdapterFieldsEqual(t, populatedAdapterValue[thread.BindingCWDUpdate](), bindingCWDUpdateToStore(populatedAdapterValue[thread.BindingCWDUpdate]()))
	if _, err := provideThreadBindingStoreAdapter(nil); err == nil || !strings.Contains(err.Error(), "binding store") {
		t.Fatalf("provideThreadBindingStoreAdapter(nil) error = %v, want binding store context", err)
	}
}

func TestThreadPromptStoreAdapterImplementsPort(t *testing.T) {
	t.Parallel()

	assertAdapterMappingByField(t, func(row promptstore.PromptTemplate) threadprompt.PromptTemplate {
		return *threadPromptTemplateFromStore(&row)
	})
	assertAdapterMappingByField(t, threadPromptSectionFromStore)
	assertAdapterMappingByField(t, threadPromptVersionToStore)
	assertAdapterMappingByField(t, threadPromptListFilterToStore)
	template := populatedAdapterValue[promptstore.PromptTemplate]()
	convertedTemplate := threadPromptTemplateFromStore(&template)
	assertAdapterFieldsEqual(t, template, *convertedTemplate)
	section := populatedAdapterValue[promptstore.PromptTemplateSection]()
	convertedSection := threadPromptSectionFromStore(section)
	assertAdapterFieldsEqual(t, section, convertedSection)
	version := populatedAdapterValue[threadprompt.PromptTemplateVersion]()
	convertedVersion := threadPromptVersionToStore(version)
	assertAdapterFieldsEqual(t, version, convertedVersion)
	filter := populatedAdapterValue[threadprompt.PromptListFilter]()
	assertAdapterFieldSetsEqual(t, filter, promptstore.ListFilter{})
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

	assertAdapterMappingByField(t, threadPromptTemplateToThread)
	assertAdapterMappingByField(t, threadPromptSectionToThread)
	assertAdapterMappingByField(t, threadPromptVersionFromThread)
	assertAdapterMappingByField(t, threadPromptListFilterFromThread)
	template := populatedAdapterValue[threadprompt.PromptTemplate]()
	section := populatedAdapterValue[threadprompt.PromptTemplateSection]()
	fake := &appRuntimePromptCatalog{templates: []threadprompt.PromptTemplate{template}, sections: []threadprompt.PromptTemplateSection{section}, canInsert: true}
	port, err := provideThreadPromptCatalog(fake)
	if err != nil {
		t.Fatalf("provideThreadPromptCatalog() error = %v", err)
	}
	assertPromptCatalogReadsAndWrites(t, port, fake)
	assertBuiltinOnlyPromptCatalog(t)
}

func TestThreadStoreOptionalCapabilities(t *testing.T) {
	t.Parallel()

	plain, err := provideThreadStoreAdapter(&appPlainThreadStore{})
	if err != nil {
		t.Fatalf("provide plain adapter: %v", err)
	}
	if _, ok := plain.(thread.ThreadPageReader); ok {
		t.Fatal("plain Store unexpectedly exposes optional page capability")
	}
	full, err := provideThreadStoreAdapter(&appCapableThreadStore{})
	if err != nil {
		t.Fatalf("provide capable adapter: %v", err)
	}
	assertThreadOptionalCapabilities(t, full)
	if _, err := provideThreadStoreAdapter(&appPartialThreadStore{}); err == nil || !strings.Contains(err.Error(), "provided together") {
		t.Fatalf("partial optional capabilities error = %v, want fail-fast", err)
	}
}

func TestThreadStoreAdaptersModuleRegistersPromptProvidersViaFx(t *testing.T) {
	t.Parallel()

	registrar := &appCapturingDynamicRegistrar{}
	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			func() contract.DynamicSectionRegistrar { return registrar },
			func() contract.BuiltinPromptRegistry { return appBuiltinPromptRegistry{} },
		),
		threadStoreAdaptersModule(),
	)
	if err := app.Err(); err != nil {
		t.Fatalf("fx.New() error = %v", err)
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

func assertPromptCatalogReadsAndWrites(t *testing.T, port thread.PromptCatalog, fake *appRuntimePromptCatalog) {
	t.Helper()
	rows, err := port.ListTemplates(context.Background(), populatedAdapterValue[thread.PromptListFilter]())
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListTemplates() = (%#v, %v)", rows, err)
	}
	assertAdapterFieldsEqual(t, fake.templates[0], rows[0])
	sections, err := port.ListSectionsByTemplateID(context.Background(), 1)
	if err != nil || len(sections) != 1 {
		t.Fatalf("ListSectionsByTemplateID() = (%#v, %v)", sections, err)
	}
	assertAdapterFieldsEqual(t, fake.sections[0], sections[0])
	version := populatedAdapterValue[thread.PromptTemplateVersion]()
	if _, err := port.InsertVersion(context.Background(), version); err != nil {
		t.Fatalf("InsertVersion() error = %v", err)
	}
	assertAdapterFieldsEqual(t, version, fake.inserted)
	if !port.CanInsertPromptVersion() {
		t.Fatal("writable runtime catalog reported read-only")
	}
}

func assertBuiltinOnlyPromptCatalog(t *testing.T) {
	t.Helper()
	runtime := provideThreadPromptRuntimeCatalog(threadPromptRuntimeCatalogParams{Builtin: appBuiltinPromptRegistry{}})
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

func populatedAdapterValue[T any]() T {
	var value T
	populateAdapterReflectValue(reflect.ValueOf(&value).Elem(), 1)
	return value
}

func assertAdapterMappingByField[Source, Target any](t *testing.T, mapValue func(Source) Target) {
	t.Helper()
	sourceType := reflect.TypeFor[Source]()
	targetType := reflect.TypeFor[Target]()
	assertAdapterFieldSetsEqual(t, reflect.New(sourceType).Elem().Interface(), reflect.New(targetType).Elem().Interface())
	for _, fieldName := range exportedAdapterFieldNames(sourceType) {
		var source Source
		sourceValue := reflect.ValueOf(&source).Elem()
		field, _ := sourceValue.Type().FieldByName(fieldName)
		populateAdapterReflectValue(sourceValue.FieldByName(fieldName), field.Index[0]+1)
		target := mapValue(source)
		targetValue := reflect.ValueOf(target)
		assertAdapterReflectValuesEqual(t, sourceValue.FieldByName(fieldName), targetValue.FieldByName(fieldName), "."+fieldName)
		assertOnlyAdapterFieldSet(t, targetValue, fieldName)
	}
}

func assertOnlyAdapterFieldSet(t *testing.T, target reflect.Value, activeField string) {
	t.Helper()
	for _, fieldName := range exportedAdapterFieldNames(target.Type()) {
		if fieldName != activeField && !target.FieldByName(fieldName).IsZero() {
			t.Fatalf("mapping %s also populated unrelated target field %s", activeField, fieldName)
		}
	}
}

func populateAdapterReflectValue(value reflect.Value, seed int) {
	if value.Type() == reflect.TypeFor[time.Time]() {
		value.Set(reflect.ValueOf(time.Unix(int64(seed), int64(seed)).UTC()))
		return
	}
	switch value.Kind() {
	case reflect.Struct:
		populateAdapterStruct(value, seed)
	case reflect.Pointer:
		value.Set(reflect.New(value.Type().Elem()))
		populateAdapterReflectValue(value.Elem(), seed+1)
	default:
		populateAdapterScalar(value, seed)
	}
}

func populateAdapterStruct(value reflect.Value, seed int) {
	for i := 0; i < value.NumField(); i++ {
		if value.Type().Field(i).IsExported() {
			populateAdapterReflectValue(value.Field(i), seed+i+1)
		}
	}
}

func populateAdapterScalar(value reflect.Value, seed int) {
	switch value.Kind() {
	case reflect.String:
		value.SetString("value-" + time.Unix(int64(seed), 0).Format(time.RFC3339))
	case reflect.Bool:
		value.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		value.SetInt(int64(seed))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		value.SetUint(uint64(seed))
	case reflect.Slice:
		populateAdapterSlice(value, seed)
	case reflect.Map:
		value.Set(reflect.MakeMap(value.Type()))
		value.SetMapIndex(reflect.ValueOf("key"), reflect.ValueOf("value"))
	}
}

func populateAdapterSlice(value reflect.Value, seed int) {
	value.Set(reflect.MakeSlice(value.Type(), 3, 3))
	for i := 0; i < value.Len(); i++ {
		populateAdapterReflectValue(value.Index(i), seed+i+1)
	}
}

func assertAdapterFieldsEqual(t *testing.T, source, target any) {
	t.Helper()
	assertAdapterReflectValuesEqual(t, reflect.ValueOf(source), reflect.ValueOf(target), "")
}

func assertAdapterFieldSetsEqual(t *testing.T, source, target any) {
	t.Helper()
	sourceType := dereferenceAdapterValue(reflect.ValueOf(source)).Type()
	targetType := dereferenceAdapterValue(reflect.ValueOf(target)).Type()
	if !reflect.DeepEqual(exportedAdapterFieldNames(sourceType), exportedAdapterFieldNames(targetType)) {
		t.Fatalf("exported field sets differ: %s=%v %s=%v", sourceType, exportedAdapterFieldNames(sourceType), targetType, exportedAdapterFieldNames(targetType))
	}
}

func assertAdapterReflectValuesEqual(t *testing.T, source, target reflect.Value, path string) {
	t.Helper()
	source = dereferenceAdapterValue(source)
	target = dereferenceAdapterValue(target)
	if source.Kind() == reflect.Struct && target.Kind() == reflect.Struct && source.Type() != reflect.TypeFor[time.Time]() {
		assertAdapterFieldSetsEqual(t, source.Interface(), target.Interface())
		for _, name := range exportedAdapterFieldNames(source.Type()) {
			assertAdapterReflectValuesEqual(t, source.FieldByName(name), target.FieldByName(name), path+"."+name)
		}
		return
	}
	if !reflect.DeepEqual(source.Interface(), target.Interface()) {
		t.Fatalf("mapped field %s = %#v, want %#v", path, target.Interface(), source.Interface())
	}
}

func dereferenceAdapterValue(value reflect.Value) reflect.Value {
	for value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return value
		}
		value = value.Elem()
	}
	return value
}

func exportedAdapterFieldNames(valueType reflect.Type) []string {
	names := make([]string, 0, valueType.NumField())
	for i := 0; i < valueType.NumField(); i++ {
		if field := valueType.Field(i); field.IsExported() {
			names = append(names, field.Name)
		}
	}
	sort.Strings(names)
	return names
}

type appPlainThreadStore struct{ threadstore.Store }

type appCapableThreadStore struct{ threadstore.Store }

func (*appCapableThreadStore) ListPage(context.Context, contract.ThreadListPageParams) (contract.ThreadListPage, error) {
	return contract.ThreadListPage{NextCursorThreadID: "all"}, nil
}

func (*appCapableThreadStore) ListLoadedPage(context.Context, contract.ThreadListPageParams) (contract.ThreadListPage, error) {
	return contract.ThreadListPage{NextCursorThreadID: "loaded"}, nil
}

func (*appCapableThreadStore) CountActive(context.Context) (int64, error) {
	return 7, nil
}

type appPartialThreadStore struct{ threadstore.Store }

func (*appPartialThreadStore) ListPage(context.Context, contract.ThreadListPageParams) (contract.ThreadListPage, error) {
	return contract.ThreadListPage{}, nil
}

type appRuntimePromptCatalog struct {
	templates []threadprompt.PromptTemplate
	sections  []threadprompt.PromptTemplateSection
	inserted  threadprompt.PromptTemplateVersion
	canInsert bool
}

func (a *appRuntimePromptCatalog) ListTemplates(context.Context, threadprompt.RuntimeListFilter) ([]threadprompt.PromptTemplate, error) {
	return append([]threadprompt.PromptTemplate(nil), a.templates...), nil
}

func (*appRuntimePromptCatalog) GetTemplate(context.Context, string, string) (*threadprompt.PromptTemplate, error) {
	return nil, errors.New("unused")
}

func (a *appRuntimePromptCatalog) ListSectionsByTemplateID(context.Context, int64) ([]threadprompt.PromptTemplateSection, error) {
	return append([]threadprompt.PromptTemplateSection(nil), a.sections...), nil
}

func (*appRuntimePromptCatalog) ListRecallSections(context.Context, string) ([]threadprompt.PromptTemplateSection, error) {
	return nil, nil
}

func (*appRuntimePromptCatalog) ListDefaultRuleSections(context.Context, string) ([]threadprompt.PromptTemplateSection, error) {
	return nil, nil
}

func (a *appRuntimePromptCatalog) InsertVersion(_ context.Context, version threadprompt.PromptTemplateVersion) (int64, error) {
	a.inserted = version
	return 9, nil
}

func (a *appRuntimePromptCatalog) CanInsertPromptVersion() bool {
	return a.canInsert
}

type appBuiltinPromptRegistry struct{}

func (appBuiltinPromptRegistry) ListTemplates() []contract.BuiltinPromptTemplate {
	return []contract.BuiltinPromptTemplate{{ID: -1, PromptKey: "builtin", AgentKey: "main", Enabled: true}}
}

func (appBuiltinPromptRegistry) GetTemplate(promptKey string) (contract.BuiltinPromptTemplate, bool) {
	return contract.BuiltinPromptTemplate{ID: -1, PromptKey: promptKey, AgentKey: "main", Enabled: true}, true
}

func (appBuiltinPromptRegistry) SectionsByTemplateID(int64) []contract.BuiltinPromptSection {
	return nil
}

type appCapturingDynamicRegistrar struct {
	names []string
}

func (r *appCapturingDynamicRegistrar) RegisterDynamicProvider(provider contract.DynamicSectionProvider) error {
	r.names = append(r.names, provider.SectionName())
	return nil
}

func (*appCapturingDynamicRegistrar) UnregisterDynamicProvider(string) bool {
	return false
}

var _ threadprompt.RuntimePromptCatalog = (*appRuntimePromptCatalog)(nil)
