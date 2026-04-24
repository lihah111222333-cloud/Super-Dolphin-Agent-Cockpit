package insight

import (
	"context"
	"log/slog"

	"github.com/kelindar/event"
	"go.uber.org/fx"

	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// Module wires the insight subscriber + flusher + service + RPC handlers
// into the core Fx tree.
//
// The subscriber is registered via fx.Invoke so it attaches to the bus
// the moment the app starts; the flusher is published into the shared
// runners group so platformrunner.RunGroup drives it with the same
// lifecycle as every other core Runner. Nothing in this module imports
// turn/tracker, keeping the plan's one-way observation wiring intact.
var Module = fx.Module("insight",
	fx.Provide(
		provideCollector,
		NewFlusher,
		NewService,
	),
	fx.Provide(
		fx.Annotate(flusherAsRunner, fx.ResultTags(`group:"runners"`)),
	),
	fx.Invoke(registerCollectorLifecycle),
)

func provideCollector(logger *slog.Logger) *collector {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return newCollector(logger, defaultQueueCapacity)
}

// flusherAsRunner narrows *Flusher to the platformrunner.Runner
// interface for the `group:"runners"` collector.
func flusherAsRunner(f *Flusher) platformrunner.Runner { return f }

type collectorLifecycleParams struct {
	fx.In

	Lifecycle  fx.Lifecycle
	Dispatcher *event.Dispatcher
	Collector  *collector
	Logger     *pkglogger.Logger `optional:"true"`
}

// registerCollectorLifecycle attaches bus subscribers on OnStart and
// cancels them on OnStop. The flusher's shutdown drain is independent
// and governed by the RunGroup via ctx cancellation.
func registerCollectorLifecycle(p collectorLifecycleParams) {
	var cancel func() = func() {}
	p.Lifecycle.Append(fx.Hook{
		OnStart: func(_ context.Context) error {
			cancel = p.Collector.subscribe(p.Dispatcher, p.Logger)
			return nil
		},
		OnStop: func(_ context.Context) error {
			cancel()
			cancel = func() {}
			return nil
		},
	})
}
