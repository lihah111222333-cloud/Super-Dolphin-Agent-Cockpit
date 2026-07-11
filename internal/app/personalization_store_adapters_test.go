package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/module/personalization"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/uipreference"
)

var _ personalization.PreferenceStore = (*personalizationPreferenceStoreAdapter)(nil)

type personalizationPreferenceStoreStub struct {
	getValue func(context.Context, string, string) (json.RawMessage, error)
	upsert   func(context.Context, uipreference.UpsertParams) error
}

func (s *personalizationPreferenceStoreStub) GetValue(ctx context.Context, cwd, key string) (json.RawMessage, error) {
	if s.getValue != nil {
		return s.getValue(ctx, cwd, key)
	}
	return nil, nil
}

func (s *personalizationPreferenceStoreStub) Upsert(ctx context.Context, params uipreference.UpsertParams) error {
	if s.upsert != nil {
		return s.upsert(ctx, params)
	}
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
	if err := store.Upsert(context.Background(), personalization.PreferenceUpsertParams{}); err == nil {
		t.Fatal("expected Upsert to reject nil underlying Store")
	}
	var typedNil *personalizationPreferenceStoreStub
	typedNilStore := providePersonalizationPreferenceStore(typedNil)
	if _, err := typedNilStore.GetValue(context.Background(), "cwd", "key"); err == nil || err.Error() != "personalization: preference store is required" {
		t.Fatalf("expected typed nil Store to return required error, got %v", err)
	}
}

// TestPersonalizationStoreAdapterFieldCoverage 自动覆盖偏好写入 DTO 的全部导出字段。
func TestPersonalizationStoreAdapterFieldCoverage(t *testing.T) {
	assertBusinessStoreAdapterFieldsMap(t, func(params personalization.PreferenceUpsertParams) (uipreference.UpsertParams, error) {
		var captured uipreference.UpsertParams
		port := providePersonalizationPreferenceStore(&personalizationPreferenceStoreStub{upsert: func(_ context.Context, stored uipreference.UpsertParams) error {
			captured = stored
			return nil
		}})
		err := port.Upsert(context.Background(), params)
		return captured, err
	})
}

// TestPersonalizationStoreAdapterCopiesMutableValues 固定读返回值与写入值均不跨边界共享内存。
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
	t.Run("upsert", func(t *testing.T) {
		value := json.RawMessage(`{"name":"domain"}`)
		port := providePersonalizationPreferenceStore(&personalizationPreferenceStoreStub{upsert: func(_ context.Context, params uipreference.UpsertParams) error {
			params.Value[0] = '['
			return nil
		}})
		if err := port.Upsert(context.Background(), personalization.PreferenceUpsertParams{Value: value}); err != nil {
			t.Fatalf("upsert preference value: %v", err)
		}
		if string(value) != `{"name":"domain"}` {
			t.Fatalf("Upsert value shared Store memory: %s", value)
		}
	})
}

// TestPersonalizationStoreAdapterPreservesErrors 固定读写错误链在 App 边界原样传播。
func TestPersonalizationStoreAdapterPreservesErrors(t *testing.T) {
	sentinel := errors.New("preference store sentinel")
	storeErr := fmt.Errorf("preference operation: %w", sentinel)
	port := providePersonalizationPreferenceStore(&personalizationPreferenceStoreStub{
		getValue: func(context.Context, string, string) (json.RawMessage, error) { return nil, storeErr },
		upsert:   func(context.Context, uipreference.UpsertParams) error { return storeErr },
	})
	_, getErr := port.GetValue(context.Background(), "cwd", "key")
	assertPersonalizationStoreErrorPreserved(t, getErr, storeErr, sentinel)
	upsertErr := port.Upsert(context.Background(), personalization.PreferenceUpsertParams{})
	assertPersonalizationStoreErrorPreserved(t, upsertErr, storeErr, sentinel)
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
