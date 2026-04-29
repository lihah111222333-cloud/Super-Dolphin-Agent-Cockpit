package skillforge

import "go.uber.org/fx"

// Module 当前不暴露 service 单例（forge 是纯函数式 API）；
// 占位以保持 Fx 树一致，并为未来 wrapper 留口。
var Module = fx.Module("skillforge")
