package heterociowner

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPostgreSQLOwnerAuthorityAcquireAndCurrentFence(t *testing.T) {
	database, state := openPostgreSQLOwnerTestDB(t)
	owner, err := NewPostgreSQLOwner(context.Background(), database, validOwnerScope())
	if err != nil {
		t.Fatalf("NewPostgreSQLOwner: %v", err)
	}
	fence, err := owner.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	current, err := owner.CurrentFence(context.Background())
	if err != nil || current != fence {
		t.Fatalf("CurrentFence = %#v, %v; want %#v", current, err, fence)
	}
	t.Run("untrusted_authority", TestPostgreSQLOwnerRejectsUntrustedAuthorityBeforeMutation)
	t.Run("standby", TestPostgreSQLOwnerRejectsStandbyBeforeDDL)
	t.Run("post_construction_drift", TestPostgreSQLOwnerRejectsPostConstructionOwnershipDrift)
	t.Run("ddl", TestPostgreSQLOwnerDDLIsIndependentCanonicalBYTEASchema)
	state.mu.Lock()
	defer state.mu.Unlock()
	assertPostgreSQLOwnerTransactionEvidence(t, state, 2)
}

func assertPostgreSQLOwnerTransactionEvidence(t *testing.T, state *postgresqlOwnerTestState, minimumTransactions int) {
	t.Helper()
	if state.authorityBeforeLock || state.serializableBegins < minimumTransactions || state.advisoryLocks != state.serializableBegins {
		t.Fatalf("transaction evidence: serializable=%d locks=%d authority_before_lock=%v", state.serializableBegins, state.advisoryLocks, state.authorityBeforeLock)
	}
}

func TestPostgreSQLOwnerRejectsStandbyBeforeDDL(t *testing.T) {
	database, state := openPostgreSQLOwnerTestDB(t)
	state.inRecovery = true
	_, err := NewPostgreSQLOwner(context.Background(), database, validOwnerScope())
	if !errors.Is(err, ErrPostgreSQLOwnerTopology) {
		t.Fatalf("NewPostgreSQLOwner error = %v, want ErrPostgreSQLOwnerTopology", err)
	}
	if state.schemaStatementCount != 0 {
		t.Fatalf("standby validation executed %d DDL statements", state.schemaStatementCount)
	}
}

func TestPostgreSQLOwnerRejectsUntrustedAuthorityBeforeMutation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*postgresqlOwnerTestState)
	}{
		{name: "loopback", mutate: func(state *postgresqlOwnerTestState) { state.serverAddress = "127.0.0.1" }},
		{name: "wrong_role", mutate: func(state *postgresqlOwnerTestState) { state.currentUser = "postgres" }},
		{name: "missing_schema", mutate: func(state *postgresqlOwnerTestState) { state.schemaOwner = "" }},
		{name: "schema_version_drift", mutate: func(state *postgresqlOwnerTestState) { state.schemaVersion = "heteroci-owner-postgresql/old" }},
		{name: "missing_table", mutate: func(state *postgresqlOwnerTestState) { delete(state.tableOwners, "routing_leases") }},
		{name: "missing_index", mutate: func(state *postgresqlOwnerTestState) { delete(state.indexNames, PostgreSQLOwnerScopeRunIndex) }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			database, state := openPostgreSQLOwnerTestDB(t)
			testCase.mutate(state)
			_, err := NewPostgreSQLOwner(context.Background(), database, validOwnerScope())
			if !errors.Is(err, ErrPostgreSQLOwnerTopology) {
				t.Fatalf("NewPostgreSQLOwner error = %v, want ErrPostgreSQLOwnerTopology", err)
			}
			if len(state.data.head) != 0 || state.schemaStatementCount != 0 {
				t.Fatalf("untrusted authority mutated runtime state: %#v", state)
			}
		})
	}
}

func TestPostgreSQLOwnerRuntimeContractSurface(t *testing.T) {
	var database *sql.DB
	if _, err := NewPostgreSQLOwnerWithClock(context.Background(), database, validOwnerScope(), time.Now); err == nil {
		t.Fatal("NewPostgreSQLOwnerWithClock accepted nil database")
	}
}

func TestPostgreSQLOwnerRejectsPostConstructionOwnershipDrift(t *testing.T) {
	database, state := openPostgreSQLOwnerTestDB(t)
	owner, err := NewPostgreSQLOwner(context.Background(), database, validOwnerScope())
	if err != nil {
		t.Fatalf("NewPostgreSQLOwner: %v", err)
	}
	state.tableOwners["routing_leases"] = "postgres"
	if _, err := owner.Acquire(context.Background()); !errors.Is(err, ErrPostgreSQLOwnerTopology) {
		t.Fatalf("Acquire ownership drift error = %v, want ErrPostgreSQLOwnerTopology", err)
	}
	if len(state.data.head) != 0 || state.advisoryLocks != 0 {
		t.Fatalf("ownership drift reached authority mutation or lock: %#v", state)
	}
}

func TestPostgreSQLOwnerDDLIsIndependentCanonicalBYTEASchema(t *testing.T) {
	statements := postgresqlOwnerSchemaStatements()
	if len(statements) < 2 || statements[0] != "CREATE SCHEMA "+PostgreSQLOwnerSchema+" AUTHORIZATION "+PostgreSQLOwnerRole {
		t.Fatalf("PostgreSQL owner DDL does not create %s first: %#v", PostgreSQLOwnerSchema, statements)
	}
	joined := PostgreSQLOwnerDDL()
	additive := PostgreSQLOwnerAdditiveIndexDDL()
	if !strings.HasSuffix(joined, ";") {
		t.Fatal("PostgreSQL owner operator DDL is not statement terminated")
	}
	for _, required := range []string{
		PostgreSQLOwnerSchema + ".owner_heads",
		PostgreSQLOwnerSchema + ".owner_epochs",
		PostgreSQLOwnerSchema + ".operation_journal",
		PostgreSQLOwnerSchema + ".routing_runs",
		PostgreSQLOwnerSchema + ".routing_reservations",
		PostgreSQLOwnerSchema + ".routing_leases",
		"request_bytes BYTEA",
		"result_bytes BYTEA",
		"lease_bytes BYTEA",
		"OWNER TO " + PostgreSQLOwnerRole,
		"COMMENT ON SCHEMA " + PostgreSQLOwnerSchema + " IS '" + PostgreSQLOwnerRuntimeSchemaVersion + "'",
		"REVOKE ALL ON SCHEMA " + PostgreSQLOwnerSchema + " FROM PUBLIC",
		"REVOKE ALL ON TABLE " + PostgreSQLOwnerSchema + ".owner_heads FROM PUBLIC",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("PostgreSQL owner DDL missing %q", required)
		}
	}
	for _, forbidden := range []string{"?", "PRAGMA", "sqlite", "IF NOT EXISTS", "heteroci_receipt"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("PostgreSQL owner DDL contains forbidden %q", forbidden)
		}
	}
	assertPostgreSQLOwnerAdditiveIndexDDL(t, joined, additive)
}

func assertPostgreSQLOwnerAdditiveIndexDDL(t *testing.T, clean, additive string) {
	t.Helper()
	for _, definition := range postgresqlOwnerIndexDefinitions() {
		if strings.Count(clean, definition.statement) != 1 {
			t.Fatalf("clean owner DDL count for %q = %d, want 1", definition.name, strings.Count(clean, definition.statement))
		}
		if strings.Count(additive, definition.statement) != 1 {
			t.Fatalf("additive owner DDL count for %q = %d, want 1", definition.name, strings.Count(additive, definition.statement))
		}
	}
	for _, forbidden := range []string{"CREATE SCHEMA", "CREATE TABLE", "IF NOT EXISTS", "heteroci_receipt"} {
		if strings.Contains(additive, forbidden) {
			t.Fatalf("PostgreSQL owner additive index DDL contains forbidden %q", forbidden)
		}
	}
}

func TestPostgreSQLOwnerIndexRegistryIsMinimalAndQueryBound(t *testing.T) {
	definitions := postgresqlOwnerIndexDefinitions()
	if len(definitions) != 3 {
		t.Fatalf("explicit PostgreSQL owner indexes = %d, want 3", len(definitions))
	}
	seen := make(map[string]struct{}, len(definitions))
	for _, definition := range definitions {
		if definition.name == "" || definition.statement == "" {
			t.Fatalf("empty PostgreSQL owner index definition: %#v", definition)
		}
		if _, exists := seen[definition.name]; exists {
			t.Fatalf("duplicate PostgreSQL owner index %q", definition.name)
		}
		seen[definition.name] = struct{}{}
	}
	for _, required := range []string{
		"CREATE UNIQUE INDEX " + PostgreSQLOwnerActiveEpochIndex + " ON heteroci_owner.owner_epochs (scope) WHERE retired_at_unix_milli IS NULL",
		"CREATE INDEX " + PostgreSQLOwnerScopeRunIndex + " ON heteroci_owner.routing_runs (scope, run_id, owner_epoch)",
		"CREATE INDEX " + PostgreSQLOwnerActiveReservationIndex + " ON heteroci_owner.routing_reservations (scope, owner_epoch, run_id) WHERE state IN ('planned', 'started')",
	} {
		if !strings.Contains(PostgreSQLOwnerAdditiveIndexDDL(), required) {
			t.Fatalf("PostgreSQL owner index registry missing %q", required)
		}
	}
	for _, redundant := range []string{
		"ON heteroci_owner.owner_heads (scope)",
		"ON heteroci_owner.operation_journal (scope, operation_id)",
		"ON heteroci_owner.routing_leases (scope, owner_epoch, run_id, unit_id)",
	} {
		if strings.Contains(PostgreSQLOwnerAdditiveIndexDDL(), redundant) {
			t.Fatalf("PostgreSQL owner index registry duplicates constraint index %q", redundant)
		}
	}
}

func TestPostgreSQL14IndexExplainFixtureCoversOwnerAndReceiptPaths(t *testing.T) {
	fixture, err := os.ReadFile("postgresql_index_explain_pg14_test.sql")
	if err != nil {
		t.Fatalf("read PostgreSQL 14 EXPLAIN fixture: %v", err)
	}
	sqlText := string(fixture)
	for _, required := range []string{
		"EXPLAIN (FORMAT JSON, COSTS OFF)",
		PostgreSQLOwnerActiveEpochIndex,
		PostgreSQLOwnerScopeRunIndex,
		PostgreSQLOwnerActiveReservationIndex,
		"routing_leases_pkey",
		"batch_receipts_pkey",
		"ORDER BY owner_epoch",
		"ORDER BY unit_id",
		"state IN ('planned', 'started')",
	} {
		if !strings.Contains(sqlText, required) {
			t.Fatalf("PostgreSQL 14 EXPLAIN fixture missing %q", required)
		}
	}
	if strings.Contains(sqlText, "EXPLAIN ANALYZE") || !strings.HasSuffix(sqlText, "ROLLBACK;\n") {
		t.Fatal("PostgreSQL 14 EXPLAIN fixture may execute mutations or is not rollback bounded")
	}
}
