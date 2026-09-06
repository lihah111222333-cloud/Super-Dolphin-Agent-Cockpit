package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// TestContentSourceUnionMigrationContract 只验证C1-0 schema边界；reservation
// producer、sqlc owner与CONTENT读写接线由后续C1-1测试覆盖。
func TestContentSourceUnionMigrationContract(t *testing.T) {
	db := openMigrationTestDB(t)
	db.SetMaxOpenConns(1)
	enableContentForeignKeys(t, db)
	if err := RunMigrations(context.Background(), db, copyMigrationsThroughVersion(t, "migrations", 139)); err != nil {
		t.Fatal(err)
	}
	var strict string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='orchestration_content_source_union'`).Scan(&strict); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(strings.TrimSpace(strict), ") STRICT") {
		t.Fatalf("union table is not STRICT: %s", strict)
	}
	assertSourceUnionSchemaObject(t, db, "index", "orchestration_content_source_union_operator_idx")
	assertSourceUnionSchemaObject(t, db, "trigger", "orchestration_content_source_union_operator_current")
	assertSourceUnionSchemaObject(t, db, "trigger", "orchestration_content_source_union_no_update")
	assertSourceUnionSchemaObject(t, db, "trigger", "orchestration_content_source_union_no_delete")
	var fkCount int
	if err := db.QueryRow(`SELECT count(*) FROM pragma_foreign_key_list('orchestration_content_source_union') WHERE "table"='orchestration_content_ingest_reservations'`).Scan(&fkCount); err != nil {
		t.Fatal(err)
	}
	if fkCount != 1 {
		t.Fatalf("union reservation foreign keys=%d, want 1", fkCount)
	}
}

func assertSourceUnionSchemaObject(t *testing.T, db *sql.DB, kind, name string) {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type=? AND name=?`, kind, name).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("missing %s %s", kind, name)
	}
}
