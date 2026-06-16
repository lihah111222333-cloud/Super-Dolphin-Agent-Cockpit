// Package cron carries DTO event shapes for the cron module.
//
// JobRunStateChanged is the single event the cron scheduler publishes
// onto the internal event dispatcher every time a cron_job_runs row
// transitions between states. It is the upstream signal the wails
// EventBridge picks up via eventsurface and re-emits to the frontend
// as cron/job/runStateChanged so the UI can incrementally update
// runsByJob without polling.
package cron

import (
	"time"

	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/eventcore"
)

// JobRunStateChanged is published by Scheduler at every CAS-success
// run state transition: pending → submitting → submitted → running →
// finished | failed | observe_lost. The fields are intentionally a
// flat snapshot — consumers do not need to chase the run row again
// for typical UI updates.
type JobRunStateChanged struct {
	shareddto.EventHeader
	JobID       string    `json:"job_id"`
	RunID       string    `json:"run_id"`
	Status      string    `json:"status"`
	TurnID      string    `json:"turn_id,omitempty"`
	Error       string    `json:"error,omitempty"`
	ScheduledAt time.Time `json:"scheduled_at,omitempty"`
}

// Type returns the dispatcher Type tag for JobRunStateChanged.
// Type 返回事件分发用的类型编号。
func (JobRunStateChanged) Type() uint32 { return shareddto.EventTypeCronJobRunStateChanged }
