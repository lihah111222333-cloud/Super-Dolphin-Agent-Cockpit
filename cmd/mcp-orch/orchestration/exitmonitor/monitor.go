package exitmonitor

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"sync"
	"time"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// Monitor is the single owner of `cmd.Wait()` for every locally
// launched orchestration process, introduced by P22 P3 to replace the
// fire-and-forget `go a.waitForExit(...)` that used to live in
// runnerActor.Run (Finding 8).
//
// Responsibilities:
//   - Arm: start a goroutine that runs cmd.Wait() for one (agentID, launchSeq,
//     cmd) triple. Goroutines are tracked in a WaitGroup so Drain can join
//     them at shutdown.
//   - Emit: synthetic-exit entry used by launcher-driven stops that bypass
//     cmd.Wait (e.g. remote AgentLauncher.Stop — no local cmd, so no Wait).
//     Emit shares the same (agentID, launchSeq) fence as Arm.
//   - ExitEvents: read-only channel the runnerActor consumes on every Run
//     iteration. Exactly one event per (agentID, launchSeq) ever reaches
//     consumers, so handleProcessExit is safe to treat as exactly-once.
//   - Drain: closes the gate (no new Arm accepted), waits for every
//     in-flight cmd.Wait to finish. Called by runnerActor.drainOnStop after
//     ctx is cancelled. bounded by the caller's ctx.
type Monitor struct {
	logger *slog.Logger
	events chan Event

	mu     sync.Mutex
	wg     sync.WaitGroup
	closed bool
	fired  map[string]struct{}

	// publishBlockTimeout bounds the worst-case `events <- ...` backpressure
	// before the monitor logs a drop. Tuned so a stuck actor is visible in
	// logs rather than pinning every wait goroutine.
	publishBlockTimeout time.Duration
}

type Target struct {
	AgentID   string
	LaunchSeq uint64
	Cmd       *exec.Cmd
}

type Event struct {
	AgentID   string
	LaunchSeq uint64
	Err       error
}

func New(logger *slog.Logger) *Monitor {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &Monitor{
		logger:              logger,
		events:              make(chan Event, 32),
		fired:               make(map[string]struct{}),
		publishBlockTimeout: 5 * time.Second,
	}
}

// Arm starts a cmd.Wait goroutine for target. Returns false if the monitor
// has already been closed by Drain — callers in that window must NOT assume
// the process will emit an exit event via the monitor; they are responsible
// for synchronous cleanup.
func (m *Monitor) Arm(target Target) bool {
	if target.Cmd == nil {
		return false
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return false
	}
	m.wg.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.wg.Done()
		err := target.Cmd.Wait()
		m.publishExit(target.AgentID, target.LaunchSeq, err)
	}()
	return true
}

// Emit is the synthetic-exit path for launcher-driven stops that do NOT have
// a local cmd to Wait on (e.g. remote launcher.Stop succeeded). Shares the
// exactly-once fence with Arm so accidental double-emit is a no-op.
func (m *Monitor) Emit(agentID string, launchSeq uint64, err error) {
	m.publishExit(agentID, launchSeq, err)
}

// ExitEvents returns the read-only event stream. runnerActor.Run is the
// only production consumer.
func (m *Monitor) ExitEvents() <-chan Event { return m.events }

// Drain closes the gate (no more Arm) and blocks until every in-flight
// cmd.Wait goroutine has finished. Callers pass a bounded ctx for the
// shutdown budget.
func (m *Monitor) Drain(ctx context.Context) error {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// publishExit enforces the exactly-once-per-(agentID, launchSeq) fence and
// pushes the event onto the buffered channel. It is safe for concurrent use;
// Arm goroutines, Emit callers, and tests all route through here.
func (m *Monitor) publishExit(agentID string, seq uint64, err error) {
	if agentID == "" || seq == 0 {
		return
	}
	if !m.claimFire(agentID, seq) {
		return
	}
	result := Event{AgentID: agentID, LaunchSeq: seq, Err: err}
	select {
	case m.events <- result:
		return
	default:
	}
	m.logger.Warn("orchestration: exit event buffer full; falling back to bounded block",
		"agent_id", agentID, "launch_seq", seq)
	timer := time.NewTimer(m.publishBlockTimeout)
	defer timer.Stop()
	select {
	case m.events <- result:
	case <-timer.C:
		m.logger.Error("orchestration: dropped exit event after publishBlockTimeout",
			"agent_id", agentID, "launch_seq", seq, "timeout", m.publishBlockTimeout)
	}
}

// claimFire attempts to reserve the (agentID, launchSeq) fence slot. Returns
// false if the slot was already claimed by an earlier publish.
func (m *Monitor) claimFire(agentID string, seq uint64) bool {
	key := exitMonitorFenceKey(agentID, seq)
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, done := m.fired[key]; done {
		return false
	}
	m.fired[key] = struct{}{}
	return true
}

func exitMonitorFenceKey(agentID string, seq uint64) string {
	return fmt.Sprintf("%s#%d", agentID, seq)
}
