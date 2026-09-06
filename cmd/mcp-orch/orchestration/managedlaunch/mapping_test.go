package managedlaunch

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestLifecycleMappingValidatorsPreserveFixedBindings(t *testing.T) {
	bootstrap := bootstrapFixture(t)
	prepared := bootstrapResultFixture(t)
	prepare := PrepareProjection{
		SchemaVersion: SchemaVersion, OperationID: bootstrap.AttemptOperationID,
		BootstrapOperationID: bootstrap.CommandOperationID, BootstrapOperationDigest: bootstrap.CommandOperationDigest,
		SourceBindingDigest: bootstrap.BootstrapRequestDigest,
		ProviderDigest:      bootstrap.ProviderDigest, ExecutableDigest: prepared.ExecutableDigest,
		WorkingDirectoryDigest: bootstrap.CWDDigest, ArgumentVectorDigest: prepared.ArgvDigest,
		EnvironmentDigest: prepared.EnvDigest, ExpectedRuntimeWriterEpoch: "writer-epoch-1",
		RequestDigest: digestFixture("e"),
	}
	if err := ValidatePrepareProjection(bootstrap, prepare); err != nil {
		t.Fatalf("ValidatePrepareProjection() error = %v", err)
	}

	commit := commitFixture(t)
	commitProjection := CommitProjection{
		SchemaVersion: SchemaVersion, OperationID: commit.Operation.AttemptOperationID,
		PreparedOperationID: commit.Operation.AttemptOperationID, SourceBindingDigest: commit.CommitOperationDigest,
		PreparedRequestDigest:      commit.PreparedRequestDigest,
		ExpectedPreparedReceiptRef: prepared.ActiveProcessReceiptRef, ExpectedPreparedReceiptDigest: prepared.ActiveProcessReceiptDigest,
		ActiveBindingDigest: commit.ActiveCurrentBindingDigest, CredentialDigest: commit.ActivatedCredentialDigest,
		RequestDigest: digestFixture("c"),
	}
	if err := ValidateCommitProjection(commit, prepared, commitProjection); err != nil {
		t.Fatalf("ValidateCommitProjection() error = %v", err)
	}

	abort := abortFixture(t)
	abortProjection := AbortProjection{
		SchemaVersion: SchemaVersion, OperationID: abort.Operation.AttemptOperationID,
		PreparedOperationID: abort.Operation.AttemptOperationID, SourceBindingDigest: abort.AbortOperationDigest,
		PreparedRequestDigest:      abort.PreparedRequestDigest,
		ExpectedPreparedReceiptRef: prepared.ActiveProcessReceiptRef, ExpectedPreparedReceiptDigest: prepared.ActiveProcessReceiptDigest,
		CleanupEvidenceRef: abort.CleanupEvidenceRef, CleanupEvidenceDigest: abort.CleanupEvidenceDigest,
		RequestDigest: digestFixture("a"),
	}
	if err := ValidateAbortProjection(abort, prepared, abortProjection); err != nil {
		t.Fatalf("ValidateAbortProjection() error = %v", err)
	}
}

func TestLifecycleMappingValidatorsRejectImplicitFieldsAndUnknownConversion(t *testing.T) {
	bootstrap := bootstrapFixture(t)
	prepared := bootstrapResultFixture(t)
	prepare := PrepareProjection{
		SchemaVersion: SchemaVersion, OperationID: bootstrap.AttemptOperationID,
		BootstrapOperationID: bootstrap.CommandOperationID, BootstrapOperationDigest: bootstrap.CommandOperationDigest,
		SourceBindingDigest: bootstrap.BootstrapRequestDigest,
		ProviderDigest:      bootstrap.ProviderDigest, WorkingDirectoryDigest: bootstrap.CWDDigest,
		ArgumentVectorDigest: digestFixture("d"), EnvironmentDigest: digestFixture("e"),
		ExpectedRuntimeWriterEpoch: "writer-epoch-1", RequestDigest: digestFixture("e"),
	}
	if err := ValidatePrepareProjection(bootstrap, prepare); !errors.Is(err, ErrInvalidMapping) {
		t.Fatalf("missing executable digest error = %v, want ErrInvalidMapping", err)
	}
	prepare.ExecutableDigest = digestFixture("c")
	prepare.ExpectedRuntimeWriterEpoch = ""
	if err := ValidatePrepareProjection(bootstrap, prepare); !errors.Is(err, ErrInvalidMapping) {
		t.Fatalf("missing writer epoch error = %v, want ErrInvalidMapping", err)
	}

	commit := commitFixture(t)
	commitProjection := CommitProjection{
		SchemaVersion: SchemaVersion, OperationID: commit.Operation.AttemptOperationID,
		PreparedOperationID: commit.Operation.AttemptOperationID, SourceBindingDigest: commit.CommitOperationDigest,
		PreparedRequestDigest:         commit.PreparedRequestDigest,
		ExpectedPreparedReceiptDigest: prepared.ActiveProcessReceiptDigest, ActiveBindingDigest: commit.ActiveCurrentBindingDigest,
		CredentialDigest: commit.ActivatedCredentialDigest, RequestDigest: digestFixture("c"),
	}
	if err := ValidateCommitProjection(commit, prepared, commitProjection); !errors.Is(err, ErrInvalidMapping) {
		t.Fatalf("missing prepared receipt ref error = %v, want ErrInvalidMapping", err)
	}
	commitProjection.ExpectedPreparedReceiptRef = prepared.ActiveProcessReceiptRef
	commitProjection.PreparedRequestDigest = digestFixture("f")
	if err := ValidateCommitProjection(commit, prepared, commitProjection); !errors.Is(err, ErrInvalidMapping) {
		t.Fatalf("mismatched prepared request digest error = %v, want ErrInvalidMapping", err)
	}

	abort := abortFixture(t)
	abortProjection := AbortProjection{
		SchemaVersion: SchemaVersion, OperationID: abort.Operation.AttemptOperationID,
		PreparedOperationID: abort.Operation.AttemptOperationID, SourceBindingDigest: abort.AbortOperationDigest,
		PreparedRequestDigest:      abort.PreparedRequestDigest,
		ExpectedPreparedReceiptRef: prepared.ActiveProcessReceiptRef, ExpectedPreparedReceiptDigest: prepared.ActiveProcessReceiptDigest,
		CleanupEvidenceRef: abort.CleanupEvidenceRef, CleanupEvidenceDigest: abort.CleanupEvidenceDigest,
		RequestDigest: digestFixture("a"),
	}
	abortProjection.PreparedRequestDigest = digestFixture("0")
	if err := ValidateAbortProjection(abort, prepared, abortProjection); !errors.Is(err, ErrInvalidMapping) {
		t.Fatalf("mismatched prepared request digest error = %v, want ErrInvalidMapping", err)
	}

	unknown := OutcomeUnknownResult{SchemaVersion: SchemaVersion, Operation: prepared.Operation,
		ReasonCode: "backend_outcome_unresolved", EvidenceRef: "evidence://unknown", EvidenceDigest: digestFixture("u")}
	query := QueryOutcome{SchemaVersion: SchemaVersion, Operation: prepared.Operation,
		Status: StatusOutcomeUnknown, OutcomeUnknown: &unknown}
	if err := ValidateQueryProjection(query, QueryProjection{
		SchemaVersion: SchemaVersion, OperationID: bootstrap.AttemptOperationID,
		OperationDigest: digestFixture("f"), SourceBindingDigest: bootstrap.BootstrapRequestDigest,
		State: "prepared", ResultDigest: digestFixture("f"),
	}); !errors.Is(err, ErrInvalidMapping) {
		t.Fatalf("unknown-to-prepared conversion error = %v, want ErrInvalidMapping", err)
	}
}

func TestValidateQueryProjectionRejectsNonStateReceiptPairs(t *testing.T) {
	prepared := bootstrapResultFixture(t)
	preparedSource := QueryOutcome{
		SchemaVersion: SchemaVersion, Operation: prepared.Operation, Status: StatusPrepared, Prepared: &prepared,
	}
	preparedTarget := QueryProjection{
		SchemaVersion: SchemaVersion, OperationID: prepared.Operation.AttemptOperationID,
		OperationDigest: digestFixture("a"), SourceBindingDigest: prepared.Operation.BootstrapRequestDigest,
		State: "prepared", PreparedReceiptRef: prepared.ActiveProcessReceiptRef,
		PreparedReceiptDigest: prepared.ActiveProcessReceiptDigest, ResultDigest: digestFixture("c"),
	}
	if err := ValidateQueryProjection(preparedSource, preparedTarget); err != nil {
		t.Fatalf("valid prepared query projection error = %v", err)
	}

	for name, mutate := range map[string]func(*QueryProjection){
		"prepared_with_closed": func(value *QueryProjection) {
			value.ClosedReceiptRef, value.ClosedReceiptDigest = "receipt://closed", digestFixture("c")
		},
		"prepared_with_unknown": func(value *QueryProjection) {
			value.UncertaintyReceiptRef, value.UncertaintyDigest = "receipt://unknown", digestFixture("f")
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := preparedTarget
			mutate(&candidate)
			if err := ValidateQueryProjection(preparedSource, candidate); !errors.Is(err, ErrInvalidMapping) {
				t.Fatalf("ValidateQueryProjection() error = %v, want ErrInvalidMapping", err)
			}
		})
	}

	commit := commitFixture(t)
	committed := CommitResult{
		SchemaVersion: SchemaVersion, Operation: commit.Operation, PreparedRequestDigest: commit.PreparedRequestDigest,
		PreparedResultDigest: commit.PreparedResultDigest, ActiveCurrentBindingDigest: commit.ActiveCurrentBindingDigest,
		ActiveCurrentBindingVersion:       commit.ActiveCurrentBindingVersion,
		ActiveCurrentBindingIncarnationID: commit.ActiveCurrentBindingIncarnationID,
		ActivatedCredentialDigest:         commit.ActivatedCredentialDigest, CommitOperationDigest: commit.CommitOperationDigest,
	}
	committedSource := QueryOutcome{
		SchemaVersion: SchemaVersion, Operation: commit.Operation, Status: StatusCommitted, Committed: &committed,
	}
	committedTarget := QueryProjection{
		SchemaVersion: SchemaVersion, OperationID: commit.Operation.AttemptOperationID,
		OperationDigest: digestFixture("a"), SourceBindingDigest: commit.Operation.BootstrapRequestDigest,
		State: "committed", PreparedReceiptRef: "receipt://prepared", PreparedReceiptDigest: digestFixture("e"),
		ClosedReceiptRef: "receipt://closed", ClosedReceiptDigest: digestFixture("d"), ResultDigest: digestFixture("c"),
	}
	if err := ValidateQueryProjection(committedSource, committedTarget); err != nil {
		t.Fatalf("valid committed query projection error = %v", err)
	}
	committedTarget.UncertaintyReceiptRef, committedTarget.UncertaintyDigest = "receipt://unknown", digestFixture("f")
	if err := ValidateQueryProjection(committedSource, committedTarget); !errors.Is(err, ErrInvalidMapping) {
		t.Fatalf("committed query with uncertainty error = %v, want ErrInvalidMapping", err)
	}
}

func TestLifecycleMappingProjectionsExcludeRawProviderAndCredential(t *testing.T) {
	values := []any{PrepareProjection{}, CommitProjection{}, AbortProjection{}, QueryProjection{}}
	for _, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal %T: %v", value, err)
		}
		text := string(encoded)
		for _, forbidden := range []string{`"provider_id":`, `"credential":`, "token", `"argv":`, `"environment":`} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("%T projection exposes forbidden raw field %q: %s", value, forbidden, text)
			}
		}
	}
}
