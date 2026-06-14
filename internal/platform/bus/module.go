package bus

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
	"go.uber.org/fx"
)

var Module = fx.Module(
	"bus",
	fx.Provide(
		NewDispatcher,
		NewThreadEmitters,
		NewUISharedFilesChangedEmitter,
		provideLogSink,
		NewSubscriberGroup,
	),
	fx.Invoke(registerLifecycle),
)

// SubscriberResult is the fx-compatible output wrapper for a SubscriberSpec.
type SubscriberResult struct {
	fx.Out

	Spec contract.SubscriberSpec `group:"bus.subscribers"`
}

type subscriberGroupIn struct {
	fx.In

	Dispatcher *event.Dispatcher
	Specs      []SubscriberSpec `group:"bus.subscribers"`
}

type logSinkParams struct {
	fx.In

	Dispatcher *event.Dispatcher
	Logger     *pkglogger.Logger
	Trace      TraceRecorder `optional:"true"`
}

func provideLogSink(p logSinkParams) *LogSink {
	return NewLogSink(LogSinkDeps{
		Dispatcher: p.Dispatcher,
		Logger:     p.Logger,
		Trace:      p.Trace,
	})
}

// NewSubscriberGroup 创建订阅器group。
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
