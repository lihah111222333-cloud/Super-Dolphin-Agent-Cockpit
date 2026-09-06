package commandintent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

const contentProofRequestDomain = "orch.command-content-proof.request/v1"
const contentProofSnapshotDomain = "orch.command-content-proof.snapshot/v1"

var (
	ErrContentAdmissionUnavailable = errors.New("content admission proof owner is unavailable")
	ErrInvalidContentProof         = errors.New("content admission proof is invalid")
	ErrContentProofMismatch        = errors.New("content admission proof does not match the authorized request")
)

// ContentAdmissionProofRequest 只把 caller candidate 作为 owner 查找条件；不包含候选 command ID。
type ContentAdmissionProofRequest struct {
	SchemaVersion              int64                    `json:"schema_version"`
	IdempotencyNamespace       string                   `json:"idempotency_namespace"`
	IdempotencyKeyDigest       string                   `json:"idempotency_key_digest"`
	CallerPrincipalRef         string                   `json:"caller_principal_ref"`
	ScopeRef                   string                   `json:"scope_ref"`
	AuthorizationRequestID     string                   `json:"authorization_request_id"`
	AuthorizationRequestDigest string                   `json:"authorization_request_digest"`
	GrantID                    string                   `json:"grant_id"`
	GrantRevision              int64                    `json:"grant_revision"`
	Source                     accesswait.FenceSnapshot `json:"source"`
	Target                     accesswait.FenceSnapshot `json:"target"`
	CandidateRef               string                   `json:"candidate_ref"`
	RequestDigest              string                   `json:"request_digest"`
}

// ContentAdmissionProof 是 CONTENT owner 签发的 active/live/readable ref快照；不是 custody receipt。
type ContentAdmissionProof struct {
	SchemaVersion   int64     `json:"schema_version"`
	OwnerID         string    `json:"owner_id"`
	OwnerGeneration int64     `json:"owner_generation"`
	ProofID         string    `json:"proof_id"`
	ProofRevision   int64     `json:"proof_revision"`
	RequestDigest   string    `json:"request_digest"`
	CanonicalRef    string    `json:"canonical_ref"`
	ObjectID        string    `json:"object_id"`
	ObjectVersion   int64     `json:"object_version"`
	RefVersion      int64     `json:"ref_version"`
	ContentState    string    `json:"content_state"`
	ReferenceState  string    `json:"reference_state"`
	KeyReadability  string    `json:"key_readability"`
	MediaType       string    `json:"media_type"`
	SizeBytes       int64     `json:"size_bytes"`
	SourceAgentID   string    `json:"source_agent_id"`
	SourceFenceRef  string    `json:"source_fence_ref"`
	OwnerProofRef   string    `json:"owner_proof_ref"`
	SnapshotDigest  string    `json:"snapshot_digest"`
	ExpiresAt       time.Time `json:"expires_at"`
}

// ContentAdmissionProofIssuer 是 assembler消费的CONTENT owner窄口；实现必须返回owner-issued ref。
type ContentAdmissionProofIssuer interface {
	IssueContentAdmissionProof(context.Context, ContentAdmissionProofRequest) (ContentAdmissionProof, error)
}

// UnavailableContentAdmissionProofIssuer 是当前唯一production-safe adapter；不读取sharedfile或本地临时密钥。
type UnavailableContentAdmissionProofIssuer struct{}

// IssueContentAdmissionProof 明确拒绝；不能把candidate ref原样升级为canonical ref。
func (UnavailableContentAdmissionProofIssuer) IssueContentAdmissionProof(context.Context, ContentAdmissionProofRequest) (ContentAdmissionProof, error) {
	return ContentAdmissionProof{}, ErrContentAdmissionUnavailable
}

func buildContentProofRequest(request ReserveAuthorizedSendDraftRequest, principal common.CallerPrincipal, authorization accesswait.Authorization) (ContentAdmissionProofRequest, error) {
	proofRequest := ContentAdmissionProofRequest{
		SchemaVersion: 1, IdempotencyNamespace: request.IdempotencyNamespace,
		IdempotencyKeyDigest: request.IdempotencyKeyDigest, CallerPrincipalRef: principal.PrincipalRef,
		ScopeRef: principal.ScopeRef, AuthorizationRequestID: authorization.RequestID,
		AuthorizationRequestDigest: request.Access.RequestDigest, GrantID: authorization.GrantID,
		GrantRevision: authorization.GrantRevision, Source: authorization.Grant.Source,
		Target: authorization.Grant.Target, CandidateRef: request.RequestContentCandidateRef,
	}
	digest, err := computeContentProofRequestDigest(proofRequest)
	if err != nil {
		return ContentAdmissionProofRequest{}, err
	}
	proofRequest.RequestDigest = digest
	if err := proofRequest.validate(); err != nil {
		return ContentAdmissionProofRequest{}, err
	}
	return proofRequest, nil
}

// validate 串联proof lookup的版本、文本、摘要、fence和self-digest校验。
func (r ContentAdmissionProofRequest) validate() error {
	if r.SchemaVersion != 1 || r.GrantRevision <= 0 {
		return ErrInvalidContentProof
	}
	if err := r.validateTextFields(); err != nil {
		return err
	}
	if err := r.validateDigests(); err != nil {
		return err
	}
	if err := r.validateFences(); err != nil {
		return err
	}
	want, err := computeContentProofRequestDigest(r)
	if err != nil || want != r.RequestDigest {
		return ErrInvalidContentProof
	}
	return nil
}

// validateTextFields 校验CONTENT proof lookup的canonical文本，不做默认归一化。
func (r ContentAdmissionProofRequest) validateTextFields() error {
	for _, item := range []struct {
		name, value string
		limit       int
	}{
		{"idempotency_namespace", r.IdempotencyNamespace, 256}, {"caller_principal_ref", r.CallerPrincipalRef, 512},
		{"scope_ref", r.ScopeRef, 512}, {"authorization_request_id", r.AuthorizationRequestID, 128},
		{"grant_id", r.GrantID, 128}, {"candidate_ref", r.CandidateRef, 512},
	} {
		if err := validateContentProofText(item.name, item.value, item.limit); err != nil {
			return err
		}
	}
	return nil
}

// validateDigests 拒绝缺失或非canonical的幂等和授权摘要。
func (r ContentAdmissionProofRequest) validateDigests() error {
	for _, item := range []struct{ name, value string }{
		{"idempotency_key_digest", r.IdempotencyKeyDigest}, {"authorization_request_digest", r.AuthorizationRequestDigest},
		{"request_digest", r.RequestDigest},
	} {
		if err := validateContentProofDigest(item.name, item.value); err != nil {
			return err
		}
	}
	return nil
}

// validateFences 要求proof lookup绑定授权后的双端active incarnation fence。
func (r ContentAdmissionProofRequest) validateFences() error {
	if err := r.Source.ValidateBound(); err != nil {
		return fmt.Errorf("%w: source fence: %v", ErrInvalidContentProof, err)
	}
	if err := r.Target.ValidateBound(); err != nil {
		return fmt.Errorf("%w: target fence: %v", ErrInvalidContentProof, err)
	}
	return nil
}

// validateContentProof 比较owner proof与授权链，并验证其内部snapshot摘要。
func validateContentProof(proofRequest ContentAdmissionProofRequest, proof ContentAdmissionProof, principal common.CallerPrincipal, authorization accesswait.Authorization) error {
	if err := proof.validateShape(); err != nil {
		return err
	}
	if proof.RequestDigest != proofRequest.RequestDigest || proof.SourceAgentID != proofRequest.Source.AgentID || proof.SourceFenceRef != proofRequest.Source.SourceFenceRef {
		return ErrContentProofMismatch
	}
	if !proof.ExpiresAt.After(authorization.AuthorizedAt) || proof.ExpiresAt.After(authorization.ExpiresAt) || proof.ExpiresAt.After(principal.ExpiresAt) {
		return ErrContentProofMismatch
	}
	want, err := computeContentProofSnapshotDigest(proof)
	if err != nil || want != proof.SnapshotDigest {
		return ErrInvalidContentProof
	}
	return nil
}

// validateShape 串联owner proof的版本状态、文本、摘要和UTC有效期校验。
func (p ContentAdmissionProof) validateShape() error {
	if err := p.validateVersionsAndState(); err != nil {
		return err
	}
	if err := p.validateTextFields(); err != nil {
		return err
	}
	if err := p.validateDigests(); err != nil {
		return err
	}
	if p.ExpiresAt.IsZero() || p.ExpiresAt.Location() != time.UTC {
		return ErrInvalidContentProof
	}
	return nil
}

// validateVersionsAndState 锁定proof的active/live/readable形态及正版本。
func (p ContentAdmissionProof) validateVersionsAndState() error {
	if p.SchemaVersion != 1 || p.OwnerGeneration <= 0 || p.ProofRevision <= 0 {
		return ErrInvalidContentProof
	}
	if p.ObjectVersion <= 0 || p.RefVersion <= 0 || p.SizeBytes <= 0 {
		return ErrInvalidContentProof
	}
	if p.ContentState != "active" || p.ReferenceState != "live" || p.KeyReadability != "readable" {
		return ErrInvalidContentProof
	}
	return nil
}

// validateTextFields 校验owner/ref/object/evidence文本边界。
func (p ContentAdmissionProof) validateTextFields() error {
	for _, item := range []struct {
		name, value string
		limit       int
	}{
		{"owner_id", p.OwnerID, 256}, {"proof_id", p.ProofID, 128}, {"canonical_ref", p.CanonicalRef, 512},
		{"object_id", p.ObjectID, 128}, {"media_type", p.MediaType, 128}, {"source_agent_id", p.SourceAgentID, 128},
		{"source_fence_ref", p.SourceFenceRef, 512}, {"owner_proof_ref", p.OwnerProofRef, 512},
	} {
		if err := validateContentProofText(item.name, item.value, item.limit); err != nil {
			return err
		}
	}
	return nil
}

// validateDigests 校验proof的request与snapshot摘要形状。
func (p ContentAdmissionProof) validateDigests() error {
	for _, item := range []struct{ name, value string }{
		{"request_digest", p.RequestDigest}, {"snapshot_digest", p.SnapshotDigest},
	} {
		if err := validateContentProofDigest(item.name, item.value); err != nil {
			return err
		}
	}
	return nil
}

func computeContentProofRequestDigest(request ContentAdmissionProofRequest) (string, error) {
	request.RequestDigest = ""
	return contentProofDigest(contentProofRequestDomain, request)
}

func computeContentProofSnapshotDigest(proof ContentAdmissionProof) (string, error) {
	proof.SnapshotDigest = ""
	return contentProofDigest(contentProofSnapshotDomain, proof)
}

func contentProofDigest(domain string, value any) (string, error) {
	payload, err := json.Marshal(struct {
		Domain string `json:"domain"`
		Value  any    `json:"value"`
	}{Domain: domain, Value: value})
	if err != nil {
		return "", fmt.Errorf("encode content proof digest: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

// validateContentProofText 拒绝空、隐式trim、NUL、非法UTF-8与超限文本。
func validateContentProofText(name, value string, limit int) error {
	if value == "" || strings.TrimSpace(value) != value || len(value) > limit || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return fmt.Errorf("%w: %s must be bounded canonical text", ErrInvalidContentProof, name)
	}
	return nil
}

func validateContentProofDigest(name, value string) error {
	if len(value) != 64 || strings.ToLower(value) != value {
		return fmt.Errorf("%w: %s must be a lowercase 256-bit digest", ErrInvalidContentProof, name)
	}
	if _, err := hex.DecodeString(value); err != nil {
		return fmt.Errorf("%w: %s is not hexadecimal", ErrInvalidContentProof, name)
	}
	return nil
}

var _ ContentAdmissionProofIssuer = UnavailableContentAdmissionProofIssuer{}
