package launchassembler

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/operatorreservation"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/operatorsource"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/orchestrationcommand"
	sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
)

type launchFixture struct {
	db        *sql.DB
	assembler *Assembler
	source    operatorsource.Source
	request   Request
}

// TestLaunchAssemblerRealContract checks the real SQLite intent and source owners.
func TestLaunchAssemblerRealContract(t *testing.T) {
	fixture := newLaunchFixture(t)
	first, err := fixture.assembler.Assemble(t.Context(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.assembler.Assemble(t.Context(), fixture.request)
	if err != nil || first != second {
		t.Fatalf("replay=%#v err=%v", second, err)
	}
	assertLaunchMutations(t, fixture.assembler, fixture.request)
	if _, err := fixture.db.ExecContext(t.Context(), `UPDATE orchestration_operator_sources SET state='revoked', revoked_at=? WHERE source_id=?`, fixedNow(), fixture.source.SourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.assembler.Assemble(t.Context(), fixture.request); err == nil {
		t.Fatal("revoked source accepted")
	}
}

func newLaunchFixture(t *testing.T) launchFixture {
	t.Helper()
	db, err := sqliteruntime.Open(t.Context(), sqliteruntime.OpenOptions{Path: filepath.Join(t.TempDir(), "launch.sqlite")})
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
	source, err := sources.Activate(t.Context(), operatorsource.ActivateRequest{PrincipalRef: "launch/principal", ScopeRef: "launch/scope", AuthContext: "launch/auth", ExpectedGeneration: 0})
	if err != nil {
		t.Fatal(err)
	}
	reservations, err := operatorreservation.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	request := launchRequest(source)
	if _, err := reservations.Create(t.Context(), reservationRequest(source)); err != nil {
		t.Fatal(err)
	}
	commands, err := orchestrationcommand.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	assembler, err := New(commands, reservations)
	if err != nil {
		t.Fatal(err)
	}
	return launchFixture{db: db, assembler: assembler, source: source, request: request}
}

func launchRequest(source operatorsource.Source) Request {
	return Request{CommandID: "launch-command-1", CallerPrincipalRef: source.PrincipalRef, ScopeRef: source.ScopeRef, IdempotencyNamespace: "launch/ns", IdempotencyDigest: strings.Repeat("a", 64), RequestDigest: strings.Repeat("b", 64), DigestKeyID: "launch-key", ContentRef: "content://operator-launch", SourceFenceRef: source.SourceFenceRef, TargetFenceRef: source.SourceFenceRef, OperatorReservationID: "launch-reservation-1", OperatorSourceID: source.SourceID, OperatorGeneration: source.Generation, ContentKind: "operator_content_ref", Profile: "operator-launch-v1"}
}

func reservationRequest(s operatorsource.Source) operatorreservation.Request {
	return operatorreservation.Request{ReservationID: "launch-reservation-1", SourceID: s.SourceID, SourceFenceRef: s.SourceFenceRef, PrincipalRef: s.PrincipalRef, ScopeRef: s.ScopeRef, AuthContext: s.AuthContext, Generation: s.Generation, WorkspaceRef: "launch/workspace", CallerPrincipalRef: s.PrincipalRef, IdempotencyNamespace: "launch/ns", IdempotencyKeyDigest: strings.Repeat("a", 64), ContentIntent: "launch", ContentProof: "proof", ContentCandidate: "content://operator-launch", ContentDigestKey: strings.Repeat("c", 64), SizeBytes: 1, MediaType: "text/plain", ProfileRef: "operator-launch-v1", QuotaRef: "launch/quota", RetentionSeconds: 60, Deadline: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02T15:04:05.000000000Z"), Source: operatorsource.Selector{SourceID: s.SourceID, SourceFenceRef: s.SourceFenceRef, PrincipalRef: s.PrincipalRef, ScopeRef: s.ScopeRef, AuthContext: s.AuthContext, Generation: s.Generation}}
}

func assertLaunchMutations(t *testing.T, a *Assembler, base Request) {
	t.Helper()
	mutations := []func(*Request){func(r *Request) { r.ContentRef = "other" }, func(r *Request) { r.Profile = "other" }, func(r *Request) { r.OperatorSourceID = "other" }, func(r *Request) { r.OperatorGeneration++ }, func(r *Request) { r.CallerPrincipalRef = "other" }, func(r *Request) { r.ScopeRef = "other" }}
	for _, mutate := range mutations {
		request := base
		mutate(&request)
		if _, err := a.Assemble(t.Context(), request); err == nil {
			t.Fatal("mutation accepted")
		}
	}
}
func fixedNow() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z")
}
