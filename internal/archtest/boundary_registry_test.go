package archtest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest"
)

func TestBoundaryRegistryValidation(t *testing.T) {
	registry := archtest.DefaultBackendBoundaryRegistry()
	if violations := archtest.ValidateBackendBoundaryRegistry(registry); len(violations) > 0 {
		t.Fatalf("canonical backend boundary registry is invalid:\n%s", strings.Join(violations, "\n"))
	}
}

func TestBoundaryRegistryRejectsInvalidEntries(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*archtest.BackendBoundaryRegistry)
		want   string
	}{
		{
			name: "missing owner",
			mutate: func(registry *archtest.BackendBoundaryRegistry) {
				registry.Rules = append(registry.Rules, archtest.BackendBoundaryRule{
					ID:           "missing_owner",
					Reason:       "synthetic invalid rule",
					Kind:         archtest.BoundaryRuleDenyImports,
					FilePatterns: []string{"internal/provider/**/*.go"},
					Deny: []archtest.BoundaryImportPolicy{{
						Owner:        "provider_runtime",
						FilePattern:  "internal/provider/**/*.go",
						ImportPrefix: "internal/store",
						Reason:       "synthetic invalid policy",
					}},
				})
			},
			want: "owner is empty",
		},
		{
			name: "unknown owner",
			mutate: func(registry *archtest.BackendBoundaryRegistry) {
				registry.Rules = append(registry.Rules, archtest.BackendBoundaryRule{
					ID:           "unknown_owner",
					Owner:        "missing",
					Reason:       "synthetic invalid rule",
					Kind:         archtest.BoundaryRuleDenyImports,
					FilePatterns: []string{"internal/provider/**/*.go"},
					Deny: []archtest.BoundaryImportPolicy{{
						Owner:        "missing",
						FilePattern:  "internal/provider/**/*.go",
						ImportPrefix: "internal/store",
						Reason:       "synthetic invalid policy",
					}},
				})
			},
			want: "unknown owner",
		},
		{
			name: "missing deny policy",
			mutate: func(registry *archtest.BackendBoundaryRegistry) {
				registry.Rules[0].Deny = nil
			},
			want: "must declare deny policies",
		},
		{
			name: "temporary exception missing remove_when",
			mutate: func(registry *archtest.BackendBoundaryRegistry) {
				rule := &registry.Rules[0]
				rule.Exceptions = append(rule.Exceptions, archtest.BoundaryException{
					ID:           "synthetic_temporary_exception",
					Owner:        rule.Owner,
					FilePattern:  "internal/contract/example.go",
					ImportPrefix: "internal/store/thread",
					Class:        archtest.BoundaryExceptionTemporary,
					Reason:       "synthetic temporary exception",
				})
			},
			want: "temporary exception missing remove_when",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			registry := archtest.DefaultBackendBoundaryRegistry()
			tc.mutate(&registry)
			violations := strings.Join(archtest.ValidateBackendBoundaryRegistry(registry), "\n")
			if !strings.Contains(violations, tc.want) {
				t.Fatalf("ValidateBackendBoundaryRegistry() missing %q in:\n%s", tc.want, violations)
			}
		})
	}
}

func TestBoundaryRegistryRejectsDetachedImportPolicies(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*archtest.BackendBoundaryRegistry)
		want   string
	}{
		{name: "owner differs from rule owner", mutate: func(registry *archtest.BackendBoundaryRegistry) {
			rule := mustMutableBackendBoundaryRule(t, registry, "provider_no_store")
			rule.Deny[0].Owner = "module_boundary"
		}, want: "owner must match rule owner"},
		{name: "file pattern outside rule", mutate: func(registry *archtest.BackendBoundaryRegistry) {
			rule := mustMutableBackendBoundaryRule(t, registry, "provider_no_store")
			rule.Deny[0].FilePattern = "internal/module/**/*.go"
		}, want: "file_pattern must be registered in rule file_patterns"},
		{name: "unsupported rule glob", mutate: func(registry *archtest.BackendBoundaryRegistry) {
			registry.Rules[0].FilePatterns[0] = "internal/contract/*.go"
		}, want: "unsupported file pattern syntax"},
		{name: "stateful sidecar ancestor allowance", mutate: func(registry *archtest.BackendBoundaryRegistry) {
			rule := mustMutableBackendBoundaryRule(t, registry, "mcp_sidecar_narrow_import_surface")
			rule.Allow = append(rule.Allow, archtest.BoundaryImportPolicy{
				Owner: rule.Owner, FilePattern: rule.FilePatterns[0], ImportPrefix: "internal/platform",
				Reason: "orchestration sidecar broad runtime primitive",
			})
		}, want: "stateful sidecar allowance must not use ancestor import prefix"},
		{name: "leading slash import prefix", mutate: func(registry *archtest.BackendBoundaryRegistry) {
			rule := mustMutableBackendBoundaryRule(t, registry, "mcp_sidecar_narrow_import_surface")
			rule.Allow = append(rule.Allow, archtest.BoundaryImportPolicy{
				Owner: rule.Owner, FilePattern: rule.FilePatterns[0], ImportPrefix: "/internal/platform",
				Reason: "orchestration sidecar broad runtime primitive",
			})
		}, want: "import_prefix must use canonical form"},
		{name: "module path import prefix", mutate: func(registry *archtest.BackendBoundaryRegistry) {
			rule := mustMutableBackendBoundaryRule(t, registry, "mcp_sidecar_narrow_import_surface")
			rule.Allow = append(rule.Allow, archtest.BoundaryImportPolicy{
				Owner: rule.Owner, FilePattern: rule.FilePatterns[0],
				ImportPrefix: "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform",
				Reason:       "orchestration sidecar broad runtime primitive",
			})
		}, want: "import_prefix must use canonical form"},
		{name: "rule scope outside owner", mutate: func(registry *archtest.BackendBoundaryRegistry) {
			rule := mustMutableBackendBoundaryRule(t, registry, "contract_reverse_pollution")
			rule.FilePatterns = []string{"internal/module/**/*.go"}
			for i := range rule.Deny {
				rule.Deny[i].FilePattern = rule.FilePatterns[0]
			}
		}, want: "rule file_pattern must be registered in owner file_patterns"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			registry := archtest.DefaultBackendBoundaryRegistry()
			tc.mutate(&registry)
			violations := strings.Join(archtest.ValidateBackendBoundaryRegistry(registry), "\n")
			if !strings.Contains(violations, tc.want) {
				t.Fatalf("ValidateBackendBoundaryRegistry() missing %q in:\n%s", tc.want, violations)
			}
		})
	}
}

func TestBoundaryRegistryRejectsNonCanonicalImportPrefixes(t *testing.T) {
	cases := []string{
		" internal/store",
		"./internal/store",
		"internal//store",
		"internal\\store",
		"../internal/store",
	}
	for _, prefix := range cases {
		t.Run(prefix, func(t *testing.T) {
			registry := archtest.DefaultBackendBoundaryRegistry()
			rule := mustMutableBackendBoundaryRule(t, &registry, "provider_no_store")
			rule.Deny[0].ImportPrefix = prefix
			violations := strings.Join(archtest.ValidateBackendBoundaryRegistry(registry), "\n")
			if !strings.Contains(violations, "import_prefix must use canonical form") {
				t.Fatalf("non-canonical import prefix %q must fail validation, got:\n%s", prefix, violations)
			}
		})
	}
}

func TestBackendBoundaryRegistryValidation(t *testing.T) {
	registry := archtest.DefaultBackendBoundaryRegistry()
	if violations := archtest.ValidateBackendBoundaryRegistry(registry); len(violations) > 0 {
		t.Fatalf("canonical backend boundary registry is invalid:\n%s", strings.Join(violations, "\n"))
	}
	if len(registry.Owners) == 0 || len(registry.Rules) == 0 {
		t.Fatalf("canonical backend boundary registry must declare owners and rules: %#v", registry)
	}

	duplicateOwner := archtest.DefaultBackendBoundaryRegistry()
	duplicateOwner.Owners = append(duplicateOwner.Owners, duplicateOwner.Owners[0])
	if violations := strings.Join(archtest.ValidateBackendBoundaryRegistry(duplicateOwner), "\n"); !strings.Contains(violations, "duplicate owner") {
		t.Fatalf("duplicate owner must fail validation, got:\n%s", violations)
	}
}

func TestBackendBoundaryRegistryRejectsGenericStatefulSidecarAllowlist(t *testing.T) {
	registry := archtest.DefaultBackendBoundaryRegistry()
	rule, ok := registry.Rule("mcp_sidecar_narrow_import_surface")
	if !ok {
		t.Fatal("mcp sidecar rule is not registered")
	}
	for i := range rule.Allow {
		if rule.Allow[i].FilePattern == "cmd/mcp-orch/**/*.go" && rule.Allow[i].ImportPrefix == "internal/platform/db" {
			rule.Allow[i].Reason = "stateful runtime primitive"
			break
		}
	}
	for i := range registry.Rules {
		if registry.Rules[i].ID == rule.ID {
			registry.Rules[i] = rule
			break
		}
	}

	violations := strings.Join(archtest.ValidateBackendBoundaryRegistry(registry), "\n")
	if !strings.Contains(violations, "stateful sidecar allowance must name its sidecar") {
		t.Fatalf("generic stateful sidecar allowlist must fail validation, got:\n%s", violations)
	}
}

func TestBackendBoundaryRegistryRejectsBroadException(t *testing.T) {
	registry := archtest.DefaultBackendBoundaryRegistry()
	rule := mustMutableBackendBoundaryRule(t, &registry, "module_no_direct_db_imports")
	rule.Exceptions = append(rule.Exceptions, archtest.BoundaryException{
		ID:           "broad_module_database_exception",
		Owner:        rule.Owner,
		FilePattern:  "internal/module/**/*.go",
		ImportPrefix: "database/sql",
		Class:        archtest.BoundaryExceptionPermanent,
		Reason:       "synthetic broad exception",
	})
	violations := strings.Join(archtest.ValidateBackendBoundaryRegistry(registry), "\n")
	if !strings.Contains(violations, "exception file_pattern must be exact") {
		t.Fatalf("broad exception must fail validation, got:\n%s", violations)
	}
}

func TestBackendBoundaryRegistryRejectsBroadScopeAllow(t *testing.T) {
	registry := archtest.DefaultBackendBoundaryRegistry()
	rule := mustMutableBackendBoundaryRule(t, &registry, "store_sqlc_store_platform_only")
	rule.ScopeAllow = append(rule.ScopeAllow, archtest.BoundaryFilePolicy{
		Owner:       rule.Owner,
		FilePattern: "internal/module/**/*.go",
		Reason:      "synthetic broad scope allow",
	})
	violations := strings.Join(archtest.ValidateBackendBoundaryRegistry(registry), "\n")
	if !strings.Contains(violations, "scope_allow file_pattern is not a registered scope") {
		t.Fatalf("broad scope allow must fail validation, got:\n%s", violations)
	}
}

func mustMutableBackendBoundaryRule(t *testing.T, registry *archtest.BackendBoundaryRegistry, id archtest.BoundaryRuleID) *archtest.BackendBoundaryRule {
	t.Helper()
	for i := range registry.Rules {
		if registry.Rules[i].ID == id {
			return &registry.Rules[i]
		}
	}
	t.Fatalf("canonical backend boundary rule %q is not registered", id)
	return nil
}

func TestBackendBoundaryRegistryReturnsDeepCopy(t *testing.T) {
	first := archtest.DefaultBackendBoundaryRegistry()
	second := archtest.DefaultBackendBoundaryRegistry()
	if len(first.Owners) == 0 || len(first.Owners[0].FilePatterns) == 0 {
		t.Fatalf("registry fixture missing owner patterns: %#v", first)
	}
	first.Owners[0].FilePatterns[0] = "mutated/**/*.go"
	if second.Owners[0].FilePatterns[0] == "mutated/**/*.go" {
		t.Fatal("registry returns shared owner pattern slices")
	}
}

func TestBackendBoundaryEvaluatorFailsClosed(t *testing.T) {
	registry := archtest.DefaultBackendBoundaryRegistry()
	if _, err := archtest.EvaluateBackendBoundary(filepath.Join(t.TempDir(), "missing"), registry); err == nil {
		t.Fatal("missing root must return an error")
	}

	emptyRoot := t.TempDir()
	if _, err := archtest.EvaluateBackendBoundary(emptyRoot, registry, registry.Rules[0].ID); err == nil {
		t.Fatal("root without matching production Go files must return an error")
	}

	brokenRoot := t.TempDir()
	brokenFile := filepath.Join(brokenRoot, "internal", "contract", "broken.go")
	if err := os.MkdirAll(filepath.Dir(brokenFile), 0o755); err != nil {
		t.Fatalf("create broken fixture directory: %v", err)
	}
	if err := os.WriteFile(brokenFile, []byte("package contract\nfunc (\n"), 0o600); err != nil {
		t.Fatalf("write broken fixture: %v", err)
	}
	if _, err := archtest.EvaluateBackendBoundary(brokenRoot, registry, "contract_reverse_pollution"); err == nil {
		t.Fatal("malformed Go source must return an error")
	}
}

func TestBackendBoundaryRegistryCoversProductionTree(t *testing.T) {
	registry := archtest.DefaultBackendBoundaryRegistry()
	evaluation, err := archtest.EvaluateBackendBoundary(repoRoot(t), registry)
	if err != nil {
		t.Fatalf("evaluate production boundary registry: %v", err)
	}
	if evaluation.CandidateFiles == 0 {
		t.Fatal("production boundary evaluation matched zero candidate files")
	}
	for _, rule := range registry.Rules {
		if evaluation.ByRule[rule.ID] == 0 {
			t.Fatalf("rule %q matched zero production files", rule.ID)
		}
	}
	if len(evaluation.Violations) > 0 {
		t.Fatalf("production boundary violations:\n%s", strings.Join(evaluation.Violations, "\n"))
	}
}
