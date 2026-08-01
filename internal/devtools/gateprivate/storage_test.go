package gateprivate

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestOpenSQLiteUsesSingleWriterAndCreatesDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coordinator.db")
	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if db.Stats().MaxOpenConnections != 1 {
		t.Fatalf("max open connections = %d, want 1", db.Stats().MaxOpenConnections)
	}
	if _, err := db.ExecContext(context.Background(), "CREATE TABLE gateprivate_test (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	if err := RestrictOwnerFile(path); err != nil {
		t.Fatal(err)
	}
}

func TestRetrySQLiteWriteReturnsNonSQLiteErrorImmediately(t *testing.T) {
	want := errors.New("stop")
	calls := 0
	err := RetrySQLiteWrite(context.Background(), 3, func() error {
		calls++
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}
