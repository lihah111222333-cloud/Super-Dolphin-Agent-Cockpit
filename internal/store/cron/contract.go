// Package cron 持久化定时 agent 任务、认领租约和单次运行记录。
//
// 该包只负责数据库边界：cron_jobs 的 CRUD、基于单条 SQLite UPDATE 的原子认领与续租，
// 以及 cron_job_runs 的状态记录。调度器、续租器和恢复策略位于上层 cron 模块，调用方
// 负责生成 claim_token，后续续租、完成和释放都通过 token 校验避免失去租约的 worker 覆盖状态。
//
// Claim 原子性依赖 SQLite 的语句级原子性：UPDATE 和内部 SELECT 子查询作为一个不可分割
// 步骤执行。没有行级锁时，外层 UPDATE 只会写入仍匹配“未认领或租约过期”条件的候选行，
// 从而避免并发 worker 重复认领同一任务。
package cron

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Run 状态常量与数据库 CHECK 约束保持一致，状态跳转由 Store 的 CAS 方法保护。
const (
	StatusPending     = "pending"
	StatusSubmitting  = "submitting"
	StatusSubmitted   = "submitted"
	StatusRunning     = "running"
	StatusFinished    = "finished"
	StatusFailed      = "failed"
	StatusObserveLost = "observe_lost"
)

// Provider 常量与数据库 CHECK 约束保持一致，上层服务可再按运行时能力收窄可用 provider。
const (
	ProviderCodex  = "codex"
	ProviderClaude = "claude"
)

// 哨兵错误暴露给上层通过 errors.Is 判断持久化边界和租约状态。
var (
	ErrJobNotFound             = errors.New("cron: job not found")
	ErrJobRunNotFound          = errors.New("cron: job run not found")
	ErrClaimTokenMismatch      = errors.New("cron: claim token mismatch (lease lost)")
	ErrStatusTransitionRefused = errors.New("cron: status transition refused (CAS mismatch)")
	ErrEmptyID                 = errors.New("cron: id is required")
	ErrEmptyCWD                = errors.New("cron: cwd is required")
	ErrEmptyProvider           = errors.New("cron: provider is required")
	ErrEmptyScheduleExpr       = errors.New("cron: schedule_expr is required")
	ErrEmptyClaimToken         = errors.New("cron: claim_token is required")
)

// Store 是 cron 持久化接口，隔离 sqlc 行结构和上层调度模块。
// 返回值会复制为 Job/Run DTO，调用方不直接依赖数据库生成类型或可空列表达方式。
type Store interface {
	CreateJob(ctx context.Context, params CreateJobParams) (Job, error)
	GetJobByID(ctx context.Context, id string) (Job, error)
	ListJobs(ctx context.Context) ([]Job, error)
	DeleteJob(ctx context.Context, id string) error
	UpdateJobSchedule(ctx context.Context, params UpdateJobScheduleParams) error
	SetJobEnabled(ctx context.Context, id string, enabled bool, now time.Time) error

	// PatchNextRunAt 只更新 next_run_at 和 updated_at，用于即时运行后避免整行读改写。
	PatchNextRunAt(ctx context.Context, id string, nextRunAt time.Time, now time.Time) error

	// ClaimDueJobsForUpdate 原子选择并标记到期任务，只处理未认领或租约已过期的行。
	// LeaseExpiresAt 由调用方基于显式 now 和租期计算，store 不使用数据库当前时间，便于测试和恢复路径复现。
	// 每次认领携带独立 ClaimToken，后续续租、完成和释放都必须带同一 token 作为租约栅栏。
	ClaimDueJobsForUpdate(ctx context.Context, params ClaimDueJobsForUpdateParams) ([]Job, error)

	RenewLease(ctx context.Context, params LeaseParams) error
	ExtendClaim(ctx context.Context, params LeaseParams) error
	ReleaseClaim(ctx context.Context, id, claimToken string, now time.Time) error
	MarkFinished(ctx context.Context, params MarkFinishedParams) error
	MarkFailed(ctx context.Context, params MarkFailedParams) error
	SetActiveTurn(ctx context.Context, params SetActiveTurnParams) error

	InsertRun(ctx context.Context, params InsertRunParams) (Run, error)
	CASRunStatus(ctx context.Context, params CASRunStatusParams) error
	SetRunTurn(ctx context.Context, params SetRunTurnParams) error
	GetRunByID(ctx context.Context, id string) (Run, error)
	GetRunByDedupeKey(ctx context.Context, dedupeKey string) (Run, error)
	ListRunsByJob(ctx context.Context, jobID string, limit int32) ([]Run, error)
	ListUnresolvedRuns(ctx context.Context) ([]Run, error)

	// GetRunningRunByTurnID 查找指定 turn 当前运行中的 run，不存在时返回 ErrJobRunNotFound。
	GetRunningRunByTurnID(ctx context.Context, turnID string) (Run, error)

	// ListJobsClaimedBy 只返回仍由指定调度身份持有且 claim_token 非空的任务。
	ListJobsClaimedBy(ctx context.Context, claimedBy string) ([]Job, error)
}

// Job 是 cron_jobs 行的跨模块 DTO，承载调度、租约、最近 turn 和失败重试状态。
// 时间字段在数据库列为空时为零值，需要区分“未发生”的调用方应显式检查 IsZero。
type Job struct {
	ID              string
	Name            string
	Prompt          string
	ScheduleType    string
	ScheduleExpr    string
	Timezone        string
	Provider        string
	Model           string
	CWD             string
	Config          json.RawMessage
	Skills          json.RawMessage
	NotifyChannel   string
	Enabled         bool
	NextRunAt       time.Time
	LastScheduledAt time.Time
	LastRunAt       time.Time
	ClaimedAt       time.Time
	ClaimedBy       string
	LeaseExpiresAt  time.Time
	ClaimToken      string
	ThreadID        string
	AgentID         string
	ActiveTurnID    string
	LastTurnID      string
	FailureCount    int32
	MaxAttempts     int32
	NextRetryAt     time.Time
	LastStatus      string
	LastErrorAt     time.Time
	LastError       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Run 是 cron_job_runs 行的跨模块 DTO，记录一次触发从提交到终态的持久化状态。
type Run struct {
	ID             string
	JobID          string
	ScheduledAt    time.Time
	IdempotencyKey string
	DedupeKey      string
	ThreadID       string
	AgentID        string
	TurnID         string
	SubmittedAt    time.Time
	Status         string
	Error          string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// CreateJobParams 是创建定时任务的输入，调用方负责提供完整调度表达式和初始下一次运行时间。
type CreateJobParams struct {
	ID            string
	Name          string
	Prompt        string
	ScheduleType  string
	ScheduleExpr  string
	Timezone      string
	Provider      string
	Model         string
	CWD           string
	Config        json.RawMessage
	Skills        json.RawMessage
	NotifyChannel string
	Enabled       bool
	NextRunAt     time.Time
	MaxAttempts   int32
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// UpdateJobScheduleParams 是更新任务配置的输入，会整体覆盖调度、provider、配置和启用状态。
type UpdateJobScheduleParams struct {
	ID            string
	Name          string
	Prompt        string
	ScheduleType  string
	ScheduleExpr  string
	Timezone      string
	Provider      string
	Model         string
	CWD           string
	Config        json.RawMessage
	Skills        json.RawMessage
	NotifyChannel string
	Enabled       bool
	NextRunAt     time.Time
	MaxAttempts   int32
	UpdatedAt     time.Time
}

// ClaimDueJobsForUpdateParams 驱动任务认领，ClaimToken 由调用方为本次租约生成。
// MaxClaim 控制单次认领数量；续租、完成和释放都会校验同一 token，防止被抢占 worker 覆盖状态。
type ClaimDueJobsForUpdateParams struct {
	Now            time.Time
	ClaimedBy      string
	LeaseExpiresAt time.Time
	ClaimToken     string
	MaxClaim       int32
}

// LeaseParams 描述续租和延长租约请求，ID 与 ClaimToken 同时作为租约校验条件。
type LeaseParams struct {
	ID             string
	ClaimToken     string
	LeaseExpiresAt time.Time
	Now            time.Time
}

// MarkFinishedParams 描述任务成功完成后的持久化更新，写入最近 turn 和下一次运行时间。
type MarkFinishedParams struct {
	ID                   string
	ClaimToken           string
	RunID                string
	ExpectedActiveTurnID string
	LastRunAt            time.Time
	LastTurnID           string
	NextRunAt            time.Time
	Now                  time.Time
}

// MarkFailedParams 描述任务失败后的持久化更新，LastStatus 保留上层判定的失败类别并递增失败次数。
type MarkFailedParams struct {
	ID                   string
	ClaimToken           string
	RunID                string
	ExpectedActiveTurnID string
	LastRunAt            time.Time
	LastTurnID           string
	LastStatus           string
	LastErrorAt          time.Time
	LastError            string
	NextRetryAt          time.Time
	Now                  time.Time
}

// SetActiveTurnParams 绑定任务当前活跃 turn，空 ThreadID 或 AgentID 会保留已有身份信息。
// 该行为允许恢复路径只补 turn id，而不覆盖此前已记录的 thread/agent 归属。
type SetActiveTurnParams struct {
	ID           string
	ClaimToken   string
	ActiveTurnID string
	ThreadID     string
	AgentID      string
	Now          time.Time
}

// InsertRunParams 描述一次触发记录的创建输入，Status 必须使用本包定义的 run 状态常量。
type InsertRunParams struct {
	ID             string
	JobID          string
	ScheduledAt    time.Time
	IdempotencyKey string
	DedupeKey      string
	Status         string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// CASRunStatusParams 描述 run 状态的比较并交换更新，当前状态不匹配时返回 ErrStatusTransitionRefused。
type CASRunStatusParams struct {
	ID             string
	ExpectedStatus string
	NextStatus     string
	Error          string
	UpdatedAt      time.Time
}

// SetRunTurnParams 绑定 run 对应的 turn，空 ThreadID 或 AgentID 会保留数据库已有值。
type SetRunTurnParams struct {
	ID          string
	ThreadID    string
	AgentID     string
	TurnID      string
	SubmittedAt time.Time
	UpdatedAt   time.Time
}
