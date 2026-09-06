package recoverycustody

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

type acceptingVerifier struct{}

func (acceptingVerifier) VerifyReceipt(context.Context, SubmissionHandle, ExternalReceipt) error {
	return nil
}

func TestRecoveryCustodyContract(t *testing.T) {
	checks := []struct {
		name string
		run  func(*testing.T)
	}{
		{"digests", TestDigestContractsAreStableAndBindEveryField},
		{"required_owner_digests", TestRequestRejectsMissingOwnerDigests},
		{"unavailable", TestUnavailableClientPreservesHandleAndIndeterminateOutcome},
		{"same_operation_conflict", TestMatchSubmissionRejectsSameOperationDifferentDigest},
		{"receipt_digest", TestReceiptDigestContract},
		{"receipt_digest_proof_tamper", TestReceiptDigestRejectsProofMetadataTampering},
		{"receipt_digest_legacy_v1", TestReceiptDigestV1IsRejectedWithoutCompatibilityFallback},
		{"receipt_single_winner", TestVerifiedReceiptResolutionHasOneWinner},
		{"receipt_replay_verifier", TestVerifiedReceiptReplayStillRequiresVerifier},
		{"receipt_handle_mismatch", TestVerifiedReceiptRejectsHandleMismatch},
		{"receipt_json", TestExternalReceiptJSONRoundTrip},
		{"receipt_validation", TestExternalReceiptRejectsInvalidEvidenceAndRetention},
		{"reject_local_authority", TestRejectAllVerifierRejectsLocalAuthorityShapes},
		{"json_fields", TestRecoveryCustodyJSONFieldsRemainEnumerable},
		{"request_field_guard", TestSubmissionRequestEveryBusinessFieldAffectsDigest},
		{"receipt_field_guard", TestExternalReceiptEveryClaimAffectsDigest},
		{"receipt_digest_mapping_guard", TestExternalReceiptDigestPayloadHasReasonedCoverage},
		{"handle_mapping_guard", TestSubmissionRequestToHandleMappingHasReasonedCoverage},
		{"public_field_guard", TestRecoveryCustodyReceiptAndOutcomeExcludeAuthorityPayloadFields},
		{"dependency_guard", TestRecoveryCustodyProductionHasNoEffectDependencies},
	}
	for _, check := range checks {
		t.Run(check.name, check.run)
	}
}

func TestDigestContractsAreStableAndBindEveryField(t *testing.T) {
	request := validRequest(t)
	wantRequest, err := ComputeRequestDigest(request)
	assertNoError(t, err)
	again, err := ComputeRequestDigest(request)
	assertNoError(t, err)
	if again != wantRequest || request.RequestDigest != wantRequest {
		t.Fatalf("request digest is not stable: first=%q second=%q finalized=%q", wantRequest, again, request.RequestDigest)
	}
	for _, test := range requestDigestMutations() {
		t.Run("request_"+test.name, func(t *testing.T) {
			mutated := request
			test.mutate(&mutated)
			got, err := ComputeRequestDigest(mutated)
			assertNoError(t, err)
			if got == wantRequest {
				t.Fatalf("changing %s did not change request digest", test.name)
			}
		})
	}

	handle := validHandle(t, request)
	wantHandle, err := ComputeHandleDigest(handle)
	assertNoError(t, err)
	again, err = ComputeHandleDigest(handle)
	assertNoError(t, err)
	if again != wantHandle || handle.HandleDigest != wantHandle {
		t.Fatalf("handle digest is not stable: first=%q second=%q finalized=%q", wantHandle, again, handle.HandleDigest)
	}
	for _, test := range handleDigestMutations() {
		t.Run("handle_"+test.name, func(t *testing.T) {
			mutated := handle
			test.mutate(&mutated)
			got, err := ComputeHandleDigest(mutated)
			assertNoError(t, err)
			if got == wantHandle {
				t.Fatalf("changing %s did not change handle digest", test.name)
			}
		})
	}
}

func TestRequestRejectsMissingOwnerDigests(t *testing.T) {
	for _, test := range requiredOwnerDigestMutations() {
		t.Run(test.name, func(t *testing.T) {
			request := validRequest(t)
			test.mutate(&request)
			if err := request.Validate(); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("Validate() error = %v, want ErrInvalidRequest", err)
			}
		})
	}
}

func TestUnavailableClientPreservesHandleAndIndeterminateOutcome(t *testing.T) {
	request := validRequest(t)
	wantHandle := validHandle(t, request)
	client := NewUnavailableClient()
	submitted, err := client.Submit(context.Background(), request)
	assertErrorIs(t, err, ErrCustodyUnavailable)
	assertIndeterminate(t, submitted, wantHandle)
	queried, err := client.Query(context.Background(), submitted.Handle)
	assertErrorIs(t, err, ErrCustodyUnavailable)
	assertIndeterminate(t, queried, wantHandle)
	if !reflect.DeepEqual(submitted, queried) {
		t.Fatalf("query changed unavailable outcome: submit=%+v query=%+v", submitted, queried)
	}
}

func TestMatchSubmissionRejectsSameOperationDifferentDigest(t *testing.T) {
	original := validRequest(t)
	handle := validHandle(t, original)
	conflict := original
	conflict.ContentSnapshotDigest = testDigest(91)
	conflict, err := FinalizeRequestDigest(conflict)
	assertNoError(t, err)
	if conflict.OperationID != original.OperationID {
		t.Fatal("fixture changed operation ID")
	}
	assertErrorIs(t, MatchSubmission(handle, conflict), ErrCustodyConflict)
}

func TestVerifiedReceiptResolutionHasOneWinner(t *testing.T) {
	for _, test := range []struct {
		name, firstKind, firstStatus, lateKind string
	}{
		{"availability_wins", ReceiptAvailability, StatusAvailable, ReceiptAbort},
		{"abort_wins", ReceiptAbort, StatusAborted, ReceiptAvailability},
	} {
		t.Run(test.name, func(t *testing.T) {
			handle := validHandle(t, validRequest(t))
			prior := Outcome{Handle: handle, Status: StatusPending}
			winner := validReceipt(t, handle, test.firstKind, "winner")
			resolved, err := ApplyVerifiedReceipt(context.Background(), prior, winner, acceptingVerifier{})
			assertNoError(t, err)
			if resolved.Status != test.firstStatus || resolved.Receipt == nil || *resolved.Receipt != winner {
				t.Fatalf("resolution = %+v, want %s with winner receipt", resolved, test.firstStatus)
			}
			replayed, err := ApplyVerifiedReceipt(context.Background(), resolved, winner, acceptingVerifier{})
			assertNoError(t, err)
			if !reflect.DeepEqual(replayed, resolved) {
				t.Fatalf("same receipt replay changed outcome: first=%+v replay=%+v", resolved, replayed)
			}
			late := validReceipt(t, handle, test.lateKind, "late")
			result, err := ApplyVerifiedReceipt(context.Background(), resolved, late, acceptingVerifier{})
			assertErrorIs(t, err, ErrResolutionConflict)
			assertZeroOutcome(t, result)
			sameKindLate := validReceipt(t, handle, test.firstKind, "same-kind-late")
			result, err = ApplyVerifiedReceipt(context.Background(), resolved, sameKindLate, acceptingVerifier{})
			assertErrorIs(t, err, ErrResolutionConflict)
			assertZeroOutcome(t, result)
		})
	}
}

func TestVerifiedReceiptReplayStillRequiresVerifier(t *testing.T) {
	handle := validHandle(t, validRequest(t))
	winner := validReceipt(t, handle, ReceiptAvailability, "verified-replay")
	resolved, err := ApplyVerifiedReceipt(
		context.Background(), Outcome{Handle: handle, Status: StatusPending}, winner, acceptingVerifier{},
	)
	assertNoError(t, err)
	replay := withAlternateReceiptEvidence(winner)
	replay = finalizeReceipt(t, replay)
	if replay.ReceiptDigest == winner.ReceiptDigest {
		t.Fatal("proof metadata mutation did not change canonical receipt digest")
	}
	result, err := ApplyVerifiedReceipt(context.Background(), resolved, replay, RejectAllReceiptVerifier{})
	assertErrorIs(t, err, ErrReceiptRejected)
	assertErrorIs(t, err, ErrCustodyUnavailable)
	assertZeroOutcome(t, result)
	replayed, err := ApplyVerifiedReceipt(context.Background(), resolved, replay, acceptingVerifier{})
	assertErrorIs(t, err, ErrResolutionConflict)
	assertZeroOutcome(t, replayed)
	if reflect.DeepEqual(replayed, resolved) {
		t.Fatal("proof metadata replay unexpectedly preserved the prior winner")
	}
}

func TestVerifiedReceiptRejectsHandleMismatch(t *testing.T) {
	handle := validHandle(t, validRequest(t))
	for _, test := range receiptHandleMutations() {
		t.Run(test.name, func(t *testing.T) {
			receipt := validReceipt(t, handle, ReceiptAvailability, test.name)
			test.mutate(&receipt)
			receipt = finalizeReceipt(t, receipt)
			result, err := ApplyVerifiedReceipt(context.Background(), Outcome{Handle: handle, Status: StatusPending}, receipt, acceptingVerifier{})
			assertErrorIs(t, err, ErrCustodyConflict)
			assertZeroOutcome(t, result)
		})
	}
}

func TestRejectAllVerifierRejectsLocalAuthorityShapes(t *testing.T) {
	handle := validHandle(t, validRequest(t))
	for _, shape := range []string{"sqlite", "file", "wal", "heteroci"} {
		t.Run(shape, func(t *testing.T) {
			receipt := validReceipt(t, handle, ReceiptAvailability, shape)
			receipt.AuthorityID = shape + "-local-authority"
			receipt = finalizeReceipt(t, receipt)
			result, err := ApplyVerifiedReceipt(context.Background(), Outcome{Handle: handle, Status: StatusPending}, receipt, RejectAllReceiptVerifier{})
			assertErrorIs(t, err, ErrReceiptRejected)
			assertErrorIs(t, err, ErrCustodyUnavailable)
			assertZeroOutcome(t, result)
		})
	}
}

func validRequest(t *testing.T) SubmissionRequest {
	t.Helper()
	request := SubmissionRequest{
		SchemaVersion: 1, OperationID: "operation-1", SecurityDomainID: "security-domain-1",
		AuthorityProfile: "external-quorum-v1", CommandID: "command-1", CommandVersion: 1,
		CommandProfileDigest: testDigest(1), AuthorizationDigest: testDigest(2), IdentityFenceDigest: testDigest(3),
		ContentSnapshotDigest: testDigest(4), ResourceSnapshotDigest: testDigest(5), BatchDigest: testDigest(6),
		IdempotencyKeyDigest: testDigest(7),
	}
	request, err := FinalizeRequestDigest(request)
	assertNoError(t, err)
	assertNoError(t, request.Validate())
	return request
}

func validHandle(t *testing.T, request SubmissionRequest) SubmissionHandle {
	t.Helper()
	handle, err := NewSubmissionHandle(request)
	assertNoError(t, err)
	assertNoError(t, handle.Validate())
	return handle
}

func validReceipt(t *testing.T, handle SubmissionHandle, kind, seed string) ExternalReceipt {
	t.Helper()
	quorumDigest := testDigest(len(seed) + 100)
	signatureDigest := testDigest(len(seed) + 200)
	receipt := ExternalReceipt{
		SchemaVersion: 1, Kind: kind, ReceiptID: "receipt-" + seed,
		AuthorityID: "external-authority-1", AuthorityClusterID: "external-cluster-1",
		AuthorityProfile: handle.AuthorityProfile, AuthorityGeneration: 1,
		OwnerID: "external-owner-1", OwnerGeneration: 1,
		AuthorityProfileGeneration: 1, AuthorityProfileDigest: testDigest(len(seed) + 10),
		SecurityDomainID: handle.SecurityDomainID, HeadID: "head-" + seed, HeadRevision: 1,
		HeadRootDigest: testDigest(len(seed) + 20), OperationID: handle.OperationID,
		RequestDigest: handle.RequestDigest, BatchDigest: handle.BatchDigest,
		SecurityTimeLineageDigest: testDigest(len(seed) + 30), SecurityTimeGeneration: 1, SecurityTimeRevision: 1,
		AntiRollbackGeneration: 1, AntiRollbackAnchorDigest: testDigest(len(seed) + 40),
		QuorumAttestationDigest:     quorumDigest,
		QuorumAttestationRef:        "quorum://external/" + seed + "/" + quorumDigest,
		SignatureDigest:             signatureDigest,
		SignatureRef:                "signature://external/" + seed + "/" + signatureDigest,
		AuthorityTime:               time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC),
		RetentionPolicyDigest:       testDigest(len(seed) + 50),
		RetentionUntil:              time.Date(2027, 9, 2, 0, 0, 0, 0, time.UTC),
		GCNotBeforeAnchorGeneration: 2,
	}
	return finalizeReceipt(t, receipt)
}

func finalizeReceipt(t *testing.T, receipt ExternalReceipt) ExternalReceipt {
	t.Helper()
	finalized, err := FinalizeReceiptDigest(receipt)
	assertNoError(t, err)
	assertNoError(t, finalized.Validate())
	return finalized
}

func withAlternateReceiptEvidence(receipt ExternalReceipt) ExternalReceipt {
	receipt.QuorumAttestationDigest = testDigest(901)
	receipt.QuorumAttestationRef = "quorum://external/alternate/" + receipt.QuorumAttestationDigest
	receipt.SignatureDigest = testDigest(902)
	receipt.SignatureRef = "signature://external/alternate/" + receipt.SignatureDigest
	return receipt
}

func requestDigestMutations() []struct {
	name   string
	mutate func(*SubmissionRequest)
} {
	return []struct {
		name   string
		mutate func(*SubmissionRequest)
	}{
		{"schema_version", func(v *SubmissionRequest) { v.SchemaVersion++ }},
		{"operation_id", func(v *SubmissionRequest) { v.OperationID += "-changed" }},
		{"security_domain_id", func(v *SubmissionRequest) { v.SecurityDomainID += "-changed" }},
		{"authority_profile", func(v *SubmissionRequest) { v.AuthorityProfile += "-changed" }},
		{"command_id", func(v *SubmissionRequest) { v.CommandID += "-changed" }},
		{"command_version", func(v *SubmissionRequest) { v.CommandVersion++ }},
		{"command_profile_digest", func(v *SubmissionRequest) { v.CommandProfileDigest = testDigest(11) }},
		{"authorization_digest", func(v *SubmissionRequest) { v.AuthorizationDigest = testDigest(12) }},
		{"identity_fence_digest", func(v *SubmissionRequest) { v.IdentityFenceDigest = testDigest(13) }},
		{"content_snapshot_digest", func(v *SubmissionRequest) { v.ContentSnapshotDigest = testDigest(14) }},
		{"resource_snapshot_digest", func(v *SubmissionRequest) { v.ResourceSnapshotDigest = testDigest(15) }},
		{"batch_digest", func(v *SubmissionRequest) { v.BatchDigest = testDigest(16) }},
		{"idempotency_key_digest", func(v *SubmissionRequest) { v.IdempotencyKeyDigest = testDigest(17) }},
	}
}

func handleDigestMutations() []struct {
	name   string
	mutate func(*SubmissionHandle)
} {
	return []struct {
		name   string
		mutate func(*SubmissionHandle)
	}{
		{"schema_version", func(v *SubmissionHandle) { v.SchemaVersion++ }},
		{"operation_id", func(v *SubmissionHandle) { v.OperationID += "-changed" }},
		{"security_domain_id", func(v *SubmissionHandle) { v.SecurityDomainID += "-changed" }},
		{"authority_profile", func(v *SubmissionHandle) { v.AuthorityProfile += "-changed" }},
		{"command_id", func(v *SubmissionHandle) { v.CommandID += "-changed" }},
		{"command_version", func(v *SubmissionHandle) { v.CommandVersion++ }},
		{"request_digest", func(v *SubmissionHandle) { v.RequestDigest = testDigest(21) }},
		{"batch_digest", func(v *SubmissionHandle) { v.BatchDigest = testDigest(22) }},
	}
}

func requiredOwnerDigestMutations() []struct {
	name   string
	mutate func(*SubmissionRequest)
} {
	return []struct {
		name   string
		mutate func(*SubmissionRequest)
	}{
		{"content", func(v *SubmissionRequest) { v.ContentSnapshotDigest = "" }},
		{"resource", func(v *SubmissionRequest) { v.ResourceSnapshotDigest = "" }},
		{"identity", func(v *SubmissionRequest) { v.IdentityFenceDigest = "" }},
		{"authorization", func(v *SubmissionRequest) { v.AuthorizationDigest = "" }},
		{"command_profile", func(v *SubmissionRequest) { v.CommandProfileDigest = "" }},
	}
}

func receiptHandleMutations() []struct {
	name   string
	mutate func(*ExternalReceipt)
} {
	return []struct {
		name   string
		mutate func(*ExternalReceipt)
	}{
		{"operation_id", func(v *ExternalReceipt) { v.OperationID += "-mismatch" }},
		{"security_domain_id", func(v *ExternalReceipt) { v.SecurityDomainID += "-mismatch" }},
		{"authority_profile", func(v *ExternalReceipt) { v.AuthorityProfile += "-mismatch" }},
		{"request_digest", func(v *ExternalReceipt) { v.RequestDigest = testDigest(31) }},
		{"batch_digest", func(v *ExternalReceipt) { v.BatchDigest = testDigest(32) }},
	}
}

func testDigest(value int) string {
	return fmt.Sprintf("%064x", value)
}

func assertIndeterminate(t *testing.T, outcome Outcome, wantHandle SubmissionHandle) {
	t.Helper()
	assertNoError(t, outcome.Validate())
	if outcome.Status != StatusIndeterminate || outcome.ReasonCode != "recovery_custody_unavailable" || outcome.Receipt != nil {
		t.Fatalf("outcome = %+v, want canonical indeterminate without receipt", outcome)
	}
	if outcome.Handle != wantHandle {
		t.Fatalf("handle = %+v, want %+v", outcome.Handle, wantHandle)
	}
	encoded, err := json.Marshal(outcome)
	assertNoError(t, err)
	for _, forbidden := range []string{"accepted", "dispatch", "provider", "journal"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("outcome exposes forbidden local progress %q: %s", forbidden, encoded)
		}
	}
}

func assertNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertErrorIs(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error = %v, want errors.Is(..., %v)", err, target)
	}
}

func assertZeroOutcome(t *testing.T, outcome Outcome) {
	t.Helper()
	if !reflect.DeepEqual(outcome, Outcome{}) {
		t.Fatalf("outcome = %+v, want zero non-authorizing result", outcome)
	}
}
