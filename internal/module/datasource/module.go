// Package datasource 提供本地文件上传、列举和删除能力，并把文件正文入库供 prompt 动态段消费。
package datasource

import (
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"go.uber.org/fx"
)

// promptProviderParams 是 datasource 注册动态 prompt provider 的 fx 入参。
// registry/provider 均为可选，缺任一项时跳过注册以支持精简运行模式。
type promptProviderParams struct {
	fx.In

	Registry contract.DynamicSectionRegistrar `optional:"true"`
	Provider *PromptProvider                  `optional:"true"`
}

// Module 组装 datasource service、RPC handler 和 prompt 动态段注册。
var Module = fx.Module("datasource",
	fx.Provide(
		NewServiceWithStore,
		NewHandlers,
		NewPromptProvider,
	),
	fx.Invoke(registerPromptProvider),
)

// registerPromptProvider 将 datasource provider 注册到 prompt 动态段系统。
// 依赖缺失时直接跳过，不影响文件上传 RPC 能力。
func registerPromptProvider(p promptProviderParams) error {
	if p.Registry == nil || p.Provider == nil {
		return nil
	}
	return p.Registry.RegisterDynamicProvider(p.Provider)
}
