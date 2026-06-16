package cron

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	cronstore "github.com/anthropic-ai/super-agent-v3/internal/store/cron"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// CompleteTurn applies a terminal turn event to the cron run that is currently
// tracking turnID. RunTick only moves a submitted turn into running; this
// method is invoked by the BusModule terminal-event subscriber once the turn
// actually completes or fails. Missing/non-running rows are surfaced so crash
// recovery races cannot silently drop terminal state.
//
// 这是 turn 终态写回 cron run 的入口。找不到 running run 要报错，
// 方便发现 recovery 或事件顺序问题。
func (s *Scheduler) CompleteTurn(ctx context.Context, turnID string, success bool, terminalErr string) error {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return nil
	}
	run, err := s.store.GetRunningRunByTurnID(ctx, turnID)
	if err != nil {
		if errors.Is(err, cronstore.ErrJobRunNotFound) {
			return fmt.Errorf("cron: running run for turn %q not found: %w", turnID, err)
		}
		return err
	}
	job, err := s.store.GetJobByID(ctx, run.JobID)
	if err != nil {
		return err
	}
	if success {
		return s.markFinished(ctx, job, run, turnID, run.ScheduledAt)
	}
	if strings.TrimSpace(terminalErr) == "" {
		terminalErr = "cron: turn terminal failure"
	}
	return s.markTerminalFailed(ctx, job, run, terminalErr)
}

func (s *Scheduler) markTerminalFailed(ctx context.Context, job cronstore.Job, run cronstore.Run, terminalErr string) error {
	now := s.now().UTC()
	s.casLogPublish(ctx, cronstore.CASRunStatusParams{
		ID: run.ID, ExpectedStatus: cronstore.StatusRunning, NextStatus: cronstore.StatusFailed,
		Error: terminalErr, UpdatedAt: now,
	}, "running->failed", job.ID, run.ID, cronstore.StatusFailed, run.TurnID, terminalErr, run.ScheduledAt)
	return s.store.MarkFailed(ctx, cronstore.MarkFailedParams{
		ID:          job.ID,
		ClaimToken:  job.ClaimToken,
		LastRunAt:   run.ScheduledAt,
		LastTurnID:  run.TurnID,
		LastStatus:  cronstore.StatusFailed,
		LastErrorAt: now,
		LastError:   terminalErr,
		NextRetryAt: s.nextRetry(job, now),
		Now:         now,
	})
}

func (s *Scheduler) markFinished(ctx context.Context, job cronstore.Job, run cronstore.Run, turnID string, scheduledAt time.Time) error {
	now := s.now().UTC()
	s.casLogPublish(ctx, cronstore.CASRunStatusParams{
		ID: run.ID, ExpectedStatus: cronstore.StatusRunning, NextStatus: cronstore.StatusFinished, UpdatedAt: now,
	}, "running->finished", job.ID, run.ID, cronstore.StatusFinished, turnID, "", scheduledAt)
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
func (s *Scheduler) nextRetry(job cronstore.Job, now time.Time) time.Time {
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
// RenewLeases 续租仍由当前实例持有的 cron run。
func (s *Scheduler) RenewLeases(ctx context.Context) error {
	jobs, err := s.store.ListJobsClaimedBy(ctx, s.cfg.ClaimedBy)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	for _, job := range jobs {
		err := s.store.RenewLease(ctx, cronstore.LeaseParams{
			ID:             job.ID,
			ClaimToken:     job.ClaimToken,
			LeaseExpiresAt: now.Add(s.cfg.LeaseTTL),
			Now:            now,
		})
		if err != nil {
			s.logger.Debug("cron: renew lease failed",
				pkglogger.String("job_id", job.ID),
				pkglogger.String("error", err.Error()),
			)
		}
	}
	return nil
}

// RecoverDanglingRuns re-attaches observation for unresolved runs after a
// process restart. It never calls StartTurn; submitting-window recovery
// only probes the provider dedupe index and then observes an existing turn.
//
// 恢复只接管 submitting/submitted/running。没有 turn_id 的 submitting
// 只能先 Lookup，不能重新提交。
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
				pkglogger.String("run_id", run.ID),
				pkglogger.String("status", run.Status),
				pkglogger.String("error", err.Error()),
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

// recoverSubmittingRun 恢复停在提交中的 cron run。
func (s *Scheduler) recoverSubmittingRun(ctx context.Context, job cronstore.Job, run cronstore.Run) error {
	if run.TurnID != "" {
		return s.observeRecoveredSubmittedRun(ctx, job, run, run.TurnID)
	}
	// 这是最容易重复提交的窗口：StartTurn 可能成功但还没落库。
	// 先查 dedupe，查不到才按失败处理。
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
		// lease 过期后无法确认旧 observer 是否还活着，按 observe_lost 处理，避免无人接手。
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
	// 恢复也用 CAS 保持状态往前走。若终态事件抢先写入，这里会失败并暴露出来。
	if run.Status == cronstore.StatusSubmitting {
		if err := s.store.CASRunStatus(ctx, cronstore.CASRunStatusParams{ID: run.ID, ExpectedStatus: cronstore.StatusSubmitting, NextStatus: cronstore.StatusSubmitted, UpdatedAt: s.now().UTC()}); err != nil {
			return err
		}
	}
	return s.store.CASRunStatus(ctx, cronstore.CASRunStatusParams{ID: run.ID, ExpectedStatus: cronstore.StatusSubmitted, NextStatus: cronstore.StatusRunning, UpdatedAt: s.now().UTC()})
}

func (s *Scheduler) finalizeRecoveredFailure(ctx context.Context, job cronstore.Job, run cronstore.Run, err error) error {
	now := s.now().UTC()
	if casErr := s.store.CASRunStatus(ctx, cronstore.CASRunStatusParams{ID: run.ID, ExpectedStatus: run.Status, NextStatus: cronstore.StatusFailed, Error: err.Error(), UpdatedAt: now}); casErr != nil {
		s.logger.Warn("cron: recovered CAS submitting->failed failed",
			pkglogger.String("run_id", run.ID),
			pkglogger.String("error", casErr.Error()),
		)
	}
	return s.store.MarkFailed(ctx, cronstore.MarkFailedParams{ID: job.ID, ClaimToken: job.ClaimToken, LastRunAt: run.ScheduledAt, LastStatus: cronstore.StatusFailed, LastErrorAt: now, LastError: err.Error(), Now: now})
}

func (s *Scheduler) finalizeRecoveredObserveLost(ctx context.Context, job cronstore.Job, run cronstore.Run, turnID string, err error) error {
	now := s.now().UTC()
	// 恢复期的 observe_lost 也不自动 retry；旧 turn 状态未知时，新 turn 会造成重复。
	if casErr := s.store.CASRunStatus(ctx, cronstore.CASRunStatusParams{ID: run.ID, ExpectedStatus: run.Status, NextStatus: cronstore.StatusObserveLost, Error: err.Error(), UpdatedAt: now}); casErr != nil {
		s.logger.Warn("cron: recovered CAS submitted->observe_lost failed",
			pkglogger.String("run_id", run.ID),
			pkglogger.String("error", casErr.Error()),
		)
	}
	return s.store.MarkFailed(ctx, cronstore.MarkFailedParams{ID: job.ID, ClaimToken: job.ClaimToken, LastRunAt: run.ScheduledAt, LastTurnID: turnID, LastStatus: cronstore.StatusObserveLost, LastErrorAt: now, LastError: err.Error(), Now: now})
}

// ExtendClaimForTurnProgress extends the active job lease when the turn bus
// reports progress, keeping long-running turns from losing their claim.
//
// 这里只延长当前 scheduler 持有且 active_turn_id 匹配的 job。
// 抢不到就交给后续 tick/recovery。
func (s *Scheduler) ExtendClaimForTurnProgress(ctx context.Context, turnID string) error {
	if strings.TrimSpace(turnID) == "" {
		return nil
	}
	jobs, err := s.store.ListJobsClaimedBy(ctx, s.cfg.ClaimedBy)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	for _, job := range jobs {
		if job.ActiveTurnID != turnID {
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
