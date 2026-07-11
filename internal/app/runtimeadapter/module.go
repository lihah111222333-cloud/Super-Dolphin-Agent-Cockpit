// Package runtimeadapter 聚合 app 装配使用的运行时 Store 消费适配器。
package runtimeadapter

import (
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/app/runtimeadapter/builtintools"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/app/runtimeadapter/cachekeepalive"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/app/runtimeadapter/mcpcontrol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/app/runtimeadapter/toolbridge"
	"go.uber.org/fx"
)

// Module 按依赖顺序透明展开运行时适配器子模块。
var Module = fx.Options(
	mcpcontroladapter.Module,
	toolbridgeadapter.Module,
	cachekeepaliveadapter.Module,
	builtintoolsadapter.Module,
)
