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

// SubscriberResult 将单个 SubscriberSpec 输出到 fx group，供 SubscriberGroup 统一启动和取消。
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

// NewSubscriberGroup 收集 fx group 中的订阅声明，并复制一份供生命周期启动使用。
// intake 初始为 true，OnStop 会先关闭 intake 再取消订阅，避免停止阶段接入新回调。
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
