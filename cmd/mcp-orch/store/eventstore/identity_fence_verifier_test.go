package eventstore

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	_ "modernc.org/sqlite"
)

type verifierIdentityReader struct {
	reservation identity.Reservation
	incarnation identity.Incarnation
	current     identity.CurrentBinding
}

func (r verifierIdentityReader) Get(context.Context, string) (identity.Reservation, error) {
	return r.reservation, nil
}

func (r verifierIdentityReader) GetIncarnation(context.Context, string) (identity.Incarnation, error) {
	return r.incarnation, nil
}

func (r verifierIdentityReader) GetCurrent(context.Context, string) (identity.CurrentBinding, error) {
	return r.current, nil
}

func TestIdentityFenceVerifierAllowsIndependentGenerations(t *testing.T) {
	reader, envelope := independentVerifierFixture()
	verifier, err := NewIdentityFenceVerifier(reader)
	if err != nil {
		t.Fatalf("NewIdentityFenceVerifier() error = %v", err)
	}
	if err := verifier.VerifyEventFence(context.Background(), envelope); err != nil {
		t.Fatalf("VerifyEventFence(rollover) error = %v, want success", err)
	}
}

func TestIdentityFenceVerifierRejectsGenerationDrift(t *testing.T) {
	baseReader, envelope := independentVerifierFixture()
	cases := []struct {
		name   string
		mutate func(*verifierIdentityReader, *EventEnvelope)
	}{
		{name: "reservation generation rollback", mutate: func(reader *verifierIdentityReader, _ *EventEnvelope) {
			reader.current.ReservationGeneration--
		}},
		{name: "incarnation generation rollback", mutate: func(reader *verifierIdentityReader, _ *EventEnvelope) {
			reader.current.IncarnationGeneration--
		}},
		{name: "envelope incarnation generation", mutate: func(_ *verifierIdentityReader, value *EventEnvelope) {
			value.IncarnationGeneration--
		}},
		{name: "zero reservation generation", mutate: func(reader *verifierIdentityReader, _ *EventEnvelope) {
			reader.reservation.ReservationGeneration = 0
		}},
		{name: "zero incarnation generation", mutate: func(reader *verifierIdentityReader, _ *EventEnvelope) {
			reader.incarnation.IncarnationGeneration = 0
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reader, value := baseReader, envelope
			tc.mutate(&reader, &value)
			verifier, err := NewIdentityFenceVerifier(reader)
			if err != nil {
				t.Fatalf("NewIdentityFenceVerifier() error = %v", err)
			}
			if err := verifier.VerifyEventFence(context.Background(), value); !errors.Is(err, ErrEventFence) {
				t.Fatalf("VerifyEventFence() error = %v, want ErrEventFence", err)
			}
		})
	}
}

func TestIdentityFenceVerifierTxUsesCallerTransactionAndRejectsBareDB(t *testing.T) {
	base, envelope := independentVerifierFixture()
	reader := &verifierTxIdentityReader{verifierIdentityReader: base}
	verifier, err := NewIdentityFenceVerifier(reader)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:eventstore-verifier-tx?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	assertVerifierTxReads(t, verifier, reader, tx, envelope)
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	reader.txCalls = nil
	assertVerifierRejectsBareDB(t, verifier, reader, db, envelope)
}

func assertVerifierTxReads(t *testing.T, verifier *IdentityFenceVerifier, reader *verifierTxIdentityReader, tx *sql.Tx, envelope EventEnvelope) {
	t.Helper()
	if err := verifier.VerifyEventFenceTx(t.Context(), tx, envelope); err != nil {
		_ = tx.Rollback()
		t.Fatalf("VerifyEventFenceTx() error = %v", err)
	}
	if len(reader.txCalls) != 3 || reader.txCalls[0] != tx || reader.txCalls[1] != tx || reader.txCalls[2] != tx {
		_ = tx.Rollback()
		t.Fatalf("transaction calls = %#v, want caller transaction for all reads", reader.txCalls)
	}
}

func assertVerifierRejectsBareDB(t *testing.T, verifier *IdentityFenceVerifier, reader *verifierTxIdentityReader, db *sql.DB, envelope EventEnvelope) {
	t.Helper()
	if err := verifier.VerifyEventFenceTx(t.Context(), db, envelope); err == nil {
		t.Fatal("VerifyEventFenceTx() unexpectedly accepted bare database")
	}
	if len(reader.txCalls) != 0 {
		t.Fatalf("bare database reached reader: %#v", reader.txCalls)
	}
}

type verifierTxIdentityReader struct {
	verifierIdentityReader
	txCalls []storedb.DBTX
}

func (r *verifierTxIdentityReader) GetWithDBTX(_ context.Context, dbtx storedb.DBTX, _ string) (identity.Reservation, error) {
	r.txCalls = append(r.txCalls, dbtx)
	return r.reservation, nil
}

func (r *verifierTxIdentityReader) GetIncarnationWithDBTX(_ context.Context, dbtx storedb.DBTX, _ string) (identity.Incarnation, error) {
	r.txCalls = append(r.txCalls, dbtx)
	return r.incarnation, nil
}

func (r *verifierTxIdentityReader) GetCurrentWithDBTX(_ context.Context, dbtx storedb.DBTX, _ string) (identity.CurrentBinding, error) {
	r.txCalls = append(r.txCalls, dbtx)
	return r.current, nil
}

func independentVerifierFixture() (verifierIdentityReader, EventEnvelope) {
	reservation := identity.Reservation{
		ReservationID: "reservation-rollover", AgentID: "agent-rollover", IdentityKind: "managed_agent",
		SourceFenceRef: "source-fence-rollover", ReservationGeneration: 3, State: "activated",
	}
	incarnation := identity.Incarnation{
		IncarnationID: "incarnation-rollover", ReservationID: reservation.ReservationID, AgentID: reservation.AgentID,
		IdentityKind: reservation.IdentityKind, ExternalIdentityRef: "external-rollover", ExternalEpoch: "external-epoch-rollover",
		SourceFenceRef: reservation.SourceFenceRef, IdentityDigest: strings.Repeat("b", 64), IncarnationGeneration: 4,
	}
	current := identity.CurrentBinding{
		AgentID: reservation.AgentID, BindingVersion: 7, ReservationID: reservation.ReservationID,
		IncarnationID: incarnation.IncarnationID, IdentityKind: reservation.IdentityKind,
		ReservationGeneration: reservation.ReservationGeneration, IncarnationGeneration: incarnation.IncarnationGeneration,
		ExternalIdentityRef: incarnation.ExternalIdentityRef, SourceFenceRef: reservation.SourceFenceRef,
		BindingDigest: strings.Repeat("c", 64), State: "active",
	}
	id := incarnation.IncarnationID
	envelope := EventEnvelope{
		RootAgentID: reservation.AgentID, SubjectAgentID: reservation.AgentID, ReservationID: reservation.ReservationID,
		IncarnationID: &id, ExternalEpoch: incarnation.ExternalEpoch, IncarnationGeneration: incarnation.IncarnationGeneration,
		IdentityDigest: incarnation.IdentityDigest, BindingVersion: current.BindingVersion, BindingDigest: current.BindingDigest,
		SourceFenceRef: reservation.SourceFenceRef,
	}
	return verifierIdentityReader{reservation: reservation, incarnation: incarnation, current: current}, envelope
}
