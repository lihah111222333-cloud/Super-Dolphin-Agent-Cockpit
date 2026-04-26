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
	t.Run("rule13_module_memory_cannot_import_module_turn", func(t *testing.T) {
		// Memory sits below turn in the assembly graph (memory.Module →
		// prompt.Module → turn). AB.7 cache-root-threading needs the
		// tool-results cache root that turn computes; the lock-step copy in
		// internal/module/memory/factory.go::nestedToolReadCacheRoot keeps
		// memory free of a turn import. Future contributors who reach for
		// `import "...module/turn"` to short-circuit that copy would silently
		// reverse the dependency direction and produce an fx graph cycle.
		if !dirExists(root, "internal/module/memory") || !dirExists(root, "internal/module/turn") {
			t.Skip("directory not yet created")
		}
		assertNoImportPrefixes(t, parseImportFiles(t, root, "internal/module/memory"), []string{internalPrefix("internal/module/turn")})
	})
}
