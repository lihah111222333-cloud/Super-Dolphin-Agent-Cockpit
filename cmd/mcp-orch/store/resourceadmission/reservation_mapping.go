package resourceadmission

import (
	"context"
	"errors"
	"time"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

// reservationFromRow 重建reserved snapshot并拒绝policy关系或空账本状态漂移。
func reservationFromRow(
	ctx context.Context,
	q *storedb.Queries,
	row storedb.OrchestrationResourceReservation,
) (ReservationSnapshot, error) {
	policy, err := q.GetResourceEffectivePolicyByID(ctx, storedb.GetResourceEffectivePolicyByIDParams{
		PolicySnapshotID: row.PolicySnapshotID,
	})
	if err != nil {
		return ReservationSnapshot{}, errors.Join(ErrSnapshotCorrupt, err)
	}
	if !reservationPolicyRowsMatch(row, policy) {
		return ReservationSnapshot{}, ErrSnapshotCorrupt
	}
	usageRows, err := q.CountResourceUsageByReservation(ctx, storedb.CountResourceUsageByReservationParams{
		ReservationID: row.ReservationID, ReservationVersion: row.ReservationVersion,
	})
	if err != nil {
		return ReservationSnapshot{}, err
	}
	if usageRows != 0 {
		return ReservationSnapshot{}, ErrSnapshotCorrupt
	}
	createdAt, validUntil, err := parseReservationTimes(row.CreatedAt, row.ValidUntil)
	if err != nil {
		return ReservationSnapshot{}, err
	}
	snapshot := reservationSnapshotFromRows(row, policy, createdAt, validUntil)
	if err := snapshot.Validate(); err != nil {
		return ReservationSnapshot{}, err
	}
	return snapshot, nil
}

// reservationSnapshotFromRows 显式映射reservation与immutable policy行到owner snapshot。
func reservationSnapshotFromRows(
	row storedb.OrchestrationResourceReservation,
	policy storedb.OrchestrationResourceEffectivePolicySnapshot,
	createdAt time.Time,
	validUntil time.Time,
) ReservationSnapshot {
	return ReservationSnapshot{
		SchemaVersion: row.SchemaVersion, ReservationID: row.ReservationID,
		ReservationVersion: row.ReservationVersion, ResourceSnapshotID: row.ResourceSnapshotID,
		ResourceSnapshotDigest: row.ResourceSnapshotDigest, ResourceStatus: row.ResourceStatus,
		RequestedKind: row.RequestedKind, CommandID: row.CommandID, CommandVersion: row.CommandVersion,
		CommandProfileDigest: row.CommandProfileDigest, CallerPrincipalRef: row.CallerPrincipalRef,
		ScopeRef: row.ScopeRef, Source: BindingAnchor{AgentID: row.SourceAgentID,
			BindingVersion: row.SourceBindingVersion, FenceRef: row.SourceFenceRef},
		Target: BindingAnchor{AgentID: row.TargetAgentID, BindingVersion: row.TargetBindingVersion,
			FenceRef: row.TargetFenceRef}, IdentityFenceDigest: row.IdentityFenceDigest,
		Content: ContentAnchor{ReferenceID: row.ContentReferenceID, ContentRef: row.ContentRef,
			ObjectID: row.ContentObjectID, ObjectVersion: row.ContentObjectVersion,
			RefVersion: row.ContentRefVersion, OwnerSnapshotDigest: row.ContentOwnerSnapshotDigest,
			AdmissionSnapshotDigest: row.ContentAdmissionSnapshotDigest},
		PolicySnapshotID: row.PolicySnapshotID, PolicyRevision: row.PolicyRevision,
		PolicySnapshotDigest: row.PolicySnapshotDigest, PolicyCanonicalHash: row.PolicyCanonicalHash,
		EffectiveLimits: effectiveVectorFromPolicyRow(policy), EffectiveLimitsDigest: row.EffectiveLimitsDigest,
		Requested: requestedVectorFromReservationRow(row), UsageDigestDomain: row.UsageDigestDomain,
		UsageWatermark: row.UsageWatermark, UsageDigest: row.UsageDigest,
		ReleaseConditionDigest: row.ReleaseConditionDigest, IdempotencyNamespace: row.IdempotencyNamespace,
		IdempotencyKeyDigest: row.IdempotencyKeyDigest, ReservationRequestDigest: row.ReservationRequestDigest,
		ReservationOperationID:     row.ReservationOperationID,
		ReservationOperationDigest: row.ReservationOperationDigest,
		ReservationReceiptRef:      row.ReservationReceiptRef, ReservationReceiptDigest: row.ReservationReceiptDigest,
		AdmissionOwner: AdmissionOwnerFence{OwnerID: row.AdmissionOwnerID,
			OwnerGeneration: row.AdmissionOwnerGeneration, WriterFenceRef: row.AdmissionWriterFenceRef,
			DeploymentFenceRef:     row.AdmissionDeploymentFenceRef,
			SecurityTimeGeneration: row.SecurityTimeGeneration, OwnerProofRef: row.OwnerProofRef},
		ValidUntil: validUntil, CreatedAt: createdAt,
	}
}

// reservationPolicyRowsMatch 校验reservation冻结的policy tuple仍精确指向原snapshot。
func reservationPolicyRowsMatch(
	reservation storedb.OrchestrationResourceReservation,
	policy storedb.OrchestrationResourceEffectivePolicySnapshot,
) bool {
	return reservation.PolicySnapshotID == policy.PolicySnapshotID &&
		reservation.PolicyRevision == policy.PolicyRevision &&
		reservation.PolicySnapshotDigest == policy.PolicySnapshotDigest &&
		reservation.PolicyCanonicalHash == policy.CanonicalPolicyHash &&
		reservation.EffectiveLimitsDigest == policy.EffectiveLimitsDigest &&
		reservation.ReleaseConditionDigest == policy.ReleaseConditionDigest
}

func effectiveVectorFromPolicyRow(row storedb.OrchestrationResourceEffectivePolicySnapshot) ResourceVector {
	return ResourceVector{
		AgentCount: row.LimitAgentCount, InFlightCount: row.LimitInFlightCount,
		FanoutCount: row.LimitFanoutCount, ConcurrencyCount: row.LimitConcurrencyCount,
		WallTimeMS: row.LimitWallTimeMs, InputTokens: row.LimitInputTokens,
		OutputTokens: row.LimitOutputTokens, CostMicrounits: row.LimitCostMicrounit,
		ContentBytes: row.LimitContentBytes, IngressBytes: row.LimitIngressBytes,
	}
}

func requestedVectorFromReservationRow(row storedb.OrchestrationResourceReservation) ResourceVector {
	return ResourceVector{
		AgentCount: row.ReservedAgentCount, InFlightCount: row.ReservedInFlightCount,
		FanoutCount: row.ReservedFanoutCount, ConcurrencyCount: row.ReservedConcurrencyCount,
		WallTimeMS: row.ReservedWallTimeMs, InputTokens: row.ReservedInputTokens,
		OutputTokens: row.ReservedOutputTokens, CostMicrounits: row.ReservedCostMicrounit,
		ContentBytes: row.ReservedContentBytes, IngressBytes: row.ReservedIngressBytes,
	}
}

func parseReservationTimes(created, validUntil string) (time.Time, time.Time, error) {
	createdAt, err := time.Parse(time.RFC3339Nano, created)
	if err != nil || !validTime(createdAt) {
		return time.Time{}, time.Time{}, ErrSnapshotCorrupt
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, validUntil)
	if err != nil || !validTime(expiresAt) {
		return time.Time{}, time.Time{}, ErrSnapshotCorrupt
	}
	return createdAt, expiresAt, nil
}

// reservationParams 把完成摘要的reserved snapshot逐字段映射到唯一INSERT参数。
func reservationParams(snapshot ReservationSnapshot) storedb.CreateResourceReservationParams {
	return storedb.CreateResourceReservationParams{
		ReservationID: snapshot.ReservationID, ResourceSnapshotID: snapshot.ResourceSnapshotID,
		ResourceSnapshotDigest: snapshot.ResourceSnapshotDigest, CommandID: snapshot.CommandID,
		CommandProfileDigest: snapshot.CommandProfileDigest, CallerPrincipalRef: snapshot.CallerPrincipalRef,
		ScopeRef: snapshot.ScopeRef, SourceAgentID: snapshot.Source.AgentID,
		SourceBindingVersion: snapshot.Source.BindingVersion, SourceFenceRef: snapshot.Source.FenceRef,
		TargetAgentID: snapshot.Target.AgentID, TargetBindingVersion: snapshot.Target.BindingVersion,
		TargetFenceRef: snapshot.Target.FenceRef, IdentityFenceDigest: snapshot.IdentityFenceDigest,
		ContentReferenceID: snapshot.Content.ReferenceID, ContentRef: snapshot.Content.ContentRef,
		ContentObjectID:                snapshot.Content.ObjectID,
		ContentOwnerSnapshotDigest:     snapshot.Content.OwnerSnapshotDigest,
		ContentAdmissionSnapshotDigest: snapshot.Content.AdmissionSnapshotDigest,
		PolicySnapshotID:               snapshot.PolicySnapshotID, PolicyRevision: snapshot.PolicyRevision,
		PolicySnapshotDigest: snapshot.PolicySnapshotDigest, PolicyCanonicalHash: snapshot.PolicyCanonicalHash,
		EffectiveLimitsDigest: snapshot.EffectiveLimitsDigest, ReservedAgentCount: snapshot.Requested.AgentCount,
		ReservedInFlightCount: snapshot.Requested.InFlightCount, ReservedFanoutCount: snapshot.Requested.FanoutCount,
		ReservedConcurrencyCount: snapshot.Requested.ConcurrencyCount,
		ReservedWallTimeMs:       snapshot.Requested.WallTimeMS, ReservedInputTokens: snapshot.Requested.InputTokens,
		ReservedOutputTokens:  snapshot.Requested.OutputTokens,
		ReservedCostMicrounit: snapshot.Requested.CostMicrounits,
		ReservedContentBytes:  snapshot.Requested.ContentBytes, ReservedIngressBytes: snapshot.Requested.IngressBytes,
		ReleaseConditionDigest: snapshot.ReleaseConditionDigest,
		IdempotencyNamespace:   snapshot.IdempotencyNamespace, IdempotencyKeyDigest: snapshot.IdempotencyKeyDigest,
		ReservationRequestDigest:    snapshot.ReservationRequestDigest,
		ReservationOperationID:      snapshot.ReservationOperationID,
		ReservationOperationDigest:  snapshot.ReservationOperationDigest,
		ReservationReceiptRef:       snapshot.ReservationReceiptRef,
		ReservationReceiptDigest:    snapshot.ReservationReceiptDigest,
		AdmissionOwnerID:            snapshot.AdmissionOwner.OwnerID,
		AdmissionOwnerGeneration:    snapshot.AdmissionOwner.OwnerGeneration,
		AdmissionWriterFenceRef:     snapshot.AdmissionOwner.WriterFenceRef,
		AdmissionDeploymentFenceRef: snapshot.AdmissionOwner.DeploymentFenceRef,
		SecurityTimeGeneration:      snapshot.AdmissionOwner.SecurityTimeGeneration,
		OwnerProofRef:               snapshot.AdmissionOwner.OwnerProofRef,
		ValidUntil:                  snapshot.ValidUntil.Format(time.RFC3339Nano),
		CreatedAt:                   snapshot.CreatedAt.Format(time.RFC3339Nano),
	}
}
