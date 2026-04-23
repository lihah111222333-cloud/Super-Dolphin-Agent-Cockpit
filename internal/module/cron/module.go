package cron

import (
	"log/slog"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	thread "github.com/anthropic-ai/super-agent-v3/internal/module/thread"
	turn "github.com/anthropic-ai/super-agent-v3/internal/module/turn"
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

// turnSubmitterParams lets provideTurnSubmitter discover an optional
// real turn.Service + SessionResolver pair. When both are wired the
// factory promotes the seam to TurnServiceAdapter; otherwise it falls
// back to NoopTurnSubmitter so binaries that import cron.Module
// without a turn stack (for example unit tests or the mcp-orch peer)
// still boot — every StartTurn then fails fast with
// ErrSubmitterNotWired, preserving the v1 guarantee that the
// scheduler cannot silently accept work it has no way to execute.
//
// The optional ThreadService drives first-trigger bootstrap: when
// provided, the submitter builds a ThreadServiceBootstrapper and
// attaches it to the adapter so a job with an empty thread_id mints
// its thread on the fly instead of failing with
// ErrJobNotBootstrapped.
type turnSubmitterParams struct {
	fx.In

	Logger        *slog.Logger              `optional:"true"`
	Service       turn.Service              `optional:"true"`
	Resolver      contract.SessionResolver  `optional:"true"`
	ThreadService thread.Service            `optional:"true"`
}

func provideTurnSubmitter(p turnSubmitterParams) TurnSubmitter {
	if p.Service == nil || p.Resolver == nil {
		return NoopTurnSubmitter{}
	}
	adapter := NewTurnServiceAdapter(p.Logger, p.Service, p.Resolver)
	if p.ThreadService != nil {
		adapter.WithBootstrapper(NewThreadServiceBootstrapper(p.Logger, p.ThreadService))
	}
	return adapter
}

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
