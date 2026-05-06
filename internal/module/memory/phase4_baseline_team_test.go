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
	projectRoot := t.TempDir()
	privateRoot := filepath.Join(t.TempDir(), "private")
	cfg := &Config{
		Enabled:             true,
		EnableTools:         true,
		RootDir:             t.TempDir(),
		ProjectRoot:         projectRoot,
		AutoMemPathOverride: privateRoot,
		Features:            MemoryFeatureFlags{TeamMemory: true},
	}
	if err := os.MkdirAll(privateRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(privateRoot) error = %v", err)
	}
	privateBody := strings.Join([]string{
		"- [Architecture](architecture.md) — start here",
	}, "\n")
	if err := os.WriteFile(filepath.Join(privateRoot, "MEMORY.md"), []byte(privateBody), 0o644); err != nil {
		t.Fatalf("WriteFile(private MEMORY.md) error = %v", err)
	}

	teamRoot, err := configuredTeamMemRoot(cfg)
	if err != nil {
		t.Fatalf("configuredTeamMemRoot() error = %v", err)
	}
	if err := os.MkdirAll(teamRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(teamRoot) error = %v", err)
	}
	teamBody := strings.Join([]string{
		"- [Dashboard owner](owner.md) — team-side guidance",
	}, "\n")
	if err := os.WriteFile(filepath.Join(teamRoot, "MEMORY.md"), []byte(teamBody), 0o644); err != nil {
		t.Fatalf("WriteFile(team MEMORY.md) error = %v", err)
	}

	team := NewTeamMemoryManager(cfg)
	provider := NewEntrypointProvider(cfg, team, nil)
	out, err := provider.Resolve(context.Background(), contract.SectionContext{
		Start:    &contract.StartInput{},
		BuildCtx: contract.BuildCtx{CWD: projectRoot, GitRoot: projectRoot},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if out == nil {
		t.Fatal("Resolve() = nil, want wrapped block with team injection")
	}
	got := *out
	if !strings.Contains(got, "source=auto") {
		t.Fatalf("Resolve() missing private auto block:\n%s", got)
	}
	if !strings.Contains(got, "source=team") {
		t.Fatalf("Resolve() missing team block (team=non-nil baseline):\n%s", got)
	}
	if !strings.Contains(got, "Dashboard owner") {
		t.Fatalf("Resolve() missing team entry content:\n%s", got)
	}
	if !strings.Contains(got, "Architecture") {
		t.Fatalf("Resolve() missing private entry content:\n%s", got)
	}
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
	projectRoot := t.TempDir()
	privateRoot := filepath.Join(t.TempDir(), "private")
	cfg := &Config{
		Enabled:             true,
		EnableTools:         true,
		RootDir:             t.TempDir(),
		ProjectRoot:         projectRoot,
		AutoMemPathOverride: privateRoot,
		// TeamMemory NOT enabled — gate.InjectTeamMemIndex stays false.
	}
	if err := os.MkdirAll(privateRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(privateRoot) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(privateRoot, "MEMORY.md"),
		[]byte("- [Private only](private.md)\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(private MEMORY.md) error = %v", err)
	}

	team := NewTeamMemoryManager(cfg)
	provider := NewEntrypointProvider(cfg, team, nil)
	out, err := provider.Resolve(context.Background(), contract.SectionContext{
		Start:    &contract.StartInput{},
		BuildCtx: contract.BuildCtx{CWD: projectRoot, GitRoot: projectRoot},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if out == nil {
		t.Fatal("Resolve() = nil, want private-only block")
	}
	got := *out
	if strings.Contains(got, "source=team") {
		t.Fatalf("Resolve() leaked team block under TeamMemory=false:\n%s", got)
	}
	if !strings.Contains(got, "source=auto") {
		t.Fatalf("Resolve() missing private auto block:\n%s", got)
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
	privateRoot := filepath.Join(t.TempDir(), "private-scope")
	teamRoot := filepath.Join(t.TempDir(), "team-scope")
	if err := os.MkdirAll(privateRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(privateRoot) error = %v", err)
	}
	if err := os.MkdirAll(teamRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(teamRoot) error = %v", err)
	}

	const sharedName = "Cross-scope baseline name"

	privateStore, err := newDiskStore(privateRoot, nil)
	if err != nil {
		t.Fatalf("newDiskStore(private) error = %v", err)
	}
	if _, err := privateStore.CreateStructured(MemoryWriteRequest{
		Name:        sharedName,
		Description: "private-side same-name fixture",
		Type:        MemoryTypeFeedback,
		Body:        "fact\nWhy: lock cross-scope baseline.\nHow to apply: see team variant for rationale.",
	}); err != nil {
		t.Fatalf("CreateStructured(private) error = %v", err)
	}
	teamStore, err := newDiskStore(teamRoot, nil)
	if err != nil {
		t.Fatalf("newDiskStore(team) error = %v", err)
	}
	if _, err := teamStore.CreateStructured(MemoryWriteRequest{
		Name:        sharedName,
		Description: "team-side same-name fixture",
		Type:        MemoryTypeFeedback,
		Body:        "fact\nWhy: cross-team coordination.\nHow to apply: this is the project-wide variant.",
	}); err != nil {
		t.Fatalf("CreateStructured(team) error = %v", err)
	}

	builder := NewManifestBuilder()
	privateEntries, err := builder.BuildManifest(privateRoot)
	if err != nil {
		t.Fatalf("BuildManifest(private) error = %v", err)
	}
	teamEntries, err := builder.BuildManifest(teamRoot)
	if err != nil {
		t.Fatalf("BuildManifest(team) error = %v", err)
	}

	privHits := findEntriesByName(privateEntries, sharedName)
	teamHits := findEntriesByName(teamEntries, sharedName)
	if len(privHits) != 1 {
		t.Fatalf("private side: hits for %q = %d, want 1; entries=%#v", sharedName, len(privHits), privateEntries)
	}
	if len(teamHits) != 1 {
		t.Fatalf("team side: hits for %q = %d, want 1; entries=%#v", sharedName, len(teamHits), teamEntries)
	}
	if privHits[0].FilePath == teamHits[0].FilePath {
		t.Fatalf("baseline expected distinct FilePath across scopes; got both=%q", privHits[0].FilePath)
	}
	// FilePath-disjoint fixture established. A real Phase 4.1 子项 3
	// ranking baseline must additionally drive the same-name pair through
	// the ranking pipeline and assert the team-side score advantage.
}
