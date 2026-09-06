// Package admissionproof 把完整command profile与CONTENT/RESOURCE owner proof组装为custody请求。
// 本包不实现owner持久化、不调用custody client，也不推进command lifecycle。
package admissionproof

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalidConfig              = errors.New("admission proof configuration is invalid")
	ErrInvalidCommandRecord       = errors.New("admission proof command record is invalid")
	ErrContentOwnerUnavailable    = errors.New("content proof owner is unavailable")
	ErrResourceOwnerUnavailable   = errors.New("resource proof owner is unavailable")
	ErrInvalidContentSnapshot     = errors.New("content proof snapshot is invalid")
	ErrInvalidResourceSnapshot    = errors.New("resource proof snapshot is invalid")
	ErrContentSnapshotMismatch    = errors.New("content proof snapshot does not match command profile")
	ErrResourceSnapshotMismatch   = errors.New("resource proof snapshot does not match command profile")
	ErrContentVerificationFailed  = errors.New("content proof verification failed")
	ErrResourceVerificationFailed = errors.New("resource proof verification failed")
)

// Config 固定未来外部custody的security domain与authority profile；不提供默认值。
type Config struct {
	SecurityDomainID string `json:"security_domain_id"`
	AuthorityProfile string `json:"authority_profile"`
}

// ContentResolveKey 只由持久command/profile派生，刻意不含content ref/object/ticket。
type ContentResolveKey struct {
	SchemaVersion        int64  `json:"schema_version"`
	CommandID            string `json:"command_id"`
	CommandVersion       int64  `json:"command_version"`
	CommandProfileDigest string `json:"command_profile_digest"`
	CallerPrincipalRef   string `json:"caller_principal_ref"`
	ScopeRef             string `json:"scope_ref"`
	SourceAgentID        string `json:"source_agent_id"`
	SourceFenceRef       string `json:"source_fence_ref"`
}

// ContentSnapshot 是CONTENT owner的active/live/readable引用证明，不暴露AEAD/KMS/backend实现。
type ContentSnapshot struct {
	SchemaVersion          int64     `json:"schema_version"`
	SnapshotID             string    `json:"snapshot_id"`
	CommandID              string    `json:"command_id"`
	CommandVersion         int64     `json:"command_version"`
	CommandProfileDigest   string    `json:"command_profile_digest"`
	CallerPrincipalRef     string    `json:"caller_principal_ref"`
	ScopeRef               string    `json:"scope_ref"`
	SourceAgentID          string    `json:"source_agent_id"`
	SourceFenceRef         string    `json:"source_fence_ref"`
	ContentRef             string    `json:"content_ref"`
	ObjectID               string    `json:"object_id"`
	ObjectVersion          int64     `json:"object_version"`
	RefVersion             int64     `json:"ref_version"`
	IngestTicketRef        string    `json:"ingest_ticket_ref"`
	ContentState           string    `json:"content_state"`
	ReferenceState         string    `json:"reference_state"`
	KeyState               string    `json:"key_state"`
	SizeBytes              int64     `json:"size_bytes"`
	MediaType              string    `json:"media_type"`
	RetentionPolicyDigest  string    `json:"retention_policy_digest"`
	OwnerID                string    `json:"owner_id"`
	OwnerGeneration        int64     `json:"owner_generation"`
	WriterFenceRef         string    `json:"writer_fence_ref"`
	SecurityTimeGeneration int64     `json:"security_time_generation"`
	OwnerProofRef          string    `json:"owner_proof_ref"`
	ValidUntil             time.Time `json:"valid_until"`
	SnapshotDigest         string    `json:"snapshot_digest"`
}

// ResourceResolveKey 绑定已经验证的identity/content摘要，不允许caller挑选reservation/policy。
type ResourceResolveKey struct {
	SchemaVersion         int64  `json:"schema_version"`
	CommandID             string `json:"command_id"`
	CommandVersion        int64  `json:"command_version"`
	CommandProfileDigest  string `json:"command_profile_digest"`
	CallerPrincipalRef    string `json:"caller_principal_ref"`
	ScopeRef              string `json:"scope_ref"`
	IdentityFenceDigest   string `json:"identity_fence_digest"`
	ContentSnapshotDigest string `json:"content_snapshot_digest"`
}

// ResourceSnapshot 是RESOURCE owner的reserved proof；具体limit vector与hard cap只以摘要绑定。
type ResourceSnapshot struct {
	SchemaVersion          int64     `json:"schema_version"`
	SnapshotID             string    `json:"snapshot_id"`
	CommandID              string    `json:"command_id"`
	CommandVersion         int64     `json:"command_version"`
	CommandProfileDigest   string    `json:"command_profile_digest"`
	CallerPrincipalRef     string    `json:"caller_principal_ref"`
	ScopeRef               string    `json:"scope_ref"`
	IdentityFenceDigest    string    `json:"identity_fence_digest"`
	ContentSnapshotDigest  string    `json:"content_snapshot_digest"`
	ReservationID          string    `json:"reservation_id"`
	ReservationVersion     int64     `json:"reservation_version"`
	ResourceStatus         string    `json:"resource_status"`
	RequestedKind          string    `json:"requested_kind"`
	PolicySnapshotID       string    `json:"policy_snapshot_id"`
	PolicyRevision         int64     `json:"policy_revision"`
	PolicySnapshotDigest   string    `json:"policy_snapshot_digest"`
	EffectiveLimitsDigest  string    `json:"effective_limits_digest"`
	UsageWatermark         int64     `json:"usage_watermark"`
	UsageDigest            string    `json:"usage_digest"`
	ReleaseConditionDigest string    `json:"release_condition_digest"`
	OwnerID                string    `json:"owner_id"`
	OwnerGeneration        int64     `json:"owner_generation"`
	WriterFenceRef         string    `json:"writer_fence_ref"`
	SecurityTimeGeneration int64     `json:"security_time_generation"`
	OwnerProofRef          string    `json:"owner_proof_ref"`
	ValidUntil             time.Time `json:"valid_until"`
	SnapshotDigest         string    `json:"snapshot_digest"`
}

// ContentResolver 按command-bound key读取CONTENT owner的current snapshot。
type ContentResolver interface {
	ResolveContentSnapshot(context.Context, ContentResolveKey) (ContentSnapshot, error)
}

// ContentVerifier 回验CONTENT owner generation、writer/security-time与proof authority。
type ContentVerifier interface {
	VerifyContentSnapshot(context.Context, ContentResolveKey, ContentSnapshot) error
}

// ResourceResolver 按command/identity/content key读取RESOURCE owner的current reservation snapshot。
type ResourceResolver interface {
	ResolveResourceSnapshot(context.Context, ResourceResolveKey) (ResourceSnapshot, error)
}

// ResourceVerifier 回验RESOURCE policy、usage、writer/security-time与proof authority。
type ResourceVerifier interface {
	VerifyResourceSnapshot(context.Context, ResourceResolveKey, ResourceSnapshot) error
}
