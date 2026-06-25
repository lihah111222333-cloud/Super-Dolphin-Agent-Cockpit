// Package datasource 提供本地文件上传、列举和删除能力，并把文件正文入库供 prompt 动态段消费。
package datasource

import (
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"go.uber.org/fx"
)

type promptProviderParams struct {
	fx.In

	Registry contract.DynamicSectionRegistrar `optional:"true"`
	Provider *PromptProvider                  `optional:"true"`
}

var Module = fx.Module("datasource",
	fx.Provide(
		NewServiceWithStore,
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
