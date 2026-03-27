package workspace

import (
	"github.com/kelindar/event"
	"go.uber.org/fx"

	storeworkspace "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/workspace"
)

var Module = fx.Module("workspace",
	fx.Provide(func(store storeworkspace.Store, dispatcher *event.Dispatcher) Service {
		return NewService(store, dispatcher)
	}),
	fx.Provide(NewWorkspaceHandlers),
)
