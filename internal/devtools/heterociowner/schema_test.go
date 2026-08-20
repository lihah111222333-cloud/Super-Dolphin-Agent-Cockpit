package heterociowner

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func TestEnsureSchemaInitializesAndIsIdempotent(t *testing.T) {
	db := newSchemaTestDB(t)
	if err := EnsureSchema(context.Background(), db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	if err := EnsureSchema(context.Background(), db); err != nil {
		t.Fatalf("EnsureSchema second call: %v", err)
	}
	var tableCount int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name LIKE 'heteroci_%'").Scan(&tableCount); err != nil {
		t.Fatalf("count owner tables: %v", err)
	}
	if tableCount != len(schemaColumns()) {
		t.Fatalf("owner table count = %d, want %d", tableCount, len(schemaColumns()))
	}
}

func TestSchemaRegistriesReturnFreshValues(t *testing.T) {
	statements := schemaStatements()
	if len(statements) == 0 {
		t.Fatal("schema statement registry is empty")
	}
	wantStatement := statements[0]
	statements[0] = "corrupted"
	if got := schemaStatements()[0]; got != wantStatement {
		t.Fatalf("private schema statement registry was mutated: got %q", got)
	}

	exported := SchemaStatements()
	exported[0] = "corrupted"
	if got := SchemaStatements()[0]; got != wantStatement {
		t.Fatalf("SchemaStatements returned shared storage: got %q", got)
	}

	columns := schemaColumns()
	headColumns := columns["heteroci_owner_heads"]
	if len(headColumns) == 0 {
		t.Fatal("owner head column registry is empty")
	}
	wantColumn := headColumns[0]
	headColumns[0] = "corrupted"
	delete(columns, "heteroci_owner_epochs")
	freshColumns := schemaColumns()
	if got := freshColumns["heteroci_owner_heads"][0]; got != wantColumn {
		t.Fatalf("schema column registry was mutated: got %q", got)
	}
	if _, ok := freshColumns["heteroci_owner_epochs"]; !ok {
		t.Fatal("schema column registry shared its table map")
	}
}

func TestEnsureSchemaRejectsUnknownAndPartialObjects(t *testing.T) {
	unknown := newSchemaTestDB(t)
	if _, err := unknown.Exec("CREATE TABLE unrelated (value TEXT)"); err != nil {
		t.Fatalf("create unrelated table: %v", err)
	}
	if err := EnsureSchema(context.Background(), unknown); err == nil {
		t.Fatal("EnsureSchema accepted unknown object")
	}

	partial := newSchemaTestDB(t)
	if _, err := partial.Exec("CREATE TABLE heteroci_owner_heads (scope TEXT PRIMARY KEY)"); err != nil {
		t.Fatalf("create partial table: %v", err)
	}
	if err := EnsureSchema(context.Background(), partial); err == nil {
		t.Fatal("EnsureSchema accepted partial known table")
	}

	missing := newSchemaTestDB(t)
	for _, statement := range SchemaStatements()[:len(SchemaStatements())-1] {
		if _, err := missing.Exec(statement); err != nil {
			t.Fatalf("create almost-complete schema: %v", err)
		}
	}
	if err := EnsureSchema(context.Background(), missing); err == nil {
		t.Fatal("EnsureSchema silently repaired missing table")
	}
}

func TestEnsureSchemaEnforcesOwnerBounds(t *testing.T) {
	db := newSchemaTestDB(t)
	if err := EnsureSchema(context.Background(), db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	_, err := db.Exec(`INSERT INTO heteroci_owner_heads(scope, owner_id, owner_epoch, revision, journal_digest, projection_digest, updated_at_unix_milli) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"sha256:"+strings.Repeat("a", 64), strings.Repeat("b", 64), 0, 1, "sha256:"+strings.Repeat("c", 64), "sha256:"+strings.Repeat("d", 64), 0)
	if err == nil {
		t.Fatal("owner head accepted zero epoch")
	}
}

func newSchemaTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open(driverName, ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}
