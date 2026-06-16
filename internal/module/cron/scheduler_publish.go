package cron

import (
	"context"
	"time"

	"github.com/kelindar/event"

	crondto "github.com/anthropic-ai/super-agent-v3/internal/dto/cron"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	cronstore "github.com/anthropic-ai/super-agent-v3/internal/store/cron"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// WithDispatcher registers the event dispatcher that the scheduler
// publishes JobRunStateChanged events on. Passing nil silently disables
// event publishing — production wiring goes through fx (provideScheduler);
// tests typically leave the dispatcher unset.
//
// dispatcher 只是通知 UI/订阅者；状态真值仍在数据库里。
func (s *Scheduler) WithDispatcher(dispatcher *event.Dispatcher) *Scheduler {
	if s != nil {
		s.dispatcher = dispatcher
	}
	return s
}

// publishRunState emits a JobRunStateChanged onto the dispatcher. It is
// fire-and-forget: subscriber failures are owned by their own handlers.
//
// 发布失败不影响调度状态，订阅侧需要自己补偿或轮询。
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
//
// 这个 helper 只做 run 行的可见状态更新；失败时仍让调用方继续释放 job claim。
func (s *Scheduler) casLogPublish(
	ctx context.Context,
	params cronstore.CASRunStatusParams,
	transition, jobID, runID, status, turnID, errStr string,
	scheduledAt time.Time,
) {
	if err := s.store.CASRunStatus(ctx, params); err != nil {
		s.logger.Warn("cron: CAS "+transition+" failed",
			pkglogger.String("job_id", jobID),
			pkglogger.String("run_id", runID),
			pkglogger.String("error", err.Error()),
		)
		return
	}
	s.publishRunState(jobID, runID, status, turnID, errStr, scheduledAt)
}
