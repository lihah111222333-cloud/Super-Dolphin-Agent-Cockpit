package exitmonitor

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

func newTestMonitor(t *testing.T) *Monitor {
	t.Helper()
	return New(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func fillEventBuffer(m *Monitor) {
	for i := 0; i < cap(m.events); i++ {
		m.events <- Event{AgentID: "prefill", LaunchSeq: uint64(i + 1)}
	}
}

func TestPublishExitFullBufferTimeoutCanRetry(t *testing.T) {
	m := newTestMonitor(t)
	m.publishBlockTimeout = 5 * time.Millisecond
	fillEventBuffer(m)

	const (
		agentID = "retry-agent"
		seq     = uint64(11)
	)
	m.Emit(agentID, seq, errors.New("first attempt"))

	key := exitMonitorFenceKey(agentID, seq)
	m.mu.Lock()
	_, fired := m.fired[key]
	m.mu.Unlock()
	if fired {
		t.Fatalf("timed-out event was marked fired before channel enqueue")
	}

	<-m.events
	m.Emit(agentID, seq, errors.New("retry attempt"))
	found := false
	for i := 0; i < cap(m.events); i++ {
		got := <-m.events
		if got.AgentID != agentID || got.LaunchSeq != seq {
			continue
		}
		if found {
			t.Fatalf("retried event was delivered more than once: %+v", got)
		}
		found = true
	}
	if !found {
		t.Fatalf("retry did not reach channel")
	}

	before := len(m.events)
	m.Emit(agentID, seq, errors.New("duplicate"))
	if got := len(m.events); got != before {
		t.Fatalf("duplicate event changed channel length from %d to %d", before, got)
	}
	m.mu.Lock()
	_, fired = m.fired[key]
	m.mu.Unlock()
	if !fired {
		t.Fatalf("successful retry did not commit fired fence")
	}
}

func TestPublishExitConcurrentSameFenceExactlyOnce(t *testing.T) {
	m := newTestMonitor(t)
	const (
		agentID  = "concurrent-agent"
		seq      = uint64(23)
		attempts = 64
	)

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Go(func() {
			<-start
			m.Emit(agentID, seq, nil)
		})
	}
	close(start)
	wg.Wait()

	count := 0
	for {
		select {
		case got := <-m.events:
			if got.AgentID == agentID && got.LaunchSeq == seq {
				count++
			}
		default:
			if count != 1 {
				t.Fatalf("concurrent publish count = %d, want exactly one", count)
			}
			return
		}
	}
}

func TestPublishExitAfterCancelledDrainStillDeduplicates(t *testing.T) {
	m := newTestMonitor(t)
	// Keep Drain's waiter pending so an already-cancelled context cannot race
	// with the zero-goroutine fast path and make the assertion nondeterministic.
	m.wg.Add(1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.Drain(ctx); !errors.Is(err, context.Canceled) {
		m.wg.Done()
		t.Fatalf("Drain(err) = %v, want context.Canceled", err)
	}
	m.wg.Done()

	m.Emit("closed-agent", 31, nil)
	m.Emit("closed-agent", 31, errors.New("duplicate"))
	select {
	case got := <-m.events:
		if got.AgentID != "closed-agent" || got.LaunchSeq != 31 {
			t.Fatalf("closed monitor event = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("Emit after cancelled Drain did not reach channel")
	}
	select {
	case got := <-m.events:
		t.Fatalf("duplicate after cancelled Drain reached channel: %+v", got)
	default:
	}
}
