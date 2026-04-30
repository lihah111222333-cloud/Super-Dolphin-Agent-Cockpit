package cliadapter

import "go.uber.org/fx"

// Module 当前不暴露 service 单例（API 是纯函数），占位以保 Fx 树一致。
var Module = fx.Module("cliadapter")
