package cron

import (
	"log/slog"

	"go.uber.org/fx"

	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	cronstore "github.com/anthropic-ai/super-agent-v3/internal/store/cron"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// Module wires the cron service + host RPC handlers, the scheduler, and
// the two Runner actors into the core Fx tree.
//
// TurnSubmitter defaults to NoopTurnSubmitter: the scheduler machinery
// runs end-to-end but every StartTurn fails fast with
// ErrSubmitterNotWired until phase 2b-integrate provides a real
// internal/module/turn-backed implementation. Overriding the submitter
// in a parent Fx module is a single fx.Decorate replacing the Noop.
var Module = fx.Module("cron",
	fx.Provide(NewService),
	fx.Provide(NewHandlers),
	fx.Provide(provideSchedulerConfig),
	fx.Provide(provideTurnSubmitter),
	fx.Provide(provideScheduler),
	fx.Provide(fx.Annotate(provideTickActor, fx.ResultTags(`group:"runners"`))),
	fx.Provide(fx.Annotate(provideLeaseActor, fx.ResultTags(`group:"runners"`))),
)

// provideSchedulerConfig returns the zero SchedulerConfig so callers who
// don't supply one (via fx.Decorate or fx.Supply) get the Default*
// timings from withDefaults().
func provideSchedulerConfig() SchedulerConfig { return SchedulerConfig{} }

// provideTurnSubmitter hands back a NoopTurnSubmitter by default. Any
// higher-level wiring can override via fx.Decorate.
func provideTurnSubmitter() TurnSubmitter { return NoopTurnSubmitter{} }

func provideScheduler(logger *slog.Logger, store cronstore.Store, submitter TurnSubmitter, cfg SchedulerConfig) *Scheduler {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return NewScheduler(logger, store, submitter, cfg)
}

func provideTickActor(logger *slog.Logger, s *Scheduler) platformrunner.Runner {
	return NewTickActor(logger, s)
}

func provideLeaseActor(logger *slog.Logger, s *Scheduler) platformrunner.Runner {
	return NewLeaseActor(logger, s)
}
