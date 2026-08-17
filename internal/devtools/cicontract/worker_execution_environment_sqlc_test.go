package cicontract

import (
	"strings"
	"testing"
)

func TestCanonicalWorkerExecutionEnvironmentOwnsUniqueGOWORKOff(t *testing.T) {
	count := 0
	for _, assignment := range CanonicalWorkerExecutionEnvironment() {
		key, value, ok := strings.Cut(assignment, "=")
		if ok && strings.EqualFold(key, "GOWORK") {
			count++
			if key != "GOWORK" || value != "off" {
				t.Fatalf("non-canonical GOWORK assignment %q", assignment)
			}
		}
	}
	if count != 1 {
		t.Fatalf("canonical worker environment has %d GOWORK entries", count)
	}
}
