package eventstore

import (
	"context"
	"testing"
)

// TestEventStoreRejectsNilContext verifies ready fails before dereferencing
// a nil context on a valid read request.
func TestEventStoreRejectsNilContext(t *testing.T) {
	db := openEventTestDB(t)
	store := mustEventStore(t, db)
	var nilContext context.Context

	if _, err := store.GetEvent(nilContext, "event-nil-context"); err == nil || err.Error() != "event store context is required" {
		t.Fatalf("GetEvent(nil context) error = %v, want event store context is required", err)
	}
}
