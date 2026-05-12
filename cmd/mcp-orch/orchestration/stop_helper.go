package orchestration

import (
	"context"
	"errors"
	"strings"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// StopResult enumerates the seven semantic outcomes of StopSpawnedAgent.
// The values double as the {result} label values for the
// dag_node_stop_spawned_agent_total counter (see stop_metric.go).
//
// ADR-016 v1.2 §2.4 + §2.5 拍板：subscriber 失败兜底要细分到 7 种语义，
// 不向调用方抛 error（仅 lookup 真错时把原始 error 一并返回供 log 用）。
type StopResult string

const (
	// StopResultSuccess: svc.StopAgent returned nil — agent was running
	// and the stop signal was accepted.
	StopResultSuccess StopResult = "success"
	// StopResultSkippedAlreadyStopped: svc.StopAgent returned a
	// non-sentinel error whose message contains "is not running" or
	// "is stopping" (helpers.go:196,199 / service_launcher_bridge.go).
	// Treated as idempotent success.
	StopResultSkippedAlreadyStopped StopResult = "skipped_already_stopped"
	// StopResultSkippedAlreadyArchived: svc.StopAgent returned an error
	// matching errAgentNotFound — the agent is already gone from the
	// service registry (archived / never registered).
	StopResultSkippedAlreadyArchived StopResult = "skipped_already_archived"
	// StopResultSkippedBindingMissing: the persisted thread exists but
	// its derived AgentID is empty. ADR-016 §2.2 documents this branch
	// — AgentID is derived from a LEFT JOIN with agent_provider_binding
	// in the sqlc query, so missing / archived binding yields "".
	StopResultSkippedBindingMissing StopResult = "skipped_binding_missing"
	// StopResultSkippedNoThreadID: caller passed an empty threadID, the
	// thread store returned a nil persisted row, or the lookup surfaced
	// a not-found sentinel (errAgentNotFound / platformdb.IsNotFound).
	StopResultSkippedNoThreadID StopResult = "skipped_no_thread_id"
	// StopResultSkippedLookupFailed: thread store lookup returned a
	// non-not-found error (RPC / DB failure). The error is returned to
	// the caller for logging but the outcome is still "skipped" — the
	// subscriber does not retry, ADR-016 §2.5.
	StopResultSkippedLookupFailed StopResult = "skipped_lookup_failed"
	// StopResultFailed: svc.StopAgent returned an error that is neither
	// a known idempotent signal nor errAgentNotFound. Returned together
	// with the original error so the caller can log + alert.
	StopResultFailed StopResult = "failed"
)

// AgentThreadLookup is the narrow port stop_helper.go requires from the
// persisted thread store. Intentionally a subset of the package-level
// AgentThreadStore (persistent_store_types.go:51-55) so unit tests can
// inject a single-method mock without re-declaring ListAll / UpdateStatus.
//
// ADR-016 §3.2 contract #1: reverse lookup must go through this method.
type AgentThreadLookup interface {
	GetByThreadID(ctx context.Context, threadID string) (*PersistedThread, error)
}

// StopAgentService is the narrow port stop_helper.go requires from the
// orchestration service. Intentionally a single-method subset of
// *service so callers (A1 subscriber) and tests can inject mocks.
//
// ADR-016 §3.2 contract #2: stop must go through service.StopAgent
// (cmd/mcp-orch/orchestration/service.go:414), never ArchiveAgent /
// stopProcess directly.
type StopAgentService interface {
	StopAgent(ctx context.Context, agentID string) error
}

// stopSpawnedAgentSink is the narrow interface used by StopSpawnedAgent
// to increment the per-result counter. stop_metric.go provides the real
// implementation; tests in stop_helper_test.go swap in a spy.
//
// Decoupled from concrete metric struct so C3.1 (this file) can be
// reviewed independently from C3.2 (counter wiring).
type stopSpawnedAgentSink interface {
	Inc(result StopResult)
}

// stopSpawnedAgentNoopSink is the package-default sink — present so the
// helper is safe to call before stop_metric.go installs the real one.
// stop_metric.go init() replaces stopSpawnedAgentMetrics.
type stopSpawnedAgentNoopSink struct{}

func (stopSpawnedAgentNoopSink) Inc(StopResult) {}

// stopSpawnedAgentMetrics is the package-level sink consulted by
// StopSpawnedAgent. stop_metric.go init() reassigns it to the real
// atomic-counter implementation; tests reassign it to a spy.
var stopSpawnedAgentMetrics stopSpawnedAgentSink = stopSpawnedAgentNoopSink{}

// StopSpawnedAgent implements the five semantic contracts of ADR-016
// v1.2 §3.2:
//
//  1. Reverse lookup: threadID -> PersistedThread.AgentID via
//     AgentThreadLookup.GetByThreadID (never queries binding directly).
//  2. Stop call: dispatches to StopAgentService.StopAgent (never
//     ArchiveAgent).
//  3. Failure handling: log.Warn + metric counter; non-lookup errors
//     are NOT propagated to the subscriber.
//  4. Empty agentID: persisted thread present but AgentID == "" -> skip
//     stop with skipped_binding_missing.
//  5. Idempotency: errAgentNotFound + "is not running" + "is stopping"
//     all count as skipped_already_*, not failed.
//
// The returned error is non-nil only when the underlying lookup or
// stop call surfaced a real failure (StopResultSkippedLookupFailed /
// StopResultFailed). Callers MUST NOT propagate it — it is provided
// solely so the caller can include it in a structured log entry.
// All other StopResult values come with err == nil.
func StopSpawnedAgent(
	ctx context.Context,
	threads AgentThreadLookup,
	svc StopAgentService,
	threadID string,
) (StopResult, error) {
	threadID = strings.TrimSpace(threadID)
	logger := pkglogger.Get()

	agentID, preflight, preflightErr := resolveAgentIDForStop(ctx, logger, threads, svc, threadID)
	if preflight != "" {
		return preflight, preflightErr
	}

	stopErr := svc.StopAgent(ctx, agentID)
	result := classifyStopError(stopErr)
	stopSpawnedAgentMetrics.Inc(result)
	return finalizeStopOutcome(logger, threadID, agentID, result, stopErr)
}

// resolveAgentIDForStop runs the §2.2 reverse-lookup path. Returns the
// resolved agentID (when non-empty), the short-circuit StopResult (when
// non-empty), and the error to surface to the caller. Exactly one of
// agentID / result is non-empty.
func resolveAgentIDForStop(
	ctx context.Context,
	logger logHandle,
	threads AgentThreadLookup,
	svc StopAgentService,
	threadID string,
) (string, StopResult, error) {
	if threadID == "" {
		stopSpawnedAgentMetrics.Inc(StopResultSkippedNoThreadID)
		return "", StopResultSkippedNoThreadID, nil
	}
	if threads == nil {
		stopSpawnedAgentMetrics.Inc(StopResultSkippedLookupFailed)
		logger.Warn("stop_helper: AgentThreadLookup is nil", "thread_id", threadID)
		return "", StopResultSkippedLookupFailed, errors.New("stop_helper: AgentThreadLookup is nil")
	}
	if svc == nil {
		stopSpawnedAgentMetrics.Inc(StopResultSkippedLookupFailed)
		logger.Warn("stop_helper: StopAgentService is nil", "thread_id", threadID)
		return "", StopResultSkippedLookupFailed, errors.New("stop_helper: StopAgentService is nil")
	}

	thread, err := threads.GetByThreadID(ctx, threadID)
	if err != nil {
		if isThreadNotFound(err) {
			stopSpawnedAgentMetrics.Inc(StopResultSkippedNoThreadID)
			logger.Warn("stop_helper: thread not found during reverse lookup",
				"thread_id", threadID, "err", err)
			return "", StopResultSkippedNoThreadID, nil
		}
		stopSpawnedAgentMetrics.Inc(StopResultSkippedLookupFailed)
		logger.Warn("stop_helper: thread lookup failed",
			"thread_id", threadID, "err", err)
		return "", StopResultSkippedLookupFailed, err
	}
	if thread == nil {
		stopSpawnedAgentMetrics.Inc(StopResultSkippedNoThreadID)
		logger.Warn("stop_helper: thread nil after lookup", "thread_id", threadID)
		return "", StopResultSkippedNoThreadID, nil
	}
	agentID := strings.TrimSpace(thread.AgentID)
	if agentID == "" {
		stopSpawnedAgentMetrics.Inc(StopResultSkippedBindingMissing)
		logger.Warn(
			"stop_helper: persisted thread has empty AgentID (binding missing or archived)",
			"thread_id", threadID,
		)
		return "", StopResultSkippedBindingMissing, nil
	}
	return agentID, "", nil
}

// finalizeStopOutcome handles the §2.4 post-stop branch: log + return.
// Idempotent results (success / skipped_already_*) drop the error;
// real failures propagate the underlying error for caller logging.
func finalizeStopOutcome(
	logger logHandle,
	threadID, agentID string,
	result StopResult,
	stopErr error,
) (StopResult, error) {
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
