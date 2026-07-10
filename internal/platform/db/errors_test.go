package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

func TestWrapStoreError_NilReturnsNil(t *testing.T) {
	t.Parallel()
	if got := WrapStoreError(nil, "op", "entity"); got != nil {
		t.Fatalf("WrapStoreError(nil) = %v, want nil", got)
	}
}

func TestWrapStoreError_AlreadyWrappedPassesThrough(t *testing.T) {
	t.Parallel()
	original := &StoreError{Operation: "get", Entity: "user", Err: errors.New("boom")}
	got := WrapStoreError(original, "list", "order")
	if got != original {
		t.Fatalf("WrapStoreError(already wrapped) should return original, got %v", got)
	}
}

func TestWrapStoreError_ClassifiesNotFound(t *testing.T) {
	t.Parallel()
	wrapped := WrapStoreError(sql.ErrNoRows, "get", "user")
	if !errors.Is(wrapped, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", wrapped)
	}
}

func TestWrapStoreError_ClassifiesConflict(t *testing.T) {
	t.Parallel()
	sqliteErr := sqliteUniqueViolation(t)
	wrapped := WrapStoreError(sqliteErr, "insert", "user")
	if !errors.Is(wrapped, ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", wrapped)
	}
}

func TestWrapStoreError_ClassifiesTimeout(t *testing.T) {
	t.Parallel()
	wrapped := WrapStoreError(context.DeadlineExceeded, "list", "order")
	if !errors.Is(wrapped, ErrTimeout) {
		t.Fatalf("expected ErrTimeout, got %v", wrapped)
	}
}

func TestWrapStoreError_ClassifiesSQLiteBusy(t *testing.T) {
	t.Parallel()
	busyErr := errors.New("database is locked")
	wrapped := WrapStoreError(busyErr, "list", "order")
	if errors.Is(wrapped, ErrTimeout) {
		t.Fatalf("text-only error must not classify as SQLite busy, got %v", wrapped)
	}
}

func TestWrapStoreError_GenericError(t *testing.T) {
	t.Parallel()
	wrapped := WrapStoreError(errors.New("random"), "update", "product")
	var se *StoreError
	if !errors.As(wrapped, &se) {
		t.Fatalf("expected *StoreError, got %T", wrapped)
	}
	if se.Kind != nil {
		t.Fatalf("generic error should have nil Kind, got %v", se.Kind)
	}
}

func TestStoreError_ErrorFormatting(t *testing.T) {
	t.Parallel()
	inner := errors.New("boom")
	cases := []struct {
		name   string
		se     StoreError
		expect string
	}{
		{"both empty", StoreError{Err: inner}, "boom"},
		{"entity empty", StoreError{Operation: "get", Err: inner}, "get: boom"},
		{"operation empty", StoreError{Entity: "user", Err: inner}, "user: boom"},
		{"both present", StoreError{Operation: "get", Entity: "user", Err: inner}, "get user: boom"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := c.se.Error(); got != c.expect {
				t.Fatalf("Error() = %q, want %q", got, c.expect)
			}
		})
	}
}

func TestStoreError_Unwrap(t *testing.T) {
	t.Parallel()
	inner := errors.New("inner")
	se := &StoreError{Err: inner}
	if got := se.Unwrap(); got != inner {
		t.Fatalf("Unwrap() = %v, want %v", got, inner)
	}
}

func TestStoreError_Is_KindMatching(t *testing.T) {
	t.Parallel()
	se := &StoreError{Kind: ErrNotFound, Err: errors.New("not found")}
	if !errors.Is(se, ErrNotFound) {
		t.Fatal("StoreError with Kind=ErrNotFound should match ErrNotFound")
	}
	if errors.Is(se, ErrConflict) {
		t.Fatal("StoreError with Kind=ErrNotFound should NOT match ErrConflict")
	}
}

func TestIsNotFound(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err  error
		want bool
	}{
		{ErrNotFound, true},
		{sql.ErrNoRows, true},
		{fmt.Errorf("wrapped: %w", ErrNotFound), true},
		{errors.New("random"), false},
		{nil, false},
	}
	for _, c := range cases {
		if got := IsNotFound(c.err); got != c.want {
			t.Errorf("IsNotFound(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

func TestIsConflict(t *testing.T) {
	t.Parallel()
	if !IsConflict(ErrConflict) {
		t.Fatal("IsConflict(ErrConflict) should be true")
	}
	if !IsConflict(sqliteUniqueViolation(t)) {
		t.Fatal("IsConflict(actual SQLite UNIQUE violation) should be true")
	}
	if IsConflict(errors.New("UNIQUE constraint failed: users.email")) {
		t.Fatal("IsConflict(text-only UNIQUE error) should be false")
	}
	if IsConflict(errors.New("random")) {
		t.Fatal("IsConflict(random) should be false")
	}
}

func TestIsTimeout(t *testing.T) {
	t.Parallel()
	if !IsTimeout(ErrTimeout) {
		t.Fatal("IsTimeout(ErrTimeout) should be true")
	}
	if !IsTimeout(context.DeadlineExceeded) {
		t.Fatal("IsTimeout(DeadlineExceeded) should be true")
	}
	if IsTimeout(errors.New("database is locked")) {
		t.Fatal("IsTimeout(text-only SQLite busy) should be false")
	}
	if IsTimeout(errors.New("random")) {
		t.Fatal("IsTimeout(random) should be false")
	}
}

func TestSQLitePrimaryResultCode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		code int
		want int
	}{
		{code: sqlite3.SQLITE_BUSY | 1<<8, want: sqlite3.SQLITE_BUSY},
		{code: sqlite3.SQLITE_LOCKED | 2<<8, want: sqlite3.SQLITE_LOCKED},
		{code: sqlite3.SQLITE_CONSTRAINT_UNIQUE, want: sqlite3.SQLITE_CONSTRAINT},
	}
	for _, tc := range cases {
		if got := sqlitePrimaryResultCode(tc.code); got != tc.want {
			t.Errorf("sqlitePrimaryResultCode(%d) = %d, want %d", tc.code, got, tc.want)
		}
	}
}

func sqliteUniqueViolation(t *testing.T) error {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec("CREATE TABLE users (email TEXT UNIQUE)"); err != nil {
		t.Fatalf("create unique table: %v", err)
	}
	if _, err := db.Exec("INSERT INTO users (email) VALUES ('person@example.com')"); err != nil {
		t.Fatalf("insert first row: %v", err)
	}
	_, err = db.Exec("INSERT INTO users (email) VALUES ('person@example.com')")
	if err == nil {
		t.Fatal("duplicate insert unexpectedly succeeded")
	}
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) {
		t.Fatalf("duplicate insert error type = %T, want *sqlite.Error", err)
	}
	return err
}

func BenchmarkWrapStoreError_NotFound(b *testing.B) {
	for b.Loop() {
		WrapStoreError(sql.ErrNoRows, "get", "user")
	}
}

func BenchmarkClassifyStoreError(b *testing.B) {
	err := errors.New("random")
	for b.Loop() {
		classifyStoreError(err)
	}
}
