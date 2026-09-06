package accesswait

import (
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
)

func TestAccessWaitTxOwnerAtomicRollbackAndCallerCommit(t *testing.T) {
	t.Run("rollback", testAccessWaitTxRollback)
	t.Run("commit", testAccessWaitTxCommit)
}

func testAccessWaitTxRollback(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	grantRequest := validGrantRequest("grant-tx-rollback", "grant/tx-rollback", strings.Repeat("a", 64))
	waitRequest := waitRequest(grantRequest, "", waitSelector(), "wait/tx-rollback", strings.Repeat("b", 64), strings.Repeat("c", 64))
	tx := beginAccessWaitTx(t, fixture.db)
	if _, err := fixture.identity.GetCurrentWithDBTX(t.Context(), tx, grantRequest.Source.AgentID); err != nil {
		t.Fatalf("identity.GetCurrentWithDBTX() error = %v", err)
	}
	grant, err := fixture.store.CreateGrantTx(t.Context(), tx, grantRequest)
	if err != nil {
		t.Fatalf("CreateGrantTx() error = %v", err)
	}
	handle, err := fixture.store.CreateWaitTx(t.Context(), tx, waitRequest)
	if err != nil {
		t.Fatalf("CreateWaitTx() error = %v", err)
	}
	assertAccessWaitTxReads(t, fixture, tx, waitRequest, grant.GrantID, grant, handle.WaitHandleID, handle)
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertAccessWaitRowCounts(t, fixture.db, 0, 0)
}

func assertAccessWaitTxReads(t *testing.T, fixture accessWaitFixture, tx *sql.Tx, request WaitRequest, grantID string, expectedGrant any, handleID string, expectedWait any) {
	t.Helper()
	gotGrant, err := fixture.store.GetGrantTx(t.Context(), tx, grantID)
	if err != nil || !reflect.DeepEqual(gotGrant, expectedGrant) {
		t.Fatalf("GetGrantTx() = %#v err=%v, want %#v", gotGrant, err, expectedGrant)
	}
	gotWait, err := fixture.store.GetWaitByKeyTx(t.Context(), tx, request.IdempotencyNamespace, request.IdempotencyKeyDigest)
	if err != nil || !reflect.DeepEqual(gotWait, expectedWait) {
		t.Fatalf("GetWaitByKeyTx() = %#v err=%v, want %#v", gotWait, err, expectedWait)
	}
	gotWaitByID, err := fixture.store.GetWaitByIDTx(t.Context(), tx, handleID)
	if err != nil || !reflect.DeepEqual(gotWaitByID, expectedWait) {
		t.Fatalf("GetWaitByIDTx() = %#v err=%v, want %#v", gotWaitByID, err, expectedWait)
	}
}

func testAccessWaitTxCommit(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	grantRequest := validGrantRequest("grant-tx-commit", "grant/tx-commit", strings.Repeat("d", 64))
	waitRequest := waitRequest(grantRequest, "", waitSelector(), "wait/tx-commit", strings.Repeat("e", 64), strings.Repeat("f", 64))
	tx := beginAccessWaitTx(t, fixture.db)
	grant, err := fixture.store.CreateGrantTx(t.Context(), tx, grantRequest)
	if err != nil {
		t.Fatalf("CreateGrantTx() error = %v", err)
	}
	handle, err := fixture.store.CreateWaitTx(t.Context(), tx, waitRequest)
	if err != nil {
		t.Fatalf("CreateWaitTx() error = %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	assertAccessWaitRowCounts(t, fixture.db, 1, 1)
	if got, err := fixture.store.GetGrant(t.Context(), grant.GrantID); err != nil || !reflect.DeepEqual(got, grant) {
		t.Fatalf("GetGrant(after commit) = %#v err=%v, want %#v", got, err, grant)
	}
	poll := PollRequest{WaitHandleID: handle.WaitHandleID, CallerPrincipalRef: handle.CallerPrincipalRef, ScopeRef: handle.ScopeRef, GrantRevision: handle.GrantRevision}
	if got, err := fixture.store.GetWait(t.Context(), poll); err != nil || !reflect.DeepEqual(got, handle) {
		t.Fatalf("GetWait(after commit) = %#v err=%v, want %#v", got, err, handle)
	}
}

func TestAccessWaitTxOwnerImmediateTransactionSharesOwnersAndRollsBack(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	fixture.db.SetMaxOpenConns(1)
	grantRequest := validGrantRequest("grant-tx-immediate", "grant/tx-immediate", strings.Repeat("0", 64))
	waitRequest := waitRequest(grantRequest, "", waitSelector(), "wait/tx-immediate", strings.Repeat("a", 64), strings.Repeat("b", 64))
	rollback := errors.New("rollback access wait immediate transaction")
	err := sqlctx.WithImmediateTx(t.Context(), fixture.db, storedb.New(fixture.db), func(_ *storedb.Queries, dbtx storedb.DBTX) error {
		if _, err := fixture.identity.GetCurrentWithDBTX(t.Context(), dbtx, grantRequest.Source.AgentID); err != nil {
			return err
		}
		grant, err := fixture.store.CreateGrantTx(t.Context(), dbtx, grantRequest)
		if err != nil {
			return err
		}
		handle, err := fixture.store.CreateWaitTx(t.Context(), dbtx, waitRequest)
		if err != nil {
			return err
		}
		gotGrant, err := fixture.store.GetGrantTx(t.Context(), dbtx, grant.GrantID)
		if err != nil || !reflect.DeepEqual(gotGrant, grant) {
			return errors.New("transaction grant replay mismatch")
		}
		gotWait, err := fixture.store.GetWaitByKeyTx(t.Context(), dbtx, waitRequest.IdempotencyNamespace, waitRequest.IdempotencyKeyDigest)
		if err != nil || !reflect.DeepEqual(gotWait, handle) {
			return errors.New("transaction wait replay mismatch")
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("WithImmediateTx() error = %v, want rollback marker", err)
	}
	assertAccessWaitRowCounts(t, fixture.db, 0, 0)
}

func TestAccessWaitTxOwnerExactReplayKeepsWaitIdentity(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	grantRequest := validGrantRequest("grant-tx-replay", "grant/tx-replay", strings.Repeat("1", 64))
	waitRequest := waitRequest(grantRequest, "", waitSelector(), "wait/tx-replay", strings.Repeat("2", 64), strings.Repeat("3", 64))
	firstTx := beginAccessWaitTx(t, fixture.db)
	firstGrant, err := fixture.store.CreateGrantTx(t.Context(), firstTx, grantRequest)
	if err != nil {
		t.Fatalf("CreateGrantTx(first) error = %v", err)
	}
	firstWait, err := fixture.store.CreateWaitTx(t.Context(), firstTx, waitRequest)
	if err != nil {
		t.Fatalf("CreateWaitTx(first) error = %v", err)
	}
	if err := firstTx.Commit(); err != nil {
		t.Fatal(err)
	}
	replayTx := beginAccessWaitTx(t, fixture.db)
	replayGrant, err := fixture.store.CreateGrantTx(t.Context(), replayTx, grantRequest)
	if err != nil || !reflect.DeepEqual(replayGrant, firstGrant) {
		t.Fatalf("CreateGrantTx(replay) = %#v err=%v, want %#v", replayGrant, err, firstGrant)
	}
	replayWait, err := fixture.store.CreateWaitTx(t.Context(), replayTx, waitRequest)
	if err != nil || !reflect.DeepEqual(replayWait, firstWait) {
		t.Fatalf("CreateWaitTx(replay) = %#v err=%v, want %#v", replayWait, err, firstWait)
	}
	if replayWait.WaitHandleID != firstWait.WaitHandleID {
		t.Fatalf("replay wait id = %q, want %q", replayWait.WaitHandleID, firstWait.WaitHandleID)
	}
	if err := replayTx.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertAccessWaitRowCounts(t, fixture.db, 1, 1)
}

func TestAccessWaitTxOwnerCleanupRollbackAndCommit(t *testing.T) {
	t.Run("rollback", testAccessWaitCleanupRollback)
	t.Run("commit", testAccessWaitCleanupCommit)
}

func testAccessWaitCleanupRollback(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	grantRequest := validGrantRequest("grant-tx-cleanup", "grant/tx-cleanup", strings.Repeat("4", 64))
	grant := mustCreateGrant(t, fixture.store, grantRequest)
	waitRequest := waitRequest(grantRequest, "", waitSelector(), "wait/tx-cleanup", strings.Repeat("5", 64), strings.Repeat("6", 64))
	handle := mustCreateWait(t, fixture.store, waitRequest)
	cancel := CancelRequest{WaitHandleID: handle.WaitHandleID, CallerPrincipalRef: grant.CallerPrincipalRef, ScopeRef: grant.ScopeRef, GrantRevision: grant.Revision, ReasonRef: "tx-cleanup", CancelledAt: time.Now().UTC()}
	revoke := RevokeGrantRequest{GrantID: grant.GrantID, GrantRevision: grant.Revision, CallerPrincipalRef: grant.CallerPrincipalRef, ScopeRef: grant.ScopeRef, ReasonRef: "tx-cleanup", RequestDigest: grant.RequestDigest, RevokedAt: time.Now().UTC()}

	rollbackTx := beginAccessWaitTx(t, fixture.db)
	if result, err := fixture.store.CancelWaitTx(t.Context(), rollbackTx, cancel); err != nil || result.Status != "cancelled" {
		t.Fatalf("CancelWaitTx(rollback) = %#v err=%v", result, err)
	}
	if result, err := fixture.store.RevokeGrantTx(t.Context(), rollbackTx, revoke); err != nil || result.Status != "revoked" {
		t.Fatalf("RevokeGrantTx(rollback) = %#v err=%v", result, err)
	}
	if err := rollbackTx.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertAccessWaitStatuses(t, fixture.db, grant.GrantID, handle.WaitHandleID, "active", "pending")

}

func testAccessWaitCleanupCommit(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	grantRequest := validGrantRequest("grant-tx-cleanup", "grant/tx-cleanup", strings.Repeat("4", 64))
	grant := mustCreateGrant(t, fixture.store, grantRequest)
	waitRequest := waitRequest(grantRequest, "", waitSelector(), "wait/tx-cleanup", strings.Repeat("5", 64), strings.Repeat("6", 64))
	handle := mustCreateWait(t, fixture.store, waitRequest)
	cancel := CancelRequest{WaitHandleID: handle.WaitHandleID, CallerPrincipalRef: grant.CallerPrincipalRef, ScopeRef: grant.ScopeRef, GrantRevision: grant.Revision, ReasonRef: "tx-cleanup", CancelledAt: time.Now().UTC()}
	revoke := RevokeGrantRequest{GrantID: grant.GrantID, GrantRevision: grant.Revision, CallerPrincipalRef: grant.CallerPrincipalRef, ScopeRef: grant.ScopeRef, ReasonRef: "tx-cleanup", RequestDigest: grant.RequestDigest, RevokedAt: time.Now().UTC()}
	commitTx := beginAccessWaitTx(t, fixture.db)
	if result, err := fixture.store.CancelWaitTx(t.Context(), commitTx, cancel); err != nil || result.Status != "cancelled" {
		t.Fatalf("CancelWaitTx(commit) = %#v err=%v", result, err)
	}
	if result, err := fixture.store.RevokeGrantTx(t.Context(), commitTx, revoke); err != nil || result.Status != "revoked" {
		t.Fatalf("RevokeGrantTx(commit) = %#v err=%v", result, err)
	}
	if err := commitTx.Commit(); err != nil {
		t.Fatal(err)
	}
	assertAccessWaitStatuses(t, fixture.db, grant.GrantID, handle.WaitHandleID, "revoked", "cancelled")
}

func TestAccessWaitTxOwnerRejectsNonTransactionsWithoutSideEffects(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	grantRequest := validGrantRequest("grant-tx-reject", "grant/tx-reject", strings.Repeat("7", 64))
	waitRequest := waitRequest(grantRequest, "", waitSelector(), "wait/tx-reject", strings.Repeat("8", 64), strings.Repeat("9", 64))
	cancel := CancelRequest{WaitHandleID: "wait-tx-reject", CallerPrincipalRef: grantRequest.CallerPrincipalRef, ScopeRef: grantRequest.ScopeRef, GrantRevision: 1, ReasonRef: "tx-reject", CancelledAt: time.Now().UTC()}
	revoke := RevokeGrantRequest{GrantID: grantRequest.GrantID, GrantRevision: 1, CallerPrincipalRef: grantRequest.CallerPrincipalRef, ScopeRef: grantRequest.ScopeRef, ReasonRef: "tx-reject", RequestDigest: grantRequest.RequestDigest, RevokedAt: time.Now().UTC()}
	var typedNil *sql.Tx
	unknown := &unknownAccessWaitDBTX{DB: fixture.db}
	for name, dbtx := range map[string]storedb.DBTX{"bare_db": fixture.db, "typed_nil": typedNil, "unknown": unknown} {
		t.Run(name, func(t *testing.T) {
			checks := []func() error{
				func() error { _, err := fixture.store.CreateGrantTx(t.Context(), dbtx, grantRequest); return err },
				func() error { _, err := fixture.store.GetGrantTx(t.Context(), dbtx, grantRequest.GrantID); return err },
				func() error { _, err := fixture.store.CreateWaitTx(t.Context(), dbtx, waitRequest); return err },
				func() error {
					_, err := fixture.store.GetWaitByKeyTx(t.Context(), dbtx, waitRequest.IdempotencyNamespace, waitRequest.IdempotencyKeyDigest)
					return err
				},
				func() error { _, err := fixture.store.GetWaitByIDTx(t.Context(), dbtx, "wait-tx-reject"); return err },
				func() error { _, err := fixture.store.CancelWaitTx(t.Context(), dbtx, cancel); return err },
				func() error { _, err := fixture.store.RevokeGrantTx(t.Context(), dbtx, revoke); return err },
			}
			for index, check := range checks {
				if err := check(); err == nil || !strings.Contains(err.Error(), "transaction-bound DBTX is required") {
					t.Fatalf("check %d error = %v, want transaction DBTX rejection", index, err)
				}
			}
		})
	}
	assertAccessWaitRowCounts(t, fixture.db, 0, 0)
}

func TestAccessWaitTxOwnerInvalidInputHasNoSideEffects(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	tx := beginAccessWaitTx(t, fixture.db)
	invalidGrant := validGrantRequest("grant-tx-invalid", "grant/tx-invalid", strings.Repeat("a", 64))
	invalidGrant.GrantID = ""
	if _, err := fixture.store.CreateGrantTx(t.Context(), tx, invalidGrant); !errors.Is(err, ErrInvalidAccess) {
		t.Fatalf("CreateGrantTx(invalid) error = %v, want ErrInvalidAccess", err)
	}
	invalidWait := WaitRequest{}
	if _, err := fixture.store.CreateWaitTx(t.Context(), tx, invalidWait); !errors.Is(err, ErrInvalidWait) {
		t.Fatalf("CreateWaitTx(invalid) error = %v, want ErrInvalidWait", err)
	}
	if _, err := fixture.store.CancelWaitTx(t.Context(), tx, CancelRequest{}); !errors.Is(err, ErrInvalidWait) {
		t.Fatalf("CancelWaitTx(invalid) error = %v, want ErrInvalidWait", err)
	}
	if _, err := fixture.store.RevokeGrantTx(t.Context(), tx, RevokeGrantRequest{}); !errors.Is(err, ErrInvalidAccess) {
		t.Fatalf("RevokeGrantTx(invalid) error = %v, want ErrInvalidAccess", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	assertAccessWaitRowCounts(t, fixture.db, 0, 0)
}

type unknownAccessWaitDBTX struct{ *sql.DB }

func beginAccessWaitTx(t *testing.T, db *sql.DB) *sql.Tx {
	t.Helper()
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

func assertAccessWaitRowCounts(t *testing.T, db *sql.DB, grants, waits int) {
	t.Helper()
	var gotGrants, gotWaits int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM orchestration_access_grants").Scan(&gotGrants); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM orchestration_event_wait_handles").Scan(&gotWaits); err != nil {
		t.Fatal(err)
	}
	if gotGrants != grants || gotWaits != waits {
		t.Fatalf("row counts = grants:%d waits:%d, want grants:%d waits:%d", gotGrants, gotWaits, grants, waits)
	}
}

func assertAccessWaitStatuses(t *testing.T, db *sql.DB, grantID, waitID, grantStatus, waitStatus string) {
	t.Helper()
	var gotGrant, gotWait string
	if err := db.QueryRowContext(t.Context(), "SELECT status FROM orchestration_access_grants WHERE grant_id = ?", grantID).Scan(&gotGrant); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(t.Context(), "SELECT status FROM orchestration_event_wait_handles WHERE wait_handle_id = ?", waitID).Scan(&gotWait); err != nil {
		t.Fatal(err)
	}
	if gotGrant != grantStatus || gotWait != waitStatus {
		t.Fatalf("statuses = grant:%q wait:%q, want grant:%q wait:%q", gotGrant, gotWait, grantStatus, waitStatus)
	}
}
