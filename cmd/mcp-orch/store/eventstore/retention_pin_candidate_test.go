package eventstore

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

// TestRecordTombstoneRetentionPinIsTransactionBound 验证 pin 读取与墓碑推进共享同一事务。
func TestRecordTombstoneRetentionPinIsTransactionBound(t *testing.T) {
	db := openEventTestDB(t)
	pin := &recordingRetentionPinVerifier{}
	store, frame := mustRetentionStoreAndFrame(t, db, pin, "retention-pin")
	if _, err := store.RecordTombstone(t.Context(), tombstoneRequestFor(frame, 0, "expired")); err != nil {
		t.Fatalf("RecordTombstone() error = %v", err)
	}
	if pin.calls != 1 || !pin.sawTransaction {
		t.Fatalf("retention pin calls=%d sawTransaction=%v, want one transaction-bound call", pin.calls, pin.sawTransaction)
	}
	if pin.observedFloor != 0 {
		t.Fatalf("retention pin observed dedup floor=%d, want 0 before advance", pin.observedFloor)
	}
	state, err := store.GetSourceState(t.Context(), frame.SourceID, frame.SourceEpoch, frame.Lane)
	if err != nil {
		t.Fatal(err)
	}
	if state.DedupFloor != 1 {
		t.Fatalf("dedup floor=%d, want 1", state.DedupFloor)
	}
}

// TestRecordTombstoneRetentionPinFailsClosed 验证 verifier 错误会回滚墓碑和 dedup floor。
func TestRecordTombstoneRetentionPinFailsClosed(t *testing.T) {
	db := openEventTestDB(t)
	verifierErr := errors.New("active wait cursor pin unavailable")
	store, frame := mustRetentionStoreAndFrame(t, db, &recordingRetentionPinVerifier{err: verifierErr}, "retention-pin-error")
	var err error
	_, err = store.RecordTombstone(t.Context(), tombstoneRequestFor(frame, 0, "expired"))
	if !errors.Is(err, verifierErr) || !errors.Is(err, ErrEventFence) {
		t.Fatalf("RecordTombstone() error = %v, want fence wrapping verifier error", err)
	}
	state, err := store.GetSourceState(t.Context(), frame.SourceID, frame.SourceEpoch, frame.Lane)
	if err != nil {
		t.Fatal(err)
	}
	if state.DedupFloor != 0 {
		t.Fatalf("dedup floor=%d after verifier failure, want 0", state.DedupFloor)
	}
	var tombstones int
	if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM orchestration_event_tombstones").Scan(&tombstones); err != nil {
		t.Fatal(err)
	}
	if tombstones != 0 {
		t.Fatalf("tombstone rows=%d after verifier failure, want 0", tombstones)
	}
}

func mustRetentionStoreAndFrame(t *testing.T, db *sql.DB, pin RetentionPinVerifier, suffix string) (*Store, IngressFrame) {
	t.Helper()
	identityStore, err := identity.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	fence, err := NewIdentityFenceVerifier(identityStore)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStoreWithRetentionPinVerifier(db, fence, pin)
	if err != nil {
		t.Fatal(err)
	}
	reservation := seedEventReservation(t, db, suffix)
	frame := eventFrame(reservation, nil, nil, suffix, 1)
	if _, err := store.Append(t.Context(), frame); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	return store, frame
}

var _ RetentionPinReader = retentionPinReaderStub{}

type retentionPinReaderStub struct{}

func (retentionPinReaderStub) HasRetentionPinTx(context.Context, storedb.DBTX, TombstoneRequest) (bool, error) {
	return false, nil
}

type recordingRetentionPinVerifier struct {
	err            error
	calls          int
	observedFloor  int64
	sawTransaction bool
}

func (v *recordingRetentionPinVerifier) VerifyRetentionPinTx(ctx context.Context, dbtx storedb.DBTX, req TombstoneRequest) error {
	v.calls++
	if dbtx == nil {
		return errors.New("retention pin verifier did not receive transaction")
	}
	v.sawTransaction = true
	if err := dbtx.QueryRowContext(ctx, `SELECT dedup_floor FROM orchestration_event_source_states WHERE source_id = ? AND source_epoch = ? AND lane = ?`, req.SourceID, req.SourceEpoch, req.Lane).Scan(&v.observedFloor); err != nil {
		return err
	}
	return v.err
}
