package cron

import (
	"context"
	"log/slog"
	"time"

	"github.com/kelindar/event"

	crondto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/cron"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
)

// WithDispatcher 设置调度器用于发布 JobRunStateChanged 的事件分发器。
// dispatcher 只是通知 UI/订阅者；状态真值仍在数据库里，nil 仅表示当前运行模式不做事件广播。
func (s *Scheduler) WithDispatcher(dispatcher *event.Dispatcher) *Scheduler {
	if s != nil {
		s.dispatcher = dispatcher
	}
	return s
}

// publishRunState 发布 run 状态变化事件。
// 事件是调度结果的旁路通知；订阅侧失败不能回滚已落库的 run/job 状态。
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

// casLogPublish 执行 run 状态 CAS 更新并在成功后广播状态变化。
// 这个 helper 只做 run 行的可见状态更新；CAS 失败会记录 transition，调用方仍继续释放 job claim。
func (s *Scheduler) casLogPublish(
	ctx context.Context,
	params CASRunStatusParams,
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
