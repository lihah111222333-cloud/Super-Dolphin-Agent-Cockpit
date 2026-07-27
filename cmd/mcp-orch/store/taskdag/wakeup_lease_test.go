package taskdag

import (
	"context"
	"strings"
	"testing"
)

func TestClaimedWakeupLeaseContextRejectsNilReceiver(t *testing.T) {
	var lease *ClaimedWakeupLease
	leaseCtx, err := lease.Context(context.Background())
	if err == nil || !strings.Contains(err.Error(), "nil receiver") {
		t.Fatalf("Context() = (%v, %v), want explicit nil receiver error", leaseCtx, err)
	}
	if leaseCtx != nil {
		t.Fatalf("Context() context = %v, want nil", leaseCtx)
	}
}
