package contract

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

const (
	// CronStatusPending is the initial run state before submission starts.
	CronStatusPending = "pending"
	// CronStatusSubmitting means the scheduler is submitting the turn.
	CronStatusSubmitting = "submitting"
	// CronStatusSubmitted means the provider accepted the turn.
	CronStatusSubmitted = "submitted"
	// CronStatusRunning means the submitted turn is still active.
	CronStatusRunning = "running"
	// CronStatusFinished means the run completed successfully.
	CronStatusFinished = "finished"
	// CronStatusFailed means the run reached a terminal failure.
	CronStatusFailed = "failed"
	// CronStatusObserveLost means submission succeeded but observation failed.
	CronStatusObserveLost = "observe_lost"
)

const (
	// CronProviderCodex is the only provider supported by the v1 scheduler.
	CronProviderCodex = "codex"
	// CronProviderClaude is reserved by the persistence layer.
	CronProviderClaude = "claude"
)

var (
	// ErrCronJobNotFound reports a missing cron job.
	ErrCronJobNotFound = errors.New("cron: job not found")
	// ErrCronJobRunNotFound reports a missing cron run.
	ErrCronJobRunNotFound = errors.New("cron: job run not found")
	// ErrCronClaimTokenMismatch reports that a scheduler lost its lease.
	ErrCronClaimTokenMismatch = errors.New("cron: claim token mismatch (lease lost)")
	// ErrCronStatusTransitionRefused reports a failed compare-and-swap transition.
	ErrCronStatusTransitionRefused = errors.New("cron: status transition refused (CAS mismatch)")
	// ErrCronEmptyID reports a missing job or run id.
	ErrCronEmptyID = errors.New("cron: id is required")
	// ErrCronEmptyCWD reports a missing working directory.
	ErrCronEmptyCWD = errors.New("cron: cwd is required")
	// ErrCronEmptyProvider reports a missing provider.
	ErrCronEmptyProvider = errors.New("cron: provider is required")
	// ErrCronEmptyScheduleExpr reports a missing schedule expression.
	ErrCronEmptyScheduleExpr = errors.New("cron: schedule_expr is required")
	// ErrCronEmptyClaimToken reports a missing scheduler claim token.
	ErrCronEmptyClaimToken = errors.New("cron: claim_token is required")
)

// CronStore is the persistence port for scheduled agent tasks and their runs.
type CronStore interface {
	CreateJob(ctx context.Context, params CronCreateJobParams) (CronJob, error)
	GetJobByID(ctx context.Context, id string) (CronJob, error)
	ListJobs(ctx context.Context) ([]CronJob, error)
	DeleteJob(ctx context.Context, id string) error
	UpdateJobSchedule(ctx context.Context, params CronUpdateJobScheduleParams) error
	SetJobEnabled(ctx context.Context, id string, enabled bool, now time.Time) error
	PatchNextRunAt(ctx context.Context, id string, nextRunAt time.Time, now time.Time) error
	ClaimDueJobsForUpdate(ctx context.Context, params CronClaimDueJobsForUpdateParams) ([]CronJob, error)
	RenewLease(ctx context.Context, params CronLeaseParams) error
	ExtendClaim(ctx context.Context, params CronLeaseParams) error
	ReleaseClaim(ctx context.Context, id, claimToken string, now time.Time) error
	MarkFinished(ctx context.Context, params CronMarkFinishedParams) error
	MarkFailed(ctx context.Context, params CronMarkFailedParams) error
	SetActiveTurn(ctx context.Context, params CronSetActiveTurnParams) error
	InsertRun(ctx context.Context, params CronInsertRunParams) (CronRun, error)
	CASRunStatus(ctx context.Context, params CronCASRunStatusParams) error
	SetRunTurn(ctx context.Context, params CronSetRunTurnParams) error
	GetRunByID(ctx context.Context, id string) (CronRun, error)
	GetRunByDedupeKey(ctx context.Context, dedupeKey string) (CronRun, error)
	ListRunsByJob(ctx context.Context, jobID string, limit int32) ([]CronRun, error)
	ListUnresolvedRuns(ctx context.Context) ([]CronRun, error)
	GetRunningRunByTurnID(ctx context.Context, turnID string) (CronRun, error)
	ListJobsClaimedBy(ctx context.Context, claimedBy string) ([]CronJob, error)
}

// CronJob is the persistence projection of a scheduled agent task.
type CronJob struct {
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

// CronRun is the persistence projection of one cron job trigger.
type CronRun struct {
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

// CronCreateJobParams is the input for CronStore.CreateJob.
type CronCreateJobParams struct {
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

// CronUpdateJobScheduleParams replaces the schedulable fields of a job.
type CronUpdateJobScheduleParams struct {
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

// CronClaimDueJobsForUpdateParams drives atomic job claiming.
type CronClaimDueJobsForUpdateParams struct {
	Now            time.Time
	ClaimedBy      string
	LeaseExpiresAt time.Time
	ClaimToken     string
	MaxClaim       int32
}

// CronLeaseParams drives lease renewal and extension.
type CronLeaseParams struct {
	ID             string
	ClaimToken     string
	LeaseExpiresAt time.Time
	Now            time.Time
}

// CronMarkFinishedParams marks a claimed job completed.
type CronMarkFinishedParams struct {
	ID         string
	ClaimToken string
	LastRunAt  time.Time
	LastTurnID string
	NextRunAt  time.Time
	Now        time.Time
}

// CronMarkFailedParams marks a claimed job failed or observe_lost.
type CronMarkFailedParams struct {
	ID          string
	ClaimToken  string
	LastRunAt   time.Time
	LastTurnID  string
	LastStatus  string
	LastErrorAt time.Time
	LastError   string
	NextRetryAt time.Time
	Now         time.Time
}

// CronSetActiveTurnParams binds the active turn to a claimed job.
type CronSetActiveTurnParams struct {
	ID           string
	ClaimToken   string
	ActiveTurnID string
	ThreadID     string
	AgentID      string
	Now          time.Time
}

// CronInsertRunParams creates a run row for one scheduled trigger.
type CronInsertRunParams struct {
	ID             string
	JobID          string
	ScheduledAt    time.Time
	IdempotencyKey string
	DedupeKey      string
	Status         string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// CronCASRunStatusParams drives compare-and-swap run status transitions.
type CronCASRunStatusParams struct {
	ID             string
	ExpectedStatus string
	NextStatus     string
	Error          string
	UpdatedAt      time.Time
}

// CronSetRunTurnParams binds a run to thread/agent/turn identities.
type CronSetRunTurnParams struct {
	ID          string
	ThreadID    string
	AgentID     string
	TurnID      string
	SubmittedAt time.Time
	UpdatedAt   time.Time
}
