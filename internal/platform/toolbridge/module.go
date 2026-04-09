package toolbridge

import (
	"reflect"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp"
	"go.uber.org/fx"
)

var Module = fx.Module("toolbridge",
	fx.Provide(NewHandler),
	fx.Invoke(func(cfg *config.Config, mgr *codexapp.ServerManager, h *Handler) {
		if cfg == nil || mgr == nil || h == nil {
			return
		}
		if dynamicToolsEnabled(cfg) {
			mgr.SetToolHandler(h.HandleToolCall)
			// TODO: SetListTools 在 driver factory 改造后接入
		}
	}),
)

func dynamicToolsEnabled(cfg *config.Config) bool {
	value := reflect.ValueOf(cfg)
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	provider := value.FieldByName("Provider")
	if !provider.IsValid() {
		return false
	}
	field := provider.FieldByName("DynamicToolsEnabled")
	return field.IsValid() && field.Kind() == reflect.Bool && field.Bool()
}
