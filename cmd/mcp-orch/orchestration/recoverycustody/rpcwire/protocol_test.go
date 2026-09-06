package rpcwire

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody"
)

type acceptingVerifier struct{ calls int }

func (v *acceptingVerifier) VerifyReceipt(context.Context, recoverycustody.SubmissionHandle, recoverycustody.ExternalReceipt) error {
	v.calls++
	return nil
}

func TestRPCWireContract(t *testing.T) {
	checks := []struct {
		name string
		run  func(*testing.T)
	}{
		{"methods_and_failures", TestProtocolMethodsAndFailureCodesAreClosed},
		{"error_mapping", TestFailureFromErrorMapsKnownDomainsAndRejectsUnknown},
		{"result_matrix", TestResultMethodAndOutcomeFailureMatrix},
		{"client_verifier", TestTerminalResultRequiresClientVerifier},
		{"canonical_round_trip", TestCanonicalJSONRoundTrip},
		{"canonical_rejections", TestCanonicalDecoderRejectsAmbiguousOrNonCanonicalJSON},
		{"nested_json_rejections", TestCanonicalDecoderRejectsNestedDuplicateAndExplicitOptionalNull},
		{"enumerable_fields", TestWireJSONFieldsRemainDynamicallyEnumerable},
		{"domain_mapping", TestSubmitAndQueryEmbedEveryDomainFieldWithoutDigestDrift},
		{"authority_mapping", TestAuthorityReceiptIdentityMappingIsOneHot},
		{"forbidden_fields", TestWirePublicShapeForbidsPayloadAndCredentialFields},
		{"dependency_boundary", TestRPCWireProductionHasNoAuthorityOrTransportDependencies},
	}
	for _, check := range checks {
		t.Run(check.name, check.run)
	}
}

func TestProtocolMethodsAndFailureCodesAreClosed(t *testing.T) {
	for method, value := range map[Method]string{
		MethodIdentity: "recovery-custody/identity",
		MethodSubmit:   "recovery-custody/submit",
		MethodQuery:    "recovery-custody/query",
	} {
		if string(method) != value {
			t.Fatalf("method value = %q, want %q", method, value)
		}
		if err := method.Validate(); err != nil {
			t.Fatalf("%q Validate() error = %v", method, err)
		}
	}
	if err := Method("resolve").Validate(); !errors.Is(err, ErrInvalidWire) {
		t.Fatalf("unknown method error = %v, want ErrInvalidWire", err)
	}
	for _, code := range []FailureCode{
		FailureInvalidRequest, FailureInvalidHandle, FailureOperationConflict, FailureReceiptRejected,
		FailureResolutionConflict, FailureOperationNotFound, FailureAuthorityUnavailable,
		FailureAuthorityReadOnly, FailureLeaseRejected, FailureCommitOutcomeUnknown,
	} {
		if err := (Failure{Code: code}).Validate(); err != nil {
			t.Fatalf("Failure{%q}.Validate() error = %v", code, err)
		}
	}
	if err := (Failure{Code: "raw_error"}).Validate(); !errors.Is(err, ErrInvalidWire) {
		t.Fatalf("unknown failure error = %v, want ErrInvalidWire", err)
	}
}

func TestFailureFromErrorMapsKnownDomainsAndRejectsUnknown(t *testing.T) {
	tests := []struct {
		err  error
		want FailureCode
	}{
		{recoverycustody.ErrInvalidRequest, FailureInvalidRequest},
		{recoverycustody.ErrInvalidHandle, FailureInvalidHandle},
		{recoverycustody.ErrCustodyConflict, FailureOperationConflict},
		{errors.Join(recoverycustody.ErrReceiptRejected, recoverycustody.ErrCustodyUnavailable), FailureReceiptRejected},
		{recoverycustody.ErrResolutionConflict, FailureResolutionConflict},
		{recoverycustody.ErrOperationNotFound, FailureOperationNotFound},
		{recoverycustody.ErrCustodyUnavailable, FailureAuthorityUnavailable},
	}
	for _, test := range tests {
		t.Run(string(test.want), func(t *testing.T) {
			got, ok := FailureFromError(fmt.Errorf("adapter boundary: %w", test.err))
			if !ok || got.Code != test.want {
				t.Fatalf("FailureFromError() = (%+v, %v), want code %q", got, ok, test.want)
			}
		})
	}
	if got, ok := FailureFromError(errors.New("/secret/path: token=unsafe")); ok || got != (Failure{}) {
		t.Fatalf("unknown error mapped to wire failure: (%+v, %v)", got, ok)
	}
	for _, authoritySpecific := range []FailureCode{
		FailureAuthorityReadOnly, FailureLeaseRejected, FailureCommitOutcomeUnknown,
	} {
		if got, ok := FailureFromError(fmt.Errorf("authority-specific %s", authoritySpecific)); ok || got != (Failure{}) {
			t.Fatalf("authority-specific error %q mapped in transport-neutral wire package", authoritySpecific)
		}
	}
}

func TestResultMethodAndOutcomeFailureMatrix(t *testing.T) {
	authority := validAuthorityIdentity()
	handle := validHandle(t)
	indeterminate := recoverycustody.Outcome{
		Handle: handle, Status: recoverycustody.StatusIndeterminate, ReasonCode: "authority_query_only",
	}
	terminal := validTerminalOutcome(t, authority, handle)
	tests := []struct {
		name    string
		method  Method
		outcome *recoverycustody.Outcome
		failure *Failure
		wantErr bool
	}{
		{"identity", MethodIdentity, nil, nil, false},
		{"identity_rejects_outcome", MethodIdentity, &indeterminate, nil, true},
		{"submit_success", MethodSubmit, &indeterminate, nil, false},
		{"submit_requires_payload", MethodSubmit, nil, nil, true},
		{"failure_only", MethodQuery, nil, &Failure{Code: FailureOperationNotFound}, false},
		{"unavailable_indeterminate", MethodSubmit, &indeterminate, &Failure{Code: FailureAuthorityUnavailable}, false},
		{"read_only_terminal", MethodSubmit, &terminal, &Failure{Code: FailureAuthorityReadOnly}, false},
		{"commit_unknown_terminal", MethodSubmit, &terminal, &Failure{Code: FailureCommitOutcomeUnknown}, false},
		{"invalid_request_cannot_have_outcome", MethodSubmit, &indeterminate, &Failure{Code: FailureInvalidRequest}, true},
		{"unavailable_cannot_claim_terminal", MethodQuery, &terminal, &Failure{Code: FailureAuthorityUnavailable}, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := Result{SchemaVersion: SchemaVersion, Authority: authority, Outcome: test.outcome, Failure: test.failure}
			err := result.ValidateForMethod(test.method)
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidateForMethod() error = %v, wantErr=%v", err, test.wantErr)
			}
		})
	}
}

func TestTerminalResultRequiresClientVerifier(t *testing.T) {
	authority := validAuthorityIdentity()
	handle := validHandle(t)
	terminal := validTerminalOutcome(t, authority, handle)
	result := Result{SchemaVersion: SchemaVersion, Authority: authority, Outcome: &terminal}
	var nilContext context.Context
	if err := result.ValidateForClient(nilContext, MethodQuery, new(acceptingVerifier)); !errors.Is(err, recoverycustody.ErrContextRequired) {
		t.Fatalf("nil context error = %v, want ErrContextRequired", err)
	}
	if err := result.ValidateForMethod(MethodQuery); err != nil {
		t.Fatalf("structural validation error = %v", err)
	}
	if err := result.ValidateForClient(t.Context(), MethodQuery, nil); !errors.Is(err, ErrInvalidWire) {
		t.Fatalf("nil verifier error = %v, want ErrInvalidWire", err)
	}
	verifier := new(acceptingVerifier)
	if err := result.ValidateForClient(t.Context(), MethodQuery, verifier); err != nil {
		t.Fatalf("ValidateForClient() error = %v", err)
	}
	if verifier.calls != 1 {
		t.Fatalf("verifier calls = %d, want 1", verifier.calls)
	}
	mutated := result
	mutated.Authority.AuthorityGeneration++
	if err := mutated.ValidateForClient(t.Context(), MethodQuery, verifier); !errors.Is(err, ErrInvalidWire) {
		t.Fatalf("identity mismatch error = %v, want ErrInvalidWire", err)
	}
}

func validAuthorityIdentity() AuthorityIdentity {
	return AuthorityIdentity{
		InstanceID: "authority-instance-1", AuthorityID: "authority-1", AuthorityClusterID: "cluster-1",
		AuthorityProfile: "external-quorum-v1", AuthorityGeneration: 3, AuthorityProfileGeneration: 5,
		AuthorityProfileDigest: testDigest(40), SecurityDomainID: "security-domain-1",
	}
}

func validRequest(t *testing.T) recoverycustody.SubmissionRequest {
	t.Helper()
	request := recoverycustody.SubmissionRequest{
		SchemaVersion: 1, OperationID: "operation-1", SecurityDomainID: "security-domain-1",
		AuthorityProfile: "external-quorum-v1", CommandID: "command-1", CommandVersion: 1,
		CommandProfileDigest: testDigest(1), AuthorizationDigest: testDigest(2), IdentityFenceDigest: testDigest(3),
		ContentSnapshotDigest: testDigest(4), ResourceSnapshotDigest: testDigest(5), BatchDigest: testDigest(6),
		IdempotencyKeyDigest: testDigest(7),
	}
	finalized, err := recoverycustody.FinalizeRequestDigest(request)
	if err != nil {
		t.Fatalf("FinalizeRequestDigest() error = %v", err)
	}
	return finalized
}

func validHandle(t *testing.T) recoverycustody.SubmissionHandle {
	t.Helper()
	handle, err := recoverycustody.NewSubmissionHandle(validRequest(t))
	if err != nil {
		t.Fatalf("NewSubmissionHandle() error = %v", err)
	}
	return handle
}

func validTerminalOutcome(t *testing.T, authority AuthorityIdentity, handle recoverycustody.SubmissionHandle) recoverycustody.Outcome {
	t.Helper()
	receipt := recoverycustody.ExternalReceipt{
		SchemaVersion: 1, Kind: recoverycustody.ReceiptAvailability, ReceiptID: "receipt-1",
		AuthorityID: authority.AuthorityID, AuthorityClusterID: authority.AuthorityClusterID,
		AuthorityProfile: authority.AuthorityProfile, AuthorityGeneration: authority.AuthorityGeneration,
		OwnerID: "owner-1", OwnerGeneration: 2, AuthorityProfileGeneration: authority.AuthorityProfileGeneration,
		AuthorityProfileDigest: authority.AuthorityProfileDigest, SecurityDomainID: authority.SecurityDomainID,
		HeadID: "head-1", HeadRevision: 4, HeadRootDigest: testDigest(41), OperationID: handle.OperationID,
		RequestDigest: handle.RequestDigest, BatchDigest: handle.BatchDigest,
		SecurityTimeLineageDigest: testDigest(42), SecurityTimeGeneration: 2, SecurityTimeRevision: 3,
		AntiRollbackGeneration: 4, AntiRollbackAnchorDigest: testDigest(43),
		QuorumAttestationDigest: testDigest(44), QuorumAttestationRef: "quorum://external/" + testDigest(44),
		SignatureDigest: testDigest(45), SignatureRef: "signature://external/" + testDigest(45),
		AuthorityTime: time.Date(2026, 9, 2, 1, 2, 3, 0, time.UTC), RetentionPolicyDigest: testDigest(46),
		RetentionUntil: time.Date(2027, 9, 2, 1, 2, 3, 0, time.UTC), GCNotBeforeAnchorGeneration: 5,
	}
	finalized, err := recoverycustody.FinalizeReceiptDigest(receipt)
	if err != nil {
		t.Fatalf("FinalizeReceiptDigest() error = %v", err)
	}
	outcome := recoverycustody.Outcome{Handle: handle, Status: recoverycustody.StatusAvailable, Receipt: &finalized}
	if err := outcome.Validate(); err != nil {
		t.Fatalf("terminal fixture error = %v", err)
	}
	return outcome
}

func testDigest(seed int) string {
	return fmt.Sprintf("%064x", seed)
}

func assertErrorContainsNoSensitiveValue(t *testing.T, err error) {
	t.Helper()
	for _, forbidden := range []string{"/secret/path", "token=unsafe", "password", "credential"} {
		if strings.Contains(strings.ToLower(err.Error()), forbidden) {
			t.Fatalf("error %q contains forbidden value %q", err, forbidden)
		}
	}
}
