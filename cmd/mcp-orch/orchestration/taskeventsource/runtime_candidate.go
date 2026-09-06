package taskeventsource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
)

var (
	// ErrRuntimeEventCandidateDisabled keeps this adapter out of production
	// lifecycle wiring until identity and event append share one DB transaction.
	ErrRuntimeEventCandidateDisabled = errors.New("runtime event producer candidate is disabled")
	// ErrRuntimeEventCandidateUnavailable indicates that no durable source state
	// exists from which a sequence can be obtained without inventing one.
	ErrRuntimeEventCandidateUnavailable = errors.New("runtime event producer source state is unavailable")
)

// ActiveSnapshotReader supplies all identity rows from one owner transaction.
// identity.Store implements this contract through GetActiveSnapshot.
type ActiveSnapshotReader interface {
	GetActiveSnapshot(context.Context, string) (identity.ActiveSnapshot, error)
}

// SourceStateReader reads the durable source/lane watermark. It deliberately
// has no AllocateSequence method: this candidate never hand-writes a sequence.
type SourceStateReader interface {
	GetSourceState(context.Context, string, string, eventstore.Lane) (eventstore.SourceState, error)
}

// RuntimeEvent describes a provider/task lifecycle observation without any
// identity or sequence fields. Those fields are always reconstructed from the
// durable owners by RuntimeEventCandidate.
type RuntimeEvent struct {
	AgentID     string
	Lane        eventstore.Lane
	EventID     string
	EventType   string
	Provenance  string
	TrustClass  string
	ContentRole string
	ContentRef  string
	ContentSize int64
	MediaType   string
	ProviderAt  *string
	ObservedAt  string
	WriterEpoch string
}

// RuntimeEventCandidate is a test-owned adapter for a future runtime seam.
// It is not registered in Fx and must be constructed with enabled=false by
// production callers. Even when enabled for tests, identity reads and event
// append are separate transactions, so this type cannot authorize production
// lifecycle events.
type RuntimeEventCandidate struct {
	identities ActiveSnapshotReader
	states     SourceStateReader
	producer   TaskEventProducer
	enabled    bool
}

// NewRuntimeEventCandidate constructs the explicit candidate adapter. Nil or
// typed-nil dependencies fail immediately; production composition must not
// call this constructor with enabled=true.
func NewRuntimeEventCandidate(identities ActiveSnapshotReader, states SourceStateReader, producer TaskEventProducer, enabled bool) (*RuntimeEventCandidate, error) {
	if isNilRuntimeDependency(identities) || isNilRuntimeDependency(states) || isNilRuntimeDependency(producer) {
		return nil, errors.New("runtime event candidate dependencies are required")
	}
	return &RuntimeEventCandidate{identities: identities, states: states, producer: producer, enabled: enabled}, nil
}

// Append resolves a complete identity snapshot, obtains the next durable
// source sequence, computes the envelope hash, and calls the sole producer.
// Missing source state is an error: sequence one must be allocated by a future
// atomic owner rather than guessed here.
func (c *RuntimeEventCandidate) Append(ctx context.Context, event RuntimeEvent) (eventstore.AppendResult, error) {
	if c == nil || isNilRuntimeDependency(c.identities) || isNilRuntimeDependency(c.states) || isNilRuntimeDependency(c.producer) {
		return eventstore.AppendResult{}, errors.New("runtime event candidate dependencies are missing")
	}
	if !c.enabled {
		return eventstore.AppendResult{}, ErrRuntimeEventCandidateDisabled
	}
	if ctx == nil {
		return eventstore.AppendResult{}, errors.New("runtime event candidate context is required")
	}
	if event.AgentID == "" {
		return eventstore.AppendResult{}, errors.New("runtime event candidate agent id is required")
	}
	snapshot, state, err := c.resolveSnapshotAndState(ctx, event)
	if err != nil {
		return eventstore.AppendResult{}, err
	}
	input, err := buildRuntimeEventInput(snapshot, state, event)
	if err != nil {
		return eventstore.AppendResult{}, err
	}
	return c.producer.Append(ctx, input)
}

func (c *RuntimeEventCandidate) resolveSnapshotAndState(ctx context.Context, event RuntimeEvent) (identity.ActiveSnapshot, eventstore.SourceState, error) {
	snapshot, err := c.identities.GetActiveSnapshot(ctx, event.AgentID)
	if err != nil {
		return identity.ActiveSnapshot{}, eventstore.SourceState{}, fmt.Errorf("read active identity snapshot: %w", err)
	}
	source, err := FromCurrentBinding(snapshot.Binding)
	if err != nil {
		return identity.ActiveSnapshot{}, eventstore.SourceState{}, err
	}
	state, err := c.states.GetSourceState(ctx, source.SourceID, source.SourceEpoch, event.Lane)
	if err != nil {
		return identity.ActiveSnapshot{}, eventstore.SourceState{}, errors.Join(ErrRuntimeEventCandidateUnavailable, err)
	}
	if err := validateCandidateSourceState(state, source, event); err != nil {
		return identity.ActiveSnapshot{}, eventstore.SourceState{}, err
	}
	return snapshot, state, nil
}

func validateCandidateSourceState(state eventstore.SourceState, source Source, event RuntimeEvent) error {
	if state.SourceID != source.SourceID || state.SourceEpoch != source.SourceEpoch || state.Lane != event.Lane ||
		state.Status != "active" || state.WriterEpoch != event.WriterEpoch {
		return errors.Join(ErrRuntimeEventCandidateUnavailable, errors.New("source state fence does not match runtime event"))
	}
	if state.NextSequence <= 0 {
		return errors.Join(ErrRuntimeEventCandidateUnavailable, errors.New("source state next sequence must be positive"))
	}
	return nil
}

func buildRuntimeEventInput(snapshot identity.ActiveSnapshot, state eventstore.SourceState, event RuntimeEvent) (EventInput, error) {
	providerAt := cloneStringPtr(event.ProviderAt)
	incarnationID := snapshot.Incarnation.IncarnationID
	envelope := eventstore.EventEnvelope{
		EventID: event.EventID, EventType: event.EventType, SchemaVersion: 1,
		RootAgentID: snapshot.Reservation.AgentID, SubjectAgentID: snapshot.Binding.AgentID,
		ReservationID: snapshot.Reservation.ReservationID, IncarnationID: &incarnationID,
		ExternalEpoch: snapshot.Incarnation.ExternalEpoch, IncarnationGeneration: snapshot.Incarnation.IncarnationGeneration,
		IdentityDigest: snapshot.Incarnation.IdentityDigest, BindingVersion: snapshot.Binding.BindingVersion,
		BindingDigest: snapshot.Binding.BindingDigest, SourceFenceRef: snapshot.Binding.SourceFenceRef,
		Provenance: event.Provenance, TrustClass: event.TrustClass, ContentRole: event.ContentRole,
		ContentRef: event.ContentRef, ContentSize: event.ContentSize, MediaType: event.MediaType,
		ProviderAt: providerAt, ObservedAt: event.ObservedAt, WriterEpoch: event.WriterEpoch,
	}
	hash, err := canonicalEnvelopeHash(envelope)
	if err != nil {
		return EventInput{}, err
	}
	return EventInput{
		Reservation: snapshot.Reservation, Incarnation: snapshot.Incarnation, Binding: snapshot.Binding,
		Lane: event.Lane, SourceSequence: state.NextSequence, EnvelopeHash: hash,
		EventID: event.EventID, EventType: event.EventType, SubjectAgentID: snapshot.Binding.AgentID,
		Provenance: event.Provenance, TrustClass: event.TrustClass, ContentRole: event.ContentRole,
		ContentRef: event.ContentRef, ContentSize: event.ContentSize, MediaType: event.MediaType,
		ProviderAt: providerAt, ObservedAt: event.ObservedAt, WriterEpoch: event.WriterEpoch,
	}, nil
}

// canonicalEnvelopeHash hashes only the canonical JSON envelope. encoding/json
// emits struct fields in declaration order, giving a deterministic digest while
// excluding source sequence and the hash itself from the envelope payload.
func canonicalEnvelopeHash(envelope eventstore.EventEnvelope) (string, error) {
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("marshal canonical event envelope: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func isNilRuntimeDependency(value any) bool {
	if value == nil {
		return true
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	default:
		return false
	}
}
