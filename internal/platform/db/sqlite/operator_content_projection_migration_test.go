package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// TestOperatorContentProjectionMigrationContract 校验C1b元数据表的严格边界。
func TestOperatorContentProjectionMigrationContract(t *testing.T) {
	db := openMigrationTestDB(t)
	db.SetMaxOpenConns(1)
	enableContentForeignKeys(t, db)
	if err := RunMigrations(context.Background(), db, copyMigrationsThroughVersion(t, "migrations", 141)); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"orchestration_operator_content_objects", "orchestration_operator_content_refs"} {
		var ddl string
		if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&ddl); err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(strings.TrimSpace(ddl), ") STRICT") {
			t.Fatalf("%s is not STRICT", table)
		}
	}
	for _, name := range []string{
		"orchestration_operator_content_objects_current", "orchestration_operator_content_objects_promote",
		"orchestration_operator_content_refs_current", "orchestration_operator_content_refs_no_update",
	} {
		assertProjectionTrigger(t, db, name)
	}
}

func assertProjectionTrigger(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='trigger' AND name=?`, name).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("missing projection trigger %s", name)
	}
}
