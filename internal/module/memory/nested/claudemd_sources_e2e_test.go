//go:build e2e
// +build e2e

package nested

// Phase 1.6 removed AutoMem / TeamMem MEMORY.md from the nested ClaudeMd
// candidate set: MemoryEntrypointProvider in the parent memory package is
// now the sole prompt-time injector for those files. The previous
// integration test (TestCombinedClaudeMdSourcesInjectTeamEntrypointThroughBuildBaseUserContext)
// asserted the deleted behaviour and is obsolete; equivalent coverage for the
// new path lives in `internal/module/memory/entrypoint_provider_test.go`
// alongside the `TestMemoryEntrypointProvider...` cases.
