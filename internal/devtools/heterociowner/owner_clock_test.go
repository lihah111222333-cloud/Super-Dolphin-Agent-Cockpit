package heterociowner

import (
	"context"
	"testing"
	"time"
)

func TestNewOwnerWithClockPersistsInjectedTimestamp(t *testing.T) {
	db := newSchemaTestDB(t)
	want := time.UnixMilli(1_800_000_000_000).UTC()
	owner, err := NewOwnerWithClock(context.Background(), db, validOwnerScope(), func() time.Time { return want })
	if err != nil {
		t.Fatalf("NewOwnerWithClock: %v", err)
	}
	if _, err := owner.Acquire(context.Background()); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	var updatedAt int64
	if err := db.QueryRowContext(context.Background(), "SELECT updated_at_unix_milli FROM heteroci_owner_heads WHERE scope = ?", validOwnerScope()).Scan(&updatedAt); err != nil {
		t.Fatalf("read owner head timestamp: %v", err)
	}
	if updatedAt != want.UnixMilli() {
		t.Fatalf("updated_at_unix_milli = %d, want %d", updatedAt, want.UnixMilli())
	}
}

func TestNewOwnerWithClockRejectsNilBeforeDatabaseMutation(t *testing.T) {
	db := newSchemaTestDB(t)
	if _, err := NewOwnerWithClock(context.Background(), db, validOwnerScope(), nil); err == nil {
		t.Fatal("NewOwnerWithClock accepted nil clock")
	}
	var tableCount int
	if err := db.QueryRowContext(context.Background(), "SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name LIKE 'heteroci_%'").Scan(&tableCount); err != nil {
		t.Fatalf("inspect schema after nil clock: %v", err)
	}
	if tableCount != 0 {
		t.Fatalf("nil clock mutated %d owner tables", tableCount)
	}
}

func TestNewOwnerUsesWallClockConstructor(t *testing.T) {
	owner := newTestOwner(t)
	if owner.now == nil {
		t.Fatal("NewOwner returned nil clock")
	}
	if _, err := owner.Acquire(context.Background()); err != nil {
		t.Fatalf("NewOwner Acquire: %v", err)
	}
}
