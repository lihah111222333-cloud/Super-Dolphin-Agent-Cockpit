package recoverycustody

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

type receiptDigestPayload struct {
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
}

// FinalizeRequestDigest 计算并写回 request digest；调用方不能传入旧摘要覆盖新依赖。
func FinalizeRequestDigest(request SubmissionRequest) (SubmissionRequest, error) {
	request.RequestDigest = ""
	digest, err := ComputeRequestDigest(request)
	if err != nil {
		return SubmissionRequest{}, err
	}
	request.RequestDigest = digest
	return request, nil
}

// ComputeRequestDigest 对所有前置 owner 摘要做固定 struct JSON + SHA-256 domain separation。
func ComputeRequestDigest(request SubmissionRequest) (string, error) {
	request.RequestDigest = ""
	return digestCanonical(RequestDigestDomain, request)
}

// NewSubmissionHandle 从已验证 request 派生稳定 non-authorizing handle。
func NewSubmissionHandle(request SubmissionRequest) (SubmissionHandle, error) {
	if err := request.Validate(); err != nil {
		return SubmissionHandle{}, err
	}
	handle := SubmissionHandle{
		SchemaVersion: 1, OperationID: request.OperationID, SecurityDomainID: request.SecurityDomainID,
		AuthorityProfile: request.AuthorityProfile, CommandID: request.CommandID, CommandVersion: request.CommandVersion,
		RequestDigest: request.RequestDigest, BatchDigest: request.BatchDigest,
	}
	digest, err := ComputeHandleDigest(handle)
	if err != nil {
		return SubmissionHandle{}, err
	}
	handle.HandleDigest = digest
	return handle, nil
}

// MatchSubmission 确认 caller 只用原 request 查询原 handle；同 operation 异 digest 明确冲突。
func MatchSubmission(handle SubmissionHandle, request SubmissionRequest) error {
	if err := handle.Validate(); err != nil {
		return err
	}
	if err := request.Validate(); err != nil {
		return err
	}
	if handle.OperationID != request.OperationID || handle.SecurityDomainID != request.SecurityDomainID ||
		handle.AuthorityProfile != request.AuthorityProfile || handle.CommandID != request.CommandID ||
		handle.CommandVersion != request.CommandVersion || handle.RequestDigest != request.RequestDigest ||
		handle.BatchDigest != request.BatchDigest {
		return ErrCustodyConflict
	}
	return nil
}

// ComputeHandleDigest 绑定原 operation/request/batch；排除摘要自身避免递归。
func ComputeHandleDigest(handle SubmissionHandle) (string, error) {
	handle.HandleDigest = ""
	return digestCanonical(HandleDigestDomain, handle)
}

// FinalizeReceiptDigest 计算并写回 receipt digest；证明摘要和内容寻址引用均受摘要保护，只有摘要自身不参与计算。
func FinalizeReceiptDigest(receipt ExternalReceipt) (ExternalReceipt, error) {
	receipt.ReceiptDigest = ""
	digest, err := ComputeReceiptDigest(receipt)
	if err != nil {
		return ExternalReceipt{}, err
	}
	receipt.ReceiptDigest = digest
	return receipt, nil
}

// ComputeReceiptDigest 绑定 receipt 的全部安全声明和 quorum/signature 证明元数据。
// ReceiptDigestDomain 使用 v2；它故意不兼容排除证明元数据的 v1 摘要，旧摘要不会隐式通过验证。
func ComputeReceiptDigest(receipt ExternalReceipt) (string, error) {
	return digestCanonical(ReceiptDigestDomain, newReceiptDigestPayload(receipt))
}

func newReceiptDigestPayload(receipt ExternalReceipt) receiptDigestPayload {
	return receiptDigestPayload{
		SchemaVersion: receipt.SchemaVersion, Kind: receipt.Kind, ReceiptID: receipt.ReceiptID,
		AuthorityID: receipt.AuthorityID, AuthorityClusterID: receipt.AuthorityClusterID,
		AuthorityProfile: receipt.AuthorityProfile, AuthorityGeneration: receipt.AuthorityGeneration,
		OwnerID: receipt.OwnerID, OwnerGeneration: receipt.OwnerGeneration,
		AuthorityProfileGeneration: receipt.AuthorityProfileGeneration,
		AuthorityProfileDigest:     receipt.AuthorityProfileDigest, SecurityDomainID: receipt.SecurityDomainID,
		HeadID: receipt.HeadID, HeadRevision: receipt.HeadRevision, HeadRootDigest: receipt.HeadRootDigest,
		OperationID: receipt.OperationID, RequestDigest: receipt.RequestDigest, BatchDigest: receipt.BatchDigest,
		SecurityTimeLineageDigest: receipt.SecurityTimeLineageDigest,
		SecurityTimeGeneration:    receipt.SecurityTimeGeneration, SecurityTimeRevision: receipt.SecurityTimeRevision,
		AntiRollbackGeneration:   receipt.AntiRollbackGeneration,
		AntiRollbackAnchorDigest: receipt.AntiRollbackAnchorDigest, AuthorityTime: receipt.AuthorityTime,
		QuorumAttestationDigest: receipt.QuorumAttestationDigest, QuorumAttestationRef: receipt.QuorumAttestationRef,
		SignatureDigest: receipt.SignatureDigest, SignatureRef: receipt.SignatureRef,
		RetentionPolicyDigest: receipt.RetentionPolicyDigest, RetentionUntil: receipt.RetentionUntil,
		GCNotBeforeAnchorGeneration: receipt.GCNotBeforeAnchorGeneration,
	}
}

func digestCanonical(domain string, value any) (string, error) {
	payload, err := json.Marshal(struct {
		Domain string `json:"domain"`
		Value  any    `json:"value"`
	}{Domain: domain, Value: value})
	if err != nil {
		return "", fmt.Errorf("encode recovery custody digest payload: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}
