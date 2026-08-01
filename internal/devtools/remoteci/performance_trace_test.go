package remoteci

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestTextPhaseObserverWritesStablePlaintext(t *testing.T) {
	var output bytes.Buffer
	observer, err := NewTextPhaseObserver(&output)
	if err != nil {
		t.Fatal(err)
	}
	startedAt := time.Date(2026, 7, 31, 1, 2, 3, 0, time.UTC)
	events := []PhaseEvent{
		{
			JobID: "job-test", Kind: PhaseEventStart, Phase: "cache.parent_exact",
			ObservedAt: startedAt, Outcome: remoteCIPhaseOutcomeRunning,
			WorkloadCount: 8, ShardCount: 2, CacheHitCount: 7, CacheMissCount: 1,
		},
		{
			JobID: "job-test", Kind: PhaseEventHeartbeat, Phase: "cache.parent_exact",
			ObservedAt: startedAt.Add(10 * time.Second), ElapsedMillis: 10_000,
			Outcome:       remoteCIPhaseOutcomeRunning,
			WorkloadCount: 8, ShardCount: 2, CacheHitCount: 7, CacheMissCount: 1,
		},
		{
			JobID: "job-test", Kind: PhaseEventFinish, Phase: "cache.parent_exact",
			ObservedAt: startedAt.Add(23 * time.Second), ElapsedMillis: 23_000,
			Outcome:       gate.RemoteCIPhaseOutcomeSucceeded,
			WorkloadCount: 8, ShardCount: 2, CacheHitCount: 7, CacheMissCount: 1,
		},
	}
	for _, event := range events {
		if err := observer.ObserveRemoteCIPhase(event); err != nil {
			t.Fatal(err)
		}
	}
	want := "" +
		"SUPER_DOLPHIN_CI_PHASE event=start job_id=job-test phase=cache.parent_exact observed_at=2026-07-31T01:02:03Z elapsed_ms=0 outcome=running workloads=8 shards=2 cache_hits=7 cache_misses=1\n" +
		"SUPER_DOLPHIN_CI_PHASE event=heartbeat job_id=job-test phase=cache.parent_exact observed_at=2026-07-31T01:02:13Z elapsed_ms=10000 outcome=running workloads=8 shards=2 cache_hits=7 cache_misses=1\n" +
		"SUPER_DOLPHIN_CI_PHASE event=finish job_id=job-test phase=cache.parent_exact observed_at=2026-07-31T01:02:26Z duration_ms=23000 outcome=succeeded workloads=8 shards=2 cache_hits=7 cache_misses=1\n"
	if output.String() != want {
		t.Fatalf("phase log = %q, want %q", output.String(), want)
	}
}

func TestTextPhaseObserverRejectsMalformedEvent(t *testing.T) {
	var output bytes.Buffer
	observer, err := NewTextPhaseObserver(&output)
	if err != nil {
		t.Fatal(err)
	}
	err = observer.ObserveRemoteCIPhase(PhaseEvent{
		JobID: "job-test", Kind: PhaseEventHeartbeat, Phase: "eci.wait",
		ObservedAt: time.Now(), ElapsedMillis: -1, Outcome: remoteCIPhaseOutcomeRunning,
	})
	if err == nil || !strings.Contains(err.Error(), "cannot be negative") {
		t.Fatalf("malformed event error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("malformed event wrote %q", output.String())
	}
}

func TestRemoteRunPerformanceTraceRecordsFailureAndObserverError(t *testing.T) {
	startedAt := time.Unix(10, 0)
	times := []time.Time{startedAt, startedAt.Add(45 * time.Millisecond)}
	nextTime := func() time.Time {
		value := times[0]
		times = times[1:]
		return value
	}
	trace := newRemoteRunPerformanceTrace(
		"job-test",
		nextTime,
		phaseObserverFunc(func(PhaseEvent) error { return errors.New("log unavailable") }),
	)
	span := trace.start("eci.wait", remoteCIPhaseCounts{workloads: 3, shards: 2})
	trace.finish(span, errors.New("worker failed"), remoteCIPhaseCounts{
		workloads: 3, shards: 2,
	})
	timings := trace.snapshot()
	if len(timings) != 1 {
		t.Fatalf("phase timing count = %d, want 1", len(timings))
	}
	if timings[0].DurationMillis != 45 || timings[0].Outcome != gate.RemoteCIPhaseOutcomeFailed {
		t.Fatalf("phase timing = %#v", timings[0])
	}
	if err := trace.observerError(); err == nil || !strings.Contains(err.Error(), "log unavailable") {
		t.Fatalf("observer error = %v", err)
	}
}

func TestRemoteRunPerformanceTraceEmitsHeartbeatUntilFinish(t *testing.T) {
	events := make(chan PhaseEvent, 16)
	trace := newRemoteRunPerformanceTrace(
		"job-heartbeat",
		time.Now,
		phaseObserverFunc(func(event PhaseEvent) error {
			events <- event
			return nil
		}),
	)
	trace.heartbeatInterval = 5 * time.Millisecond
	span := trace.start("eci.wait", remoteCIPhaseCounts{workloads: 4, shards: 1})

	startEvent := <-events
	if startEvent.Kind != PhaseEventStart || startEvent.Phase != "eci.wait" {
		t.Fatalf("start event = %#v", startEvent)
	}
	var heartbeat PhaseEvent
	select {
	case heartbeat = <-events:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("heartbeat was not emitted")
	}
	if heartbeat.Kind != PhaseEventHeartbeat || heartbeat.ElapsedMillis <= 0 {
		t.Fatalf("heartbeat event = %#v", heartbeat)
	}

	trace.finish(span, nil, remoteCIPhaseCounts{workloads: 4, shards: 1})
	var finishEvent PhaseEvent
	for finishEvent.Kind != PhaseEventFinish {
		select {
		case finishEvent = <-events:
		case <-time.After(200 * time.Millisecond):
			t.Fatal("finish event was not emitted")
		}
	}
	if finishEvent.Outcome != gate.RemoteCIPhaseOutcomeSucceeded {
		t.Fatalf("finish event = %#v", finishEvent)
	}
	select {
	case event := <-events:
		t.Fatalf("event emitted after finish: %#v", event)
	case <-time.After(20 * time.Millisecond):
	}
}

type phaseObserverFunc func(PhaseEvent) error

func (observe phaseObserverFunc) ObserveRemoteCIPhase(event PhaseEvent) error {
	return observe(event)
}
