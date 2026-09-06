package recoverycustody

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"
)

// Validate 验证外部 receipt 的结构；authority真实性仍必须由 ReceiptVerifier确认。
func (r ExternalReceipt) Validate() error {
	if err := validateReceiptHeader(r); err != nil {
		return err
	}
	if err := validateReceiptTextFields(r); err != nil {
		return err
	}
	if err := validateReceiptDigests(r); err != nil {
		return err
	}
	return validateReceiptDigest(r)
}

func validateReceiptHeader(receipt ExternalReceipt) error {
	if receipt.SchemaVersion != 1 {
		return fmt.Errorf("%w: receipt schema version is invalid", ErrInvalidReceipt)
	}
	if receipt.Kind != ReceiptAvailability && receipt.Kind != ReceiptAbort {
		return fmt.Errorf("%w: receipt kind is unsupported", ErrInvalidReceipt)
	}
	if err := validateReceiptGenerations(receipt); err != nil {
		return err
	}
	return validateReceiptTimes(receipt)
}

func validateReceiptGenerations(receipt ExternalReceipt) error {
	values := []int64{
		receipt.AuthorityGeneration, receipt.OwnerGeneration, receipt.AuthorityProfileGeneration,
		receipt.HeadRevision, receipt.SecurityTimeGeneration, receipt.SecurityTimeRevision,
		receipt.AntiRollbackGeneration, receipt.GCNotBeforeAnchorGeneration,
	}
	for _, value := range values {
		if value <= 0 {
			return fmt.Errorf("%w: receipt generation or revision is invalid", ErrInvalidReceipt)
		}
	}
	if receipt.GCNotBeforeAnchorGeneration < receipt.AntiRollbackGeneration {
		return fmt.Errorf("%w: gc anchor generation precedes anti-rollback generation", ErrInvalidReceipt)
	}
	return nil
}

func validateReceiptTimes(receipt ExternalReceipt) error {
	if !isCanonicalReceiptTime(receipt.AuthorityTime) || !isCanonicalReceiptTime(receipt.RetentionUntil) {
		return fmt.Errorf("%w: receipt authority or retention time is invalid", ErrInvalidReceipt)
	}
	if !receipt.RetentionUntil.After(receipt.AuthorityTime) {
		return fmt.Errorf("%w: receipt retention must outlive authority time", ErrInvalidReceipt)
	}
	return nil
}

func isCanonicalReceiptTime(value time.Time) bool {
	return !value.IsZero() && value.Location() == timeUTC()
}

func validateReceiptTextFields(receipt ExternalReceipt) error {
	for _, item := range []struct {
		name, value string
		limit       int
	}{
		{"receipt_id", receipt.ReceiptID, 128}, {"authority_id", receipt.AuthorityID, 256},
		{"authority_cluster_id", receipt.AuthorityClusterID, 256}, {"authority_profile", receipt.AuthorityProfile, 128},
		{"owner_id", receipt.OwnerID, 256}, {"security_domain_id", receipt.SecurityDomainID, 256},
		{"head_id", receipt.HeadID, 256}, {"operation_id", receipt.OperationID, 128},
	} {
		if err := validateCanonicalText(ErrInvalidReceipt, item.name, item.value, item.limit); err != nil {
			return err
		}
	}
	if err := validateEvidenceRef("quorum_attestation_ref", receipt.QuorumAttestationRef, "quorum://", receipt.QuorumAttestationDigest); err != nil {
		return err
	}
	return validateEvidenceRef("signature_ref", receipt.SignatureRef, "signature://", receipt.SignatureDigest)
}

func validateReceiptDigests(receipt ExternalReceipt) error {
	for _, item := range []struct{ name, value string }{
		{"authority_profile_digest", receipt.AuthorityProfileDigest},
		{"head_root_digest", receipt.HeadRootDigest}, {"request_digest", receipt.RequestDigest},
		{"batch_digest", receipt.BatchDigest}, {"security_time_lineage_digest", receipt.SecurityTimeLineageDigest},
		{"anti_rollback_anchor_digest", receipt.AntiRollbackAnchorDigest},
		{"quorum_attestation_digest", receipt.QuorumAttestationDigest}, {"signature_digest", receipt.SignatureDigest},
		{"retention_policy_digest", receipt.RetentionPolicyDigest}, {"receipt_digest", receipt.ReceiptDigest},
	} {
		if err := validateDigest(ErrInvalidReceipt, item.name, item.value); err != nil {
			return err
		}
	}
	return nil
}

// validateEvidenceRef 校验证据引用使用固定类型前缀并以对应内容摘要结尾。
func validateEvidenceRef(name, value, prefix, digest string) error {
	if err := validateCanonicalText(ErrInvalidReceipt, name, value, 512); err != nil {
		return err
	}
	if !strings.HasPrefix(value, prefix) || !strings.HasSuffix(value, "/"+digest) {
		return fmt.Errorf("%w: %s must be a content-addressed %s reference", ErrInvalidReceipt, name, prefix)
	}
	for _, char := range value {
		if char <= 0x20 || char >= 0x7f || strings.ContainsRune("?#\\", char) {
			return fmt.Errorf("%w: %s contains a forbidden reference character", ErrInvalidReceipt, name)
		}
	}
	return nil
}

func validateReceiptDigest(receipt ExternalReceipt) error {
	want, err := ComputeReceiptDigest(receipt)
	if err != nil || want != receipt.ReceiptDigest {
		return fmt.Errorf("%w: receipt digest mismatch", ErrInvalidReceipt)
	}
	return nil
}

// Validate 验证 outcome shape；terminal outcome仍需外部 verifier证据。
func (o Outcome) Validate() error {
	if err := o.Handle.Validate(); err != nil {
		return err
	}
	switch o.Status {
	case StatusPending:
		return validateUnresolvedOutcome(o, false)
	case StatusIndeterminate:
		return validateUnresolvedOutcome(o, true)
	case StatusAvailable, StatusAborted:
		return validateTerminalOutcome(o)
	default:
		return ErrInvalidReceipt
	}
}

func validateUnresolvedOutcome(outcome Outcome, reasonRequired bool) error {
	if outcome.Receipt != nil {
		return ErrInvalidReceipt
	}
	if !reasonRequired && outcome.ReasonCode != "" {
		return ErrInvalidReceipt
	}
	if reasonRequired {
		return validateCanonicalText(ErrInvalidReceipt, "reason_code", outcome.ReasonCode, 128)
	}
	return nil
}

// validateTerminalOutcome 校验终态只携带与查询句柄和状态一致的receipt。
func validateTerminalOutcome(outcome Outcome) error {
	if outcome.ReasonCode != "" || outcome.Receipt == nil {
		return ErrInvalidReceipt
	}
	if err := outcome.Receipt.Validate(); err != nil {
		return err
	}
	if !receiptMatchesHandle(outcome.Handle, *outcome.Receipt) {
		return ErrInvalidReceipt
	}
	if receiptStatus(*outcome.Receipt) != outcome.Status {
		return ErrInvalidReceipt
	}
	return nil
}

// ApplyVerifiedReceipt 验证外部 receipt 并执行 availability/abort 单赢家 reducer。
func ApplyVerifiedReceipt(ctx context.Context, prior Outcome, candidate ExternalReceipt, verifier ReceiptVerifier) (Outcome, error) {
	if ctx == nil {
		return Outcome{}, ErrContextRequired
	}
	if err := ctx.Err(); err != nil {
		return Outcome{}, err
	}
	if err := prior.Validate(); err != nil {
		return Outcome{}, err
	}
	if err := candidate.Validate(); err != nil {
		return Outcome{}, err
	}
	if isNilVerifier(verifier) {
		return Outcome{}, ErrReceiptRejected
	}
	if !receiptMatchesHandle(prior.Handle, candidate) {
		return Outcome{}, ErrCustodyConflict
	}
	if err := verifier.VerifyReceipt(ctx, prior.Handle, candidate); err != nil {
		return Outcome{}, fmt.Errorf("%w: %w", ErrReceiptRejected, err)
	}
	return reduceVerifiedReceipt(prior, candidate)
}

// reduceVerifiedReceipt 按状态与canonical receipt摘要执行终态单赢家归并。
func reduceVerifiedReceipt(prior Outcome, candidate ExternalReceipt) (Outcome, error) {
	status := receiptStatus(candidate)
	if prior.Status == StatusPending || prior.Status == StatusIndeterminate {
		result := Outcome{Handle: prior.Handle, Status: status, Receipt: &candidate}
		return result, result.Validate()
	}
	if prior.Status != status || prior.Receipt == nil || prior.Receipt.ReceiptDigest != candidate.ReceiptDigest {
		return Outcome{}, ErrResolutionConflict
	}
	return prior, nil
}

func receiptMatchesHandle(handle SubmissionHandle, receipt ExternalReceipt) bool {
	return handle.OperationID == receipt.OperationID && handle.SecurityDomainID == receipt.SecurityDomainID &&
		handle.AuthorityProfile == receipt.AuthorityProfile &&
		handle.RequestDigest == receipt.RequestDigest && handle.BatchDigest == receipt.BatchDigest
}

func receiptStatus(receipt ExternalReceipt) string {
	if receipt.Kind == ReceiptAvailability {
		return StatusAvailable
	}
	return StatusAborted
}

func isNilVerifier(verifier ReceiptVerifier) bool {
	if verifier == nil {
		return true
	}
	value := reflect.ValueOf(verifier)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func timeUTC() *time.Location { return time.UTC }
