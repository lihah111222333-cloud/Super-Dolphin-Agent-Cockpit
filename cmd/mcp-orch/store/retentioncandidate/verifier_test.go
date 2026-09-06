package retentioncandidate

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
)

func TestVerifierPendingHandleAndFloorEquality(t *testing.T) {
	db := newCandidateDB(t)
	req := candidateRequest(t, 3)
	_, err := db.Exec(`INSERT INTO orchestration_event_wait_handles VALUES ('handle', ?, ?, ?, ?, 'pending', 1)`, req.SourceID, req.SourceEpoch, req.Lane, req.ExpectedFloor)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyCandidate(t, db, req); !errors.Is(err, eventstore.ErrEventRetentionPinned) {
		t.Fatalf("VerifyRetentionPinTx()=%v, want retention pin", err)
	}
	if _, err := db.Exec(`UPDATE orchestration_event_wait_handles SET after_source_sequence = ?`, req.ExpectedFloor-1); err != nil {
		t.Fatal(err)
	}
	if err := verifyCandidate(t, db, req); err != nil {
		t.Fatalf("below-floor cursor should not pin: %v", err)
	}
}

func TestVerifierLatestNonterminalOperationHead(t *testing.T) {
	db := newCandidateDB(t)
	req := candidateRequest(t, 1)
	_, err := db.Exec(`INSERT INTO orchestration_wait_operations VALUES ('op', ?, 'ready')`, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO orchestration_wait_operation_target_journal (operation_key, operation_digest, target_key, target_index, version, phase, source_id, source_epoch, lane, after_source_sequence, schema_version) VALUES ('op', ?, 'target', 1, 1, 'waiting', ?, ?, ?, ?, 1)`, strings.Repeat("a", 64), req.SourceID, req.SourceEpoch, req.Lane, req.ExpectedFloor)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyCandidate(t, db, req); !errors.Is(err, eventstore.ErrEventRetentionPinned) {
		t.Fatalf("nonterminal target head=%v, want retention pin", err)
	}
	if _, err := db.Exec(`UPDATE orchestration_wait_operation_target_journal SET phase = 'satisfied'`); err != nil {
		t.Fatal(err)
	}
	if err := verifyCandidate(t, db, req); err != nil {
		t.Fatalf("terminal target head should not pin: %v", err)
	}
}

func TestVerifierUsesLatestTargetHeadAndRejectsClearedOperationDrift(t *testing.T) {
	db := newCandidateDB(t)
	req := candidateRequest(t, 1)
	if _, err := db.Exec(`INSERT INTO orchestration_wait_operations VALUES ('op-latest', ?, 'ready')`, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	insertTarget := `INSERT INTO orchestration_wait_operation_target_journal (operation_key, operation_digest, target_key, target_index, version, phase, source_id, source_epoch, lane, after_source_sequence, schema_version) VALUES ('op-latest', ?, 'target', 1, ?, ?, ?, ?, ?, ?, 1)`
	if _, err := db.Exec(insertTarget, strings.Repeat("a", 64), 1, "waiting", req.SourceID, req.SourceEpoch, req.Lane, req.ExpectedFloor); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(insertTarget, strings.Repeat("a", 64), 2, "satisfied", req.SourceID, req.SourceEpoch, req.Lane, req.ExpectedFloor); err != nil {
		t.Fatal(err)
	}
	if err := verifyCandidate(t, db, req); err != nil {
		t.Fatalf("latest terminal target should release pin: %v", err)
	}
	if _, err := db.Exec(`UPDATE orchestration_wait_operations SET status = 'cleanup_cleared'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE orchestration_wait_operation_target_journal SET version = 3, phase = 'waiting'`); err != nil {
		t.Fatal(err)
	}
	if err := verifyCandidate(t, db, req); !errors.Is(err, errInvalidCandidate) {
		t.Fatalf("cleared operation drift = %v, want invalid candidate", err)
	}
}

func TestVerifierRejectsCandidateOverflow(t *testing.T) {
	db := newCandidateDB(t)
	req := candidateRequest(t, 2)
	for index := int64(0); index < MaxPins+1; index++ {
		_, err := db.Exec(`INSERT INTO orchestration_event_wait_handles VALUES (?, ?, ?, ?, ?, 'pending', 1)`, fmt.Sprintf("handle-%d", index), req.SourceID, req.SourceEpoch, req.Lane, req.ExpectedFloor)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := verifyCandidate(t, db, req); !errors.Is(err, errInvalidCandidate) {
		t.Fatalf("overflow error=%v, want invalid candidate", err)
	}
}

func TestVerifierRejectsMalformedCandidateAndUnboundTransaction(t *testing.T) {
	db := newCandidateDB(t)
	req := candidateRequest(t, 0)
	if _, err := db.Exec(`INSERT INTO orchestration_event_wait_handles VALUES ('bad', ?, ?, ?, ?, 'unknown', 1)`, req.SourceID, req.SourceEpoch, req.Lane, req.ExpectedFloor); err != nil {
		t.Fatal(err)
	}
	if err := verifyCandidate(t, db, req); !errors.Is(err, errInvalidCandidate) {
		t.Fatalf("malformed candidate error=%v, want invalid candidate", err)
	}
	var typedNil *sql.Tx
	if err := New().VerifyRetentionPinTx(t.Context(), typedNil, req); !errors.Is(err, errInvalidCandidate) {
		t.Fatalf("typed-nil transaction error=%v, want invalid candidate", err)
	}
}

func verifyCandidate(t *testing.T, db *sql.DB, req eventstore.TombstoneRequest) error {
	t.Helper()
	var result error
	err := sqlctx.WithImmediateTx(t.Context(), db, storedb.New(db), func(_ *storedb.Queries, txdb storedb.DBTX) error {
		result = New().VerifyRetentionPinTx(t.Context(), txdb, req)
		return result
	})
	if err != nil {
		return err
	}
	return result
}

func newCandidateDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	stmts := []string{
		`CREATE TABLE orchestration_event_wait_handles (wait_handle_id TEXT, source_id TEXT, source_epoch TEXT, lane TEXT, after_source_sequence INTEGER, status TEXT, schema_version INTEGER)`,
		`CREATE TABLE orchestration_wait_operations (operation_key TEXT, operation_digest TEXT, status TEXT)`,
		`CREATE TABLE orchestration_wait_operation_target_journal (operation_key TEXT, operation_digest TEXT, target_key TEXT, target_index INTEGER, version INTEGER, phase TEXT, source_id TEXT, source_epoch TEXT, lane TEXT, after_source_sequence INTEGER, schema_version INTEGER)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func candidateRequest(t *testing.T, floor int64) eventstore.TombstoneRequest {
	t.Helper()
	cursor, err := eventstore.CanonicalCursor("source", "epoch", eventstore.LaneControl, floor+1)
	if err != nil {
		t.Fatal(err)
	}
	return eventstore.TombstoneRequest{SourceID: "source", SourceEpoch: "epoch", Lane: eventstore.LaneControl, SourceSequence: floor + 1, EnvelopeHash: strings.Repeat("a", 64), EventID: "event", CanonicalCursor: cursor, ExpectedFloor: floor, Status: "expired"}
}
