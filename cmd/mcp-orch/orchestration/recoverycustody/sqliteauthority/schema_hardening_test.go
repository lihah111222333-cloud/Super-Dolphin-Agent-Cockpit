package sqliteauthority

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody"
	platformsqlite "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
)

func TestSQLiteAuthoritySchemaHardening(t *testing.T) {
	t.Run("open_rejects_rewritten_schema", testOpenRejectsRewrittenSchema)
	t.Run("resolution_terminal_transition_preserves_checkpoint", testResolutionTerminalTransitionPreservesCheckpoint)
	t.Run("terminal_receipt_requires_exact_anchor_binding", testTerminalReceiptRequiresExactAnchorBinding)
	t.Run("head_completion_requires_checkpoint_anchor_ref", testHeadCompletionRequiresCheckpointAnchorRef)
	t.Run("persisted_receipt_identity_recheck", testPersistedReceiptIdentityRecheck)
	t.Run("all_contract_digests_reject_noncanonical_values", testAllContractDigestsRejectNoncanonicalValues)
	t.Run("schema_digest_columns_have_exact_guards", testSchemaDigestColumnsHaveExactGuards)
	t.Run("strict_json_rejects_noncanonical_unknown_and_trailing", testStrictJSONRejectsNoncanonicalUnknownAndTrailing)
}

func testPersistedReceiptIdentityRecheck(t *testing.T) {
	config := authorityTestConfig(t, "persisted-receipt-identity")
	if err := Bootstrap(t.Context(), config, acceptingLease{}); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	finalizer := &evidenceFinalizerFixture{config: config}
	authority, err := OpenAuthority(t.Context(), config, acceptingLease{}, finalizer, acceptingReceiptVerifier{})
	if err != nil {
		t.Fatalf("OpenAuthority() error = %v", err)
	}
	t.Cleanup(func() {
		if err := authority.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	pending, err := authority.Submit(t.Context(), schemaTestRequest(t))
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	resolution := ResolutionRequest{SchemaVersion: AuthoritySchemaVersion, ResolutionID: "resolution-persisted-identity",
		Handle: pending.Handle, Kind: recoverycustody.ReceiptAvailability, FinalizationOperationID: "finalization-persisted-identity"}
	resolution, err = FinalizeResolutionRequestDigest(resolution)
	if err != nil {
		t.Fatalf("FinalizeResolutionRequestDigest() error = %v", err)
	}
	if _, err := authority.Resolve(t.Context(), resolution); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	// Simulate a process reopening the same DB with drifted configured identity. The
	// permissive fixture verifier must not turn the persisted receipt into authority.
	authority.config.AuthorityID += "-drifted"
	if _, err := authority.Query(t.Context(), pending.Handle); !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("Query() error = %v, want ErrSchemaMismatch", err)
	}
}

func testOpenRejectsRewrittenSchema(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *sql.DB)
	}{
		{name: "same_name_trigger_sql", mutate: func(t *testing.T, db *sql.DB) {
			hardeningExec(t, db, "DROP TRIGGER submissions_no_delete")
			hardeningExec(t, db, `CREATE TRIGGER submissions_no_delete BEFORE DELETE ON submissions
BEGIN SELECT RAISE(ABORT, 'rewritten submissions trigger'); END`)
		}},
		{name: "same_name_index_sql", mutate: func(t *testing.T, db *sql.DB) {
			hardeningExec(t, db, "DROP INDEX resolutions_one_active_per_head")
			hardeningExec(t, db, `CREATE UNIQUE INDEX resolutions_one_active_per_head
ON resolutions(head_id) WHERE state IN ('active_resolution')`)
		}},
		{name: "table_constraint_sql", mutate: hardeningRewriteSubmissionConstraint},
		{name: "missing_schema_object", mutate: func(t *testing.T, db *sql.DB) {
			hardeningExec(t, db, "DROP TRIGGER authority_meta_no_delete")
		}},
		{name: "stale_schema_digest", mutate: hardeningStaleStoredSchemaDigest},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := authorityTestConfig(t, test.name)
			if err := Bootstrap(t.Context(), config, acceptingLease{}); err != nil {
				t.Fatalf("Bootstrap() error = %v", err)
			}
			db, err := platformsqlite.Open(t.Context(), platformsqlite.OpenOptions{Path: config.Path})
			if err != nil {
				t.Fatalf("open schema mutation fixture: %v", err)
			}
			test.mutate(t, db)
			if err := db.Close(); err != nil {
				t.Fatalf("close schema mutation fixture: %v", err)
			}
			assertHardeningOpenSchemaMismatch(t, config)
		})
	}
}

func hardeningRewriteSubmissionConstraint(t *testing.T, db *sql.DB) {
	t.Helper()
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatalf("acquire schema rewrite connection: %v", err)
	}
	defer conn.Close()
	var createSQL string
	if err := conn.QueryRowContext(t.Context(), "SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'submissions'").Scan(&createSQL); err != nil {
		t.Fatalf("read submissions table SQL: %v", err)
	}
	oldConstraint := "status IN ('pending', 'available', 'aborted')"
	newConstraint := "status IN ('pending', 'available', 'aborted', 'forged')"
	mutated := strings.Replace(createSQL, oldConstraint, newConstraint, 1)
	if mutated == createSQL {
		t.Fatalf("submissions table SQL does not contain %q", oldConstraint)
	}
	var schemaVersion int64
	if err := conn.QueryRowContext(t.Context(), "PRAGMA schema_version").Scan(&schemaVersion); err != nil {
		t.Fatalf("read schema_version: %v", err)
	}
	if _, err := conn.ExecContext(t.Context(), "PRAGMA writable_schema = ON"); err != nil {
		t.Fatalf("enable writable_schema: %v", err)
	}
	if _, err := conn.ExecContext(t.Context(), "UPDATE sqlite_master SET sql = ? WHERE type = 'table' AND name = 'submissions'", mutated); err != nil {
		t.Fatalf("rewrite submissions table SQL: %v", err)
	}
	if _, err := conn.ExecContext(t.Context(), "PRAGMA writable_schema = OFF"); err != nil {
		t.Fatalf("disable writable_schema: %v", err)
	}
	if _, err := conn.ExecContext(t.Context(), fmt.Sprintf("PRAGMA schema_version = %d", schemaVersion+1)); err != nil {
		t.Fatalf("advance schema_version: %v", err)
	}
}

func hardeningStaleStoredSchemaDigest(t *testing.T, db *sql.DB) {
	t.Helper()
	var triggerSQL string
	if err := db.QueryRow("SELECT sql FROM sqlite_master WHERE type = 'trigger' AND name = 'authority_schema_no_update'").Scan(&triggerSQL); err != nil {
		t.Fatalf("read authority_schema_no_update SQL: %v", err)
	}
	hardeningExec(t, db, "DROP TRIGGER authority_schema_no_update")
	hardeningExec(t, db, "UPDATE authority_schema SET schema_digest = ? WHERE singleton = 1", strings.Repeat("0", 64))
	hardeningExec(t, db, triggerSQL)
}

func assertHardeningOpenSchemaMismatch(t *testing.T, config Config) {
	t.Helper()
	authority, err := OpenAuthority(t.Context(), config, acceptingLease{}, inertFinalizer{}, acceptingReceiptVerifier{})
	if authority != nil {
		_ = authority.Close()
	}
	if authority != nil || !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("OpenAuthority() = (%v, %v), want nil ErrSchemaMismatch", authority, err)
	}
}

func testResolutionTerminalTransitionPreservesCheckpoint(t *testing.T) {
	fixture := newHardeningAnchoredFixture(t, nil)
	tests := []struct {
		name       string
		assignment string
		argument   any
	}{
		{name: "checkpoint_generation", assignment: "checkpoint_generation = ?", argument: fixture.evidence.Checkpoint.Generation + 1},
		{name: "checkpoint_anchor_digest", assignment: "checkpoint_anchor_digest = ?", argument: schemaTestDigest("c")},
		{name: "result_head_root_digest", assignment: "result_head_root_digest = ?", argument: schemaTestDigest("d")},
		{name: "finalization_digest", assignment: "finalization_digest = ?", argument: schemaTestDigest("e")},
		{name: "finalized_at", assignment: "finalized_at = ?", argument: "2026-09-03T00:00:00Z"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			statement := "UPDATE resolutions SET state = 'terminal', " + test.assignment + ", updated_at = ? WHERE resolution_id = ?"
			_, err := fixture.authority.db.ExecContext(t.Context(), statement, test.argument, "2026-09-03T00:00:01Z", fixture.expected.ResolutionID)
			if err == nil {
				t.Fatalf("anchored -> terminal changed %s", test.name)
			}
		})
	}
	result, err := fixture.authority.db.ExecContext(t.Context(),
		"UPDATE resolutions SET state = 'terminal', updated_at = ? WHERE resolution_id = ?",
		"2026-09-03T00:00:01Z", fixture.expected.ResolutionID)
	if err != nil {
		t.Fatalf("exact anchored -> terminal transition error = %v", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		t.Fatalf("exact anchored -> terminal rows = (%d, %v), want 1", rows, err)
	}
}

func testTerminalReceiptRequiresExactAnchorBinding(t *testing.T) {
	tests := []struct {
		name                   string
		mutateEvidence         func(*FinalizedEvidence)
		mutateStoredCheckpoint func(*FinalizedCheckpoint)
	}{
		{name: "kind", mutateEvidence: func(e *FinalizedEvidence) { e.Receipt.Kind = recoverycustody.ReceiptAbort }},
		{name: "head_id", mutateEvidence: func(e *FinalizedEvidence) { e.Receipt.HeadID = "forged-head" }},
		{name: "head_revision", mutateEvidence: func(e *FinalizedEvidence) { e.Receipt.HeadRevision++ }},
		{name: "security_domain", mutateEvidence: func(e *FinalizedEvidence) { e.Receipt.SecurityDomainID = "forged-domain" }},
		{name: "authority_profile", mutateEvidence: func(e *FinalizedEvidence) { e.Receipt.AuthorityProfile = "forged-profile" }},
		{name: "anti_rollback_generation", mutateEvidence: func(e *FinalizedEvidence) { e.Receipt.AntiRollbackGeneration++ }},
		{name: "checkpoint_generation", mutateEvidence: func(e *FinalizedEvidence) { e.Checkpoint.Generation++ }},
		{name: "anchor_digest", mutateEvidence: func(e *FinalizedEvidence) { e.Receipt.AntiRollbackAnchorDigest = schemaTestDigest("c") }},
		{name: "head_root_digest", mutateEvidence: func(e *FinalizedEvidence) { e.Receipt.HeadRootDigest = schemaTestDigest("d") }},
		{name: "finalization_digest", mutateStoredCheckpoint: func(c *FinalizedCheckpoint) { c.FinalizationDigest = schemaTestDigest("e") }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newHardeningAnchoredFixture(t, test.mutateStoredCheckpoint)
			evidence := fixture.evidence
			if test.mutateEvidence != nil {
				test.mutateEvidence(&evidence)
			}
			if err := hardeningInsertTerminalReceipt(t, fixture, evidence); err == nil {
				t.Fatalf("terminal receipt accepted mismatched %s", test.name)
			}
		})
	}

	t.Run("exact_binding", func(t *testing.T) {
		fixture := newHardeningAnchoredFixture(t, nil)
		if err := hardeningInsertTerminalReceipt(t, fixture, fixture.evidence); err != nil {
			t.Fatalf("exact terminal receipt insert error = %v", err)
		}
	})
}

func testHeadCompletionRequiresCheckpointAnchorRef(t *testing.T) {
	fixture := newHardeningAnchoredFixture(t, nil)
	if err := hardeningInsertTerminalReceipt(t, fixture, fixture.evidence); err != nil {
		t.Fatalf("insert exact terminal receipt: %v", err)
	}
	forged := fixture.evidence
	forged.Checkpoint.AnchorRef += "/forged"
	err := withRawImmediate(t.Context(), fixture.authority.db, func(conn *sql.Conn) error {
		return fixture.authority.advanceHead(t.Context(), conn, fixture.expected, forged)
	})
	if err == nil {
		t.Fatal("head completion accepted mismatched checkpoint anchor_ref")
	}
	err = withRawImmediate(t.Context(), fixture.authority.db, func(conn *sql.Conn) error {
		return fixture.authority.advanceHead(t.Context(), conn, fixture.expected, fixture.evidence)
	})
	if err != nil {
		t.Fatalf("head completion with exact anchor_ref error = %v", err)
	}
	var got string
	if err := fixture.authority.db.QueryRowContext(t.Context(), `SELECT anti_rollback_attestation_ref FROM authority_heads
WHERE security_domain_id = ? AND authority_profile = ?`, fixture.config.SecurityDomainID, fixture.config.AuthorityProfile).Scan(&got); err != nil {
		t.Fatalf("read completed head anchor_ref: %v", err)
	}
	if got != fixture.evidence.Checkpoint.AnchorRef {
		t.Fatalf("completed head anchor_ref = %q, want %q", got, fixture.evidence.Checkpoint.AnchorRef)
	}
}

func testAllContractDigestsRejectNoncanonicalValues(t *testing.T) {
	config := authorityTestConfig(t, "digest-contracts")
	request := schemaTestRequest(t)
	handle, err := recoverycustody.NewSubmissionHandle(request)
	if err != nil {
		t.Fatalf("NewSubmissionHandle() error = %v", err)
	}
	resolution, err := FinalizeResolutionRequestDigest(ResolutionRequest{SchemaVersion: AuthoritySchemaVersion,
		ResolutionID: "resolution-digest-contract", Handle: handle, Kind: recoverycustody.ReceiptAvailability,
		FinalizationOperationID: "finalization-digest-contract"})
	if err != nil {
		t.Fatalf("FinalizeResolutionRequestDigest() error = %v", err)
	}
	expected, err := BindExpectedResolutionDigest(ExpectedResolution{ResolutionRequest: resolution, HeadID: config.HeadID, ExpectedHeadRevision: 0,
		ExpectedHeadRootDigest: config.InitialHeadRootDigest, ExpectedAntiRollbackGeneration: config.AntiRollbackGeneration,
		ExpectedAnchorDigest: config.AntiRollbackAnchorDigest, SecurityTimeLineageDigest: config.SecurityTimeLineageDigest,
		ExpectedSecurityTimeGeneration: config.SecurityTimeGeneration, ExpectedSecurityTimeRevision: config.SecurityTimeRevision})
	if err != nil {
		t.Fatalf("BindExpectedResolutionDigest() error = %v", err)
	}
	evidence, err := finalizedEvidenceFixture(config, expected)
	if err != nil {
		t.Fatalf("finalizedEvidenceFixture() error = %v", err)
	}

	assertHardeningDigestFieldsRejected(t, "config", config, func(value Config) error { return value.Validate() })
	assertHardeningDigestFieldsRejected(t, "submission_request", request, func(value recoverycustody.SubmissionRequest) error { return value.Validate() })
	assertHardeningDigestFieldsRejected(t, "submission_handle", handle, func(value recoverycustody.SubmissionHandle) error { return value.Validate() })
	assertHardeningDigestFieldsRejected(t, "resolution", resolution, func(value ResolutionRequest) error { return value.Validate() })
	assertHardeningDigestFieldsRejected(t, "expected_resolution", expected, func(value ExpectedResolution) error { return value.Validate() })
	assertHardeningDigestFieldsRejected(t, "finalized_evidence", evidence, func(value FinalizedEvidence) error { return value.Validate(expected) })
}

func testSchemaDigestColumnsHaveExactGuards(t *testing.T) {
	config := authorityTestConfig(t, "schema-digest-constraints")
	db, err := platformsqlite.Open(t.Context(), platformsqlite.OpenOptions{Path: config.Path})
	if err != nil {
		t.Fatalf("open schema digest constraint fixture: %v", err)
	}
	defer db.Close()
	count := 0
	for _, line := range strings.Split(authoritySchemaSQL, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 || fields[1] != "TEXT" || !strings.HasSuffix(fields[0], "_digest") {
			continue
		}
		count++
		column := fields[0]
		for _, guard := range []string{
			"length(" + column + ") = 64",
			"instr(" + column + ", char(0)) = 0",
			column + " NOT GLOB '*[^0-9a-f]*'",
		} {
			if !strings.Contains(line, guard) {
				t.Errorf("digest column %s is missing guard %q", column, guard)
			}
		}
		t.Run(fmt.Sprintf("%03d_%s", count, column), func(t *testing.T) {
			assertHardeningDigestColumnRejects(t, db, strings.TrimSuffix(strings.TrimSpace(line), ","), column)
		})
	}
	if count == 0 {
		t.Fatal("authority schema contains no digest columns")
	}
}

func assertHardeningDigestColumnRejects(t *testing.T, db *sql.DB, definition, column string) {
	t.Helper()
	hardeningExec(t, db, "CREATE TABLE digest_constraint_case ("+definition+") STRICT")
	t.Cleanup(func() {
		if _, err := db.Exec("DROP TABLE IF EXISTS digest_constraint_case"); err != nil {
			t.Errorf("drop digest constraint case: %v", err)
		}
	})
	for _, invalid := range []struct {
		name  string
		value string
	}{
		{name: "tail_nul", value: strings.Repeat("a", 64) + "\x00"},
		{name: "uppercase", value: strings.Repeat("A", 64)},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			if _, err := db.ExecContext(t.Context(), "INSERT INTO digest_constraint_case("+column+") VALUES(?)", invalid.value); err == nil {
				t.Fatalf("digest column %s accepted %s", column, invalid.name)
			}
		})
	}
	if _, err := db.ExecContext(t.Context(), "DROP TABLE digest_constraint_case"); err != nil {
		t.Fatalf("drop digest constraint case: %v", err)
	}
}

func testStrictJSONRejectsNoncanonicalUnknownAndTrailing(t *testing.T) {
	request := schemaTestRequest(t)
	canonical, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal canonical request: %v", err)
	}
	var decoded recoverycustody.SubmissionRequest
	if err := strictJSON(string(canonical), &decoded); err != nil {
		t.Fatalf("strictJSON(canonical) error = %v", err)
	}
	tests := []struct {
		name    string
		encoded string
	}{
		{name: "noncanonical", encoded: "\n" + string(canonical)},
		{name: "unknown_field", encoded: strings.TrimSuffix(string(canonical), "}") + `,"unknown":true}`},
		{name: "trailing_value", encoded: string(canonical) + `{}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var destination recoverycustody.SubmissionRequest
			if err := strictJSON(test.encoded, &destination); err == nil {
				t.Fatalf("strictJSON() accepted %s JSON", test.name)
			}
		})
	}
}

type hardeningAnchoredFixture struct {
	authority *Authority
	config    Config
	expected  ExpectedResolution
	evidence  FinalizedEvidence
}

func newHardeningAnchoredFixture(t *testing.T, mutateStoredCheckpoint func(*FinalizedCheckpoint)) hardeningAnchoredFixture {
	t.Helper()
	config, authority := openHardeningAuthority(t)
	expected, evidence := prepareHardeningResolution(t, authority, config)
	installHardeningAnchor(t, authority, expected, evidence, mutateStoredCheckpoint)
	return hardeningAnchoredFixture{authority: authority, config: config, expected: expected, evidence: evidence}
}

func openHardeningAuthority(t *testing.T) (Config, *Authority) {
	t.Helper()
	config := authorityTestConfig(t, "anchored")
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
	return config, authority
}

func prepareHardeningResolution(t *testing.T, authority *Authority, config Config) (ExpectedResolution, FinalizedEvidence) {
	t.Helper()
	pending, err := authority.Submit(t.Context(), schemaTestRequest(t))
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	resolution, err := FinalizeResolutionRequestDigest(ResolutionRequest{SchemaVersion: AuthoritySchemaVersion,
		ResolutionID: "resolution-hardening", Handle: pending.Handle, Kind: recoverycustody.ReceiptAvailability,
		FinalizationOperationID: "finalization-hardening"})
	if err != nil {
		t.Fatalf("FinalizeResolutionRequestDigest() error = %v", err)
	}
	expected, created, terminal, err := authority.prepareResolution(t.Context(), resolution)
	if err != nil || !created || terminal {
		t.Fatalf("prepareResolution() = (%+v, %v, %v, %v), want created active resolution", expected, created, terminal, err)
	}
	evidence, err := finalizedEvidenceFixture(config, expected)
	if err != nil {
		t.Fatalf("finalizedEvidenceFixture() error = %v", err)
	}
	return expected, evidence
}

func installHardeningAnchor(
	t *testing.T,
	authority *Authority,
	expected ExpectedResolution,
	evidence FinalizedEvidence,
	mutateStoredCheckpoint func(*FinalizedCheckpoint),
) {
	t.Helper()
	storedCheckpoint := evidence.Checkpoint
	if mutateStoredCheckpoint != nil {
		mutateStoredCheckpoint(&storedCheckpoint)
	}
	now := "2026-09-02T01:02:04Z"
	err := withRawImmediate(t.Context(), authority.db, func(conn *sql.Conn) error {
		if err := anchorExpectedResolution(t.Context(), conn, expected, evidence.Checkpoint, now); err != nil {
			return err
		}
		return authority.insertCheckpoint(t.Context(), conn, expected, storedCheckpoint)
	})
	if err != nil {
		t.Fatalf("prepare anchored fixture: %v", err)
	}
}

func hardeningInsertTerminalReceipt(t *testing.T, fixture hardeningAnchoredFixture, evidence FinalizedEvidence) error {
	t.Helper()
	receiptJSON, err := json.Marshal(evidence.Receipt)
	if err != nil {
		return fmt.Errorf("marshal terminal receipt: %w", err)
	}
	return withRawImmediate(t.Context(), fixture.authority.db, func(conn *sql.Conn) error {
		_, err := conn.ExecContext(t.Context(), insertTerminalReceiptSQL,
			terminalReceiptInsertArgs(fixture.expected, evidence, string(receiptJSON), "2026-09-02T01:02:05Z")...)
		return err
	})
}

func hardeningExec(t *testing.T, db *sql.DB, statement string, arguments ...any) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), statement, arguments...); err != nil {
		t.Fatalf("execute schema fixture mutation: %v", err)
	}
}

func assertHardeningDigestFieldsRejected[T any](t *testing.T, group string, valid T, validate func(T) error) {
	t.Helper()
	paths := hardeningDigestFieldPaths(reflect.TypeOf(valid), nil, "")
	if len(paths) == 0 {
		t.Fatalf("%s contains no digest fields", group)
	}
	invalidValues := []struct {
		name  string
		value string
	}{
		{name: "tail_nul", value: strings.Repeat("a", 64) + "\x00"},
		{name: "uppercase", value: strings.Repeat("A", 64)},
	}
	for _, path := range paths {
		for _, invalid := range invalidValues {
			name := group + "/" + path.name + "/" + invalid.name
			t.Run(name, func(t *testing.T) {
				mutated := valid
				field := reflect.ValueOf(&mutated).Elem().FieldByIndex(path.index)
				field.SetString(invalid.value)
				if err := validate(mutated); err == nil {
					t.Fatalf("%s accepted %s digest", path.name, invalid.name)
				}
			})
		}
	}
}

type hardeningDigestFieldPath struct {
	name  string
	index []int
}

func hardeningDigestFieldPaths(valueType reflect.Type, prefix []int, namePrefix string) []hardeningDigestFieldPath {
	var paths []hardeningDigestFieldPath
	for index := 0; index < valueType.NumField(); index++ {
		field := valueType.Field(index)
		path := append(append([]int(nil), prefix...), index)
		name := field.Name
		if namePrefix != "" {
			name = namePrefix + "." + name
		}
		if field.Type.Kind() == reflect.String && strings.HasSuffix(field.Name, "Digest") {
			paths = append(paths, hardeningDigestFieldPath{name: name, index: path})
			continue
		}
		if field.Type.Kind() == reflect.Struct && field.PkgPath == "" {
			paths = append(paths, hardeningDigestFieldPaths(field.Type, path, name)...)
		}
	}
	return paths
}
