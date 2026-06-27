// Package turn 负责 turn 生命周期管理：输入组装、provider 提交、状态追踪与中断处理。
package turn

import (
	"context"
	"errors"
	"time"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	turndedupe "github.com/anthropic-ai/super-agent-v3/internal/store/turndedupe"
)

var Module = fx.Module("turn",
	fx.Provide(
		fx.Annotate(
			provideTurnDedupeStore,
			fx.ParamTags(`optional:"true"`),
		),
		fx.Annotate(
			NewServiceWithPromptAssemblyAndTurnContext,
			// 这些跨模块依赖均为可选注入：缺失时 turn 仍能启动，只跳过对应的 skill、observation、dedupe 或 tracing 能力。
			fx.ParamTags("", `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`),
			// 同时发布完整 turn.Service 和窄接口 TurnThreadCleaner，避免 thread 模块反向导入 turn 包。
			fx.As(new(Service)),
			fx.As(new(contract.TurnThreadCleaner)),
		),
		// 发布 cron 使用的窄执行器接口，避免 cron 模块直接依赖 turn 包。
		provideCronTurnExecutor,
		NewOrchestrationTurnStarter,
		fx.Annotate(
			NewTurnHandlers,
			fx.ParamTags("", `optional:"true"`, "", `optional:"true"`, "", `optional:"true"`),
		),
		// 轨迹收集器允许缺少 observation 依赖；这种部署仍能启动，只是不补终态/token/skill 快照。
		fx.Annotate(
			NewTrajectoryCollector,
			fx.ParamTags(`optional:"true"`, `optional:"true"`),
		),
		fx.Annotate(
			NewTrajectorySubscribers,
			fx.ParamTags("", `optional:"true"`, `optional:"true"`),
		),
		// skill evaluator 是无外部依赖的纯判断器，直接提供默认实现即可。
		NewDefaultEvaluator,
		func(e *DefaultEvaluator) Evaluator { return e },
		func() Redactor { return NewDefaultRedactor() },
	),
	fx.Invoke(registerTurnServiceLifecycle),
)

// registerTurnServiceLifecycle 把 turn.Service 挂入 fx.Lifecycle，确保应用停止时取消后台 watcher。
// Shutdown 通过私有接口断言发现，避免把生命周期方法暴露进公共 Service 契约。
func registerTurnServiceLifecycle(lc fx.Lifecycle, svc Service) {
	if svc == nil {
		return
	}
	type shutdowner interface{ Shutdown() }
	sd, ok := svc.(shutdowner)
	if !ok {
		return
	}
	lc.Append(fx.Hook{
		OnStop: func(context.Context) error {
			sd.Shutdown()
			return nil
		},
	})
}

// provideCronTurnExecutor 把 turn.Service 包装为 cron 使用的窄接口，避免 cron 模块直接导入 turn 包。
func provideCronTurnExecutor(svc Service) contract.CronTurnExecutor {
	return NewCronExecutorAdapter(svc)
}

type turnDedupeStoreAdapter struct {
	store turndedupe.Store
}

// provideTurnDedupeStore 把 store/turndedupe 装配成 turn 服务可消费的契约端口。
// store 缺失时保持原有 optional 语义，turn 会退回仅使用进程内 tracker。
func provideTurnDedupeStore(store turndedupe.Store) turnDedupeStore {
	if store == nil {
		return nil
	}
	return turnDedupeStoreAdapter{store: store}
}

// Upsert 将 turn 模块的去重写入参数转换为 store/turndedupe 参数。
func (a turnDedupeStoreAdapter) Upsert(ctx context.Context, p turnDedupeUpsertParams) error {
	return a.store.Upsert(ctx, turndedupe.UpsertParams{
		DedupeKey:   p.DedupeKey,
		LocalTurnID: p.LocalTurnID,
		ThreadID:    p.ThreadID,
		Now:         p.Now,
	})
}

// BindProviderTurnID 将 provider turn ID 回写请求转发到持久化 registry。
func (a turnDedupeStoreAdapter) BindProviderTurnID(ctx context.Context, p turnDedupeBindProviderTurnIDParams) error {
	return a.store.BindProviderTurnID(ctx, turndedupe.BindProviderTurnIDParams{
		DedupeKey:      p.DedupeKey,
		ProviderTurnID: p.ProviderTurnID,
		Now:            p.Now,
	})
}

// MarkTerminal 标记 dedupe key 已进入终态，后续 live 查询不再复用该记录。
func (a turnDedupeStoreAdapter) MarkTerminal(ctx context.Context, dedupeKey string, now time.Time) error {
	return a.store.MarkTerminal(ctx, dedupeKey, now)
}

// GetLive 读取 live registry 行，并把 store not-found 映射为 turn 模块哨兵错误。
func (a turnDedupeStoreAdapter) GetLive(ctx context.Context, dedupeKey string) (turnDedupeEntry, error) {
	entry, err := a.store.GetLive(ctx, dedupeKey)
	if err != nil {
		if errors.Is(err, turndedupe.ErrNotFound) {
			return turnDedupeEntry{}, errTurnDedupeNotFound
		}
		return turnDedupeEntry{}, err
	}
	return turnDedupeEntry{
		DedupeKey:      entry.DedupeKey,
		LocalTurnID:    entry.LocalTurnID,
		ProviderTurnID: entry.ProviderTurnID,
		ThreadID:       entry.ThreadID,
		CreatedAt:      entry.CreatedAt,
		UpdatedAt:      entry.UpdatedAt,
		TerminalAt:     entry.TerminalAt,
	}, nil
}
