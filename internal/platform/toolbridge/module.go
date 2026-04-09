package toolbridge

import (
	"github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp"
	"go.uber.org/fx"
)

var Module = fx.Module("toolbridge",
	fx.Provide(NewHandler),
	fx.Invoke(func(mgr *codexapp.ServerManager, factory *codexapp.DriverFactory, h *Handler) {
		if mgr == nil || factory == nil || h == nil {
			return
		}
		mgr.SetToolHandler(h.HandleToolCall)
		factory.SetListTools(h.ListToolsForCodex)
	}),
)
