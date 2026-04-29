package cron

import (
	"context"
	"log/slog"
	"time"

	"github.com/kelindar/event"

	crondto "github.com/anthropic-ai/super-agent-v3/internal/dto/cron"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	cronstore "github.com/anthropic-ai/super-agent-v3/internal/store/cron"
)

// WithDispatcher registers the event dispatcher that the scheduler
// publishes JobRunStateChanged events on. Passing nil silently disables
// event publishing — production wiring goes through fx (provideScheduler);
// tests typically leave the dispatcher unset.
func (s *Scheduler) WithDispatcher(dispatcher *event.Dispatcher) *Scheduler {
	if s != nil {
		s.dispatcher = dispatcher
	}
	return s
}

// publishRunState emits a JobRunStateChanged onto the dispatcher. It is
// fire-and-forget: subscriber failures are owned by their own handlers.
func (s *Scheduler) publishRunState(jobID, runID, status, turnID, errStr string, scheduledAt time.Time) {
	if s == nil || s.dispatcher == nil {
		return
	}
	event.Publish(s.dispatcher, crondto.JobRunStateChanged{
		EventHeader: shareddto.EventHeader{Timestamp: s.now().UTC()},
		JobID:       jobID,
		RunID:       runID,
		Status:      status,
		TurnID:      turnID,
		Error:       errStr,
		ScheduledAt: scheduledAt,
	})
}

// casLogPublish runs a CAS run-status transition; on error it logs a
// warning labeled with the transition string (same fail-soft posture
// the inline finalize sites had before consolidation), on success it
// publishes a JobRunStateChanged. The transition arg is e.g.
// "submitting->failed".
func (s *Scheduler) casLogPublish(
	ctx context.Context,
	params cronstore.CASRunStatusParams,
	transition, jobID, runID, status, turnID, errStr string,
	scheduledAt time.Time,
) {
	if err := s.store.CASRunStatus(ctx, params); err != nil {
		s.logger.Warn("cron: CAS "+transition+" failed",
			slog.String("job_id", jobID),
			slog.String("run_id", runID),
			slog.String("error", err.Error()),
		)
		return
	}
	s.publishRunState(jobID, runID, status, turnID, errStr, scheduledAt)
}
