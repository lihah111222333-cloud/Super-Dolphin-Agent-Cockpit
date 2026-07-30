package gate

import (
	"strings"
	"testing"
	"time"
)

func TestValidateRemoteCIPhaseTimingsRejectsAmbiguousEvents(t *testing.T) {
	timing := RemoteCIPhaseTiming{
		Phase:          "cache.parent_exact",
		StartedAt:      time.Unix(1, 0).UTC(),
		DurationMillis: 12,
		Outcome:        RemoteCIPhaseOutcomeSucceeded,
		WorkloadCount:  3,
		CacheHitCount:  2,
		CacheMissCount: 1,
	}
	if err := validateRemoteCIPhaseTimings([]RemoteCIPhaseTiming{timing}); err != nil {
		t.Fatalf("valid phase timing rejected: %v", err)
	}
	timing.Phase = "cache parent exact"
	if err := validateRemoteCIPhaseTimings([]RemoteCIPhaseTiming{timing}); err == nil ||
		!strings.Contains(err.Error(), "not canonical") {
		t.Fatalf("invalid phase timing error = %v", err)
	}
}
