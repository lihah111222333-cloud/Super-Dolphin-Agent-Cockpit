package buslog

import "go.uber.org/fx"

// Module 注册业务异常日志 Store，供 UI 和诊断模块读取异常事件。
var Module = fx.Module("store.buslog",
	fx.Provide(NewStore),
)
