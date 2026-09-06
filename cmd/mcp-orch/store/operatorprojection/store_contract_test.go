package operatorprojection

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/operatorreservation"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/operatorsource"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
)

func TestOperatorProjectionStoreContract(t *testing.T) {
	db, reservation := projectionFixture(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z")
	object, err := store.CreatePrepared(t.Context(), sqlc.CreatePreparedObjectParams{
		ObjectID: "operator-object-1", ReservationID: reservation.ReservationID, SourceID: reservation.SourceID, SourceGeneration: reservation.Generation,
		AeadAlgorithm: "aes-256-gcm", KeyWrapProfile: "operator/v1", KeyHandleRef: "handle/1", KeyReceiptRef: "receipt/1",
		KeyReceiptDigest: strings.Repeat("a", 64), NonceHex: strings.Repeat("b", 24), AadProfile: "orch.content.operator.single.aad/v1",
		AadDigest: strings.Repeat("c", 64), MediaType: "text/plain", SizeBytes: 1, CiphertextSha256: strings.Repeat("d", 64),
		ObjectReceiptRef: "object-receipt/1", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if object.ContentState != "prepared" || object.RefVersion != 0 {
		t.Fatalf("prepared=%#v", object)
	}
	ref, err := store.PromoteAndCreateRef(t.Context(), object.ObjectID, sqlc.CreateActiveRefParams{
		ReferenceID: "operator-ref-1", CanonicalRef: "content://operator-ref-1", ObjectID: object.ObjectID,
		ReservationID: reservation.ReservationID, SourceID: reservation.SourceID, SourceGeneration: reservation.Generation,
		CallerPrincipalRef: reservation.CallerPrincipalRef, ScopeRef: reservation.ScopeRef, ProofDigest: strings.Repeat("e", 64),
		ValidUntil: time.Now().UTC().Add(time.Hour).Format("2006-01-02T15:04:05.000000000Z"), CreatedAt: now,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := store.ResolveActiveRef(t.Context(), ref.CanonicalRef)
	if err != nil || resolved.ReferenceID != ref.ReferenceID {
		t.Fatalf("resolve=%#v err=%v", resolved, err)
	}
}

func projectionFixture(t *testing.T) (*sql.DB, sqlc.OrchestrationOperatorReservation) {
	t.Helper()
	db, err := sqliteruntime.Open(t.Context(), sqliteruntime.OpenOptions{Path: filepath.Join(t.TempDir(), "projection.sqlite")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqliteruntime.RunEmbeddedMigrations(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	sources, err := operatorsource.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	source, err := sources.Activate(t.Context(), operatorsource.ActivateRequest{PrincipalRef: "projection/operator", ScopeRef: "projection/scope", AuthContext: "projection/auth", ExpectedGeneration: 0})
	if err != nil {
		t.Fatal(err)
	}
	reservations, err := operatorreservation.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	request := operatorreservation.Request{ReservationID: "operator-reservation-1", SourceID: source.SourceID, SourceFenceRef: source.SourceFenceRef, PrincipalRef: source.PrincipalRef, ScopeRef: source.ScopeRef, AuthContext: source.AuthContext, Generation: source.Generation, WorkspaceRef: "projection/workspace", CallerPrincipalRef: source.PrincipalRef, IdempotencyNamespace: "projection/idempotency", IdempotencyKeyDigest: strings.Repeat("1", 64), ContentIntent: "projection/content", ContentProof: "projection/proof", ContentCandidate: "projection/candidate", ContentDigestKey: strings.Repeat("2", 64), SizeBytes: 1, MediaType: "text/plain", ProfileRef: "projection/profile", QuotaRef: "projection/quota", RetentionSeconds: 60, Deadline: time.Now().UTC().Add(time.Hour).Format("2006-01-02T15:04:05.000000000Z"), Source: operatorsource.Selector{SourceID: source.SourceID, SourceFenceRef: source.SourceFenceRef, PrincipalRef: source.PrincipalRef, ScopeRef: source.ScopeRef, AuthContext: source.AuthContext, Generation: source.Generation}}
	reservation, err := reservations.Create(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	return db, reservation.OrchestrationOperatorReservation
}
