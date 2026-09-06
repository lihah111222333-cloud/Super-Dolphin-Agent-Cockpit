package eventstore

import (
	"database/sql"
	"errors"
	"testing"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
)

// TestGetSourceStateTxUsesCallerTransaction 锁定 source watermark 读取不会开启嵌套事务。
func TestGetSourceStateTxUsesCallerTransaction(t *testing.T) {
	db := openEventTestDB(t)
	store := mustEventStore(t, db)
	seedTxSourceState(t, db)
	assertTxSourceStateMutation(t, db, store)
	assertTxSourceStateAfterRollback(t, store)
}

func seedTxSourceState(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), `INSERT INTO orchestration_event_source_states (source_id, source_epoch, lane, next_sequence, materialized_sequence, dedup_floor, revision, status, gap_reason, writer_epoch) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, "tx-source", "tx-epoch", "output", 1, 0, 0, 1, "active", "", "writer-1"); err != nil {
		t.Fatalf("seed source state: %v", err)
	}
}

func assertTxSourceStateMutation(t *testing.T, db *sql.DB, store *Store) {
	t.Helper()
	tx := beginEventTx(t, db)
	updateTxSourceState(t, tx)
	state, err := store.GetSourceStateTx(t.Context(), tx, "tx-source", "tx-epoch", LaneOutput)
	if err != nil {
		rollbackEventTx(t, tx)
		t.Fatalf("GetSourceStateTx() error = %v", err)
	}
	assertSourceStateWatermarks(t, state, sourceStateWatermarks{next: 5, materialized: 4, revision: 2}, "transaction source state")
	rollbackEventTx(t, tx)
}

func assertTxSourceStateAfterRollback(t *testing.T, store *Store) {
	t.Helper()
	state := mustGetEventSourceState(t, store, "tx-source", "tx-epoch", LaneOutput, "GetSourceState(after rollback)")
	assertSourceStateWatermarks(t, state, sourceStateWatermarks{next: 1, materialized: 0, revision: 1}, "rolled-back source state")
}

type sourceStateWatermarks struct {
	next         int64
	materialized int64
	revision     int64
}

func beginEventTx(t *testing.T, db *sql.DB) *sql.Tx {
	t.Helper()
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	return tx
}

func updateTxSourceState(t *testing.T, tx *sql.Tx) {
	t.Helper()
	if _, err := tx.ExecContext(t.Context(), `UPDATE orchestration_event_source_states SET next_sequence = 5, materialized_sequence = 4, revision = 2 WHERE source_id = ? AND source_epoch = ? AND lane = ?`, "tx-source", "tx-epoch", "output"); err != nil {
		rollbackEventTx(t, tx)
		t.Fatalf("change source state in transaction: %v", err)
	}
}

func rollbackEventTx(t *testing.T, tx *sql.Tx) {
	t.Helper()
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
}

func mustGetEventSourceState(t *testing.T, store *Store, sourceID, sourceEpoch string, lane Lane, operation string) SourceState {
	t.Helper()
	state, err := store.GetSourceState(t.Context(), sourceID, sourceEpoch, lane)
	if err != nil {
		t.Fatalf("%s error = %v", operation, err)
	}
	return state
}

func assertSourceStateWatermarks(t *testing.T, state SourceState, want sourceStateWatermarks, label string) {
	t.Helper()
	if state.NextSequence != want.next || state.MaterializedSequence != want.materialized || state.Revision != want.revision {
		t.Fatalf("%s = %#v, want next=%d materialized=%d revision=%d", label, state, want.next, want.materialized, want.revision)
	}
}

// TestAppendTxCommitsCanonicalRowsTogether 验证 receipt、event 和 source watermark 同批提交。
func TestAppendTxCommitsCanonicalRowsTogether(t *testing.T) {
	db := openEventTestDB(t)
	store := mustEventStore(t, db)
	reservation := seedEventReservation(t, db, "tx-append")
	frame := eventFrame(reservation, nil, nil, "tx-append", 1)
	appendTxAndAssertRows(t, db, store, frame)
	assertCommittedAppend(t, store, frame)
}

func appendTxAndAssertRows(t *testing.T, db *sql.DB, store *Store, frame IngressFrame) {
	t.Helper()
	tx := beginEventTx(t, db)
	result := appendEventInTx(t, store, tx, frame)
	assertTxAppendResult(t, tx, result)
	assertTxAppendRows(t, tx)
	commitEventTx(t, tx)
}

func appendEventInTx(t *testing.T, store *Store, tx *sql.Tx, frame IngressFrame) AppendResult {
	t.Helper()
	result, err := store.AppendTx(t.Context(), tx, frame)
	if err != nil {
		rollbackEventTx(t, tx)
		t.Fatalf("AppendTx() error = %v", err)
	}
	return result
}

func commitEventTx(t *testing.T, tx *sql.Tx) {
	t.Helper()
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
}

func assertTxAppendResult(t *testing.T, tx *sql.Tx, result AppendResult) {
	t.Helper()
	if result.Receipt.Status == "materialized" && result.Event != nil && result.State.NextSequence == 2 {
		return
	}
	_ = tx.Rollback()
	t.Fatalf("AppendTx() result = %#v, want materialized event and next=2", result)
}

func assertTxAppendRows(t *testing.T, tx *sql.Tx) {
	t.Helper()
	receiptCount := countEventRowsInTx(t, tx, "orchestration_event_ingress_receipts")
	eventCount := countEventRowsInTx(t, tx, "orchestration_agent_events")
	if receiptCount != 1 || eventCount != 1 {
		rollbackEventTx(t, tx)
		t.Fatalf("transaction rows = receipts:%d events:%d, want 1/1", receiptCount, eventCount)
	}
}

func countEventRowsInTx(t *testing.T, tx *sql.Tx, table string) int {
	t.Helper()
	var count int
	if err := tx.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
		rollbackEventTx(t, tx)
		t.Fatalf("count %s rows in transaction: %v", table, err)
	}
	return count
}

func assertCommittedAppend(t *testing.T, store *Store, frame IngressFrame) {
	t.Helper()
	if _, err := store.GetReceipt(t.Context(), frame.SourceID, frame.SourceEpoch, frame.Lane, frame.SourceSequence); err != nil {
		t.Fatalf("GetReceipt(after commit) error = %v", err)
	}
	if _, err := store.GetEvent(t.Context(), frame.Envelope.EventID); err != nil {
		t.Fatalf("GetEvent(after commit) error = %v", err)
	}
	state, err := store.GetSourceState(t.Context(), frame.SourceID, frame.SourceEpoch, frame.Lane)
	if err != nil {
		t.Fatalf("GetSourceState(after commit) error = %v", err)
	}
	if state.NextSequence != 2 || state.MaterializedSequence != 1 {
		t.Fatalf("source state after commit = %#v, want next=2 materialized=1", state)
	}
}

// TestAppendTxRollbackLeavesNoCanonicalRows 验证调用方回滚会清理整笔 append。
func TestAppendTxRollbackLeavesNoCanonicalRows(t *testing.T) {
	db := openEventTestDB(t)
	store := mustEventStore(t, db)
	reservation := seedEventReservation(t, db, "tx-rollback")
	frame := eventFrame(reservation, nil, nil, "tx-rollback", 1)
	appendAndRollback(t, db, store, frame)
	assertMissingEventSourceState(t, store, frame)
	assertNoCanonicalRows(t, db)
}

func appendAndRollback(t *testing.T, db *sql.DB, store *Store, frame IngressFrame) {
	t.Helper()
	tx := beginEventTx(t, db)
	appendEventInTx(t, store, tx, frame)
	rollbackEventTx(t, tx)
}

func assertMissingEventSourceState(t *testing.T, store *Store, frame IngressFrame) {
	t.Helper()
	if _, err := store.GetSourceState(t.Context(), frame.SourceID, frame.SourceEpoch, frame.Lane); !errors.Is(err, ErrEventNotFound) {
		t.Fatalf("source state after rollback = %v, want ErrEventNotFound", err)
	}
}

func assertNoCanonicalRows(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, table := range []string{"orchestration_event_ingress_receipts", "orchestration_agent_events"} {
		if count := countEventRows(t, db, table); count != 0 {
			t.Fatalf("%s rows after rollback = %d, want 0", table, count)
		}
	}
}

// TestAppendTxRejectsNonTransactionalDBTX 防止裸 database/sql 对象越过 Tx 接缝。
func TestAppendTxRejectsNonTransactionalDBTX(t *testing.T) {
	db := openEventTestDB(t)
	store := mustEventStore(t, db)
	reservation := seedEventReservation(t, db, "tx-non-transaction")
	frame := eventFrame(reservation, nil, nil, "tx-non-transaction", 1)
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatalf("Conn() error = %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	var typedNil *sql.Tx
	for _, testCase := range []struct {
		name string
		dbtx storedb.DBTX
	}{
		{name: "db", dbtx: db},
		{name: "conn", dbtx: conn},
		{name: "typed_nil_tx", dbtx: typedNil},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			assertAppendTxRejects(t, store, frame, testCase.dbtx)
		})
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close Conn() = %v", err)
	}
	assertMissingEventSourceState(t, store, frame)
}

// TestAppendTxRechecksIngressFenceInCallerTransaction ensures the lower-level
// transaction entry point cannot bypass the canonical identity/source fence.
func TestAppendTxRechecksIngressFenceInCallerTransaction(t *testing.T) {
	db := openEventTestDB(t)
	store := mustEventStore(t, db)
	reservation, incarnation, binding := seedBoundEventIdentity(t, db, "tx-fence")
	frame := eventFrame(reservation, &incarnation, &binding, "tx-fence", 1)
	frame.Envelope.BindingDigest = eventDigest("tx-fence-wrong")
	frame.EnvelopeHash, _ = CanonicalEnvelopeHash(frame.Envelope)
	tx := beginEventTx(t, db)
	if _, err := store.AppendTx(t.Context(), tx, frame); !errors.Is(err, ErrEventFence) {
		rollbackEventTx(t, tx)
		t.Fatalf("AppendTx(forged fence) error = %v, want ErrEventFence", err)
	}
	rollbackEventTx(t, tx)
	if _, err := store.GetSourceState(t.Context(), frame.SourceID, frame.SourceEpoch, frame.Lane); !errors.Is(err, ErrEventNotFound) {
		t.Fatalf("source state after rejected AppendTx = %v, want absent", err)
	}
}

func assertAppendTxRejects(t *testing.T, store *Store, frame IngressFrame, dbtx storedb.DBTX) {
	t.Helper()
	if _, err := store.AppendTx(t.Context(), dbtx, frame); err == nil {
		t.Fatalf("AppendTx() unexpectedly accepted %T", dbtx)
	}
}

// TestWithTransactionCommitsAndRollsBackCallback 验证 owner callback 的基本事务边界。
func TestWithTransactionCommitsAndRollsBackCallback(t *testing.T) {
	db := openEventTestDB(t)
	store := mustEventStore(t, db)
	createTxOwnerProbe(t, db)
	assertCommittedOwnerTransaction(t, db, store)
	assertRolledBackOwnerTransaction(t, db, store)
}

func createTxOwnerProbe(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE tx_owner_probe (value TEXT NOT NULL)`); err != nil {
		t.Fatalf("create tx owner probe: %v", err)
	}
}

func assertCommittedOwnerTransaction(t *testing.T, db *sql.DB, store *Store) {
	t.Helper()
	callbackCalls := 0
	if err := store.WithTransaction(t.Context(), commitOwnerProbe(t, &callbackCalls)); err != nil {
		t.Fatalf("WithTransaction(commit) error = %v", err)
	}
	if callbackCalls != 1 {
		t.Fatalf("commit callback calls = %d, want 1", callbackCalls)
	}
	if count := countEventRows(t, db, "tx_owner_probe"); count != 1 {
		t.Fatalf("probe rows after commit = %d, want 1", count)
	}
}

func commitOwnerProbe(t *testing.T, callbackCalls *int) func(storedb.DBTX) error {
	t.Helper()
	return func(txdb storedb.DBTX) error {
		*callbackCalls++
		if err := sqlctx.RequireTransactionDBTX(txdb); err != nil {
			return err
		}
		_, err := txdb.ExecContext(t.Context(), `INSERT INTO tx_owner_probe(value) VALUES ('committed')`)
		return err
	}
}

func assertRolledBackOwnerTransaction(t *testing.T, db *sql.DB, store *Store) {
	t.Helper()
	failure := errors.New("rollback callback")
	if err := store.WithTransaction(t.Context(), rollbackOwnerProbe(t, failure)); !errors.Is(err, failure) {
		t.Fatalf("WithTransaction(rollback) error = %v, want callback failure", err)
	}
	if count := countEventRows(t, db, "tx_owner_probe"); count != 1 {
		t.Fatalf("probe rows after rollback = %d, want 1", count)
	}
}

func rollbackOwnerProbe(t *testing.T, failure error) func(storedb.DBTX) error {
	t.Helper()
	return func(txdb storedb.DBTX) error {
		if _, err := txdb.ExecContext(t.Context(), `INSERT INTO tx_owner_probe(value) VALUES ('rolled-back')`); err != nil {
			return err
		}
		return failure
	}
}
