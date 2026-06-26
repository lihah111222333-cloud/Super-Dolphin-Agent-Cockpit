// Package cron 定义 cron 模块发布到事件总线的 DTO。
// 这些 DTO 是 scheduler、eventsurface 和前端增量更新之间的 wire 边界。
package cron

import (
	"time"

	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
)

// JobRunStateChanged 在 cron_job_runs 状态 CAS 成功后发布。
// 字段保持扁平快照，前端可直接更新 runsByJob，不需要再回查 run 行。
type JobRunStateChanged struct {
	shareddto.EventHeader
	JobID       string    `json:"job_id"`
	RunID       string    `json:"run_id"`
	Status      string    `json:"status"`
	TurnID      string    `json:"turn_id,omitempty"`
	Error       string    `json:"error,omitempty"`
	ScheduledAt time.Time `json:"scheduled_at,omitempty"`
}

// Type 返回 cron job run 状态变更事件分发用的类型编号。
func (JobRunStateChanged) Type() uint32 { return shareddto.EventTypeCronJobRunStateChanged }
