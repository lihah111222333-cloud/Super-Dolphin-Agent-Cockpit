package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// TestOperatorReservationMigrationContract 校验C1a metadata schema边界；Store
// producer与object/ref投影由后续切片验证。
func TestOperatorReservationMigrationContract(t *testing.T) {
	db := openMigrationTestDB(t)
	db.SetMaxOpenConns(1)
	enableContentForeignKeys(t, db)
	if err := RunMigrations(context.Background(), db, copyMigrationsThroughVersion(t, "migrations", 140)); err != nil {
		t.Fatal(err)
	}
	var ddl string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='orchestration_operator_reservations'`).Scan(&ddl); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(strings.TrimSpace(ddl), ") STRICT") {
		t.Fatalf("operator reservation table is not STRICT: %s", ddl)
	}
	for _, object := range []string{
		"orchestration_operator_reservations_source_guard",
		"orchestration_operator_reservations_no_update",
		"orchestration_operator_reservations_no_delete",
	} {
		assertOperatorReservationObject(t, db, "trigger", object)
	}
	var fk int
	if err := db.QueryRow(`SELECT count(*) FROM pragma_foreign_key_list('orchestration_operator_reservations') WHERE "table"='orchestration_operator_sources'`).Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 1 {
		t.Fatalf("operator source FK count=%d, want 1", fk)
	}
}

func assertOperatorReservationObject(t *testing.T, db *sql.DB, kind, name string) {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type=? AND name=?`, kind, name).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("missing %s %s", kind, name)
	}
}
