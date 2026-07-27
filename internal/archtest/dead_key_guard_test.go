package archtest

import "testing"

func TestFreezeRegistryIntegrity(t *testing.T) {
	t.Parallel()

	violations := freezeRegistryIntegrityViolations()
	if len(violations) == 0 {
		return
	}

	t.Fatalf("freeze registry integrity violations (%d):\n%s", len(violations), formatViolations(violations))
}
