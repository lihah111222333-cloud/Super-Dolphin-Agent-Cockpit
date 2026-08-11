package gate

import (
	"database/sql"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestRecordLocalWorkloadPassBatchCollisionFieldGuard proves that every
// persisted local-authority field is covered by either the homomorphic
// collision key or a one-column collision drift.  The schema is the producer
// field source: a new persisted column cannot be silently omitted from this
// guard.
func TestRecordLocalWorkloadPassBatchCollisionFieldGuard(t *testing.T) {
	store, batch, _ := localPassAuthorityFixture(t)
	if err := store.RecordLocalWorkloadPassBatch(batch); err != nil {
		t.Fatalf("homomorphic local PASS rewrite: %v", err)
	}

	for _, table := range []string{
		"ci_local_workload_origins",
		"ci_local_workload_executions",
		"ci_local_workload_pass_evidence",
	} {
		t.Run(table, func(t *testing.T) {
			assertLocalAuthorityCollisionFieldGuard(t, table)
		})
	}
}

func assertLocalAuthorityCollisionFieldGuard(t *testing.T, table string) {
	t.Helper()
	store, _, _ := localPassAuthorityFixture(t)
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	persisted := localAuthoritySchemaColumns(t, database, table)
	produced := localAuthorityProducedColumns(t, database, table)
	assertLocalAuthoritySchemaProducerParity(t, persisted, produced)
	keyColumns := localAuthorityPrimaryKeyColumns(t, database, table)
	driftColumns := localAuthorityColumnDifference(produced, keyColumns)
	if len(driftColumns) == 0 {
		t.Fatal("schema has no non-key collision fields to guard")
	}
	covered := append(append([]string(nil), keyColumns...), driftColumns...)
	if missing := localAuthorityColumnDifference(produced, covered); len(missing) != 0 {
		t.Fatalf("persisted producer fields missing collision coverage: %v", missing)
	}
	for _, column := range driftColumns {
		t.Run(column, func(t *testing.T) {
			assertLocalAuthorityCollisionDriftRejected(t, table, column)
		})
	}
}

func assertLocalAuthoritySchemaProducerParity(t *testing.T, persisted, produced []string) {
	t.Helper()
	if missing := localAuthorityColumnDifference(persisted, produced); len(missing) != 0 {
		t.Fatalf("schema fields absent from the plain INSERT producer: %v", missing)
	}
	if stale := localAuthorityColumnDifference(produced, persisted); len(stale) != 0 {
		t.Fatalf("plain INSERT producer has fields absent from schema: %v", stale)
	}
}

func assertLocalAuthorityCollisionDriftRejected(t *testing.T, table, column string) {
	t.Helper()
	store, batch, _ := localPassAuthorityFixture(t)
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	localAuthorityEnableUnconstrainedDrift(t, database)
	localAuthorityDriftColumn(t, database, table, column, batch)
	if err := store.RecordLocalWorkloadPassBatch(batch); err == nil {
		t.Fatalf("%s.%s collision drift rewrote authority evidence", table, column)
	}
}

func localAuthorityEnableUnconstrainedDrift(t *testing.T, database *sql.DB) {
	t.Helper()
	database.SetMaxOpenConns(1)
	if _, err := database.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatalf("disable foreign keys for collision drift: %v", err)
	}
	if _, err := database.Exec(`PRAGMA ignore_check_constraints = ON`); err != nil {
		t.Fatalf("disable checks for collision drift: %v", err)
	}
}

// TestRecordLocalWorkloadPassBatchRollsBackAfterEvidenceCollision verifies the
// insertion order is still atomic when the final evidence insert collides with
// an earlier local authority row.
func TestRecordLocalWorkloadPassBatchRollsBackAfterEvidenceCollision(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	_, fixture, _ := localPassAuthorityFixture(t)
	entryOne := localPassAuthorityEntry(t, fixture.Entries[0], localPassAuthorityExpandedFixtureWorkload(t, FrontendPreflightTargetCriticalGuards), "first")
	entryTwo := localPassAuthorityEntry(t, fixture.Entries[0], localPassAuthorityExpandedFixtureWorkload(t, FrontendPreflightTargetTurnContractVerify), "second")
	preexisting := localPassAuthorityBatch(t, "local-preexisting-second", []LocalWorkloadPassEntry{entryTwo})
	if err := store.RecordLocalWorkloadPassBatch(preexisting); err != nil {
		t.Fatalf("record preexisting second entry: %v", err)
	}

	database := openWorkloadPassDatabase(t, store)
	before := localAuthorityTablesSnapshot(t, database)
	database.Close()

	candidate := localPassAuthorityBatch(t, "local-rollback-candidate", []LocalWorkloadPassEntry{entryOne, entryTwo})
	if err := store.RecordLocalWorkloadPassBatch(candidate); err == nil {
		t.Fatal("evidence collision rewrote local PASS authority")
	} else if !strings.Contains(err.Error(), "insert local workload PASS evidence") {
		t.Fatalf("candidate error = %v, want final evidence collision", err)
	}

	database = openWorkloadPassDatabase(t, store)
	defer database.Close()
	assertLocalAuthorityRowCount(t, database, "ci_local_workload_origins", `run_id = ?`, candidate.Origin.RunID, 0)
	assertLocalAuthorityRowCount(t, database, "ci_local_workload_executions", `run_id = ? AND workload_id = ?`, candidate.Origin.RunID, entryOne.Identity.WorkloadID, 0)
	assertLocalAuthorityRowCount(t, database, "ci_local_workload_pass_evidence", `identity_digest = ? AND local_generation = ?`, entryOne.Identity.IdentityDigest, fmt.Sprint(candidate.Origin.LocalGeneration), 0)
	if after := localAuthorityTablesSnapshot(t, database); !reflect.DeepEqual(after, before) {
		t.Fatalf("evidence collision changed preexisting local authority rows:\n before=%v\n after=%v", before, after)
	}
}

func localPassAuthorityExpandedFixtureWorkload(t *testing.T, target string) Workload {
	t.Helper()
	workload, err := expandedTargetWorkload(GateIDFrontendPreflight, workloadTargetFrontendGuard, target)
	if err != nil {
		t.Fatal(err)
	}
	return workload
}

func localPassAuthorityEntry(t *testing.T, template LocalWorkloadPassEntry, workload Workload, suffix string) LocalWorkloadPassEntry {
	t.Helper()
	workloadID := GateID(workload.ID)
	executionDigest := WorkloadPassExecutionDigest(workload)
	identity := template.Identity
	identity.WorkloadID = workloadID
	identity.ExecutionDigest = executionDigest
	identity.InputDigest = digestForWorkloadPass("local-input-" + suffix)
	identity.IdentityDigest = workloadPassIdentityDigest(t, identity)
	execution := template.Execution
	execution.GateID = workloadID
	execution.ShardIdentity = "local/canonical/" + suffix
	execution.ArgvDigest = executionDigest
	execution.StartedAt = execution.StartedAt.Add(time.Second)
	execution.CompletedAt = execution.CompletedAt.Add(time.Second)
	log := PlainTextLog("local canonical workload passed " + suffix)
	execution.Log = log
	execution.LogDigest = digestPlanLog(log)
	return LocalWorkloadPassEntry{Identity: identity, Environment: template.Environment, Execution: execution}
}

func localPassAuthorityBatch(t *testing.T, runID string, entries []LocalWorkloadPassEntry) LocalWorkloadPassBatch {
	t.Helper()
	if len(entries) == 0 {
		t.Fatal("local authority batch needs entries")
	}
	origin := localPassTestOrigin()
	origin.RunID = runID
	var err error
	origin.HostContextDigest, err = LocalWorkloadPassHostContextDigest(entries[0].Environment)
	if err != nil {
		t.Fatal(err)
	}
	origin.ProjectionDigest, err = LocalWorkloadPassProjectionDigest(origin, entries)
	if err != nil {
		t.Fatal(err)
	}
	return LocalWorkloadPassBatch{Origin: origin, Entries: entries}
}

func localAuthoritySchemaColumns(t *testing.T, database interface {
	Query(string, ...any) (*sql.Rows, error)
}, table string) []string {
	t.Helper()
	rows, err := database.Query("PRAGMA table_info(" + localAuthorityQuotedIdentifier(table) + ")")
	if err != nil {
		t.Fatalf("read %s schema: %v", table, err)
	}
	defer rows.Close()
	columns := make([]string, 0)
	for rows.Next() {
		var ordinal int
		var name, columnType string
		var required, key int
		var defaultValue any
		if err := rows.Scan(&ordinal, &name, &columnType, &required, &defaultValue, &key); err != nil {
			t.Fatalf("scan %s schema: %v", table, err)
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read %s schema rows: %v", table, err)
	}
	sort.Strings(columns)
	return columns
}

func localAuthorityPrimaryKeyColumns(t *testing.T, database interface {
	Query(string, ...any) (*sql.Rows, error)
}, table string) []string {
	t.Helper()
	rows, err := database.Query("PRAGMA table_info(" + localAuthorityQuotedIdentifier(table) + ")")
	if err != nil {
		t.Fatalf("read %s primary key schema: %v", table, err)
	}
	defer rows.Close()
	columns := make([]string, 0)
	for rows.Next() {
		var ordinal int
		var name, columnType string
		var required, key int
		var defaultValue any
		if err := rows.Scan(&ordinal, &name, &columnType, &required, &defaultValue, &key); err != nil {
			t.Fatalf("scan %s primary key schema: %v", table, err)
		}
		if key > 0 {
			columns = append(columns, name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read %s primary key rows: %v", table, err)
	}
	sort.Strings(columns)
	return columns
}

func localAuthorityProducedColumns(t *testing.T, database interface {
	Query(string, ...any) (*sql.Rows, error)
}, table string) []string {
	t.Helper()
	rows, err := database.Query("SELECT * FROM " + localAuthorityQuotedIdentifier(table) + " LIMIT 1")
	if err != nil {
		t.Fatalf("read %s plain INSERT projection: %v", table, err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatalf("read %s plain INSERT projection columns: %v", table, err)
	}
	if !rows.Next() {
		t.Fatalf("%s has no row from the plain INSERT producer", table)
	}
	values := make([]any, len(columns))
	pointers := make([]any, len(columns))
	for index := range values {
		pointers[index] = &values[index]
	}
	if err := rows.Scan(pointers...); err != nil {
		t.Fatalf("scan %s plain INSERT projection: %v", table, err)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read %s plain INSERT projection rows: %v", table, err)
	}
	sort.Strings(columns)
	return columns
}

func localAuthorityColumnDifference(producer, covered []string) []string {
	set := make(map[string]struct{}, len(covered))
	for _, column := range covered {
		set[column] = struct{}{}
	}
	missing := make([]string, 0)
	for _, column := range producer {
		if _, ok := set[column]; !ok {
			missing = append(missing, column)
		}
	}
	return missing
}

func localAuthorityDriftColumn(t *testing.T, database interface {
	Exec(string, ...any) (sql.Result, error)
}, table, column string, batch LocalWorkloadPassBatch) {
	t.Helper()
	where, args, err := localAuthorityCollisionWhere(table, batch)
	if err != nil {
		t.Fatal(err)
	}
	query := "UPDATE " + localAuthorityQuotedIdentifier(table) + " SET " + localAuthorityQuotedIdentifier(column) + " = ? WHERE " + where
	queryArgs := append([]any{"collision-drift-" + column}, args...)
	result, err := database.Exec(query, queryArgs...)
	if err != nil {
		t.Fatalf("drift %s.%s: %v", table, column, err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		t.Fatalf("count %s.%s drift: %v", table, column, err)
	}
	if changed != 1 {
		t.Fatalf("%s.%s drift rows = %d, want 1", table, column, changed)
	}
}

func localAuthorityCollisionWhere(table string, batch LocalWorkloadPassBatch) (string, []any, error) {
	entry := batch.Entries[0]
	switch table {
	case "ci_local_workload_origins":
		return `run_id = ?`, []any{batch.Origin.RunID}, nil
	case "ci_local_workload_executions":
		return `run_id = ? AND workload_id = ?`, []any{batch.Origin.RunID, entry.Identity.WorkloadID}, nil
	case "ci_local_workload_pass_evidence":
		return `identity_digest = ? AND local_generation = ?`, []any{entry.Identity.IdentityDigest, fmt.Sprint(batch.Origin.LocalGeneration)}, nil
	default:
		return "", nil, fmt.Errorf("unsupported local authority table %q", table)
	}
}

func localAuthorityQuotedIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func localAuthorityTablesSnapshot(t *testing.T, database interface {
	Query(string, ...any) (*sql.Rows, error)
}) map[string][][]string {
	t.Helper()
	result := make(map[string][][]string)
	for _, table := range []string{"ci_local_workload_origins", "ci_local_workload_executions", "ci_local_workload_pass_evidence"} {
		rows, err := database.Query("SELECT * FROM " + localAuthorityQuotedIdentifier(table))
		if err != nil {
			t.Fatalf("snapshot %s: %v", table, err)
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatalf("snapshot %s columns: %v", table, err)
		}
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for index := range values {
				pointers[index] = &values[index]
			}
			if err := rows.Scan(pointers...); err != nil {
				rows.Close()
				t.Fatalf("snapshot %s row: %v", table, err)
			}
			row := make([]string, len(values))
			for index, value := range values {
				row[index] = fmt.Sprintf("%T:%v", value, value)
			}
			result[table] = append(result[table], row)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatalf("snapshot %s rows: %v", table, err)
		}
		if err := rows.Close(); err != nil {
			t.Fatalf("close %s snapshot: %v", table, err)
		}
	}
	return result
}

func assertLocalAuthorityRowCount(t *testing.T, database interface {
	QueryRow(string, ...any) *sql.Row
}, table, predicate string, arguments ...any) {
	t.Helper()
	if len(arguments) == 0 {
		t.Fatal("local authority row count requires arguments and expected count")
	}
	want, ok := arguments[len(arguments)-1].(int)
	if !ok {
		t.Fatalf("local authority row count expected value %T is not int", arguments[len(arguments)-1])
	}
	var got int
	if err := database.QueryRow("SELECT COUNT(*) FROM "+localAuthorityQuotedIdentifier(table)+" WHERE "+predicate, arguments[:len(arguments)-1]...).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s count = %d, want %d", table, got, want)
	}
}
