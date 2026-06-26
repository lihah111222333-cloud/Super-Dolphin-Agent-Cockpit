package cron

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"log/slog"

	cronstore "github.com/anthropic-ai/super-agent-v3/internal/store/cron"
)

// CompleteTurn 将 turn 终态事件写回当前追踪该 turnID 的 cron run。
// RunTick 只把已提交 turn 推到 running；终态由 BusModule 订阅事件后进入这里。
// 找不到 running run 会返回错误，方便暴露恢复流程或事件顺序问题。
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

// markTerminalFailed 将 running run 标记为 failed 并计算下一次重试时间。
func (s *Scheduler) markTerminalFailed(ctx context.Context, job cronstore.Job, run cronstore.Run, terminalErr string) error {
	now := s.now().UTC()
	nextRetryAt, err := s.nextRetry(job, now)
	if err != nil {
		return err
	}
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
		NextRetryAt: nextRetryAt,
		Now:         now,
	})
}

// markFinished 将 running run 标记为 finished 并推算 job 的下一次运行时间。
func (s *Scheduler) markFinished(ctx context.Context, job cronstore.Job, run cronstore.Run, turnID string, scheduledAt time.Time) error {
	now := s.now().UTC()
	nextRunAt, err := ComputeNextRunAt(job.ScheduleExpr, job.Timezone, now)
	if err != nil {
		return err
	}
	if nextRunAt.IsZero() {
		return errors.New("cron: computed next_run_at is zero")
	}
	s.casLogPublish(ctx, cronstore.CASRunStatusParams{
		ID: run.ID, ExpectedStatus: cronstore.StatusRunning, NextStatus: cronstore.StatusFinished, UpdatedAt: now,
	}, "running->finished", job.ID, run.ID, cronstore.StatusFinished, turnID, "", scheduledAt)
	return s.store.MarkFinished(ctx, cronstore.MarkFinishedParams{
		ID:         job.ID,
		ClaimToken: job.ClaimToken,
		LastRunAt:  scheduledAt,
		LastTurnID: turnID,
		NextRunAt:  nextRunAt,
		Now:        now,
	})
}

// nextRetry 计算下一次重试时间。
// 重试次数耗尽或重试会跨过下一次正常 schedule 时返回零值，交给正常 tick 接管。
func (s *Scheduler) nextRetry(job cronstore.Job, now time.Time) (time.Time, error) {
	if job.MaxAttempts <= 0 || job.FailureCount+1 >= job.MaxAttempts {
		return time.Time{}, nil
	}
	nextRunAt, err := ComputeNextRunAt(job.ScheduleExpr, job.Timezone, now)
	if err != nil {
		return time.Time{}, err
	}
	return NextRetryAt(s.cfg.Backoff, now, nextRunAt, job.FailureCount+1), nil
}

// RenewLeases 是 lease actor 的续租入口。
// 它只延长当前实例 claim 的 job；单个续租失败只记录日志，后续 tick/recovery 会接手过期 claim。
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
				slog.String("job_id", job.ID),
				slog.String("error", err.Error()),
			)
		}
	}
	return nil
}

// RecoverDanglingRuns 在进程重启后重新接管未解决的 cron run。
// 恢复流程绝不调用 StartTurn；submitting 窗口只查 provider 去重索引再观察既有 turn。
// 只处理 submitting/submitted/running，避免重启后对同一 dedupeKey 重复提交。
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

// recoverDanglingRun 按 run 状态分发到对应的恢复函数，未知状态直接跳过。
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

// recoverSubmittedRun 恢复 submitted 状态的 run，turn_id 缺失时直接标记为失败。
func (s *Scheduler) recoverSubmittedRun(ctx context.Context, job cronstore.Job, run cronstore.Run) error {
	if run.TurnID == "" {
		return s.finalizeRecoveredFailure(ctx, job, run, errors.New("cron: submitted run missing turn_id"))
	}
	return s.observeRecoveredSubmittedRun(ctx, job, run, run.TurnID)
}

// recoverRunningRun 恢复 running 状态的 run，租约过期或 Observe 失败时转为 observe_lost。
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

// observeRecoveredSubmittedRun 恢复时 Observe 已提交的 turn 并推进状态到 running。
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

// finalizeRecoveredFailure 将恢复时确认失败的 run 标记为 failed 并更新 job 状态。
func (s *Scheduler) finalizeRecoveredFailure(ctx context.Context, job cronstore.Job, run cronstore.Run, err error) error {
	now := s.now().UTC()
	if casErr := s.store.CASRunStatus(ctx, cronstore.CASRunStatusParams{ID: run.ID, ExpectedStatus: run.Status, NextStatus: cronstore.StatusFailed, Error: err.Error(), UpdatedAt: now}); casErr != nil {
		s.logger.Warn("cron: recovered CAS submitting->failed failed",
			slog.String("run_id", run.ID),
			slog.String("error", casErr.Error()),
		)
	}
	return s.store.MarkFailed(ctx, cronstore.MarkFailedParams{ID: job.ID, ClaimToken: job.ClaimToken, LastRunAt: run.ScheduledAt, LastStatus: cronstore.StatusFailed, LastErrorAt: now, LastError: err.Error(), Now: now})
}

// finalizeRecoveredObserveLost 将恢复时无法追踪的 run 标记为 observe_lost，不触发重试。
func (s *Scheduler) finalizeRecoveredObserveLost(ctx context.Context, job cronstore.Job, run cronstore.Run, turnID string, err error) error {
	now := s.now().UTC()
	// 恢复期的 observe_lost 也不自动 retry；旧 turn 状态未知时，新 turn 会造成重复。
	if casErr := s.store.CASRunStatus(ctx, cronstore.CASRunStatusParams{ID: run.ID, ExpectedStatus: run.Status, NextStatus: cronstore.StatusObserveLost, Error: err.Error(), UpdatedAt: now}); casErr != nil {
		s.logger.Warn("cron: recovered CAS submitted->observe_lost failed",
			slog.String("run_id", run.ID),
			slog.String("error", casErr.Error()),
		)
	}
	return s.store.MarkFailed(ctx, cronstore.MarkFailedParams{ID: job.ID, ClaimToken: job.ClaimToken, LastRunAt: run.ScheduledAt, LastTurnID: turnID, LastStatus: cronstore.StatusObserveLost, LastErrorAt: now, LastError: err.Error(), Now: now})
}

// ExtendClaimForTurnProgress 在 turn 进度事件到达时延长 active job 的 claim。
// 这里只续租当前 scheduler 持有且 active_turn_id 匹配的 job；找不到匹配项时交给后续 tick/recovery。
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

// decodeSkillList 从 run 快照中的 JSONB 字节解码技能名称列表。
// 它只服务恢复提交请求的字段投影；解析失败返回 nil，后续 StartTurn/Observe 边界仍决定是否失败。
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
