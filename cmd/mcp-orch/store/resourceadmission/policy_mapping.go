package resourceadmission

import (
	"fmt"
	"strings"
	"time"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

// newPolicySnapshot 从已验证请求和显式创建时间构造完整不可变projection。
func newPolicySnapshot(request PublishEffectivePolicyRequest, createdAt time.Time) (PolicySnapshot, error) {
	snapshot := PolicySnapshot{
		SchemaVersion: request.SchemaVersion, SnapshotID: request.SnapshotID, LineageID: request.LineageID,
		Revision: request.Revision, RequestedKind: request.RequestedKind,
		CallerPrincipalRef: request.CallerPrincipalRef, ScopeRef: request.ScopeRef,
		PolicyApplicabilityDigest: request.PolicyApplicabilityDigest, DimensionSet: ResourceDimensionSet,
		SourcePolicySetRef: request.SourcePolicySetRef, SourcePolicySetDigest: request.SourcePolicySetDigest,
		PrecedenceTraceRef: request.PrecedenceTraceRef, PrecedenceTraceDigest: request.PrecedenceTraceDigest,
		CanonicalizationProfile: PolicyCanonicalizationProfile, CanonicalPolicyHash: request.CanonicalPolicyHash,
		EffectiveLimits: request.EffectiveLimits, EffectiveLimitsDigest: request.EffectiveLimitsDigest,
		ReleaseConditionDigest: request.ReleaseConditionDigest,
		IdempotencyNamespace:   request.IdempotencyNamespace, IdempotencyKeyDigest: request.IdempotencyKeyDigest,
		PublishRequestDigest: request.PublishRequestDigest, PublishOperationID: request.PublishOperationID,
		PublishOperationDigest: request.PublishOperationDigest, PublishReceiptRef: request.PublishReceiptRef,
		PolicyOwner: request.PolicyOwner, EffectiveAt: request.EffectiveAt, ValidUntil: request.ValidUntil,
		CreatedAt: createdAt,
	}
	finalized, err := FinalizePolicySnapshot(snapshot)
	if err != nil {
		return PolicySnapshot{}, err
	}
	if err := finalized.Validate(); err != nil {
		return PolicySnapshot{}, err
	}
	return finalized, nil
}

// policyCreateParams 无损映射完整policy projection到sqlc insert参数。
func policyCreateParams(snapshot PolicySnapshot) storedb.CreateResourceEffectivePolicyParams {
	limits := snapshot.EffectiveLimits
	return storedb.CreateResourceEffectivePolicyParams{
		PolicySnapshotID: snapshot.SnapshotID, PolicyLineageID: snapshot.LineageID,
		PolicyRevision: snapshot.Revision, CallerPrincipalRef: snapshot.CallerPrincipalRef,
		ScopeRef: snapshot.ScopeRef, PolicyApplicabilityDigest: snapshot.PolicyApplicabilityDigest,
		SourcePolicySetRef: snapshot.SourcePolicySetRef, SourcePolicySetDigest: snapshot.SourcePolicySetDigest,
		PrecedenceTraceRef: snapshot.PrecedenceTraceRef, PrecedenceTraceDigest: snapshot.PrecedenceTraceDigest,
		CanonicalPolicyHash: snapshot.CanonicalPolicyHash, PolicySnapshotDigest: snapshot.PolicySnapshotDigest,
		EffectiveLimitsDigest: snapshot.EffectiveLimitsDigest, ReleaseConditionDigest: snapshot.ReleaseConditionDigest,
		LimitAgentCount: limits.AgentCount, LimitInFlightCount: limits.InFlightCount,
		LimitFanoutCount: limits.FanoutCount, LimitConcurrencyCount: limits.ConcurrencyCount,
		LimitWallTimeMs: limits.WallTimeMS, LimitInputTokens: limits.InputTokens,
		LimitOutputTokens: limits.OutputTokens, LimitCostMicrounit: limits.CostMicrounits,
		LimitContentBytes: limits.ContentBytes, LimitIngressBytes: limits.IngressBytes,
		IdempotencyNamespace: snapshot.IdempotencyNamespace, IdempotencyKeyDigest: snapshot.IdempotencyKeyDigest,
		PublishRequestDigest: snapshot.PublishRequestDigest, PublishOperationID: snapshot.PublishOperationID,
		PublishOperationDigest: snapshot.PublishOperationDigest, PublishReceiptRef: snapshot.PublishReceiptRef,
		PublishReceiptDigest: snapshot.PublishReceiptDigest, PolicyOwnerID: snapshot.PolicyOwner.OwnerID,
		PolicyOwnerGeneration: snapshot.PolicyOwner.OwnerGeneration,
		PolicyWriterFenceRef:  snapshot.PolicyOwner.WriterFenceRef, PolicyOwnerProofRef: snapshot.PolicyOwner.OwnerProofRef,
		DeploymentFenceRef:     snapshot.PolicyOwner.DeploymentFenceRef,
		SecurityTimeGeneration: snapshot.PolicyOwner.SecurityTimeGeneration,
		EffectiveAt:            utcPolicyText(snapshot.EffectiveAt), ValidUntil: utcPolicyText(snapshot.ValidUntil),
		CreatedAt: utcPolicyText(snapshot.CreatedAt),
	}
}

// policyFromRow 严格重建并重算两级摘要，任一列漂移均拒绝读取。
func policyFromRow(row storedb.OrchestrationResourceEffectivePolicySnapshot) (PolicySnapshot, error) {
	effectiveAt, err := parsePolicyTime("effective_at", row.EffectiveAt)
	if err != nil {
		return PolicySnapshot{}, err
	}
	validUntil, err := parsePolicyTime("valid_until", row.ValidUntil)
	if err != nil {
		return PolicySnapshot{}, err
	}
	createdAt, err := parsePolicyTime("created_at", row.CreatedAt)
	if err != nil {
		return PolicySnapshot{}, err
	}
	snapshot := PolicySnapshot{
		SchemaVersion: row.SchemaVersion, SnapshotID: row.PolicySnapshotID, LineageID: row.PolicyLineageID,
		Revision: row.PolicyRevision, RequestedKind: row.RequestedKind,
		CallerPrincipalRef: row.CallerPrincipalRef, ScopeRef: row.ScopeRef,
		PolicyApplicabilityDigest: row.PolicyApplicabilityDigest, DimensionSet: row.DimensionSet,
		SourcePolicySetRef: row.SourcePolicySetRef, SourcePolicySetDigest: row.SourcePolicySetDigest,
		PrecedenceTraceRef: row.PrecedenceTraceRef, PrecedenceTraceDigest: row.PrecedenceTraceDigest,
		CanonicalizationProfile: row.CanonicalizationProfile, CanonicalPolicyHash: row.CanonicalPolicyHash,
		PolicySnapshotDigest: row.PolicySnapshotDigest,
		EffectiveLimits: ResourceVector{
			AgentCount: row.LimitAgentCount, InFlightCount: row.LimitInFlightCount,
			FanoutCount: row.LimitFanoutCount, ConcurrencyCount: row.LimitConcurrencyCount,
			WallTimeMS: row.LimitWallTimeMs, InputTokens: row.LimitInputTokens,
			OutputTokens: row.LimitOutputTokens, CostMicrounits: row.LimitCostMicrounit,
			ContentBytes: row.LimitContentBytes, IngressBytes: row.LimitIngressBytes,
		},
		EffectiveLimitsDigest: row.EffectiveLimitsDigest, ReleaseConditionDigest: row.ReleaseConditionDigest,
		IdempotencyNamespace: row.IdempotencyNamespace, IdempotencyKeyDigest: row.IdempotencyKeyDigest,
		PublishRequestDigest: row.PublishRequestDigest, PublishOperationID: row.PublishOperationID,
		PublishOperationDigest: row.PublishOperationDigest, PublishReceiptRef: row.PublishReceiptRef,
		PublishReceiptDigest: row.PublishReceiptDigest,
		PolicyOwner: PolicyOwnerFence{
			OwnerID: row.PolicyOwnerID, OwnerGeneration: row.PolicyOwnerGeneration,
			WriterFenceRef: row.PolicyWriterFenceRef, OwnerProofRef: row.PolicyOwnerProofRef,
			DeploymentFenceRef: row.DeploymentFenceRef, SecurityTimeGeneration: row.SecurityTimeGeneration,
		},
		EffectiveAt: effectiveAt, ValidUntil: validUntil, CreatedAt: createdAt,
	}
	if err := snapshot.Validate(); err != nil {
		return PolicySnapshot{}, err
	}
	return snapshot, nil
}

func utcPolicyText(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

// parsePolicyTime 拒绝SQLite可接受但digest不接受的非规范或非UTC时间文本。
func parsePolicyTime(name, value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: parse policy %s: %w", ErrSnapshotCorrupt, name, err)
	}
	if parsed.Location() != time.UTC || !strings.HasSuffix(value, "Z") || value != utcPolicyText(parsed) {
		return time.Time{}, fmt.Errorf("%w: policy %s is not canonical UTC", ErrSnapshotCorrupt, name)
	}
	return parsed.UTC(), nil
}
