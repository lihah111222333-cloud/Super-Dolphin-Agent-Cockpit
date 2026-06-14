package cron

import "github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"

// fromCronJob projects a sqlc.CronJob row (shared across Create / Get /
// List / Claim queries because they all project the same 32 columns) into
// the domain Job. pgtype.Timestamptz values become zero-value time.Time
// when the column was NULL.
// fromCronJob 从cron任务处理cron存储。
func fromCronJob(r sqlc.CronJob) Job {
	return Job{
		ID:              r.ID,
		Name:            r.Name,
		Prompt:          r.Prompt,
		ScheduleType:    r.ScheduleType,
		ScheduleExpr:    r.ScheduleExpr,
		Timezone:        r.Timezone,
		Provider:        r.Provider,
		Model:           r.Model,
		CWD:             r.CWD,
		Config:          cloneBytes(r.Config),
		Skills:          cloneBytes(r.Skills),
		NotifyChannel:   r.NotifyChannel,
		Enabled:         r.Enabled,
		NextRunAt:       fromTS(r.NextRunAt),
		LastScheduledAt: fromTS(r.LastScheduledAt),
		LastRunAt:       fromTS(r.LastRunAt),
		ClaimedAt:       fromTS(r.ClaimedAt),
		ClaimedBy:       r.ClaimedBy,
		LeaseExpiresAt:  fromTS(r.LeaseExpiresAt),
		ClaimToken:      r.ClaimToken,
		ThreadID:        r.ThreadID,
		AgentID:         r.AgentID,
		ActiveTurnID:    r.ActiveTurnID,
		LastTurnID:      r.LastTurnID,
		FailureCount:    r.FailureCount,
		MaxAttempts:     r.MaxAttempts,
		NextRetryAt:     fromTS(r.NextRetryAt),
		LastStatus:      r.LastStatus,
		LastErrorAt:     fromTS(r.LastErrorAt),
		LastError:       r.LastError,
		CreatedAt:       fromTS(r.CreatedAt),
		UpdatedAt:       fromTS(r.UpdatedAt),
	}
}

func fromCronJobRun(r sqlc.CronJobRun) Run {
	return Run{
		ID:             r.ID,
		JobID:          r.JobID,
		ScheduledAt:    fromTS(r.ScheduledAt),
		IdempotencyKey: r.IdempotencyKey,
		DedupeKey:      r.DedupeKey,
		ThreadID:       r.ThreadID,
		AgentID:        r.AgentID,
		TurnID:         r.TurnID,
		SubmittedAt:    fromTS(r.SubmittedAt),
		Status:         r.Status,
		Error:          r.Error,
		CreatedAt:      fromTS(r.CreatedAt),
		UpdatedAt:      fromTS(r.UpdatedAt),
	}
}

func cloneBytes(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
