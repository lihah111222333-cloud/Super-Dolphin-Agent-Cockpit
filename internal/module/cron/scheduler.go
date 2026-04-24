package cron

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	cronstore "github.com/anthropic-ai/super-agent-v3/internal/store/cron"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// Defaults match the P1b plan. Every one of them is overridable via the
// SchedulerConfig so tests can pin time-sensitive behaviour.
const (
	DefaultLeaseTTL       = 30 * time.Minute
	DefaultLeaseHeartbeat = 5 * time.Minute
	DefaultTickInterval   = 10 * time.Second
	DefaultMaxClaim       = 16
)

// StartTurnRequest is the subset of inputs the scheduler passes to the
// turn layer. We expose a narrow struct (rather than passing the
// cron.Job wholesale) so the turn.Service extension in the integrate PR
// only sees what it needs.
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

// StartTurnResult captures the outcome of StartTurn. The submitter
// guarantees that DedupeKey uniqueness is honoured at the provider level
// — multiple StartTurn calls with the same DedupeKey must return the
// same TurnID. ThreadID / AgentID may be populated when a fresh agent
// binding is created.
type StartTurnResult struct {
	TurnID   string
	ThreadID string
	AgentID  string
}

// ObservedTurn is the result of TurnSubmitter.LookupByDedupeKey. When
// Found is false the caller treats the trigger as "never submitted".
type ObservedTurn struct {
	Found  bool
	TurnID string
}

// TurnSubmitter is the seam between the cron scheduler and the turn
// layer. The real implementation (internal/module/turn) lands in the
// follow-up integrate PR; this module's default production wiring plugs
// in NoopTurnSubmitter so the scheduler can ship + be tested end-to-end
// without dragging in the full turn stack.
type TurnSubmitter interface {
	// StartTurn submits a new turn, idempotent on DedupeKey. When the
	// provider has already accepted a turn for the same DedupeKey the
	// submitter must return the existing TurnID unchanged.
	StartTurn(ctx context.Context, req StartTurnRequest) (StartTurnResult, error)

	// LookupByDedupeKey probes the provider-side dedupe index without
	// submitting. Used by crash recovery: a run stuck in "submitting"
	// with no turn_id must not re-issue StartTurn; instead the scheduler
	// asks whether the provider already accepted one.
	LookupByDedupeKey(ctx context.Context, dedupeKey string) (ObservedTurn, error)

	// Observe registers an observer for a turn that has already been
	// submitted. Returning nil means the caller can move the run into
	// running / finished based on later terminal events; returning an
	// error classified as permanently-lost (see NotFound / NotRecoverable
	// below) moves the run into observe_lost.
	Observe(ctx context.Context, turnID string) error
}

// ErrTurnNotFound / ErrTurnPermissionDenied map to the P1b plan's
// observe_lost terminal: the scheduler refuses to re-StartTurn once a
// run has already been submitted, and observation is also unavailable,
// so the only safe outcome is to mark the run observe_lost and alert.
var (
	ErrTurnNotFound         = errors.New("cron: turn not found on provider")
	ErrTurnPermissionDenied = errors.New("cron: turn observation permission denied")
)

// NoopTurnSubmitter is the default provider used when the app boots
// without a real turn layer wired. Every StartTurn fails fast with
// ErrSubmitterNotWired so no silent data loss occurs; LookupByDedupeKey
// always reports Found=false.
type NoopTurnSubmitter struct{}

// ErrSubmitterNotWired flags that the NoopTurnSubmitter is in use.
var ErrSubmitterNotWired = errors.New("cron: turn submitter is not wired (phase 2b-integrate missing)")

func (NoopTurnSubmitter) StartTurn(context.Context, StartTurnRequest) (StartTurnResult, error) {
	return StartTurnResult{}, ErrSubmitterNotWired
}
func (NoopTurnSubmitter) LookupByDedupeKey(context.Context, string) (ObservedTurn, error) {
	return ObservedTurn{Found: false}, nil
}
func (NoopTurnSubmitter) Observe(context.Context, string) error {
	return ErrSubmitterNotWired
}

// BootstrapRequest carries the inputs the adapter hands to the
// ThreadBootstrapper when a cron job fires for the first time
// (job.thread_id is empty). The bootstrapper must return a Result
// whose ThreadID is non-empty; AgentID may be empty when the thread
// layer chose to share an existing agent. Provider-specific config
// (codexHome / codexInstanceKey / codexModelProvider / …) rides in
// Config verbatim; the bootstrapper decides how to translate that
// into a thread.StartRequest.
type BootstrapRequest struct {
	JobID    string
	Provider string
	Model    string
	CWD      string
	Name     string
	Config   json.RawMessage
}

// BootstrapResult is returned by ThreadBootstrapper. ThreadID is
// persisted onto cron_jobs by the scheduler via SetActiveTurn once
// StartTurn succeeds, so a second trigger reuses the same thread.
type BootstrapResult struct {
	ThreadID string
	AgentID  string
}

// ThreadBootstrapper creates (or resolves) the agent + thread pair a
// cron job needs on its first trigger. The default production wiring
// plugs in NoopThreadBootstrapper so the scheduler keeps failing
// fast with ErrJobNotBootstrapped until a deployment opts in by
// providing a real implementation (typically thread.Service backed).
type ThreadBootstrapper interface {
	BootstrapThread(ctx context.Context, req BootstrapRequest) (BootstrapResult, error)
}

// ErrBootstrapperNotWired indicates the deployment has no bootstrapper
// bound; the TurnServiceAdapter keeps the v1 behavior (surface
// ErrJobNotBootstrapped to the scheduler) rather than silently
// looping on a dead cron job.
var ErrBootstrapperNotWired = errors.New("cron: thread bootstrapper is not wired")

// NoopThreadBootstrapper is the default value installed when nobody
// supplies a real ThreadBootstrapper. Its BootstrapThread always
// errors with ErrBootstrapperNotWired so the adapter can distinguish
// "nobody opted in" from "the bootstrap tried and failed".
type NoopThreadBootstrapper struct{}

func (NoopThreadBootstrapper) BootstrapThread(context.Context, BootstrapRequest) (BootstrapResult, error) {
	return BootstrapResult{}, ErrBootstrapperNotWired
}

// SchedulerConfig carries tunable time / capacity parameters. Zero
// fields fall back to the Default* constants.
type SchedulerConfig struct {
	ClaimedBy      string
	LeaseTTL       time.Duration
	LeaseHeartbeat time.Duration
	TickInterval   time.Duration
	MaxClaim       int32
	Backoff        BackoffConfig
}

func (c SchedulerConfig) withDefaults() SchedulerConfig {
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
		c.Backoff.Base = DefaultBackoff.Base
	}
	if c.Backoff.Cap <= 0 {
		c.Backoff.Cap = DefaultBackoff.Cap
	}
	return c
}

// Scheduler owns the claim + submit + observe + mark state machine. It
// is driven by the tick actor (ticks) and the lease actor (heartbeats);
// tests can drive it directly via RunTick / RenewLeases.
type Scheduler struct {
	logger    *slog.Logger
	store     cronstore.Store
	submitter TurnSubmitter
	cfg       SchedulerConfig

	// clock + uuid are overridable for deterministic tests.
	now   func() time.Time
	newID func() string
}

// NewScheduler wires a Scheduler with its dependencies. A nil submitter
// defaults to NoopTurnSubmitter; a nil logger falls back to the package
// default.
func NewScheduler(logger *slog.Logger, store cronstore.Store, submitter TurnSubmitter, cfg SchedulerConfig) *Scheduler {
	if logger == nil {
		logger = pkglogger.Get()
	}
	if submitter == nil {
		submitter = NoopTurnSubmitter{}
	}
	return &Scheduler{
		logger:    logger,
		store:     store,
		submitter: submitter,
		cfg:       cfg.withDefaults(),
		now:       time.Now,
		newID:     func() string { return uuid.NewString() },
	}
}

// ClaimToken returns a new UUID usable as a claim_token. Exported so the
// tick actor can generate fresh tokens without reaching into unexported
// internals.
func (s *Scheduler) ClaimToken() string { return s.newID() }

// RunTick claims due jobs and drives each through the three-phase state
// machine. It is safe to call from multiple schedulers pointing at the
// same DB because ClaimDueJobs uses FOR UPDATE SKIP LOCKED.
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

func (s *Scheduler) claimOneDueJob(ctx context.Context) ([]cronstore.Job, error) {
	now := s.now().UTC()
	return s.store.ClaimDueJobs(ctx, cronstore.ClaimDueJobsParams{
		Now:            now,
		ClaimedBy:      s.cfg.ClaimedBy,
		LeaseExpiresAt: now.Add(s.cfg.LeaseTTL),
		ClaimToken:     s.newID(),
		MaxClaim:       1,
	})
}

// driveJob runs the three-phase state machine for one claimed job. On
// any terminal branch the claim is released via MarkFinished or
// MarkFailed; MarkFinished also advances next_run_at from the parsed
// cron expression.
func (s *Scheduler) driveJob(ctx context.Context, job cronstore.Job) error {
	now := s.now().UTC()
	scheduledAt := scheduledAtForJob(job, now)
	run, dedupe, err := s.createPendingRun(ctx, job, scheduledAt, now)
	if err != nil {
		return err
	}
	if err := s.markRunSubmitting(ctx, run.ID); err != nil {
		return err
	}
	startResult, err := s.submitter.StartTurn(ctx, buildStartTurnRequest(job, run.ID, dedupe, scheduledAt))
	if err != nil {
		return s.finalizeFailure(ctx, job, run, scheduledAt, err)
	}
	if err := s.persistSubmittedTurn(ctx, job, run, startResult); err != nil {
		return err
	}
	if err := s.observeStartedTurn(ctx, job, run, startResult); err != nil {
		return err
	}
	return s.markFinished(ctx, job, run, startResult.TurnID, scheduledAt)
}

func scheduledAtForJob(job cronstore.Job, now time.Time) time.Time {
	if !job.NextRetryAt.IsZero() {
		return job.NextRetryAt
	}
	if !job.NextRunAt.IsZero() {
		return job.NextRunAt
	}
	return now
}

func (s *Scheduler) createPendingRun(ctx context.Context, job cronstore.Job, scheduledAt, now time.Time) (cronstore.Run, string, error) {
	idempotencyKey := s.newID()
	dedupe := DedupeKey(job.ID, scheduledAt, idempotencyKey)
	run, err := s.store.InsertRun(ctx, cronstore.InsertRunParams{
		ID:             s.newID(),
		JobID:          job.ID,
		ScheduledAt:    scheduledAt,
		IdempotencyKey: idempotencyKey,
		DedupeKey:      dedupe,
		Status:         cronstore.StatusPending,
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	return run, dedupe, err
}

func (s *Scheduler) markRunSubmitting(ctx context.Context, runID string) error {
	return s.store.CASRunStatus(ctx, cronstore.CASRunStatusParams{
		ID:             runID,
		ExpectedStatus: cronstore.StatusPending,
		NextStatus:     cronstore.StatusSubmitting,
		UpdatedAt:      s.now().UTC(),
	})
}

func buildStartTurnRequest(job cronstore.Job, runID, dedupe string, scheduledAt time.Time) StartTurnRequest {
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
		Skills:       decodeSkillList(job.Skills),
		Prompt:       job.Prompt,
		ScheduledAt:  scheduledAt,
		MaxAttempts:  job.MaxAttempts,
		FailureCount: job.FailureCount,
	}
}

func (s *Scheduler) persistSubmittedTurn(ctx context.Context, job cronstore.Job, run cronstore.Run, result StartTurnResult) error {
	updatedAt := s.now().UTC()
	if err := s.store.SetRunTurn(ctx, cronstore.SetRunTurnParams{
		ID:          run.ID,
		ThreadID:    result.ThreadID,
		AgentID:     result.AgentID,
		TurnID:      result.TurnID,
		SubmittedAt: updatedAt,
		UpdatedAt:   updatedAt,
	}); err != nil {
		return err
	}
	if err := s.store.CASRunStatus(ctx, cronstore.CASRunStatusParams{
		ID:             run.ID,
		ExpectedStatus: cronstore.StatusSubmitting,
		NextStatus:     cronstore.StatusSubmitted,
		UpdatedAt:      updatedAt,
	}); err != nil {
		return err
	}
	return s.store.SetActiveTurn(ctx, cronstore.SetActiveTurnParams{
		ID:           job.ID,
		ClaimToken:   job.ClaimToken,
		ActiveTurnID: result.TurnID,
		ThreadID:     result.ThreadID,
		AgentID:      result.AgentID,
		Now:          updatedAt,
	})
}

func (s *Scheduler) observeStartedTurn(ctx context.Context, job cronstore.Job, run cronstore.Run, result StartTurnResult) error {
	if err := s.submitter.Observe(ctx, result.TurnID); err != nil {
		return s.finalizeObserveLost(ctx, job, run, result, err)
	}
	return s.store.CASRunStatus(ctx, cronstore.CASRunStatusParams{
		ID:             run.ID,
		ExpectedStatus: cronstore.StatusSubmitted,
		NextStatus:     cronstore.StatusRunning,
		UpdatedAt:      s.now().UTC(),
	})
}

func (s *Scheduler) finalizeFailure(ctx context.Context, job cronstore.Job, run cronstore.Run, scheduledAt time.Time, startErr error) error {
	now := s.now().UTC()
	nextRetry := s.nextRetry(job, scheduledAt, now)
	// Record the failed run transition. A CAS error here is not fatal
	// (the subsequent MarkFailed owns the job-level state transition)
	// but we log it so DB hiccups during finalize are observable.
	if err := s.store.CASRunStatus(ctx, cronstore.CASRunStatusParams{
		ID:             run.ID,
		ExpectedStatus: cronstore.StatusSubmitting,
		NextStatus:     cronstore.StatusFailed,
		Error:          startErr.Error(),
		UpdatedAt:      now,
	}); err != nil {
		s.logger.Warn("cron: CAS submitting->failed failed",
			slog.String("job_id", job.ID),
			slog.String("run_id", run.ID),
			slog.String("error", err.Error()),
		)
	}
	return s.store.MarkFailed(ctx, cronstore.MarkFailedParams{
		ID:          job.ID,
		ClaimToken:  job.ClaimToken,
		LastRunAt:   scheduledAt,
		LastTurnID:  "",
		LastStatus:  cronstore.StatusFailed,
		LastErrorAt: now,
		LastError:   startErr.Error(),
		NextRetryAt: nextRetry,
		Now:         now,
	})
}

func (s *Scheduler) finalizeObserveLost(ctx context.Context, job cronstore.Job, run cronstore.Run, result StartTurnResult, observeErr error) error {
	now := s.now().UTC()
	if err := s.store.CASRunStatus(ctx, cronstore.CASRunStatusParams{
		ID:             run.ID,
		ExpectedStatus: cronstore.StatusSubmitted,
		NextStatus:     cronstore.StatusObserveLost,
		Error:          observeErr.Error(),
		UpdatedAt:      now,
	}); err != nil {
		s.logger.Warn("cron: CAS submitted->observe_lost failed",
			slog.String("job_id", job.ID),
			slog.String("run_id", run.ID),
			slog.String("turn_id", result.TurnID),
			slog.String("error", err.Error()),
		)
	}
	return s.store.MarkFailed(ctx, cronstore.MarkFailedParams{
		ID:          job.ID,
		ClaimToken:  job.ClaimToken,
		LastRunAt:   now,
		LastTurnID:  result.TurnID,
		LastStatus:  cronstore.StatusObserveLost,
		LastErrorAt: now,
		LastError:   observeErr.Error(),
		NextRetryAt: time.Time{}, // observe_lost does not retry automatically
		Now:         now,
	})
}

func (s *Scheduler) markFinished(ctx context.Context, job cronstore.Job, run cronstore.Run, turnID string, scheduledAt time.Time) error {
	now := s.now().UTC()
	if err := s.store.CASRunStatus(ctx, cronstore.CASRunStatusParams{
		ID:             run.ID,
		ExpectedStatus: cronstore.StatusRunning,
		NextStatus:     cronstore.StatusFinished,
		UpdatedAt:      now,
	}); err != nil {
		s.logger.Warn("cron: CAS running->finished failed",
			slog.String("job_id", job.ID),
			slog.String("run_id", run.ID),
			slog.String("turn_id", turnID),
			slog.String("error", err.Error()),
		)
	}
	nextRunAt, err := ComputeNextRunAt(job.ScheduleExpr, job.Timezone, now)
	if err != nil || nextRunAt.IsZero() {
		// Preserve the current NextRunAt so a parse regression doesn't
		// wedge the job into "never schedules again".
		nextRunAt = job.NextRunAt
	}
	return s.store.MarkFinished(ctx, cronstore.MarkFinishedParams{
		ID:         job.ID,
		ClaimToken: job.ClaimToken,
		LastRunAt:  scheduledAt,
		LastTurnID: turnID,
		NextRunAt:  nextRunAt,
		Now:        now,
	})
}

// nextRetry returns the timestamp of the next retry attempt, respecting
// the P1b plan's "retry 必须被下一次 schedule 上界截断" rule. Returns
// time.Time{} when retries are exhausted or the next retry would cross
// into the next schedule.
func (s *Scheduler) nextRetry(job cronstore.Job, scheduledAt, now time.Time) time.Time {
	if job.MaxAttempts <= 0 || job.FailureCount+1 >= job.MaxAttempts {
		return time.Time{}
	}
	nextRunAt, err := ComputeNextRunAt(job.ScheduleExpr, job.Timezone, now)
	if err != nil {
		nextRunAt = job.NextRunAt
	}
	return NextRetryAt(s.cfg.Backoff, now, nextRunAt, job.FailureCount+1)
}

// RenewLeases is the entry point for the lease actor. It pulls the jobs
// currently claimed by this scheduler and bumps their lease expiration
// by LeaseTTL. Any renew failure (token mismatch, DB hiccup) is logged
// and otherwise ignored — a dropped lease simply means another
// scheduler gets to claim the job next tick, which is the intended
// recovery path.
func (s *Scheduler) RenewLeases(ctx context.Context) error {
	jobs, err := s.store.ListJobs(ctx)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	for _, job := range jobs {
		if job.ClaimedBy != s.cfg.ClaimedBy || job.ClaimToken == "" {
			continue
		}
		err := s.store.RenewLease(ctx, cronstore.LeaseParams{
			ID:             job.ID,
			ClaimToken:     job.ClaimToken,
			LeaseExpiresAt: now.Add(s.cfg.LeaseTTL),
			Now:            now,
		})
		if err != nil {
			s.logger.Debug("cron: renew lease failed",
				slog.String("job_id", job.ID),
				slog.String("error", err.Error()),
			)
		}
	}
	return nil
}

// RecoverDanglingRuns re-attaches observation for unresolved runs after a
// process restart. It never calls StartTurn; submitting-window recovery
// only probes the provider dedupe index and then observes an existing turn.
func (s *Scheduler) RecoverDanglingRuns(ctx context.Context) error {
	runs, err := s.store.ListUnresolvedRuns(ctx)
	if err != nil {
		return err
	}
	var joined error
	for _, run := range runs {
		if err := s.recoverDanglingRun(ctx, run); err != nil {
			joined = errors.Join(joined, err)
			s.logger.Warn("cron: recover dangling run failed",
				slog.String("run_id", run.ID),
				slog.String("status", run.Status),
				slog.String("error", err.Error()),
			)
		}
	}
	return joined
}

func (s *Scheduler) recoverDanglingRun(ctx context.Context, run cronstore.Run) error {
	job, err := s.store.GetJobByID(ctx, run.JobID)
	if err != nil {
		return err
	}
	switch run.Status {
	case cronstore.StatusSubmitting:
		return s.recoverSubmittingRun(ctx, job, run)
	case cronstore.StatusSubmitted:
		return s.recoverSubmittedRun(ctx, job, run)
	case cronstore.StatusRunning:
		return s.recoverRunningRun(ctx, job, run)
	default:
		return nil
	}
}

func (s *Scheduler) recoverSubmittingRun(ctx context.Context, job cronstore.Job, run cronstore.Run) error {
	if run.TurnID != "" {
		return s.observeRecoveredSubmittedRun(ctx, job, run, run.TurnID)
	}
	observed, err := s.submitter.LookupByDedupeKey(ctx, run.DedupeKey)
	if err != nil {
		return err
	}
	if !observed.Found || observed.TurnID == "" {
		return s.finalizeRecoveredFailure(ctx, job, run, errors.New("cron: provider dedupe lookup missed"))
	}
	if err := s.store.SetRunTurn(ctx, cronstore.SetRunTurnParams{
		ID: run.ID, ThreadID: run.ThreadID, AgentID: run.AgentID,
		TurnID: observed.TurnID, SubmittedAt: s.now().UTC(), UpdatedAt: s.now().UTC(),
	}); err != nil {
		return err
	}
	return s.observeRecoveredSubmittedRun(ctx, job, run, observed.TurnID)
}

func (s *Scheduler) recoverSubmittedRun(ctx context.Context, job cronstore.Job, run cronstore.Run) error {
	if run.TurnID == "" {
		return s.finalizeRecoveredFailure(ctx, job, run, errors.New("cron: submitted run missing turn_id"))
	}
	return s.observeRecoveredSubmittedRun(ctx, job, run, run.TurnID)
}

func (s *Scheduler) recoverRunningRun(ctx context.Context, job cronstore.Job, run cronstore.Run) error {
	if run.TurnID == "" {
		return s.finalizeRecoveredFailure(ctx, job, run, errors.New("cron: running run missing turn_id"))
	}
	if !job.LeaseExpiresAt.IsZero() && job.LeaseExpiresAt.Before(s.now().UTC()) {
		return s.finalizeRecoveredObserveLost(ctx, job, run, run.TurnID, errors.New("cron: running lease expired"))
	}
	if err := s.submitter.Observe(ctx, run.TurnID); err != nil {
		return s.finalizeRecoveredObserveLost(ctx, job, run, run.TurnID, err)
	}
	return nil
}

func (s *Scheduler) observeRecoveredSubmittedRun(ctx context.Context, job cronstore.Job, run cronstore.Run, turnID string) error {
	if err := s.submitter.Observe(ctx, turnID); err != nil {
		return s.finalizeRecoveredObserveLost(ctx, job, run, turnID, err)
	}
	if run.Status == cronstore.StatusSubmitting {
		if err := s.store.CASRunStatus(ctx, cronstore.CASRunStatusParams{ID: run.ID, ExpectedStatus: cronstore.StatusSubmitting, NextStatus: cronstore.StatusSubmitted, UpdatedAt: s.now().UTC()}); err != nil {
			return err
		}
	}
	return s.store.CASRunStatus(ctx, cronstore.CASRunStatusParams{ID: run.ID, ExpectedStatus: cronstore.StatusSubmitted, NextStatus: cronstore.StatusRunning, UpdatedAt: s.now().UTC()})
}

func (s *Scheduler) finalizeRecoveredFailure(ctx context.Context, job cronstore.Job, run cronstore.Run, err error) error {
	now := s.now().UTC()
	_ = s.store.CASRunStatus(ctx, cronstore.CASRunStatusParams{ID: run.ID, ExpectedStatus: run.Status, NextStatus: cronstore.StatusFailed, Error: err.Error(), UpdatedAt: now})
	return s.store.MarkFailed(ctx, cronstore.MarkFailedParams{ID: job.ID, ClaimToken: job.ClaimToken, LastRunAt: run.ScheduledAt, LastStatus: cronstore.StatusFailed, LastErrorAt: now, LastError: err.Error(), Now: now})
}

func (s *Scheduler) finalizeRecoveredObserveLost(ctx context.Context, job cronstore.Job, run cronstore.Run, turnID string, err error) error {
	now := s.now().UTC()
	_ = s.store.CASRunStatus(ctx, cronstore.CASRunStatusParams{ID: run.ID, ExpectedStatus: run.Status, NextStatus: cronstore.StatusObserveLost, Error: err.Error(), UpdatedAt: now})
	return s.store.MarkFailed(ctx, cronstore.MarkFailedParams{ID: job.ID, ClaimToken: job.ClaimToken, LastRunAt: run.ScheduledAt, LastTurnID: turnID, LastStatus: cronstore.StatusObserveLost, LastErrorAt: now, LastError: err.Error(), Now: now})
}

// ExtendClaimForTurnProgress extends the active job lease when the turn bus
// reports progress, keeping long-running turns from losing their claim.
func (s *Scheduler) ExtendClaimForTurnProgress(ctx context.Context, turnID string) error {
	if strings.TrimSpace(turnID) == "" {
		return nil
	}
	jobs, err := s.store.ListJobs(ctx)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	for _, job := range jobs {
		if job.ClaimedBy != s.cfg.ClaimedBy || job.ActiveTurnID != turnID || job.ClaimToken == "" {
			continue
		}
		return s.store.ExtendClaim(ctx, cronstore.LeaseParams{ID: job.ID, ClaimToken: job.ClaimToken, LeaseExpiresAt: now.Add(s.cfg.LeaseTTL), Now: now})
	}
	return nil
}

func decodeSkillList(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}
