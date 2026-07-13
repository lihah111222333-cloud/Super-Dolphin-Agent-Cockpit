package dbquery

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"
	_ "modernc.org/sqlite"
)

func TestPlaceholderReturnsZeroRowsFromTypedSQLCQuery(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close SQLite: %v", err)
		}
	})

	queries := sqlc.New(db)
	rawRows, err := queries.PlaceholderDBQuery(context.Background())
	if err != nil {
		t.Fatalf("PlaceholderDBQuery() error = %v", err)
	}
	if rawRows == nil || len(rawRows) != 0 {
		t.Fatalf("PlaceholderDBQuery() rows = %#v, want non-nil zero rows", rawRows)
	}

	rows, err := NewStore(queries, db, time.Second).Placeholder(context.Background())
	if err != nil {
		t.Fatalf("Placeholder() error = %v", err)
	}
	if rows == nil || len(rows) != 0 {
		t.Fatalf("Placeholder() rows = %#v, want non-nil zero rows", rows)
	}
}
