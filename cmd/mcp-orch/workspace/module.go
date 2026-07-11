package workspace

import (
	"github.com/kelindar/event"
	"go.uber.org/fx"

	storeworkspace "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/workspace"
)

// Module 注册 workspace 服务、事件和 RPC handlers。
var Module = fx.Module("workspace",
	fx.Provide(func(store storeworkspace.Store, dispatcher *event.Dispatcher) Service {
		return NewService(store, dispatcher)
	}),
	fx.Provide(NewWorkspaceHandlers),
)
