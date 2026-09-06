package sqliteauthority

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestWriterFenceTakeoverFencesExistingConnection(t *testing.T) {
	db := openWriterFenceTestDB(t)
	mustExecWriterFence(t, db, `CREATE TABLE mutation_rows (id INTEGER PRIMARY KEY, value TEXT NOT NULL)`)
	initial := WriterFence{SchemaVersion: WriterFenceSchemaVersion, WriterEpoch: 1, DeploymentFence: "deployment-1"}
	mustInstallWriterFence(t, db, initial)

	// Keep the original connection/token across takeover; an open RW handle is not authority.
	oldConn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatalf("acquire old connection: %v", err)
	}
	defer oldConn.Close()
	next := WriterFence{SchemaVersion: WriterFenceSchemaVersion, WriterEpoch: 2, DeploymentFence: "deployment-2"}
	mustTakeoverWriterFence(t, db, initial, next)

	mutated := false
	err = withTestWriterFenceTransaction(t.Context(), db, initial, func(conn *sql.Conn) error {
		mutated = true
		_, execErr := conn.ExecContext(t.Context(), `INSERT INTO mutation_rows(value) VALUES ('stale')`)
		return execErr
	})
	if !errors.Is(err, ErrStaleWriter) {
		t.Fatalf("stale mutation error = %v, want ErrStaleWriter", err)
	}
	if mutated {
		t.Fatal("stale writer callback executed")
	}
	assertWriterFenceRowCount(t, db, 0)

	if err := withTestWriterFenceTransaction(t.Context(), db, next, func(conn *sql.Conn) error {
		_, execErr := conn.ExecContext(t.Context(), `INSERT INTO mutation_rows(value) VALUES ('fresh')`)
		return execErr
	}); err != nil {
		t.Fatalf("successor mutation: %v", err)
	}
	assertWriterFenceRowCount(t, db, 1)
}

func TestWriterFenceRejectsMalformedOrDuplicateBootstrap(t *testing.T) {
	db := openWriterFenceTestDB(t)
	initial := WriterFence{SchemaVersion: WriterFenceSchemaVersion, WriterEpoch: 1, DeploymentFence: "deployment-1"}
	mustInstallWriterFence(t, db, initial)
	if err := withRawImmediate(t.Context(), db, func(conn *sql.Conn) error { return seedWriterFence(t.Context(), conn, initial) }); !errors.Is(err, ErrWriterFenceSchema) {
		t.Fatalf("duplicate install error = %v, want ErrWriterFenceSchema", err)
	}
	staleExpected := initial
	next := WriterFence{SchemaVersion: WriterFenceSchemaVersion, WriterEpoch: 2, DeploymentFence: "deployment-2"}
	if err := withRawImmediate(t.Context(), db, func(conn *sql.Conn) error { return takeoverWriterFence(t.Context(), conn, staleExpected, next) }); err != nil {
		t.Fatalf("first takeover: %v", err)
	}
	if err := withRawImmediate(t.Context(), db, func(conn *sql.Conn) error {
		return takeoverWriterFence(t.Context(), conn, staleExpected, WriterFence{SchemaVersion: WriterFenceSchemaVersion, WriterEpoch: 3, DeploymentFence: "deployment-3"})
	}); !errors.Is(err, ErrStaleWriter) {
		t.Fatalf("stale takeover error = %v, want ErrStaleWriter", err)
	}
	if _, err := db.ExecContext(t.Context(), `DELETE FROM authority_writer_fence WHERE singleton = 1`); err == nil {
		t.Fatal("writer fence delete unexpectedly succeeded")
	}
	if _, err := readWriterFence(t.Context(), nil); !errors.Is(err, ErrWriterFenceSchema) {
		t.Fatalf("nil read error = %v, want ErrWriterFenceSchema", err)
	}
	for _, statement := range []string{
		`UPDATE authority_writer_fence SET writer_epoch = 1 WHERE singleton = 1`,
		`UPDATE authority_writer_fence SET writer_epoch = 2, deployment_fence = 'deployment-forged' WHERE singleton = 1`,
	} {
		if _, err := db.ExecContext(t.Context(), statement); err == nil {
			t.Fatalf("writer fence guard accepted %s", statement)
		}
	}
}

func mustExecWriterFence(t *testing.T, db *sql.DB, query string) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), query); err != nil {
		t.Fatalf("writer fence SQL: %v", err)
	}
}

func mustInstallWriterFence(t *testing.T, db *sql.DB, fence WriterFence) {
	t.Helper()
	if err := withRawImmediate(t.Context(), db, func(conn *sql.Conn) error {
		if _, err := conn.ExecContext(t.Context(), authoritySchemaSQL); err != nil {
			return err
		}
		digest, err := schemaDigest(t.Context(), conn)
		if err != nil {
			return err
		}
		if _, err := conn.ExecContext(t.Context(), insertAuthoritySchemaSQL, AuthorityDatabaseSchemaRevision, digest, "2026-09-02T00:00:00Z"); err != nil {
			return err
		}
		return seedWriterFence(t.Context(), conn, fence)
	}); err != nil {
		t.Fatalf("install writer fence: %v", err)
	}
}

func mustTakeoverWriterFence(t *testing.T, db *sql.DB, expected, next WriterFence) {
	t.Helper()
	if err := withRawImmediate(t.Context(), db, func(conn *sql.Conn) error { return takeoverWriterFence(t.Context(), conn, expected, next) }); err != nil {
		t.Fatalf("takeover writer fence: %v", err)
	}
}

func withTestWriterFenceTransaction(ctx context.Context, db *sql.DB, fence WriterFence, mutate func(*sql.Conn) error) error {
	return withRawImmediate(ctx, db, func(conn *sql.Conn) error {
		if err := casWriterFence(ctx, conn, fence); err != nil {
			return err
		}
		return mutate(conn)
	})
}

func assertWriterFenceRowCount(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM mutation_rows`).Scan(&count); err != nil {
		t.Fatalf("count mutation rows: %v", err)
	}
	if count != want {
		t.Fatalf("mutation row count = %d, want %d", count, want)
	}
}

func openWriterFenceTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(2)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close sqlite: %v", err)
		}
	})
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("ping sqlite: %v", err)
	}
	return db
}
