package workspace

import (
	"github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	storeworkspace "github.com/anthropic-ai/super-agent-v3/internal/store/workspace"
	"go.uber.org/fx"
)

var Module = fx.Module("workspace",
	fx.Provide(func(store storeworkspace.Store, emitters *bus.WorkspaceEmitters) Service {
		return NewService(store, emitters)
	}),
	fx.Provide(NewWorkspaceHandlers),
)
