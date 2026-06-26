// Package statemachine 提供状态机平台模块的 Fx 装配入口。
package statemachine

import "go.uber.org/fx"

// Module 注册状态机平台模块；当前模块仅作为统一装配锚点保留。
var Module = fx.Module("statemachine")
