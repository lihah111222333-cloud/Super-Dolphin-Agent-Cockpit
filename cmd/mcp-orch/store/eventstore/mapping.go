package eventstore

import (
	"fmt"
	"time"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

// sourceStateFromRow 将 SQLC source state 行映射并校验单调水位。
func sourceStateFromRow(row storedb.OrchestrationEventSourceState) (SourceState, error) {
	if err := validateSourceStateRow(row); err != nil {
		return SourceState{}, err
	}
	return SourceState{SourceID: row.SourceID, SourceEpoch: row.SourceEpoch, Lane: Lane(row.Lane),
		NextSequence: row.NextSequence, MaterializedSequence: row.MaterializedSequence,
		DedupFloor: row.DedupFloor, Revision: row.Revision, Status: row.Status,
		GapReason: row.GapReason, WriterEpoch: row.WriterEpoch, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}, nil
}

// validateSourceStateRow 在公开读取前校验 source identity、水位和审计字段。
func validateSourceStateRow(row storedb.OrchestrationEventSourceState) error {
	if err := validateSourceStateIdentity(row); err != nil {
		return err
	}
	if err := validateSourceStateWatermarks(row); err != nil {
		return err
	}
	return validateSourceStateTimes(row)
}

// validateSourceStateIdentity 校验 source、epoch、lane 和 writer 身份字段。
func validateSourceStateIdentity(row storedb.OrchestrationEventSourceState) error {
	if err := validateText("source_id", row.SourceID, 256, false); err != nil {
		return err
	}
	if err := validateText("source_epoch", row.SourceEpoch, 128, false); err != nil {
		return err
	}
	if !validLane(Lane(row.Lane)) || !knownSourceStatus(row.Status) {
		return invalidEvent("stored source identity or status is invalid")
	}
	if err := validateText("gap_reason", row.GapReason, 512, true); err != nil {
		return err
	}
	return validateText("writer_epoch", row.WriterEpoch, 128, false)
}

// validateSourceStateWatermarks 校验 source 水位单调关系和 gap 说明。
func validateSourceStateWatermarks(row storedb.OrchestrationEventSourceState) error {
	if row.NextSequence < 1 || row.MaterializedSequence < 0 || row.DedupFloor < 0 || row.Revision < 1 ||
		row.MaterializedSequence >= row.NextSequence || row.NextSequence != row.MaterializedSequence+1 ||
		row.DedupFloor > row.MaterializedSequence {
		return invalidEvent("stored source state has invalid watermarks")
	}
	if row.Status == "gap" && row.GapReason == "" {
		return invalidEvent("stored gap source state lacks reason")
	}
	return nil
}

func validateSourceStateTimes(row storedb.OrchestrationEventSourceState) error {
	for _, field := range []struct{ name, value string }{{"created_at", row.CreatedAt}, {"updated_at", row.UpdatedAt}} {
		if err := validateTimestamp(field.name, field.value); err != nil {
			return err
		}
	}
	return nil
}

// ingressReceiptFromRow 将 SQLC ingress 行映射为接收回执并校验 envelope。
func ingressReceiptFromRow(row storedb.OrchestrationEventIngressReceipt) (IngressReceipt, error) {
	frame, err := frameFromReceiptRow(row)
	if err != nil {
		return IngressReceipt{}, err
	}
	if !knownReceiptStatus(row.Status) || len(row.ErrorRef) > 512 {
		return IngressReceipt{}, invalidEvent("stored ingress receipt status or error is invalid")
	}
	return IngressReceipt{IngressFrame: frame, Status: row.Status, ErrorRef: row.ErrorRef}, nil
}

// agentEventFromRow 将 SQLC canonical 行映射为不可变事件。
func agentEventFromRow(row storedb.OrchestrationAgentEvent) (AgentEvent, error) {
	frame, err := frameFromEventRow(row)
	if err != nil {
		return AgentEvent{}, err
	}
	if row.Status != "materialized" {
		return AgentEvent{}, invalidEvent("stored canonical event is not materialized")
	}
	return AgentEvent{IngressFrame: frame, Status: row.Status}, nil
}

func frameFromReceiptRow(row storedb.OrchestrationEventIngressReceipt) (IngressFrame, error) {
	return validateAndBuildFrame(IngressFrame{SourceID: row.SourceID, SourceEpoch: row.SourceEpoch, Lane: Lane(row.Lane),
		SourceSequence: row.SourceSequence, EnvelopeHash: row.EnvelopeHash, CanonicalCursor: row.CanonicalCursor,
		Envelope: EventEnvelope{EventID: row.EventID, EventType: row.EventType, SchemaVersion: row.SchemaVersion,
			RootAgentID: row.RootAgentID, SubjectAgentID: row.SubjectAgentID, ReservationID: row.ReservationID,
			IncarnationID: row.IncarnationID, ExternalEpoch: row.ExternalEpoch, IncarnationGeneration: row.IncarnationGeneration,
			IdentityDigest: row.IdentityDigest, BindingVersion: row.BindingVersion, BindingDigest: row.BindingDigest,
			SourceFenceRef: row.SourceFenceRef, Provenance: row.Provenance, TrustClass: row.TrustClass,
			ContentRole: row.ContentRole, ContentRef: row.ContentRef, ContentSize: row.ContentSize,
			MediaType: row.MediaType, ProviderAt: row.ProviderAt, ObservedAt: row.ObservedAt, WriterEpoch: row.WriterEpoch}})
}

func frameFromEventRow(row storedb.OrchestrationAgentEvent) (IngressFrame, error) {
	return validateAndBuildFrame(IngressFrame{SourceID: row.SourceID, SourceEpoch: row.SourceEpoch, Lane: Lane(row.Lane),
		SourceSequence: row.SourceSequence, EnvelopeHash: row.EnvelopeHash, CanonicalCursor: row.CanonicalCursor,
		Envelope: EventEnvelope{EventID: row.EventID, EventType: row.EventType, SchemaVersion: row.SchemaVersion,
			RootAgentID: row.RootAgentID, SubjectAgentID: row.SubjectAgentID, ReservationID: row.ReservationID,
			IncarnationID: row.IncarnationID, ExternalEpoch: row.ExternalEpoch, IncarnationGeneration: row.IncarnationGeneration,
			IdentityDigest: row.IdentityDigest, BindingVersion: row.BindingVersion, BindingDigest: row.BindingDigest,
			SourceFenceRef: row.SourceFenceRef, Provenance: row.Provenance, TrustClass: row.TrustClass,
			ContentRole: row.ContentRole, ContentRef: row.ContentRef, ContentSize: row.ContentSize,
			MediaType: row.MediaType, ProviderAt: row.ProviderAt, ObservedAt: row.ObservedAt, WriterEpoch: row.WriterEpoch}})
}

func validateAndBuildFrame(frame IngressFrame) (IngressFrame, error) {
	if err := frame.Validate(); err != nil {
		return IngressFrame{}, err
	}
	return frame, nil
}

// materializationCursorFromRow 将 SQLC cursor 行映射并校验投影水位。
func materializationCursorFromRow(row storedb.OrchestrationEventMaterializationCursor) (MaterializationCursor, error) {
	if err := validateText("projection_id", row.ProjectionID, 128, false); err != nil {
		return MaterializationCursor{}, err
	}
	if err := validateText("source_id", row.SourceID, 256, false); err != nil {
		return MaterializationCursor{}, err
	}
	if err := validateText("source_epoch", row.SourceEpoch, 128, false); err != nil {
		return MaterializationCursor{}, err
	}
	if !validLane(Lane(row.Lane)) || row.MaterializedSequence < 0 || row.Revision < 1 || !knownCursorStatus(row.Status) {
		return MaterializationCursor{}, invalidEvent("stored materialization cursor is invalid")
	}
	if err := validateDigest("materialized_hash", row.MaterializedHash, row.MaterializedSequence > 0); err != nil {
		return MaterializationCursor{}, err
	}
	if err := validateTimestamp("updated_at", row.UpdatedAt); err != nil {
		return MaterializationCursor{}, err
	}
	return MaterializationCursor{ProjectionID: row.ProjectionID, SourceID: row.SourceID, SourceEpoch: row.SourceEpoch,
		Lane: Lane(row.Lane), MaterializedSequence: row.MaterializedSequence, MaterializedHash: row.MaterializedHash,
		Revision: row.Revision, Status: row.Status, UpdatedAt: row.UpdatedAt}, nil
}

// tombstoneFromRow 将 SQLC tombstone 行映射并校验保留摘要。
func tombstoneFromRow(row storedb.OrchestrationEventTombstone) (Tombstone, error) {
	if err := validateTombstoneFields(row); err != nil {
		return Tombstone{}, err
	}
	if err := validateTimestamp("created_at", row.CreatedAt); err != nil {
		return Tombstone{}, err
	}
	return Tombstone{SourceID: row.SourceID, SourceEpoch: row.SourceEpoch, Lane: Lane(row.Lane),
		SourceSequence: row.SourceSequence, EnvelopeHash: row.EnvelopeHash, EventID: row.EventID,
		CanonicalCursor: row.CanonicalCursor, DedupFloor: row.DedupFloor, Status: row.Status, CreatedAt: row.CreatedAt}, nil
}

// validateTombstoneFields 校验 tombstone 的 source、序列、摘要和游标字段。
func validateTombstoneFields(row storedb.OrchestrationEventTombstone) error {
	if err := validateText("source_id", row.SourceID, 256, false); err != nil {
		return err
	}
	if err := validateText("source_epoch", row.SourceEpoch, 128, false); err != nil {
		return err
	}
	if !validLane(Lane(row.Lane)) || row.SourceSequence < 1 || row.DedupFloor < 0 || !knownTombstoneStatus(row.Status) {
		return invalidEvent("stored tombstone is invalid")
	}
	if err := validateDigest("envelope_hash", row.EnvelopeHash, true); err != nil {
		return err
	}
	if err := validateText("event_id", row.EventID, 128, false); err != nil {
		return err
	}
	return validateText("canonical_cursor", row.CanonicalCursor, 512, false)
}

func validateTimestamp(name, value string) error {
	if parsed, err := time.Parse(time.RFC3339Nano, value); err != nil || parsed.IsZero() {
		return invalidEvent(fmt.Sprintf("%s timestamp is invalid", name))
	}
	return nil
}

func knownSourceStatus(value string) bool {
	switch value {
	case "active", "gap", "quarantined", "closed":
		return true
	default:
		return false
	}
}

func knownReceiptStatus(value string) bool {
	switch value {
	case "staged", "materialized", "quarantined", "gap":
		return true
	default:
		return false
	}
}

func knownCursorStatus(value string) bool {
	return knownSourceStatus(value)
}

func knownTombstoneStatus(value string) bool {
	switch value {
	case "materialized", "expired", "quarantined":
		return true
	default:
		return false
	}
}
