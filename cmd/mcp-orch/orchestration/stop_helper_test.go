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
	thread      *PersistedThread
	err         error
	calls       int
	update      PersistedThreadStatusUpdate
	updateCalls int
	updateErr   error
}

func (s *stopHelperThreadSpy) GetByThreadID(_ context.Context, _ string) (*PersistedThread, error) {
	s.calls++
	return s.thread, s.err
}

func (s *stopHelperThreadSpy) UpdateStatus(_ context.Context, update PersistedThreadStatusUpdate) error {
	s.updateCalls++
	s.update = update
	return s.updateErr
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

// --- helpers --------------------------------------------------------------

func assertResult(t *testing.T, got, want StopResult) {
	t.Helper()
	if got != want {
		t.Fatalf("StopSpawnedAgent result = %q, want %q", got, want)
	}
}

func assertMetricDelta(t *testing.T, before StopSpawnedAgentMetrics, counter *stopSpawnedAgentCounter, want StopResult) {
	t.Helper()
	after := StopSpawnedAgentCounters(counter)
	for _, result := range []StopResult{
		StopResultSuccess,
		StopResultSkippedAlreadyStopped,
		StopResultSkippedAlreadyArchived,
		StopResultSkippedBindingMissing,
		StopResultSkippedNoThreadID,
		StopResultSkippedLookupFailed,
		StopResultFailed,
	} {
		delta := metricValue(after, result) - metricValue(before, result)
		wantDelta := int64(0)
		if result == want {
			wantDelta = 1
		}
		if delta != wantDelta {
			t.Fatalf("metric %q delta = %d, want %d; before=%+v after=%+v",
				result, delta, wantDelta, before, after)
		}
	}
}

func metricValue(snapshot StopSpawnedAgentMetrics, result StopResult) int64 {
	switch result {
	case StopResultSuccess:
		return snapshot.Success
	case StopResultSkippedAlreadyStopped:
		return snapshot.SkippedAlreadyStopped
	case StopResultSkippedAlreadyArchived:
		return snapshot.SkippedAlreadyArchived
	case StopResultSkippedBindingMissing:
		return snapshot.SkippedBindingMissing
	case StopResultSkippedNoThreadID:
		return snapshot.SkippedNoThreadID
	case StopResultSkippedLookupFailed:
		return snapshot.SkippedLookupFailed
	case StopResultFailed:
		return snapshot.Failed
	default:
		return 0
	}
}

// --- tests ---------------------------------------------------------------

func TestStopSpawnedAgent_Success(t *testing.T) {
	counter := newStopSpawnedAgentCounter()
	before := StopSpawnedAgentCounters(counter)
	threads := &stopHelperThreadSpy{
		thread: &PersistedThread{ThreadID: "thr-1", AgentID: "agent-1"},
	}
	svc := &stopHelperServiceSpy{stopErr: nil}

	result, err := StopSpawnedAgent(context.Background(), threads, svc, "thr-1", counter)
	if err != nil {
		t.Fatalf("Success branch must not surface error, got %v", err)
	}
	assertResult(t, result, StopResultSuccess)
	assertMetricDelta(t, before, counter, StopResultSuccess)
	if svc.calls != 1 || svc.lastAgentID != "agent-1" {
		t.Fatalf("StopAgent calls=%d lastAgentID=%q, want 1 / agent-1",
			svc.calls, svc.lastAgentID)
	}
}

func TestStopSpawnedAgent_SkippedAlreadyStopped(t *testing.T) {
	counter := newStopSpawnedAgentCounter()
	before := StopSpawnedAgentCounters(counter)
	threads := &stopHelperThreadSpy{
		thread: &PersistedThread{ThreadID: "thr-1", AgentID: "agent-1"},
	}
	// Matches helpers.go / service_launcher_bridge.go sentinel wrapping.
	svc := &stopHelperServiceSpy{
		stopErr: fmt.Errorf("%w: agent %q is not running", errAgentNotRunningForStopper, "agent-1"),
	}

	result, err := StopSpawnedAgent(context.Background(), threads, svc, "thr-1", counter)
	if err != nil {
		t.Fatalf("skipped_already_stopped must not surface error, got %v", err)
	}
	assertResult(t, result, StopResultSkippedAlreadyStopped)
	assertMetricDelta(t, before, counter, StopResultSkippedAlreadyStopped)
}

func TestStopSpawnedAgent_SkippedStopping(t *testing.T) {
	counter := newStopSpawnedAgentCounter()
	before := StopSpawnedAgentCounters(counter)
	threads := &stopHelperThreadSpy{
		thread: &PersistedThread{ThreadID: "thr-1", AgentID: "agent-1"},
	}
	// Matches helpers.go / service_launcher_bridge.go sentinel wrapping.
	svc := &stopHelperServiceSpy{
		stopErr: fmt.Errorf("%w: agent %q is stopping", errAgentStoppingForStopper, "agent-1"),
	}

	result, err := StopSpawnedAgent(context.Background(), threads, svc, "thr-1", counter)
	if err != nil {
		t.Fatalf("is-stopping branch must not surface error, got %v", err)
	}
	assertResult(t, result, StopResultSkippedAlreadyStopped)
	assertMetricDelta(t, before, counter, StopResultSkippedAlreadyStopped)
}

func TestStopSpawnedAgent_SkippedAlreadyArchived(t *testing.T) {
	counter := newStopSpawnedAgentCounter()
	before := StopSpawnedAgentCounters(counter)
	threads := &stopHelperThreadSpy{
		thread: &PersistedThread{ThreadID: "thr-1", AgentID: "agent-1"},
	}
	// service.StopAgent surfaces fmt.Errorf("%w: ...", errAgentNotFound)
	// — wrap so errors.Is matches.
	svc := &stopHelperServiceSpy{
		stopErr: fmt.Errorf("wrapped: %w", errAgentNotFound),
	}

	result, err := StopSpawnedAgent(context.Background(), threads, svc, "thr-1", counter)
	if err != nil {
		t.Fatalf("already_archived must not surface error, got %v", err)
	}
	assertResult(t, result, StopResultSkippedAlreadyArchived)
	assertMetricDelta(t, before, counter, StopResultSkippedAlreadyArchived)
}

func TestStopSpawnedAgent_MarksPersistedThreadStoppedWhenRuntimeAlreadyGone(t *testing.T) {
	counter := newStopSpawnedAgentCounter()
	before := StopSpawnedAgentCounters(counter)
	threads := &stopHelperThreadSpy{
		thread: &PersistedThread{ThreadID: "thr-1", AgentID: "agent-1", Status: "created"},
	}
	svc := &stopHelperServiceSpy{
		stopErr: fmt.Errorf("wrapped: %w", errAgentNotFound),
	}

	result, err := StopSpawnedAgent(context.Background(), threads, svc, "thr-1", counter)
	if err != nil {
		t.Fatalf("already-gone runtime must not surface error, got %v", err)
	}
	assertResult(t, result, StopResultSkippedAlreadyArchived)
	assertMetricDelta(t, before, counter, StopResultSkippedAlreadyArchived)
	if threads.updateCalls != 1 {
		t.Fatalf("UpdateStatus calls = %d, want 1", threads.updateCalls)
	}
	if threads.update.ThreadID != "thr-1" || threads.update.Status != "stopped" || threads.update.UpdatedAt <= 0 {
		t.Fatalf("UpdateStatus = %#v, want thread stopped", threads.update)
	}
}

func TestStopSpawnedAgent_SkippedBindingMissing(t *testing.T) {
	counter := newStopSpawnedAgentCounter()
	before := StopSpawnedAgentCounters(counter)
	// Thread exists but derived AgentID is "" — binding missing/archived.
	threads := &stopHelperThreadSpy{
		thread: &PersistedThread{ThreadID: "thr-1", AgentID: ""},
	}
	svc := &stopHelperServiceSpy{}

	result, err := StopSpawnedAgent(context.Background(), threads, svc, "thr-1", counter)
	if err != nil {
		t.Fatalf("binding_missing must not surface error, got %v", err)
	}
	assertResult(t, result, StopResultSkippedBindingMissing)
	assertMetricDelta(t, before, counter, StopResultSkippedBindingMissing)
	if svc.calls != 0 {
		t.Fatalf("StopAgent should not be called when AgentID is empty, got %d calls",
			svc.calls)
	}
}

func TestStopSpawnedAgent_SkippedNoThreadID_EmptyInput(t *testing.T) {
	counter := newStopSpawnedAgentCounter()
	before := StopSpawnedAgentCounters(counter)
	threads := &stopHelperThreadSpy{}
	svc := &stopHelperServiceSpy{}

	result, err := StopSpawnedAgent(context.Background(), threads, svc, "   ", counter)
	if err != nil {
		t.Fatalf("empty threadID must not surface error, got %v", err)
	}
	assertResult(t, result, StopResultSkippedNoThreadID)
	assertMetricDelta(t, before, counter, StopResultSkippedNoThreadID)
	if threads.calls != 0 {
		t.Fatalf("lookup should be skipped for empty threadID, got %d calls",
			threads.calls)
	}
}

func TestStopSpawnedAgent_SkippedNoThreadID_NilRow(t *testing.T) {
	counter := newStopSpawnedAgentCounter()
	before := StopSpawnedAgentCounters(counter)
	// thread store returns (nil, nil) — row absent without error sentinel.
	threads := &stopHelperThreadSpy{thread: nil, err: nil}
	svc := &stopHelperServiceSpy{}

	result, err := StopSpawnedAgent(context.Background(), threads, svc, "thr-missing", counter)
	if err != nil {
		t.Fatalf("nil-row no_thread_id must not surface error, got %v", err)
	}
	assertResult(t, result, StopResultSkippedNoThreadID)
	assertMetricDelta(t, before, counter, StopResultSkippedNoThreadID)
	if svc.calls != 0 {
		t.Fatalf("StopAgent should not be called when row absent, got %d calls",
			svc.calls)
	}
}

func TestStopSpawnedAgent_SkippedNoThreadID_NotFoundSentinel(t *testing.T) {
	counter := newStopSpawnedAgentCounter()
	before := StopSpawnedAgentCounters(counter)
	// store returns errAgentNotFound (archive_test.go:208 pattern).
	threads := &stopHelperThreadSpy{thread: nil, err: errAgentNotFound}
	svc := &stopHelperServiceSpy{}

	result, err := StopSpawnedAgent(context.Background(), threads, svc, "thr-missing", counter)
	if !errors.Is(err, errAgentNotFound) {
		t.Fatalf("StopSpawnedAgent error = %v, want agent not found", err)
	}
	assertResult(t, result, StopResultSkippedNoThreadID)
	assertMetricDelta(t, before, counter, StopResultSkippedNoThreadID)
}

func TestStopSpawnedAgent_SkippedLookupFailed(t *testing.T) {
	counter := newStopSpawnedAgentCounter()
	before := StopSpawnedAgentCounters(counter)
	lookupErr := errors.New("rpc broken pipe")
	threads := &stopHelperThreadSpy{err: lookupErr}
	svc := &stopHelperServiceSpy{}

	result, err := StopSpawnedAgent(context.Background(), threads, svc, "thr-1", counter)
	if !errors.Is(err, lookupErr) {
		t.Fatalf("lookup_failed must surface original error for log, got %v", err)
	}
	assertResult(t, result, StopResultSkippedLookupFailed)
	assertMetricDelta(t, before, counter, StopResultSkippedLookupFailed)
	if svc.calls != 0 {
		t.Fatalf("StopAgent should not be called after lookup failed, got %d calls",
			svc.calls)
	}
}

func TestStopSpawnedAgent_Failed(t *testing.T) {
	counter := newStopSpawnedAgentCounter()
	before := StopSpawnedAgentCounters(counter)
	threads := &stopHelperThreadSpy{
		thread: &PersistedThread{ThreadID: "thr-1", AgentID: "agent-1"},
	}
	stopErr := errors.New("transport closed")
	svc := &stopHelperServiceSpy{stopErr: stopErr}

	result, err := StopSpawnedAgent(context.Background(), threads, svc, "thr-1", counter)
	if !errors.Is(err, stopErr) {
		t.Fatalf("failed must surface original error for log, got %v", err)
	}
	assertResult(t, result, StopResultFailed)
	assertMetricDelta(t, before, counter, StopResultFailed)
}

// --- counter integration sanity ------------------------------------------

// TestStopSpawnedAgent_ExplicitCounterIncrements proves the caller-owned
// counter receives Inc() calls without package-global runtime state.
func TestStopSpawnedAgent_ExplicitCounterIncrements(t *testing.T) {
	counter := newStopSpawnedAgentCounter()
	before := counter.Snapshot()

	threads := &stopHelperThreadSpy{
		thread: &PersistedThread{ThreadID: "thr-1", AgentID: "agent-1"},
	}
	svc := &stopHelperServiceSpy{}
	result, err := StopSpawnedAgent(context.Background(), threads, svc, "thr-1", counter)
	if err != nil {
		t.Fatalf("Success branch must not surface error, got %v", err)
	}
	if result != StopResultSuccess {
		t.Fatalf("result = %q, want success", result)
	}

	after := counter.Snapshot()
	if after.Success != before.Success+1 {
		t.Fatalf("default counter Success delta = %d, want 1",
			after.Success-before.Success)
	}
}
