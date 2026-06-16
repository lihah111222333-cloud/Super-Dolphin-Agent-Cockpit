package skill

import "testing"

// TestHashResolutionEnvelopeFallsBackForUnmarshalableValue verifies hash generation never panics.
func TestHashResolutionEnvelopeFallsBackForUnmarshalableValue(t *testing.T) {
	t.Parallel()

	got := hashResolutionEnvelope(map[string]any{"bad": func() {}})
	if got == "" {
		t.Fatal("hashResolutionEnvelope() = empty, want fallback hash")
	}
}
