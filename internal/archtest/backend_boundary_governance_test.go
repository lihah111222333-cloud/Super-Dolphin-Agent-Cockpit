package archtest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/archtest"
)

func TestValidateDefaultBackendBoundaryGovernance(t *testing.T) {
	t.Parallel()

	violations := archtest.ValidateBackendBoundaryGovernance(repoRoot(t), archtest.DefaultBackendBoundaryRegistry())
	if len(violations) > 0 {
		t.Fatalf("default backend boundary governance is invalid:\n%s", strings.Join(violations, "\n"))
	}
}

func TestValidateBackendBoundaryGovernanceRejectsUnregisteredSurface(t *testing.T) {
	t.Parallel()

	root, registry := validBackendBoundaryGovernanceFixture(t)
	writeGovernanceFixtureFile(t, root, "internal/unregistered/service.go", "package unregistered\n")
	assertGovernanceViolation(t, root, registry, "unregistered backend surface \"internal/unregistered\"")
}

func TestValidateBackendBoundaryGovernanceRejectsInvalidReferences(t *testing.T) {
	t.Parallel()

	runGovernanceMutationCases(t, []governanceMutationCase{
		{
			name: "unknown rule",
			mutate: func(registry *archtest.BackendBoundaryRegistry) {
				registry.Surfaces[0].RuleIDs = []archtest.BoundaryRuleID{"missing_rule"}
			},
			want: "unknown rule \"missing_rule\"",
		},
		{
			name: "unknown guard",
			mutate: func(registry *archtest.BackendBoundaryRegistry) {
				registry.Surfaces[0].GuardIDs = []archtest.BoundaryGuardID{"missing_guard"}
			},
			want: "unknown guard \"missing_guard\"",
		},
		{
			name: "rule matches no surface file",
			mutate: func(registry *archtest.BackendBoundaryRegistry) {
				registry.Surfaces[0].RuleIDs = []archtest.BoundaryRuleID{"contract_reverse_pollution"}
			},
			want: "backend surface \"cmd/tool\" rule \"contract_reverse_pollution\" matches no applicable Go file",
		},
		{
			name: "rule has no enforcing policy for surface file",
			mutate: func(registry *archtest.BackendBoundaryRegistry) {
				rule := mustCanonicalBackendBoundaryRule(t, *registry, "pkg_no_internal_imports")
				rule.Deny = nil
				replaceCanonicalBackendBoundaryRule(t, registry, rule)
			},
			want: "backend surface \"pkg/lib\" rule \"pkg_no_internal_imports\" has no enforcing policy for an applicable Go file",
		},
	})
}

func TestValidateBackendBoundaryGovernanceRejectsInvalidDescriptors(t *testing.T) {
	t.Parallel()

	runGovernanceMutationCases(t, []governanceMutationCase{
		{
			name: "missing guard test",
			mutate: func(registry *archtest.BackendBoundaryRegistry) {
				registry.Guards[0].TestNames = []string{"TestMissingGuard"}
			},
			want: "test \"TestMissingGuard\" is not a runnable top-level Go test",
		},
		{
			name: "guard outside internal",
			mutate: func(registry *archtest.BackendBoundaryRegistry) {
				registry.Guards[0].File = "cmd/service/special_guard_test.go"
			},
			want: "must be a canonical internal/**/*_test.go path",
		},
		{
			name: "orphan guard",
			mutate: func(registry *archtest.BackendBoundaryRegistry) {
				registry.Guards = append(registry.Guards, archtest.BackendBoundaryGuard{
					ID:        "orphan_guard",
					File:      "internal/archtest/special_guard_test.go",
					TestNames: []string{"TestSpecialGuard"},
					Reason:    "synthetic orphan guard",
				})
			},
			want: "guard \"orphan_guard\" is not referenced by any backend surface",
		},
		{
			name: "empty mechanism",
			mutate: func(registry *archtest.BackendBoundaryRegistry) {
				registry.Surfaces[0].RuleIDs = nil
				registry.Surfaces[0].GuardIDs = nil
			},
			want: "has no canonical rules or specialized guards",
		},
		{
			name: "duplicate surface",
			mutate: func(registry *archtest.BackendBoundaryRegistry) {
				registry.Surfaces = append(registry.Surfaces, registry.Surfaces[0])
			},
			want: "duplicate backend surface",
		},
		{
			name: "stale surface",
			mutate: func(registry *archtest.BackendBoundaryRegistry) {
				registry.Surfaces[2].Path = "internal/missing"
			},
			want: "registered backend surface \"internal/missing\" is missing or contains no Go source",
		},
	})
}

type governanceMutationCase struct {
	name   string
	mutate func(*archtest.BackendBoundaryRegistry)
	want   string
}

func runGovernanceMutationCases(t *testing.T, cases []governanceMutationCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, registry := validBackendBoundaryGovernanceFixture(t)
			tc.mutate(&registry)
			assertGovernanceViolation(t, root, registry, tc.want)
		})
	}
}

func TestBackendBoundaryGovernanceRegistryReturnsDeepCopy(t *testing.T) {
	t.Parallel()

	first := archtest.DefaultBackendBoundaryRegistry()
	second := archtest.DefaultBackendBoundaryRegistry()
	if len(first.Guards) == 0 || len(first.Surfaces) == 0 || len(first.Guards[0].TestNames) == 0 || len(first.Surfaces[0].RuleIDs) == 0 {
		t.Fatalf("default governance fixture is incomplete: %#v", first)
	}
	first.Guards[0].TestNames[0] = "TestMutated"
	first.Surfaces[0].RuleIDs[0] = "mutated_rule"
	if second.Guards[0].TestNames[0] == "TestMutated" || second.Surfaces[0].RuleIDs[0] == "mutated_rule" {
		t.Fatal("default registry shares nested governance slices")
	}
	for index := range first.Guards {
		if len(first.Guards[index].BuildTags) == 0 {
			continue
		}
		first.Guards[index].BuildTags[0] = "mutated_tag"
		if second.Guards[index].BuildTags[0] == "mutated_tag" {
			t.Fatal("default registry shares guard build tags")
		}
		return
	}
	t.Fatal("default registry has no tagged guard to exercise deep copy")
}

func TestDefaultGovernanceUsesDirectGuardsForTestOnlySurfaces(t *testing.T) {
	t.Parallel()

	registry := archtest.DefaultBackendBoundaryRegistry()
	guards := make(map[archtest.BoundaryGuardID]archtest.BackendBoundaryGuard, len(registry.Guards))
	for _, guard := range registry.Guards {
		guards[guard.ID] = guard
	}
	for _, surfacePath := range []string{"internal/e2e", "internal/guards"} {
		var surface *archtest.BackendBoundarySurface
		for index := range registry.Surfaces {
			if registry.Surfaces[index].Path == surfacePath {
				surface = &registry.Surfaces[index]
				break
			}
		}
		if surface == nil || len(surface.GuardIDs) == 0 {
			t.Fatalf("test-only surface %q has no specialized guard", surfacePath)
		}
		for _, guardID := range surface.GuardIDs {
			guard, ok := guards[guardID]
			if !ok || !strings.HasPrefix(guard.File, surfacePath+"/") {
				t.Errorf("surface %q guard %q resolves to %q, want a direct test under the surface", surfacePath, guardID, guard.File)
			}
		}
	}
}

func TestDiscoverRunnableGoTestsMatchesGoToolSelectionAndSignatures(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "runnable_test.go")
	source := `package fixture
import "testing"
type T = testing.T
func Test(t *testing.T) {}
func TestAlias(t *T) {}
func TestEmptyResults(t *testing.T) () {}
func TestGeneric[P any](t *testing.T) {}
func TestReturns(t *testing.T) error { return nil }
`
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatalf("write runnable test fixture: %v", err)
	}
	names, err := archtest.DiscoverRunnableGoTests(path)
	if err != nil {
		t.Fatalf("discover runnable tests: %v", err)
	}
	want := []string{"Test", "TestAlias", "TestEmptyResults"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("runnable tests = %v, want %v", names, want)
	}

	excluded := filepath.Join(root, "excluded_test.go")
	excludedSource := "//go:build ignore\n\npackage fixture\n\nimport \"testing\"\n\nfunc TestExcluded(t *testing.T) {}\n"
	if err := os.WriteFile(excluded, []byte(excludedSource), 0o600); err != nil {
		t.Fatalf("write build-excluded fixture: %v", err)
	}
	names, err = archtest.DiscoverRunnableGoTests(excluded)
	if err != nil {
		t.Fatalf("discover build-excluded tests: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("build-excluded tests reported as runnable: %v", names)
	}
}

func TestDiscoverRunnableGoTestsHonorsGOFLAGSBuildTags(t *testing.T) {
	t.Setenv("GOFLAGS", "-tags=archtest_governance")

	path := filepath.Join(t.TempDir(), "tagged_test.go")
	source := "//go:build archtest_governance\n\npackage fixture\n\nimport \"testing\"\n\nfunc TestTaggedGuard(t *testing.T) {}\n"
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatalf("write tagged guard fixture: %v", err)
	}
	names, err := archtest.DiscoverRunnableGoTests(path)
	if err != nil {
		t.Fatalf("discover tagged guard: %v", err)
	}
	if strings.Join(names, ",") != "TestTaggedGuard" {
		t.Fatalf("tagged runnable tests = %v, want TestTaggedGuard", names)
	}
}

func TestDiscoverRunnableGoTestsMatchesGOFLAGSQuotedSemantics(t *testing.T) {
	testCases := []struct {
		name       string
		goFlags    string
		constraint string
		wantError  bool
	}{
		{name: "quoted tag value", goFlags: `"-tags=foo bar"`, constraint: "foo && bar"},
		{name: "double dash tags", goFlags: "--tags=foo", constraint: "foo"},
		{name: "last repeated tags flag wins", goFlags: "-tags=missing -tags=foo", constraint: "foo"},
		{name: "split tags value is invalid", goFlags: "-tags foo", constraint: "foo", wantError: true},
		{name: "bare tags flag is invalid", goFlags: "-tags", constraint: "foo", wantError: true},
		{name: "unterminated quote is invalid", goFlags: `"-tags=foo`, constraint: "foo", wantError: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("GOFLAGS", testCase.goFlags)
			path := filepath.Join(t.TempDir(), "tagged_test.go")
			source := "//go:build " + testCase.constraint + "\n\npackage fixture\n\nimport \"testing\"\n\nfunc TestTaggedGuard(t *testing.T) {}\n"
			if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
				t.Fatalf("write tagged guard fixture: %v", err)
			}
			names, err := archtest.DiscoverRunnableGoTests(path)
			if testCase.wantError {
				if err == nil {
					t.Fatalf("DiscoverRunnableGoTests with GOFLAGS %q succeeded, want error", testCase.goFlags)
				}
				return
			}
			if err != nil {
				t.Fatalf("DiscoverRunnableGoTests with GOFLAGS %q: %v", testCase.goFlags, err)
			}
			if strings.Join(names, ",") != "TestTaggedGuard" {
				t.Fatalf("tagged runnable tests = %v, want TestTaggedGuard", names)
			}
		})
	}
}

func TestValidateBackendBoundaryGovernanceRejectsGuardSymlinkEscape(t *testing.T) {
	t.Parallel()

	root, registry := validBackendBoundaryGovernanceFixture(t)
	external := filepath.Join(t.TempDir(), "external_guard_test.go")
	if err := os.WriteFile(external, []byte("package archtest\n\nimport \"testing\"\n\nfunc TestExternalGuard(t *testing.T) {}\n"), 0o600); err != nil {
		t.Fatalf("write external guard fixture: %v", err)
	}
	guardRel := "internal/archtest/escaped_guard_test.go"
	guardPath := filepath.Join(root, filepath.FromSlash(guardRel))
	if err := os.Symlink(external, guardPath); err != nil {
		t.Skipf("create symlink fixture: %v", err)
	}
	registry.Guards[0].File = guardRel
	registry.Guards[0].TestNames = []string{"TestExternalGuard"}
	assertGovernanceViolation(t, root, registry, "must resolve to a regular file within the repository internal tree")
}

func TestValidateBackendBoundaryGovernanceRejectsGuardParentSymlinkEscape(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	externalInternal := filepath.Join(t.TempDir(), "internal")
	guardPath := filepath.Join(externalInternal, "archtest", "guard_test.go")
	if err := os.MkdirAll(filepath.Dir(guardPath), 0o755); err != nil {
		t.Fatalf("create external archtest directory: %v", err)
	}
	if err := os.WriteFile(guardPath, []byte("package archtest\n\nimport \"testing\"\n\nfunc TestExternalGuard(t *testing.T) {}\n"), 0o600); err != nil {
		t.Fatalf("write external guard fixture: %v", err)
	}
	if err := os.Symlink(externalInternal, filepath.Join(root, "internal")); err != nil {
		t.Skipf("create parent symlink fixture: %v", err)
	}
	registry := archtest.DefaultBackendBoundaryRegistry()
	registry.Guards = []archtest.BackendBoundaryGuard{{ID: "external_guard", File: "internal/archtest/guard_test.go", TestNames: []string{"TestExternalGuard"}, Reason: "synthetic external guard"}}
	registry.Surfaces = []archtest.BackendBoundarySurface{{Path: "internal/archtest", GuardIDs: []archtest.BoundaryGuardID{"external_guard"}, Reason: "synthetic escaped surface"}}
	assertGovernanceViolation(t, root, registry, "must resolve to a regular file within the repository internal tree")
}

func validBackendBoundaryGovernanceFixture(t *testing.T) (string, archtest.BackendBoundaryRegistry) {
	t.Helper()

	root := t.TempDir()
	writeGovernanceFixtureFile(t, root, "cmd/tool/main.go", "package main\n")
	writeGovernanceFixtureFile(t, root, "internal/archtest/backend_boundary_governance_test.go", "package archtest\n\nimport \"testing\"\n\nfunc TestValidateDefaultBackendBoundaryGovernance(t *testing.T) {}\n")
	writeGovernanceFixtureFile(t, root, "internal/service/service.go", "package service\n")
	writeGovernanceFixtureFile(t, root, "pkg/lib/lib.go", "package lib\n")

	registry := archtest.DefaultBackendBoundaryRegistry()
	registry.Guards = registry.Guards[:1]
	registry.Surfaces = []archtest.BackendBoundarySurface{
		{Path: "cmd/tool", RuleIDs: []archtest.BoundaryRuleID{"fx_assembly_scope"}, Reason: "fixture command"},
		{Path: "internal/archtest", GuardIDs: []archtest.BoundaryGuardID{"backend_surface_governance"}, Reason: "fixture guard package"},
		{Path: "internal/service", RuleIDs: []archtest.BoundaryRuleID{"fx_assembly_scope"}, Reason: "fixture internal service"},
		{Path: "pkg/lib", RuleIDs: []archtest.BoundaryRuleID{"pkg_no_internal_imports"}, Reason: "fixture public library"},
	}
	return root, registry
}

func writeGovernanceFixtureFile(t *testing.T, root, rel, source string) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatalf("write fixture %s: %v", rel, err)
	}
}

func assertGovernanceViolation(t *testing.T, root string, registry archtest.BackendBoundaryRegistry, want string) {
	t.Helper()

	got := strings.Join(archtest.ValidateBackendBoundaryGovernance(root, registry), "\n")
	if !strings.Contains(got, want) {
		t.Fatalf("governance violations missing %q:\n%s", want, got)
	}
}
