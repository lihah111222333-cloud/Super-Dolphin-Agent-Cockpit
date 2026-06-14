package orchestration

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type StopResult string

const (
	StopResultSuccess                StopResult = "success"
	StopResultSkippedAlreadyStopped  StopResult = "skipped_already_stopped"
	StopResultSkippedAlreadyArchived StopResult = "skipped_already_archived"
	StopResultSkippedBindingMissing  StopResult = "skipped_binding_missing"
	StopResultSkippedNoThreadID      StopResult = "skipped_no_thread_id"
	StopResultSkippedLookupFailed    StopResult = "skipped_lookup_failed"
	StopResultFailed                 StopResult = "failed"
)

type AgentThreadLookup interface {
	GetByThreadID(ctx context.Context, threadID string) (*PersistedThread, error)
	UpdateStatus(ctx context.Context, params PersistedThreadStatusUpdate) error
}

type StopAgentService interface {
	StopAgent(ctx context.Context, agentID string) error
}

type stopSpawnedAgentSink interface {
	Inc(result StopResult)
}

// StopSpawnedAgent 停止spawned代理。
func StopSpawnedAgent(ctx context.Context, threads AgentThreadLookup, svc StopAgentService, threadID string) (StopResult, error) {
	threadID = strings.TrimSpace(threadID)
	logger := pkglogger.Get()

	agentID, thread, preflight, preflightErr := resolveAgentIDForStop(ctx, logger, threads, svc, threadID)
	if preflight != "" {
		return preflight, preflightErr
	}

	stopErr := svc.StopAgent(ctx, agentID)
	result := classifyStopError(stopErr)
	switch result {
	case StopResultSuccess, StopResultSkippedAlreadyStopped, StopResultSkippedAlreadyArchived:
		status := strings.ToLower(strings.TrimSpace(thread.Status))
		if status != persistedThreadStatusArchived && status != "stopped" {
			if err := threads.UpdateStatus(ctx, PersistedThreadStatusUpdate{ThreadID: threadID, Status: "stopped", UpdatedAt: time.Now().Unix()}); err != nil {
				logger.Warn("stop_helper: persisted thread stop status update failed", "thread_id", threadID, "result", string(result), "err", err)
				recordStopSpawnedAgentMetric(StopResultFailed)
				return StopResultFailed, err
			}
		}
	}
	recordStopSpawnedAgentMetric(result)
	return finalizeStopOutcome(logger, threadID, agentID, result, stopErr)
}

type StopSpawnedAgentMetrics struct {
	Success                int64
	SkippedAlreadyStopped  int64
	SkippedAlreadyArchived int64
	SkippedBindingMissing  int64
	SkippedNoThreadID      int64
	SkippedLookupFailed    int64
	Failed                 int64
}

type stopSpawnedAgentCounter struct {
	success                atomic.Int64
	skippedAlreadyStopped  atomic.Int64
	skippedAlreadyArchived atomic.Int64
	skippedBindingMissing  atomic.Int64
	skippedNoThreadID      atomic.Int64
	skippedLookupFailed    atomic.Int64
	failed                 atomic.Int64
}

// Inc 累加编排。
func (c *stopSpawnedAgentCounter) Inc(result StopResult) {
	if c == nil {
		return
	}
	switch result {
	case StopResultSuccess:
		c.success.Add(1)
	case StopResultSkippedAlreadyStopped:
		c.skippedAlreadyStopped.Add(1)
	case StopResultSkippedAlreadyArchived:
		c.skippedAlreadyArchived.Add(1)
	case StopResultSkippedBindingMissing:
		c.skippedBindingMissing.Add(1)
	case StopResultSkippedNoThreadID:
		c.skippedNoThreadID.Add(1)
	case StopResultSkippedLookupFailed:
		c.skippedLookupFailed.Add(1)
	case StopResultFailed:
		c.failed.Add(1)
	}
}

// Snapshot 处理快照。
func (c *stopSpawnedAgentCounter) Snapshot() StopSpawnedAgentMetrics {
	if c == nil {
		return StopSpawnedAgentMetrics{}
	}
	return StopSpawnedAgentMetrics{
		Success:                c.success.Load(),
		SkippedAlreadyStopped:  c.skippedAlreadyStopped.Load(),
		SkippedAlreadyArchived: c.skippedAlreadyArchived.Load(),
		SkippedBindingMissing:  c.skippedBindingMissing.Load(),
		SkippedNoThreadID:      c.skippedNoThreadID.Load(),
		SkippedLookupFailed:    c.skippedLookupFailed.Load(),
		Failed:                 c.failed.Load(),
	}
}

var defaultStopSpawnedAgentCounter = &stopSpawnedAgentCounter{}

func recordStopSpawnedAgentMetric(result StopResult) {
	defaultStopSpawnedAgentCounter.Inc(result)
}

// StopSpawnedAgentCounters 停止spawned代理counters。
func StopSpawnedAgentCounters() StopSpawnedAgentMetrics {
	return defaultStopSpawnedAgentCounter.Snapshot()
}

// resolveAgentIDForStop 为stop解析代理ID。
func resolveAgentIDForStop(ctx context.Context, logger logHandle, threads AgentThreadLookup, svc StopAgentService, threadID string) (string, *PersistedThread, StopResult, error) {
	if threadID == "" {
		recordStopSpawnedAgentMetric(StopResultSkippedNoThreadID)
		return "", nil, StopResultSkippedNoThreadID, nil
	}
	if threads == nil {
		recordStopSpawnedAgentMetric(StopResultSkippedLookupFailed)
		logger.Warn("stop_helper: AgentThreadLookup is nil", "thread_id", threadID)
		return "", nil, StopResultSkippedLookupFailed, errors.New("stop_helper: AgentThreadLookup is nil")
	}
	if svc == nil {
		recordStopSpawnedAgentMetric(StopResultSkippedLookupFailed)
		logger.Warn("stop_helper: StopAgentService is nil", "thread_id", threadID)
		return "", nil, StopResultSkippedLookupFailed, errors.New("stop_helper: StopAgentService is nil")
	}

	thread, err := threads.GetByThreadID(ctx, threadID)
	if err != nil {
		if isThreadNotFound(err) {
			recordStopSpawnedAgentMetric(StopResultSkippedNoThreadID)
			logger.Warn("stop_helper: thread not found during reverse lookup", "thread_id", threadID, "err", err)
			return "", nil, StopResultSkippedNoThreadID, err
		}
		recordStopSpawnedAgentMetric(StopResultSkippedLookupFailed)
		logger.Warn("stop_helper: thread lookup failed", "thread_id", threadID, "err", err)
		return "", nil, StopResultSkippedLookupFailed, err
	}
	if thread == nil {
		recordStopSpawnedAgentMetric(StopResultSkippedNoThreadID)
		logger.Warn("stop_helper: thread nil after lookup", "thread_id", threadID)
		return "", nil, StopResultSkippedNoThreadID, nil
	}
	agentID := strings.TrimSpace(thread.AgentID)
	if agentID == "" {
		recordStopSpawnedAgentMetric(StopResultSkippedBindingMissing)
		logger.Warn("stop_helper: persisted thread has empty AgentID (binding missing or archived)", "thread_id", threadID)
		return "", thread, StopResultSkippedBindingMissing, nil
	}
	return agentID, thread, "", nil
}

// finalizeStopOutcome handles the §2.4 post-stop branch: log + return.
// Idempotent results (success / skipped_already_*) drop the error;
// real failures propagate the underlying error for caller logging.
func finalizeStopOutcome(logger logHandle, threadID, agentID string, result StopResult, stopErr error) (StopResult, error) {
	switch result {
	case StopResultSuccess:
		return result, nil
	case StopResultSkippedAlreadyStopped, StopResultSkippedAlreadyArchived:
		logger.Info(
			"stop_helper: spawned agent stop skipped (idempotent)",
			"thread_id", threadID,
			"agent_id", agentID,
			"result", string(result),
			"err", stopErr,
		)
		return result, nil
	default:
		logger.Warn(
			"stop_helper: spawned agent stop failed",
			"thread_id", threadID,
			"agent_id", agentID,
			"err", stopErr,
		)
		return result, stopErr
	}
}

// logHandle is the subset of *slog.Logger used by stop_helper.go. Keeping
// it narrow lets callers pass either pkglogger.Get() or any test stub.
type logHandle interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
}

// isThreadNotFound mirrors archive.go:archiveLookupNotFound — the two
// sentinels surface "thread row was not found" rather than a real
// store failure. ADR-016 §2.2 maps this to skipped_no_thread_id.
func isThreadNotFound(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, errAgentNotFound) || platformdb.IsNotFound(err)
}

// classifyStopError implements the §2.4 switch — translate the error
// returned by service.StopAgent into a StopResult label.
//
// String-match for "is not running" / "is stopping" is the documented
// temporary tactic: helpers.go:196 / helpers.go:199 /
// service_launcher_bridge.go:355,428,492,497 build the error via
// fmt.Errorf, so there is no sentinel to errors.Is against until a
// follow-up introduces errAgentNotRunning.
func classifyStopError(err error) StopResult {
	if err == nil {
		return StopResultSuccess
	}
	if errors.Is(err, errAgentNotFound) {
		return StopResultSkippedAlreadyArchived
	}
	msg := err.Error()
	if strings.Contains(msg, "is not running") {
		return StopResultSkippedAlreadyStopped
	}
	if strings.Contains(msg, "is stopping") {
		return StopResultSkippedAlreadyStopped
	}
	return StopResultFailed
}
