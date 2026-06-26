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

// provideStore 将完整 cronstore.Store 收窄为模块内 Store 接口。
// 这样 service 只依赖 CRUD 需要的 store 面，避免被 sqlc 实现细节牵连。
func provideStore(s cronstore.Store) Store { return s }

// provideSchedulerConfig 返回零值 SchedulerConfig，让 withDefaults 统一补齐默认时序。
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

// provideTurnSubmitter 根据可选依赖决定使用真实 turn adapter 或 NoopTurnSubmitter。
// Service 和 Resolver 必须同时存在；缺一半时 fail-fast 的 Noop 会保留启动能力但拒绝提交 turn。
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

// schedulerParams 收集构造 Scheduler 所需依赖，Dispatcher 可选接入事件发布。
type schedulerParams struct {
	fx.In

	Logger     *slog.Logger
	Store      cronstore.Store
	Submitter  TurnSubmitter
	Cfg        SchedulerConfig
	Dispatcher *event.Dispatcher `optional:"true"`
}

// provideScheduler 构造 Scheduler，并在 Dispatcher 存在时开启事件分发。
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

// provideTickActor 将 tick actor 注入 runner group。
func provideTickActor(logger *slog.Logger, s *Scheduler) contract.Runner {
	return NewTickActor(logger, s)
}

// provideLeaseActor 将 lease heartbeat actor 注入 runner group。
func provideLeaseActor(logger *slog.Logger, s *Scheduler) contract.Runner {
	return NewLeaseActor(logger, s)
}
