package heterociowner

import (
	"context"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

func TestOwnerAcquireCreatesAndTakesOverFence(t *testing.T) {
	owner := newTestOwner(t)
	if _, err := owner.CurrentFence(context.Background()); err == nil {
		t.Fatal("CurrentFence accepted missing owner head")
	}
	first, second := acquireOwnerPair(t, owner)
	assertOwnerFenceProgression(t, first, second)
	assertOwnerEpochRetired(t, owner, first)
	if first.OwnerID == second.OwnerID {
		t.Fatal("takeover reused owner identity")
	}
	current, err := owner.CurrentFence(context.Background())
	if err != nil || current != second {
		t.Fatalf("CurrentFence = %#v, %v; want %#v", current, err, second)
	}
}

func assertOwnerEpochRetired(t *testing.T, owner *Owner, fence heteroci.OwnerFence) {
	t.Helper()
	var retired int64
	if err := owner.db.QueryRowContext(context.Background(), "SELECT retired_at_unix_milli FROM heteroci_owner_epochs WHERE scope = ? AND owner_epoch = ?", fence.Scope, fence.OwnerEpoch).Scan(&retired); err != nil {
		t.Fatalf("read retired owner epoch: %v", err)
	}
	if retired <= 0 {
		t.Fatalf("retired owner epoch timestamp = %d", retired)
	}
}

func newTestOwner(t *testing.T) *Owner {
	t.Helper()
	db := newSchemaTestDB(t)
	owner, err := NewOwner(context.Background(), db, validOwnerScope())
	if err != nil {
		t.Fatalf("NewOwner: %v", err)
	}
	return owner
}

func acquireOwnerPair(t *testing.T, owner *Owner) (heteroci.OwnerFence, heteroci.OwnerFence) {
	t.Helper()
	first, err := owner.Acquire(context.Background())
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	second, err := owner.Acquire(context.Background())
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	return first, second
}

func assertOwnerFenceProgression(t *testing.T, first, second heteroci.OwnerFence) {
	t.Helper()
	if first.OwnerEpoch != 1 || first.Revision != 1 || second.OwnerEpoch != 2 || second.Revision != 2 {
		t.Fatalf("fence progression = %#v then %#v", first, second)
	}
}

func TestNewOwnerRejectsInvalidInputs(t *testing.T) {
	if _, err := NewOwner(context.Background(), nil, validOwnerScope()); err == nil {
		t.Fatal("NewOwner accepted nil database")
	}
	db := newSchemaTestDB(t)
	if _, err := NewOwner(context.Background(), db, heteroci.RoutingOwnerScope("invalid")); err == nil {
		t.Fatal("NewOwner accepted invalid scope")
	}
}

func TestNewOwnerEnablesSQLiteForeignKeys(t *testing.T) {
	owner := newTestOwner(t)
	var enabled int
	if err := owner.db.QueryRowContext(context.Background(), "PRAGMA foreign_keys").Scan(&enabled); err != nil {
		t.Fatalf("read foreign_keys pragma: %v", err)
	}
	if enabled != 1 {
		t.Fatalf("foreign_keys = %d, want 1", enabled)
	}
}

func validOwnerScope() heteroci.RoutingOwnerScope {
	return heteroci.RoutingOwnerScope("sha256:" + strings.Repeat("a", 64))
}
