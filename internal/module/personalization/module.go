package personalization

import (
	"fmt"

	"go.uber.org/fx"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
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
