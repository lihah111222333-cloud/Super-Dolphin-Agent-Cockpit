package operatorreservation

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/operatorsource"
	sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
)

func TestOperatorReservationStoreContract(t *testing.T) {
	db, source := operatorReservationTestDB(t)
	request := operatorReservationRequest(source, "reservation-1", "idem-1")
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Create(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := store.Create(t.Context(), request)
	if err != nil || replay != first {
		t.Fatalf("replay=%#v err=%v want exact first=%#v", replay, err, first)
	}
	resolved, err := store.Resolve(t.Context(), request.IdempotencyNamespace, request.IdempotencyKeyDigest)
	if err != nil || resolved != first {
		t.Fatalf("resolve=%#v err=%v want=%#v", resolved, err, first)
	}
	conflict := request
	conflict.ContentIntent = "launch/changed"
	if _, err := store.Create(t.Context(), conflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict error=%v", err)
	}
	selector := operatorsource.Selector{PrincipalRef: source.PrincipalRef, ScopeRef: source.ScopeRef, AuthContext: source.AuthContext, SourceID: source.SourceID, SourceFenceRef: source.SourceFenceRef, Generation: source.Generation}
	if _, err := sourceStore(t, db).Revoke(t.Context(), selector); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Resolve(t.Context(), request.IdempotencyNamespace, request.IdempotencyKeyDigest); !errors.Is(err, ErrStaleSource) {
		t.Fatalf("revoked resolve error=%v", err)
	}
}

func operatorReservationTestDB(t *testing.T) (*sql.DB, operatorsource.Source) {
	t.Helper()
	root := t.TempDir()
	db, err := sqliteruntime.Open(t.Context(), sqliteruntime.OpenOptions{Path: filepath.Join(root, "operator-reservation.sqlite")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqliteruntime.RunEmbeddedMigrations(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	source, err := sourceStore(t, db).Activate(t.Context(), operatorsource.ActivateRequest{PrincipalRef: "operator/test", ScopeRef: "scope/test", AuthContext: "auth/test", ExpectedGeneration: 0})
	if err != nil {
		t.Fatal(err)
	}
	return db, source
}

func sourceStore(t *testing.T, db *sql.DB) *operatorsource.Store {
	t.Helper()
	store, err := operatorsource.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func operatorReservationRequest(source operatorsource.Source, reservationID, _ string) Request {
	return Request{
		ReservationID: reservationID, SourceID: source.SourceID, SourceFenceRef: source.SourceFenceRef,
		PrincipalRef: source.PrincipalRef, ScopeRef: source.ScopeRef, AuthContext: source.AuthContext,
		Generation: source.Generation, WorkspaceRef: "workspace/test", CallerPrincipalRef: "operator/test",
		IdempotencyNamespace: "operator-reservation/test", IdempotencyKeyDigest: strings.Repeat("a", 64),
		ContentIntent: "launch/content", ContentProof: "proof/test", ContentCandidate: "candidate/test",
		ContentDigestKey: strings.Repeat("b", 64), SizeBytes: 1, MediaType: "text/plain", ProfileRef: "profile/test",
		QuotaRef: "quota/test", RetentionSeconds: 60, Deadline: time.Now().UTC().Add(time.Hour).Format("2006-01-02T15:04:05.000000000Z"),
		Source: operatorsource.Selector{PrincipalRef: source.PrincipalRef, ScopeRef: source.ScopeRef, AuthContext: source.AuthContext, SourceID: source.SourceID, SourceFenceRef: source.SourceFenceRef, Generation: source.Generation},
	}
}
