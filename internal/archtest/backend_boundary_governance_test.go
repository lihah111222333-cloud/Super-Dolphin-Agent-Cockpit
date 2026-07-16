package archtest_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest"
)

func TestValidateDefaultBackendBoundaryGovernance(t *testing.T) {
	t.Parallel()

	violations := archtest.ValidateBackendBoundaryGovernance(repoRoot(t), archtest.DefaultBackendBoundaryRegistry())
	if len(violations) > 0 {
		t.Fatalf("default backend boundary governance is invalid:\n%s", strings.Join(violations, "\n"))
	}
}

func TestBackendBoundaryGovernanceRejectsOrphanCanonicalRule(t *testing.T) {
	registry := archtest.DefaultBackendBoundaryRegistry()
	ruleID := registry.Rules[0].ID
	for index := range registry.Surfaces {
		registry.Surfaces[index].RuleIDs = slices.DeleteFunc(
			registry.Surfaces[index].RuleIDs,
			func(id archtest.BoundaryRuleID) bool { return id == ruleID },
		)
	}
	violations := strings.Join(archtest.ValidateBackendBoundaryGovernance(repoRoot(t), registry), "\n")
	want := `rule "` + string(ruleID) + `" is not referenced by any backend surface`
	if !strings.Contains(violations, want) {
		t.Fatalf("orphan canonical rule was accepted:\n%s", violations)
	}
}

func TestBackendBoundaryModuleSurfaceIncludesNoStoreRule(t *testing.T) {
	for _, surface := range archtest.DefaultBackendBoundaryRegistry().Surfaces {
		if surface.Path != "internal/module" {
			continue
		}
		if !slices.Contains(surface.RuleIDs, archtest.BoundaryRuleID("module_no_store_imports")) {
			t.Fatalf("internal/module rules = %v", surface.RuleIDs)
		}
		return
	}
	t.Fatal("internal/module backend boundary surface is missing")
}

func TestAppAdapterBoundaryUsesAuditedProductionImports(t *testing.T) {
	const ruleID = archtest.BoundaryRuleID("app_adapter_narrow_import_surface")
	registry := archtest.DefaultBackendBoundaryRegistry()
	rule, ok := registry.Rule(ruleID)
	if !ok {
		t.Fatal("app adapter boundary rule is missing")
	}
	assertAppAdapterRuleDescriptor(t, rule)
	storeguardPolicies := assertAppAdapterAllowPolicies(t, rule)
	evaluation := assertAppAdapterProductionImports(t, registry, ruleID)
	assertAppAdapterRuleRegistrations(t, registry, ruleID)
	t.Logf("app adapter production candidates=%d allow_policies=%d storeguard_users=%d", evaluation.ByRule[ruleID], len(rule.Allow), storeguardPolicies)
}

// assertAppAdapterRuleDescriptor 校验 typed rule 与 production-only 语义。
func assertAppAdapterRuleDescriptor(t *testing.T, rule archtest.BackendBoundaryRule) {
	t.Helper()
	if rule.Owner != archtest.BoundaryOwnerID("app_adapter_boundary") {
		t.Fatalf("app adapter rule owner = %q", rule.Owner)
	}
	if rule.Kind != archtest.BoundaryRuleAllowInternalImports {
		t.Fatalf("app adapter rule kind = %q", rule.Kind)
	}
	if !rule.SkipTestFiles {
		t.Fatal("app adapter rule must skip test files")
	}
}

// assertAppAdapterAllowPolicies 从 canonical rule 统计 storeguard 使用者，并拒绝测试依赖泄漏。
func assertAppAdapterAllowPolicies(t *testing.T, rule archtest.BackendBoundaryRule) int {
	t.Helper()
	storeguardPolicies := 0
	for _, policy := range rule.Allow {
		if policy.ImportPrefix == "internal/app/internal/storeguard" {
			storeguardPolicies++
			if strings.Contains(policy.FilePattern, "/skill/") || strings.Contains(policy.FilePattern, "/thread/") {
				t.Fatalf("storeguard policy must not cover skill or thread adapter: %q", policy.FilePattern)
			}
		}
		if isAppAdapterTestOnlyImport(policy.ImportPrefix) {
			t.Fatalf("production app adapter allow registry contains test-only import %q", policy.ImportPrefix)
		}
		if isAppAdapterBroadImport(policy.ImportPrefix) {
			t.Fatalf("production app adapter allow registry contains broad import %q", policy.ImportPrefix)
		}
		if isAppAdapterChildImport(policy.ImportPrefix) && !isAppAdapterAggregatorFile(policy.FilePattern) {
			t.Fatalf("non-aggregator %q may not import sibling adapter %q", policy.FilePattern, policy.ImportPrefix)
		}
	}
	if storeguardPolicies != 10 {
		t.Fatalf("storeguard production policies = %d, want 10 actual adapter users", storeguardPolicies)
	}
	return storeguardPolicies
}

// isAppAdapterTestOnlyImport 识别不得进入 production allow registry 的测试依赖前缀。
func isAppAdapterTestOnlyImport(prefix string) bool {
	return prefix == "internal/testutil" || strings.HasPrefix(prefix, "internal/testutil/") || prefix == "go.uber.org/fx/fxtest"
}

// isAppAdapterBroadImport 拒绝会放开整个 module、store 或 adapter family 的前缀。
func isAppAdapterBroadImport(prefix string) bool {
	return prefix == "internal/module" || prefix == "internal/store" || prefix == "internal/app/storeadapter" || prefix == "internal/app/runtimeadapter"
}

// isAppAdapterChildImport 识别只允许由两个根 aggregator 使用的 adapter child imports。
func isAppAdapterChildImport(prefix string) bool {
	return strings.HasPrefix(prefix, "internal/app/storeadapter/") || strings.HasPrefix(prefix, "internal/app/runtimeadapter/")
}

func isAppAdapterAggregatorFile(pattern string) bool {
	return pattern == "internal/app/storeadapter/module.go" || pattern == "internal/app/runtimeadapter/module.go"
}

func TestAppAdapterProductionAllowRegistryRejectsTestOnlyImports(t *testing.T) {
	cases := map[string]bool{
		"internal/testutil":              true,
		"internal/testutil/storeadapter": true,
		"go.uber.org/fx/fxtest":          true,
		"internal/store/thread":          false,
	}
	for prefix, want := range cases {
		if got := isAppAdapterTestOnlyImport(prefix); got != want {
			t.Errorf("isAppAdapterTestOnlyImport(%q) = %v, want %v", prefix, got, want)
		}
	}
}

// assertAppAdapterProductionImports 由 canonical evaluator 证明规则命中生产候选且当前实现无违规。
func assertAppAdapterProductionImports(t *testing.T, registry archtest.BackendBoundaryRegistry, ruleID archtest.BoundaryRuleID) archtest.BoundaryEvaluation {
	t.Helper()
	evaluation, err := archtest.EvaluateBackendBoundary(repoRoot(t), registry, ruleID)
	if err != nil {
		t.Fatalf("evaluate app adapter boundary: %v", err)
	}
	if evaluation.ByRule[ruleID] == 0 || evaluation.CandidateFiles == 0 {
		t.Fatalf("app adapter production candidates = %#v", evaluation)
	}
	if len(evaluation.Violations) != 0 {
		t.Fatalf("app adapter production imports violate the canonical registry:\n%s", strings.Join(evaluation.Violations, "\n"))
	}
	return evaluation
}

// assertAppAdapterRuleRegistrations 校验 Onion、Cross 与 internal/app surface 共用同一 canonical rule。
func assertAppAdapterRuleRegistrations(t *testing.T, registry archtest.BackendBoundaryRegistry, ruleID archtest.BoundaryRuleID) {
	t.Helper()
	if !slices.Contains(archtest.OnionBoundaryRuleIDs(), ruleID) {
		t.Errorf("onion rule set is missing %q", ruleID)
	}
	if !slices.Contains(archtest.CrossDomainBoundaryRuleIDs(), ruleID) {
		t.Errorf("cross rule set is missing %q", ruleID)
	}
	for _, surface := range registry.Surfaces {
		if surface.Path == "internal/app" {
			if !slices.Contains(surface.RuleIDs, ruleID) {
				t.Fatalf("internal/app rules = %v", surface.RuleIDs)
			}
			return
		}
	}
	t.Fatal("internal/app backend boundary surface is missing")
}

func TestValidateBackendBoundaryGovernanceReportsCanonicalPositions(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name   string
		marker string
		label  string
		mutate func(*archtest.BackendBoundaryRegistry)
	}{
		{name: "owner", marker: `{ID: "contract_boundary"`, label: "owner[0] reason is empty", mutate: func(registry *archtest.BackendBoundaryRegistry) { registry.Owners[0].Reason = "" }},
		{name: "rule", marker: "defaultContractReversePollutionRule(patterns),", label: "rule[0] reason is empty", mutate: func(registry *archtest.BackendBoundaryRegistry) { registry.Rules[0].Reason = "" }},
		{name: "guard", marker: `{ID: "backend_surface_governance"`, label: "guard[0] reason is empty", mutate: func(registry *archtest.BackendBoundaryRegistry) { registry.Guards[0].Reason = "" }},
		{name: "surface", marker: `backendBoundarySurface("cmd/agent-runtime"`, label: "surface[0] reason is empty", mutate: func(registry *archtest.BackendBoundaryRegistry) { registry.Surfaces[0].Reason = "" }},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			registry := archtest.DefaultBackendBoundaryRegistry()
			testCase.mutate(&registry)
			violations := archtest.ValidateBackendBoundaryGovernance(repoRoot(t), registry)
			assertCanonicalRegistryPosition(t, violations, testCase.label, testCase.marker)
		})
	}
}

func TestValidateBackendBoundaryGovernanceSyntheticPositionFallback(t *testing.T) {
	t.Parallel()

	registry := archtest.BackendBoundaryRegistry{
		Owners: []archtest.BackendBoundaryOwner{{ID: "synthetic", FilePatterns: []string{"internal/synthetic/**/*.go"}}},
	}
	violations := archtest.ValidateBackendBoundaryGovernance(t.TempDir(), registry)
	got := strings.Join(violations, "\n")
	if !strings.Contains(got, "synthetic registry: owner[0] reason is empty") {
		t.Fatalf("synthetic registry violation has no explicit fallback label: %s", got)
	}
	if strings.Contains(got, "backend_boundary_registry.go:") {
		t.Fatalf("synthetic registry fabricated a canonical source position: %s", got)
	}
}

func TestValidateBackendBoundaryGovernanceFailsWhenCanonicalPositionSourceMissing(t *testing.T) {
	t.Parallel()

	violations := archtest.ValidateBackendBoundaryGovernance(t.TempDir(), archtest.DefaultBackendBoundaryRegistry())
	got := strings.Join(violations, "\n")
	if !strings.Contains(got, "internal/archtest/backend_boundary_registry.go") || !strings.Contains(got, "read canonical backend boundary registry source") {
		t.Fatalf("missing canonical source did not fail fast with context: %s", got)
	}
}

func assertCanonicalRegistryPosition(t *testing.T, violations []string, label, marker string) {
	t.Helper()
	pattern := regexp.MustCompile(`^internal/archtest/backend_boundary_registry\.go:([1-9][0-9]*):([1-9][0-9]*): ` + regexp.QuoteMeta(label) + `$`)
	for _, violation := range violations {
		match := pattern.FindStringSubmatch(violation)
		if match == nil {
			continue
		}
		line, err := strconv.Atoi(match[1])
		if err != nil {
			t.Fatalf("parse reported source line %q: %v", match[1], err)
		}
		source, err := os.ReadFile(filepath.Join(repoRoot(t), "internal/archtest/backend_boundary_registry.go"))
		if err != nil {
			t.Fatalf("read canonical registry source: %v", err)
		}
		lines := strings.Split(string(source), "\n")
		if line > len(lines) || !strings.Contains(lines[line-1], marker) {
			t.Fatalf("reported position %s does not identify physical entry containing %q", violation, marker)
		}
		return
	}
	t.Fatalf("no canonical position for %q in violations: %v", label, violations)
}

func TestDefaultBackendBoundaryGovernanceRequiresSemanticRulesForFXOnlySurfaces(t *testing.T) {
	t.Parallel()

	registry := archtest.DefaultBackendBoundaryRegistry()
	assertSemanticSurfaceMappings(t, registry)
	assertSemanticRuleDescriptors(t, registry)
}

func assertSemanticSurfaceMappings(t *testing.T, registry archtest.BackendBoundaryRegistry) {
	t.Helper()
	wantRules := map[string]archtest.BoundaryRuleID{
		"cmd/agent-runtime":                  "command_narrow_import_surface",
		"cmd/agent-terminal":                 "command_narrow_import_surface",
		"cmd/codex-worktree-setup":           "command_narrow_import_surface",
		"cmd/mcp-schema-compiler-helper":     "command_narrow_import_surface",
		"cmd/super-dolphin-gate":             "command_narrow_import_surface",
		"cmd/super-dolphin-release-manifest": "command_narrow_import_surface",
		"cmd/super-dolphin-guard":            "command_narrow_import_surface",
		"cmd/super-dolphin-updater":          "command_narrow_import_surface",
		"internal/devtools":                  "internal_support_narrow_import_surface",
		"internal/dto":                       "internal_support_narrow_import_surface",
		"internal/testutil":                  "internal_support_narrow_import_surface",
		"internal/util":                      "internal_support_narrow_import_surface",
	}

	for _, surface := range registry.Surfaces {
		wantRule, ok := wantRules[surface.Path]
		if !ok {
			continue
		}
		delete(wantRules, surface.Path)
		if !containsBoundaryRuleID(surface.RuleIDs, wantRule) {
			t.Errorf("surface %q rules = %v, want semantic rule %q", surface.Path, surface.RuleIDs, wantRule)
		}
	}
	if len(wantRules) > 0 {
		t.Fatalf("semantic governance surfaces are missing: %v", wantRules)
	}
}

func assertSemanticRuleDescriptors(t *testing.T, registry archtest.BackendBoundaryRegistry) {
	t.Helper()
	wantPolicies := map[archtest.BoundaryRuleID]map[string][]string{
		"command_narrow_import_surface": {
			"cmd/agent-runtime/**/*.go":                  {"internal/app", "internal/platform/rlimit", "internal/platform/runtimeenv"},
			"cmd/agent-terminal/**/*.go":                 {"internal/app", "internal/platform/appupdaterecovery", "internal/platform/pidregistry", "internal/platform/rlimit", "internal/platform/runner", "internal/platform/runtimeenv"},
			"cmd/codex-worktree-setup/**/*.go":           {"internal/platform/config", "internal/util/pathutil"},
			"cmd/mcp-schema-compiler-helper/**/*.go":     {"internal/platform/toolbridge/schema"},
			"cmd/super-dolphin-gate/**/*.go":             {"internal/devtools/gate", "internal/devtools/localci"},
			"cmd/super-dolphin-release-manifest/**/*.go": {"internal/module/appupdate"},
			"cmd/super-dolphin-guard/**/*.go":            {"internal/platform/appupdaterecovery", "internal/platform/pidregistry", "internal/platform/runtimeenv"},
			"cmd/super-dolphin-updater/**/*.go":          {"internal/platform/appupdatefailure", "internal/platform/appupdaterecovery", "internal/platform/pidregistry", "internal/platform/runtimeenv", "internal/util/ctxutil", "internal/util/safego"},
		},
		"internal_support_narrow_import_surface": {
			"internal/devtools/**/*.go": {"internal/devtools", "internal/platform/config", "internal/platform/db"},
			"internal/dto/**/*.go":      {"internal/dto"},
			"internal/testutil/**/*.go": {"internal/contract", "internal/testutil"},
			"internal/util/**/*.go": {
				"internal/dto/provider",
				"internal/platform/config",
				"internal/platform/runtimesafe",
				"internal/platform/sessionpaths",
				"internal/platform/shared",
				"internal/util",
			},
		},
	}

	for id, want := range wantPolicies {
		rule, ok := registry.Rule(id)
		if !ok {
			t.Errorf("semantic rule %q is missing", id)
			continue
		}
		if rule.Kind != archtest.BoundaryRuleAllowInternalImports {
			t.Errorf("semantic rule %q kind = %q, want %q", id, rule.Kind, archtest.BoundaryRuleAllowInternalImports)
		}
		if len(rule.FilePatterns) == 0 || len(rule.Allow) == 0 {
			t.Errorf("semantic rule %q is an empty placeholder: %#v", id, rule)
		}
		assertSemanticRulePolicies(t, rule, want)
	}
}

func assertSemanticRulePolicies(t *testing.T, rule archtest.BackendBoundaryRule, want map[string][]string) {
	t.Helper()
	wantPairs := make(map[string]bool)
	for pattern, prefixes := range want {
		for _, prefix := range prefixes {
			wantPairs[pattern+"\x00"+prefix] = true
		}
	}
	for _, pattern := range rule.FilePatterns {
		if _, ok := want[pattern]; !ok {
			t.Errorf("semantic rule %q has unexpected file pattern %q", rule.ID, pattern)
		}
	}
	if len(rule.FilePatterns) != len(want) {
		t.Errorf("semantic rule %q file patterns = %v, want exact patterns %v", rule.ID, rule.FilePatterns, want)
	}
	for _, policy := range rule.Allow {
		pair := policy.FilePattern + "\x00" + policy.ImportPrefix
		if !wantPairs[pair] {
			t.Errorf("semantic rule %q has unexpected allow policy %q -> %q", rule.ID, policy.FilePattern, policy.ImportPrefix)
		}
		delete(wantPairs, pair)
		if policy.Reason == "" {
			t.Errorf("semantic rule %q allow policy %q -> %q has no reason", rule.ID, policy.FilePattern, policy.ImportPrefix)
		}
	}
	if len(wantPairs) > 0 {
		t.Errorf("semantic rule %q is missing exact allow policies: %v", rule.ID, wantPairs)
	}
}

func containsBoundaryRuleID(ids []archtest.BoundaryRuleID, want archtest.BoundaryRuleID) bool {
	return slices.Contains(ids, want)
}

func TestValidateBackendBoundaryGovernanceRejectsUnregisteredSurface(t *testing.T) {
	t.Parallel()

	root, registry := validBackendBoundaryGovernanceFixture(t)
	writeGovernanceFixtureFile(t, root, "internal/unregistered/service.go", "package unregistered\n")
	assertGovernanceViolation(t, root, registry, "unregistered backend surface \"internal/unregistered\"")
}

func TestValidateBackendBoundaryGovernanceRejectsGuardSurfaceMismatch(t *testing.T) {
	t.Parallel()

	root, registry := validBackendBoundaryGovernanceFixture(t)
	registry.Surfaces[0].GuardIDs = []archtest.BoundaryGuardID{"backend_surface_governance"}
	assertGovernanceViolation(
		t,
		root,
		registry,
		"surface \"cmd/tool\" guard \"backend_surface_governance\" is not declared in guard applies_to",
	)
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
					AppliesTo: []archtest.BoundarySurfaceID{"internal/archtest"},
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

func TestValidateBackendBoundaryGovernanceRejectsInvalidGuardApplicability(t *testing.T) {
	t.Parallel()

	runGovernanceMutationCases(t, []governanceMutationCase{
		{
			name: "empty guard applies_to",
			mutate: func(registry *archtest.BackendBoundaryRegistry) {
				registry.Guards[0].AppliesTo = nil
			},
			want: "guard[0] applies_to is empty",
		},
		{
			name: "duplicate guard applies_to",
			mutate: func(registry *archtest.BackendBoundaryRegistry) {
				registry.Guards[0].AppliesTo = append(registry.Guards[0].AppliesTo, registry.Guards[0].AppliesTo[0])
			},
			want: "guard[0] duplicate applies_to surface \"internal/archtest\"",
		},
		{
			name: "unknown guard applies_to",
			mutate: func(registry *archtest.BackendBoundaryRegistry) {
				registry.Guards[0].AppliesTo = []archtest.BoundarySurfaceID{"internal/missing"}
			},
			want: "guard \"backend_surface_governance\" applies_to unknown backend surface \"internal/missing\"",
		},
		{
			name: "missing reverse guard reference",
			mutate: func(registry *archtest.BackendBoundaryRegistry) {
				registry.Guards[0].AppliesTo = append(registry.Guards[0].AppliesTo, "cmd/tool")
			},
			want: "guard \"backend_surface_governance\" applies_to surface \"cmd/tool\" but surface does not reference guard",
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
	assertDefaultGovernanceFixtureComplete(t, first)
	first.Guards[0].TestNames[0] = "TestMutated"
	first.Guards[0].AppliesTo[0] = "internal/mutated"
	first.Surfaces[0].RuleIDs[0] = "mutated_rule"
	assertGovernanceNestedSlicesUnchanged(t, second)
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

func assertDefaultGovernanceFixtureComplete(t *testing.T, registry archtest.BackendBoundaryRegistry) {
	t.Helper()
	if len(registry.Guards) == 0 || len(registry.Surfaces) == 0 {
		t.Fatalf("default governance fixture has no guards or surfaces: %#v", registry)
	}
	if len(registry.Guards[0].TestNames) == 0 || len(registry.Guards[0].AppliesTo) == 0 {
		t.Fatalf("default governance guard fixture is incomplete: %#v", registry.Guards[0])
	}
	if len(registry.Surfaces[0].RuleIDs) == 0 {
		t.Fatalf("default governance surface fixture is incomplete: %#v", registry.Surfaces[0])
	}
}

func assertGovernanceNestedSlicesUnchanged(t *testing.T, registry archtest.BackendBoundaryRegistry) {
	t.Helper()
	if registry.Guards[0].TestNames[0] == "TestMutated" {
		t.Fatal("default registry shares guard test names")
	}
	if registry.Guards[0].AppliesTo[0] == "internal/mutated" {
		t.Fatal("default registry shares guard applies_to surfaces")
	}
	if registry.Surfaces[0].RuleIDs[0] == "mutated_rule" {
		t.Fatal("default registry shares surface rule IDs")
	}
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
			assertGuardAppliesToSurface(t, guard, surfacePath)
		}
	}
}

func assertGuardAppliesToSurface(t *testing.T, guard archtest.BackendBoundaryGuard, surface string) {
	t.Helper()
	for _, appliesTo := range guard.AppliesTo {
		if string(appliesTo) == surface {
			return
		}
	}
	t.Errorf("surface %q guard %q does not declare applies_to", surface, guard.ID)
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
	registry.Guards = []archtest.BackendBoundaryGuard{{
		ID:        "external_guard",
		File:      "internal/archtest/guard_test.go",
		TestNames: []string{"TestExternalGuard"},
		AppliesTo: []archtest.BoundarySurfaceID{"internal/archtest"},
		Reason:    "synthetic external guard",
	}}
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
