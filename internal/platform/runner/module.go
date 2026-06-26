package runner

import "go.uber.org/fx"

// Module 提供 runner 平台层的 fx 依赖注入模块。
var Module = fx.Module(
	"runner",
	fx.Provide(NewContract),
)
