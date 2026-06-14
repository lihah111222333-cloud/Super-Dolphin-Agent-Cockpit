package cron

import (
	"log/slog"

	"github.com/kelindar/event"
	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	cronstore "github.com/anthropic-ai/super-agent-v3/internal/store/cron"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// Module wires the cron service + host RPC handlers, the scheduler, and
// the two Runner actors into the core Fx tree.
//
// TurnSubmitter defaults to NoopTurnSubmitter: the scheduler machinery
// runs end-to-end but every StartTurn fails fast with
// ErrSubmitterNotWired until phase 2b-integrate provides a real
// contract.CronTurnExecutor-backed implementation. Overriding the submitter
// in a parent Fx module is a single fx.Decorate replacing the Noop.
//
// cron.Module 可以在没有 turn stack 的进程里启动，但真正触发会明确失败。
// 不要把 Noop 的 StartTurn 错误吞掉。
var Module = fx.Module("cron",
	fx.Provide(provideStore),
	fx.Provide(NewService),
	fx.Provide(NewHandlers),
	fx.Provide(provideSchedulerConfig),
	fx.Provide(provideTurnSubmitter),
	fx.Provide(provideScheduler),
	fx.Provide(fx.Annotate(provideTickActor, fx.ResultTags(`group:"runners"`))),
	fx.Provide(fx.Annotate(provideLeaseActor, fx.ResultTags(`group:"runners"`))),
	fx.Provide(NewCronProgressSubscribers),
)

// provideStore narrows the fully-featured cronstore.Store into the
// module-local Store interface. cronstore.Store is a superset, so
// the assignment is legal; the narrower facade keeps the service
// layer decoupled from the sqlc-backed implementation surface.
func provideStore(s cronstore.Store) Store { return s }

// provideSchedulerConfig returns the zero SchedulerConfig so callers who
// don't supply one (via fx.Decorate or fx.Supply) get the Default*
// timings from withDefaults().
func provideSchedulerConfig() SchedulerConfig { return SchedulerConfig{} }

// turnSubmitterParams lets provideTurnSubmitter discover an optional
// real CronTurnExecutor + SessionResolver pair. When both are wired the
// factory promotes the seam to TurnServiceAdapter; otherwise it falls
// back to NoopTurnSubmitter so binaries that import cron.Module
// without a turn stack (for example unit tests or the mcp-orch peer)
// still boot — every StartTurn then fails fast with
// ErrSubmitterNotWired, preserving the v1 guarantee that the
// scheduler cannot silently accept work it has no way to execute.
//
// The optional CronThreadStarter drives first-trigger bootstrap: when
// provided, the submitter builds a ThreadServiceBootstrapper and
// attaches it to the adapter so a job with an empty thread_id mints
// its thread on the fly instead of failing with
// ErrJobNotBootstrapped.
//
// Service 和 Resolver 必须一起接入；缺一半时回到 Noop，避免半初始化后才在调度中出错。
type turnSubmitterParams struct {
	fx.In

	Logger        *slog.Logger               `optional:"true"`
	Service       contract.CronTurnExecutor  `optional:"true"`
	Resolver      contract.SessionResolver   `optional:"true"`
	ThreadService contract.CronThreadStarter `optional:"true"`
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

type schedulerParams struct {
	fx.In

	Logger     *slog.Logger
	Store      cronstore.Store
	Submitter  TurnSubmitter
	Cfg        SchedulerConfig
	Dispatcher *event.Dispatcher `optional:"true"`
}

func provideScheduler(p schedulerParams) *Scheduler {
	logger := p.Logger
	if logger == nil {
		logger = pkglogger.Get()
	}
	s := NewScheduler(logger, p.Store, p.Submitter, p.Cfg)
	if p.Dispatcher != nil {
		s.WithDispatcher(p.Dispatcher)
	}
	return s
}

func provideTickActor(logger *slog.Logger, s *Scheduler) contract.Runner {
	return NewTickActor(logger, s)
}

func provideLeaseActor(logger *slog.Logger, s *Scheduler) contract.Runner {
	return NewLeaseActor(logger, s)
}
