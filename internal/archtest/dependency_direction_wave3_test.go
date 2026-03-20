package archtest_test

import "testing"

func TestWave3DependencyDirection(t *testing.T) {
	root := repoRoot(t)
	t.Run("rule11_module_turn_cannot_import_provider", func(t *testing.T) {
		if !dirExists(root, "internal/module/turn") {
			t.Skip("directory not yet created")
		}
		assertNoImportPrefixes(t, parseImportFiles(t, root, "internal/module/turn"), []string{internalPrefix("internal/provider/")})
	})
	t.Run("rule12_unified_cannot_import_concrete_provider", func(t *testing.T) {
		if !dirExists(root, "internal/provider/unified") {
			t.Skip("directory not yet created")
		}
		assertNoImportPrefixes(t, parseImportFiles(t, root, "internal/provider/unified"), []string{
			internalPrefix("internal/provider/claudecli"),
			internalPrefix("internal/provider/codexapp"),
		})
	})
}
