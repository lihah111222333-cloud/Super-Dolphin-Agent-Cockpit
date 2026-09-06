package sqliteauthority

import (
	"errors"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody"
)

// TestSubmitDifferentOperationSameIdempotencyKeyConflicts verifies that the
// idempotency unique index is a domain conflict and does not create a second row.
func TestSubmitDifferentOperationSameIdempotencyKeyConflicts(t *testing.T) {
	config := authorityTestConfig(t, "submit-idempotency-conflict")
	authority := bootstrapAndOpenOutcomeAuthority(t, config, inertFinalizer{})
	t.Cleanup(func() { closeOutcomeAuthority(t, authority) })

	first := schemaTestRequest(t)
	pending, err := authority.Submit(t.Context(), first)
	if err != nil || pending.Status != recoverycustody.StatusPending {
		t.Fatalf("first Submit() = (%+v, %v), want pending", pending, err)
	}

	second := first
	second.OperationID = "operation-2"
	second, err = recoverycustody.FinalizeRequestDigest(second)
	if err != nil {
		t.Fatalf("FinalizeRequestDigest(second) error = %v", err)
	}
	_, conflictErr := authority.Submit(t.Context(), second)
	if !errors.Is(conflictErr, recoverycustody.ErrCustodyConflict) {
		t.Fatalf("cross-operation same-key Submit() error = %v, want ErrCustodyConflict", conflictErr)
	}
	if errors.Is(conflictErr, recoverycustody.ErrCustodyUnavailable) {
		t.Fatalf("cross-operation same-key Submit() leaked unavailable classification: %v", conflictErr)
	}
	assertOutcomeRowCount(t, authority.db, "submissions", 1)

	replayed, err := authority.Submit(t.Context(), first)
	if err != nil || replayed != pending {
		t.Fatalf("exact Submit replay = (%+v, %v), want %+v", replayed, err, pending)
	}
}
