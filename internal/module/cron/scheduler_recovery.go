package cron

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/pkg/cronmetrics"
)

const recoverDanglingRunsBatchLimit int32 = 128

type terminalRunLookupStore interface {
	GetSubmittedOrRunningRunByTurnID(ctx context.Context, turnID string) (RunRecord, error)
}

type unresolvedRunsPageStore interface {
	ListUnresolvedRunsPage(ctx context.Context, limit int32, cursor string) ([]RunRecord, error)
}

// CompleteTurn 将 turn 终态事件写回当前追踪该 turnID 的 cron run。
// 终态事件可能早于 submitted->running 的观察 CAS 到达，因此这里按 turnID 查找
// submitted/running 未解决 run，并用当前状态做 CAS 收尾。
func (s *Scheduler) CompleteTurn(ctx context.Context, turnID string, success bool, terminalErr string) error {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return nil
	}
	run, err := s.terminalRunByTurnID(ctx, turnID)
	if err != nil {
		return err
	}
	job, err := s.store.GetJobByID(ctx, run.JobID)
	if err != nil {
		return err
	}
	if err := validateTerminalFence(job, run, turnID); err != nil {
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

// terminalRunByTurnID 定位可被终态事件收尾的 unresolved run。
// running 走专用查询，submitted 则回看未解决 run，避免终态事件抢在 Observe 前到达时丢失收尾。
func (s *Scheduler) terminalRunByTurnID(ctx context.Context, turnID string) (RunRecord, error) {
	lookupStore, ok := s.store.(terminalRunLookupStore)
	if !ok {
		return RunRecord{}, errors.New("cron: store does not implement submitted/running turn lookup")
	}
	run, err := lookupStore.GetSubmittedOrRunningRunByTurnID(ctx, turnID)
	if err == nil {
		return run, nil
	}
	if !errors.Is(err, ErrStoreJobRunNotFound) {
		return RunRecord{}, err
	}
	return RunRecord{}, fmt.Errorf("cron: submitted/running run for turn %q not found: %w", turnID, ErrStoreJobRunNotFound)
}

// validateTerminalFence 在 run 终态落库前确认 job 当前 claim 仍然指向同一个 turn。
// 旧 turn 的迟到事件必须停在这里，不能继续 CAS run 或释放新的 job claim。
func validateTerminalFence(job JobRecord, run RunRecord, turnID string) error {
	if run.JobID != job.ID || run.TurnID != turnID || job.ActiveTurnID != turnID || strings.TrimSpace(job.ClaimToken) == "" {
		return fmt.Errorf("%w: job_id=%s run_id=%s turn_id=%s active_turn_id=%s",
			ErrStoreClaimTokenMismatch, job.ID, run.ID, turnID, job.ActiveTurnID)
	}
	return nil
}

// markTerminalFailed 将 submitted/running run 标记为 failed 并计算下一次重试时间。
func (s *Scheduler) markTerminalFailed(ctx context.Context, job JobRecord, run RunRecord, terminalErr string) error {
	now := s.now().UTC()
	nextRetryAt, nextRunAt, err := s.nextRetryAndRun(job, now)
	if err != nil {
		return err
	}
	if err := s.store.CASRunStatus(ctx, CASRunStatusParams{
		ID: run.ID, ExpectedStatus: run.Status, NextStatus: statusFailed,
		Error: terminalErr, UpdatedAt: now,
	}); err != nil {
		return err
	}
	s.publishRunState(job.ID, run.ID, statusFailed, run.TurnID, terminalErr, run.ScheduledAt)
	return s.store.MarkFailed(ctx, MarkFailedParams{
		ID:                   job.ID,
		ClaimToken:           job.ClaimToken,
		RunID:                run.ID,
		ExpectedActiveTurnID: run.TurnID,
		LastRunAt:            run.ScheduledAt,
		LastTurnID:           run.TurnID,
		LastStatus:           statusFailed,
		LastErrorAt:          now,
		LastError:            terminalErr,
		NextRunAt:            nextRunAt,
		NextRetryAt:          nextRetryAt,
		Now:                  now,
	})
}

// markFinished 将 submitted/running run 标记为 finished 并推算 job 的下一次运行时间。
func (s *Scheduler) markFinished(ctx context.Context, job JobRecord, run RunRecord, turnID string, scheduledAt time.Time) error {
	now := s.now().UTC()
	nextRunAt, err := ComputeNextRunAt(job.ScheduleExpr, job.Timezone, now)
	if err != nil {
		return err
	}
	if nextRunAt.IsZero() {
		return errors.New("cron: computed next_run_at is zero")
	}
	if err := s.store.CASRunStatus(ctx, CASRunStatusParams{
		ID: run.ID, ExpectedStatus: run.Status, NextStatus: statusFinished, UpdatedAt: now,
	}); err != nil {
		return err
	}
	s.publishRunState(job.ID, run.ID, statusFinished, turnID, "", scheduledAt)
	return s.store.MarkFinished(ctx, MarkFinishedParams{
		ID:                   job.ID,
		ClaimToken:           job.ClaimToken,
		RunID:                run.ID,
		ExpectedActiveTurnID: turnID,
		LastRunAt:            scheduledAt,
		LastTurnID:           turnID,
		NextRunAt:            nextRunAt,
		Now:                  now,
	})
}

// nextRetryAndRun 计算失败后的 retry 时间和下一次正常 cron 时间。
// retry 耗尽或会越过下一次正常 schedule 时返回零值 retry，但仍返回新的 next_run_at，
// 避免 MarkFailed 保留旧 due 时间导致同一失败 run 被下一轮 tick 立刻重复领取。
func (s *Scheduler) nextRetryAndRun(job JobRecord, now time.Time) (time.Time, time.Time, error) {
	nextRunAt, err := ComputeNextRunAt(job.ScheduleExpr, job.Timezone, now)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if nextRunAt.IsZero() {
		return time.Time{}, time.Time{}, errors.New("cron: computed next_run_at is zero")
	}
	if job.MaxAttempts <= 0 || job.FailureCount+1 >= job.MaxAttempts {
		return time.Time{}, nextRunAt, nil
	}
	return NextRetryAt(s.cfg.Backoff, now, nextRunAt, job.FailureCount+1), nextRunAt, nil
}

type leaseRenewFailure struct {
	job JobRecord
	err error
}

type leaseRenewalError struct {
	failures []leaseRenewFailure
	err      error
}

// Error 返回聚合后的续租失败文本。
func (e *leaseRenewalError) Error() string {
	if e == nil || e.err == nil {
		return "cron: lease renewal failed"
	}
	return e.err.Error()
}

// Unwrap 保留 errors.Is/As 对底层 store 错误的识别能力。
func (e *leaseRenewalError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

type leaseLostTurnCanceler interface {
	CancelLeaseLostTurn(context.Context, JobRecord) error
}

// cancelLeaseFailures 中断已无法安全续租的 active turn，并按 job 聚合失败。
func (s *Scheduler) cancelLeaseFailures(ctx context.Context, failures []leaseRenewFailure) error {
	canceler, ok := s.submitter.(leaseLostTurnCanceler)
	var joined error
	for _, failure := range failures {
		if strings.TrimSpace(failure.job.ActiveTurnID) == "" {
			continue
		}
		if !ok {
			joined = errors.Join(joined, fmt.Errorf(
				"cron: cancel lease-lost job %s: turn canceler is not wired", failure.job.ID,
			))
			continue
		}
		if err := canceler.CancelLeaseLostTurn(ctx, failure.job); err != nil {
			joined = errors.Join(joined, fmt.Errorf("cron: cancel lease-lost job %s: %w", failure.job.ID, err))
		}
	}
	return joined
}

// RenewLeases 是 lease actor 的续租入口。
// 它只延长当前实例 claim 的 job；失败按 job 聚合并交给 lease actor 在安全预算内重试。
func (s *Scheduler) RenewLeases(ctx context.Context) error {
	jobs, err := s.store.ListJobsClaimedBy(ctx, s.cfg.ClaimedBy)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	var failures []leaseRenewFailure
	var joined error
	for _, job := range jobs {
		err := s.store.RenewLease(ctx, LeaseParams{
			ID:             job.ID,
			ClaimToken:     job.ClaimToken,
			LeaseExpiresAt: now.Add(s.cfg.LeaseTTL),
			Now:            now,
		})
		if err != nil {
			wrapped := fmt.Errorf("cron: renew lease job %s: %w", job.ID, err)
			failures = append(failures, leaseRenewFailure{job: job, err: wrapped})
			joined = errors.Join(joined, wrapped)
		}
	}
	if joined != nil {
		return &leaseRenewalError{failures: failures, err: joined}
	}
	return nil
}

// RecoverDanglingRuns 在进程重启后重新接管未解决的 cron run。
// 恢复流程绝不调用 StartTurn；submitting 窗口只查 provider 去重索引再观察既有 turn。
// 只处理 submitting/submitted/running，避免重启后对同一 dedupeKey 重复提交。
func (s *Scheduler) RecoverDanglingRuns(ctx context.Context) error {
	pageStore, ok := s.store.(unresolvedRunsPageStore)
	if !ok {
		return errors.New("cron: store does not implement paged unresolved run recovery")
	}
	var joined error
	cursor := ""
	for {
		runs, err := pageStore.ListUnresolvedRunsPage(ctx, recoverDanglingRunsBatchLimit, cursor)
		if err != nil {
			return err
		}
		s.logger.Info("cron: recover dangling runs batch",
			slog.Int("limit", int(recoverDanglingRunsBatchLimit)),
			slog.Int("count", len(runs)),
			slog.String("cursor", cursor),
		)
		if len(runs) == 0 {
			break
		}
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
		cursor = strings.TrimSpace(runs[len(runs)-1].ID)
		if cursor == "" {
			return errors.New("cron: unresolved run page ended with empty cursor")
		}
	}
	return joined
}

// recoverDanglingRun 按 run 状态分发到对应的恢复函数，未知状态直接跳过。
func (s *Scheduler) recoverDanglingRun(ctx context.Context, run RunRecord) error {
	job, err := s.store.GetJobByID(ctx, run.JobID)
	if err != nil {
		return err
	}
	switch run.Status {
	case statusSubmitting:
		return s.recoverSubmittingRun(ctx, job, run)
	case statusSubmitted:
		return s.recoverSubmittedRun(ctx, job, run)
	case statusRunning:
		return s.recoverRunningRun(ctx, job, run)
	default:
		return nil
	}
}

// recoverSubmittingRun 恢复停在提交中的 cron run。
func (s *Scheduler) recoverSubmittingRun(ctx context.Context, job JobRecord, run RunRecord) error {
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
	if err := s.store.SetRunTurn(ctx, SetRunTurnParams{
		ID: run.ID, ThreadID: run.ThreadID, AgentID: run.AgentID,
		TurnID: observed.TurnID, SubmittedAt: s.now().UTC(), UpdatedAt: s.now().UTC(),
	}); err != nil {
		return err
	}
	return s.observeRecoveredSubmittedRun(ctx, job, run, observed.TurnID)
}

// recoverSubmittedRun 恢复 submitted 状态的 run，turn_id 缺失时直接标记为失败。
func (s *Scheduler) recoverSubmittedRun(ctx context.Context, job JobRecord, run RunRecord) error {
	if run.TurnID == "" {
		return s.finalizeRecoveredFailure(ctx, job, run, errors.New("cron: submitted run missing turn_id"))
	}
	return s.observeRecoveredSubmittedRun(ctx, job, run, run.TurnID)
}

// recoverRunningRun 恢复 running 状态的 run，租约过期或 Observe 失败时转为 observe_lost。
func (s *Scheduler) recoverRunningRun(ctx context.Context, job JobRecord, run RunRecord) error {
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
func (s *Scheduler) observeRecoveredSubmittedRun(ctx context.Context, job JobRecord, run RunRecord, turnID string) error {
	if err := s.submitter.Observe(ctx, turnID); err != nil {
		return s.finalizeRecoveredObserveLost(ctx, job, run, turnID, err)
	}
	// 恢复也用 CAS 保持状态往前走。若终态事件抢先写入，这里会失败并暴露出来。
	if run.Status == statusSubmitting {
		if err := s.store.CASRunStatus(ctx, CASRunStatusParams{ID: run.ID, ExpectedStatus: statusSubmitting, NextStatus: statusSubmitted, UpdatedAt: s.now().UTC()}); err != nil {
			return err
		}
	}
	return s.store.CASRunStatus(ctx, CASRunStatusParams{ID: run.ID, ExpectedStatus: statusSubmitted, NextStatus: statusRunning, UpdatedAt: s.now().UTC()})
}

// finalizeRecoveredFailure 将恢复时确认失败的 run 标记为 failed 并更新 job 状态。
func (s *Scheduler) finalizeRecoveredFailure(ctx context.Context, job JobRecord, run RunRecord, err error) error {
	now := s.now().UTC()
	nextRetryAt, nextRunAt, scheduleErr := s.nextRetryAndRun(job, now)
	if scheduleErr != nil {
		return scheduleErr
	}
	return s.finalizeRecoveredRun(ctx, job, run, statusFailed, s.store.FinalizeRecoveredRun(ctx, FinalizeRecoveredRunParams{
		ExpectedRunStatus: run.Status,
		MarkFailedParams: MarkFailedParams{
			ID: job.ID, ClaimToken: job.ClaimToken, RunID: run.ID, ExpectedActiveTurnID: run.TurnID,
			LastRunAt: run.ScheduledAt, LastTurnID: run.TurnID, LastStatus: statusFailed,
			LastErrorAt: now, LastError: err.Error(), NextRunAt: nextRunAt, NextRetryAt: nextRetryAt, Now: now,
		},
	}))
}

// finalizeRecoveredObserveLost 将恢复时无法追踪的 run 标记为 observe_lost，不触发重试。
func (s *Scheduler) finalizeRecoveredObserveLost(ctx context.Context, job JobRecord, run RunRecord, turnID string, err error) error {
	now := s.now().UTC()
	_, nextRunAt, scheduleErr := s.nextRetryAndRun(job, now)
	if scheduleErr != nil {
		return scheduleErr
	}
	// 恢复期的 observe_lost 也不自动 retry；旧 turn 状态未知时，新 turn 会造成重复。
	return s.finalizeRecoveredRun(ctx, job, run, statusObserveLost, s.store.FinalizeRecoveredRun(ctx, FinalizeRecoveredRunParams{
		ExpectedRunStatus: run.Status,
		MarkFailedParams: MarkFailedParams{
			ID: job.ID, ClaimToken: job.ClaimToken, RunID: run.ID, ExpectedActiveTurnID: turnID,
			LastRunAt: run.ScheduledAt, LastTurnID: turnID, LastStatus: statusObserveLost,
			LastErrorAt: now, LastError: err.Error(), NextRunAt: nextRunAt, Now: now,
		},
	}))
}

// finalizeRecoveredRun 复核事务冲突是否为同一终态的幂等完成，并保留所有非幂等错误。
func (s *Scheduler) finalizeRecoveredRun(ctx context.Context, job JobRecord, run RunRecord, terminalStatus string, finalizeErr error) error {
	if finalizeErr == nil {
		return nil
	}
	if !isRecoveryFinalizeConflict(finalizeErr) {
		s.recordRecoveryFinalizeError(job, run, terminalStatus, finalizeErr)
		return finalizeErr
	}
	cronmetrics.IncRecoveryFinalizeConflict()
	s.logger.Warn("cron: recovery finalization conflict",
		slog.String("metric", cronRecoveryFinalizeConflictMetric),
		slog.String("job_id", job.ID),
		slog.String("run_id", run.ID),
		slog.String("turn_id", run.TurnID),
		slog.String("expected_status", run.Status),
		slog.String("terminal_status", terminalStatus),
		slog.String("error", finalizeErr.Error()),
	)
	currentRun, currentJob, readErr := s.readRecoveryFinalizeState(ctx, run.ID, job.ID)
	if readErr != nil {
		err := errors.Join(finalizeErr, readErr)
		s.recordRecoveryFinalizeError(job, run, terminalStatus, err)
		return err
	}
	if recoveredFinalizationMatches(currentRun, currentJob, job.ID, run.TurnID, terminalStatus) {
		return nil
	}
	err := fmt.Errorf("cron: recovered finalization conflict job_id=%s run_id=%s expected_status=%s terminal_status=%s: %w", job.ID, run.ID, run.Status, terminalStatus, finalizeErr)
	s.recordRecoveryFinalizeError(job, run, terminalStatus, err)
	return err
}

func isRecoveryFinalizeConflict(err error) bool {
	return errors.Is(err, ErrStoreClaimTokenMismatch) || errors.Is(err, ErrStoreStatusTransitionRefused)
}

func (s *Scheduler) readRecoveryFinalizeState(ctx context.Context, runID, jobID string) (RunRecord, JobRecord, error) {
	currentRun, err := s.store.GetRunByID(ctx, runID)
	if err != nil {
		return RunRecord{}, JobRecord{}, err
	}
	currentJob, err := s.store.GetJobByID(ctx, jobID)
	return currentRun, currentJob, err
}

// recoveredFinalizationMatches 判断重读后的 run/job 是否已共同落到同一合法终态。
func recoveredFinalizationMatches(currentRun RunRecord, currentJob JobRecord, jobID, turnID, terminalStatus string) bool {
	return currentRun.JobID == jobID && currentRun.TurnID == turnID && currentRun.Status == terminalStatus &&
		currentJob.LastStatus == terminalStatus && currentJob.LastTurnID == turnID &&
		currentJob.ActiveTurnID == "" && currentJob.ClaimToken == ""
}

// recordRecoveryFinalizeError keeps recovery-finalization errors visible without
// weakening the caller-visible error. The required identity and expected status
// fields make a stale turn or lost claim diagnosable from one log event.
func (s *Scheduler) recordRecoveryFinalizeError(job JobRecord, run RunRecord, terminalStatus string, err error) {
	cronmetrics.IncRecoveryFinalizeError()
	s.logger.Error("cron: recovery finalization failed",
		slog.String("metric", cronRecoveryFinalizeErrorMetric),
		slog.String("job_id", job.ID),
		slog.String("run_id", run.ID),
		slog.String("turn_id", run.TurnID),
		slog.String("expected_status", run.Status),
		slog.String("terminal_status", terminalStatus),
		slog.String("error", err.Error()),
	)
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
		return s.store.ExtendClaim(ctx, LeaseParams{ID: job.ID, ClaimToken: job.ClaimToken, LeaseExpiresAt: now.Add(s.cfg.LeaseTTL), Now: now})
	}
	return nil
}

// decodeSkillList 从 run 快照中的 JSONB 字节解码技能名称列表。
// 解析失败必须阻断本次提交，避免把损坏快照当作空 skills 继续执行。
func decodeSkillList(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("cron: decode skills snapshot: %w", err)
	}
	return out, nil
}
