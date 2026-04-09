package toolbridge

import (
	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp"
	"go.uber.org/fx"
)

var Module = fx.Module("toolbridge",
	fx.Provide(NewHandler),
	fx.Invoke(func(cfg *config.Config, mgr *codexapp.ServerManager, factory *codexapp.DriverFactory, h *Handler) {
		if cfg == nil || mgr == nil || factory == nil || h == nil {
			return
		}
		if cfg.Provider.DynamicToolsEnabled {
			mgr.SetToolHandler(h.HandleToolCall)
			factory.SetListTools(h.ListToolsForCodex)
		}
	}),
)
