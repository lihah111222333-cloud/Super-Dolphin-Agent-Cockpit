package remoteci

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const (
	remoteCIPhaseLogPrefix                                          = "SUPER_DOLPHIN_CI_PHASE"
	defaultRemoteCIPhaseHeartbeatInterval                           = 10 * time.Second
	remoteCIPhaseOutcomeRunning           gate.RemoteCIPhaseOutcome = "running"
)

// PhaseEventKind 区分阶段开始、存活心跳和最终完成事件。
type PhaseEventKind string

const (
	PhaseEventStart     PhaseEventKind = "start"
	PhaseEventHeartbeat PhaseEventKind = "heartbeat"
	PhaseEventFinish    PhaseEventKind = "finish"
)

// PhaseEvent 把一次实时阶段事件绑定到不可重复的远程作业身份。
type PhaseEvent struct {
	JobID          string
	Kind           PhaseEventKind
	Phase          string
	ObservedAt     time.Time
	ElapsedMillis  int64
	Outcome        gate.RemoteCIPhaseOutcome
	WorkloadCount  int
	ShardCount     int
	CacheHitCount  int
	CacheMissCount int
}

// PhaseObserver 接收协调器实时阶段事件；持久化仍由运行账本负责。
type PhaseObserver interface {
	ObserveRemoteCIPhase(PhaseEvent) error
}

// TextPhaseObserver 以稳定的普通文本格式逐行输出性能事件。
type TextPhaseObserver struct {
	writer io.Writer
	mutex  sync.Mutex
}

// NewTextPhaseObserver 构造不会输出 JSON 或二进制编码的阶段日志观察器。
func NewTextPhaseObserver(writer io.Writer) (*TextPhaseObserver, error) {
	if writer == nil {
		return nil, errors.New("remote CI phase log writer is required")
	}
	return &TextPhaseObserver{writer: writer}, nil
}

// ObserveRemoteCIPhase 原子输出一条便于人和日志采集器读取的阶段记录。
func (observer *TextPhaseObserver) ObserveRemoteCIPhase(event PhaseEvent) error {
	if observer == nil || observer.writer == nil {
		return errors.New("remote CI text phase observer is not initialized")
	}
	if err := validateRemoteCIPhaseEvent(event); err != nil {
		return err
	}
	observer.mutex.Lock()
	defer observer.mutex.Unlock()
	durationField := "elapsed_ms"
	if event.Kind == PhaseEventFinish {
		durationField = "duration_ms"
	}
	_, err := fmt.Fprintf(
		observer.writer,
		"%s event=%s job_id=%s phase=%s observed_at=%s %s=%d outcome=%s workloads=%d shards=%d cache_hits=%d cache_misses=%d\n",
		remoteCIPhaseLogPrefix,
		event.Kind,
		event.JobID,
		event.Phase,
		event.ObservedAt.UTC().Format(time.RFC3339Nano),
		durationField,
		event.ElapsedMillis,
		event.Outcome,
		event.WorkloadCount,
		event.ShardCount,
		event.CacheHitCount,
		event.CacheMissCount,
	)
	if err != nil {
		return fmt.Errorf("write remote CI phase log: %w", err)
	}
	return nil
}

func validateRemoteCIPhaseEvent(event PhaseEvent) error {
	if event.JobID == "" {
		return errors.New("remote CI phase event job identity is required")
	}
	if event.Phase == "" {
		return errors.New("remote CI phase event name is required")
	}
	if event.ObservedAt.IsZero() {
		return errors.New("remote CI phase event observation time is required")
	}
	if event.ElapsedMillis < 0 ||
		event.WorkloadCount < 0 ||
		event.ShardCount < 0 ||
		event.CacheHitCount < 0 ||
		event.CacheMissCount < 0 {
		return errors.New("remote CI phase event counters cannot be negative")
	}
	switch event.Kind {
	case PhaseEventStart, PhaseEventHeartbeat:
		if event.Outcome != remoteCIPhaseOutcomeRunning {
			return fmt.Errorf("remote CI %s event outcome must be %q", event.Kind, remoteCIPhaseOutcomeRunning)
		}
	case PhaseEventFinish:
		if event.Outcome != gate.RemoteCIPhaseOutcomeSucceeded &&
			event.Outcome != gate.RemoteCIPhaseOutcomeFailed {
			return fmt.Errorf("remote CI finish event has unsupported outcome %q", event.Outcome)
		}
	default:
		return fmt.Errorf("unsupported remote CI phase event kind %q", event.Kind)
	}
	return nil
}

type remoteCIPhaseCounts struct {
	workloads   int
	shards      int
	cacheHits   int
	cacheMisses int
}

type remoteCIPhaseSpan struct {
	phase         string
	startedAt     time.Time
	initialCounts remoteCIPhaseCounts
	stopHeartbeat chan struct{}
	heartbeatDone chan struct{}
	finishOnce    sync.Once
}

type remoteRunPerformanceTrace struct {
	jobID             string
	now               func() time.Time
	observer          PhaseObserver
	heartbeatInterval time.Duration
	mutex             sync.Mutex
	timings           []gate.RemoteCIPhaseTiming
	err               error
}

func newRemoteRunPerformanceTrace(
	jobID string,
	now func() time.Time,
	observer PhaseObserver,
) *remoteRunPerformanceTrace {
	return &remoteRunPerformanceTrace{
		jobID:             jobID,
		now:               now,
		observer:          observer,
		heartbeatInterval: defaultRemoteCIPhaseHeartbeatInterval,
	}
}

func (trace *remoteRunPerformanceTrace) start(
	phase string,
	counts remoteCIPhaseCounts,
) *remoteCIPhaseSpan {
	if trace == nil {
		return nil
	}
	startedAt := trace.now()
	span := &remoteCIPhaseSpan{
		phase:         phase,
		startedAt:     startedAt,
		initialCounts: counts,
	}
	trace.observe(trace.phaseEvent(PhaseEventStart, phase, startedAt, startedAt, remoteCIPhaseOutcomeRunning, counts))
	if trace.observer != nil {
		span.stopHeartbeat = make(chan struct{})
		span.heartbeatDone = make(chan struct{})
		go trace.emitHeartbeats(span)
	}
	return span
}

func (trace *remoteRunPerformanceTrace) finish(
	span *remoteCIPhaseSpan,
	phaseErr error,
	counts remoteCIPhaseCounts,
) {
	if trace == nil || span == nil {
		return
	}
	span.finishOnce.Do(func() {
		if span.stopHeartbeat != nil {
			close(span.stopHeartbeat)
			<-span.heartbeatDone
		}
		completedAt := trace.now()
		duration := max(completedAt.Sub(span.startedAt), 0)
		outcome := gate.RemoteCIPhaseOutcomeSucceeded
		if phaseErr != nil {
			outcome = gate.RemoteCIPhaseOutcomeFailed
		}
		trace.record(gate.RemoteCIPhaseTiming{
			Phase:          span.phase,
			StartedAt:      span.startedAt.UTC(),
			DurationMillis: duration.Milliseconds(),
			Outcome:        outcome,
			WorkloadCount:  counts.workloads,
			ShardCount:     counts.shards,
			CacheHitCount:  counts.cacheHits,
			CacheMissCount: counts.cacheMisses,
		})
	})
}

func (trace *remoteRunPerformanceTrace) record(timing gate.RemoteCIPhaseTiming) {
	if trace == nil {
		return
	}
	trace.mutex.Lock()
	trace.timings = append(trace.timings, timing)
	trace.mutex.Unlock()
	counts := remoteCIPhaseCounts{
		workloads:   timing.WorkloadCount,
		shards:      timing.ShardCount,
		cacheHits:   timing.CacheHitCount,
		cacheMisses: timing.CacheMissCount,
	}
	trace.observe(trace.phaseEvent(
		PhaseEventFinish,
		timing.Phase,
		timing.StartedAt,
		timing.StartedAt.Add(time.Duration(timing.DurationMillis)*time.Millisecond),
		timing.Outcome,
		counts,
	))
}

func (trace *remoteRunPerformanceTrace) emitHeartbeats(span *remoteCIPhaseSpan) {
	defer close(span.heartbeatDone)
	ticker := time.NewTicker(trace.heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-span.stopHeartbeat:
			return
		case <-ticker.C:
			observedAt := trace.now()
			trace.observe(trace.phaseEvent(
				PhaseEventHeartbeat,
				span.phase,
				span.startedAt,
				observedAt,
				remoteCIPhaseOutcomeRunning,
				span.initialCounts,
			))
		}
	}
}

func (trace *remoteRunPerformanceTrace) phaseEvent(
	kind PhaseEventKind,
	phase string,
	startedAt time.Time,
	observedAt time.Time,
	outcome gate.RemoteCIPhaseOutcome,
	counts remoteCIPhaseCounts,
) PhaseEvent {
	return PhaseEvent{
		JobID:          trace.jobID,
		Kind:           kind,
		Phase:          phase,
		ObservedAt:     observedAt.UTC(),
		ElapsedMillis:  max(observedAt.Sub(startedAt), 0).Milliseconds(),
		Outcome:        outcome,
		WorkloadCount:  counts.workloads,
		ShardCount:     counts.shards,
		CacheHitCount:  counts.cacheHits,
		CacheMissCount: counts.cacheMisses,
	}
}

func (trace *remoteRunPerformanceTrace) observe(event PhaseEvent) {
	if trace == nil || trace.observer == nil {
		return
	}
	err := trace.observer.ObserveRemoteCIPhase(event)
	if err == nil {
		return
	}
	trace.mutex.Lock()
	defer trace.mutex.Unlock()
	trace.err = errors.Join(trace.err, err)
}

func (trace *remoteRunPerformanceTrace) snapshot() []gate.RemoteCIPhaseTiming {
	if trace == nil {
		return nil
	}
	trace.mutex.Lock()
	defer trace.mutex.Unlock()
	return append([]gate.RemoteCIPhaseTiming(nil), trace.timings...)
}

func (trace *remoteRunPerformanceTrace) observerError() error {
	if trace == nil {
		return nil
	}
	trace.mutex.Lock()
	defer trace.mutex.Unlock()
	return trace.err
}
