package ailog

import "go.uber.org/fx"

// Module 注册 AI 日志 Store，供日志面板和诊断查询按接口注入。
var Module = fx.Module("store.ailog",
	fx.Provide(NewStore),
)
