package identity

import (
	"context"
	"testing"
)

// TestIdentityStoreRejectsNilContext verifies ready fails before dereferencing
// a nil context on a valid read request.
func TestIdentityStoreRejectsNilContext(t *testing.T) {
	db := openIdentityTestDB(t)
	store := mustIdentityStore(t, db)
	var nilContext context.Context

	if _, err := store.Get(nilContext, "reservation-nil-context"); err == nil || err.Error() != "identity store context is required" {
		t.Fatalf("Get(nil context) error = %v, want identity store context is required", err)
	}
}
