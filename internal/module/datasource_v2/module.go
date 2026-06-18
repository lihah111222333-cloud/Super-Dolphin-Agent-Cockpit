package datasourcev2

import (
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"go.uber.org/fx"
)

type promptProviderParams struct {
	fx.In

	Registry contract.DynamicSectionRegistrar `optional:"true"`
	Provider *PromptProvider                  `optional:"true"`
}

// Module 注册 datasource_v2 的 service、JSON-RPC handler 和 prompt 动态段。
var Module = fx.Module("datasource_v2",
	fx.Provide(
		NewService,
		NewHandlers,
		NewPromptProvider,
	),
	fx.Invoke(registerPromptProvider),
)

func registerPromptProvider(p promptProviderParams) error {
	if p.Registry == nil || p.Provider == nil {
		return nil
	}
	return p.Registry.RegisterDynamicProvider(p.Provider)
}
