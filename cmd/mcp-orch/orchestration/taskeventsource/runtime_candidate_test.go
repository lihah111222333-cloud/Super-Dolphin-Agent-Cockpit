package taskeventsource

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
)

type candidateIdentityReader struct {
	snapshot identity.ActiveSnapshot
	err      error
}

func (r candidateIdentityReader) GetActiveSnapshot(context.Context, string) (identity.ActiveSnapshot, error) {
	return r.snapshot, r.err
}

type candidateStateReader struct {
	state eventstore.SourceState
	err   error
}

func (r candidateStateReader) GetSourceState(context.Context, string, string, eventstore.Lane) (eventstore.SourceState, error) {
	return r.state, r.err
}

type candidateProducer struct {
	input EventInput
	calls int
}

func (p *candidateProducer) Append(_ context.Context, input EventInput) (eventstore.AppendResult, error) {
	p.input = input
	p.calls++
	return eventstore.AppendResult{}, nil
}

func TestRuntimeEventCandidateIsDisabledByDefault(t *testing.T) {
	producer := &candidateProducer{}
	candidate, err := NewRuntimeEventCandidate(candidateFixtureReader(), candidateFixtureStateReader(), producer, false)
	if err != nil {
		t.Fatalf("NewRuntimeEventCandidate() error = %v", err)
	}
	if _, err := candidate.Append(context.Background(), candidateRuntimeEvent()); !errors.Is(err, ErrRuntimeEventCandidateDisabled) {
		t.Fatalf("Append() error = %v, want disabled", err)
	}
	if producer.calls != 0 {
		t.Fatalf("producer calls = %d, want 0 while disabled", producer.calls)
	}
}

func TestRuntimeEventCandidateBuildsIdentitySequenceAndHash(t *testing.T) {
	producer := &candidateProducer{}
	candidate, err := NewRuntimeEventCandidate(candidateFixtureReader(), candidateFixtureStateReader(), producer, true)
	if err != nil {
		t.Fatalf("NewRuntimeEventCandidate() error = %v", err)
	}
	if _, err := candidate.Append(context.Background(), candidateRuntimeEvent()); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if producer.calls != 1 {
		t.Fatalf("producer calls = %d, want 1", producer.calls)
	}
	if producer.input.SourceSequence != 7 || producer.input.Lane != eventstore.LaneOutput {
		t.Fatalf("producer input sequence/lane = %d/%q, want 7/output", producer.input.SourceSequence, producer.input.Lane)
	}
	assertCandidateIdentity(t, producer.input)
	assertCandidateHash(t, producer.input.EnvelopeHash)
}

func assertCandidateIdentity(t *testing.T, input EventInput) {
	t.Helper()
	if input.SubjectAgentID != "candidate-agent" {
		t.Fatalf("subject agent = %q", input.SubjectAgentID)
	}
	if input.Reservation.AgentID != "candidate-agent" {
		t.Fatalf("reservation agent = %q", input.Reservation.AgentID)
	}
	if input.Incarnation.IncarnationID != "candidate-incarnation" {
		t.Fatalf("incarnation id = %q", input.Incarnation.IncarnationID)
	}
}

func assertCandidateHash(t *testing.T, hash string) {
	t.Helper()
	if len(hash) != 64 {
		t.Fatalf("envelope hash length = %d", len(hash))
	}
	if strings.ToLower(hash) != hash {
		t.Fatalf("envelope hash is not lowercase: %q", hash)
	}
}

func TestRuntimeEventCandidateCallsCanonicalProducer(t *testing.T) {
	appender := &recordingAppender{}
	producer, err := NewProducer(appender)
	if err != nil {
		t.Fatalf("NewProducer() error = %v", err)
	}
	candidate, err := NewRuntimeEventCandidate(candidateFixtureReader(), candidateFixtureStateReader(), producer, true)
	if err != nil {
		t.Fatalf("NewRuntimeEventCandidate() error = %v", err)
	}
	if _, err := candidate.Append(context.Background(), candidateRuntimeEvent()); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if appender.called != 1 {
		t.Fatalf("canonical appender calls = %d, want 1", appender.called)
	}
	if appender.frame.SourceSequence != 7 || appender.frame.Envelope.EventID != "candidate-event" {
		t.Fatalf("canonical frame = %#v", appender.frame)
	}
	wantHash, err := canonicalEnvelopeHash(appender.frame.Envelope)
	if err != nil {
		t.Fatalf("canonicalEnvelopeHash() error = %v", err)
	}
	if appender.frame.EnvelopeHash != wantHash {
		t.Fatalf("canonical envelope hash = %q, want %q", appender.frame.EnvelopeHash, wantHash)
	}
}

func TestRuntimeEventCandidateRejectsMissingSourceStateWithoutAppend(t *testing.T) {
	producer := &candidateProducer{}
	stateErr := errors.New("source state not found")
	candidate, err := NewRuntimeEventCandidate(candidateFixtureReader(), candidateStateReader{err: stateErr}, producer, true)
	if err != nil {
		t.Fatalf("NewRuntimeEventCandidate() error = %v", err)
	}
	if _, err := candidate.Append(context.Background(), candidateRuntimeEvent()); !errors.Is(err, ErrRuntimeEventCandidateUnavailable) {
		t.Fatalf("Append() error = %v, want source unavailable", err)
	}
	if producer.calls != 0 {
		t.Fatalf("producer calls = %d, want 0 when durable sequence is unavailable", producer.calls)
	}
}

func TestRuntimeEventCandidateRejectsGapSourceWithoutAppend(t *testing.T) {
	producer := &candidateProducer{}
	state := candidateFixtureStateReader()
	state.state.Status = "gap"
	candidate, err := NewRuntimeEventCandidate(candidateFixtureReader(), state, producer, true)
	if err != nil {
		t.Fatalf("NewRuntimeEventCandidate() error = %v", err)
	}
	if _, err := candidate.Append(context.Background(), candidateRuntimeEvent()); !errors.Is(err, ErrRuntimeEventCandidateUnavailable) {
		t.Fatalf("Append() error = %v, want source unavailable", err)
	}
	if producer.calls != 0 {
		t.Fatalf("producer calls = %d, want 0 for gap source", producer.calls)
	}
}

func TestRuntimeEventCandidateRejectsNilDependencies(t *testing.T) {
	reader := candidateFixtureReader()
	state := candidateFixtureStateReader()
	producer := &candidateProducer{}
	if _, err := NewRuntimeEventCandidate(nil, state, producer, true); err == nil {
		t.Fatal("nil identity reader unexpectedly accepted")
	}
	if _, err := NewRuntimeEventCandidate(reader, nil, producer, true); err == nil {
		t.Fatal("nil state reader unexpectedly accepted")
	}
	if _, err := NewRuntimeEventCandidate(reader, state, nil, true); err == nil {
		t.Fatal("nil producer unexpectedly accepted")
	}
}

func candidateFixtureReader() candidateIdentityReader {
	digest := strings.Repeat("a", 64)
	return candidateIdentityReader{snapshot: identity.ActiveSnapshot{
		Reservation: identity.Reservation{ReservationID: "candidate-reservation", AgentID: "candidate-agent", IdentityKind: "native_subagent", SourceFenceRef: "candidate-fence", ReservationGeneration: 2, State: "activated", SchemaVersion: 1},
		Incarnation: identity.Incarnation{IncarnationID: "candidate-incarnation", ReservationID: "candidate-reservation", AgentID: "candidate-agent", IdentityKind: "native_subagent", ExternalIdentityRef: "provider-session", ExternalEpoch: "provider-epoch", SourceFenceRef: "candidate-fence", IdentityDigest: digest, IncarnationGeneration: 3, SchemaVersion: 1},
		Binding:     identity.CurrentBinding{AgentID: "candidate-agent", BindingVersion: 4, ReservationID: "candidate-reservation", IncarnationID: "candidate-incarnation", IdentityKind: "native_subagent", ReservationGeneration: 2, IncarnationGeneration: 3, ExternalIdentityRef: "provider-session", SourceFenceRef: "candidate-fence", BindingDigest: digest, State: "active", SchemaVersion: 1},
	}}
}

func candidateFixtureStateReader() candidateStateReader {
	return candidateStateReader{state: eventstore.SourceState{SourceID: "task/candidate-agent", SourceEpoch: "candidate-incarnation", Lane: eventstore.LaneOutput, NextSequence: 7, MaterializedSequence: 6, Revision: 6, Status: "active", WriterEpoch: "writer-1"}}
}

func candidateRuntimeEvent() RuntimeEvent {
	return RuntimeEvent{AgentID: "candidate-agent", Lane: eventstore.LaneOutput, EventID: "candidate-event", EventType: "task.output", Provenance: "provider-observation", TrustClass: "trusted", ContentRole: "none", ObservedAt: "2026-09-03T00:00:00Z", WriterEpoch: "writer-1"}
}
