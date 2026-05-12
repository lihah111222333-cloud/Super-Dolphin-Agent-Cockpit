package orchestration

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// --- spies ----------------------------------------------------------------

// stopHelperThreadSpy is the AgentThreadLookup test double — returns the
// programmed thread / error and records call count.
type stopHelperThreadSpy struct {
	thread *PersistedThread
	err    error
	calls  int
}

func (s *stopHelperThreadSpy) GetByThreadID(_ context.Context, _ string) (*PersistedThread, error) {
	s.calls++
	return s.thread, s.err
}

// stopHelperServiceSpy is the StopAgentService test double — programmed
// stop error + records last agentID and call count.
type stopHelperServiceSpy struct {
	stopErr     error
	lastAgentID string
	calls       int
}

func (s *stopHelperServiceSpy) StopAgent(_ context.Context, agentID string) error {
	s.calls++
	s.lastAgentID = agentID
	return s.stopErr
}

// stopHelperMetricSpy is a stopSpawnedAgentSink double. Each Inc is
// recorded so the test can assert the helper hits the right label
// exactly once per call.
type stopHelperMetricSpy struct {
	counts map[StopResult]int
}

func newStopHelperMetricSpy() *stopHelperMetricSpy {
	return &stopHelperMetricSpy{counts: make(map[StopResult]int)}
}

func (m *stopHelperMetricSpy) Inc(result StopResult) {
	m.counts[result]++
}

// withMetricSpy swaps stopSpawnedAgentMetrics for the lifetime of the
// test. Returns a restore func — defer-friendly.
func withMetricSpy(t *testing.T) *stopHelperMetricSpy {
	t.Helper()
	spy := newStopHelperMetricSpy()
	prev := stopSpawnedAgentMetrics
	stopSpawnedAgentMetrics = spy
	t.Cleanup(func() { stopSpawnedAgentMetrics = prev })
	return spy
}

// --- helpers --------------------------------------------------------------

func assertResult(t *testing.T, got, want StopResult) {
	t.Helper()
	if got != want {
		t.Fatalf("StopSpawnedAgent result = %q, want %q", got, want)
	}
}

func assertMetric(t *testing.T, spy *stopHelperMetricSpy, want StopResult) {
	t.Helper()
	if spy.counts[want] != 1 {
		t.Fatalf("metric %q count = %d, want 1; full snapshot=%v",
			want, spy.counts[want], spy.counts)
	}
	for label, n := range spy.counts {
		if label == want {
			continue
		}
		if n != 0 {
			t.Fatalf("metric %q unexpectedly = %d (want 0); only %q should fire",
				label, n, want)
		}
	}
}

// --- tests ---------------------------------------------------------------

func TestStopSpawnedAgent_Success(t *testing.T) {
	metrics := withMetricSpy(t)
	threads := &stopHelperThreadSpy{
		thread: &PersistedThread{ThreadID: "thr-1", AgentID: "agent-1"},
	}
	svc := &stopHelperServiceSpy{stopErr: nil}

	result, err := StopSpawnedAgent(context.Background(), threads, svc, "thr-1")
	if err != nil {
		t.Fatalf("Success branch must not surface error, got %v", err)
	}
	assertResult(t, result, StopResultSuccess)
	assertMetric(t, metrics, StopResultSuccess)
	if svc.calls != 1 || svc.lastAgentID != "agent-1" {
		t.Fatalf("StopAgent calls=%d lastAgentID=%q, want 1 / agent-1",
			svc.calls, svc.lastAgentID)
	}
}

func TestStopSpawnedAgent_SkippedAlreadyStopped(t *testing.T) {
	metrics := withMetricSpy(t)
	threads := &stopHelperThreadSpy{
		thread: &PersistedThread{ThreadID: "thr-1", AgentID: "agent-1"},
	}
	// Matches helpers.go:196 / service_launcher_bridge.go:355,492 wording.
	svc := &stopHelperServiceSpy{
		stopErr: fmt.Errorf("agent %q is not running", "agent-1"),
	}

	result, err := StopSpawnedAgent(context.Background(), threads, svc, "thr-1")
	if err != nil {
		t.Fatalf("skipped_already_stopped must not surface error, got %v", err)
	}
	assertResult(t, result, StopResultSkippedAlreadyStopped)
	assertMetric(t, metrics, StopResultSkippedAlreadyStopped)
}

func TestStopSpawnedAgent_SkippedStopping(t *testing.T) {
	metrics := withMetricSpy(t)
	threads := &stopHelperThreadSpy{
		thread: &PersistedThread{ThreadID: "thr-1", AgentID: "agent-1"},
	}
	// Matches helpers.go:199 / service_launcher_bridge.go:428,497 wording.
	svc := &stopHelperServiceSpy{
		stopErr: fmt.Errorf("agent %q is stopping", "agent-1"),
	}

	result, err := StopSpawnedAgent(context.Background(), threads, svc, "thr-1")
	if err != nil {
		t.Fatalf("is-stopping branch must not surface error, got %v", err)
	}
	assertResult(t, result, StopResultSkippedAlreadyStopped)
	assertMetric(t, metrics, StopResultSkippedAlreadyStopped)
}

func TestStopSpawnedAgent_SkippedAlreadyArchived(t *testing.T) {
	metrics := withMetricSpy(t)
	threads := &stopHelperThreadSpy{
		thread: &PersistedThread{ThreadID: "thr-1", AgentID: "agent-1"},
	}
	// service.StopAgent surfaces fmt.Errorf("%w: ...", errAgentNotFound)
	// — wrap so errors.Is matches.
	svc := &stopHelperServiceSpy{
		stopErr: fmt.Errorf("wrapped: %w", errAgentNotFound),
	}

	result, err := StopSpawnedAgent(context.Background(), threads, svc, "thr-1")
	if err != nil {
		t.Fatalf("already_archived must not surface error, got %v", err)
	}
	assertResult(t, result, StopResultSkippedAlreadyArchived)
	assertMetric(t, metrics, StopResultSkippedAlreadyArchived)
}

func TestStopSpawnedAgent_SkippedBindingMissing(t *testing.T) {
	metrics := withMetricSpy(t)
	// Thread exists but derived AgentID is "" — binding missing/archived.
	threads := &stopHelperThreadSpy{
		thread: &PersistedThread{ThreadID: "thr-1", AgentID: ""},
	}
	svc := &stopHelperServiceSpy{}

	result, err := StopSpawnedAgent(context.Background(), threads, svc, "thr-1")
	if err != nil {
		t.Fatalf("binding_missing must not surface error, got %v", err)
	}
	assertResult(t, result, StopResultSkippedBindingMissing)
	assertMetric(t, metrics, StopResultSkippedBindingMissing)
	if svc.calls != 0 {
		t.Fatalf("StopAgent should not be called when AgentID is empty, got %d calls",
			svc.calls)
	}
}

func TestStopSpawnedAgent_SkippedNoThreadID_EmptyInput(t *testing.T) {
	metrics := withMetricSpy(t)
	threads := &stopHelperThreadSpy{}
	svc := &stopHelperServiceSpy{}

	result, err := StopSpawnedAgent(context.Background(), threads, svc, "   ")
	if err != nil {
		t.Fatalf("empty threadID must not surface error, got %v", err)
	}
	assertResult(t, result, StopResultSkippedNoThreadID)
	assertMetric(t, metrics, StopResultSkippedNoThreadID)
	if threads.calls != 0 {
		t.Fatalf("lookup should be skipped for empty threadID, got %d calls",
			threads.calls)
	}
}

func TestStopSpawnedAgent_SkippedNoThreadID_NilRow(t *testing.T) {
	metrics := withMetricSpy(t)
	// thread store returns (nil, nil) — row absent without error sentinel.
	threads := &stopHelperThreadSpy{thread: nil, err: nil}
	svc := &stopHelperServiceSpy{}

	result, err := StopSpawnedAgent(context.Background(), threads, svc, "thr-missing")
	if err != nil {
		t.Fatalf("nil-row no_thread_id must not surface error, got %v", err)
	}
	assertResult(t, result, StopResultSkippedNoThreadID)
	assertMetric(t, metrics, StopResultSkippedNoThreadID)
	if svc.calls != 0 {
		t.Fatalf("StopAgent should not be called when row absent, got %d calls",
			svc.calls)
	}
}

func TestStopSpawnedAgent_SkippedNoThreadID_NotFoundSentinel(t *testing.T) {
	metrics := withMetricSpy(t)
	// store returns errAgentNotFound (archive_test.go:208 pattern).
	threads := &stopHelperThreadSpy{thread: nil, err: errAgentNotFound}
	svc := &stopHelperServiceSpy{}

	result, err := StopSpawnedAgent(context.Background(), threads, svc, "thr-missing")
	if err != nil {
		t.Fatalf("not-found sentinel must collapse to no_thread_id w/o error, got %v",
			err)
	}
	assertResult(t, result, StopResultSkippedNoThreadID)
	assertMetric(t, metrics, StopResultSkippedNoThreadID)
}

func TestStopSpawnedAgent_SkippedLookupFailed(t *testing.T) {
	metrics := withMetricSpy(t)
	lookupErr := errors.New("rpc broken pipe")
	threads := &stopHelperThreadSpy{err: lookupErr}
	svc := &stopHelperServiceSpy{}

	result, err := StopSpawnedAgent(context.Background(), threads, svc, "thr-1")
	if !errors.Is(err, lookupErr) {
		t.Fatalf("lookup_failed must surface original error for log, got %v", err)
	}
	assertResult(t, result, StopResultSkippedLookupFailed)
	assertMetric(t, metrics, StopResultSkippedLookupFailed)
	if svc.calls != 0 {
		t.Fatalf("StopAgent should not be called after lookup failed, got %d calls",
			svc.calls)
	}
}

func TestStopSpawnedAgent_Failed(t *testing.T) {
	metrics := withMetricSpy(t)
	threads := &stopHelperThreadSpy{
		thread: &PersistedThread{ThreadID: "thr-1", AgentID: "agent-1"},
	}
	stopErr := errors.New("transport closed")
	svc := &stopHelperServiceSpy{stopErr: stopErr}

	result, err := StopSpawnedAgent(context.Background(), threads, svc, "thr-1")
	if !errors.Is(err, stopErr) {
		t.Fatalf("failed must surface original error for log, got %v", err)
	}
	assertResult(t, result, StopResultFailed)
	assertMetric(t, metrics, StopResultFailed)
}

// --- counter integration sanity ------------------------------------------

// TestStopSpawnedAgent_DefaultCounterIncrements proves the
// stop_metric.go init() wires defaultStopSpawnedAgentCounter into
// stopSpawnedAgentMetrics — i.e. the package singleton actually
// receives Inc() calls when no spy is installed.
func TestStopSpawnedAgent_DefaultCounterIncrements(t *testing.T) {
	before := defaultStopSpawnedAgentCounter.Snapshot()

	threads := &stopHelperThreadSpy{
		thread: &PersistedThread{ThreadID: "thr-1", AgentID: "agent-1"},
	}
	svc := &stopHelperServiceSpy{}
	result, err := StopSpawnedAgent(context.Background(), threads, svc, "thr-1")
	if err != nil {
		t.Fatalf("Success branch must not surface error, got %v", err)
	}
	if result != StopResultSuccess {
		t.Fatalf("result = %q, want success", result)
	}

	after := defaultStopSpawnedAgentCounter.Snapshot()
	if after.Success != before.Success+1 {
		t.Fatalf("default counter Success delta = %d, want 1",
			after.Success-before.Success)
	}
}
