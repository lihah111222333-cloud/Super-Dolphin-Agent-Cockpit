package personalizationadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/personalization"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/uipreference"
)

var _ personalization.PreferenceStore = (*personalizationPreferenceStoreAdapter)(nil)

type personalizationPreferenceStoreStub struct {
	getValue func(context.Context, string, string) (json.RawMessage, error)
}

func (s *personalizationPreferenceStoreStub) GetValue(ctx context.Context, cwd, key string) (json.RawMessage, error) {
	if s.getValue != nil {
		return s.getValue(ctx, cwd, key)
	}
	return nil, nil
}

func (*personalizationPreferenceStoreStub) Upsert(context.Context, uipreference.UpsertParams) error {
	return nil
}

func (*personalizationPreferenceStoreStub) List(context.Context, string) ([]uipreference.UIPreference, error) {
	return nil, nil
}

// TestPersonalizationStoreAdapterNilSemantics 固定 nil provider 仍返回可调用 adapter，并在方法调用时明确报错。
func TestPersonalizationStoreAdapterNilSemantics(t *testing.T) {
	store := providePersonalizationPreferenceStore(nil)
	if store == nil {
		t.Fatal("expected nonnil personalization preference adapter")
	}
	if _, err := store.GetValue(context.Background(), "cwd", "key"); err == nil {
		t.Fatal("expected GetValue to reject nil underlying Store")
	}
	var typedNil *personalizationPreferenceStoreStub
	typedNilStore := providePersonalizationPreferenceStore(typedNil)
	if _, err := typedNilStore.GetValue(context.Background(), "cwd", "key"); err == nil || err.Error() != "personalization: preference store is required" {
		t.Fatalf("expected typed nil Store to return required error, got %v", err)
	}
}

// TestPersonalizationStoreAdapterCopiesMutableValues 固定读返回值不跨边界共享内存。
func TestPersonalizationStoreAdapterCopiesMutableValues(t *testing.T) {
	t.Run("get_value", func(t *testing.T) {
		stored := json.RawMessage(`{"name":"stored"}`)
		port := providePersonalizationPreferenceStore(&personalizationPreferenceStoreStub{getValue: func(context.Context, string, string) (json.RawMessage, error) {
			return stored, nil
		}})
		got, err := port.GetValue(context.Background(), "cwd", "profile")
		if err != nil {
			t.Fatalf("get preference value: %v", err)
		}
		stored[0] = '['
		if string(got) != `{"name":"stored"}` {
			t.Fatalf("GetValue result shared Store memory: %s", got)
		}
	})
}

// TestPersonalizationStoreAdapterPreservesErrors 固定读取错误链在 App 边界原样传播。
func TestPersonalizationStoreAdapterPreservesErrors(t *testing.T) {
	sentinel := errors.New("preference store sentinel")
	storeErr := fmt.Errorf("preference operation: %w", sentinel)
	port := providePersonalizationPreferenceStore(&personalizationPreferenceStoreStub{
		getValue: func(context.Context, string, string) (json.RawMessage, error) { return nil, storeErr },
	})
	_, getErr := port.GetValue(context.Background(), "cwd", "key")
	assertPersonalizationStoreErrorPreserved(t, getErr, storeErr, sentinel)
}

// TestPersonalizationStoreAdapterPreservesNotFoundForService 证明 not-found 穿过 App 后仍映射为空 profile。
func TestPersonalizationStoreAdapterPreservesNotFoundForService(t *testing.T) {
	port := providePersonalizationPreferenceStore(&personalizationPreferenceStoreStub{getValue: func(context.Context, string, string) (json.RawMessage, error) {
		return nil, platformdb.ErrNotFound
	}})
	result, err := personalization.NewService(port).GetProfile(context.Background(), "/workspace")
	if err != nil {
		t.Fatalf("GetProfile should treat not found as empty profile: %v", err)
	}
	if result.Profile.DisplayName != "" || result.Profile.Role != "" || result.Profile.Background != "" || result.Profile.CustomInstructions != "" {
		t.Fatalf("expected empty profile, got %+v", result.Profile)
	}
}

func assertPersonalizationStoreErrorPreserved(t *testing.T, got, storeErr, sentinel error) {
	t.Helper()
	if got != storeErr {
		t.Fatalf("expected original Store error, got %v", got)
	}
	if !errors.Is(got, sentinel) {
		t.Fatalf("expected errors.Is to preserve sentinel, got %v", got)
	}
}
