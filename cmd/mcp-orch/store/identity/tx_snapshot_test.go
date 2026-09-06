package identity

import (
	"database/sql"
	"errors"
	"testing"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

// TestGetActiveSnapshotTxUsesCallerTransaction 锁定 identity snapshot 不会打开嵌套连接。
func TestGetActiveSnapshotTxUsesCallerTransaction(t *testing.T) {
	db := openIdentityTestDB(t)
	store := mustIdentityStore(t, db)
	chain := createIdentityChain(t, db, store, "tx-snapshot", 1)
	if _, err := store.PrepareBinding(t.Context(), chain.prep); err != nil {
		t.Fatalf("PrepareBinding() error = %v", err)
	}
	active, err := store.FinalizeBinding(t.Context(), finalizeRequestFor(chain, "receipt-tx-snapshot"))
	if err != nil {
		t.Fatalf("FinalizeBinding() error = %v", err)
	}

	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	if _, err := tx.ExecContext(t.Context(), `INSERT INTO orchestration_current_bindings (
		agent_id, binding_version, reservation_id, incarnation_id, identity_kind,
		reservation_generation, incarnation_generation, external_identity_ref,
		source_fence_ref, active_process_receipt_ref, binding_digest, state, schema_version
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'revoked', ?)`,
		active.AgentID, active.BindingVersion+1, active.ReservationID, active.IncarnationID,
		active.IdentityKind, active.ReservationGeneration, active.IncarnationGeneration,
		active.ExternalIdentityRef, active.SourceFenceRef, active.ActiveProcessReceiptRef,
		active.BindingDigest, active.SchemaVersion); err != nil {
		_ = tx.Rollback()
		t.Fatalf("revoke binding in transaction: %v", err)
	}
	if _, err := store.GetActiveSnapshotTx(t.Context(), tx, active.AgentID); !errors.Is(err, ErrIdentityFence) {
		_ = tx.Rollback()
		t.Fatalf("GetActiveSnapshotTx() error = %v, want ErrIdentityFence", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if _, err := store.GetActiveSnapshot(t.Context(), active.AgentID); err != nil {
		t.Fatalf("GetActiveSnapshot(after rollback) error = %v", err)
	}
}

// TestGetActiveSnapshotTxRejectsNonTransactionalDBTX 防止裸 database/sql 对象越过 Tx 接缝。
func TestGetActiveSnapshotTxRejectsNonTransactionalDBTX(t *testing.T) {
	db := openIdentityTestDB(t)
	store := mustIdentityStore(t, db)
	for _, testCase := range []struct {
		name string
		dbtx storedb.DBTX
	}{
		{name: "database", dbtx: db},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := store.GetActiveSnapshotTx(t.Context(), testCase.dbtx, "agent"); err == nil {
				t.Fatal("GetActiveSnapshotTx() unexpectedly accepted non-transactional DBTX")
			}
		})
	}
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatalf("Conn() error = %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := store.GetActiveSnapshotTx(t.Context(), conn, "agent"); err == nil {
		t.Fatal("GetActiveSnapshotTx() unexpectedly accepted *sql.Conn")
	}
	var typedNil *sql.Tx
	if _, err := store.GetActiveSnapshotTx(t.Context(), typedNil, "agent"); err == nil {
		t.Fatal("GetActiveSnapshotTx() unexpectedly accepted typed-nil *sql.Tx")
	}
}

// TestIdentityTxReadersUseCallerTransaction verifies each additive Tx getter
// shares the caller connection and refuses a bare database handle.
func TestIdentityTxReadersUseCallerTransaction(t *testing.T) {
	db := openIdentityTestDB(t)
	store := mustIdentityStore(t, db)
	chain := createIdentityChain(t, db, store, "tx-readers", 1)
	if _, err := store.PrepareBinding(t.Context(), chain.prep); err != nil {
		t.Fatal(err)
	}
	active, err := store.FinalizeBinding(t.Context(), finalizeRequestFor(chain, "receipt-tx-readers"))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	assertIdentityTxReaders(t, store, tx, active)
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertIdentityBareDBRejected(t, store, db, active)
}

func assertIdentityTxReaders(t *testing.T, store *Store, tx *sql.Tx, active CurrentBinding) {
	t.Helper()
	reservation, err := store.GetWithDBTX(t.Context(), tx, active.ReservationID)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	incarnation, err := store.GetIncarnationWithDBTX(t.Context(), tx, active.IncarnationID)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	binding, err := store.GetCurrentWithDBTX(t.Context(), tx, active.AgentID)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if reservation.ReservationID != active.ReservationID || incarnation.IncarnationID != active.IncarnationID || binding.BindingVersion != active.BindingVersion {
		_ = tx.Rollback()
		t.Fatalf("Tx readers returned mismatched chain: reservation=%#v incarnation=%#v binding=%#v", reservation, incarnation, binding)
	}
}

func assertIdentityBareDBRejected(t *testing.T, store *Store, db *sql.DB, active CurrentBinding) {
	t.Helper()
	for _, name := range []string{"reservation", "incarnation", "binding"} {
		t.Run(name, func(t *testing.T) {
			var err error
			switch name {
			case "reservation":
				_, err = store.GetWithDBTX(t.Context(), db, active.ReservationID)
			case "incarnation":
				_, err = store.GetIncarnationWithDBTX(t.Context(), db, active.IncarnationID)
			case "binding":
				_, err = store.GetCurrentWithDBTX(t.Context(), db, active.AgentID)
			}
			if err == nil {
				t.Fatal("Tx reader unexpectedly accepted bare database")
			}
		})
	}
}
