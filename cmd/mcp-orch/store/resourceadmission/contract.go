// Package resourceadmission owns durable effective-policy snapshots and
// non-authorizing reserved resource snapshots for the orchestration sidecar.
// It does not claim, dispatch, permit, release, or measure external effects.
package resourceadmission

import (
	"context"
	"errors"
	"time"
)

const (
	SchemaVersion                 int64 = 1
	ReservationVersion            int64 = 1
	RequestedKindSendMessage            = "send_message"
	ResourceStatusReserved              = "reserved"
	ResourceDimensionSet                = "orch.resource.dimensions/v1"
	PolicyCanonicalizationProfile       = "orch.resource.effective-policy/v1"
	EmptyUsageWatermark           int64 = 0
	EmptyUsageLedgerDomain              = "orch.resource.usage-ledger/v1"
	EmptyUsageLedgerCanonical           = "orch.resource.usage-ledger/v1\nempty\n"
	EmptyUsageLedgerDigest              = "c290c54b3c8466b643e128da65eda3cc1a43f85799c73b38e8dd862c7d4cbe31"
)

var (
	ErrInvalidRequest      = errors.New("resource admission request is invalid")
	ErrStoreUninitialized  = errors.New("resource admission store is not initialized")
	ErrPolicyUnavailable   = errors.New("resource admission policy is unavailable")
	ErrPolicyIncomplete    = errors.New("resource admission policy is incomplete")
	ErrPolicyConflict      = errors.New("resource admission policy conflicts with the original record")
	ErrPolicyStale         = errors.New("resource admission policy is stale")
	ErrReplayConflict      = errors.New("resource admission operation conflicts with the original request")
	ErrReservationNotFound = errors.New("resource admission reservation is not found")
	ErrProfileUnavailable  = errors.New("resource admission command profile is unavailable")
	ErrProfileMismatch     = errors.New("resource admission command profile does not match")
	ErrIdentityFence       = errors.New("resource admission identity fence does not match")
	ErrContentUnavailable  = errors.New("resource admission content is unavailable")
	ErrContentMismatch     = errors.New("resource admission content does not match")
	ErrLimitExceeded       = errors.New("resource admission limit is exceeded")
	ErrWriterFence         = errors.New("resource admission writer fence does not match")
	ErrSnapshotCorrupt     = errors.New("resource admission snapshot is corrupt")
	ErrOutcomeUnknown      = errors.New("resource admission operation outcome is unknown")
)

// ResourceVector is the closed RESOURCE v1 dimension set. Every value is
// mandatory and strictly positive; zero never means an implicit hard cap.
type ResourceVector struct {
	AgentCount       int64 `json:"agent_count"`
	InFlightCount    int64 `json:"in_flight_count"`
	FanoutCount      int64 `json:"fanout_count"`
	ConcurrencyCount int64 `json:"concurrency_count"`
	WallTimeMS       int64 `json:"wall_time_ms"`
	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	CostMicrounits   int64 `json:"cost_microunits"`
	ContentBytes     int64 `json:"content_bytes"`
	IngressBytes     int64 `json:"ingress_bytes"`
}

// PolicyOwnerFence is the exact policy-writer fence persisted by schema v133.
type PolicyOwnerFence struct {
	OwnerID                string `json:"owner_id"`
	OwnerGeneration        int64  `json:"owner_generation"`
	WriterFenceRef         string `json:"writer_fence_ref"`
	OwnerProofRef          string `json:"owner_proof_ref"`
	DeploymentFenceRef     string `json:"deployment_fence_ref"`
	SecurityTimeGeneration int64  `json:"security_time_generation"`
}

// AdmissionOwnerFence is the exact reservation-writer fence persisted by
// schema v133. It has an owner proof and no policy deployment selector.
type AdmissionOwnerFence struct {
	OwnerID                string `json:"owner_id"`
	OwnerGeneration        int64  `json:"owner_generation"`
	WriterFenceRef         string `json:"writer_fence_ref"`
	DeploymentFenceRef     string `json:"deployment_fence_ref"`
	SecurityTimeGeneration int64  `json:"security_time_generation"`
	OwnerProofRef          string `json:"owner_proof_ref"`
}

// PublishEffectivePolicyRequest contains only typed and directly persisted
// policy inputs. Source and precedence bodies remain with their external owner.
type PublishEffectivePolicyRequest struct {
	SchemaVersion             int64            `json:"schema_version"`
	SnapshotID                string           `json:"snapshot_id"`
	LineageID                 string           `json:"lineage_id"`
	Revision                  int64            `json:"revision"`
	RequestedKind             string           `json:"requested_kind"`
	CallerPrincipalRef        string           `json:"caller_principal_ref"`
	ScopeRef                  string           `json:"scope_ref"`
	PolicyApplicabilityDigest string           `json:"policy_applicability_digest"`
	SourcePolicySetRef        string           `json:"source_policy_set_ref"`
	SourcePolicySetDigest     string           `json:"source_policy_set_digest"`
	PrecedenceTraceRef        string           `json:"precedence_trace_ref"`
	PrecedenceTraceDigest     string           `json:"precedence_trace_digest"`
	EffectiveLimits           ResourceVector   `json:"effective_limits"`
	EffectiveLimitsDigest     string           `json:"effective_limits_digest"`
	CanonicalPolicyHash       string           `json:"canonical_policy_hash"`
	ReleaseConditionDigest    string           `json:"release_condition_digest"`
	IdempotencyNamespace      string           `json:"idempotency_namespace"`
	IdempotencyKeyDigest      string           `json:"idempotency_key_digest"`
	PublishRequestDigest      string           `json:"publish_request_digest"`
	PublishOperationID        string           `json:"publish_operation_id"`
	PublishOperationDigest    string           `json:"publish_operation_digest"`
	PublishReceiptRef         string           `json:"publish_receipt_ref"`
	PolicyOwner               PolicyOwnerFence `json:"policy_owner"`
	EffectiveAt               time.Time        `json:"effective_at"`
	ValidUntil                time.Time        `json:"valid_until"`
}

// PolicySnapshot is a one-to-one typed projection of the immutable effective
// policy row. It does not retain data that schema v133 cannot reconstruct.
type PolicySnapshot struct {
	SchemaVersion             int64            `json:"schema_version"`
	SnapshotID                string           `json:"snapshot_id"`
	LineageID                 string           `json:"lineage_id"`
	Revision                  int64            `json:"revision"`
	RequestedKind             string           `json:"requested_kind"`
	CallerPrincipalRef        string           `json:"caller_principal_ref"`
	ScopeRef                  string           `json:"scope_ref"`
	PolicyApplicabilityDigest string           `json:"policy_applicability_digest"`
	DimensionSet              string           `json:"dimension_set"`
	SourcePolicySetRef        string           `json:"source_policy_set_ref"`
	SourcePolicySetDigest     string           `json:"source_policy_set_digest"`
	PrecedenceTraceRef        string           `json:"precedence_trace_ref"`
	PrecedenceTraceDigest     string           `json:"precedence_trace_digest"`
	CanonicalizationProfile   string           `json:"canonicalization_profile"`
	CanonicalPolicyHash       string           `json:"canonical_policy_hash"`
	PolicySnapshotDigest      string           `json:"policy_snapshot_digest"`
	EffectiveLimits           ResourceVector   `json:"effective_limits"`
	EffectiveLimitsDigest     string           `json:"effective_limits_digest"`
	ReleaseConditionDigest    string           `json:"release_condition_digest"`
	IdempotencyNamespace      string           `json:"idempotency_namespace"`
	IdempotencyKeyDigest      string           `json:"idempotency_key_digest"`
	PublishRequestDigest      string           `json:"publish_request_digest"`
	PublishOperationID        string           `json:"publish_operation_id"`
	PublishOperationDigest    string           `json:"publish_operation_digest"`
	PublishReceiptRef         string           `json:"publish_receipt_ref"`
	PublishReceiptDigest      string           `json:"publish_receipt_digest"`
	PolicyOwner               PolicyOwnerFence `json:"policy_owner"`
	EffectiveAt               time.Time        `json:"effective_at"`
	ValidUntil                time.Time        `json:"valid_until"`
	CreatedAt                 time.Time        `json:"created_at"`
}

// ReserveRequest contains only caller-stable candidates and owner-verified
// admission digests. Authority joins are re-read by the store.
type ReserveRequest struct {
	SchemaVersion                  int64               `json:"schema_version"`
	ReservationID                  string              `json:"reservation_id"`
	ResourceSnapshotID             string              `json:"resource_snapshot_id"`
	CommandID                      string              `json:"command_id"`
	CommandVersion                 int64               `json:"command_version"`
	CommandProfileDigest           string              `json:"command_profile_digest"`
	IdentityFenceDigest            string              `json:"identity_fence_digest"`
	ContentAdmissionSnapshotDigest string              `json:"content_admission_snapshot_digest"`
	Requested                      ResourceVector      `json:"requested"`
	IdempotencyNamespace           string              `json:"idempotency_namespace"`
	IdempotencyKeyDigest           string              `json:"idempotency_key_digest"`
	ReservationRequestDigest       string              `json:"reservation_request_digest"`
	ReservationOperationID         string              `json:"reservation_operation_id"`
	ReservationOperationDigest     string              `json:"reservation_operation_digest"`
	ReservationReceiptRef          string              `json:"reservation_receipt_ref"`
	AdmissionOwner                 AdmissionOwnerFence `json:"admission_owner"`
}

// BindingAnchor is the minimal current-binding identity persisted with a
// reservation. Full equality is enforced at the transaction boundary.
type BindingAnchor struct {
	AgentID        string `json:"agent_id"`
	BindingVersion int64  `json:"binding_version"`
	FenceRef       string `json:"fence_ref"`
}

// ContentAnchor is the complete CONTENT relation persisted by schema v133.
type ContentAnchor struct {
	ReferenceID             string `json:"reference_id"`
	ContentRef              string `json:"content_ref"`
	ObjectID                string `json:"object_id"`
	ObjectVersion           int64  `json:"object_version"`
	RefVersion              int64  `json:"ref_version"`
	OwnerSnapshotDigest     string `json:"owner_snapshot_digest"`
	AdmissionSnapshotDigest string `json:"admission_snapshot_digest"`
}

// ResolveReservedSnapshotRequest contains only the verified proof-chain key.
type ResolveReservedSnapshotRequest struct {
	SchemaVersion                  int64  `json:"schema_version"`
	CommandID                      string `json:"command_id"`
	CommandVersion                 int64  `json:"command_version"`
	CommandProfileDigest           string `json:"command_profile_digest"`
	IdentityFenceDigest            string `json:"identity_fence_digest"`
	ContentAdmissionSnapshotDigest string `json:"content_admission_snapshot_digest"`
}

// ReservationSnapshot is the complete reserved owner snapshot reconstructable
// from the v133 reservation row and its exact effective-policy join.
type ReservationSnapshot struct {
	SchemaVersion              int64               `json:"schema_version"`
	ReservationID              string              `json:"reservation_id"`
	ReservationVersion         int64               `json:"reservation_version"`
	ResourceSnapshotID         string              `json:"resource_snapshot_id"`
	ResourceSnapshotDigest     string              `json:"resource_snapshot_digest"`
	ResourceStatus             string              `json:"resource_status"`
	RequestedKind              string              `json:"requested_kind"`
	CommandID                  string              `json:"command_id"`
	CommandVersion             int64               `json:"command_version"`
	CommandProfileDigest       string              `json:"command_profile_digest"`
	CallerPrincipalRef         string              `json:"caller_principal_ref"`
	ScopeRef                   string              `json:"scope_ref"`
	Source                     BindingAnchor       `json:"source"`
	Target                     BindingAnchor       `json:"target"`
	IdentityFenceDigest        string              `json:"identity_fence_digest"`
	Content                    ContentAnchor       `json:"content"`
	PolicySnapshotID           string              `json:"policy_snapshot_id"`
	PolicyRevision             int64               `json:"policy_revision"`
	PolicySnapshotDigest       string              `json:"policy_snapshot_digest"`
	PolicyCanonicalHash        string              `json:"policy_canonical_hash"`
	EffectiveLimits            ResourceVector      `json:"effective_limits"`
	EffectiveLimitsDigest      string              `json:"effective_limits_digest"`
	Requested                  ResourceVector      `json:"requested"`
	UsageDigestDomain          string              `json:"usage_digest_domain"`
	UsageWatermark             int64               `json:"usage_watermark"`
	UsageDigest                string              `json:"usage_digest"`
	ReleaseConditionDigest     string              `json:"release_condition_digest"`
	IdempotencyNamespace       string              `json:"idempotency_namespace"`
	IdempotencyKeyDigest       string              `json:"idempotency_key_digest"`
	ReservationRequestDigest   string              `json:"reservation_request_digest"`
	ReservationOperationID     string              `json:"reservation_operation_id"`
	ReservationOperationDigest string              `json:"reservation_operation_digest"`
	ReservationReceiptRef      string              `json:"reservation_receipt_ref"`
	ReservationReceiptDigest   string              `json:"reservation_receipt_digest"`
	AdmissionOwner             AdmissionOwnerFence `json:"admission_owner"`
	ValidUntil                 time.Time           `json:"valid_until"`
	CreatedAt                  time.Time           `json:"created_at"`
}

// StoreAPI is the complete RESOURCE v1 persistence surface for this slice.
type StoreAPI interface {
	PublishEffectivePolicy(context.Context, PublishEffectivePolicyRequest) (PolicySnapshot, error)
	Reserve(context.Context, ReserveRequest) (ReservationSnapshot, error)
	Get(context.Context, string) (ReservationSnapshot, error)
	ResolveReservedSnapshot(context.Context, ResolveReservedSnapshotRequest) (ReservationSnapshot, error)
}
