// Package content 是CONTENT-1 ingest/object/ref metadata的唯一SQLite写owner。
// 本包不提供decrypt、delete、destroy、GC、provider dispatch或recovery custody。
package content

import (
	"context"
	"errors"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/contentobject"
	"time"
)

var (
	ErrInvalidRequest     = errors.New("content owner request is invalid")
	ErrStoreUninitialized = errors.New("content owner is not initialized")
	ErrIngestNotFound     = errors.New("content ingest reservation is not found")
	ErrIngestExpired      = errors.New("content ingest reservation is expired")
	ErrReplayConflict     = errors.New("content operation conflicts with the original request")
	ErrInvalidTransition  = errors.New("content state transition is invalid")
	ErrReceiptMismatch    = errors.New("content seal or object receipt mismatch")
	ErrContentCorrupt     = errors.New("content owner record is corrupt")
	ErrContentNotFound    = errors.New("content ref is not found")
	ErrContentUnavailable = errors.New("content ref is unavailable")
)

// SourceBinding 保存task来源的完整ID-1绑定快照。
type SourceBinding struct {
	AgentID                 string    `json:"agent_id"`
	IdentityKind            string    `json:"identity_kind"`
	IdentityReservationID   string    `json:"identity_reservation_id"`
	ReservationGeneration   int64     `json:"reservation_generation"`
	IncarnationID           string    `json:"incarnation_id"`
	IncarnationGeneration   int64     `json:"incarnation_generation"`
	ExternalIdentityRef     string    `json:"external_identity_ref"`
	BindingVersion          int64     `json:"binding_version"`
	BindingDigest           string    `json:"binding_digest"`
	FenceRef                string    `json:"fence_ref"`
	ActiveProcessReceiptRef string    `json:"active_process_receipt_ref"`
	BindingUpdatedAt        time.Time `json:"binding_updated_at"`
}

// ReserveIngestRequest 描述CONTENT reservation及其不可变来源摘要。
type ReserveIngestRequest struct {
	SchemaVersion          int64         `json:"schema_version"`
	ReservationID          string        `json:"reservation_id"`
	TicketID               string        `json:"ticket_id"`
	ObjectID               string        `json:"object_id"`
	OwnerID                string        `json:"owner_id"`
	OwnerGeneration        int64         `json:"owner_generation"`
	WriterFenceRef         string        `json:"writer_fence_ref"`
	SecurityTimeGeneration int64         `json:"security_time_generation"`
	WorkspaceRef           string        `json:"workspace_ref"`
	CallerPrincipalRef     string        `json:"caller_principal_ref"`
	ScopeRef               string        `json:"scope_ref"`
	Source                 SourceBinding `json:"source"`
	IdempotencyNamespace   string        `json:"idempotency_namespace"`
	IdempotencyKeyDigest   string        `json:"idempotency_key_digest"`
	ProofRequestDigest     string        `json:"proof_request_digest"`
	CandidateRefDigest     string        `json:"candidate_ref_digest"`
	ContentIntentDigest    string        `json:"content_intent_digest"`
	DigestKeyID            string        `json:"digest_key_id"`
	DigestKeyVersion       int64         `json:"digest_key_version"`
	SizeBytes              int64         `json:"size_bytes"`
	MediaType              string        `json:"media_type"`
	KeyWrapProfile         string        `json:"key_wrap_profile"`
	QuotaSnapshotRef       string        `json:"quota_snapshot_ref"`
	QuotaSnapshotDigest    string        `json:"quota_snapshot_digest"`
	RetentionPolicyID      string        `json:"retention_policy_id"`
	RetentionPolicyVersion int64         `json:"retention_policy_version"`
	RetentionPolicyDigest  string        `json:"retention_policy_digest"`
	AssemblyDeadline       time.Time     `json:"assembly_deadline"`
}

// IngestReservation 是严格校验后的CONTENT reservation。
type IngestReservation struct {
	ReserveIngestRequest
	ObjectVersion      int64     `json:"object_version"`
	PrepareOperationID string    `json:"prepare_operation_id"`
	PrepareStartedAt   time.Time `json:"prepare_started_at"`
	State              string    `json:"state"`
	Revision           int64     `json:"revision"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// PutSinglePreparedRequest 携带有界明文进入prepared对象流程。
type PutSinglePreparedRequest struct {
	SchemaVersion int64  `json:"schema_version"`
	TicketID      string `json:"ticket_id"`
	OperationID   string `json:"operation_id"`
	Plaintext     []byte `json:"-"`
}

// PreparedObject 是密封和对象写入均有receipt后的prepared对象。
type PreparedObject struct {
	SchemaVersion    int64                    `json:"schema_version"`
	ReservationID    string                   `json:"reservation_id"`
	ObjectID         string                   `json:"object_id"`
	ObjectVersion    int64                    `json:"object_version"`
	State            string                   `json:"state"`
	KeyState         string                   `json:"key_state"`
	KeyHandleRef     string                   `json:"key_handle_ref"`
	KeyReceiptRef    string                   `json:"key_receipt_ref"`
	KeyReceiptDigest string                   `json:"key_receipt_digest"`
	NonceHex         string                   `json:"nonce_hex"`
	AADDigest        string                   `json:"aad_digest"`
	CiphertextHash   string                   `json:"ciphertext_hash"`
	CiphertextSize   int64                    `json:"ciphertext_size"`
	ObjectPutReceipt contentobject.PutReceipt `json:"object_put_receipt"`
	PreparedAt       time.Time                `json:"prepared_at"`
}

// PromoteRequest 描述prepared对象到active ref的不可变证明。
type PromoteRequest struct {
	SchemaVersion  int64     `json:"schema_version"`
	TicketID       string    `json:"ticket_id"`
	ReferenceID    string    `json:"reference_id"`
	CanonicalRef   string    `json:"canonical_ref"`
	ProofID        string    `json:"proof_id"`
	ProofRevision  int64     `json:"proof_revision"`
	OwnerProofRef  string    `json:"owner_proof_ref"`
	ProofExpiresAt time.Time `json:"proof_expires_at"`
}

// ActiveRef 是CONTENT owner签发的active/live引用快照。
type ActiveRef struct {
	SchemaVersion          int64     `json:"schema_version"`
	ReferenceID            string    `json:"reference_id"`
	CanonicalRef           string    `json:"canonical_ref"`
	ObjectID               string    `json:"object_id"`
	ObjectVersion          int64     `json:"object_version"`
	RefVersion             int64     `json:"ref_version"`
	TicketID               string    `json:"ticket_id"`
	ContentState           string    `json:"content_state"`
	ReferenceState         string    `json:"reference_state"`
	KeyState               string    `json:"key_state"`
	CallerPrincipalRef     string    `json:"caller_principal_ref"`
	ScopeRef               string    `json:"scope_ref"`
	SourceAgentID          string    `json:"source_agent_id"`
	SourceFenceRef         string    `json:"source_fence_ref"`
	ProofRequestDigest     string    `json:"proof_request_digest"`
	ProofID                string    `json:"proof_id"`
	ProofRevision          int64     `json:"proof_revision"`
	OwnerID                string    `json:"owner_id"`
	OwnerGeneration        int64     `json:"owner_generation"`
	WriterFenceRef         string    `json:"writer_fence_ref"`
	SecurityTimeGeneration int64     `json:"security_time_generation"`
	OwnerProofRef          string    `json:"owner_proof_ref"`
	SnapshotDigest         string    `json:"snapshot_digest"`
	ValidUntil             time.Time `json:"valid_until"`
	MediaType              string    `json:"media_type"`
	SizeBytes              int64     `json:"size_bytes"`
	RetentionPolicyDigest  string    `json:"retention_policy_digest"`
	CreatedAt              time.Time `json:"created_at"`
}

// ResolveActiveRefRequest 按canonical ref请求严格解析。
type ResolveActiveRefRequest struct {
	CanonicalRef string `json:"canonical_ref"`
}

// StoreAPI 是CONTENT组合层消费的窄owner接口。
type StoreAPI interface {
	ReserveIngest(context.Context, ReserveIngestRequest) (IngestReservation, error)
	PutSinglePrepared(context.Context, PutSinglePreparedRequest) (PreparedObject, error)
	Promote(context.Context, PromoteRequest) (ActiveRef, error)
	ResolveActiveRef(context.Context, ResolveActiveRefRequest) (ActiveRef, error)
}
