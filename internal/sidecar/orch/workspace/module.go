package workspace

import (
	"github.com/kelindar/event"
	"go.uber.org/fx"

	storeworkspace "github.com/anthropic-ai/super-agent-v3/internal/sidecar/orch/store/workspace"
)

// Module is part of the workspace package API.
var Module = fx.Module("workspace",
	fx.Provide(func(store storeworkspace.Store, dispatcher *event.Dispatcher) Service {
		return NewService(store, dispatcher)
	}),
	fx.Provide(NewWorkspaceHandlers),
)
