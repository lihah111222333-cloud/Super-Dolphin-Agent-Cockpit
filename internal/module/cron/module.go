package cron

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

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
	fx.Provide(newCronStoreAdapter),
	fx.Provide(provideStore),
	fx.Provide(provideSchedulerStore),
	fx.Provide(NewService),
	fx.Provide(NewHandlers),
	fx.Provide(provideSchedulerConfig),
	fx.Provide(provideTurnSubmitter),
	fx.Provide(provideScheduler),
	fx.Provide(fx.Annotate(provideTickActor, fx.ResultTags(`group:"runners"`))),
	fx.Provide(fx.Annotate(provideLeaseActor, fx.ResultTags(`group:"runners"`))),
	fx.Provide(NewCronProgressSubscribers),
)

type cronStoreAdapter struct {
	*cronSubmitAdapter
	store cronstore.Store
}

type cronSubmitAdapter struct {
	store cronstore.Store
}

// newCronStoreAdapter 把 store.cron 的完整持久化接口收在 module 装配边界内。
// 其它 cron 生产文件只能依赖本包窄端口，避免继续泄露 store DTO 和状态常量。
func newCronStoreAdapter(store cronstore.Store) *cronStoreAdapter {
	return &cronStoreAdapter{
		cronSubmitAdapter: &cronSubmitAdapter{store: store},
		store:             store,
	}
}

// provideStore 将完整 cron 持久化实现收窄为 service CRUD 端口。
func provideStore(adapter *cronStoreAdapter) Store { return adapter }

// provideSchedulerStore 将完整 cron 持久化实现收窄为 scheduler 状态机端口。
func provideSchedulerStore(adapter *cronStoreAdapter) SchedulerStore { return adapter }

// CreateJob 把 service 的创建参数转换为 store 入参，并把返回行转成本包记录。
func (a *cronStoreAdapter) CreateJob(ctx context.Context, p createJobParams) (jobRecord, error) {
	row, err := a.store.CreateJob(ctx, cronstore.CreateJobParams{
		ID:            p.ID,
		Name:          p.Name,
		Prompt:        p.Prompt,
		ScheduleType:  p.ScheduleType,
		ScheduleExpr:  p.ScheduleExpr,
		Timezone:      p.Timezone,
		Provider:      p.Provider,
		Model:         p.Model,
		CWD:           p.CWD,
		Config:        p.Config,
		Skills:        p.Skills,
		NotifyChannel: p.NotifyChannel,
		Enabled:       p.Enabled,
		NextRunAt:     p.NextRunAt,
		MaxAttempts:   p.MaxAttempts,
		CreatedAt:     p.CreatedAt,
		UpdatedAt:     p.UpdatedAt,
	})
	return fromStoreJob(row), mapCronStoreError(err)
}

// GetJobByID 按 ID 读取任务，并屏蔽 store 层的 DTO 和错误类型。
func (a *cronStoreAdapter) GetJobByID(ctx context.Context, id string) (jobRecord, error) {
	row, err := a.store.GetJobByID(ctx, id)
	return fromStoreJob(row), mapCronStoreError(err)
}

// ListJobs 列出任务并逐条转换为 cron 模块内部记录。
func (a *cronStoreAdapter) ListJobs(ctx context.Context) ([]jobRecord, error) {
	rows, err := a.store.ListJobs(ctx)
	if err != nil {
		return nil, mapCronStoreError(err)
	}
	return fromStoreJobs(rows), nil
}

// DeleteJob 删除任务，并把 not found 等 store 错误映射到本包错误。
func (a *cronStoreAdapter) DeleteJob(ctx context.Context, id string) error {
	return mapCronStoreError(a.store.DeleteJob(ctx, id))
}

// UpdateJobSchedule 覆盖任务调度配置，避免 service 直接构造 store 入参。
func (a *cronStoreAdapter) UpdateJobSchedule(ctx context.Context, p updateJobScheduleParams) error {
	return mapCronStoreError(a.store.UpdateJobSchedule(ctx, cronstore.UpdateJobScheduleParams{
		ID:            p.ID,
		Name:          p.Name,
		Prompt:        p.Prompt,
		ScheduleType:  p.ScheduleType,
		ScheduleExpr:  p.ScheduleExpr,
		Timezone:      p.Timezone,
		Provider:      p.Provider,
		Model:         p.Model,
		CWD:           p.CWD,
		Config:        p.Config,
		Skills:        p.Skills,
		NotifyChannel: p.NotifyChannel,
		Enabled:       p.Enabled,
		NextRunAt:     p.NextRunAt,
		MaxAttempts:   p.MaxAttempts,
		UpdatedAt:     p.UpdatedAt,
	}))
}

// SetJobEnabled 切换任务启停状态，并统一转换 store 层错误。
func (a *cronStoreAdapter) SetJobEnabled(ctx context.Context, id string, enabled bool, now time.Time) error {
	return mapCronStoreError(a.store.SetJobEnabled(ctx, id, enabled, now))
}

// PatchNextRunAt 更新下一次运行时间，供手动触发和调度推进复用。
func (a *cronStoreAdapter) PatchNextRunAt(ctx context.Context, id string, nextRunAt time.Time, now time.Time) error {
	return mapCronStoreError(a.store.PatchNextRunAt(ctx, id, nextRunAt, now))
}

// ListRunsByJob 查询任务运行记录，并返回本包 runRecord 切片。
func (a *cronStoreAdapter) ListRunsByJob(ctx context.Context, jobID string, limit int32) ([]runRecord, error) {
	rows, err := a.store.ListRunsByJob(ctx, jobID, limit)
	if err != nil {
		return nil, mapCronStoreError(err)
	}
	return fromStoreRuns(rows), nil
}

// ClaimDueJobsForUpdate 认领到期任务，并把 claim 入参限制在 scheduler 所需字段内。
func (a *cronStoreAdapter) ClaimDueJobsForUpdate(ctx context.Context, p claimDueJobsForUpdateParams) ([]jobRecord, error) {
	rows, err := a.store.ClaimDueJobsForUpdate(ctx, cronstore.ClaimDueJobsForUpdateParams{
		Now:            p.Now,
		ClaimedBy:      p.ClaimedBy,
		LeaseExpiresAt: p.LeaseExpiresAt,
		ClaimToken:     p.ClaimToken,
		MaxClaim:       p.MaxClaim,
	})
	if err != nil {
		return nil, mapCronStoreError(err)
	}
	return fromStoreJobs(rows), nil
}

// RenewLease 延长当前 worker 持有的 job 租约。
func (a *cronStoreAdapter) RenewLease(ctx context.Context, p leaseParams) error {
	return mapCronStoreError(a.store.RenewLease(ctx, cronstore.LeaseParams{
		ID:             p.ID,
		ClaimToken:     p.ClaimToken,
		LeaseExpiresAt: p.LeaseExpiresAt,
		Now:            p.Now,
	}))
}

// ExtendClaim 处理 turn progress 触发的租约延长，失败时保留本包错误语义。
func (a *cronStoreAdapter) ExtendClaim(ctx context.Context, p leaseParams) error {
	return mapCronStoreError(a.store.ExtendClaim(ctx, cronstore.LeaseParams{
		ID:             p.ID,
		ClaimToken:     p.ClaimToken,
		LeaseExpiresAt: p.LeaseExpiresAt,
		Now:            p.Now,
	}))
}

// MarkFinished 按 claim 栅栏标记任务成功完成，并写入下一次运行时间。
func (a *cronStoreAdapter) MarkFinished(ctx context.Context, p markFinishedParams) error {
	return mapCronStoreError(a.store.MarkFinished(ctx, cronstore.MarkFinishedParams{
		ID:                   p.ID,
		ClaimToken:           p.ClaimToken,
		RunID:                p.RunID,
		ExpectedActiveTurnID: p.ExpectedActiveTurnID,
		LastRunAt:            p.LastRunAt,
		LastTurnID:           p.LastTurnID,
		NextRunAt:            p.NextRunAt,
		Now:                  p.Now,
	}))
}

// MarkFailed 按 claim 栅栏记录失败或 observe_lost 状态。
func (a *cronStoreAdapter) MarkFailed(ctx context.Context, p markFailedParams) error {
	return mapCronStoreError(a.store.MarkFailed(ctx, cronstore.MarkFailedParams{
		ID:                   p.ID,
		ClaimToken:           p.ClaimToken,
		RunID:                p.RunID,
		ExpectedActiveTurnID: p.ExpectedActiveTurnID,
		LastRunAt:            p.LastRunAt,
		LastTurnID:           p.LastTurnID,
		LastStatus:           p.LastStatus,
		LastErrorAt:          p.LastErrorAt,
		LastError:            p.LastError,
		NextRunAt:            p.NextRunAt,
		NextRetryAt:          p.NextRetryAt,
		Now:                  p.Now,
	}))
}

// SetActiveTurn 绑定 job 当前活跃 turn，并保留 thread/agent 身份信息。
func (a *cronStoreAdapter) SetActiveTurn(ctx context.Context, p setActiveTurnParams) error {
	return mapCronStoreError(a.store.SetActiveTurn(ctx, cronstore.SetActiveTurnParams{
		ID:           p.ID,
		ClaimToken:   p.ClaimToken,
		ActiveTurnID: p.ActiveTurnID,
		ThreadID:     p.ThreadID,
		AgentID:      p.AgentID,
		Now:          p.Now,
	}))
}

// SubmitRunWithActiveTurn 原子保存 run turn、job active turn 和 submitted 状态。
func (a *cronSubmitAdapter) SubmitRunWithActiveTurn(ctx context.Context, p submitRunWithActiveTurnParams) error {
	return mapCronStoreError(a.store.SubmitRunWithActiveTurn(ctx, cronstore.SubmitRunWithActiveTurnParams{
		RunID:        p.RunID,
		JobID:        p.JobID,
		ClaimToken:   p.ClaimToken,
		ActiveTurnID: p.ActiveTurnID,
		ThreadID:     p.ThreadID,
		AgentID:      p.AgentID,
		SubmittedAt:  p.SubmittedAt,
		Now:          p.Now,
	}))
}

// InsertRun 创建一次 run 记录，返回值会转成本包 runRecord。
func (a *cronStoreAdapter) InsertRun(ctx context.Context, p insertRunParams) (runRecord, error) {
	row, err := a.store.InsertRun(ctx, cronstore.InsertRunParams{
		ID:             p.ID,
		JobID:          p.JobID,
		ScheduledAt:    p.ScheduledAt,
		IdempotencyKey: p.IdempotencyKey,
		DedupeKey:      p.DedupeKey,
		Status:         p.Status,
		CreatedAt:      p.CreatedAt,
		UpdatedAt:      p.UpdatedAt,
	})
	return fromStoreRun(row), mapCronStoreError(err)
}

// CASRunStatus 执行 run 状态比较交换，避免 scheduler 依赖 store 参数类型。
func (a *cronStoreAdapter) CASRunStatus(ctx context.Context, p casRunStatusParams) error {
	return mapCronStoreError(a.store.CASRunStatus(ctx, cronstore.CASRunStatusParams{
		ID:             p.ID,
		ExpectedStatus: p.ExpectedStatus,
		NextStatus:     p.NextStatus,
		Error:          p.Error,
		UpdatedAt:      p.UpdatedAt,
	}))
}

// SetRunTurn 记录 run 对应的实际 turn 信息。
func (a *cronStoreAdapter) SetRunTurn(ctx context.Context, p setRunTurnParams) error {
	return mapCronStoreError(a.store.SetRunTurn(ctx, cronstore.SetRunTurnParams{
		ID:          p.ID,
		ThreadID:    p.ThreadID,
		AgentID:     p.AgentID,
		TurnID:      p.TurnID,
		SubmittedAt: p.SubmittedAt,
		UpdatedAt:   p.UpdatedAt,
	}))
}

// GetRunningRunByTurnID 查找当前 running run，用于终态事件收尾。
func (a *cronStoreAdapter) GetRunningRunByTurnID(ctx context.Context, turnID string) (runRecord, error) {
	row, err := a.store.GetRunningRunByTurnID(ctx, turnID)
	return fromStoreRun(row), mapCronStoreError(err)
}

// GetSubmittedOrRunningRunByTurnID 查找可由终态事件收尾的 submitted/running run。
func (a *cronSubmitAdapter) GetSubmittedOrRunningRunByTurnID(ctx context.Context, turnID string) (runRecord, error) {
	row, err := a.store.GetSubmittedOrRunningRunByTurnID(ctx, turnID)
	return fromStoreRun(row), mapCronStoreError(err)
}

// ListUnresolvedRuns 列出恢复流程需要接管的 run。
func (a *cronStoreAdapter) ListUnresolvedRuns(ctx context.Context) ([]runRecord, error) {
	rows, err := a.store.ListUnresolvedRuns(ctx)
	if err != nil {
		return nil, mapCronStoreError(err)
	}
	return fromStoreRuns(rows), nil
}

// ListUnresolvedRunsPage 分页列出恢复流程需要接管的 run。
func (a *cronSubmitAdapter) ListUnresolvedRunsPage(ctx context.Context, limit int32, cursor string) ([]runRecord, error) {
	rows, err := a.store.ListUnresolvedRunsPage(ctx, limit, cursor)
	if err != nil {
		return nil, mapCronStoreError(err)
	}
	return fromStoreRuns(rows), nil
}

// ListJobsClaimedBy 查询当前调度身份仍持有的 job，用于续租和 progress 延长。
func (a *cronStoreAdapter) ListJobsClaimedBy(ctx context.Context, claimedBy string) ([]jobRecord, error) {
	rows, err := a.store.ListJobsClaimedBy(ctx, claimedBy)
	if err != nil {
		return nil, mapCronStoreError(err)
	}
	return fromStoreJobs(rows), nil
}

func fromStoreJobs(rows []cronstore.Job) []jobRecord {
	out := make([]jobRecord, len(rows))
	for i, row := range rows {
		out[i] = fromStoreJob(row)
	}
	return out
}

// fromStoreJob 复制 store Job 字段到本包记录，JSON 字节会复制后再交给上层。
func fromStoreJob(row cronstore.Job) jobRecord {
	return jobRecord{
		ID:              row.ID,
		Name:            row.Name,
		Prompt:          row.Prompt,
		ScheduleType:    row.ScheduleType,
		ScheduleExpr:    row.ScheduleExpr,
		Timezone:        row.Timezone,
		Provider:        row.Provider,
		Model:           row.Model,
		CWD:             row.CWD,
		Config:          cloneStoreJSON(row.Config),
		Skills:          cloneStoreJSON(row.Skills),
		NotifyChannel:   row.NotifyChannel,
		Enabled:         row.Enabled,
		NextRunAt:       row.NextRunAt,
		LastScheduledAt: row.LastScheduledAt,
		LastRunAt:       row.LastRunAt,
		ClaimedAt:       row.ClaimedAt,
		ClaimedBy:       row.ClaimedBy,
		LeaseExpiresAt:  row.LeaseExpiresAt,
		ClaimToken:      row.ClaimToken,
		ThreadID:        row.ThreadID,
		AgentID:         row.AgentID,
		ActiveTurnID:    row.ActiveTurnID,
		LastTurnID:      row.LastTurnID,
		FailureCount:    row.FailureCount,
		MaxAttempts:     row.MaxAttempts,
		NextRetryAt:     row.NextRetryAt,
		LastStatus:      row.LastStatus,
		LastErrorAt:     row.LastErrorAt,
		LastError:       row.LastError,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
	}
}

func fromStoreRuns(rows []cronstore.Run) []runRecord {
	out := make([]runRecord, len(rows))
	for i, row := range rows {
		out[i] = fromStoreRun(row)
	}
	return out
}

func fromStoreRun(row cronstore.Run) runRecord {
	return runRecord{
		ID:             row.ID,
		JobID:          row.JobID,
		ScheduledAt:    row.ScheduledAt,
		IdempotencyKey: row.IdempotencyKey,
		DedupeKey:      row.DedupeKey,
		ThreadID:       row.ThreadID,
		AgentID:        row.AgentID,
		TurnID:         row.TurnID,
		SubmittedAt:    row.SubmittedAt,
		Status:         row.Status,
		Error:          row.Error,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
	}
}

func cloneStoreJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	out := make([]byte, len(raw))
	copy(out, raw)
	return out
}

// mapCronStoreError 把 store 哨兵错误换成本包错误，避免非 assembly 文件依赖 store 包。
func mapCronStoreError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, cronstore.ErrJobNotFound):
		return fmt.Errorf("%w: %v", errStoreJobNotFound, err)
	case errors.Is(err, cronstore.ErrJobRunNotFound):
		return fmt.Errorf("%w: %v", errStoreJobRunNotFound, err)
	case errors.Is(err, cronstore.ErrClaimTokenMismatch):
		return fmt.Errorf("%w: %v", errStoreClaimTokenMismatch, err)
	case errors.Is(err, cronstore.ErrStatusTransitionRefused):
		return fmt.Errorf("%w: %v", errStoreStatusTransitionRefused, err)
	case errors.Is(err, cronstore.ErrEmptyID):
		return fmt.Errorf("%w: %v", errStoreEmptyID, err)
	case errors.Is(err, cronstore.ErrEmptyCWD):
		return fmt.Errorf("%w: %v", errStoreEmptyCWD, err)
	case errors.Is(err, cronstore.ErrEmptyProvider):
		return fmt.Errorf("%w: %v", errStoreEmptyProvider, err)
	case errors.Is(err, cronstore.ErrEmptyScheduleExpr):
		return fmt.Errorf("%w: %v", errStoreEmptyScheduleExpr, err)
	default:
		return err
	}
}

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
	Store      SchedulerStore
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
