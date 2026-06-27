package personalization

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/store/uipreference"
)

// promptProviderParams 是注册个性化 prompt 提供器所需的 fx 依赖参数。
type promptProviderParams struct {
	fx.In

	Registry contract.DynamicSectionRegistrar `optional:"true"`
	Provider *PromptProvider                  `optional:"true"`
}

// Module 装配个性化模块，注册 service、RPC 处理器和 prompt 提供器。
var Module = fx.Module("personalization",
	fx.Provide(
		newUIPreferenceStorePort,
		NewService,
		NewHandlers,
		NewPromptProvider,
	),
	fx.Invoke(registerPromptProvider),
)

// registerPromptProvider 在注册表中注册个性化 prompt 提供器，两者均为 nil 时静默跳过。
func registerPromptProvider(p promptProviderParams) error {
	if p.Registry == nil || p.Provider == nil {
		return nil
	}
	if err := p.Registry.RegisterDynamicProvider(p.Provider); err != nil {
		return fmt.Errorf("personalization: register prompt provider: %w", err)
	}
	return nil
}

type uiPreferenceStoreAdapter struct {
	store uipreference.Store
}

// newUIPreferenceStorePort 把 store concrete 收窄成 personalization 需要的模块内端口。
func newUIPreferenceStorePort(store uipreference.Store) preferenceStore {
	return uiPreferenceStoreAdapter{store: store}
}

// GetValue 从底层 uipreference store 读取单个项目偏好，并在装配异常时快速失败。
func (a uiPreferenceStoreAdapter) GetValue(ctx context.Context, cwd, key string) (json.RawMessage, error) {
	if a.store == nil {
		return nil, fmt.Errorf("personalization: preference store is required")
	}
	return a.store.GetValue(ctx, cwd, key)
}

// Upsert 将模块内写入参数转换为 store DTO，并在装配异常时快速失败。
func (a uiPreferenceStoreAdapter) Upsert(ctx context.Context, params preferenceUpsertParams) error {
	if a.store == nil {
		return fmt.Errorf("personalization: preference store is required")
	}
	return a.store.Upsert(ctx, uipreference.UpsertParams{
		Cwd:   params.Cwd,
		Key:   params.Key,
		Value: params.Value,
	})
}
