package agentstatus

import "go.uber.org/fx"

// Module 注册 agentstatus Store，供状态面板和编排状态查询按接口注入。
var Module = fx.Module("store.agentstatus",
	fx.Provide(NewStore),
)
