package workspace

import "go.uber.org/fx"

var Module = fx.Module("workspace",
	fx.Provide(NewService),
	fx.Provide(NewWorkspaceHandlers),
)
