package insight

import (
	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	pkglogger "github.com/anthropic-ai/super-agent-v3/internal/platform/logging"
)

// Module wires the insight subscriber + flusher + service + RPC handlers
// into the core Fx tree.
//
// The subscriber is provided into BusModule's bus.subscribers group; the
// flusher is published into the shared runners group so platformrunner.RunGroup
// drives it with the same lifecycle as every other core Runner. Nothing in this
// module imports turn/tracker, keeping the plan's one-way observation wiring
// intact.
var Module = fx.Module("insight",
	fx.Provide(
		provideCollector,
		NewFlusher,
		NewService,
		NewInsightSubscribers,
	),
	fx.Provide(
		fx.Annotate(flusherAsRunner, fx.ResultTags(`group:"runners"`)),
	),
)

func provideCollector(logger *pkglogger.Logger) *collector {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return newCollector(logger, defaultQueueCapacity)
}

// flusherAsRunner narrows *Flusher to the contract Runner interface
// for the `group:"runners"` collector.
func flusherAsRunner(f *Flusher) contract.Runner { return f }
