package eventstore

import storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"

func ingressReceiptParams(frame IngressFrame, status, errorRef string) storedb.InsertOrchestrationEventIngressReceiptParams {
	e := frame.Envelope
	return storedb.InsertOrchestrationEventIngressReceiptParams{
		SourceID: frame.SourceID, SourceEpoch: frame.SourceEpoch, Lane: string(frame.Lane), SourceSequence: frame.SourceSequence,
		EventID: e.EventID, EnvelopeHash: frame.EnvelopeHash, Status: status, EventType: e.EventType, SchemaVersion: e.SchemaVersion,
		RootAgentID: e.RootAgentID, SubjectAgentID: e.SubjectAgentID, ReservationID: e.ReservationID, IncarnationID: e.IncarnationID,
		ExternalEpoch: e.ExternalEpoch, IncarnationGeneration: e.IncarnationGeneration, IdentityDigest: e.IdentityDigest,
		BindingVersion: e.BindingVersion, BindingDigest: e.BindingDigest, SourceFenceRef: e.SourceFenceRef,
		Provenance: e.Provenance, TrustClass: e.TrustClass, ContentRole: e.ContentRole, ContentRef: e.ContentRef,
		ContentSize: e.ContentSize, MediaType: e.MediaType, ProviderAt: e.ProviderAt, ObservedAt: e.ObservedAt,
		WriterEpoch: e.WriterEpoch, CanonicalCursor: frame.CanonicalCursor, ErrorRef: errorRef,
	}
}

func agentEventParams(frame IngressFrame) storedb.InsertOrchestrationAgentEventParams {
	e := frame.Envelope
	return storedb.InsertOrchestrationAgentEventParams{
		EventID: e.EventID, SourceID: frame.SourceID, SourceEpoch: frame.SourceEpoch, Lane: string(frame.Lane),
		SourceSequence: frame.SourceSequence, CanonicalCursor: frame.CanonicalCursor, EnvelopeHash: frame.EnvelopeHash,
		EventType: e.EventType, SchemaVersion: e.SchemaVersion, RootAgentID: e.RootAgentID, SubjectAgentID: e.SubjectAgentID,
		ReservationID: e.ReservationID, IncarnationID: e.IncarnationID, ExternalEpoch: e.ExternalEpoch,
		IncarnationGeneration: e.IncarnationGeneration, IdentityDigest: e.IdentityDigest, BindingVersion: e.BindingVersion,
		BindingDigest: e.BindingDigest, SourceFenceRef: e.SourceFenceRef, Provenance: e.Provenance, TrustClass: e.TrustClass,
		ContentRole: e.ContentRole, ContentRef: e.ContentRef, ContentSize: e.ContentSize, MediaType: e.MediaType,
		ProviderAt: e.ProviderAt, ObservedAt: e.ObservedAt, WriterEpoch: e.WriterEpoch,
	}
}
