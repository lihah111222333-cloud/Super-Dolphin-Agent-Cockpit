package content

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/contentcrypto"
)

// Validate 校验CONTENT reservation的版本、范围、canonical字段、摘要和ID-1 snapshot。
func (r ReserveIngestRequest) Validate() error {
	if !validReserveHeader(r) {
		return ErrInvalidRequest
	}
	if !validContentTime(r.AssemblyDeadline) {
		return ErrInvalidRequest
	}
	if err := validateReserveTextFields(r); err != nil {
		return err
	}
	if err := validateReserveDigests(r); err != nil {
		return err
	}
	return r.Source.validate()
}

// validReserveHeader 校验reservation固定版本、generation和encrypted-single大小边界。
func validReserveHeader(request ReserveIngestRequest) bool {
	return request.SchemaVersion == 1 && request.OwnerGeneration > 0 && request.SecurityTimeGeneration > 0 &&
		request.DigestKeyVersion > 0 && request.RetentionPolicyVersion > 0 && request.SizeBytes > 0 &&
		request.SizeBytes <= contentcrypto.MaxSingleBytes
}

func validateReserveTextFields(request ReserveIngestRequest) error {
	for _, item := range []struct {
		name, value string
		limit       int
	}{
		{"reservation_id", request.ReservationID, 128}, {"ticket_id", request.TicketID, 128},
		{"object_id", request.ObjectID, 128}, {"owner_id", request.OwnerID, 256},
		{"writer_fence_ref", request.WriterFenceRef, 512}, {"workspace_ref", request.WorkspaceRef, 512},
		{"caller_principal_ref", request.CallerPrincipalRef, 512}, {"scope_ref", request.ScopeRef, 512},
		{"idempotency_namespace", request.IdempotencyNamespace, 256}, {"digest_key_id", request.DigestKeyID, 128},
		{"media_type", request.MediaType, 128}, {"key_wrap_profile", request.KeyWrapProfile, 128},
		{"quota_snapshot_ref", request.QuotaSnapshotRef, 512}, {"retention_policy_id", request.RetentionPolicyID, 128},
	} {
		if err := validateContentText(item.name, item.value, item.limit); err != nil {
			return err
		}
	}
	return nil
}

func validateReserveDigests(request ReserveIngestRequest) error {
	for _, item := range []struct{ name, value string }{
		{"idempotency_key_digest", request.IdempotencyKeyDigest}, {"proof_request_digest", request.ProofRequestDigest},
		{"candidate_ref_digest", request.CandidateRefDigest}, {"content_intent_digest", request.ContentIntentDigest},
		{"quota_snapshot_digest", request.QuotaSnapshotDigest}, {"retention_policy_digest", request.RetentionPolicyDigest},
	} {
		if err := validateContentDigest(item.name, item.value); err != nil {
			return err
		}
	}
	return nil
}

// validate 校验reservation携带的完整ID-1 current binding快照。
func (s SourceBinding) validate() error {
	if s.ReservationGeneration <= 0 || s.IncarnationGeneration <= 0 || s.BindingVersion <= 0 || !validContentTime(s.BindingUpdatedAt) {
		return ErrInvalidRequest
	}
	if !validIdentityKind(s.IdentityKind) {
		return ErrInvalidRequest
	}
	for _, item := range []struct {
		name, value string
		limit       int
	}{
		{"source_agent_id", s.AgentID, 128}, {"source_identity_reservation_id", s.IdentityReservationID, 128},
		{"source_incarnation_id", s.IncarnationID, 128}, {"source_external_identity_ref", s.ExternalIdentityRef, 512},
		{"source_fence_ref", s.FenceRef, 512}, {"source_active_process_receipt_ref", s.ActiveProcessReceiptRef, 512},
	} {
		if err := validateContentText(item.name, item.value, item.limit); err != nil {
			return err
		}
	}
	return validateContentDigest("source_binding_digest", s.BindingDigest)
}

// Validate 拒绝非稳定形状operation以及空或超过64KiB的明文。
func (r PutSinglePreparedRequest) Validate() error {
	if r.SchemaVersion != 1 || !validContentText(r.TicketID, 128) || !validContentText(r.OperationID, 128) {
		return ErrInvalidRequest
	}
	if len(r.Plaintext) == 0 || len(r.Plaintext) > contentcrypto.MaxSingleBytes {
		return ErrInvalidRequest
	}
	return nil
}

// Validate 校验promotion签发live ref所需的全部owner proof字段。
func (r PromoteRequest) Validate() error {
	if r.SchemaVersion != 1 || r.ProofRevision <= 0 || !validContentTime(r.ProofExpiresAt) {
		return ErrInvalidRequest
	}
	for _, item := range []struct {
		name, value string
		limit       int
	}{
		{"ticket_id", r.TicketID, 128}, {"reference_id", r.ReferenceID, 128},
		{"canonical_ref", r.CanonicalRef, 512}, {"proof_id", r.ProofID, 128},
		{"owner_proof_ref", r.OwnerProofRef, 512},
	} {
		if err := validateContentText(item.name, item.value, item.limit); err != nil {
			return err
		}
	}
	return nil
}

// Validate 复验active ref的状态、authority字段、有效期和snapshot self-digest。
func (r ActiveRef) Validate() error {
	if !validActiveRefHeader(r) {
		return ErrContentCorrupt
	}
	if r.ContentState != "active" || r.ReferenceState != "live" || r.KeyState != "active" {
		return ErrContentUnavailable
	}
	if err := validateActiveRefFields(r); err != nil {
		return ErrContentCorrupt
	}
	want, err := computeActiveRefDigest(r)
	if err != nil || want != r.SnapshotDigest {
		return ErrContentCorrupt
	}
	return nil
}

// validActiveRefHeader 校验active ref固定版本、generation、大小和UTC时间。
func validActiveRefHeader(ref ActiveRef) bool {
	return ref.SchemaVersion == 1 && ref.ObjectVersion == 1 && ref.RefVersion == 1 && ref.ProofRevision > 0 &&
		ref.OwnerGeneration > 0 && ref.SecurityTimeGeneration > 0 && ref.SizeBytes > 0 &&
		ref.SizeBytes <= contentcrypto.MaxSingleBytes && validContentTime(ref.ValidUntil) && validContentTime(ref.CreatedAt)
}

func validateActiveRefFields(ref ActiveRef) error {
	for _, item := range []struct {
		name, value string
		limit       int
	}{
		{"reference_id", ref.ReferenceID, 128}, {"canonical_ref", ref.CanonicalRef, 512},
		{"object_id", ref.ObjectID, 128}, {"ticket_id", ref.TicketID, 128},
		{"caller_principal_ref", ref.CallerPrincipalRef, 512}, {"scope_ref", ref.ScopeRef, 512},
		{"source_agent_id", ref.SourceAgentID, 128}, {"source_fence_ref", ref.SourceFenceRef, 512},
		{"proof_id", ref.ProofID, 128}, {"owner_id", ref.OwnerID, 256},
		{"writer_fence_ref", ref.WriterFenceRef, 512}, {"owner_proof_ref", ref.OwnerProofRef, 512},
		{"media_type", ref.MediaType, 128},
	} {
		if err := validateContentText(item.name, item.value, item.limit); err != nil {
			return err
		}
	}
	return validateActiveRefDigests(ref)
}

func validateActiveRefDigests(ref ActiveRef) error {
	for _, item := range []struct{ name, value string }{
		{"proof_request_digest", ref.ProofRequestDigest}, {"snapshot_digest", ref.SnapshotDigest},
		{"retention_policy_digest", ref.RetentionPolicyDigest},
	} {
		if err := validateContentDigest(item.name, item.value); err != nil {
			return err
		}
	}
	return nil
}

func computeActiveRefDigest(ref ActiveRef) (string, error) {
	ref.SnapshotDigest = ""
	ref.CreatedAt = time.Time{}
	payload, err := json.Marshal(struct {
		Domain string    `json:"domain"`
		Ref    ActiveRef `json:"ref"`
	}{Domain: "orch.content.active-ref/v1", Ref: ref})
	if err != nil {
		return "", fmt.Errorf("encode active content ref digest: %w", err)
	}
	return digestBytes(payload), nil
}

func validateContentText(name, value string, limit int) error {
	if !validContentText(value, limit) {
		return fmt.Errorf("%w: %s must be bounded canonical text", ErrInvalidRequest, name)
	}
	return nil
}

func validContentText(value string, limit int) bool {
	return value != "" && len(value) <= limit && utf8.ValidString(value) &&
		strings.TrimSpace(value) == value && !strings.ContainsRune(value, 0)
}

func validateContentDigest(name, value string) error {
	if len(value) != 64 || value != strings.ToLower(value) {
		return fmt.Errorf("%w: %s must be lowercase 256-bit digest", ErrInvalidRequest, name)
	}
	if _, err := hex.DecodeString(value); err != nil {
		return fmt.Errorf("%w: %s is not hexadecimal", ErrInvalidRequest, name)
	}
	return nil
}

func validContentTime(value time.Time) bool { return !value.IsZero() && value.Location() == time.UTC }

func validIdentityKind(kind string) bool {
	switch kind {
	case "provider_session", "native_subagent", "managed_agent", "a2a_remote_agent":
		return true
	default:
		return false
	}
}
