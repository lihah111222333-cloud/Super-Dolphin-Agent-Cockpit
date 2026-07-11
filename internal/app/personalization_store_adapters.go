package app

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/anthropic-ai/super-agent-v3/internal/module/personalization"
	"github.com/anthropic-ai/super-agent-v3/internal/store/uipreference"
)

var errPersonalizationPreferenceStoreRequired = errors.New("personalization: preference store is required")

type personalizationPreferenceStoreAdapter struct {
	store uipreference.Store
}

var _ personalization.PreferenceStore = (*personalizationPreferenceStoreAdapter)(nil)

// providePersonalizationPreferenceStore 保持 optional 装配时机，始终返回可调用的领域适配器。
func providePersonalizationPreferenceStore(store uipreference.Store) personalization.PreferenceStore {
	return &personalizationPreferenceStoreAdapter{store: store}
}

// GetValue 读取偏好 JSON，缺失 Store 时明确失败，并复制返回值的底层数组。
func (a *personalizationPreferenceStoreAdapter) GetValue(ctx context.Context, cwd, key string) (json.RawMessage, error) {
	if err := a.validate(); err != nil {
		return nil, err
	}
	value, err := a.store.GetValue(ctx, cwd, key)
	if err != nil {
		return nil, err
	}
	return copyPersonalizationPreferenceValue(value), nil
}

// Upsert 逐字段转换领域参数，缺失 Store 时明确失败，并复制写入值的底层数组。
func (a *personalizationPreferenceStoreAdapter) Upsert(ctx context.Context, params personalization.PreferenceUpsertParams) error {
	if err := a.validate(); err != nil {
		return err
	}
	return a.store.Upsert(ctx, uipreference.UpsertParams{
		Cwd:   params.Cwd,
		Key:   params.Key,
		Value: copyPersonalizationPreferenceValue(params.Value),
	})
}

// validate 确保方法调用时底层 Store 已真实装配。
func (a *personalizationPreferenceStoreAdapter) validate() error {
	if a == nil || isNilBusinessStore(a.store) {
		return errPersonalizationPreferenceStoreRequired
	}
	return nil
}

// copyPersonalizationPreferenceValue 复制 JSON 值，避免持久化边界两侧共享内存。
func copyPersonalizationPreferenceValue(value json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), value...)
}
