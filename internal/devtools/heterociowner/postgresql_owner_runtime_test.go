package heterociowner

import (
	"context"
	"database/sql"
	"errors"
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
	joined := strings.Join(statements, "\n")
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
	for _, forbidden := range []string{"?", "PRAGMA", "sqlite", "IF NOT EXISTS", PostgreSQLReceiptSchema} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("PostgreSQL owner DDL contains forbidden %q", forbidden)
		}
	}
}
