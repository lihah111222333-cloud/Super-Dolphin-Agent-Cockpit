package taskeventsource

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
)

type recordingAppender struct {
	frame  eventstore.IngressFrame
	called int
}

func (a *recordingAppender) Append(_ context.Context, frame eventstore.IngressFrame) (eventstore.AppendResult, error) {
	a.called++
	a.frame = frame
	return eventstore.AppendResult{}, nil
}

func TestProducerMapsExplicitIdentityAndBindsSource(t *testing.T) {
	identity := producerIdentity()
	appender := &recordingAppender{}
	producer, err := NewProducer(appender)
	if err != nil {
		t.Fatalf("NewProducer() error = %v", err)
	}
	providerAt := "provider-session"
	_, err = producer.Append(context.Background(), producerEventInput(identity, &providerAt))
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if appender.called != 1 {
		t.Fatalf("appender calls = %d, want 1", appender.called)
	}
	assertProducerFrame(t, appender.frame, identity)
	providerAt = "mutated"
	if *appender.frame.Envelope.ProviderAt != "provider-session" {
		t.Fatalf("provider_at pointer aliases caller: %q", *appender.frame.Envelope.ProviderAt)
	}
}

func TestProducerAllowsIndependentIdentityGenerations(t *testing.T) {
	identity := producerIdentity()
	if identity.reservation.ReservationGeneration != 3 || identity.incarnation.IncarnationGeneration != 4 {
		t.Fatalf("fixture generations = reservation:%d incarnation:%d, want 3:4", identity.reservation.ReservationGeneration, identity.incarnation.IncarnationGeneration)
	}
	appender := &recordingAppender{}
	producer, err := NewProducer(appender)
	if err != nil {
		t.Fatalf("NewProducer() error = %v", err)
	}
	if _, err := producer.Append(context.Background(), producerEventInput(identity, nil)); err != nil {
		t.Fatalf("Append(rollover) error = %v, want success", err)
	}
	if appender.called != 1 {
		t.Fatalf("appender calls = %d, want 1", appender.called)
	}
}

// producerEventInput creates one explicit trusted observation fixture for mapping tests.
func producerEventInput(identity producerIdentityFixture, providerAt *string) EventInput {
	return EventInput{
		Reservation: identity.reservation, Incarnation: identity.incarnation, Binding: identity.binding,
		Lane: eventstore.LaneOutput, SourceSequence: 3, EnvelopeHash: strings.Repeat("a", 64),
		EventID: "event-3", EventType: "task.output", SubjectAgentID: identity.binding.AgentID,
		Provenance: "provider-observation", TrustClass: "trusted", ContentRole: "output",
		ContentRef: "content/object-3", ContentSize: 12, MediaType: "application/json",
		ProviderAt: providerAt, ObservedAt: "2026-09-02T00:00:00Z", WriterEpoch: "writer-1",
	}
}

// assertProducerFrame verifies source tuple and cursor are derived from the binding.
func assertProducerFrame(t *testing.T, frame eventstore.IngressFrame, identity producerIdentityFixture) {
	t.Helper()
	assertProducerSource(t, frame)
	assertProducerEnvelopeIdentity(t, frame.Envelope, identity)
}

// assertProducerSource verifies the task source never follows caller-supplied metadata.
func assertProducerSource(t *testing.T, frame eventstore.IngressFrame) {
	t.Helper()
	if frame.SourceID != "task/agent-alpha" || frame.SourceEpoch != "incarnation-alpha" || frame.Lane != eventstore.LaneOutput || frame.SourceSequence != 3 {
		t.Fatalf("frame source binding = %#v", frame)
	}
	if frame.CanonicalCursor != "task/agent-alpha/incarnation-alpha/output/3" {
		t.Fatalf("frame cursor = %q", frame.CanonicalCursor)
	}
}

// assertProducerEnvelopeIdentity verifies all identity fields are copied from owner snapshots.
func assertProducerEnvelopeIdentity(t *testing.T, envelope eventstore.EventEnvelope, identity producerIdentityFixture) {
	t.Helper()
	assertProducerEnvelopeLogicalIdentity(t, envelope, identity)
	assertProducerEnvelopeIncarnation(t, envelope, identity)
	if envelope.BindingVersion != identity.binding.BindingVersion || envelope.BindingDigest != identity.binding.BindingDigest {
		t.Fatalf("envelope binding fence = %#v", envelope)
	}
}

// assertProducerEnvelopeLogicalIdentity verifies root/subject/reservation ownership.
func assertProducerEnvelopeLogicalIdentity(t *testing.T, envelope eventstore.EventEnvelope, identity producerIdentityFixture) {
	t.Helper()
	if envelope.RootAgentID != "agent-alpha" || envelope.SubjectAgentID != "agent-alpha" || envelope.ReservationID != identity.reservation.ReservationID {
		t.Fatalf("envelope logical identity = %#v", envelope)
	}
}

// assertProducerEnvelopeIncarnation verifies immutable external epoch and digest fields.
func assertProducerEnvelopeIncarnation(t *testing.T, envelope eventstore.EventEnvelope, identity producerIdentityFixture) {
	t.Helper()
	if envelope.IncarnationID == nil || *envelope.IncarnationID != identity.incarnation.IncarnationID {
		t.Fatalf("envelope incarnation = %#v", envelope.IncarnationID)
	}
	if envelope.ExternalEpoch != identity.incarnation.ExternalEpoch || envelope.IncarnationGeneration != identity.incarnation.IncarnationGeneration || envelope.IdentityDigest != identity.incarnation.IdentityDigest {
		t.Fatalf("envelope incarnation fence = %#v", envelope)
	}
}

func TestProducerRejectsIdentityDriftBeforeAppender(t *testing.T) {
	base := producerIdentity()
	tests := []struct {
		name   string
		mutate func(*producerIdentityFixture)
	}{
		{name: "reservation agent", mutate: func(v *producerIdentityFixture) { v.reservation.AgentID = "other" }},
		{name: "reservation state", mutate: func(v *producerIdentityFixture) { v.reservation.State = "identity_pending" }},
		{name: "incarnation", mutate: func(v *producerIdentityFixture) { v.incarnation.IncarnationID = "other" }},
		{name: "reservation generation drift", mutate: func(v *producerIdentityFixture) { v.binding.ReservationGeneration++ }},
		{name: "reservation binding identity kind", mutate: func(v *producerIdentityFixture) { v.binding.IdentityKind = "native_subagent" }},
		{name: "reservation incarnation identity kind", mutate: func(v *producerIdentityFixture) { v.incarnation.IdentityKind = "native_subagent" }},
		{name: "incarnation generation drift", mutate: func(v *producerIdentityFixture) { v.binding.IncarnationGeneration++ }},
		{name: "zero reservation generation", mutate: func(v *producerIdentityFixture) {
			v.reservation.ReservationGeneration = 0
			v.binding.ReservationGeneration = 0
		}},
		{name: "zero incarnation generation", mutate: func(v *producerIdentityFixture) {
			v.incarnation.IncarnationGeneration = 0
			v.binding.IncarnationGeneration = 0
		}},
		{name: "binding agent", mutate: func(v *producerIdentityFixture) { v.binding.AgentID = "other-agent" }},
		{name: "subject metadata", mutate: func(v *producerIdentityFixture) { v.subject = "other-agent" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := base
			tt.mutate(&fixture)
			appender := &recordingAppender{}
			producer, err := NewProducer(appender)
			if err != nil {
				t.Fatalf("NewProducer() error = %v", err)
			}
			_, err = producer.Append(context.Background(), EventInput{
				Reservation: fixture.reservation, Incarnation: fixture.incarnation, Binding: fixture.binding,
				Lane: eventstore.LaneControl, SourceSequence: 1, EnvelopeHash: strings.Repeat("a", 64),
				EventID: "event-1", EventType: "task.control", SubjectAgentID: fixture.subject,
				Provenance: "test", TrustClass: "trusted", ContentRole: "none",
				ObservedAt: "2026-09-02T00:00:00Z", WriterEpoch: "writer-1",
			})
			if !errors.Is(err, ErrInvalidSource) {
				t.Fatalf("Append() error = %v, want ErrInvalidSource", err)
			}
			if appender.called != 0 {
				t.Fatalf("appender calls = %d, want 0 on identity rejection", appender.called)
			}
		})
	}
}

// TestProducerRejectsUnsupportedIdentitySnapshotSchemaBeforeAppender 验证不支持的 durable schema 不会触达事件写入口。
func TestProducerRejectsUnsupportedIdentitySnapshotSchemaBeforeAppender(t *testing.T) {
	base := producerIdentity()
	tests := []struct {
		name   string
		mutate func(*producerIdentityFixture)
	}{
		{name: "reservation schema", mutate: func(v *producerIdentityFixture) { v.reservation.SchemaVersion = 2 }},
		{name: "incarnation schema", mutate: func(v *producerIdentityFixture) { v.incarnation.SchemaVersion = 2 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			identity := base
			tt.mutate(&identity)
			appender := &recordingAppender{}
			producer, err := NewProducer(appender)
			if err != nil {
				t.Fatalf("NewProducer() error = %v", err)
			}
			if _, err := producer.Append(context.Background(), producerEventInput(identity, nil)); !errors.Is(err, ErrInvalidSource) {
				t.Fatalf("Append() error = %v, want ErrInvalidSource", err)
			}
			if appender.called != 0 {
				t.Fatalf("appender calls = %d, want 0 for unsupported snapshot schema", appender.called)
			}
		})
	}
}

func TestNewProducerRejectsMissingAppender(t *testing.T) {
	if _, err := NewProducer(nil); err == nil {
		t.Fatal("NewProducer(nil) unexpectedly succeeded")
	}
	var typedNil *recordingAppender
	if _, err := NewProducer(typedNil); err == nil {
		t.Fatal("NewProducer(typed nil) unexpectedly succeeded")
	}
}

func TestProducerRejectsNilContextBeforeAppender(t *testing.T) {
	identity := producerIdentity()
	appender := &recordingAppender{}
	producer, err := NewProducer(appender)
	if err != nil {
		t.Fatalf("NewProducer() error = %v", err)
	}
	_, err = producer.Append(nil, EventInput{
		Reservation: identity.reservation, Incarnation: identity.incarnation, Binding: identity.binding,
		Lane: eventstore.LaneControl, SourceSequence: 1, EnvelopeHash: strings.Repeat("a", 64),
		EventID: "event-1", EventType: "task.control", SubjectAgentID: identity.binding.AgentID,
		Provenance: "test", TrustClass: "trusted", ContentRole: "none",
		ObservedAt: "2026-09-02T00:00:00Z", WriterEpoch: "writer-1",
	})
	if err == nil || !strings.Contains(err.Error(), "context is required") {
		t.Fatalf("Append(nil context) error = %v", err)
	}
	if appender.called != 0 {
		t.Fatalf("appender calls = %d, want 0 for nil context", appender.called)
	}
}

func TestProducerRejectsMalformedEnvelopeBeforeAppender(t *testing.T) {
	identity := producerIdentity()
	appender := &recordingAppender{}
	producer, err := NewProducer(appender)
	if err != nil {
		t.Fatalf("NewProducer() error = %v", err)
	}
	input := producerEventInput(identity, nil)
	input.EnvelopeHash = strings.Repeat("A", 64)
	_, err = producer.Append(context.Background(), input)
	if !errors.Is(err, eventstore.ErrInvalidEvent) {
		t.Fatalf("Append() error = %v, want ErrInvalidEvent", err)
	}
	if appender.called != 0 {
		t.Fatalf("appender calls = %d, want 0 for malformed envelope", appender.called)
	}
}

type producerIdentityFixture struct {
	reservation identity.Reservation
	incarnation identity.Incarnation
	binding     identity.CurrentBinding
	subject     string
}

func producerIdentity() producerIdentityFixture {
	digest := strings.Repeat("a", 64)
	reservation := identity.Reservation{
		ReservationID: "reservation-alpha", AgentID: "agent-alpha", IdentityKind: "managed_agent",
		SourceKind: "first_launch", CallerPrincipalRef: "principal-alpha", ScopeRef: "scope-alpha",
		IdempotencyNamespace: "test", IdempotencyKeyDigest: digest, RequestDigest: digest,
		SourceFenceRef: "source-fence-alpha", TargetFenceRef: "target-fence-alpha",
		ReservationGeneration: 3, State: "activated", SchemaVersion: 1,
	}
	incarnation := identity.Incarnation{
		IncarnationID: "incarnation-alpha", ReservationID: reservation.ReservationID, AgentID: reservation.AgentID,
		IdentityKind: reservation.IdentityKind, ExternalIdentityRef: "external-alpha", ExternalEpoch: "external-epoch-alpha",
		SourceFenceRef: reservation.SourceFenceRef, IdentityDigest: strings.Repeat("b", 64), IncarnationGeneration: 4, SchemaVersion: 1,
	}
	binding := identity.CurrentBinding{
		AgentID: reservation.AgentID, BindingVersion: 7, ReservationID: reservation.ReservationID,
		IncarnationID: incarnation.IncarnationID, IdentityKind: reservation.IdentityKind,
		ReservationGeneration: reservation.ReservationGeneration, IncarnationGeneration: incarnation.IncarnationGeneration,
		ExternalIdentityRef: incarnation.ExternalIdentityRef, SourceFenceRef: reservation.SourceFenceRef,
		BindingDigest: strings.Repeat("c", 64), State: "active", SchemaVersion: 1,
	}
	return producerIdentityFixture{reservation: reservation, incarnation: incarnation, binding: binding, subject: reservation.AgentID}
}

var _ Appender = (*recordingAppender)(nil)
