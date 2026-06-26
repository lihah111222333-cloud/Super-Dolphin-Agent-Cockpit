package cron

import (
	"time"

	db "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

// milliToTime 将毫秒时间戳转换为 time.Time，0 值按未设置处理。
func milliToTime(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return db.TimeFromMillis(ms)
}

// milliPtrToTime 将可空毫秒时间戳转换为 time.Time，nil 和 0 都表示未设置。
func milliPtrToTime(ms *int64) time.Time {
	if ms == nil || *ms == 0 {
		return time.Time{}
	}
	return db.TimeFromMillis(*ms)
}

// fromCronJob 将 sqlc.CronJob 行转换为领域 Job。
// 毫秒时间戳会转换成 time.Time，NULL 或 0 保持为零值时间。
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
		Enabled:         r.Enabled != 0,
		NextRunAt:       milliToTime(r.NextRunAt),
		LastScheduledAt: milliPtrToTime(r.LastScheduledAt),
		LastRunAt:       milliPtrToTime(r.LastRunAt),
		ClaimedAt:       milliPtrToTime(r.ClaimedAt),
		ClaimedBy:       r.ClaimedBy,
		LeaseExpiresAt:  milliPtrToTime(r.LeaseExpiresAt),
		ClaimToken:      r.ClaimToken,
		ThreadID:        r.ThreadID,
		AgentID:         r.AgentID,
		ActiveTurnID:    r.ActiveTurnID,
		LastTurnID:      r.LastTurnID,
		FailureCount:    int32(r.FailureCount),
		MaxAttempts:     int32(r.MaxAttempts),
		NextRetryAt:     milliPtrToTime(r.NextRetryAt),
		LastStatus:      r.LastStatus,
		LastErrorAt:     milliPtrToTime(r.LastErrorAt),
		LastError:       r.LastError,
		CreatedAt:       milliToTime(r.CreatedAt),
		UpdatedAt:       milliToTime(r.UpdatedAt),
	}
}

// fromCronJobRun 将 sqlc.CronJobRun 行转换为领域 Run，保持状态字段原样交给上层判断。
func fromCronJobRun(r sqlc.CronJobRun) Run {
	return Run{
		ID:             r.ID,
		JobID:          r.JobID,
		ScheduledAt:    milliToTime(r.ScheduledAt),
		IdempotencyKey: r.IdempotencyKey,
		DedupeKey:      r.DedupeKey,
		ThreadID:       r.ThreadID,
		AgentID:        r.AgentID,
		TurnID:         r.TurnID,
		SubmittedAt:    milliPtrToTime(r.SubmittedAt),
		Status:         r.Status,
		Error:          r.Error,
		CreatedAt:      milliToTime(r.CreatedAt),
		UpdatedAt:      milliToTime(r.UpdatedAt),
	}
}

// cloneBytes 复制配置类字节切片，避免调用方修改 sqlc 返回缓冲区。
func cloneBytes(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
