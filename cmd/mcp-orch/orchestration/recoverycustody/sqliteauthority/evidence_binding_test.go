package sqliteauthority

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody"
)

type evidenceMutation func(Config, ExpectedResolution, FinalizedEvidence) (FinalizedEvidence, error)

type evidenceMutationCase struct {
	name     string
	mutate   evidenceMutation
	verifier recoverycustody.ReceiptVerifier
	wantErr  error
}

type mutatingEvidenceFinalizer struct {
	config Config
	mutate evidenceMutation
}

func (f mutatingEvidenceFinalizer) Finalize(
	_ context.Context,
	request FinalizationRequest,
) (FinalizedEvidence, error) {
	expected := request.ExpectedResolution
	evidence, err := finalizedEvidenceFixture(f.config, expected)
	if err != nil {
		return FinalizedEvidence{}, err
	}
	return f.mutate(f.config, expected, evidence)
}

func (f mutatingEvidenceFinalizer) Query(
	_ context.Context,
	request FinalizationRequest,
) (FinalizationObservation, error) {
	return finalizationObservationFixture(request, FinalizationQueryAbsent, nil), nil
}

type rejectingReceiptVerifier struct{}

func (rejectingReceiptVerifier) VerifyReceipt(
	context.Context,
	recoverycustody.SubmissionHandle,
	recoverycustody.ExternalReceipt,
) error {
	return errors.New("fixture verifier rejected receipt")
}

func TestSQLiteAuthorityRejectsEvidenceBindingMutationsWithoutPartialTerminalWrite(t *testing.T) {
	mutations := append(expectedEvidenceMutations(), checkpointEvidenceMutations()...)
	mutations = append(mutations, receiptRequestHeadMutations()...)
	mutations = append(mutations, receiptAuthorityMutations()...)
	mutations = append(mutations, receiptSecurityRetentionMutations()...)
	mutations = append(mutations, evidenceMutationCase{name: "external_verifier_rejection",
		mutate: identityEvidenceMutation, verifier: rejectingReceiptVerifier{}, wantErr: recoverycustody.ErrReceiptRejected})

	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			testRejectedEvidenceHasNoPartialTerminalWrite(t, mutation)
		})
	}
}

func expectedEvidenceMutations() []evidenceMutationCase {
	return []evidenceMutationCase{
		{name: "expected_kind", mutate: mutateExpected(func(expected *ExpectedResolution) {
			expected.Kind = recoverycustody.ReceiptAbort
		})},
		{name: "expected_operation", mutate: mutateExpected(func(expected *ExpectedResolution) {
			expected.Handle.OperationID = "operation-mutated"
		})},
		{name: "expected_request_digest", mutate: mutateExpected(func(expected *ExpectedResolution) {
			expected.Handle.RequestDigest = schemaTestDigest("c")
		})},
		{name: "expected_batch_digest", mutate: mutateExpected(func(expected *ExpectedResolution) {
			expected.Handle.BatchDigest = schemaTestDigest("c")
		})},
		{name: "expected_head_id", mutate: mutateExpected(func(expected *ExpectedResolution) {
			expected.HeadID = "head-mutated"
		})},
		{name: "expected_head_revision", mutate: mutateExpected(func(expected *ExpectedResolution) {
			expected.ExpectedHeadRevision++
		})},
		{name: "expected_head_root", mutate: mutateExpected(func(expected *ExpectedResolution) {
			expected.ExpectedHeadRootDigest = schemaTestDigest("c")
		})},
		{name: "expected_security_lineage", mutate: mutateExpected(func(expected *ExpectedResolution) {
			expected.SecurityTimeLineageDigest = schemaTestDigest("c")
		})},
		{name: "expected_security_generation", mutate: mutateExpected(func(expected *ExpectedResolution) {
			expected.ExpectedSecurityTimeGeneration--
		})},
		{name: "expected_security_revision", mutate: mutateExpected(func(expected *ExpectedResolution) {
			expected.ExpectedSecurityTimeRevision--
		})},
		{name: "expected_antirollback_predecessor", mutate: mutateExpected(func(expected *ExpectedResolution) {
			expected.ExpectedAntiRollbackGeneration++
		})},
		{name: "expected_antirollback_anchor", mutate: mutateExpected(func(expected *ExpectedResolution) {
			expected.ExpectedAnchorDigest = schemaTestDigest("c")
		})},
		{name: "expected_resolution_digest", mutate: mutateExpected(func(expected *ExpectedResolution) {
			expected.ExpectedResolutionDigest = schemaTestDigest("c")
		})},
	}
}

func checkpointEvidenceMutations() []evidenceMutationCase {
	return []evidenceMutationCase{
		{name: "checkpoint_head_id", mutate: mutateCheckpoint(func(checkpoint *FinalizedCheckpoint) {
			checkpoint.HeadID = "head-mutated"
		})},
		{name: "checkpoint_generation", mutate: mutateCheckpoint(func(checkpoint *FinalizedCheckpoint) {
			checkpoint.Generation++
		})},
		{name: "checkpoint_predecessor_generation", mutate: mutateCheckpoint(func(checkpoint *FinalizedCheckpoint) {
			checkpoint.PredecessorGeneration++
		})},
		{name: "checkpoint_predecessor_anchor", mutate: mutateCheckpoint(func(checkpoint *FinalizedCheckpoint) {
			checkpoint.PredecessorAnchorDigest = schemaTestDigest("c")
		})},
		{name: "checkpoint_anchor", mutate: mutateCheckpoint(func(checkpoint *FinalizedCheckpoint) {
			checkpoint.AnchorDigest = schemaTestDigest("c")
		})},
		{name: "checkpoint_head_root", mutate: mutateCheckpoint(func(checkpoint *FinalizedCheckpoint) {
			checkpoint.ResultHeadRootDigest = schemaTestDigest("c")
		})},
		{name: "checkpoint_finalization_digest", mutate: mutateCheckpoint(func(checkpoint *FinalizedCheckpoint) {
			checkpoint.FinalizationDigest = schemaTestDigest("c")
		})},
	}
}

func receiptRequestHeadMutations() []evidenceMutationCase {
	return []evidenceMutationCase{
		{name: "receipt_kind", mutate: mutateReceipt(func(receipt *recoverycustody.ExternalReceipt) {
			receipt.Kind = recoverycustody.ReceiptAbort
		})},
		{name: "receipt_operation", mutate: mutateReceipt(func(receipt *recoverycustody.ExternalReceipt) {
			receipt.OperationID = "operation-mutated"
		})},
		{name: "receipt_request_digest", mutate: mutateReceipt(func(receipt *recoverycustody.ExternalReceipt) {
			receipt.RequestDigest = schemaTestDigest("c")
		})},
		{name: "receipt_batch_digest", mutate: mutateReceipt(func(receipt *recoverycustody.ExternalReceipt) {
			receipt.BatchDigest = schemaTestDigest("c")
		})},
		{name: "receipt_head_id", mutate: mutateReceipt(func(receipt *recoverycustody.ExternalReceipt) {
			receipt.HeadID = "head-mutated"
		})},
		{name: "receipt_head_revision", mutate: mutateReceipt(func(receipt *recoverycustody.ExternalReceipt) {
			receipt.HeadRevision++
		})},
		{name: "receipt_head_root", mutate: mutateReceipt(func(receipt *recoverycustody.ExternalReceipt) {
			receipt.HeadRootDigest = schemaTestDigest("c")
		})},
	}
}

func receiptAuthorityMutations() []evidenceMutationCase {
	return []evidenceMutationCase{
		{name: "receipt_authority_id", mutate: mutateReceipt(func(receipt *recoverycustody.ExternalReceipt) {
			receipt.AuthorityID = "authority-mutated"
		})},
		{name: "receipt_authority_cluster", mutate: mutateReceipt(func(receipt *recoverycustody.ExternalReceipt) {
			receipt.AuthorityClusterID = "cluster-mutated"
		})},
		{name: "receipt_authority_generation", mutate: mutateReceipt(func(receipt *recoverycustody.ExternalReceipt) {
			receipt.AuthorityGeneration++
		})},
		{name: "receipt_owner_id", mutate: mutateReceipt(func(receipt *recoverycustody.ExternalReceipt) {
			receipt.OwnerID = "owner-mutated"
		})},
		{name: "receipt_owner_generation", mutate: mutateReceipt(func(receipt *recoverycustody.ExternalReceipt) {
			receipt.OwnerGeneration++
		})},
		{name: "receipt_authority_profile", mutate: mutateReceipt(func(receipt *recoverycustody.ExternalReceipt) {
			receipt.AuthorityProfile = "external-quorum-v2"
		})},
		{name: "receipt_authority_profile_generation", mutate: mutateReceipt(func(receipt *recoverycustody.ExternalReceipt) {
			receipt.AuthorityProfileGeneration++
		})},
		{name: "receipt_authority_profile_digest", mutate: mutateReceipt(func(receipt *recoverycustody.ExternalReceipt) {
			receipt.AuthorityProfileDigest = schemaTestDigest("c")
		})},
	}
}

func receiptSecurityRetentionMutations() []evidenceMutationCase {
	return []evidenceMutationCase{
		{name: "receipt_security_domain", mutate: mutateReceipt(func(receipt *recoverycustody.ExternalReceipt) {
			receipt.SecurityDomainID = "security-domain-mutated"
		})},
		{name: "receipt_security_lineage", mutate: mutateReceipt(func(receipt *recoverycustody.ExternalReceipt) {
			receipt.SecurityTimeLineageDigest = schemaTestDigest("c")
		})},
		{name: "receipt_security_generation", mutate: mutateReceipt(func(receipt *recoverycustody.ExternalReceipt) {
			receipt.SecurityTimeGeneration--
		})},
		{name: "receipt_security_revision", mutate: mutateReceipt(func(receipt *recoverycustody.ExternalReceipt) {
			receipt.SecurityTimeRevision--
		})},
		{name: "receipt_antirollback_generation", mutate: mutateReceipt(func(receipt *recoverycustody.ExternalReceipt) {
			receipt.AntiRollbackGeneration++
		})},
		{name: "receipt_antirollback_anchor", mutate: mutateReceipt(func(receipt *recoverycustody.ExternalReceipt) {
			receipt.AntiRollbackAnchorDigest = schemaTestDigest("c")
		})},
		{name: "receipt_retention_policy", mutate: mutateReceipt(func(receipt *recoverycustody.ExternalReceipt) {
			receipt.RetentionPolicyDigest = schemaTestDigest("c")
		})},
		{name: "receipt_retention_until", mutate: mutateReceipt(func(receipt *recoverycustody.ExternalReceipt) {
			receipt.RetentionUntil = receipt.AuthorityTime
		})},
		{name: "receipt_gc_anchor_generation", mutate: mutateReceipt(func(receipt *recoverycustody.ExternalReceipt) {
			receipt.GCNotBeforeAnchorGeneration = receipt.AntiRollbackGeneration - 1
		})},
		{name: "receipt_authority_time", mutate: mutateReceipt(func(receipt *recoverycustody.ExternalReceipt) {
			receipt.AuthorityTime = receipt.AuthorityTime.In(time.FixedZone("fixture-offset", 60*60))
		})},
	}
}

func TestSQLiteAuthorityAvailabilityAbortSingleWinnerAndExactReplay(t *testing.T) {
	cases := []struct {
		name       string
		winnerKind string
		wantStatus string
	}{
		{name: "availability_wins", winnerKind: recoverycustody.ReceiptAvailability, wantStatus: recoverycustody.StatusAvailable},
		{name: "abort_wins", winnerKind: recoverycustody.ReceiptAbort, wantStatus: recoverycustody.StatusAborted},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			testSingleWinnerAndExactReplay(t, test.name, test.winnerKind, test.wantStatus)
		})
	}
}

func testSingleWinnerAndExactReplay(t *testing.T, name, winnerKind, wantStatus string) {
	t.Helper()
	config := authorityTestConfig(t, "winner-"+name)
	if err := Bootstrap(t.Context(), config, acceptingLease{}); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	finalizer := &evidenceFinalizerFixture{config: config}
	authority := openEvidenceTestAuthority(t, config, finalizer, acceptingReceiptVerifier{})
	request := schemaTestRequest(t)
	pending, err := authority.Submit(t.Context(), request)
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	winner := finalizedResolutionRequest(t, pending.Handle, "winner", winnerKind)
	terminal, err := authority.Resolve(t.Context(), winner)
	if err != nil || terminal.Status != wantStatus || terminal.Receipt == nil {
		t.Fatalf("Resolve(winner) = (%+v, %v), want %s terminal", terminal, err, wantStatus)
	}

	assertExactTerminalReplay(t, authority, request, winner, terminal)
	loser := finalizedResolutionRequest(t, pending.Handle, "loser", oppositeReceiptKind(winnerKind))
	if outcome, err := authority.Resolve(t.Context(), loser); !errors.Is(err, recoverycustody.ErrResolutionConflict) ||
		!reflect.DeepEqual(outcome, recoverycustody.Outcome{}) {
		t.Fatalf("Resolve(loser) = (%+v, %v), want zero ErrResolutionConflict", outcome, err)
	}
	assertExactQueriedOutcome(t, authority, pending.Handle, terminal)
	assertTerminalEvidenceCounts(t, authority.db, request.OperationID, 1, 1)
	if finalizer.finalizeCalls != 1 || finalizer.queryCalls != 1 {
		t.Fatalf("finalizer calls after exact terminal replay = finalize:%d query:%d, want 1/1",
			finalizer.finalizeCalls, finalizer.queryCalls)
	}
}

func oppositeReceiptKind(kind string) string {
	if kind == recoverycustody.ReceiptAbort {
		return recoverycustody.ReceiptAvailability
	}
	return recoverycustody.ReceiptAbort
}

func mutateExpected(mutate func(*ExpectedResolution)) evidenceMutation {
	return func(config Config, expected ExpectedResolution, _ FinalizedEvidence) (FinalizedEvidence, error) {
		mutate(&expected)
		return finalizedEvidenceFixture(config, expected)
	}
}

func mutateCheckpoint(mutate func(*FinalizedCheckpoint)) evidenceMutation {
	return func(_ Config, _ ExpectedResolution, evidence FinalizedEvidence) (FinalizedEvidence, error) {
		mutate(&evidence.Checkpoint)
		return evidence, nil
	}
}

func mutateReceipt(mutate func(*recoverycustody.ExternalReceipt)) evidenceMutation {
	return func(_ Config, _ ExpectedResolution, evidence FinalizedEvidence) (FinalizedEvidence, error) {
		mutate(&evidence.Receipt)
		receipt, err := recoverycustody.FinalizeReceiptDigest(evidence.Receipt)
		if err != nil {
			return FinalizedEvidence{}, err
		}
		evidence.Receipt = receipt
		return evidence, nil
	}
}

func identityEvidenceMutation(_ Config, _ ExpectedResolution, evidence FinalizedEvidence) (FinalizedEvidence, error) {
	return evidence, nil
}

func testRejectedEvidenceHasNoPartialTerminalWrite(
	t *testing.T,
	mutation evidenceMutationCase,
) {
	t.Helper()
	config, authority := setupMutationAuthority(t, mutation)
	pending := submitPendingEvidenceRequest(t, authority)
	resolution := finalizedResolutionRequest(t, pending.Handle, "binding", recoverycustody.ReceiptAvailability)
	assertMutatedEvidenceRejected(t, authority, pending.Handle, resolution, mutation.wantErr)
	assertNoPartialTerminalWrite(t, authority, config, resolution)
	assertIndeterminateQuery(t, authority, pending.Handle)
}

func setupMutationAuthority(t *testing.T, mutation evidenceMutationCase) (Config, *Authority) {
	t.Helper()
	config := authorityTestConfig(t, "binding-"+mutation.name)
	config.SecurityTimeGeneration = 2
	if err := Bootstrap(t.Context(), config, acceptingLease{}); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	verifier := mutation.verifier
	if verifier == nil {
		verifier = acceptingReceiptVerifier{}
	}
	finalizer := mutatingEvidenceFinalizer{config: config, mutate: mutation.mutate}
	return config, openEvidenceTestAuthority(t, config, finalizer, verifier)
}

func submitPendingEvidenceRequest(t *testing.T, authority *Authority) recoverycustody.Outcome {
	t.Helper()
	pending, err := authority.Submit(t.Context(), schemaTestRequest(t))
	if err != nil || pending.Status != recoverycustody.StatusPending {
		t.Fatalf("Submit() = (%+v, %v), want pending", pending, err)
	}
	return pending
}

func assertMutatedEvidenceRejected(
	t *testing.T,
	authority *Authority,
	handle recoverycustody.SubmissionHandle,
	resolution ResolutionRequest,
	wantErr error,
) {
	t.Helper()
	outcome, err := authority.Resolve(t.Context(), resolution)
	if err == nil {
		t.Fatalf("Resolve(mutated evidence) = %+v, want rejection", outcome)
	}
	if wantErr != nil && !errors.Is(err, wantErr) {
		t.Fatalf("Resolve(mutated evidence) error = %v, want %v", err, wantErr)
	}
	if outcome.Status != recoverycustody.StatusIndeterminate || outcome.Handle != handle || outcome.Receipt != nil {
		t.Fatalf("Resolve(mutated evidence) outcome = %+v, want same-handle indeterminate", outcome)
	}
}

func assertIndeterminateQuery(t *testing.T, authority *Authority, handle recoverycustody.SubmissionHandle) {
	t.Helper()
	queried, queryErr := authority.Query(t.Context(), handle)
	if queryErr != nil || queried.Status != recoverycustody.StatusIndeterminate || queried.Handle != handle || queried.Receipt != nil {
		t.Fatalf("Query(same handle) = (%+v, %v), want indeterminate", queried, queryErr)
	}
}

func openEvidenceTestAuthority(
	t *testing.T,
	config Config,
	finalizer EvidenceFinalizer,
	verifier recoverycustody.ReceiptVerifier,
) *Authority {
	t.Helper()
	authority, err := OpenAuthority(t.Context(), config, acceptingLease{}, finalizer, verifier)
	if err != nil {
		t.Fatalf("OpenAuthority() error = %v", err)
	}
	t.Cleanup(func() {
		if err := authority.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return authority
}

func finalizedResolutionRequest(
	t *testing.T,
	handle recoverycustody.SubmissionHandle,
	identity string,
	kind string,
) ResolutionRequest {
	t.Helper()
	request := ResolutionRequest{SchemaVersion: 1, ResolutionID: "resolution-" + identity, Handle: handle,
		Kind: kind, FinalizationOperationID: "finalization-" + identity}
	finalized, err := FinalizeResolutionRequestDigest(request)
	if err != nil {
		t.Fatalf("FinalizeResolutionRequestDigest() error = %v", err)
	}
	return finalized
}

func assertNoPartialTerminalWrite(t *testing.T, authority *Authority, config Config, resolution ResolutionRequest) {
	t.Helper()
	assertTerminalEvidenceCounts(t, authority.db, resolution.Handle.OperationID, 0, 0)
	assertSubmissionRemainsPending(t, authority.db, resolution.Handle.OperationID)
	assertResolutionRemainsActive(t, authority.db, resolution.Handle.OperationID)
	assertAuthorityHeadRemainsClaimed(t, authority.db, config, resolution.ResolutionID)
}

func assertSubmissionRemainsPending(t *testing.T, db *sql.DB, operationID string) {
	t.Helper()
	var status string
	var receiptDigest sql.NullString
	if err := db.QueryRow(
		"SELECT status, receipt_digest FROM submissions WHERE operation_id = ?",
		operationID,
	).Scan(&status, &receiptDigest); err != nil {
		t.Fatalf("read submission terminal state: %v", err)
	}
	if status != recoverycustody.StatusPending || receiptDigest.Valid {
		t.Fatalf("submission terminal state = (%q, %+v), want pending without receipt", status, receiptDigest)
	}
}

func assertResolutionRemainsActive(t *testing.T, db *sql.DB, operationID string) {
	t.Helper()
	var state string
	var checkpointGeneration sql.NullInt64
	var checkpointAnchor, resultHeadRoot, finalizationDigest, finalizedAt sql.NullString
	if err := db.QueryRow(`SELECT state, checkpoint_generation, checkpoint_anchor_digest,
result_head_root_digest, finalization_digest, finalized_at FROM resolutions WHERE operation_id = ?`,
		operationID).Scan(&state, &checkpointGeneration, &checkpointAnchor, &resultHeadRoot,
		&finalizationDigest, &finalizedAt); err != nil {
		t.Fatalf("read resolution terminal state: %v", err)
	}
	if state != resolutionStateActive || checkpointGeneration.Valid || checkpointAnchor.Valid || resultHeadRoot.Valid ||
		finalizationDigest.Valid || finalizedAt.Valid {
		t.Fatalf("resolution partial terminal state = (%q, %+v, %+v, %+v, %+v, %+v)", state,
			checkpointGeneration, checkpointAnchor, resultHeadRoot, finalizationDigest, finalizedAt)
	}
}

func assertAuthorityHeadRemainsClaimed(t *testing.T, db *sql.DB, config Config, resolutionID string) {
	t.Helper()
	head, err := scanAuthorityHead(db.QueryRow(selectAuthorityHeadSQL, config.SecurityDomainID, config.AuthorityProfile))
	if err != nil {
		t.Fatalf("read authority head: %v", err)
	}
	want := authorityHead{HeadID: config.HeadID, Revision: 0, RootDigest: config.InitialHeadRootDigest,
		SecurityTimeLineageDigest: config.SecurityTimeLineageDigest, SecurityTimeGeneration: config.SecurityTimeGeneration,
		SecurityTimeRevision: config.SecurityTimeRevision, AntiRollbackGeneration: config.AntiRollbackGeneration,
		AntiRollbackAnchorDigest:   config.AntiRollbackAnchorDigest,
		AntiRollbackAttestationRef: config.AntiRollbackAttestationRef,
		ActiveResolutionID:         sql.NullString{String: resolutionID, Valid: true}}
	if !reflect.DeepEqual(head, want) {
		t.Fatalf("authority head after rejection = %+v, want %+v", head, want)
	}
}

func assertTerminalEvidenceCounts(t *testing.T, db *sql.DB, operationID string, wantCheckpoints, wantReceipts int) {
	t.Helper()
	assertTableCount(t, db, `SELECT count(*) FROM antirollback_checkpoints
WHERE resolution_id = (SELECT resolution_id FROM resolutions WHERE operation_id = ?)`, operationID,
		"antirollback_checkpoints", wantCheckpoints)
	assertTableCount(t, db, `SELECT count(*) FROM terminal_receipts
WHERE resolution_id = (SELECT resolution_id FROM resolutions WHERE operation_id = ?)`, operationID,
		"terminal_receipts", wantReceipts)
}

func assertTableCount(t *testing.T, db *sql.DB, query, operationID, table string, want int) {
	t.Helper()
	var count int
	if err := db.QueryRow(query, operationID).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if count != want {
		t.Fatalf("%s count = %d, want %d", table, count, want)
	}
}

func assertExactTerminalReplay(
	t *testing.T,
	authority *Authority,
	request recoverycustody.SubmissionRequest,
	resolution ResolutionRequest,
	want recoverycustody.Outcome,
) {
	t.Helper()
	assertExactQueriedOutcome(t, authority, resolution.Handle, want)
	replayedResolution, err := authority.Resolve(t.Context(), resolution)
	if err != nil || !reflect.DeepEqual(replayedResolution, want) {
		t.Fatalf("Resolve(exact replay) = (%+v, %v), want %+v", replayedResolution, err, want)
	}
	replayedSubmission, err := authority.Submit(t.Context(), request)
	if err != nil || !reflect.DeepEqual(replayedSubmission, want) {
		t.Fatalf("Submit(exact replay) = (%+v, %v), want %+v", replayedSubmission, err, want)
	}
}

func assertExactQueriedOutcome(
	t *testing.T,
	authority *Authority,
	handle recoverycustody.SubmissionHandle,
	want recoverycustody.Outcome,
) {
	t.Helper()
	queried, err := authority.Query(t.Context(), handle)
	if err != nil || !reflect.DeepEqual(queried, want) {
		t.Fatalf("Query(exact terminal) = (%+v, %v), want %+v", queried, err, want)
	}
}
