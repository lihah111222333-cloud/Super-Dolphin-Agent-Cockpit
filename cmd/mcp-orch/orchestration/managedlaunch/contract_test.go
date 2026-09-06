package managedlaunch

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type fakeBackend struct{}

func (*fakeBackend) Bootstrap(context.Context, BootstrapRequest) (KnownOutcome, error) {
	return KnownOutcome{}, nil
}

func (*fakeBackend) CommitBootstrap(context.Context, CommitRequest) (KnownOutcome, error) {
	return KnownOutcome{}, nil
}

func (*fakeBackend) AbortBootstrap(context.Context, AbortRequest) (KnownOutcome, error) {
	return KnownOutcome{}, nil
}

func (*fakeBackend) QueryOperation(context.Context, OperationKey) (QueryOutcome, error) {
	return QueryOutcome{}, nil
}

func TestManagedLaunchContract(t *testing.T) {
	t.Run("bootstrap_key", testBootstrapKey)
	t.Run("prepared_only", testBootstrapResultPreparedOnly)
	t.Run("commit_binding", testCommitBinding)
	t.Run("abort_cleanup", testAbortCleanupBinding)
	t.Run("outcome_one_hot", testOutcomePresenceOneHot)
	t.Run("unknown_query_only", testUnknownIsQueryOnly)
	t.Run("typed_nil_backend", testTypedNilBackendRejected)
	t.Run("json_round_trip", testJSONRoundTrip)
}

func testBootstrapKey(t *testing.T) {
	request := bootstrapFixture(t)
	key, err := request.OperationKey()
	assertNoError(t, err)
	assertNoError(t, key.Validate())
	assertNoError(t, MatchOperationKey(key, key))
	assertOperationKeyDerived(t, request, key)

	conflict := request
	conflict.AgentDigest = digestFixture("f")
	conflict, err = FinalizeBootstrapRequestDigest(conflict)
	assertNoError(t, err)
	conflictKey, err := conflict.OperationKey()
	assertNoError(t, err)
	if conflictKey.AttemptOperationID != key.AttemptOperationID || conflictKey.IdempotencyKeyDigest != key.IdempotencyKeyDigest {
		t.Fatal("fixture changed stable operation/idempotency identity")
	}
	if conflictKey.BootstrapRequestDigest == key.BootstrapRequestDigest {
		t.Fatal("same operation key payload mutation did not produce conflict digest")
	}
	if err := MatchOperationKey(key, conflictKey); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("MatchOperationKey(changed digest) error = %v, want ErrOperationConflict", err)
	}
}

func assertOperationKeyDerived(t *testing.T, request BootstrapRequest, key OperationKey) {
	t.Helper()
	if key.CommandOperationID != request.CommandOperationID ||
		key.CommandOperationDigest != request.CommandOperationDigest ||
		key.AttemptOperationID != request.AttemptOperationID ||
		key.AttemptOperationDigest != request.AttemptOperationDigest ||
		key.IdempotencyKeyDigest != request.IdempotencyKeyDigest ||
		key.BootstrapRequestDigest != request.BootstrapRequestDigest {
		t.Fatalf("OperationKey() = %+v, does not preserve bootstrap identity", key)
	}
}

func testBootstrapResultPreparedOnly(t *testing.T) {
	result := bootstrapResultFixture(t)
	assertNoError(t, result.Validate())
	for _, status := range []string{StatusCommitted, StatusAborted, StatusOutcomeUnknown, ""} {
		mutated := result
		mutated.Status = status
		mutated, err := FinalizeBootstrapResultDigest(mutated)
		assertNoError(t, err)
		if err := mutated.Validate(); !errors.Is(err, ErrInvalidResult) {
			t.Fatalf("BootstrapResult.Validate(status=%q) error = %v, want ErrInvalidResult", status, err)
		}
	}
}

func testCommitBinding(t *testing.T) {
	request := commitFixture(t)
	assertNoError(t, request.Validate())
	for _, mutate := range []func(*CommitRequest){
		func(value *CommitRequest) { value.PreparedResultDigest = digestFixture("1") },
		func(value *CommitRequest) { value.ActiveCurrentBindingDigest = digestFixture("2") },
		func(value *CommitRequest) { value.ActiveCurrentBindingVersion++ },
		func(value *CommitRequest) { value.ActiveCurrentBindingIncarnationID += "-other" },
		func(value *CommitRequest) { value.ActivatedCredentialDigest = digestFixture("3") },
	} {
		mutated := request
		mutate(&mutated)
		if err := mutated.Validate(); !errors.Is(err, ErrInvalidCommit) {
			t.Fatalf("changed commit binding error = %v, want ErrInvalidCommit", err)
		}
	}
	for name, preparedRequestDigest := range map[string]string{
		"missing":    "",
		"malformed":  "not-a-digest",
		"mismatched": digestFixture("mismatched-request"),
	} {
		t.Run("prepared_request_digest_"+name, func(t *testing.T) {
			mutated := request
			mutated.PreparedRequestDigest = preparedRequestDigest
			mutated, err := FinalizeCommitOperationDigest(mutated)
			assertNoError(t, err)
			if err := mutated.Validate(); !errors.Is(err, ErrInvalidCommit) {
				t.Fatalf("prepared request digest %q error = %v, want ErrInvalidCommit", name, err)
			}
		})
	}
}

func testAbortCleanupBinding(t *testing.T) {
	request := abortFixture(t)
	assertNoError(t, request.Validate())
	for _, mutate := range []func(*AbortRequest){
		func(value *AbortRequest) { value.PreparedResultDigest = digestFixture("1") },
		func(value *AbortRequest) { value.CleanupOperationID += "-other" },
		func(value *AbortRequest) { value.CleanupOperationDigest = digestFixture("2") },
		func(value *AbortRequest) { value.CleanupEvidenceRef += "-other" },
		func(value *AbortRequest) { value.CleanupEvidenceDigest = digestFixture("3") },
	} {
		mutated := request
		mutate(&mutated)
		if err := mutated.Validate(); !errors.Is(err, ErrInvalidAbort) {
			t.Fatalf("changed abort binding error = %v, want ErrInvalidAbort", err)
		}
	}
	for name, preparedRequestDigest := range map[string]string{
		"missing":    "",
		"malformed":  "not-a-digest",
		"mismatched": digestFixture("mismatched-request"),
	} {
		t.Run("prepared_request_digest_"+name, func(t *testing.T) {
			mutated := request
			mutated.PreparedRequestDigest = preparedRequestDigest
			mutated, err := FinalizeAbortOperationDigest(mutated)
			assertNoError(t, err)
			if err := mutated.Validate(); !errors.Is(err, ErrInvalidAbort) {
				t.Fatalf("prepared request digest %q error = %v, want ErrInvalidAbort", name, err)
			}
		})
	}
}

func testOutcomePresenceOneHot(t *testing.T) {
	prepared := bootstrapResultFixture(t)
	key := prepared.Operation
	known := KnownOutcome{SchemaVersion: SchemaVersion, Operation: key, Status: StatusPrepared, Prepared: &prepared}
	assertNoError(t, known.Validate())

	commit := commitFixture(t)
	committed := CommitResult{
		SchemaVersion: SchemaVersion, Operation: commit.Operation, PreparedRequestDigest: commit.PreparedRequestDigest,
		PreparedResultDigest:              commit.PreparedResultDigest,
		ActiveCurrentBindingDigest:        commit.ActiveCurrentBindingDigest,
		ActiveCurrentBindingVersion:       commit.ActiveCurrentBindingVersion,
		ActiveCurrentBindingIncarnationID: commit.ActiveCurrentBindingIncarnationID,
		ActivatedCredentialDigest:         commit.ActivatedCredentialDigest, CommitOperationDigest: commit.CommitOperationDigest,
	}
	query := QueryOutcome{SchemaVersion: SchemaVersion, Operation: key, Status: StatusCommitted, Committed: &committed}
	assertNoError(t, query.Validate())

	abort := abortFixture(t)
	aborted := AbortResult{
		SchemaVersion: SchemaVersion, Operation: abort.Operation, PreparedRequestDigest: abort.PreparedRequestDigest,
		PreparedResultDigest: abort.PreparedResultDigest,
		CleanupOperationID:   abort.CleanupOperationID, CleanupOperationDigest: abort.CleanupOperationDigest,
		CleanupEvidenceRef: abort.CleanupEvidenceRef, CleanupEvidenceDigest: abort.CleanupEvidenceDigest,
		AbortOperationDigest: abort.AbortOperationDigest,
	}
	query = QueryOutcome{SchemaVersion: SchemaVersion, Operation: key, Status: StatusAborted, Aborted: &aborted}
	assertNoError(t, query.Validate())

	unknown := OutcomeUnknownResult{
		SchemaVersion: SchemaVersion, Operation: key, ReasonCode: "backend_outcome_unresolved",
		EvidenceRef: "receipt-owner://query/unknown", EvidenceDigest: digestFixture("e"),
	}
	query = QueryOutcome{SchemaVersion: SchemaVersion, Operation: key, Status: StatusOutcomeUnknown, OutcomeUnknown: &unknown}
	assertNoError(t, query.Validate())

	for name, invalid := range map[string]QueryOutcome{
		"none": {SchemaVersion: SchemaVersion, Operation: key, Status: StatusPrepared},
		"two": {
			SchemaVersion: SchemaVersion, Operation: key, Status: StatusPrepared,
			Prepared: &prepared, OutcomeUnknown: &unknown,
		},
		"status_mismatch": {SchemaVersion: SchemaVersion, Operation: key, Status: StatusCommitted, Prepared: &prepared},
	} {
		t.Run(name, func(t *testing.T) {
			if err := invalid.Validate(); !errors.Is(err, ErrInvalidOutcome) {
				t.Fatalf("QueryOutcome.Validate() error = %v, want ErrInvalidOutcome", err)
			}
		})
	}
}

func testUnknownIsQueryOnly(t *testing.T) {
	backendType := reflect.TypeFor[Backend]()
	if backendType.NumMethod() != 4 {
		t.Fatalf("Backend method count = %d, want exact four-method seam", backendType.NumMethod())
	}
	for _, name := range []string{"Bootstrap", "CommitBootstrap", "AbortBootstrap"} {
		assertKnownBackendMethod(t, backendType, name)
	}
	query, ok := backendType.MethodByName("QueryOperation")
	if !ok || query.Type.NumOut() != 2 || query.Type.Out(0) != reflect.TypeFor[QueryOutcome]() {
		t.Fatalf("Backend.QueryOperation must return QueryOutcome, got %v", query.Type)
	}
	for name := range managedLaunchJSONFields(t, reflect.TypeFor[KnownOutcome]()) {
		if strings.Contains(name, "unknown") {
			t.Fatalf("KnownOutcome exposes unknown branch %q", name)
		}
	}
}

func assertKnownBackendMethod(t *testing.T, backendType reflect.Type, name string) {
	t.Helper()
	method, ok := backendType.MethodByName(name)
	if !ok {
		t.Fatalf("Backend.%s is missing", name)
	}
	if method.Type.NumOut() != 2 || method.Type.Out(0) != reflect.TypeFor[KnownOutcome]() {
		t.Fatalf("Backend.%s first result = %v, want KnownOutcome", name, method.Type.Out(0))
	}
}

func testTypedNilBackendRejected(t *testing.T) {
	if err := RequireBackend(nil); !errors.Is(err, ErrBackendUnavailable) {
		t.Fatalf("RequireBackend(nil) error = %v", err)
	}
	var typedNil *fakeBackend
	if err := RequireBackend(typedNil); !errors.Is(err, ErrBackendUnavailable) {
		t.Fatalf("RequireBackend(typed nil) error = %v", err)
	}
	if err := RequireBackend(&fakeBackend{}); err != nil {
		t.Fatalf("RequireBackend(non-nil) error = %v", err)
	}
}

func testJSONRoundTrip(t *testing.T) {
	values := []any{
		bootstrapFixture(t), bootstrapResultFixture(t), commitFixture(t), abortFixture(t),
	}
	for _, value := range values {
		t.Run(reflect.TypeOf(value).Name(), func(t *testing.T) {
			encoded, err := json.Marshal(value)
			assertNoError(t, err)
			decoded := reflect.New(reflect.TypeOf(value))
			assertNoError(t, json.Unmarshal(encoded, decoded.Interface()))
			if !reflect.DeepEqual(value, decoded.Elem().Interface()) {
				t.Fatalf("JSON round trip changed value: got=%+v want=%+v", decoded.Elem().Interface(), value)
			}
		})
	}
}

func bootstrapFixture(t *testing.T) BootstrapRequest {
	t.Helper()
	request := BootstrapRequest{
		SchemaVersion:      SchemaVersion,
		CommandOperationID: "command-operation-1", CommandOperationDigest: digestFixture("1"),
		AttemptOperationID: "attempt-operation-1", AttemptOperationDigest: digestFixture("2"),
		AgentID: "agent-1", AgentDigest: digestFixture("3"), ProviderID: "provider-1",
		ProviderDigest: digestFixture("4"), ModelDigest: digestFixture("5"), CWDDigest: digestFixture("6"),
		ReservationID: "reservation-1", ReservationGeneration: 1,
		SourceFenceRef: "source-fence://1", SourceFenceDigest: digestFixture("7"),
		TargetFenceRef: "target-fence://1", TargetFenceDigest: digestFixture("8"),
		CredentialLeaseID: "credential-lease-1", CredentialLeaseDigest: digestFixture("9"),
		IdempotencyKeyDigest: digestFixture("a"),
	}
	finalized, err := FinalizeBootstrapRequestDigest(request)
	assertNoError(t, err)
	assertNoError(t, finalized.Validate())
	return finalized
}

func bootstrapResultFixture(t *testing.T) BootstrapResult {
	t.Helper()
	key, err := bootstrapFixture(t).OperationKey()
	assertNoError(t, err)
	result := BootstrapResult{
		SchemaVersion: SchemaVersion, Operation: key, Status: StatusPrepared,
		OpaqueHandle: "opaque-managed-runtime-handle", ProviderThreadIdentity: "provider-thread-1",
		ProviderSessionIdentity: "provider-session-1", RuntimeEpoch: "runtime-epoch-1", ExternalEpoch: "external-epoch-1",
		ProcessGeneration: 1, ActiveProcessReceiptRef: "active-process-receipt://1",
		ActiveProcessReceiptDigest: digestFixture("b"), RuntimeOwnerID: "managed-runtime-owner-1",
		RuntimeOwnerGeneration: 1, ExecutableDigest: digestFixture("c"), ArgvDigest: digestFixture("d"),
		EnvDigest: digestFixture("e"), CWDDigest: digestFixture("6"),
	}
	finalized, err := FinalizeBootstrapResultDigest(result)
	assertNoError(t, err)
	assertNoError(t, finalized.Validate())
	return finalized
}

func commitFixture(t *testing.T) CommitRequest {
	t.Helper()
	prepared := bootstrapResultFixture(t)
	request := CommitRequest{
		SchemaVersion: SchemaVersion, Operation: prepared.Operation, PreparedRequestDigest: prepared.Operation.BootstrapRequestDigest,
		PreparedResultDigest:       prepared.ResultDigest,
		ActiveCurrentBindingDigest: digestFixture("1"), ActiveCurrentBindingVersion: 1,
		ActiveCurrentBindingIncarnationID: "incarnation-1", ActivatedCredentialDigest: digestFixture("2"),
	}
	finalized, err := FinalizeCommitOperationDigest(request)
	assertNoError(t, err)
	assertNoError(t, finalized.Validate())
	return finalized
}

func abortFixture(t *testing.T) AbortRequest {
	t.Helper()
	prepared := bootstrapResultFixture(t)
	request := AbortRequest{
		SchemaVersion: SchemaVersion, Operation: prepared.Operation, PreparedRequestDigest: prepared.Operation.BootstrapRequestDigest,
		PreparedResultDigest: prepared.ResultDigest,
		CleanupOperationID:   "cleanup-operation-1", CleanupOperationDigest: digestFixture("3"),
		CleanupEvidenceRef: "cleanup-evidence://1", CleanupEvidenceDigest: digestFixture("4"),
	}
	finalized, err := FinalizeAbortOperationDigest(request)
	assertNoError(t, err)
	assertNoError(t, finalized.Validate())
	return finalized
}

func digestFixture(character string) string { return strings.Repeat(character, 64) }

func assertNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
