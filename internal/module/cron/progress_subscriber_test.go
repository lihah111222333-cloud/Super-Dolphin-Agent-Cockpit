package cron

import (
	"log/slog"
	"testing"
)

func TestCronProgressWorkerQueueIsBoundedAndCoalescesProgress(t *testing.T) {
	worker := newCronProgressWorker(nil, slog.New(slog.DiscardHandler))
	worker.queueLimit = 2

	worker.enqueue(cronProgressRequest{kind: cronExtendClaim, turnID: "turn-1"})
	worker.enqueue(cronProgressRequest{kind: cronExtendClaim, turnID: "turn-1"})
	worker.enqueue(cronProgressRequest{kind: cronExtendClaim, turnID: "turn-2"})
	worker.enqueue(cronProgressRequest{kind: cronExtendClaim, turnID: "turn-3"})
	worker.enqueue(cronProgressRequest{kind: cronCompleteTurn, turnID: "turn-terminal", success: true})

	snapshot := worker.HealthSnapshot()
	if snapshot.Backlog > 2 {
		t.Fatalf("Backlog = %d, want bounded <= 2", snapshot.Backlog)
	}
	if snapshot.Coalesced == 0 {
		t.Fatalf("Coalesced = 0, want duplicate progress coalesced")
	}
	if snapshot.Dropped == 0 {
		t.Fatalf("Dropped = 0, want progress dropped under pressure")
	}
	if !snapshot.HasTerminal {
		t.Fatalf("HasTerminal = false, want terminal event preserved: %+v", snapshot)
	}
}
