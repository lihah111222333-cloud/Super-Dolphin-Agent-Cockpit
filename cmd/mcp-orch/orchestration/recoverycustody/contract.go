// Package recoverycustody 定义恢复域外 custody 的 transport-neutral 合同。
// 本包不实现 authority、持久化、dispatch 或 command lifecycle 推进。
package recoverycustody

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	RequestDigestDomain = "orch.recovery-custody.request/v1"
	HandleDigestDomain  = "orch.recovery-custody.handle/v1"
	// ReceiptDigestDomain v2 binds quorum/signature proof metadata. Receipts
	// carrying the pre-v2 digest are rejected by Validate; no implicit fallback
	// preserves the unsafe proof-field omission from v1.
	ReceiptDigestDomain = "orch.recovery-custody.receipt/v2"
	StatusPending       = "pending"
	StatusIndeterminate = "indeterminate"
	StatusAvailable     = "available"
	StatusAborted       = "aborted"
	ReceiptAvailability = "availability"
	ReceiptAbort        = "abort_tombstone"
)

var (
	ErrInvalidRequest     = errors.New("recovery custody request is invalid")
	ErrInvalidHandle      = errors.New("recovery custody submission handle is invalid")
	ErrInvalidReceipt     = errors.New("recovery custody receipt is invalid")
	ErrCustodyUnavailable = errors.New("recovery custody authority is unavailable")
	ErrCustodyConflict    = errors.New("recovery custody operation conflicts with the original request")
	ErrOperationNotFound  = errors.New("recovery custody operation was not found")
	ErrReceiptRejected    = errors.New("recovery custody receipt was rejected")
	ErrResolutionConflict = errors.New("recovery custody terminal resolution conflicts with the prior winner")
	ErrContextRequired    = errors.New("recovery custody context is required")
)

// SubmissionRequest 绑定 command profile 与全部 OUT 前置 owner 摘要；缺任一依赖即非法。
type SubmissionRequest struct {
	SchemaVersion          int64  `json:"schema_version"`
	OperationID            string `json:"operation_id"`
	SecurityDomainID       string `json:"security_domain_id"`
	AuthorityProfile       string `json:"authority_profile"`
	CommandID              string `json:"command_id"`
	CommandVersion         int64  `json:"command_version"`
	CommandProfileDigest   string `json:"command_profile_digest"`
	AuthorizationDigest    string `json:"authorization_digest"`
	IdentityFenceDigest    string `json:"identity_fence_digest"`
	ContentSnapshotDigest  string `json:"content_snapshot_digest"`
	ResourceSnapshotDigest string `json:"resource_snapshot_digest"`
	BatchDigest            string `json:"batch_digest"`
	IdempotencyKeyDigest   string `json:"idempotency_key_digest"`
	RequestDigest          string `json:"request_digest"`
}

// SubmissionHandle 是稳定、不可授权的同操作查询句柄。
type SubmissionHandle struct {
	SchemaVersion    int64  `json:"schema_version"`
	OperationID      string `json:"operation_id"`
	SecurityDomainID string `json:"security_domain_id"`
	AuthorityProfile string `json:"authority_profile"`
	CommandID        string `json:"command_id"`
	CommandVersion   int64  `json:"command_version"`
	RequestDigest    string `json:"request_digest"`
	BatchDigest      string `json:"batch_digest"`
	HandleDigest     string `json:"handle_digest"`
}

// ExternalReceipt 只描述未来外部 authority 的 availability 或 abort 证明。
type ExternalReceipt struct {
	SchemaVersion               int64     `json:"schema_version"`
	Kind                        string    `json:"kind"`
	ReceiptID                   string    `json:"receipt_id"`
	AuthorityID                 string    `json:"authority_id"`
	AuthorityClusterID          string    `json:"authority_cluster_id"`
	AuthorityProfile            string    `json:"authority_profile"`
	AuthorityGeneration         int64     `json:"authority_generation"`
	OwnerID                     string    `json:"owner_id"`
	OwnerGeneration             int64     `json:"owner_generation"`
	AuthorityProfileGeneration  int64     `json:"authority_profile_generation"`
	AuthorityProfileDigest      string    `json:"authority_profile_digest"`
	SecurityDomainID            string    `json:"security_domain_id"`
	HeadID                      string    `json:"head_id"`
	HeadRevision                int64     `json:"head_revision"`
	HeadRootDigest              string    `json:"head_root_digest"`
	OperationID                 string    `json:"operation_id"`
	RequestDigest               string    `json:"request_digest"`
	BatchDigest                 string    `json:"batch_digest"`
	SecurityTimeLineageDigest   string    `json:"security_time_lineage_digest"`
	SecurityTimeGeneration      int64     `json:"security_time_generation"`
	SecurityTimeRevision        int64     `json:"security_time_revision"`
	AntiRollbackGeneration      int64     `json:"anti_rollback_generation"`
	AntiRollbackAnchorDigest    string    `json:"anti_rollback_anchor_digest"`
	QuorumAttestationDigest     string    `json:"quorum_attestation_digest"`
	QuorumAttestationRef        string    `json:"quorum_attestation_ref"`
	SignatureDigest             string    `json:"signature_digest"`
	SignatureRef                string    `json:"signature_ref"`
	AuthorityTime               time.Time `json:"authority_time"`
	RetentionPolicyDigest       string    `json:"retention_policy_digest"`
	RetentionUntil              time.Time `json:"retention_until"`
	GCNotBeforeAnchorGeneration int64     `json:"gc_not_before_anchor_generation"`
	ReceiptDigest               string    `json:"receipt_digest"`
}

// Outcome 是 submission 查询结果；available/aborted 必须由 verified external receipt产生。
type Outcome struct {
	Handle     SubmissionHandle `json:"handle"`
	Status     string           `json:"status"`
	ReasonCode string           `json:"reason_code,omitempty"`
	Receipt    *ExternalReceipt `json:"receipt,omitempty"`
}

// Client 是未来外部 custody adapter 的使用方窄端口。
type Client interface {
	Submit(context.Context, SubmissionRequest) (Outcome, error)
	Query(context.Context, SubmissionHandle) (Outcome, error)
}

// ReceiptVerifier 验证外部 authority identity、quorum、签名、代际和 anti-rollback。
type ReceiptVerifier interface {
	VerifyReceipt(context.Context, SubmissionHandle, ExternalReceipt) error
}

// Validate 验证全部依赖摘要与 domain-separated request digest。
func (r SubmissionRequest) Validate() error {
	if r.SchemaVersion != 1 || r.CommandVersion <= 0 {
		return fmt.Errorf("%w: unsupported request or command version", ErrInvalidRequest)
	}
	for _, item := range []struct {
		name, value string
		limit       int
	}{
		{"operation_id", r.OperationID, 128}, {"security_domain_id", r.SecurityDomainID, 256},
		{"authority_profile", r.AuthorityProfile, 128}, {"command_id", r.CommandID, 128},
	} {
		if err := validateCanonicalText(ErrInvalidRequest, item.name, item.value, item.limit); err != nil {
			return err
		}
	}
	for _, item := range requestDigests(r) {
		if err := validateDigest(ErrInvalidRequest, item.name, item.value); err != nil {
			return err
		}
	}
	want, err := ComputeRequestDigest(r)
	if err != nil || want != r.RequestDigest {
		return fmt.Errorf("%w: request digest mismatch", ErrInvalidRequest)
	}
	return nil
}

// Validate 验证查询句柄自身摘要，不把 handle 当授权 fence。
func (h SubmissionHandle) Validate() error {
	if h.SchemaVersion != 1 || h.CommandVersion <= 0 {
		return fmt.Errorf("%w: unsupported handle or command version", ErrInvalidHandle)
	}
	for _, item := range []struct {
		name, value string
		limit       int
	}{
		{"operation_id", h.OperationID, 128}, {"security_domain_id", h.SecurityDomainID, 256},
		{"authority_profile", h.AuthorityProfile, 128}, {"command_id", h.CommandID, 128},
	} {
		if err := validateCanonicalText(ErrInvalidHandle, item.name, item.value, item.limit); err != nil {
			return err
		}
	}
	for _, item := range []struct{ name, value string }{
		{"request_digest", h.RequestDigest}, {"batch_digest", h.BatchDigest}, {"handle_digest", h.HandleDigest},
	} {
		if err := validateDigest(ErrInvalidHandle, item.name, item.value); err != nil {
			return err
		}
	}
	want, err := ComputeHandleDigest(h)
	if err != nil || want != h.HandleDigest {
		return fmt.Errorf("%w: handle digest mismatch", ErrInvalidHandle)
	}
	return nil
}

func requestDigests(r SubmissionRequest) []struct{ name, value string } {
	return []struct{ name, value string }{
		{"command_profile_digest", r.CommandProfileDigest}, {"authorization_digest", r.AuthorizationDigest},
		{"identity_fence_digest", r.IdentityFenceDigest}, {"content_snapshot_digest", r.ContentSnapshotDigest},
		{"resource_snapshot_digest", r.ResourceSnapshotDigest}, {"batch_digest", r.BatchDigest},
		{"idempotency_key_digest", r.IdempotencyKeyDigest}, {"request_digest", r.RequestDigest},
	}
}

// validateCanonicalText 校验合同文本非空、无首尾空白且满足长度与UTF-8边界。
func validateCanonicalText(base error, name, value string, limit int) error {
	if value == "" || strings.TrimSpace(value) != value || len(value) > limit || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return fmt.Errorf("%w: %s must be bounded canonical text", base, name)
	}
	return nil
}

func validateDigest(base error, name, value string) error {
	if len(value) != 64 || strings.ToLower(value) != value {
		return fmt.Errorf("%w: %s must be a lowercase 256-bit digest", base, name)
	}
	for _, char := range value {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return fmt.Errorf("%w: %s is not hexadecimal", base, name)
		}
	}
	return nil
}
