package bus

import (
	"context"

	"github.com/kelindar/event"
	"go.uber.org/fx"
)

var Module = fx.Module(
	"bus",
	fx.Provide(
		NewDispatcher,
		NewAgentEmitters,
		NewTurnEmitters,
		NewToolEmitters,
		NewTaskEmitters,
		NewWorkspaceEmitters,
		NewUIEmitters,
		NewLogSink,
	),
	fx.Invoke(registerLifecycle),
)

func registerLifecycle(lc fx.Lifecycle, sink *LogSink, dispatcher *event.Dispatcher) {
	lc.Append(fx.Hook{
		OnStop: func(context.Context) error {
			sink.Close()
			if dispatcher == nil {
				return nil
			}
			return dispatcher.Close()
		},
	})
}
