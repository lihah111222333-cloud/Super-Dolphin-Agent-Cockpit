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

// StopResult 是 StopSpawnedAgent 对调用方和指标暴露的标准结果枚举。
type StopResult string

// StopSpawnedAgent 结果常量。
const (
	StopResultSuccess                StopResult = "success"
	StopResultSkippedAlreadyStopped  StopResult = "skipped_already_stopped"
	StopResultSkippedAlreadyArchived StopResult = "skipped_already_archived"
	StopResultSkippedBindingMissing  StopResult = "skipped_binding_missing"
	StopResultSkippedNoThreadID      StopResult = "skipped_no_thread_id"
	StopResultSkippedLookupFailed    StopResult = "skipped_lookup_failed"
	StopResultFailed                 StopResult = "failed"
)

// AgentThreadLookup 是按 thread 反查 agent 绑定并更新持久化状态的窄接口。
type AgentThreadLookup interface {
	GetByThreadID(ctx context.Context, threadID string) (*PersistedThread, error)
	UpdateStatus(ctx context.Context, params PersistedThreadStatusUpdate) error
}

// StopAgentService 是停止 agent 所需的最小 service 接口。
type StopAgentService interface {
	StopAgent(ctx context.Context, agentID string) error
}

// StopSpawnedAgent 根据 spawned thread 反查 agent 并执行停止。
// 已停止/已归档视为幂等成功；真实失败会保留错误返回给调用方。
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

// StopSpawnedAgentMetrics 汇总 StopSpawnedAgent 各类结果计数。
type StopSpawnedAgentMetrics struct {
	Success                int64
	SkippedAlreadyStopped  int64
	SkippedAlreadyArchived int64
	SkippedBindingMissing  int64
	SkippedNoThreadID      int64
	SkippedLookupFailed    int64
	Failed                 int64
}

// stopSpawnedAgentCounter 用原子计数记录 stop helper 结果，供测试和诊断读取。
type stopSpawnedAgentCounter struct {
	success                atomic.Int64
	skippedAlreadyStopped  atomic.Int64
	skippedAlreadyArchived atomic.Int64
	skippedBindingMissing  atomic.Int64
	skippedNoThreadID      atomic.Int64
	skippedLookupFailed    atomic.Int64
	failed                 atomic.Int64
}

// Inc 按 StopResult 累加对应指标。
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

// Snapshot 返回当前计数快照，不暴露底层 atomic 字段。
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

// recordStopSpawnedAgentMetric 记录全局 StopSpawnedAgent 结果指标。
func recordStopSpawnedAgentMetric(result StopResult) {
	defaultStopSpawnedAgentCounter.Inc(result)
}

// StopSpawnedAgentCounters 返回 spawned agent 停止路径的全局计数快照。
func StopSpawnedAgentCounters() StopSpawnedAgentMetrics {
	return defaultStopSpawnedAgentCounter.Snapshot()
}

// resolveAgentIDForStop 从持久化 thread 反查 agent id，并把前置失败映射为 StopResult。
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

// finalizeStopOutcome 统一停止后的日志和返回值。
// 幂等结果会吞掉底层错误，真实失败继续把错误交给调用方记录和处理。
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

// logHandle 是 stop_helper.go 需要的最小日志接口。
// 保持窄接口，方便生产传 pkglogger.Get()，测试传轻量 stub。
type logHandle interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
}

// isThreadNotFound 识别“线程行不存在”这类可幂等跳过的查找结果。
// 它只处理 not found 语义，真正的 store 失败会保留给调用方。
func isThreadNotFound(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, errAgentNotFound) || platformdb.IsNotFound(err)
}

// classifyStopError 将 service.StopAgent 的错误归类为 StopResult。
func classifyStopError(err error) StopResult {
	if err == nil {
		return StopResultSuccess
	}
	if errors.Is(err, errAgentNotFound) {
		return StopResultSkippedAlreadyArchived
	}
	if errors.Is(err, errAgentNotRunningForStopper) || errors.Is(err, errAgentStoppingForStopper) {
		return StopResultSkippedAlreadyStopped
	}
	return StopResultFailed
}
