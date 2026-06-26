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

	cronstore "github.com/anthropic-ai/super-agent-v3/internal/store/cron"
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

// Job 是 cronstore.Job 面向 RPC/JSON 消费方的展示投影。
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

// Run 是 cronstore.Run 面向 RPC/JSON 消费方的展示投影。
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

// Store 是 cron service 使用的持久化最小接口。
// 保持窄接口可以让测试只 stub CRUD 面，也避免 service 直接依赖 scheduler 专用 store 方法。
//
// 这个窄接口只给 CRUD service 用。scheduler 的恢复和续租直接用
// cronstore.Store，别塞到这里。
type Store interface {
	CreateJob(ctx context.Context, params cronstore.CreateJobParams) (cronstore.Job, error)
	GetJobByID(ctx context.Context, id string) (cronstore.Job, error)
	ListJobs(ctx context.Context) ([]cronstore.Job, error)
	DeleteJob(ctx context.Context, id string) error
	UpdateJobSchedule(ctx context.Context, params cronstore.UpdateJobScheduleParams) error
	SetJobEnabled(ctx context.Context, id string, enabled bool, now time.Time) error
	PatchNextRunAt(ctx context.Context, id string, nextRunAt time.Time, now time.Time) error
	ListRunsByJob(ctx context.Context, jobID string, limit int32) ([]cronstore.Run, error)
}
