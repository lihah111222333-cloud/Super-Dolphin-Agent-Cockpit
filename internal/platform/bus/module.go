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
		NewThreadEmitters,
		NewLogSink,
		NewSubscriberGroup,
	),
	fx.Invoke(registerLifecycle),
)

type SubscriberResult struct {
	fx.Out

	Spec SubscriberSpec `group:"bus.subscribers"`
}

type subscriberGroupIn struct {
	fx.In

	Dispatcher *event.Dispatcher
	Specs      []SubscriberSpec `group:"bus.subscribers"`
}

func NewSubscriberGroup(in subscriberGroupIn) *SubscriberGroup {
	return &SubscriberGroup{dispatcher: in.Dispatcher, specs: append([]SubscriberSpec(nil), in.Specs...), intake: true}
}

func registerLifecycle(lc fx.Lifecycle, sink *LogSink, dispatcher *event.Dispatcher, subscribers *SubscriberGroup) {
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			if subscribers == nil {
				return nil
			}
			return subscribers.Start()
		},
		OnStop: func(context.Context) error {
			if subscribers != nil {
				subscribers.StopIntake()
				subscribers.Cancel()
			}
			sink.Close()
			if dispatcher == nil {
				return nil
			}
			return dispatcher.Close()
		},
	})
}
