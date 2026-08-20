package heterociowner

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

func TestWithImmediateTxCommitsAndRollsBack(t *testing.T) {
	db := newSchemaTestDB(t)
	if err := EnsureSchema(context.Background(), db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	if err := WithImmediateTx(context.Background(), db, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), "INSERT INTO heteroci_owner_heads(scope, owner_id, owner_epoch, revision, journal_digest, projection_digest, updated_at_unix_milli) VALUES (?, ?, ?, ?, ?, ?, ?)", "sha256:"+strings.Repeat("a", 64), strings.Repeat("b", 64), 1, 1, testDigest("c"), testDigest("d"), 0)
		return err
	}); err != nil {
		t.Fatalf("commit immediate tx: %v", err)
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM heteroci_owner_heads").Scan(&count); err != nil || count != 1 {
		t.Fatalf("committed head count = %d, err=%v", count, err)
	}
	sentinel := errors.New("rollback")
	if err := WithImmediateTx(context.Background(), db, func(tx *sql.Tx) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("rollback error = %v, want sentinel", err)
	}
}

func testDigest(letter string) string { return "sha256:" + strings.Repeat(letter, 64) }
