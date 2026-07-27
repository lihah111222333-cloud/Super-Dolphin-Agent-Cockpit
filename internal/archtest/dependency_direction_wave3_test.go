package archtest_test

import "testing"

func TestWave3DependencyDirection(t *testing.T) {
	root := repoRoot(t)
	t.Run("module_cannot_import_outer_implementations", func(t *testing.T) {
		assertCanonicalBoundaryRule(t, root, "module_no_outer_implementation_imports")
	})
	t.Run("unified_cannot_import_concrete_provider", func(t *testing.T) {
		assertCanonicalBoundaryRule(t, root, "provider_unified_no_concrete_imports")
	})
	t.Run("module_siblings_cannot_import_concrete_peers", func(t *testing.T) {
		assertCanonicalBoundaryRule(t, root, "module_horizontal_deep_import")
	})
}
