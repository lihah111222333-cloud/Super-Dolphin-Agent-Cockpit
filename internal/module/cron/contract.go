// Package cron exposes the host-side CRUD surface and the scheduler runtime
// for scheduled agent tasks.
//
// 这里有两件事：service 先把坏配置挡住；scheduler 只跑已入库的 job，
// 靠 CAS 和 claim_token 防止重复推进。
package cron

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Service 是 cronjob/* JSON-RPC 方法面向宿主的业务门面。
// 所有方法必须先校验输入再访问 store，并返回已映射的领域错误。
type Service interface {
	CreateJob(ctx context.Context, req CreateJobRequest) (Job, error)
	GetJob(ctx context.Context, id string) (Job, error)
	ListJobs(ctx context.Context) ([]Job, error)
	UpdateJob(ctx context.Context, req UpdateJobRequest) (Job, error)
	SetJobEnabled(ctx context.Context, id string, enabled bool) error
	DeleteJob(ctx context.Context, id string) error
	ListJobRuns(ctx context.Context, jobID string, limit int32) ([]Run, error)
	RunOnce(ctx context.Context, jobID string) (Job, error)
}

// Sentinel errors exposed for RPC mapping.
var (
	ErrProviderNotSupported = errors.New("cron: provider not supported in v1 (only 'codex')")
	ErrMissingCWD           = errors.New("cron: cwd is required")
	ErrMissingName          = errors.New("cron: name is required")
	ErrMissingPrompt        = errors.New("cron: prompt is required")
	ErrMissingSchedule      = errors.New("cron: schedule_expr is required")
	ErrInvalidMaxAttempts   = errors.New("cron: max_attempts must be >= 0")
	ErrInvalidConfig        = errors.New("cron: config is invalid for provider")
	ErrNotFound             = errors.New("cron: job not found")
	ErrJobDisabled          = errors.New("cron: cannot trigger disabled job")
)

const (
	providerCodex = "codex"

	statusPending     = "pending"
	statusSubmitting  = "submitting"
	statusSubmitted   = "submitted"
	statusRunning     = "running"
	statusFinished    = "finished"
	statusFailed      = "failed"
	statusObserveLost = "observe_lost"
)

var (
	errStoreJobNotFound             = errors.New("cron: job not found")
	errStoreJobRunNotFound          = errors.New("cron: job run not found")
	errStoreClaimTokenMismatch      = errors.New("cron: claim token mismatch (lease lost)")
	errStoreStatusTransitionRefused = errors.New("cron: status transition refused (CAS mismatch)")
	errStoreEmptyID                 = errors.New("cron: id is required")
	errStoreEmptyCWD                = errors.New("cron: cwd is required")
	errStoreEmptyProvider           = errors.New("cron: provider is required")
	errStoreEmptyScheduleExpr       = errors.New("cron: schedule_expr is required")
)

// CreateJobRequest 是 CreateJob 的已校验输入。
// NextRunAt 可为空；为空时 service 默认设置为 now+1 minute，让 scheduler 下一轮 tick 可触发。
type CreateJobRequest struct {
	Name          string
	Prompt        string
	ScheduleType  string
	ScheduleExpr  string
	Timezone      string
	Provider      string
	Model         string
	CWD           string
	Config        json.RawMessage
	Skills        []string
	NotifyChannel string
	Enabled       bool
	NextRunAt     time.Time
	MaxAttempts   int32
}

// UpdateJobRequest 是 UpdateJob 的完整替换输入，不提供部分更新语义。
// 调用方需要先 GetJob，再构造完整请求，避免误清空未展示字段。
type UpdateJobRequest struct {
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
	Skills        []string
	NotifyChannel string
	Enabled       bool
	NextRunAt     time.Time
	MaxAttempts   int32
}

// Job 是 cron 模块面向 RPC/JSON 消费方的展示投影。
// 时间字段已转为 RFC3339 字符串，skills JSONB 也已解码为字符串切片。
type Job struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Prompt          string   `json:"prompt"`
	ScheduleType    string   `json:"schedule_type"`
	ScheduleExpr    string   `json:"schedule_expr"`
	Timezone        string   `json:"timezone,omitempty"`
	Provider        string   `json:"provider"`
	Model           string   `json:"model,omitempty"`
	CWD             string   `json:"cwd"`
	Config          any      `json:"config,omitempty"`
	Skills          []string `json:"skills,omitempty"`
	NotifyChannel   string   `json:"notify_channel,omitempty"`
	Enabled         bool     `json:"enabled"`
	NextRunAt       string   `json:"next_run_at,omitempty"`
	LastScheduledAt string   `json:"last_scheduled_at,omitempty"`
	LastRunAt       string   `json:"last_run_at,omitempty"`
	ThreadID        string   `json:"thread_id,omitempty"`
	AgentID         string   `json:"agent_id,omitempty"`
	ActiveTurnID    string   `json:"active_turn_id,omitempty"`
	LastTurnID      string   `json:"last_turn_id,omitempty"`
	FailureCount    int32    `json:"failure_count"`
	MaxAttempts     int32    `json:"max_attempts"`
	LastStatus      string   `json:"last_status,omitempty"`
	LastError       string   `json:"last_error,omitempty"`
	LastErrorAt     string   `json:"last_error_at,omitempty"`
	CreatedAt       string   `json:"created_at,omitempty"`
	UpdatedAt       string   `json:"updated_at,omitempty"`
}

// Run 是 cron 模块面向 RPC/JSON 消费方的运行记录投影。
type Run struct {
	ID             string `json:"id"`
	JobID          string `json:"job_id"`
	ScheduledAt    string `json:"scheduled_at,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	DedupeKey      string `json:"dedupe_key,omitempty"`
	ThreadID       string `json:"thread_id,omitempty"`
	AgentID        string `json:"agent_id,omitempty"`
	TurnID         string `json:"turn_id,omitempty"`
	SubmittedAt    string `json:"submitted_at,omitempty"`
	Status         string `json:"status"`
	Error          string `json:"error,omitempty"`
	CreatedAt      string `json:"created_at,omitempty"`
	UpdatedAt      string `json:"updated_at,omitempty"`
}

type jobRecord struct {
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

type runRecord struct {
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

type createJobParams struct {
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

type updateJobScheduleParams struct {
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

type claimDueJobsForUpdateParams struct {
	Now            time.Time
	ClaimedBy      string
	LeaseExpiresAt time.Time
	ClaimToken     string
	MaxClaim       int32
}

type leaseParams struct {
	ID             string
	ClaimToken     string
	LeaseExpiresAt time.Time
	Now            time.Time
}

type markFinishedParams struct {
	ID                   string
	ClaimToken           string
	RunID                string
	ExpectedActiveTurnID string
	LastRunAt            time.Time
	LastTurnID           string
	NextRunAt            time.Time
	Now                  time.Time
}

type markFailedParams struct {
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

type setActiveTurnParams struct {
	ID           string
	ClaimToken   string
	ActiveTurnID string
	ThreadID     string
	AgentID      string
	Now          time.Time
}

type insertRunParams struct {
	ID             string
	JobID          string
	ScheduledAt    time.Time
	IdempotencyKey string
	DedupeKey      string
	Status         string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type casRunStatusParams struct {
	ID             string
	ExpectedStatus string
	NextStatus     string
	Error          string
	UpdatedAt      time.Time
}

type setRunTurnParams struct {
	ID          string
	ThreadID    string
	AgentID     string
	TurnID      string
	SubmittedAt time.Time
	UpdatedAt   time.Time
}

// Store 是 cron service 使用的持久化最小接口。
// 保持窄接口可以让测试只 stub CRUD 面，也避免 service 直接依赖 scheduler 专用 store 方法。
type Store interface {
	CreateJob(ctx context.Context, params createJobParams) (jobRecord, error)
	GetJobByID(ctx context.Context, id string) (jobRecord, error)
	ListJobs(ctx context.Context) ([]jobRecord, error)
	DeleteJob(ctx context.Context, id string) error
	UpdateJobSchedule(ctx context.Context, params updateJobScheduleParams) error
	SetJobEnabled(ctx context.Context, id string, enabled bool, now time.Time) error
	PatchNextRunAt(ctx context.Context, id string, nextRunAt time.Time, now time.Time) error
	ListRunsByJob(ctx context.Context, jobID string, limit int32) ([]runRecord, error)
}

// SchedulerStore 是 scheduler 状态机使用的持久化端口。
// 它只暴露 claim、run CAS、续租和恢复所需动作，store DTO 在 module.go 里统一转换。
type SchedulerStore interface {
	ClaimDueJobsForUpdate(ctx context.Context, params claimDueJobsForUpdateParams) ([]jobRecord, error)
	RenewLease(ctx context.Context, params leaseParams) error
	ExtendClaim(ctx context.Context, params leaseParams) error
	MarkFinished(ctx context.Context, params markFinishedParams) error
	MarkFailed(ctx context.Context, params markFailedParams) error
	SetActiveTurn(ctx context.Context, params setActiveTurnParams) error

	InsertRun(ctx context.Context, params insertRunParams) (runRecord, error)
	CASRunStatus(ctx context.Context, params casRunStatusParams) error
	SetRunTurn(ctx context.Context, params setRunTurnParams) error
	GetRunningRunByTurnID(ctx context.Context, turnID string) (runRecord, error)
	ListUnresolvedRuns(ctx context.Context) ([]runRecord, error)
	GetJobByID(ctx context.Context, id string) (jobRecord, error)
	ListJobsClaimedBy(ctx context.Context, claimedBy string) ([]jobRecord, error)
}
