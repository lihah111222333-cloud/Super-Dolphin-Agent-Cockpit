package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/store/uipreference"
)

func TestProvideDisabledBuiltinToolsFnReturnsPreferenceReadError(t *testing.T) {
	readErr := errors.New("ui preference read failed")
	fn := provideDisabledBuiltinToolsFn(appPreferenceErrorStore{err: readErr}, []contract.NativeToolDescriptor{
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

type appPreferenceErrorStore struct {
	err error
}

func (s appPreferenceErrorStore) GetValue(context.Context, string, string) (json.RawMessage, error) {
	return nil, s.err
}

func (appPreferenceErrorStore) Upsert(context.Context, uipreference.UpsertParams) error {
	return nil
}

func (appPreferenceErrorStore) List(context.Context, string) ([]uipreference.UIPreference, error) {
	return nil, nil
}
