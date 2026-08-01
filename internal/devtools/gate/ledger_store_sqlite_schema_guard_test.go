package gate

import (
	"database/sql"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
)

type sqliteProjectionContract struct {
	table      string
	producer   reflect.Type
	expand     map[string]reflect.Type
	columns    map[string]string
	exemptions map[string]string
	synthetic  map[string]string
}

func TestDurationLedgerSQLiteSchemaCoversQueryableProducerFields(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, contract := range sqliteProjectionContracts() {
		columns := sqliteTableColumns(t, database, contract.table)
		if err := validateSQLiteProjectionContract(contract, columns); err != nil {
			t.Errorf("%s: %v", contract.table, err)
		}
	}
	assertSQLiteSchemaTableRegistry(t, database)
}

func TestDurationLedgerSQLiteFieldGuardFailsWhenProducerMappingIsRemoved(t *testing.T) {
	contract := sqliteProjectionContracts()[0]
	columns := make([]string, 0, len(contract.columns)+len(contract.synthetic))
	for _, column := range contract.columns {
		columns = append(columns, column)
	}
	for column := range contract.synthetic {
		columns = append(columns, column)
	}
	delete(contract.columns, "CompletedAt")
	err := validateSQLiteProjectionContract(contract, columns)
	if err == nil || !strings.Contains(err.Error(), "CompletedAt") {
		t.Fatalf("field guard error = %v", err)
	}
}

func TestDurationLedgerSQLiteQueryPlansUseRequiredIndexes(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, test := range sqliteRequiredQueryPlans() {
		t.Run(test.name, func(t *testing.T) {
			assertSQLiteQueryPlanUsesIndex(t, database, test.query, test.index, test.args...)
		})
	}
}

type sqliteQueryPlanTest struct {
	name  string
	query string
	index string
	args  []any
}

func sqliteRequiredQueryPlans() []sqliteQueryPlanTest {
	return []sqliteQueryPlanTest{
		{
			name: "duration planning",
			query: `SELECT workload_id, command_digest
				FROM duration_samples INDEXED BY idx_duration_samples_planning
				WHERE platform = ? AND runner = ? AND toolchain = ?`,
			index: "idx_duration_samples_planning",
			args:  []any{"linux/amd64", "runner", "toolchain"},
		},
		{
			name:  "pass identity",
			query: `SELECT workload_id FROM ci_workload_pass_proofs WHERE identity_digest = ?`,
			index: "sqlite_autoindex_ci_workload_pass_proofs_1",
			args:  []any{"sha256:identity"},
		},
		{
			name: "fingerprint lookup",
			query: `SELECT identity_digest FROM ci_workload_fingerprints
				WHERE workload_id = ? AND input_digest = ? AND environment_digest = ?
				ORDER BY observed_at_unix_ms DESC`,
			index: "idx_ci_workload_fingerprint_lookup",
			args:  []any{"unit", "sha256:input", "sha256:environment"},
		},
		{
			name: "workload identity alias lookup",
			query: `SELECT identity_digest FROM ci_workload_identity_aliases
				WHERE workload_id = ? ORDER BY observed_at_unix_ms DESC, identity_digest`,
			index: "idx_ci_workload_identity_alias_lookup",
			args:  []any{"unit"},
		},
		{
			name: "fingerprint latest observation",
			query: `SELECT source_tree_sha FROM ci_workload_fingerprint_observations
				WHERE identity_digest = ?
				ORDER BY observed_at_unix_ms DESC, source_tree_sha DESC`,
			index: "idx_ci_workload_fingerprint_observation_latest",
			args:  []any{"sha256:identity"},
		},
		{
			name: "catalog observation",
			query: `SELECT catalog_digest FROM ci_catalog_observations
				WHERE source_tree_sha = ? AND entrypoint = ?
				ORDER BY observed_at_unix_ms DESC`,
			index: "idx_ci_catalog_observations_tree_entrypoint",
			args:  []any{"tree", "git-pre-commit"},
		},
		{
			name: "run tree status",
			query: `SELECT job_id FROM ci_runs
				WHERE source_tree_sha = ? AND status = ?
				ORDER BY completed_at_unix_ms DESC`,
			index: "idx_ci_runs_tree_status",
			args:  []any{"tree", "passed"},
		},
		{
			name: "run workload history",
			query: `SELECT job_id FROM ci_run_workloads
				WHERE workload_id = ? AND disposition = ?`,
			index: "idx_ci_run_workloads_lookup",
			args:  []any{"unit", "reused"},
		},
		{
			name: "requester run history",
			query: `SELECT job_id FROM ci_run_requesters
				WHERE requester_fingerprint = ?
				ORDER BY started_at_unix_ms DESC, job_id DESC`,
			index: "idx_ci_run_requesters_lookup",
			args:  []any{"sha256:requester"},
		},
		{
			name: "remote CI phase hotspots",
			query: `SELECT job_id, duration_ms FROM ci_run_phase_timings
				WHERE phase = ?
				ORDER BY duration_ms DESC, started_at_unix_ms DESC`,
			index: "idx_ci_run_phase_timings_hotspots",
			args:  []any{"cache.parent_prepare"},
		},
	}
}

func sqliteProjectionContracts() []sqliteProjectionContract {
	return sqliteProjectionContractsForTest()
}

func validateSQLiteProjectionContract(
	contract sqliteProjectionContract,
	actualColumns []string,
) error {
	producerFields := sqliteProducerFieldPaths(contract.producer, contract.expand)
	var problems []string
	problems = append(problems, validateSQLiteProducerCoverage(contract, producerFields)...)
	problems = append(problems, validateSQLiteProducerMappings(contract, producerFields)...)
	problems = append(problems, validateSQLiteProducerExemptions(contract, producerFields)...)
	expectedColumns, syntheticProblems := expectedSQLiteProjectionColumns(contract)
	problems = append(problems, syntheticProblems...)
	sort.Strings(expectedColumns)
	slices.Sort(actualColumns)
	if !slices.Equal(expectedColumns, actualColumns) {
		problems = append(problems, fmt.Sprintf(
			"columns=%v want=%v",
			actualColumns,
			expectedColumns,
		))
	}
	if len(problems) > 0 {
		return errorsJoinStrings(problems)
	}
	return nil
}

func validateSQLiteProducerCoverage(
	contract sqliteProjectionContract,
	producerFields []string,
) []string {
	var problems []string
	for _, field := range producerFields {
		_, mapped := contract.columns[field]
		reason, exempt := contract.exemptions[field]
		if !mapped && (!exempt || strings.TrimSpace(reason) == "") {
			problems = append(problems, "missing producer field "+field)
		}
	}
	return problems
}

func validateSQLiteProducerMappings(
	contract sqliteProjectionContract,
	producerFields []string,
) []string {
	var problems []string
	for field := range contract.columns {
		if !slices.Contains(producerFields, field) {
			problems = append(problems, "stale producer mapping "+field)
		}
	}
	return problems
}

func validateSQLiteProducerExemptions(
	contract sqliteProjectionContract,
	producerFields []string,
) []string {
	var problems []string
	for field, reason := range contract.exemptions {
		if !slices.Contains(producerFields, field) || strings.TrimSpace(reason) == "" {
			problems = append(problems, "invalid producer exemption "+field)
		}
	}
	return problems
}

func expectedSQLiteProjectionColumns(contract sqliteProjectionContract) ([]string, []string) {
	expectedColumns := make([]string, 0, len(contract.columns)+len(contract.synthetic))
	for _, column := range contract.columns {
		expectedColumns = append(expectedColumns, column)
	}
	var problems []string
	for column, reason := range contract.synthetic {
		if strings.TrimSpace(reason) == "" {
			problems = append(problems, "empty synthetic column reason "+column)
		}
		expectedColumns = append(expectedColumns, column)
	}
	return expectedColumns, problems
}

func sqliteProducerFieldPaths(
	producer reflect.Type,
	expand map[string]reflect.Type,
) []string {
	fields := make([]string, 0, producer.NumField())
	for index := 0; index < producer.NumField(); index++ {
		field := producer.Field(index)
		if nested, ok := expand[field.Name]; ok {
			for nestedIndex := 0; nestedIndex < nested.NumField(); nestedIndex++ {
				fields = append(fields, field.Name+"."+nested.Field(nestedIndex).Name)
			}
			continue
		}
		fields = append(fields, field.Name)
	}
	sort.Strings(fields)
	return fields
}

func sqliteTableColumns(t *testing.T, database *sql.DB, table string) []string {
	t.Helper()
	rows, err := database.Query(fmt.Sprintf("PRAGMA table_info(%q)", table))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var (
			cid, notNull, primaryKey int
			name, dataType           string
			defaultValue             any
		)
		if err := rows.Scan(
			&cid,
			&name,
			&dataType,
			&notNull,
			&defaultValue,
			&primaryKey,
		); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return columns
}

func assertSQLiteSchemaTableRegistry(t *testing.T, database *sql.DB) {
	t.Helper()
	rows, err := database.Query(`
		SELECT name
		FROM sqlite_master
		WHERE type = 'table' AND name NOT LIKE 'sqlite_%'
		ORDER BY name
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var actual []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		actual = append(actual, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	expected := []string{
		"ci_catalog_observations", "ci_catalog_workloads", "ci_gate_executions",
		"ci_query_meta", "ci_run_phase_timings", "ci_run_requesters", "ci_run_warnings", "ci_run_workloads", "ci_runs", "ci_schema_migrations",
		"ci_shard_workloads", "ci_shards", "ci_workload_catalogs",
		"ci_workload_fingerprint_observations", "ci_workload_fingerprints", "ci_workload_identity_aliases", "ci_workload_pass_proofs",
		"duration_calibrations", "duration_ledger_meta", "duration_samples",
	}
	if !slices.Equal(actual, expected) {
		t.Fatalf("SQLite tables = %v, want %v", actual, expected)
	}
}

func assertSQLiteQueryPlanUsesIndex(
	t *testing.T,
	database *sql.DB,
	query string,
	index string,
	args ...any,
) {
	t.Helper()
	rows, err := database.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(details, "\n"), index) {
		t.Fatalf("query plan = %v, want index %q", details, index)
	}
}

func errorsJoinStrings(values []string) error {
	return fmt.Errorf("%s", strings.Join(values, "; "))
}
