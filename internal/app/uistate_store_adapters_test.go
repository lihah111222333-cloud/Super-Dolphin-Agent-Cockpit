package app

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/module/uistate"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
	"github.com/anthropic-ai/super-agent-v3/internal/store/uipreference"
	storeadaptertest "github.com/anthropic-ai/super-agent-v3/internal/testutil/storeadapter"
)

type uiStatePreferenceStoreTestDouble struct {
	value  json.RawMessage
	rows   []uipreference.UIPreference
	upsert uipreference.UpsertParams
	err    error
}

func (s *uiStatePreferenceStoreTestDouble) GetValue(context.Context, string, string) (json.RawMessage, error) {
	return s.value, s.err
}

func (s *uiStatePreferenceStoreTestDouble) Upsert(_ context.Context, value uipreference.UpsertParams) error {
	s.upsert = value
	return s.err
}

func (s *uiStatePreferenceStoreTestDouble) List(context.Context, string) ([]uipreference.UIPreference, error) {
	return s.rows, s.err
}

type uiStateSharedFileReaderTestDouble struct {
	file *sharedfilestore.SharedFile
	err  error
}

func (s *uiStateSharedFileReaderTestDouble) Get(context.Context, string) (*sharedfilestore.SharedFile, error) {
	return s.file, s.err
}

func (*uiStateSharedFileReaderTestDouble) List(
	context.Context,
	sharedfilestore.ListFilter,
) ([]sharedfilestore.SharedFile, error) {
	return nil, nil
}

type uiStateBindingStoreTestDouble struct {
	bindingstore.Store
	rows []bindingstore.Binding
	err  error
}

func (s *uiStateBindingStoreTestDouble) ListAgentThreadBindings(context.Context) ([]bindingstore.Binding, error) {
	return s.rows, s.err
}

// TestUIStateStoreAdapterProvidersPreserveNil 固定三个 provider 对 nil/typed nil 均直接返回 nil 领域端口。
func TestUIStateStoreAdapterProvidersPreserveNil(t *testing.T) {
	t.Parallel()
	if port := provideUIStatePreferenceStore(nil); port != nil {
		t.Fatalf("nil preference Store = %T, want nil", port)
	}
	var preferences *uiStatePreferenceStoreTestDouble
	if port := provideUIStatePreferenceStore(preferences); port != nil {
		t.Fatalf("typed nil preference Store = %T, want nil", port)
	}
	if port := provideUIStateSharedFileReader(nil); port != nil {
		t.Fatalf("nil shared file reader = %T, want nil", port)
	}
	var sharedFiles *uiStateSharedFileReaderTestDouble
	if port := provideUIStateSharedFileReader(sharedFiles); port != nil {
		t.Fatalf("typed nil shared file reader = %T, want nil", port)
	}
	if port := provideUIStateBindingLookup(nil); port != nil {
		t.Fatalf("nil binding Store = %T, want nil", port)
	}
	var bindings *uiStateBindingStoreTestDouble
	if port := provideUIStateBindingLookup(bindings); port != nil {
		t.Fatalf("typed nil binding Store = %T, want nil", port)
	}
}

// TestUIStatePreferenceAdapterMapsFields 固定偏好写入和列表的全部领域字段。
func TestUIStatePreferenceAdapterMapsFields(t *testing.T) {
	t.Parallel()
	storeadaptertest.AssertFieldsMapE(t, func(value uistate.PreferenceUpsertParams) (uipreference.UpsertParams, error) {
		return toStoreUIStatePreferenceUpsert(value), nil
	})
	row := uipreference.UIPreference{Cwd: "/repo", Key: "theme", Value: json.RawMessage(`{"dark":true}`)}
	mapped := fromStoreUIStatePreference(row)
	if mapped.Cwd != row.Cwd || mapped.Key != row.Key || string(mapped.Value) != string(row.Value) {
		t.Fatalf("preference mapping = %#v, want fields from %#v", mapped, row)
	}
}

// TestUIStateBindingAdapterMapsAndTrimsFields one-hot 覆盖全部八个 binding 字符串字段。
func TestUIStateBindingAdapterMapsAndTrimsFields(t *testing.T) {
	t.Parallel()
	targetType := reflect.TypeFor[uistate.BindingEntry]()
	for index := range targetType.NumField() {
		field := targetType.Field(index)
		t.Run(field.Name, func(t *testing.T) {
			source := reflect.New(reflect.TypeFor[bindingstore.Binding]()).Elem()
			source.FieldByName(field.Name).SetString(" value ")
			mapped := reflect.ValueOf(fromStoreUIStateBinding(source.Interface().(bindingstore.Binding)))
			if got := mapped.FieldByName(field.Name).String(); got != "value" {
				t.Fatalf("%s = %q, want trimmed value", field.Name, got)
			}
		})
	}
}

// TestUIStateSharedFileAdapterMapsFields 固定 shared file 的 path/content 映射与 nil 文件语义。
func TestUIStateSharedFileAdapterMapsFields(t *testing.T) {
	t.Parallel()
	file := sharedfilestore.SharedFile{Path: "prompts/lsp.md", Content: "hint"}
	port := provideUIStateSharedFileReader(&uiStateSharedFileReaderTestDouble{file: &file})
	got, err := port.Get(context.Background(), file.Path)
	if err != nil || got == nil || got.Path != file.Path || got.Content != file.Content {
		t.Fatalf("Get = (%#v, %v), want mapped file", got, err)
	}
	empty := provideUIStateSharedFileReader(&uiStateSharedFileReaderTestDouble{})
	if got, err := empty.Get(context.Background(), "missing"); got != nil || err != nil {
		t.Fatalf("Get missing = (%#v, %v), want (nil, nil)", got, err)
	}
}

// TestUIStatePreferenceAdapterCopiesJSON 固定 Get、Upsert、List 三条路径均不共享 RawMessage。
func TestUIStatePreferenceAdapterCopiesJSON(t *testing.T) {
	getValue := json.RawMessage(`{"theme":"dark"}`)
	listValue := json.RawMessage(`{"density":"compact"}`)
	root := &uiStatePreferenceStoreTestDouble{
		value: getValue,
		rows:  []uipreference.UIPreference{{Cwd: "/repo", Key: "density", Value: listValue}},
	}
	port := provideUIStatePreferenceStore(root)
	got, err := port.GetValue(context.Background(), "/repo", "theme")
	if err != nil {
		t.Fatalf("GetValue: %v", err)
	}
	got[0] = '['
	writeValue := json.RawMessage(`{"sidebar":true}`)
	if err := port.Upsert(context.Background(), uistate.PreferenceUpsertParams{Value: writeValue}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	root.upsert.Value[0] = '['
	rows, err := port.List(context.Background(), "/repo")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	rows[0].Value[0] = '['
	if string(getValue) != `{"theme":"dark"}` || string(writeValue) != `{"sidebar":true}` || string(listValue) != `{"density":"compact"}` {
		t.Fatalf("RawMessage ownership leaked: %s %s %s", getValue, writeValue, listValue)
	}
}

// TestUIStateAdaptersReturnIndependentLists 固定 Preference 和 Binding 结果切片不共享 Store backing array。
func TestUIStateAdaptersReturnIndependentLists(t *testing.T) {
	preferences := &uiStatePreferenceStoreTestDouble{rows: []uipreference.UIPreference{{Cwd: "/repo", Key: "theme"}}}
	preferenceRows, err := provideUIStatePreferenceStore(preferences).List(context.Background(), "/repo")
	if err != nil {
		t.Fatalf("preference List: %v", err)
	}
	preferenceRows[0].Key = "changed"
	bindings := &uiStateBindingStoreTestDouble{rows: []bindingstore.Binding{{AgentID: "agent-1"}}}
	bindingRows, err := provideUIStateBindingLookup(bindings).ListAgentThreadBindings(context.Background())
	if err != nil {
		t.Fatalf("binding List: %v", err)
	}
	bindingRows[0].AgentID = "changed"
	if preferences.rows[0].Key != "theme" || bindings.rows[0].AgentID != "agent-1" {
		t.Fatalf("Store rows mutated: %#v %#v", preferences.rows, bindings.rows)
	}
}

// TestUIStateStoreAdaptersPreserveErrors 固定所有 Store 错误对象身份。
func TestUIStateStoreAdaptersPreserveErrors(t *testing.T) {
	wantErr := errors.New("uistate Store failed")
	preferences := provideUIStatePreferenceStore(&uiStatePreferenceStoreTestDouble{err: wantErr})
	operations := []func() error{
		func() error { _, err := preferences.GetValue(context.Background(), "/repo", "key"); return err },
		func() error { return preferences.Upsert(context.Background(), uistate.PreferenceUpsertParams{}) },
		func() error { _, err := preferences.List(context.Background(), "/repo"); return err },
		func() error {
			_, err := provideUIStateSharedFileReader(&uiStateSharedFileReaderTestDouble{err: wantErr}).Get(context.Background(), "file")
			return err
		},
		func() error {
			_, err := provideUIStateBindingLookup(&uiStateBindingStoreTestDouble{err: wantErr}).ListAgentThreadBindings(context.Background())
			return err
		},
	}
	for index, operation := range operations {
		if err := operation(); err != wantErr || !errors.Is(err, wantErr) {
			t.Fatalf("operation %d error = %v, want identical %v", index, err, wantErr)
		}
	}
}

// TestBusinessStoreAdaptersModuleOwnsUIStatePorts 通过真实 Fx lifecycle 证明 App bundle 拥有三个端口。
func TestBusinessStoreAdaptersModuleOwnsUIStatePorts(t *testing.T) {
	preferences := &uiStatePreferenceStoreTestDouble{}
	sharedFiles := &uiStateSharedFileReaderTestDouble{}
	bindings := &uiStateBindingStoreTestDouble{}
	var preferencePort uistate.PreferenceStore
	var sharedFilePort uistate.SharedFileReader
	var bindingPort uistate.BindingLookup
	app := fx.New(
		fx.NopLogger,
		fx.Provide(func() uipreference.Store { return preferences }),
		fx.Provide(func() sharedfilestore.Reader { return sharedFiles }),
		fx.Provide(func() bindingstore.Store { return bindings }),
		businessStoreAdaptersModule(),
		fx.Populate(&preferencePort, &sharedFilePort, &bindingPort),
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
	if preferencePort == nil || sharedFilePort == nil || bindingPort == nil {
		t.Fatalf("uistate ports = (%T, %T, %T), want all non-nil", preferencePort, sharedFilePort, bindingPort)
	}
}
