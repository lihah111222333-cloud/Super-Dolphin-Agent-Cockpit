package auditlog

import "go.uber.org/fx"

// Module 注册审计日志 Store，供写入和查询路径共享同一持久化实现。
var Module = fx.Module("store.auditlog",
	fx.Provide(NewStore),
)
