package resourceadmission

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

const (
	limitsDigestDomain                 = "orch.resource.limits/v1"
	applicabilityDigestDomain          = "orch.resource.policy-applicability/v1"
	policyPublishRequestDigestDomain   = "orch.resource.publish-policy-request/v1"
	policyPublishOperationDigestDomain = "orch.resource.publish-policy-operation/v1"
	policySnapshotDigestDomain         = "orch.resource.policy-snapshot/v1"
	policyPublishReceiptDigestDomain   = "orch.resource.publish-policy-receipt/v1"
	reservationRequestDigestDomain     = "orch.resource.reservation-request/v1"
	reservationOperationDigestDomain   = "orch.resource.reservation-operation/v1"
	resourceSnapshotDigestDomain       = "orch.resource.reserved-snapshot/v1"
	reservationReceiptDigestDomain     = "orch.resource.reservation-receipt/v1"
)

// ComputeResourceVectorDigest 计算关闭十维vector的完整摘要。
func ComputeResourceVectorDigest(vector ResourceVector) (string, error) {
	if err := vector.Validate(); err != nil {
		return "", err
	}
	return digestValue(limitsDigestDomain, vector)
}

// ComputePolicyApplicabilityDigest 计算精确持久化policy selector摘要。
func ComputePolicyApplicabilityDigest(request PublishEffectivePolicyRequest) (string, error) {
	input := policyApplicabilityInput{
		SchemaVersion: request.SchemaVersion, RequestedKind: request.RequestedKind,
		CallerPrincipalRef: request.CallerPrincipalRef, ScopeRef: request.ScopeRef,
	}
	if err := input.Validate(); err != nil {
		return "", err
	}
	return digestValue(applicabilityDigestDomain, input)
}

// ComputeCanonicalPolicyHash 计算typed effective-policy canonical hash。
func ComputeCanonicalPolicyHash(request PublishEffectivePolicyRequest) (string, error) {
	if err := validateCanonicalPolicyInput(request); err != nil {
		return "", err
	}
	input := canonicalPolicyInput{
		SchemaVersion: request.SchemaVersion, LineageID: request.LineageID, Revision: request.Revision,
		RequestedKind: request.RequestedKind, CallerPrincipalRef: request.CallerPrincipalRef, ScopeRef: request.ScopeRef,
		PolicyApplicabilityDigest: request.PolicyApplicabilityDigest, DimensionSet: ResourceDimensionSet,
		SourcePolicySetRef: request.SourcePolicySetRef, SourcePolicySetDigest: request.SourcePolicySetDigest,
		PrecedenceTraceRef: request.PrecedenceTraceRef, PrecedenceTraceDigest: request.PrecedenceTraceDigest,
		CanonicalizationProfile: PolicyCanonicalizationProfile, EffectiveLimits: request.EffectiveLimits,
		EffectiveLimitsDigest: request.EffectiveLimitsDigest, ReleaseConditionDigest: request.ReleaseConditionDigest,
		EffectiveAt: request.EffectiveAt, ValidUntil: request.ValidUntil,
	}
	return digestValue(PolicyCanonicalizationProfile, input)
}

type canonicalPolicyInput struct {
	SchemaVersion             int64          `json:"schema_version"`
	LineageID                 string         `json:"lineage_id"`
	Revision                  int64          `json:"revision"`
	RequestedKind             string         `json:"requested_kind"`
	CallerPrincipalRef        string         `json:"caller_principal_ref"`
	ScopeRef                  string         `json:"scope_ref"`
	PolicyApplicabilityDigest string         `json:"policy_applicability_digest"`
	DimensionSet              string         `json:"dimension_set"`
	SourcePolicySetRef        string         `json:"source_policy_set_ref"`
	SourcePolicySetDigest     string         `json:"source_policy_set_digest"`
	PrecedenceTraceRef        string         `json:"precedence_trace_ref"`
	PrecedenceTraceDigest     string         `json:"precedence_trace_digest"`
	CanonicalizationProfile   string         `json:"canonicalization_profile"`
	EffectiveLimits           ResourceVector `json:"effective_limits"`
	EffectiveLimitsDigest     string         `json:"effective_limits_digest"`
	ReleaseConditionDigest    string         `json:"release_condition_digest"`
	EffectiveAt               time.Time      `json:"effective_at"`
	ValidUntil                time.Time      `json:"valid_until"`
}

type policyApplicabilityInput struct {
	SchemaVersion      int64  `json:"schema_version"`
	RequestedKind      string `json:"requested_kind"`
	CallerPrincipalRef string `json:"caller_principal_ref"`
	ScopeRef           string `json:"scope_ref"`
}

// FinalizePublishEffectivePolicyRequest 填充policy发布请求的全部派生摘要。
func FinalizePublishEffectivePolicyRequest(request PublishEffectivePolicyRequest) (PublishEffectivePolicyRequest, error) {
	var err error
	request.PolicyApplicabilityDigest, err = ComputePolicyApplicabilityDigest(request)
	if err != nil {
		return PublishEffectivePolicyRequest{}, err
	}
	request.EffectiveLimitsDigest, err = ComputeResourceVectorDigest(request.EffectiveLimits)
	if err != nil {
		return PublishEffectivePolicyRequest{}, err
	}
	request.CanonicalPolicyHash, err = ComputeCanonicalPolicyHash(request)
	if err != nil {
		return PublishEffectivePolicyRequest{}, err
	}
	request.PublishRequestDigest = ""
	request.PublishOperationDigest = ""
	request.PublishRequestDigest, err = ComputePublishRequestDigest(request)
	if err != nil {
		return PublishEffectivePolicyRequest{}, err
	}
	request.PublishOperationDigest, err = ComputePublishOperationDigest(request)
	if err != nil {
		return PublishEffectivePolicyRequest{}, err
	}
	return request, nil
}

// ComputePublishRequestDigest 排除重试candidate身份并绑定全部稳定请求字段。
func ComputePublishRequestDigest(request PublishEffectivePolicyRequest) (string, error) {
	if !validText(request.SnapshotID, 128) || !validText(request.PublishReceiptRef, 512) {
		return "", ErrPolicyIncomplete
	}
	request.SnapshotID = ""
	request.PublishReceiptRef = ""
	request.PublishRequestDigest = ""
	request.PublishOperationDigest = ""
	if err := request.validateShape(false); err != nil {
		return "", err
	}
	return digestValue(policyPublishRequestDigestDomain, request)
}

// ComputePublishOperationDigest 把stable operation绑定到完整发布请求摘要。
func ComputePublishOperationDigest(request PublishEffectivePolicyRequest) (string, error) {
	if !validText(request.PublishOperationID, 128) || !validDigest(request.PublishRequestDigest) {
		return "", ErrPolicyIncomplete
	}
	input := operationDigestInput{OperationID: request.PublishOperationID, RequestDigest: request.PublishRequestDigest}
	return digestValue(policyPublishOperationDigestDomain, input)
}

// ComputePolicySnapshotDigest 计算全部可重建policy字段的snapshot摘要。
func ComputePolicySnapshotDigest(snapshot PolicySnapshot) (string, error) {
	snapshot.PolicySnapshotDigest = ""
	snapshot.PublishReceiptDigest = ""
	if err := snapshot.validateShape(false); err != nil {
		return "", err
	}
	return digestValue(policySnapshotDigestDomain, snapshot)
}

// FinalizePolicySnapshot 按非循环顺序计算policy snapshot与receipt摘要。
func FinalizePolicySnapshot(snapshot PolicySnapshot) (PolicySnapshot, error) {
	snapshot.PolicySnapshotDigest = ""
	snapshot.PublishReceiptDigest = ""
	digest, err := ComputePolicySnapshotDigest(snapshot)
	if err != nil {
		return PolicySnapshot{}, err
	}
	snapshot.PolicySnapshotDigest = digest
	snapshot.PublishReceiptDigest, err = ComputePolicyPublishReceiptDigest(snapshot)
	if err != nil {
		return PolicySnapshot{}, err
	}
	return snapshot, nil
}

// ComputePolicyPublishReceiptDigest 把owner receipt绑定到最终policy snapshot。
func ComputePolicyPublishReceiptDigest(snapshot PolicySnapshot) (string, error) {
	if err := snapshot.validateReceiptInput(); err != nil {
		return "", err
	}
	input := policyReceiptInput{
		ReceiptRef: snapshot.PublishReceiptRef, OperationID: snapshot.PublishOperationID,
		OperationDigest: snapshot.PublishOperationDigest, RequestDigest: snapshot.PublishRequestDigest,
		SnapshotID: snapshot.SnapshotID, SnapshotDigest: snapshot.PolicySnapshotDigest,
		PolicyOwner: snapshot.PolicyOwner, CreatedAt: snapshot.CreatedAt,
	}
	return digestValue(policyPublishReceiptDigestDomain, input)
}

type policyReceiptInput struct {
	ReceiptRef      string           `json:"receipt_ref"`
	OperationID     string           `json:"operation_id"`
	OperationDigest string           `json:"operation_digest"`
	RequestDigest   string           `json:"request_digest"`
	SnapshotID      string           `json:"snapshot_id"`
	SnapshotDigest  string           `json:"snapshot_digest"`
	PolicyOwner     PolicyOwnerFence `json:"policy_owner"`
	CreatedAt       time.Time        `json:"created_at"`
}

// FinalizeReserveRequest 填充reservation request与operation摘要。
func FinalizeReserveRequest(request ReserveRequest) (ReserveRequest, error) {
	request.ReservationRequestDigest = ""
	request.ReservationOperationDigest = ""
	digest, err := ComputeReservationRequestDigest(request)
	if err != nil {
		return ReserveRequest{}, err
	}
	request.ReservationRequestDigest = digest
	request.ReservationOperationDigest, err = ComputeReservationOperationDigest(request)
	if err != nil {
		return ReserveRequest{}, err
	}
	return request, nil
}

// ComputeReservationRequestDigest 排除candidate身份并绑定全部稳定reservation输入。
func ComputeReservationRequestDigest(request ReserveRequest) (string, error) {
	if !validReserveCandidateIDs(request) {
		return "", ErrInvalidRequest
	}
	request.ReservationID = ""
	request.ResourceSnapshotID = ""
	request.ReservationReceiptRef = ""
	request.ReservationRequestDigest = ""
	request.ReservationOperationDigest = ""
	if err := request.validateShape(false); err != nil {
		return "", err
	}
	return digestValue(reservationRequestDigestDomain, request)
}

// ComputeReservationOperationDigest 把单一operation绑定到reservation request。
func ComputeReservationOperationDigest(request ReserveRequest) (string, error) {
	if !validText(request.ReservationOperationID, 128) || !validDigest(request.ReservationRequestDigest) {
		return "", ErrInvalidRequest
	}
	input := operationDigestInput{OperationID: request.ReservationOperationID, RequestDigest: request.ReservationRequestDigest}
	return digestValue(reservationOperationDigestDomain, input)
}

type operationDigestInput struct {
	OperationID   string `json:"operation_id"`
	RequestDigest string `json:"request_digest"`
}

// ComputeResourceSnapshotDigest 计算全部可重建reserved字段的snapshot摘要。
func ComputeResourceSnapshotDigest(snapshot ReservationSnapshot) (string, error) {
	snapshot.ResourceSnapshotDigest = ""
	snapshot.ReservationReceiptDigest = ""
	if err := snapshot.validateShape(false); err != nil {
		return "", err
	}
	return digestValue(resourceSnapshotDigestDomain, snapshot)
}

// FinalizeReservationSnapshot 按非循环顺序计算resource snapshot与receipt摘要。
func FinalizeReservationSnapshot(snapshot ReservationSnapshot) (ReservationSnapshot, error) {
	snapshot.ResourceSnapshotDigest = ""
	snapshot.ReservationReceiptDigest = ""
	digest, err := ComputeResourceSnapshotDigest(snapshot)
	if err != nil {
		return ReservationSnapshot{}, err
	}
	snapshot.ResourceSnapshotDigest = digest
	snapshot.ReservationReceiptDigest, err = ComputeReservationReceiptDigest(snapshot)
	if err != nil {
		return ReservationSnapshot{}, err
	}
	return snapshot, nil
}

// ComputeReservationReceiptDigest 把本地receipt绑定到最终resource snapshot。
func ComputeReservationReceiptDigest(snapshot ReservationSnapshot) (string, error) {
	if err := snapshot.validateReceiptInput(); err != nil {
		return "", err
	}
	input := reservationReceiptInput{
		ReceiptRef: snapshot.ReservationReceiptRef, OperationID: snapshot.ReservationOperationID,
		OperationDigest: snapshot.ReservationOperationDigest, RequestDigest: snapshot.ReservationRequestDigest,
		SnapshotID: snapshot.ResourceSnapshotID, SnapshotDigest: snapshot.ResourceSnapshotDigest,
		AdmissionOwner: snapshot.AdmissionOwner, CreatedAt: snapshot.CreatedAt,
	}
	return digestValue(reservationReceiptDigestDomain, input)
}

type reservationReceiptInput struct {
	ReceiptRef      string              `json:"receipt_ref"`
	OperationID     string              `json:"operation_id"`
	OperationDigest string              `json:"operation_digest"`
	RequestDigest   string              `json:"request_digest"`
	SnapshotID      string              `json:"snapshot_id"`
	SnapshotDigest  string              `json:"snapshot_digest"`
	AdmissionOwner  AdmissionOwnerFence `json:"admission_owner"`
	CreatedAt       time.Time           `json:"created_at"`
}

// digestValue 以显式domain envelope计算规范JSON SHA-256摘要。
func digestValue(domain string, value any) (string, error) {
	payload, err := json.Marshal(struct {
		Domain string `json:"domain"`
		Value  any    `json:"value"`
	}{domain, value})
	if err != nil {
		return "", fmt.Errorf("encode resource digest: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}
