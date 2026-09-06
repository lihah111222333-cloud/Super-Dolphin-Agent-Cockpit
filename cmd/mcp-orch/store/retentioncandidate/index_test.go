package retentioncandidate

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
)

func TestBuildIndexSnapshotClassifiesPinnedAndStale(t *testing.T) {
	db := newCandidateDB(t)
	req := candidateRequest(t, 3)
	if _, err := db.Exec(`INSERT INTO orchestration_event_wait_handles VALUES ('stale', ?, ?, ?, ?, 'pending', 1), ('pinned', ?, ?, ?, ?, 'pending', 1)`, req.SourceID, req.SourceEpoch, req.Lane, req.ExpectedFloor-1, req.SourceID, req.SourceEpoch, req.Lane, req.ExpectedFloor); err != nil {
		t.Fatal(err)
	}
	snapshot := buildSnapshot(t, db, req)
	if len(snapshot.Entries) != 2 || snapshot.PinnedCount != 1 {
		t.Fatalf("snapshot = %#v, want two entries and one pin", snapshot)
	}
	dispositions := make(map[string]CandidateDisposition, len(snapshot.Entries))
	for _, entry := range snapshot.Entries {
		dispositions[entry.PinKey] = entry.Disposition
	}
	if dispositions["stale"] != CandidateStale || dispositions["pinned"] != CandidatePinned {
		t.Fatalf("entry dispositions = %#v", dispositions)
	}
	plan := snapshot.PlanGC()
	if plan.Disposition != GCBlockedPinned || plan.CandidateCount != 2 || plan.PinnedCount != 1 {
		t.Fatalf("GC plan = %#v, want blocked_pinned", plan)
	}
}

func TestBuildIndexSnapshotWithoutPinsRemainsUnverified(t *testing.T) {
	db := newCandidateDB(t)
	req := candidateRequest(t, 0)
	snapshot := buildSnapshot(t, db, req)
	plan := snapshot.PlanGC()
	if plan.Disposition != GCBlockedUnverified {
		t.Fatalf("GC plan = %#v, want blocked_unverified", plan)
	}
}

func TestBuildIndexSnapshotRejectsMalformedCandidate(t *testing.T) {
	db := newCandidateDB(t)
	req := candidateRequest(t, 0)
	if _, err := db.Exec(`INSERT INTO orchestration_event_wait_handles VALUES ('unknown', ?, ?, ?, ?, 'unknown', 1)`, req.SourceID, req.SourceEpoch, req.Lane, req.ExpectedFloor); err != nil {
		t.Fatal(err)
	}
	if _, err := buildSnapshotErr(t, db, req); !errors.Is(err, errInvalidCandidate) {
		t.Fatalf("BuildIndexSnapshotTx() error = %v, want invalid candidate", err)
	}
}

func TestBuildIndexSnapshotRequiresTransaction(t *testing.T) {
	db := newCandidateDB(t)
	req := candidateRequest(t, 0)
	if _, err := New().BuildIndexSnapshotTx(t.Context(), db, req); !errors.Is(err, errInvalidCandidate) {
		t.Fatalf("unbound transaction error = %v, want invalid candidate", err)
	}
}

func buildSnapshot(t *testing.T, db *sql.DB, req eventstore.TombstoneRequest) IndexSnapshot {
	t.Helper()
	snapshot, err := buildSnapshotErr(t, db, req)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func buildSnapshotErr(t *testing.T, db *sql.DB, req eventstore.TombstoneRequest) (IndexSnapshot, error) {
	t.Helper()
	var snapshot IndexSnapshot
	err := sqlctx.WithImmediateTx(t.Context(), db, storedb.New(db), func(_ *storedb.Queries, txdb storedb.DBTX) error {
		var err error
		snapshot, err = New().BuildIndexSnapshotTx(t.Context(), txdb, req)
		return err
	})
	return snapshot, err
}
