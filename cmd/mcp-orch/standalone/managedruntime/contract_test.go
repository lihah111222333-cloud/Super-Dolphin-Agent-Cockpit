package managedruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
)

func TestManagedRuntimeContractValidation(t *testing.T) {
	prepare := validPrepareRequest(t, "contract")
	prepared := validPreparedReceipt(t, prepare, "contract")
	commit := validCommitRequest(t, prepared, "contract")
	abort := validAbortRequest(t, prepared, "contract")
	committed := validClosedReceipt(t, prepared, commit.OperationID, commit.RequestDigest, StateCommitted, "contract")
	aborted := validClosedReceipt(t, prepared, abort.OperationID, abort.RequestDigest, StateAborted, "contract")

	for name, validate := range map[string]func() error{
		"prepare": prepare.Validate, "prepared": prepared.Validate, "commit": commit.Validate,
		"abort": abort.Validate, "committed": committed.Validate, "aborted": aborted.Validate,
	} {
		t.Run(name, func(t *testing.T) {
			if err := validate(); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
	if err := MatchPrepare(prepare, prepared); err != nil {
		t.Fatalf("MatchPrepare() error = %v", err)
	}
}

func TestManagedRuntimeOperationIDAndDigestConflict(t *testing.T) {
	request := validPrepareRequest(t, "conflict")
	receipt := validPreparedReceipt(t, request, "conflict")

	changed := request
	changed.ArgumentVectorDigest = testDigest("different-arguments")
	changed = mustFinalizePrepare(t, changed)
	if err := MatchPrepare(changed, receipt); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("same operation with different digest error = %v, want ErrOperationConflict", err)
	}

	changed = request
	changed.OperationID = "prepare-operation-other"
	changed = mustFinalizePrepare(t, changed)
	if err := MatchPrepare(changed, receipt); !errors.Is(err, ErrOperationNotFound) {
		t.Fatalf("different operation error = %v, want ErrOperationNotFound", err)
	}
}

func TestManagedRuntimeQueryClosedStateMatrix(t *testing.T) {
	operationID := "prepare-operation-query"
	operationDigest := testDigest("query-operation")
	ref := "receipt://managed-runtime/query"
	digest := testDigest("query-receipt")
	tests := []struct {
		name  string
		state State
		set   func(*QueryResult)
	}{
		{name: "absent", state: StateAbsent},
		{name: "prepared", state: StatePrepared, set: func(result *QueryResult) {
			result.PreparedReceiptRef, result.PreparedReceiptDigest = ref+"/prepared", digest
		}},
		{name: "committed", state: StateCommitted, set: func(result *QueryResult) {
			result.PreparedReceiptRef, result.PreparedReceiptDigest = ref+"/prepared", digest
			result.ClosedReceiptRef, result.ClosedReceiptDigest = ref+"/committed", testDigest("committed")
		}},
		{name: "aborted", state: StateAborted, set: func(result *QueryResult) {
			result.PreparedReceiptRef, result.PreparedReceiptDigest = ref+"/prepared", digest
			result.ClosedReceiptRef, result.ClosedReceiptDigest = ref+"/aborted", testDigest("aborted")
		}},
		{name: "outcome_unknown", state: StateOutcomeUnknown, set: func(result *QueryResult) {
			result.UncertaintyReceiptRef, result.UncertaintyDigest = ref+"/unknown", testDigest("unknown")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := QueryResult{SchemaVersion: SchemaVersion, OperationID: operationID, OperationDigest: operationDigest, State: test.state}
			if test.set != nil {
				test.set(&result)
			}
			result = mustFinalizeQueryResult(t, result)
			if err := result.Validate(); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func TestManagedRuntimeOutcomeUnknownIsQueryOnly(t *testing.T) {
	prepare := validPrepareRequest(t, "unknown")
	prepared := validPreparedReceipt(t, prepare, "unknown")
	prepared.State = StateOutcomeUnknown
	prepared = mustFinalizePrepared(t, prepared)
	if err := prepared.Validate(); !errors.Is(err, ErrInvalidPreparedReceipt) {
		t.Fatalf("prepared outcome_unknown error = %v, want ErrInvalidPreparedReceipt", err)
	}

	closed := validClosedReceipt(t, validPreparedReceipt(t, prepare, "unknown-closed"), "close-unknown", testDigest("close-unknown"), StateCommitted, "unknown")
	closed.State = StateOutcomeUnknown
	closed = mustFinalizeClosed(t, closed)
	if err := closed.Validate(); !errors.Is(err, ErrInvalidClosedReceipt) {
		t.Fatalf("closed outcome_unknown error = %v, want ErrInvalidClosedReceipt", err)
	}
}

func TestManagedRuntimeProductionPrerequisitesNoGo(t *testing.T) {
	valid := validProductionPrerequisites()
	if err := valid.Validate(); err != nil {
		t.Fatalf("complete prerequisites Validate() error = %v", err)
	}
	tests := []struct {
		name  string
		clear func(*ProductionPrerequisites)
	}{
		{name: "real_helper", clear: func(value *ProductionPrerequisites) { value.RealHelper = DependencyEvidence{} }},
		{name: "reconnectable_endpoint", clear: func(value *ProductionPrerequisites) { value.ReconnectableEndpoint = DependencyEvidence{} }},
		{name: "durable_store", clear: func(value *ProductionPrerequisites) { value.DurableStore = DependencyEvidence{} }},
		{name: "signature_verifier", clear: func(value *ProductionPrerequisites) { value.SignatureVerifier = DependencyEvidence{} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.clear(&candidate)
			if err := candidate.Validate(); !errors.Is(err, ErrProductionNoGo) {
				t.Fatalf("Validate() error = %v, want ErrProductionNoGo", err)
			}
		})
	}
}

func validPrepareRequest(t *testing.T, suffix string) PrepareRequest {
	t.Helper()
	return mustFinalizePrepare(t, PrepareRequest{
		SchemaVersion: SchemaVersion, OperationID: "prepare-operation-" + suffix,
		BootstrapOperationID: "managed-launch-bootstrap-" + suffix, BootstrapOperationDigest: testDigest("bootstrap-" + suffix),
		ProviderDigest: testDigest("provider-" + suffix), ExecutableDigest: testDigest("executable-" + suffix),
		WorkingDirectoryDigest: testDigest("cwd-" + suffix), ArgumentVectorDigest: testDigest("arguments-" + suffix),
		EnvironmentDigest: testDigest("environment-" + suffix), ExpectedRuntimeWriterEpoch: "writer-epoch-" + suffix,
	})
}

func validPreparedReceipt(t *testing.T, request PrepareRequest, suffix string) PreparedReceipt {
	t.Helper()
	return mustFinalizePrepared(t, PreparedReceipt{
		SchemaVersion: SchemaVersion, OperationID: request.OperationID, RequestDigest: request.RequestDigest, State: StatePrepared,
		OpaqueHandleRef: "opaque://managed-runtime/" + suffix, OpaqueHandleDigest: testDigest("opaque-" + suffix),
		Process: StableProcessIdentity{PID: 4242, ProcessStartToken: "start-token-" + suffix,
			ExecutableIdentity: "/verified/bin/managed-runtime-" + suffix, ExecutableDigest: request.ExecutableDigest},
		ProviderThreadID: "provider-thread-" + suffix, ProviderSessionID: "provider-session-" + suffix,
		ExternalEpoch: "external-epoch-" + suffix, RuntimeOwnerID: "runtime-owner-" + suffix,
		RuntimeGeneration: 7, ProcessGeneration: 11, RuntimeWriterEpoch: request.ExpectedRuntimeWriterEpoch,
		ReceiptRef: "receipt://managed-runtime/prepared/" + suffix,
	})
}

func validCommitRequest(t *testing.T, prepared PreparedReceipt, suffix string) CommitRequest {
	t.Helper()
	request := CommitRequest{
		SchemaVersion: SchemaVersion, OperationID: "commit-operation-" + suffix,
		PreparedOperationID: prepared.OperationID, PreparedRequestDigest: prepared.RequestDigest,
		ExpectedPreparedReceiptRef: prepared.ReceiptRef, ExpectedPreparedReceiptDigest: prepared.ReceiptDigest,
		ActiveBindingDigest: testDigest("active-binding-" + suffix), CredentialDigest: testDigest("credential-" + suffix),
	}
	finalized, err := FinalizeCommitRequestDigest(request)
	if err != nil {
		t.Fatalf("FinalizeCommitRequestDigest() error = %v", err)
	}
	return finalized
}

func validAbortRequest(t *testing.T, prepared PreparedReceipt, suffix string) AbortRequest {
	t.Helper()
	request := AbortRequest{
		SchemaVersion: SchemaVersion, OperationID: "abort-operation-" + suffix,
		PreparedOperationID: prepared.OperationID, PreparedRequestDigest: prepared.RequestDigest,
		ExpectedPreparedReceiptRef: prepared.ReceiptRef, ExpectedPreparedReceiptDigest: prepared.ReceiptDigest,
		CleanupEvidenceRef:    "evidence://managed-runtime/cleanup/" + suffix,
		CleanupEvidenceDigest: testDigest("cleanup-" + suffix),
	}
	finalized, err := FinalizeAbortRequestDigest(request)
	if err != nil {
		t.Fatalf("FinalizeAbortRequestDigest() error = %v", err)
	}
	return finalized
}

func validClosedReceipt(
	t *testing.T,
	prepared PreparedReceipt,
	operationID string,
	operationDigest string,
	state State,
	suffix string,
) ClosedReceipt {
	t.Helper()
	return mustFinalizeClosed(t, ClosedReceipt{
		SchemaVersion: SchemaVersion, PreparedOperationID: prepared.OperationID, PreparedRequestDigest: prepared.RequestDigest,
		OperationID: operationID, OperationDigest: operationDigest, State: state,
		PreparedReceiptRef: prepared.ReceiptRef, PreparedReceiptDigest: prepared.ReceiptDigest,
		ClosureEvidenceRef:    "evidence://managed-runtime/closure/" + suffix,
		ClosureEvidenceDigest: testDigest("closure-" + suffix), ReceiptRef: "receipt://managed-runtime/closed/" + suffix,
	})
}

func validProductionPrerequisites() ProductionPrerequisites {
	return ProductionPrerequisites{
		SchemaVersion:         SchemaVersion,
		RealHelper:            DependencyEvidence{Ref: "evidence://managed-runtime/helper", Digest: testDigest("helper")},
		ReconnectableEndpoint: DependencyEvidence{Ref: "evidence://managed-runtime/endpoint", Digest: testDigest("endpoint")},
		DurableStore:          DependencyEvidence{Ref: "evidence://managed-runtime/store", Digest: testDigest("store")},
		SignatureVerifier:     DependencyEvidence{Ref: "evidence://managed-runtime/verifier", Digest: testDigest("verifier")},
	}
}

func mustFinalizePrepare(t *testing.T, request PrepareRequest) PrepareRequest {
	t.Helper()
	finalized, err := FinalizePrepareRequestDigest(request)
	if err != nil {
		t.Fatalf("FinalizePrepareRequestDigest() error = %v", err)
	}
	return finalized
}

func mustFinalizePrepared(t *testing.T, receipt PreparedReceipt) PreparedReceipt {
	t.Helper()
	finalized, err := FinalizePreparedReceiptDigest(receipt)
	if err != nil {
		t.Fatalf("FinalizePreparedReceiptDigest() error = %v", err)
	}
	return finalized
}

func mustFinalizeClosed(t *testing.T, receipt ClosedReceipt) ClosedReceipt {
	t.Helper()
	finalized, err := FinalizeClosedReceiptDigest(receipt)
	if err != nil {
		t.Fatalf("FinalizeClosedReceiptDigest() error = %v", err)
	}
	return finalized
}

func mustFinalizeQueryResult(t *testing.T, result QueryResult) QueryResult {
	t.Helper()
	finalized, err := FinalizeQueryResultDigest(result)
	if err != nil {
		t.Fatalf("FinalizeQueryResultDigest() error = %v", err)
	}
	return finalized
}

func testDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
