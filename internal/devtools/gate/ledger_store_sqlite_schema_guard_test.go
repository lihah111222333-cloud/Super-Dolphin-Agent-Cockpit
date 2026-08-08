package gate

import (
	"database/sql"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
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
	contract := sqliteProjectionContractForTable(t, "ci_gate_executions")
	columns := make([]string, 0, len(contract.columns)+len(contract.synthetic))
	for _, column := range contract.columns {
		columns = append(columns, column)
	}
	for column := range contract.synthetic {
		columns = append(columns, column)
	}
	delete(contract.columns, "TestTimings")
	err := validateSQLiteProjectionContract(contract, columns)
	if err == nil || !strings.Contains(err.Error(), "TestTimings") {
		t.Fatalf("field guard error = %v", err)
	}
}

func TestDurationLedgerSQLiteFieldGuardFailsWhenProducerMappingIsStale(t *testing.T) {
	contract := sqliteProjectionContractForTable(t, "ci_workload_executions")
	columns := make([]string, 0, len(contract.columns)+len(contract.synthetic)+1)
	for _, column := range contract.columns {
		columns = append(columns, column)
	}
	for column := range contract.synthetic {
		columns = append(columns, column)
	}
	contract.columns["RetiredTestTimings"] = "retired_test_timings_json"
	columns = append(columns, "retired_test_timings_json")
	err := validateSQLiteProjectionContract(contract, columns)
	if err == nil || !strings.Contains(err.Error(), "stale producer mapping RetiredTestTimings") {
		t.Fatalf("field guard error = %v", err)
	}
}

func TestDurationLedgerSQLiteTimingWarningFieldGuardRejectsMissingEvidenceFields(t *testing.T) {
	for _, field := range []string{"EvidenceKind", "EvidenceStartedAt", "EvidenceDurationMS"} {
		t.Run(field, func(t *testing.T) {
			contract := sqliteProjectionContractForTable(t, cicontract.LiveTimingWarningsTable)
			columns, _ := expectedSQLiteProjectionColumns(contract)
			delete(contract.columns, field)
			err := validateSQLiteProjectionContract(contract, columns)
			if err == nil || !strings.Contains(err.Error(), field) {
				t.Fatalf("timing warning field guard error = %v, want %s rejection", err, field)
			}
		})
	}
}

func TestDurationLedgerSQLiteTimingWarningFieldGuardRejectsStaleProviderStart(t *testing.T) {
	contract := sqliteProjectionContractForTable(t, cicontract.LiveTimingWarningsTable)
	columns, _ := expectedSQLiteProjectionColumns(contract)
	contract.columns["ProviderStartedAt"] = "provider_started_at_unix_ms"
	columns = append(columns, "provider_started_at_unix_ms")
	err := validateSQLiteProjectionContract(contract, columns)
	if err == nil || !strings.Contains(err.Error(), "ProviderStartedAt") {
		t.Fatalf("timing warning stale field guard error = %v", err)
	}
}

func sqliteProjectionContractForTable(t *testing.T, table string) sqliteProjectionContract {
	t.Helper()
	for _, contract := range sqliteProjectionContracts() {
		if contract.table == table {
			return contract
		}
	}
	t.Fatalf("projection contract for table %q is not registered", table)
	return sqliteProjectionContract{}
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
			assertSQLiteQueryPlan(t, database, test.query, test.expectedAccess, test.rejectFullTableScan, test.args...)
		})
	}
}

func TestDurationLedgerSQLiteCreatesRequiredQueryIndexes(t *testing.T) {
	database := openInitializedDurationLedgerSQLiteForTest(t)
	assertDurationLedgerSQLiteIndexExpectations(t, database, sqliteRequiredIndexExpectations())
	assertDurationLedgerSQLiteRetiredIndexes(t, database)
}

func openInitializedDurationLedgerSQLiteForTest(t *testing.T) *sql.DB {
	t.Helper()
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func sqliteRequiredIndexExpectations() []sqliteIndexExpectation {
	return []sqliteIndexExpectation{
		{
			table: "ci_catalog_observations", name: "idx_ci_catalog_observations_catalog_order",
			columns: []sqliteIndexColumnExpectation{
				{name: "catalog_digest"}, {name: "observed_at_unix_ms", descending: true},
				{name: "source_tree_sha"}, {name: "entrypoint"}, {name: "profile"}, {name: "accepted_generation"},
			},
		},
		{
			table: "ci_catalog_observations", name: "idx_ci_catalog_observations_retention",
			columns: []sqliteIndexColumnExpectation{{name: "accepted_generation"}, {name: "catalog_digest"}},
		},
		{
			table: "remote_ci_calibration_checkpoints", name: "idx_remote_ci_calibration_checkpoints_retention",
			columns: []sqliteIndexColumnExpectation{{name: "accepted_generation"}, {name: "identity"}},
		},
		{
			table: "ci_workload_pass_evidence", name: "idx_ci_workload_pass_evidence_origin_job",
			columns: []sqliteIndexColumnExpectation{{name: "origin_job_id"}},
		},
		{
			table: "ci_workload_pass_evidence", name: "idx_ci_workload_pass_evidence_retention",
			columns: []sqliteIndexColumnExpectation{{name: "accepted_generation"}, {name: "identity_digest"}},
		},
		{
			table: "ci_workload_executions", name: "idx_ci_workload_executions_shard_fk",
			columns: []sqliteIndexColumnExpectation{{name: "job_id"}, {name: "shard_identity"}},
		},
		{
			table: cicontract.TimingObservationsTable, name: "idx_ci_timing_observations_compile_group",
			columns: []sqliteIndexColumnExpectation{{name: "job_id"}, {name: "scope"}, {name: "shard_identity"}, {name: "compile_group_id"}, {name: "compile_artifact_key"}, {name: "phase"}},
		},
		{
			table: cicontract.CompileTimingObservationsTable, name: "idx_ci_compile_timing_lookup",
			columns: []sqliteIndexColumnExpectation{{name: "package_target"}, {name: "semantic_key"}, {name: "platform"}, {name: "runner_identity_digest"}, {name: "toolchain_digest"}, {name: "execution_mode"}, {name: "resource_class_id"}, {name: "resource_cpu"}, {name: "resource_memory_gib"}, {name: "job_id"}},
		},
		{
			table: cicontract.CompileTimingObservationsTable, name: "idx_ci_compile_timing_job",
			columns: []sqliteIndexColumnExpectation{{name: "job_id"}, {name: "id"}},
		},
	}
}

func assertDurationLedgerSQLiteIndexExpectations(t *testing.T, database *sql.DB, expectations []sqliteIndexExpectation) {
	t.Helper()
	for _, expectation := range expectations {
		t.Run(expectation.table+"/"+expectation.name, func(t *testing.T) {
			indexes := sqliteIndexListForTest(t, database, expectation.table)
			index, ok := indexes[expectation.name]
			if !ok {
				t.Fatalf("PRAGMA index_list(%q) omitted canonical index", expectation.table)
			}
			if index.unique != 0 || index.origin != "c" || index.partial != expectation.partial {
				t.Fatalf("PRAGMA index_list(%q) entry = %#v, want non-unique canonical index", expectation.table, index)
			}
			actualColumns := sqliteIndexColumnsForTest(t, database, expectation.name)
			if !slices.Equal(actualColumns, expectation.columns) {
				t.Fatalf("PRAGMA index_xinfo(%q) = %#v, want %#v", expectation.name, actualColumns, expectation.columns)
			}
		})
	}
}

func assertDurationLedgerSQLiteRetiredIndexes(t *testing.T, database *sql.DB) {
	t.Helper()
	if indexes := sqliteIndexListForTest(t, database, "ci_workload_pass_evidence"); indexes["idx_ci_workload_pass_evidence_lookup"].name != "" {
		t.Fatal("retired duplicate workload pass evidence lookup index is present")
	}
	for _, retired := range []struct {
		table string
		name  string
	}{
		{table: "ci_runs", name: "idx_ci_runs_reusable_pass"},
		{table: "ci_workload_pass_evidence", name: "idx_ci_workload_pass_evidence_migration"},
	} {
		if indexes := sqliteIndexListForTest(t, database, retired.table); indexes[retired.name].name != "" {
			t.Fatalf("retired index %s is present", retired.name)
		}
	}
}

func TestDurationLedgerSQLiteRejectsRetiredObjects(t *testing.T) {
	database := openInitializedDurationLedgerSQLiteForTest(t)
	assertDurationLedgerSQLiteObjectsAbsent(t, database, []string{
		"ci_workload_pass_evidence_aliases",
		"ci_schema_migrations",
		"idx_ci_runs_reusable_pass",
		"idx_ci_workload_pass_evidence_migration",
		"duration_ledger_raw_events",
		"idx_duration_ledger_raw_events_recorded_at",
		"duration_ledger_raw_events_no_update",
		"duration_ledger_raw_events_no_delete",
	})
	var autoVacuum int
	if err := database.QueryRow(`PRAGMA auto_vacuum`).Scan(&autoVacuum); err != nil {
		t.Fatal(err)
	}
	if autoVacuum != 1 {
		t.Fatalf("auto_vacuum = %d, want FULL(1)", autoVacuum)
	}
}

func assertDurationLedgerSQLiteObjectsAbsent(t *testing.T, database *sql.DB, names []string) {
	t.Helper()
	for _, name := range names {
		var count int
		if err := database.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = ?`, name).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("retired SQLite object %q is present", name)
		}
	}
}

type sqliteIndexListEntry struct {
	name    string
	unique  int
	origin  string
	partial int
}

type sqliteIndexColumnExpectation struct {
	name       string
	descending bool
}

type sqliteIndexExpectation struct {
	table   string
	name    string
	columns []sqliteIndexColumnExpectation
	partial int
}

func sqliteIndexListForTest(t *testing.T, database *sql.DB, table string) map[string]sqliteIndexListEntry {
	t.Helper()
	rows, err := database.Query(fmt.Sprintf("PRAGMA index_list(%q)", table))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	indexes := map[string]sqliteIndexListEntry{}
	for rows.Next() {
		var (
			seq   int
			entry sqliteIndexListEntry
		)
		if err := rows.Scan(&seq, &entry.name, &entry.unique, &entry.origin, &entry.partial); err != nil {
			t.Fatal(err)
		}
		indexes[entry.name] = entry
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return indexes
}

func sqliteIndexColumnsForTest(t *testing.T, database *sql.DB, index string) []sqliteIndexColumnExpectation {
	t.Helper()
	rows, err := database.Query(fmt.Sprintf("PRAGMA index_xinfo(%q)", index))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var columns []sqliteIndexColumnExpectation
	for rows.Next() {
		var (
			seqno, cid, desc, key int
			name, collation       sql.NullString
		)
		if err := rows.Scan(&seqno, &cid, &name, &desc, &collation, &key); err != nil {
			t.Fatal(err)
		}
		if key == 1 {
			if !name.Valid {
				t.Fatalf("PRAGMA index_xinfo(%q) returned unnamed key column", index)
			}
			columns = append(columns, sqliteIndexColumnExpectation{name: name.String, descending: desc == 1})
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return columns
}

func TestDurationLedgerSQLiteCascadeForeignKeysHaveLeadingIndexes(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	tables := sqliteSchemaTablesForTest(t, database)
	for _, table := range tables {
		foreignKeys := sqliteCascadeForeignKeysForTest(t, database, table)
		for _, foreignKey := range foreignKeys {
			indexes := sqliteIndexListForTest(t, database, table)
			indexed := false
			for indexName := range indexes {
				columns := sqliteIndexColumnsForTest(t, database, indexName)
				if sqliteIndexHasLeadingColumns(columns, foreignKey.columns) {
					indexed = true
					break
				}
			}
			if !indexed {
				t.Fatalf("ON DELETE CASCADE %s(%v) -> %s(%v) has no leading child-key index", table, foreignKey.columns, foreignKey.parentTable, foreignKey.parentColumns)
			}
		}
	}
}

func TestDurationLedgerSQLiteRetentionDeletesUseGenerationLeadingIndexes(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`CREATE TEMP TABLE retention_stale_generations (accepted_generation TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	for _, binding := range cicontract.RetentionRootBindings() {
		indexName := map[string]string{
			"duration_samples":                  "idx_duration_samples_retention",
			"duration_shard_overheads":          "idx_duration_shard_overheads_retention",
			"duration_shard_overhead_samples":   "idx_duration_shard_overhead_samples_retention",
			"ci_catalog_observations":           "idx_ci_catalog_observations_retention",
			"ci_runs":                           "idx_ci_runs_accepted_generation",
			"ci_workload_pass_evidence":         "idx_ci_workload_pass_evidence_retention",
			"remote_ci_calibration_checkpoints": "idx_remote_ci_calibration_checkpoints_retention",
		}[binding.Table]
		if indexName == "" {
			t.Fatalf("retention root %q has no canonical generation-leading index expectation", binding.Table)
		}
		t.Run(binding.Table, func(t *testing.T) {
			assertSQLiteQueryPlan(t, database, retentionDeleteQuery(binding), []string{"USING COVERING INDEX " + indexName}, []string{binding.Table})
		})
	}
}

type sqliteCascadeForeignKey struct {
	parentTable   string
	columns       []sqliteIndexColumnExpectation
	parentColumns []string
}

func sqliteSchemaTablesForTest(t *testing.T, database *sql.DB) []string {
	t.Helper()
	rows, err := database.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, table)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return tables
}

func sqliteCascadeForeignKeysForTest(t *testing.T, database *sql.DB, table string) []sqliteCascadeForeignKey {
	t.Helper()
	rows, err := database.Query(fmt.Sprintf("PRAGMA foreign_key_list(%q)", table))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type foreignKeyPart struct {
		parentTable, from, to, onDelete string
		seq, id                         int
	}
	parts := map[int][]foreignKeyPart{}
	for rows.Next() {
		var part foreignKeyPart
		var onUpdate, match string
		if err := rows.Scan(&part.id, &part.seq, &part.parentTable, &part.from, &part.to, &onUpdate, &part.onDelete, &match); err != nil {
			t.Fatal(err)
		}
		if strings.EqualFold(part.onDelete, "CASCADE") {
			parts[part.id] = append(parts[part.id], part)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	var foreignKeys []sqliteCascadeForeignKey
	for _, group := range parts {
		slices.SortFunc(group, func(left, right foreignKeyPart) int { return left.seq - right.seq })
		foreignKey := sqliteCascadeForeignKey{parentTable: group[0].parentTable}
		for _, part := range group {
			foreignKey.columns = append(foreignKey.columns, sqliteIndexColumnExpectation{name: part.from})
			foreignKey.parentColumns = append(foreignKey.parentColumns, part.to)
		}
		foreignKeys = append(foreignKeys, foreignKey)
	}
	return foreignKeys
}

func sqliteIndexHasLeadingColumns(index, foreignKey []sqliteIndexColumnExpectation) bool {
	if len(index) < len(foreignKey) {
		return false
	}
	for position, column := range foreignKey {
		if index[position] != column {
			return false
		}
	}
	return true
}

type sqliteQueryPlanTest struct {
	name                string
	query               string
	expectedAccess      []string
	rejectFullTableScan []string
	args                []any
}

func sqliteRequiredQueryPlans() []sqliteQueryPlanTest {
	return []sqliteQueryPlanTest{
		{
			name: "duration planning",
			query: `SELECT workload_id, command_digest, input_digest, execution_mode, resource_class_id, resource_cpu, resource_memory_gib,
				COALESCE(SUM(CASE WHEN succeeded = 1 THEN duration_ms ELSE 0 END), 0),
				COALESCE(SUM(CASE WHEN succeeded = 1 THEN 1 ELSE 0 END), 0),
				COALESCE(MAX(CASE WHEN succeeded = 0 THEN duration_ms ELSE 0 END), 0)
				FROM duration_samples
				WHERE execution_mode = ? AND platform = ? AND runner = ? AND toolchain = ?
				GROUP BY workload_id, command_digest, input_digest, execution_mode, resource_class_id, resource_cpu, resource_memory_gib`,
			expectedAccess:      []string{"USING INDEX idx_duration_samples_planning"},
			rejectFullTableScan: []string{"duration_samples"},
			args:                []any{DurationExecutionModeNormal, "linux/amd64", "runner", "toolchain"},
		},
		{
			name: "catalog observation loader",
			query: `SELECT source_tree_sha, entrypoint, profile, accepted_generation, observed_at_unix_ms
				FROM ci_catalog_observations
				WHERE catalog_digest = ?
				ORDER BY observed_at_unix_ms DESC, source_tree_sha, entrypoint, profile`,
			expectedAccess:      []string{"USING COVERING INDEX idx_ci_catalog_observations_catalog_order"},
			rejectFullTableScan: []string{"ci_catalog_observations"},
			args:                []any{"catalog"},
		},
		{
			name: "shard cascade foreign key",
			query: `SELECT workload_id
				FROM ci_workload_executions
				WHERE job_id = ? AND shard_identity = ?`,
			expectedAccess:      []string{"USING INDEX idx_ci_workload_executions_shard_fk"},
			rejectFullTableScan: []string{"ci_workload_executions"},
			args:                []any{"job", "shard"},
		},
		{
			name: "accepted generation retention",
			query: `SELECT accepted_generation
				FROM ci_workload_pass_evidence`,
			expectedAccess:      []string{"USING COVERING INDEX idx_ci_workload_pass_evidence_retention"},
			rejectFullTableScan: []string{"ci_workload_pass_evidence"},
		},
		{
			name: "shard overhead sample loader",
			query: `SELECT accepted_generation, provenance_digest, job_id, shard_identity,
				total_started_at_unix_ms, total_completed_at_unix_ms,
				workload_envelope_start_unix_ms, workload_envelope_end_unix_ms,
				accounted_duration_ms, accounted_interval_count, overhead_ms
				FROM duration_shard_overhead_samples
				WHERE accepted_generation = ? AND provenance_digest = ?
				ORDER BY job_id, shard_identity`,
			expectedAccess:      []string{"USING INDEX idx_duration_shard_overhead_samples_planning"},
			rejectFullTableScan: []string{"duration_shard_overhead_samples"},
			args:                []any{"1", "sha256:" + strings.Repeat("a", 64)},
		},
		{
			name: "compile group timing loader",
			query: `SELECT compile_group_id, compile_artifact_key, phase, duration_ms
				FROM ci_timing_observations
				WHERE job_id = ? AND scope = ? AND shard_identity = ?
				ORDER BY compile_group_id, compile_artifact_key, phase`,
			expectedAccess:      []string{"USING INDEX idx_ci_timing_observations_compile_group"},
			rejectFullTableScan: []string{"ci_timing_observations"},
			args:                []any{"job", string(cicontract.TimingScopeCompileGroup), "shard"},
		},
		{
			name: "compile timing run loader",
			query: `SELECT observations.package_target
				FROM ci_compile_timing_observations AS observations
				INNER JOIN ci_runs AS runs ON runs.job_id = observations.job_id
				WHERE observations.job_id = ? AND observations.measurement = 'measured'
					AND observations.aggregation = 'raw' AND runs.status = 'passed'
					AND runs.authoritative = 1 AND runs.cleanup_complete = 1`,
			expectedAccess:      []string{"USING INDEX idx_ci_compile_timing_job"},
			rejectFullTableScan: []string{"ci_compile_timing_observations"},
			args:                []any{"job"},
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
	for field := range producer.Fields() {
		if nested, ok := expand[field.Name]; ok {
			for nestedField := range nested.Fields() {
				fields = append(fields, field.Name+"."+nestedField.Name)
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
	expected := cicontract.SQLAuthoritySchemaTables()
	if !slices.Equal(actual, expected) {
		t.Fatalf("SQLite tables = %v, want %v", actual, expected)
	}
}

func assertSQLiteQueryPlan(
	t *testing.T,
	database *sql.DB,
	query string,
	expectedAccess []string,
	rejectFullTableScan []string,
	args ...any,
) {
	t.Helper()
	details := sqliteQueryPlanDetails(t, database, query, args...)
	plan := strings.Join(details, "\n")
	if strings.Contains(plan, "USE TEMP B-TREE") {
		t.Fatalf("query plan = %v, contains a temporary sort", details)
	}
	assertSQLiteQueryPlanAccess(t, details, expectedAccess)
	assertSQLiteQueryPlanNoFullTableScan(t, details, rejectFullTableScan)
}

// sqliteQueryPlanDetails 读取指定生产 SQL 的 SQLite 查询计划明细。
func sqliteQueryPlanDetails(t *testing.T, database *sql.DB, query string, args ...any) []string {
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
	return details
}

// assertSQLiteQueryPlanAccess 拒绝缺少目标索引访问方式的查询计划。
func assertSQLiteQueryPlanAccess(t *testing.T, details, expectedAccess []string) {
	t.Helper()
	plan := strings.Join(details, "\n")
	for _, access := range expectedAccess {
		if !strings.Contains(plan, access) {
			t.Fatalf("query plan = %v, want access %q", details, access)
		}
	}
}

// assertSQLiteQueryPlanNoFullTableScan 拒绝目标热路径的无索引全表扫描。
func assertSQLiteQueryPlanNoFullTableScan(t *testing.T, details, tables []string) {
	t.Helper()
	for _, table := range tables {
		for _, detail := range details {
			if (detail == "SCAN "+table || strings.HasPrefix(detail, "SCAN "+table+" ")) && !strings.Contains(detail, "USING") {
				t.Fatalf("query plan = %v, contains full table scan of %s", details, table)
			}
		}
	}
}

func errorsJoinStrings(values []string) error {
	return fmt.Errorf("%s", strings.Join(values, "; "))
}
