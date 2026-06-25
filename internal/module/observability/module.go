// Package observability 把可观测性 RPC 处理器装配到 Fx 依赖树。
package observability

import "go.uber.org/fx"

// Module 注册 observability 模块的 RPC 处理器。
var Module = fx.Module("module.observability",
	fx.Provide(NewHandlers),
)
