package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// Phase 4.0 baseline tests for team injection paths (p25 L86 flagged the
// existing entrypoint_provider_test cases all use team=nil). These lock
// the team=non-nil branch so a future Phase 4.1 ranking change cannot
// silently drop team-block injection.
//
// CONTRACT NOTE — runtime-ready gate isolation (reviewer E): the
// team-memory runtime-ready flag is process-global. We use the project's
// shared `withTeamMemoryRuntimeReady(t, ready)` helper (see
// `subpkg_compat_test.go`) which goes through `SwapRuntimeReadyFuncForTest`
// to install a per-test function pointer (and a Cleanup that restores
// the previous one), so concurrent t.Parallel() tests in the same
// package don't fight over a single atomic.Bool. Tests in this file
// therefore do NOT call `t.Parallel()`.

func TestPhase4BaselineEntrypointProviderInjectsTeamBlock(t *testing.T) {
	t.Setenv(envHarnessKind, "")
	withTeamMemoryRuntimeReady(t, true)

	cfg := newPhase4BaselineConfig(t, true)
	writePhase4BaselineMemory(t, cfg.AutoMemPathOverride, "- [Architecture](architecture.md) — start here")
	writePhase4BaselineMemory(t, mustConfiguredTeamMemRoot(t, cfg), "- [Dashboard owner](owner.md) — team-side guidance")

	team := NewTeamMemoryManager(cfg)
	provider := NewEntrypointProvider(cfg, team, nil)
	out, err := provider.Resolve(context.Background(), contract.SectionContext{
		Start:    &contract.StartInput{},
		BuildCtx: contract.BuildCtx{CWD: cfg.ProjectRoot, GitRoot: cfg.ProjectRoot},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if out == nil {
		t.Fatal("Resolve() = nil, want wrapped block with team injection")
	}
	got := *out
	assertPhase4BaselineContainsAll(t, got, "team=non-nil baseline", []string{
		"source=auto",
		"source=team",
		"Dashboard owner",
		"Architecture",
	})
}

func TestPhase4BaselineEntrypointProviderTeamDisabledOmitsTeamBlock(t *testing.T) {
	// Counter-baseline (reviewer D upgrade, sharpened by reviewer H):
	// keep runtime-ready ON to defend against a FUTURE regression that
	// wires runtime-ready into the entrypoint-injection gating path.
	// Today gate.InjectTeamMemIndex (gate.go:87) reads only TeamMemEnabled
	// — runtime-ready is checked deeper in team_manager.isTeamMemoryEnabled
	// and does NOT participate in this entrypoint short-circuit. Pinning
	// runtime-ready=true here ensures that if someone later folds the
	// runtime-ready check into entrypoint gating, this test will FAIL
	// (currently TeamMemEnabled=false alone suffices to omit the block).
	t.Setenv(envHarnessKind, "")
	withTeamMemoryRuntimeReady(t, true)
	cfg := newPhase4BaselineConfig(t, false)
	writePhase4BaselineMemory(t, cfg.AutoMemPathOverride, "- [Private only](private.md)")

	team := NewTeamMemoryManager(cfg)
	provider := NewEntrypointProvider(cfg, team, nil)
	out, err := provider.Resolve(context.Background(), contract.SectionContext{
		Start:    &contract.StartInput{},
		BuildCtx: contract.BuildCtx{CWD: cfg.ProjectRoot, GitRoot: cfg.ProjectRoot},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if out == nil {
		t.Fatal("Resolve() = nil, want private-only block")
	}
	got := *out
	assertPhase4BaselineOmits(t, got, "source=team", "TeamMemory=false")
	assertPhase4BaselineContainsAll(t, got, "private-only block", []string{"source=auto"})
}

func newPhase4BaselineConfig(t *testing.T, enableTeam bool) *Config {
	t.Helper()
	cfg := &Config{
		Enabled:             true,
		EnableTools:         true,
		RootDir:             t.TempDir(),
		ProjectRoot:         t.TempDir(),
		AutoMemPathOverride: filepath.Join(t.TempDir(), "private"),
	}
	if enableTeam {
		cfg.Features = MemoryFeatureFlags{TeamMemory: true}
	}
	return cfg
}

func mustConfiguredTeamMemRoot(t *testing.T, cfg *Config) string {
	t.Helper()
	teamRoot, err := configuredTeamMemRoot(cfg)
	if err != nil {
		t.Fatalf("configuredTeamMemRoot() error = %v", err)
	}
	return teamRoot
}

func writePhase4BaselineMemory(t *testing.T, root, body string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", root, err)
	}
	if err := os.WriteFile(filepath.Join(root, "MEMORY.md"), []byte(body+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(MEMORY.md) error = %v", err)
	}
}

func assertPhase4BaselineContainsAll(t *testing.T, got, context string, values []string) {
	t.Helper()
	for _, value := range values {
		if !strings.Contains(got, value) {
			t.Fatalf("Resolve() missing %s marker %q:\n%s", context, value, got)
		}
	}
}

func assertPhase4BaselineOmits(t *testing.T, got, value, context string) {
	t.Helper()
	if strings.Contains(got, value) {
		t.Fatalf("Resolve() leaked %s under %s:\n%s", value, context, got)
	}
}

// TestPhase4BaselineCrossScopeFilePathDisjoint locks the cross-scope
// invariant that a same-name entry in private + team scopes lives in
// distinct files on disk. Renamed from "CrossScopeManifestFixture"
// (reviewer D: the original name suggested a ranking baseline, but the
// assertion only verifies FilePath disjointness — a real Phase 4.1 子项
// 3 ranking baseline must additionally compare scoring deltas, which is
// out of scope here). This test is the prerequisite fixture for that
// future ranking baseline.
func TestPhase4BaselineCrossScopeFilePathDisjoint(t *testing.T) {
	// Use two completely disjoint temp roots so each BuildManifest scans
	// only its own scope. (Default cfg.AutoMemPathOverride layout puts
	// teamRoot under privateRoot, which would let walkDir cross scopes.)
	privateRoot, teamRoot := newPhase4CrossScopeRoots(t)

	const sharedName = "Cross-scope baseline name"

	createPhase4CrossScopeEntry(t, privateRoot, MemoryWriteRequest{
		Name:        sharedName,
		Description: "private-side same-name fixture",
		Type:        MemoryTypeFeedback,
		Body:        "fact\nWhy: lock cross-scope baseline.\nHow to apply: see team variant for rationale.",
	})
	createPhase4CrossScopeEntry(t, teamRoot, MemoryWriteRequest{
		Name:        sharedName,
		Description: "team-side same-name fixture",
		Type:        MemoryTypeFeedback,
		Body:        "fact\nWhy: cross-team coordination.\nHow to apply: this is the project-wide variant.",
	})

	builder := NewManifestBuilder()
	privateFilePath := buildPhase4CrossScopeFilePath(t, builder, privateRoot, sharedName, "private side")
	teamFilePath := buildPhase4CrossScopeFilePath(t, builder, teamRoot, sharedName, "team side")
	if privateFilePath == teamFilePath {
		t.Fatalf("baseline expected distinct FilePath across scopes; got both=%q", privateFilePath)
	}
	// FilePath-disjoint fixture established. A real Phase 4.1 子项 3
	// ranking baseline must additionally drive the same-name pair through
	// the ranking pipeline and assert the team-side score advantage.
}

func newPhase4CrossScopeRoots(t *testing.T) (string, string) {
	t.Helper()
	privateRoot := filepath.Join(t.TempDir(), "private-scope")
	teamRoot := filepath.Join(t.TempDir(), "team-scope")
	mkdirPhase4Root(t, privateRoot, "privateRoot")
	mkdirPhase4Root(t, teamRoot, "teamRoot")
	return privateRoot, teamRoot
}

func mkdirPhase4Root(t *testing.T, root, label string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", label, err)
	}
}

func createPhase4CrossScopeEntry(t *testing.T, root string, req MemoryWriteRequest) {
	t.Helper()
	store, err := newDiskStore(root, nil)
	if err != nil {
		t.Fatalf("newDiskStore(%s) error = %v", root, err)
	}
	if _, err := store.CreateStructured(req); err != nil {
		t.Fatalf("CreateStructured(%s) error = %v", root, err)
	}
}

func buildPhase4CrossScopeFilePath(t *testing.T, builder *ManifestBuilder, root, sharedName, label string) string {
	t.Helper()
	entries, err := builder.BuildManifest(root)
	if err != nil {
		t.Fatalf("BuildManifest(%s) error = %v", label, err)
	}
	hits := findEntriesByName(entries, sharedName)
	if len(hits) != 1 {
		t.Fatalf("%s: hits for %q = %d, want 1; entries=%#v", label, sharedName, len(hits), entries)
	}
	return hits[0].FilePath
}
