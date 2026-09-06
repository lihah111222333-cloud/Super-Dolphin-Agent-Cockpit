package sqliteauthority

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody"
	platformsqlite "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
)

type acceptingLease struct{}

func (acceptingLease) Validate(context.Context, AuthorityScope) error { return nil }

func (lease acceptingLease) WithValidatedScope(ctx context.Context, scope AuthorityScope, callback func() error) error {
	if err := lease.Validate(ctx, scope); err != nil {
		return err
	}
	return callback()
}

type inertFinalizer struct{}

func (inertFinalizer) Finalize(context.Context, FinalizationRequest) (FinalizedEvidence, error) {
	return FinalizedEvidence{}, ErrInvalidEvidence
}

func (inertFinalizer) Query(context.Context, FinalizationRequest) (FinalizationObservation, error) {
	return FinalizationObservation{}, ErrInvalidEvidence
}

type acceptingReceiptVerifier struct{}

func (acceptingReceiptVerifier) VerifyReceipt(context.Context, recoverycustody.SubmissionHandle, recoverycustody.ExternalReceipt) error {
	return nil
}

type evidenceFinalizerFixture struct {
	config        Config
	finalizeCalls int
	queryCalls    int
}

func (f *evidenceFinalizerFixture) Finalize(_ context.Context, request FinalizationRequest) (FinalizedEvidence, error) {
	f.finalizeCalls++
	return finalizedEvidenceFixture(f.config, request.ExpectedResolution)
}

func (f *evidenceFinalizerFixture) Query(_ context.Context, request FinalizationRequest) (FinalizationObservation, error) {
	f.queryCalls++
	return finalizationObservationFixture(request, FinalizationQueryAbsent, nil), nil
}

// finalizationObservationFixture 生成与原请求精确绑定的本地非权威查询观察。
func finalizationObservationFixture(
	request FinalizationRequest,
	state FinalizationQueryState,
	evidence *FinalizedEvidence,
) FinalizationObservation {
	return FinalizationObservation{SchemaVersion: AuthoritySchemaVersion, State: state,
		FinalizationOperationID:   request.FinalizationOperationID,
		FinalizationRequestDigest: request.FinalizationRequestDigest, ObservationGeneration: 1,
		ReadFenceDigest: schemaTestDigest("f"), Evidence: evidence}
}

func TestSQLiteAuthoritySchemaContract(t *testing.T) {
	t.Run("bootstrap_and_open_exact_schema", testBootstrapAndOpenExactSchema)
	t.Run("open_does_not_bootstrap", testOpenDoesNotBootstrap)
	t.Run("digest_tail_nul_rejected", testDigestTailNULRejected)
	t.Run("resolution_digest_derivation", testResolutionDigestDerivation)
	t.Run("submit_resolve_and_single_winner", testSubmitResolveAndSingleWinner)
	t.Run("raw_terminal_receipt_binding", testRawTerminalReceiptBinding)
	t.Run("resolution_finalized_at_nul_rejected", testResolutionFinalizedAtNULRejected)
}

func testBootstrapAndOpenExactSchema(t *testing.T) {
	config := authorityTestConfig(t, "exact")
	if err := Bootstrap(t.Context(), config, acceptingLease{}); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	authority, err := OpenAuthority(t.Context(), config, acceptingLease{}, inertFinalizer{}, acceptingReceiptVerifier{})
	if err != nil {
		t.Fatalf("OpenAuthority() error = %v", err)
	}
	t.Cleanup(func() {
		if err := authority.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	if authority.db.Stats().MaxOpenConnections != 1 {
		t.Fatalf("MaxOpenConnections = %d, want 1", authority.db.Stats().MaxOpenConnections)
	}
	assertAuthorityTablesAndColumns(t, authority.db)
	assertAuthorityFilePrivate(t, config.Path)
}

func testOpenDoesNotBootstrap(t *testing.T) {
	config := authorityTestConfig(t, "missing")
	db, err := platformsqlite.Open(t.Context(), platformsqlite.OpenOptions{Path: config.Path})
	if err != nil {
		t.Fatalf("create empty authority DB: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close empty authority DB: %v", err)
	}
	authority, err := OpenAuthority(t.Context(), config, acceptingLease{}, inertFinalizer{}, acceptingReceiptVerifier{})
	if authority != nil || !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("OpenAuthority() = (%v, %v), want nil ErrSchemaMismatch", authority, err)
	}
	db, err = platformsqlite.Open(t.Context(), platformsqlite.OpenOptions{Path: config.Path})
	if err != nil {
		t.Fatalf("reopen empty authority DB: %v", err)
	}
	defer db.Close()
	var tableCount int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&tableCount); err != nil {
		t.Fatalf("count tables: %v", err)
	}
	if tableCount != 0 {
		t.Fatalf("OpenAuthority bootstrapped %d tables", tableCount)
	}
}

func testDigestTailNULRejected(t *testing.T) {
	config := authorityTestConfig(t, "nul")
	db, err := platformsqlite.Open(t.Context(), platformsqlite.OpenOptions{Path: config.Path})
	if err != nil {
		t.Fatalf("open NUL fixture DB: %v", err)
	}
	defer db.Close()
	err = withRawImmediate(t.Context(), db, func(conn *sql.Conn) error {
		if _, err := conn.ExecContext(t.Context(), authoritySchemaSQL); err != nil {
			return err
		}
		_, err := conn.ExecContext(t.Context(), insertAuthoritySchemaSQL, AuthorityDatabaseSchemaRevision,
			strings.Repeat("a", 64)+"\x00tail", "2026-09-02T00:00:00Z")
		return err
	})
	if err == nil {
		t.Fatal("schema_digest with trailing NUL was accepted")
	}
}

func testResolutionDigestDerivation(t *testing.T) {
	request := ResolutionRequest{SchemaVersion: 1, ResolutionID: "resolution-1", Handle: schemaTestHandle(t),
		Kind: recoverycustody.ReceiptAvailability, FinalizationOperationID: "finalization-1"}
	finalized, err := FinalizeResolutionRequestDigest(request)
	if err != nil {
		t.Fatalf("FinalizeResolutionRequestDigest() error = %v", err)
	}
	if err := finalized.Validate(); err != nil {
		t.Fatalf("finalized request Validate() error = %v", err)
	}
	mutated := finalized
	mutated.FinalizationOperationID = "finalization-2"
	if err := mutated.Validate(); err == nil {
		t.Fatal("finalization operation mutation retained valid digests")
	}
	expected, err := BindExpectedResolutionDigest(ExpectedResolution{ResolutionRequest: finalized, HeadID: "head-1",
		ExpectedHeadRootDigest: schemaTestDigest("1"), ExpectedAntiRollbackGeneration: 1,
		ExpectedAnchorDigest: schemaTestDigest("2"), SecurityTimeLineageDigest: schemaTestDigest("3"),
		ExpectedSecurityTimeGeneration: 1, ExpectedSecurityTimeRevision: 1})
	if err != nil {
		t.Fatalf("BindExpectedResolutionDigest() error = %v", err)
	}
	finalization, err := BindFinalizationRequest(expected)
	if err != nil || finalization.Validate() != nil {
		t.Fatalf("BindFinalizationRequest() = (%+v, %v)", finalization, err)
	}
}

func testSubmitResolveAndSingleWinner(t *testing.T) {
	config := authorityTestConfig(t, "resolve")
	if err := Bootstrap(t.Context(), config, acceptingLease{}); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	finalizer := &evidenceFinalizerFixture{config: config}
	authority, err := OpenAuthority(t.Context(), config, acceptingLease{}, finalizer, acceptingReceiptVerifier{})
	if err != nil {
		t.Fatalf("OpenAuthority() error = %v", err)
	}
	defer authority.Close()
	resolution := resolveAvailableReceipt(t, authority, finalizer)
	assertLosingAbortConflict(t, authority, resolution)
}

func testRawTerminalReceiptBinding(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*FinalizedEvidence)
	}{
		{name: "submission_request_digest", mutate: func(e *FinalizedEvidence) { e.Receipt.RequestDigest = schemaTestDigest("c") }},
		{name: "submission_batch_digest", mutate: func(e *FinalizedEvidence) { e.Receipt.BatchDigest = schemaTestDigest("c") }},
		{name: "security_lineage", mutate: func(e *FinalizedEvidence) { e.Receipt.SecurityTimeLineageDigest = schemaTestDigest("c") }},
		{name: "security_tuple_not_strict", mutate: func(e *FinalizedEvidence) { e.Receipt.SecurityTimeRevision-- }},
		{name: "authority_id", mutate: func(e *FinalizedEvidence) { e.Receipt.AuthorityID += "-forged" }},
		{name: "authority_cluster", mutate: func(e *FinalizedEvidence) { e.Receipt.AuthorityClusterID += "-forged" }},
		{name: "authority_generation", mutate: func(e *FinalizedEvidence) { e.Receipt.AuthorityGeneration++ }},
		{name: "owner_id", mutate: func(e *FinalizedEvidence) { e.Receipt.OwnerID += "-forged" }},
		{name: "owner_generation", mutate: func(e *FinalizedEvidence) { e.Receipt.OwnerGeneration++ }},
		{name: "profile_generation", mutate: func(e *FinalizedEvidence) { e.Receipt.AuthorityProfileGeneration++ }},
		{name: "profile_digest", mutate: func(e *FinalizedEvidence) { e.Receipt.AuthorityProfileDigest = schemaTestDigest("c") }},
		{name: "retention_policy", mutate: func(e *FinalizedEvidence) { e.Receipt.RetentionPolicyDigest = schemaTestDigest("c") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newHardeningAnchoredFixture(t, nil)
			evidence := fixture.evidence
			test.mutate(&evidence)
			if err := hardeningInsertTerminalReceipt(t, fixture, evidence); err == nil {
				t.Fatalf("raw terminal receipt accepted forged %s", test.name)
			}
		})
	}
}

func testResolutionFinalizedAtNULRejected(t *testing.T) {
	config, authority := openHardeningAuthority(t)
	expected, evidence := prepareHardeningResolution(t, authority, config)
	_, err := authority.db.ExecContext(t.Context(), `UPDATE resolutions SET state = 'anchored',
checkpoint_generation = ?, checkpoint_anchor_digest = ?, result_head_root_digest = ?, finalization_digest = ?,
finalized_at = ?, updated_at = ? WHERE resolution_id = ?`, evidence.Checkpoint.Generation,
		evidence.Checkpoint.AnchorDigest, evidence.Checkpoint.ResultHeadRootDigest, evidence.Checkpoint.FinalizationDigest,
		"2026-09-02T01:02:03Z\x00tail", "2026-09-02T01:02:04Z", expected.ResolutionID)
	if err == nil {
		t.Fatal("resolutions.finalized_at accepted trailing NUL")
	}
}

// resolveAvailableReceipt 提交请求并验证可用回执成为唯一获胜结果。
func resolveAvailableReceipt(t *testing.T, authority *Authority, finalizer *evidenceFinalizerFixture) ResolutionRequest {
	t.Helper()
	request := schemaTestRequest(t)
	pending, err := authority.Submit(t.Context(), request)
	if err != nil || pending.Status != recoverycustody.StatusPending {
		t.Fatalf("Submit() = (%+v, %v), want pending", pending, err)
	}
	resolution := ResolutionRequest{SchemaVersion: 1, ResolutionID: "resolution-1", Handle: pending.Handle,
		Kind: recoverycustody.ReceiptAvailability, FinalizationOperationID: "finalization-1"}
	resolution, err = FinalizeResolutionRequestDigest(resolution)
	if err != nil {
		t.Fatalf("FinalizeResolutionRequestDigest() error = %v", err)
	}
	terminal, err := authority.Resolve(t.Context(), resolution)
	if err != nil || terminal.Status != recoverycustody.StatusAvailable || terminal.Receipt == nil {
		t.Fatalf("Resolve() = (%+v, %v), want available receipt", terminal, err)
	}
	if finalizer.finalizeCalls != 1 || finalizer.queryCalls != 1 {
		t.Fatalf("finalizer calls = finalize:%d query:%d, want 1/1", finalizer.finalizeCalls, finalizer.queryCalls)
	}
	return resolution
}

// assertLosingAbortConflict 验证已决请求拒绝后续终止候选。
func assertLosingAbortConflict(t *testing.T, authority *Authority, resolution ResolutionRequest) {
	t.Helper()
	abort := resolution
	abort.ResolutionID = "resolution-abort"
	abort.Kind = recoverycustody.ReceiptAbort
	abort.FinalizationOperationID = "finalization-abort"
	abort, err := FinalizeResolutionRequestDigest(abort)
	if err != nil {
		t.Fatalf("finalize abort candidate: %v", err)
	}
	if _, err := authority.Resolve(t.Context(), abort); !errors.Is(err, recoverycustody.ErrResolutionConflict) {
		t.Fatalf("losing abort Resolve() error = %v, want ErrResolutionConflict", err)
	}
}

func assertAuthorityTablesAndColumns(t *testing.T, db *sql.DB) {
	t.Helper()
	required := requiredAuthorityColumns()
	tables := make([]string, 0, len(required))
	for table := range required {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	for _, table := range tables {
		rows, err := db.Query("PRAGMA table_info(" + table + ")")
		if err != nil {
			t.Fatalf("table_info(%s): %v", table, err)
		}
		var columns []string
		for rows.Next() {
			var cid, notNull, primaryKey int
			var name, columnType string
			var defaultValue any
			if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
				rows.Close()
				t.Fatalf("scan table_info(%s): %v", table, err)
			}
			columns = append(columns, name)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatalf("iterate table_info(%s): %v", table, err)
		}
		if err := rows.Close(); err != nil {
			t.Fatalf("close table_info(%s): %v", table, err)
		}
		if !reflect.DeepEqual(columns, required[table]) {
			t.Fatalf("columns[%s] = %v, want %v", table, columns, required[table])
		}
	}
}

func assertAuthorityFilePrivate(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat authority file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("authority mode = %#o, want 0600", info.Mode().Perm())
	}
}

func authorityTestConfig(t *testing.T, name string) Config {
	t.Helper()
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	state := filepath.Join(root, "authority")
	product := filepath.Join(root, "product")
	for _, directory := range []string{workspace, state, product} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatalf("mkdir fixture: %v", err)
		}
	}
	return Config{SchemaVersion: 1, Path: filepath.Join(state, name+".db"),
		ProductSQLitePath: filepath.Join(product, "product.db"), WorkspaceRoot: workspace,
		StoreID: "store-1", AuthorityID: "authority-1", AuthorityClusterID: "cluster-1",
		AuthorityProfile: "external-quorum-v1", AuthorityGeneration: 1, OwnerID: "owner-1", OwnerGeneration: 1,
		OwnerFenceDigest: schemaTestDigest("1"), AuthorityProfileGeneration: 1,
		AuthorityProfileDigest: schemaTestDigest("2"), SecurityDomainID: "security-domain-1",
		SecurityTimeLineageDigest: schemaTestDigest("3"), SecurityTimeGeneration: 1, SecurityTimeRevision: 1,
		RetentionPolicyDigest: schemaTestDigest("4"), HeadID: "head-1", InitialHeadRootDigest: schemaTestDigest("5"),
		AntiRollbackGeneration: 1, AntiRollbackAnchorDigest: schemaTestDigest("6"),
		AntiRollbackAttestationRef: "anchor://initial/" + schemaTestDigest("6"), WriterEpoch: 1,
		DeploymentFence: "deployment-1"}
}

func schemaTestHandle(t *testing.T) recoverycustody.SubmissionHandle {
	t.Helper()
	request := schemaTestRequest(t)
	handle, err := recoverycustody.NewSubmissionHandle(request)
	if err != nil {
		t.Fatalf("NewSubmissionHandle() error = %v", err)
	}
	return handle
}

func schemaTestRequest(t *testing.T) recoverycustody.SubmissionRequest {
	t.Helper()
	request := recoverycustody.SubmissionRequest{SchemaVersion: 1, OperationID: "operation-1",
		SecurityDomainID: "security-domain-1", AuthorityProfile: "external-quorum-v1", CommandID: "command-1",
		CommandVersion: 1, CommandProfileDigest: schemaTestDigest("1"), AuthorizationDigest: schemaTestDigest("2"),
		IdentityFenceDigest: schemaTestDigest("3"), ContentSnapshotDigest: schemaTestDigest("4"),
		ResourceSnapshotDigest: schemaTestDigest("5"), BatchDigest: schemaTestDigest("6"),
		IdempotencyKeyDigest: schemaTestDigest("7")}
	request, err := recoverycustody.FinalizeRequestDigest(request)
	if err != nil {
		t.Fatalf("FinalizeRequestDigest() error = %v", err)
	}
	return request
}

func finalizedEvidenceFixture(config Config, expected ExpectedResolution) (FinalizedEvidence, error) {
	finalization, err := BindFinalizationRequest(expected)
	if err != nil {
		return FinalizedEvidence{}, err
	}
	checkpoint := FinalizedCheckpoint{HeadID: expected.HeadID, Generation: expected.ExpectedAntiRollbackGeneration + 1,
		PredecessorGeneration: expected.ExpectedAntiRollbackGeneration, PredecessorAnchorDigest: expected.ExpectedAnchorDigest,
		AnchorDigest: schemaTestDigest("8"), ResultHeadRootDigest: schemaTestDigest("9"),
		AnchorRef: "anchor://checkpoint/" + schemaTestDigest("8"), FinalizationDigest: finalization.FinalizationRequestDigest,
		FinalizationRef: "finalization://evidence/" + finalization.FinalizationRequestDigest,
		AnchoredAt:      time.Date(2026, 9, 2, 1, 2, 3, 0, time.UTC)}
	quorumDigest := schemaTestDigest("a")
	signatureDigest := schemaTestDigest("b")
	receipt := recoverycustody.ExternalReceipt{SchemaVersion: 1, Kind: expected.Kind, ReceiptID: "receipt-1",
		AuthorityID: config.AuthorityID, AuthorityClusterID: config.AuthorityClusterID,
		AuthorityProfile: config.AuthorityProfile, AuthorityGeneration: config.AuthorityGeneration,
		OwnerID: config.OwnerID, OwnerGeneration: config.OwnerGeneration,
		AuthorityProfileGeneration: config.AuthorityProfileGeneration, AuthorityProfileDigest: config.AuthorityProfileDigest,
		SecurityDomainID: config.SecurityDomainID, HeadID: expected.HeadID, HeadRevision: expected.ExpectedHeadRevision + 1,
		HeadRootDigest: checkpoint.ResultHeadRootDigest, OperationID: expected.Handle.OperationID,
		RequestDigest: expected.Handle.RequestDigest, BatchDigest: expected.Handle.BatchDigest,
		SecurityTimeLineageDigest: expected.SecurityTimeLineageDigest,
		SecurityTimeGeneration:    expected.ExpectedSecurityTimeGeneration,
		SecurityTimeRevision:      expected.ExpectedSecurityTimeRevision + 1,
		AntiRollbackGeneration:    checkpoint.Generation, AntiRollbackAnchorDigest: checkpoint.AnchorDigest,
		QuorumAttestationDigest: quorumDigest, QuorumAttestationRef: "quorum://fixture/" + quorumDigest,
		SignatureDigest: signatureDigest, SignatureRef: "signature://fixture/" + signatureDigest,
		AuthorityTime: checkpoint.AnchoredAt, RetentionPolicyDigest: config.RetentionPolicyDigest,
		RetentionUntil: checkpoint.AnchoredAt.Add(24 * time.Hour), GCNotBeforeAnchorGeneration: checkpoint.Generation}
	receipt, err = recoverycustody.FinalizeReceiptDigest(receipt)
	if err != nil {
		return FinalizedEvidence{}, err
	}
	return FinalizedEvidence{Receipt: receipt, Checkpoint: checkpoint}, nil
}

func schemaTestDigest(character string) string { return strings.Repeat(character, 64) }
