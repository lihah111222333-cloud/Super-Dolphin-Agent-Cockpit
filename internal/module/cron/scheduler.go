package cron

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kelindar/event"

	"github.com/lihah111222333-cloud/super-dolphin-agent/pkg/cronmetrics"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// scheduler 默认时序和容量参数。
// 每一项都可由 SchedulerConfig 覆盖，测试可注入短周期或固定容量。
const (
	DefaultLeaseTTL       = 30 * time.Minute
	DefaultLeaseHeartbeat = 5 * time.Minute
	DefaultTickInterval   = 10 * time.Second
	DefaultMaxClaim       = 16

	cronRecoveryFinalizeConflictMetric = "cron_recovery_finalize_conflict_total"
	cronRecoveryFinalizeErrorMetric    = "cron_recovery_finalize_error_total"
)

// StartTurnRequest 是 scheduler 传给 turn 层的最小输入集。
// 不直接暴露 JobRecord，避免 turn 层依赖调度内部字段；新增字段时，也要检查
// buildStartTurnRequest、buildPrepareInput 和首次 bootstrap 是否需要带上。
type StartTurnRequest struct {
	JobID        string
	RunID        string
	DedupeKey    string
	ThreadID     string
	AgentID      string
	Provider     string
	Model        string
	CWD          string
	Config       json.RawMessage
	Skills       []string
	Prompt       string
	ScheduledAt  time.Time
	MaxAttempts  int32
	FailureCount int32
}

// StartTurnResult 是 turn 提交成功后的绑定结果。
// submitter 必须按 DedupeKey 保证幂等；新建线程或 agent 时需要回填 ThreadID/AgentID。
type StartTurnResult struct {
	TurnID   string
	ThreadID string
	AgentID  string
}

// ObservedTurn 是按 DedupeKey 查重得到的已提交 turn 结果。
// Found=false 表示 provider 没有确认提交，恢复流程才允许按失败路径收尾。
type ObservedTurn struct {
	Found  bool
	TurnID string
}

// TurnSubmitter 隔离 cron scheduler 与 turn 执行层。
// 三个方法分工很清楚：StartTurn 提交，Lookup 只给恢复查重，
// Observe 接管已提交 turn。恢复时不要再 StartTurn。
type TurnSubmitter interface {
	// StartTurn 提交一次新 turn，并按 DedupeKey 保持 provider 侧幂等。
	// 相同 DedupeKey 已被接受时必须返回同一个 TurnID。
	StartTurn(ctx context.Context, req StartTurnRequest) (StartTurnResult, error)

	// LookupByDedupeKey 只查 provider 侧去重索引，不提交新 turn。
	// 恢复 submitting 且缺 turn_id 的 run 时必须先查它，避免重复启动。
	LookupByDedupeKey(ctx context.Context, dedupeKey string) (ObservedTurn, error)

	// Observe 接管已经提交的 turn。
	// 成功后 run 可进入 running；永久无法追踪时 scheduler 会转成 observe_lost。
	Observe(ctx context.Context, turnID string) error
}

// ErrTurnNotFound 和 ErrTurnPermissionDenied 表示已提交 turn 无法再被追踪。
// scheduler 不能重新 StartTurn，只能把 run 标记为 observe_lost 并等待人工处理。
var (
	ErrTurnNotFound         = errors.New("cron: turn not found on provider")
	ErrTurnPermissionDenied = errors.New("cron: turn observation permission denied")
)

// NoopTurnSubmitter 是没有真实 turn 层时的默认 submitter。
// StartTurn 必须 fail-fast；Lookup 只返回 Found=false，不制造假 turn。
type NoopTurnSubmitter struct{}

// ErrSubmitterNotWired 表示当前运行时没有接入真实 turn submitter。
var ErrSubmitterNotWired = errors.New("cron: turn submitter is not wired (phase 2b-integrate missing)")

// StartTurn 在 NoopTurnSubmitter 中始终拒绝提交，避免静默丢任务。
func (NoopTurnSubmitter) StartTurn(context.Context, StartTurnRequest) (StartTurnResult, error) {
	return StartTurnResult{}, ErrSubmitterNotWired
}

// LookupByDedupeKey 在 NoopTurnSubmitter 中始终返回未找到。
func (NoopTurnSubmitter) LookupByDedupeKey(context.Context, string) (ObservedTurn, error) {
	return ObservedTurn{Found: false}, nil
}

// Observe 在 NoopTurnSubmitter 中始终返回未接线错误。
func (NoopTurnSubmitter) Observe(context.Context, string) error {
	return ErrSubmitterNotWired
}

// BootstrapRequest 是首次触发且 job.thread_id 为空时交给 ThreadBootstrapper 的输入。
// Config 原样传给下游 provider 绑定解析，bootstrapper 必须返回非空 ThreadID。
type BootstrapRequest struct {
	JobID    string
	Provider string
	Model    string
	CWD      string
	Name     string
	Config   json.RawMessage
}

// BootstrapResult 是线程引导结果。
// ThreadID 会在 StartTurn 成功后由 scheduler 持久化到 cron_jobs，后续触发复用同一线程。
type BootstrapResult struct {
	ThreadID string
	AgentID  string
}

// ThreadBootstrapper 为首次触发的 cron job 创建或解析 agent/thread。
// 没有真实实现时必须 fail-fast，不能把任务静默落到默认身份。
type ThreadBootstrapper interface {
	BootstrapThread(ctx context.Context, req BootstrapRequest) (BootstrapResult, error)
}

// ErrBootstrapperNotWired 表示当前运行时没有接入线程引导器。
// 调用方应把它转成可见失败，而不是在缺 thread_id 的 job 上反复空转。
var ErrBootstrapperNotWired = errors.New("cron: thread bootstrapper is not wired")

// NoopThreadBootstrapper 是未配置线程引导器时的默认实现。
// BootstrapThread 始终返回 ErrBootstrapperNotWired，便于区分未接线和真实引导失败。
type NoopThreadBootstrapper struct{}

// BootstrapThread 在 NoopThreadBootstrapper 中始终返回未接线错误。
func (NoopThreadBootstrapper) BootstrapThread(context.Context, BootstrapRequest) (BootstrapResult, error) {
	return BootstrapResult{}, ErrBootstrapperNotWired
}

// SchedulerConfig 保存 scheduler 可调的时间和容量参数。
// 零值字段会在 withDefaults 中补齐，避免调用方散落默认值。
type SchedulerConfig struct {
	ClaimedBy      string
	LeaseTTL       time.Duration
	LeaseHeartbeat time.Duration
	TickInterval   time.Duration
	MaxClaim       int32
	Backoff        BackoffConfig
}

// withDefaults 补齐 scheduler 配置的默认值。
func (c SchedulerConfig) withDefaults() SchedulerConfig {
	defaultBackoff := DefaultBackoff()
	if strings.TrimSpace(c.ClaimedBy) == "" {
		c.ClaimedBy = "cron-scheduler"
	}
	if c.LeaseTTL <= 0 {
		c.LeaseTTL = DefaultLeaseTTL
	}
	if c.LeaseHeartbeat <= 0 {
		c.LeaseHeartbeat = DefaultLeaseHeartbeat
	}
	if c.TickInterval <= 0 {
		c.TickInterval = DefaultTickInterval
	}
	if c.MaxClaim <= 0 {
		c.MaxClaim = DefaultMaxClaim
	}
	if c.Backoff.Base <= 0 {
		c.Backoff.Base = defaultBackoff.Base
	}
	if c.Backoff.Cap <= 0 {
		c.Backoff.Cap = defaultBackoff.Cap
	}
	return c
}

// Scheduler 负责 cron job 的 claim、submit、observe 和终态标记状态机。
// TickActor 驱动 RunTick，lease actor 驱动 RenewLeases；测试可直接调用这些入口。
type Scheduler struct {
	logger     *slog.Logger
	store      SchedulerStore
	submitter  TurnSubmitter
	cfg        SchedulerConfig
	metrics    *cronmetrics.Metrics
	dispatcher *event.Dispatcher

	// clock/uuid 可被测试替换，保证调度时间和 token 可预测。
	now   func() time.Time
	newID func() string
}

type submittedTurnStore interface {
	SubmitRunWithActiveTurn(ctx context.Context, params SubmitRunWithActiveTurnParams) error
}

// NewScheduler 创建 cron 调度器并注入存储、turn 和恢复指标依赖。
// nil submitter 会替换为 NoopTurnSubmitter，确保未接线时 fail-fast。
func NewScheduler(logger *slog.Logger, store SchedulerStore, submitter TurnSubmitter, cfg SchedulerConfig, metrics *cronmetrics.Metrics) *Scheduler {
	if logger == nil {
		logger = pkglogger.Get()
	}
	if submitter == nil {
		submitter = NoopTurnSubmitter{}
	}
	if metrics == nil {
		panic("cron metrics is required")
	}
	return &Scheduler{
		logger:    logger,
		store:     store,
		submitter: submitter,
		cfg:       cfg.withDefaults(),
		metrics:   metrics,
		now:       time.Now,
		newID:     func() string { return uuid.NewString() },
	}
}

// ClaimToken 生成本轮调度领取用的 token。
// 该方法给 tick actor 使用，避免外部直接依赖 scheduler 的 ID 生成细节。
func (s *Scheduler) ClaimToken() string { return s.newID() }

// RunTick 领取到期 job 并逐个推进提交状态机。
// 多 scheduler 指向同一 DB 时依赖 ClaimDueJobsForUpdate 的原子 UPDATE 防重复领取。
// 一次只 claim 一个 job，再循环到 MaxClaim。这样单个 job 出错不会拖住整轮，
// 也能避免同一 job 被异常反复推进。
func (s *Scheduler) RunTick(ctx context.Context) error {
	var firstErr error
	seen := map[string]struct{}{}
	for i := int32(0); i < s.cfg.MaxClaim; i++ {
		jobs, err := s.claimOneDueJob(ctx)
		if err != nil {
			s.logger.Warn("cron: claim due jobs failed", slog.String("error", err.Error()))
			return err
		}
		if len(jobs) == 0 {
			break
		}
		if _, ok := seen[jobs[0].ID]; ok {
			break
		}
		seen[jobs[0].ID] = struct{}{}
		if err := s.driveJob(ctx, jobs[0]); err != nil {
			firstErr = errors.Join(firstErr, err)
			s.logger.Warn("cron: drive job failed",
				slog.String("job_id", jobs[0].ID),
				slog.String("error", err.Error()),
			)
		}
	}
	return firstErr
}

// claimOneDueJob 领取一个当前到期的 cron job，使用 atomic CAS 防止并发重复领取。
func (s *Scheduler) claimOneDueJob(ctx context.Context) ([]JobRecord, error) {
	now := s.now().UTC()
	return s.store.ClaimDueJobsForUpdate(ctx, ClaimDueJobsForUpdateParams{
		Now:            now,
		ClaimedBy:      s.cfg.ClaimedBy,
		LeaseExpiresAt: now.Add(s.cfg.LeaseTTL),
		ClaimToken:     s.newID(),
		MaxClaim:       1,
	})
}

// driveJob 推进一个已领取 job 的 pending -> submitting -> submitted -> running 状态机。
// 任何终态分支都必须释放 claim；成功完成时由 markFinished 推算下一次 next_run_at。
// 正常只走 pending -> submitting -> submitted -> running，后续靠终态事件结束。
// StartTurn/Observe 失败各有固定落点，不要回退重提。
func (s *Scheduler) driveJob(ctx context.Context, job JobRecord) error {
	now := s.now().UTC()
	scheduledAt := scheduledAtForJob(job, now)
	run, dedupe, err := s.createPendingRun(ctx, job, scheduledAt, now)
	if err != nil {
		return err
	}
	if err := s.markRunSubmitting(ctx, job.ID, run.ID, scheduledAt); err != nil {
		return err
	}
	req, err := buildStartTurnRequest(job, run.ID, dedupe, scheduledAt)
	if err != nil {
		return s.finalizeFailure(ctx, job, run, scheduledAt, err)
	}
	startResult, err := s.submitter.StartTurn(ctx, req)
	if err != nil {
		return s.finalizeFailure(ctx, job, run, scheduledAt, err)
	}
	if err := s.persistSubmittedTurn(ctx, job, run, startResult); err != nil {
		return err
	}
	return s.observeStartedTurn(ctx, job, run, startResult)
}

// scheduledAtForJob 按优先级选取本次运行的 scheduled_at：retry > next_run_at > now。
func scheduledAtForJob(job JobRecord, now time.Time) time.Time {
	if !job.NextRetryAt.IsZero() {
		return job.NextRetryAt
	}
	if !job.NextRunAt.IsZero() {
		return job.NextRunAt
	}
	return now
}

// createPendingRun 插入 pending 状态的 cron run，生成唯一 idempotency_key 和 dedupe_key。
func (s *Scheduler) createPendingRun(ctx context.Context, job JobRecord, scheduledAt, now time.Time) (RunRecord, string, error) {
	idempotencyKey := s.newID()
	dedupe := DedupeKey(job.ID, scheduledAt, idempotencyKey)
	// 每个 run 都生成自己的 idempotency_key；dedupe_key 也只属于这次提交。
	// 别复用 job 级值，否则 retry/RunOnce 会互相影响。
	run, err := s.store.InsertRun(ctx, InsertRunParams{
		ID:             s.newID(),
		JobID:          job.ID,
		ScheduledAt:    scheduledAt,
		IdempotencyKey: idempotencyKey,
		DedupeKey:      dedupe,
		Status:         statusPending,
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	if err == nil {
		s.publishRunState(job.ID, run.ID, statusPending, "", "", scheduledAt)
	}
	return run, dedupe, err
}

// markRunSubmitting 将 run 状态从 pending 原子推进到 submitting。
func (s *Scheduler) markRunSubmitting(ctx context.Context, jobID, runID string, scheduledAt time.Time) error {
	if err := s.store.CASRunStatus(ctx, CASRunStatusParams{
		ID:             runID,
		ExpectedStatus: statusPending,
		NextStatus:     statusSubmitting,
		UpdatedAt:      s.now().UTC(),
	}); err != nil {
		return err
	}
	s.publishRunState(jobID, runID, statusSubmitting, "", "", scheduledAt)
	return nil
}

// buildStartTurnRequest 从 job 行和本次 run 信息构造 StartTurnRequest。
func buildStartTurnRequest(job JobRecord, runID, dedupe string, scheduledAt time.Time) (StartTurnRequest, error) {
	skills, err := decodeSkillList(job.Skills)
	if err != nil {
		return StartTurnRequest{}, err
	}
	return StartTurnRequest{
		JobID:        job.ID,
		RunID:        runID,
		DedupeKey:    dedupe,
		ThreadID:     job.ThreadID,
		AgentID:      job.AgentID,
		Provider:     job.Provider,
		Model:        job.Model,
		CWD:          job.CWD,
		Config:       job.Config,
		Skills:       skills,
		Prompt:       job.Prompt,
		ScheduledAt:  scheduledAt,
		MaxAttempts:  job.MaxAttempts,
		FailureCount: job.FailureCount,
	}, nil
}

// persistSubmittedTurn 保存已提交 turn 和 cron run 的绑定关系。
func (s *Scheduler) persistSubmittedTurn(ctx context.Context, job JobRecord, run RunRecord, result StartTurnResult) error {
	updatedAt := s.now().UTC()
	submitStore, ok := s.store.(submittedTurnStore)
	if !ok {
		return errors.New("cron: store does not implement atomic submitted turn persistence")
	}
	if err := submitStore.SubmitRunWithActiveTurn(ctx, SubmitRunWithActiveTurnParams{
		RunID:        run.ID,
		JobID:        job.ID,
		ClaimToken:   job.ClaimToken,
		ActiveTurnID: result.TurnID,
		ThreadID:     result.ThreadID,
		AgentID:      result.AgentID,
		SubmittedAt:  updatedAt,
		Now:          updatedAt,
	}); err != nil {
		return err
	}
	s.publishRunState(job.ID, run.ID, statusSubmitted, result.TurnID, "", run.ScheduledAt)
	return nil
}

// observeStartedTurn 调用 Observe 将已提交 turn 纳入追踪，失败时转为 observe_lost。
func (s *Scheduler) observeStartedTurn(ctx context.Context, job JobRecord, run RunRecord, result StartTurnResult) error {
	if err := s.submitter.Observe(ctx, result.TurnID); err != nil {
		// 到这里 turn 已被 provider 接收。Observe 失败时只能记为
		// observe_lost，不能再启动一个新 turn。
		return s.finalizeObserveLost(ctx, job, run, result, err)
	}
	if err := s.store.CASRunStatus(ctx, CASRunStatusParams{
		ID:             run.ID,
		ExpectedStatus: statusSubmitted,
		NextStatus:     statusRunning,
		UpdatedAt:      s.now().UTC(),
	}); err != nil {
		return err
	}
	s.publishRunState(job.ID, run.ID, statusRunning, result.TurnID, "", run.ScheduledAt)
	return nil
}

// finalizeFailure 在 StartTurn 失败时将 run 标记为 failed 并计算下一次重试时间。
func (s *Scheduler) finalizeFailure(ctx context.Context, job JobRecord, run RunRecord, scheduledAt time.Time, startErr error) error {
	now := s.now().UTC()
	nextRetry, nextRunAt, err := s.nextRetryAndRun(job, now)
	if err != nil {
		return err
	}
	// run 级 CAS 失败不阻断 job 级 MarkFailed，但要记录，便于排查收尾期 DB 抖动。
	s.casLogPublish(ctx, CASRunStatusParams{
		ID: run.ID, ExpectedStatus: statusSubmitting, NextStatus: statusFailed,
		Error: startErr.Error(), UpdatedAt: now,
	}, "submitting->failed", job.ID, run.ID, statusFailed, "", startErr.Error(), scheduledAt)
	return s.store.MarkFailed(ctx, MarkFailedParams{
		ID:                   job.ID,
		ClaimToken:           job.ClaimToken,
		RunID:                run.ID,
		ExpectedActiveTurnID: "",
		LastRunAt:            scheduledAt,
		LastTurnID:           "",
		LastStatus:           statusFailed,
		LastErrorAt:          now,
		LastError:            startErr.Error(),
		NextRunAt:            nextRunAt,
		NextRetryAt:          nextRetry,
		Now:                  now,
	})
}

// finalizeObserveLost 在 Observe 失败后将 run 标记为 observe_lost，不触发自动重试。
func (s *Scheduler) finalizeObserveLost(ctx context.Context, job JobRecord, run RunRecord, result StartTurnResult, observeErr error) error {
	now := s.now().UTC()
	_, nextRunAt, err := s.nextRetryAndRun(job, now)
	if err != nil {
		return err
	}
	// observe_lost 需要人工看，不能自动 retry。无法跟踪旧 turn 时重试会制造重复任务。
	s.casLogPublish(ctx, CASRunStatusParams{
		ID: run.ID, ExpectedStatus: statusSubmitted, NextStatus: statusObserveLost,
		Error: observeErr.Error(), UpdatedAt: now,
	}, "submitted->observe_lost", job.ID, run.ID, statusObserveLost, result.TurnID, observeErr.Error(), run.ScheduledAt)
	return s.store.MarkFailed(ctx, MarkFailedParams{
		ID:                   job.ID,
		ClaimToken:           job.ClaimToken,
		RunID:                run.ID,
		ExpectedActiveTurnID: result.TurnID,
		LastRunAt:            now,
		LastTurnID:           result.TurnID,
		LastStatus:           statusObserveLost,
		LastErrorAt:          now,
		LastError:            observeErr.Error(),
		NextRunAt:            nextRunAt,
		NextRetryAt:          time.Time{}, // observe_lost 不自动重试，避免重复 turn
		Now:                  now,
	})
}
