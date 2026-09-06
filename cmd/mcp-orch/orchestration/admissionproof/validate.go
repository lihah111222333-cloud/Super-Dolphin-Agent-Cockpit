package admissionproof

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// validate 串联CONTENT snapshot的版本状态、文本、摘要、时间与self-digest校验。
func (s ContentSnapshot) validate() error {
	if err := s.validateVersionsAndState(); err != nil {
		return err
	}
	if err := validateContentTexts(s); err != nil {
		return err
	}
	if err := validateContentDigests(s); err != nil {
		return err
	}
	if !validAdmissionTime(s.ValidUntil) {
		return ErrInvalidContentSnapshot
	}
	want, err := ComputeContentSnapshotDigest(s)
	if err != nil || want != s.SnapshotDigest {
		return ErrInvalidContentSnapshot
	}
	return nil
}

// validateVersionsAndState 锁定CONTENT active/live/readable状态和正版本。
func (s ContentSnapshot) validateVersionsAndState() error {
	if s.SchemaVersion != 1 {
		return ErrInvalidContentSnapshot
	}
	for _, version := range []int64{s.CommandVersion, s.ObjectVersion, s.RefVersion, s.OwnerGeneration, s.SecurityTimeGeneration, s.SizeBytes} {
		if version <= 0 {
			return ErrInvalidContentSnapshot
		}
	}
	if !contentSnapshotStateAllowed(s) {
		return ErrInvalidContentSnapshot
	}
	return nil
}

func contentSnapshotStateAllowed(snapshot ContentSnapshot) bool {
	keyReadable := snapshot.KeyState == "active" || snapshot.KeyState == "retired_readable"
	return snapshot.ContentState == "active" && snapshot.ReferenceState == "live" && keyReadable
}

func validateContentTexts(snapshot ContentSnapshot) error {
	for _, item := range []struct {
		name, value string
		limit       int
	}{
		{"snapshot_id", snapshot.SnapshotID, 128}, {"command_id", snapshot.CommandID, 128},
		{"caller_principal_ref", snapshot.CallerPrincipalRef, 512}, {"scope_ref", snapshot.ScopeRef, 512},
		{"source_agent_id", snapshot.SourceAgentID, 128}, {"source_fence_ref", snapshot.SourceFenceRef, 512},
		{"content_ref", snapshot.ContentRef, 512},
		{"object_id", snapshot.ObjectID, 128}, {"ingest_ticket_ref", snapshot.IngestTicketRef, 512},
		{"media_type", snapshot.MediaType, 128}, {"writer_fence_ref", snapshot.WriterFenceRef, 512},
		{"owner_id", snapshot.OwnerID, 256}, {"owner_proof_ref", snapshot.OwnerProofRef, 512},
	} {
		if err := validateProofText(ErrInvalidContentSnapshot, item.name, item.value, item.limit); err != nil {
			return err
		}
	}
	return nil
}

func validateContentDigests(snapshot ContentSnapshot) error {
	for _, item := range []struct{ name, value string }{
		{"command_profile_digest", snapshot.CommandProfileDigest},
		{"retention_policy_digest", snapshot.RetentionPolicyDigest}, {"snapshot_digest", snapshot.SnapshotDigest},
	} {
		if err := validateProofDigest(ErrInvalidContentSnapshot, item.name, item.value); err != nil {
			return err
		}
	}
	return nil
}

// validate 串联RESOURCE snapshot的版本状态、文本、摘要、时间与self-digest校验。
func (s ResourceSnapshot) validate() error {
	if err := s.validateVersionsAndState(); err != nil {
		return err
	}
	if err := validateResourceTexts(s); err != nil {
		return err
	}
	if err := validateResourceDigests(s); err != nil {
		return err
	}
	want, err := ComputeResourceSnapshotDigest(s)
	if err != nil || want != s.SnapshotDigest {
		return ErrInvalidResourceSnapshot
	}
	return nil
}

// validateVersionsAndState 锁定RESOURCE reserved状态、正版本和结构化有效期。
func (s ResourceSnapshot) validateVersionsAndState() error {
	if s.SchemaVersion != 1 {
		return ErrInvalidResourceSnapshot
	}
	for _, version := range []int64{s.CommandVersion, s.ReservationVersion, s.PolicyRevision, s.OwnerGeneration, s.SecurityTimeGeneration} {
		if version <= 0 {
			return ErrInvalidResourceSnapshot
		}
	}
	if s.UsageWatermark < 0 || s.ResourceStatus != "reserved" {
		return ErrInvalidResourceSnapshot
	}
	if s.RequestedKind != "send_message" || !validAdmissionTime(s.ValidUntil) {
		return ErrInvalidResourceSnapshot
	}
	return nil
}

func validateResourceTexts(snapshot ResourceSnapshot) error {
	for _, item := range []struct {
		name, value string
		limit       int
	}{
		{"snapshot_id", snapshot.SnapshotID, 128}, {"command_id", snapshot.CommandID, 128},
		{"caller_principal_ref", snapshot.CallerPrincipalRef, 512}, {"scope_ref", snapshot.ScopeRef, 512},
		{"reservation_id", snapshot.ReservationID, 128}, {"policy_snapshot_id", snapshot.PolicySnapshotID, 128},
		{"owner_id", snapshot.OwnerID, 256}, {"writer_fence_ref", snapshot.WriterFenceRef, 512},
		{"owner_proof_ref", snapshot.OwnerProofRef, 512},
	} {
		if err := validateProofText(ErrInvalidResourceSnapshot, item.name, item.value, item.limit); err != nil {
			return err
		}
	}
	return nil
}

func validateResourceDigests(snapshot ResourceSnapshot) error {
	for _, item := range []struct{ name, value string }{
		{"command_profile_digest", snapshot.CommandProfileDigest}, {"identity_fence_digest", snapshot.IdentityFenceDigest},
		{"content_snapshot_digest", snapshot.ContentSnapshotDigest}, {"policy_snapshot_digest", snapshot.PolicySnapshotDigest},
		{"effective_limits_digest", snapshot.EffectiveLimitsDigest}, {"usage_digest", snapshot.UsageDigest},
		{"release_condition_digest", snapshot.ReleaseConditionDigest}, {"snapshot_digest", snapshot.SnapshotDigest},
	} {
		if err := validateProofDigest(ErrInvalidResourceSnapshot, item.name, item.value); err != nil {
			return err
		}
	}
	return nil
}

// validateProofText 拒绝空、隐式trim、NUL、非法UTF-8与超限文本。
func validateProofText(base error, name, value string, limit int) error {
	if value == "" || strings.TrimSpace(value) != value || len(value) > limit || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return fmt.Errorf("%w: %s must be bounded canonical text", base, name)
	}
	return nil
}

func validateProofDigest(base error, name, value string) error {
	if len(value) != 64 || strings.ToLower(value) != value {
		return fmt.Errorf("%w: %s must be a lowercase 256-bit digest", base, name)
	}
	if _, err := hex.DecodeString(value); err != nil {
		return fmt.Errorf("%w: %s is not hexadecimal", base, name)
	}
	return nil
}

func validAdmissionTime(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC
}
