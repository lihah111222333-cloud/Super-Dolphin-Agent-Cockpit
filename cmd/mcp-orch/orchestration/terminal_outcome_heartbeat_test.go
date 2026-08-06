package orchestration

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/taskdag"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestOutboxProjectorHeartbeatPreventsConcurrentReclaimDuringSlowDAGProjection(t *testing.T) {
	svc, db := newTerminalOutcomeTestService(t)
	agent := addTerminalOutcomeTestAgent(svc)
	commit, err := terminalOutcomeCommitFromEvent(agent, terminalOutcomeEvent(true))
	if err != nil {
		t.Fatalf("terminalOutcomeCommitFromEvent() error = %v", err)
	}
	if _, err := svc.terminalOutcomes.CommitTerminalOutcome(context.Background(), commit); err != nil {
		t.Fatalf("CommitTerminalOutcome() error = %v", err)
	}
	lookup := &blockingTerminalOutcomeLookup{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
		nodes:   []taskdag.Node{{DagKey: "dag-heartbeat", NodeKey: "node-heartbeat", Status: "running"}},
	}
	svc.terminalDAG = &DAGSubscriberDeps{
		LookupStore: lookup,
		FlowStore:   &dagSubscriberFlowSpy{},
		EventBus:    svc.eventBus,
	}
	const lease = 500 * time.Millisecond
	projected := make(chan error, 1)
	go func() {
		projected <- svc.ProcessTerminalOutcomeOutbox(context.Background(), "worker-heartbeat-a", lease, 1)
	}()
	select {
	case <-lookup.started:
	case <-time.After(time.Second):
		t.Fatal("slow DAG projection did not start")
	}
	time.Sleep(2*lease + lease/2)
	reclaimed, err := svc.terminalOutcomes.ClaimTerminalOutcomeOutbox(context.Background(), "worker-heartbeat-b", lease, 1)
	if err != nil {
		t.Fatalf("worker B claim: %v", err)
	}
	if len(reclaimed) != 0 {
		t.Fatalf("worker B reclaimed live item = %#v", reclaimed)
	}
	close(lookup.release)
	if err := <-projected; err != nil {
		t.Fatalf("worker A projection: %v", err)
	}
	var status string
	if err := db.QueryRow("SELECT status FROM terminal_outcome_outbox_v2").Scan(&status); err != nil {
		t.Fatalf("read projected outbox: %v", err)
	}
	if status != "projected" {
		t.Fatalf("outbox status = %q, want projected", status)
	}
}

func TestOutboxProjectorHeartbeatRenewFailureCancelsWorkWithoutAck(t *testing.T) {
	svc, db := newTerminalOutcomeTestService(t)
	const (
		workerID = "worker-heartbeat-renew-error"
		lease    = 30 * time.Millisecond
	)
	item := claimTerminalOutcomeHeartbeatTestItem(t, svc, workerID, lease)
	lookup := &blockingTerminalOutcomeLookup{started: make(chan struct{}, 1), release: make(chan struct{})}
	svc.terminalDAG = &DAGSubscriberDeps{LookupStore: lookup, FlowStore: &dagSubscriberFlowSpy{}, EventBus: svc.eventBus}

	renewErr := errors.New("heartbeat renew failed")
	probe := &terminalOutcomeHeartbeatPort{TerminalOutcomeCommitPort: svc.terminalOutcomes}
	probe.renew = func(ctx context.Context, outboxID int64, workerID, claimToken string, lease time.Duration) (time.Time, error) {
		if probe.renewCalls.Add(1) == 1 {
			return probe.TerminalOutcomeCommitPort.RenewTerminalOutcomeOutbox(ctx, outboxID, workerID, claimToken, lease)
		}
		return time.Time{}, renewErr
	}
	svc.terminalOutcomes = probe

	result := make(chan error, 1)
	go func() {
		result <- svc.processTerminalOutcomeOutboxItem(t.Context(), workerID, lease, item)
	}()
	waitForTerminalOutcomeHeartbeatWork(t, lookup.started)
	err := waitForTerminalOutcomeHeartbeatResult(t, result)
	if !errors.Is(err, context.Canceled) || !errors.Is(err, renewErr) {
		t.Fatalf("processTerminalOutcomeOutboxItem() error = %v, want joined cancellation and renew failure", err)
	}
	assertTerminalOutcomeHeartbeatNotAcked(t, probe, db)
}

func TestOutboxProjectorHeartbeatPanicCompletesWithoutAck(t *testing.T) {
	svc, db := newTerminalOutcomeTestService(t)
	const (
		workerID = "worker-heartbeat-panic"
		lease    = 30 * time.Millisecond
	)
	item := claimTerminalOutcomeHeartbeatTestItem(t, svc, workerID, lease)
	lookup := &blockingTerminalOutcomeLookup{started: make(chan struct{}, 1), release: make(chan struct{})}
	svc.terminalDAG = &DAGSubscriberDeps{LookupStore: lookup, FlowStore: &dagSubscriberFlowSpy{}, EventBus: svc.eventBus}

	probe := &terminalOutcomeHeartbeatPort{TerminalOutcomeCommitPort: svc.terminalOutcomes}
	probe.renew = func(ctx context.Context, outboxID int64, workerID, claimToken string, lease time.Duration) (time.Time, error) {
		if probe.renewCalls.Add(1) == 1 {
			return probe.TerminalOutcomeCommitPort.RenewTerminalOutcomeOutbox(ctx, outboxID, workerID, claimToken, lease)
		}
		panic("unstable heartbeat detail")
	}
	svc.terminalOutcomes = probe

	result := make(chan error, 1)
	go func() {
		result <- svc.processTerminalOutcomeOutboxItem(t.Context(), workerID, lease, item)
	}()
	waitForTerminalOutcomeHeartbeatWork(t, lookup.started)
	err := waitForTerminalOutcomeHeartbeatResult(t, result)
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "terminal outcome heartbeat panicked") {
		t.Fatalf("processTerminalOutcomeOutboxItem() error = %v, want joined cancellation and stable heartbeat panic error", err)
	}
	if strings.Contains(err.Error(), "unstable heartbeat detail") {
		t.Fatalf("processTerminalOutcomeOutboxItem() leaked panic detail: %v", err)
	}
	assertTerminalOutcomeHeartbeatNotAcked(t, probe, db)
}

func TestOutboxProjectorCallerCancellationJoinsWithoutAck(t *testing.T) {
	svc, db := newTerminalOutcomeTestService(t)
	const (
		workerID = "worker-heartbeat-caller-cancel"
		lease    = 300 * time.Millisecond
	)
	item := claimTerminalOutcomeHeartbeatTestItem(t, svc, workerID, lease)
	lookup := &blockingTerminalOutcomeLookup{started: make(chan struct{}, 1), release: make(chan struct{})}
	svc.terminalDAG = &DAGSubscriberDeps{LookupStore: lookup, FlowStore: &dagSubscriberFlowSpy{}, EventBus: svc.eventBus}
	probe := &terminalOutcomeHeartbeatPort{TerminalOutcomeCommitPort: svc.terminalOutcomes}
	svc.terminalOutcomes = probe

	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		result <- svc.processTerminalOutcomeOutboxItem(ctx, workerID, lease, item)
	}()
	waitForTerminalOutcomeHeartbeatWork(t, lookup.started)
	cancel()
	err := waitForTerminalOutcomeHeartbeatResult(t, result)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("processTerminalOutcomeOutboxItem() error = %v, want joined caller cancellation", err)
	}
	assertTerminalOutcomeHeartbeatNotAcked(t, probe, db)
}

type terminalOutcomeHeartbeatPort struct {
	contract.TerminalOutcomeCommitPort
	renew      func(context.Context, int64, string, string, time.Duration) (time.Time, error)
	renewCalls atomic.Int32
	ackCalls   atomic.Int32
}

func (p *terminalOutcomeHeartbeatPort) RenewTerminalOutcomeOutbox(ctx context.Context, outboxID int64, workerID, claimToken string, lease time.Duration) (time.Time, error) {
	if p.renew != nil {
		return p.renew(ctx, outboxID, workerID, claimToken, lease)
	}
	return p.TerminalOutcomeCommitPort.RenewTerminalOutcomeOutbox(ctx, outboxID, workerID, claimToken, lease)
}

func (p *terminalOutcomeHeartbeatPort) MarkTerminalOutcomeProjected(ctx context.Context, outboxID int64, workerID, claimToken string) error {
	p.ackCalls.Add(1)
	return p.TerminalOutcomeCommitPort.MarkTerminalOutcomeProjected(ctx, outboxID, workerID, claimToken)
}

func claimTerminalOutcomeHeartbeatTestItem(t *testing.T, svc *service, workerID string, lease time.Duration) contract.TerminalOutcomeOutboxItem {
	t.Helper()
	agent := addTerminalOutcomeTestAgent(svc)
	commit, err := terminalOutcomeCommitFromEvent(agent, terminalOutcomeEvent(true))
	if err != nil {
		t.Fatalf("terminalOutcomeCommitFromEvent() error = %v", err)
	}
	if _, err := svc.terminalOutcomes.CommitTerminalOutcome(t.Context(), commit); err != nil {
		t.Fatalf("CommitTerminalOutcome() error = %v", err)
	}
	items, err := svc.terminalOutcomes.ClaimTerminalOutcomeOutbox(t.Context(), workerID, lease, 1)
	if err != nil {
		t.Fatalf("ClaimTerminalOutcomeOutbox() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("ClaimTerminalOutcomeOutbox() items = %d, want 1", len(items))
	}
	return items[0]
}

func waitForTerminalOutcomeHeartbeatWork(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("terminal outcome projection work did not start")
	}
}

func waitForTerminalOutcomeHeartbeatResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(time.Second):
		t.Fatal("terminal outcome heartbeat did not complete")
		return nil
	}
}

func assertTerminalOutcomeHeartbeatNotAcked(t *testing.T, probe *terminalOutcomeHeartbeatPort, db *sql.DB) {
	t.Helper()
	if got := probe.ackCalls.Load(); got != 0 {
		t.Fatalf("MarkTerminalOutcomeProjected() calls = %d, want 0", got)
	}
	assertTerminalOutboxStatus(t, db, "claimed")
}

func assertTerminalOutboxStatus(t *testing.T, db *sql.DB, want string) {
	t.Helper()
	var status string
	if err := db.QueryRow("SELECT status FROM terminal_outcome_outbox_v2").Scan(&status); err != nil {
		t.Fatalf("query outbox status: %v", err)
	}
	if status != want {
		t.Fatalf("outbox status = %q, want %s", status, want)
	}
}

type blockingTerminalOutcomeLookup struct {
	started chan struct{}
	release chan struct{}
	nodes   []taskdag.Node
}

func (l *blockingTerminalOutcomeLookup) LookupNodesBySpawningThread(ctx context.Context, _ string) ([]taskdag.Node, error) {
	select {
	case l.started <- struct{}{}:
	default:
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-l.release:
		return append([]taskdag.Node(nil), l.nodes...), nil
	}
}
