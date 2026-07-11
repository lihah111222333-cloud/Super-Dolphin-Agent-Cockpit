package builtintoolsadapter

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/uipreference"
)

func TestProvideNativeToolDescriptorsReturnsNilForNilRegistry(t *testing.T) {
	t.Parallel()

	if got := provideNativeToolDescriptors(nil); got != nil {
		t.Fatalf("provideNativeToolDescriptors(nil) = %#v, want nil", got)
	}
}

func TestProvideDisabledBuiltinToolsFnIgnoresDefaultDisabledToolsWithoutPreference(t *testing.T) {
	t.Parallel()
	fn := provideDisabledBuiltinToolsFn(&preferenceStoreStub{}, []contract.NativeToolDescriptor{
		{ID: "apply_patch", Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft, DefaultDisabled: true},
		{ID: "web_search", Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft, DefaultDisabled: false},
	})

	got, err := fn(context.Background(), "/repo", "codex")
	if err != nil {
		t.Fatalf("disabled tools fn error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("disabled tools fn tools = %#v, want no default-disabled tools", got)
	}
}

func TestProvideDisabledBuiltinToolsFnReturnsExplicitDisabledSoftTools(t *testing.T) {
	t.Parallel()
	fn := provideDisabledBuiltinToolsFn(&preferenceStoreStub{values: map[string]json.RawMessage{
		"/repo\x00config/builtinTools.disabled": json.RawMessage(`["apply_patch","Read"]`),
	}}, []contract.NativeToolDescriptor{
		{ID: "apply_patch", Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft, DefaultDisabled: true},
		{ID: "Read", Provider: "claude", FilterMode: contract.NativeToolFilterModeHard, DefaultDisabled: true},
	})

	got, err := fn(context.Background(), "/repo", "codex")
	if err != nil {
		t.Fatalf("disabled tools fn error = %v", err)
	}
	want := []string{"apply_patch"}
	if !equalStringSlices(got, want) {
		t.Fatalf("disabled tools fn tools = %#v, want %#v", got, want)
	}
}

func TestProvideDisabledBuiltinToolsFnReturnsPreferenceReadError(t *testing.T) {
	readErr := errors.New("ui preference read failed")
	fn := provideDisabledBuiltinToolsFn(preferenceErrorStore{err: readErr}, []contract.NativeToolDescriptor{
		{ID: "Read", Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft, DefaultDisabled: true},
	})

	got, err := fn(context.Background(), "/repo", "codex")
	if !errors.Is(err, readErr) {
		t.Fatalf("disabled tools fn error = %v, want %v", err, readErr)
	}
	if got != nil {
		t.Fatalf("disabled tools fn tools = %#v, want nil on preference read error", got)
	}
}

type preferenceErrorStore struct {
	err error
}

func (s preferenceErrorStore) GetValue(context.Context, string, string) (json.RawMessage, error) {
	return nil, s.err
}

func (preferenceErrorStore) Upsert(context.Context, uipreference.UpsertParams) error { return nil }
func (preferenceErrorStore) List(context.Context, string) ([]uipreference.UIPreference, error) {
	return nil, nil
}

type preferenceStoreStub struct {
	values map[string]json.RawMessage
}

func (s *preferenceStoreStub) GetValue(_ context.Context, cwd, key string) (json.RawMessage, error) {
	if value, ok := s.values[cwd+"\x00"+key]; ok {
		return value, nil
	}
	return nil, platformdb.ErrNotFound
}

func (s *preferenceStoreStub) Upsert(_ context.Context, params uipreference.UpsertParams) error {
	if s.values == nil {
		s.values = make(map[string]json.RawMessage)
	}
	s.values[params.Cwd+"\x00"+params.Key] = append(json.RawMessage(nil), params.Value...)
	return nil
}

func (s *preferenceStoreStub) List(context.Context, string) ([]uipreference.UIPreference, error) {
	return nil, nil
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
