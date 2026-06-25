// Package appupdate 提供应用自动更新的检查、下载和安装能力，支持 GitHub Releases 和自定义 manifest 两种更新源。
package appupdate

import "go.uber.org/fx"

// serviceParams 是 fx 注入 service 所需参数的容器。
type serviceParams struct {
	fx.In

	Config      Config
	RequestQuit RequestQuit `optional:"true"`
}

var Module = fx.Module("appupdate",
	fx.Provide(
		ProvideConfig,
		NewService,
		NewHandlers,
	),
)
