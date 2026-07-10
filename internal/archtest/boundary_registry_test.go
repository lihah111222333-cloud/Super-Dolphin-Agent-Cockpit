package archtest_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/archtest"
)

const (
	boundaryRuleProviderNoStore      = "provider_no_store"
	boundaryRuleProviderNoPlatformDB = "provider_no_platform_db"
	boundaryRulePlatformNoModule     = "platform_no_module"
	boundaryRulePlatformNoStore      = "platform_no_store"
	boundaryRuleMCPOrchAllowed       = "mcp_orch_allowed_internal_boundary"
)

type boundaryRegistry struct {
	Owners []boundaryOwner
	Rules  []boundaryImportRule
}

type boundaryOwner struct {
	ID     string
	Roots  []string
	Reason string
}

type boundaryImportRule struct {
	ID                       string
	Owner                    string
	Reason                   string
	AllowedImportPrefixes    []string
	DisallowedImportPrefixes []string
	Exceptions               []boundaryException
	SkipTestFiles            bool
}

type boundaryException struct {
	RelPath      string
	ImportPrefix string
	Reason       string
	Temporary    bool
	RemoveWhen   string
}

func defaultBoundaryRegistry() boundaryRegistry {
	return boundaryRegistry{
		Owners: []boundaryOwner{
			{
				ID:     "provider_runtime",
				Roots:  []string{"internal/provider/claudecli", "internal/provider/codexapp", "internal/provider/unified"},
				Reason: "provider adapters own transport/runtime integration and must not reach into persistence internals",
			},
			{
				ID:     "platform_runtime",
				Roots:  []string{"internal/platform"},
				Reason: "platform packages provide infrastructure primitives and must not depend upward on business/store ownership",
			},
			{
				ID:     "mcp_orch_sidecar",
				Roots:  []string{"cmd/mcp-orch"},
				Reason: "mcp-orch is a sidecar entrypoint with a narrow shared internal import surface",
			},
		},
		Rules: []boundaryImportRule{
			{
				ID:                       boundaryRuleProviderNoStore,
				Owner:                    "provider_runtime",
				Reason:                   "provider adapters consume session contracts and must not import product store packages directly",
				DisallowedImportPrefixes: []string{"internal/store"},
			},
			{
				ID:                       boundaryRuleProviderNoPlatformDB,
				Owner:                    "provider_runtime",
				Reason:                   "provider production code must not own SQLite handles or DB lifecycle",
				DisallowedImportPrefixes: []string{"internal/platform/db"},
				SkipTestFiles:            true,
			},
			{
				ID:                       boundaryRulePlatformNoModule,
				Owner:                    "platform_runtime",
				Reason:                   "platform infrastructure stays below business modules",
				DisallowedImportPrefixes: []string{"internal/module"},
			},
			{
				ID:                       boundaryRulePlatformNoStore,
				Owner:                    "platform_runtime",
				Reason:                   "platform infrastructure must not depend on product store subpackages",
				DisallowedImportPrefixes: []string{"internal/store"},
				SkipTestFiles:            true,
			},
			{
				ID:     boundaryRuleMCPOrchAllowed,
				Owner:  "mcp_orch_sidecar",
				Reason: "mcp-orch may share explicit cross-cutting contracts and platform primitives, but not app/runtime host internals",
				AllowedImportPrefixes: []string{
					"internal/contract", "internal/dto", "internal/platform/config",
					"internal/platform/db", "internal/platform/bus", "internal/platform/discovery", "internal/platform/notify", "internal/platform/runner",
					"internal/platform/rpc", "internal/platform/runtimesafe", "internal/platform/securefs", "internal/platform/shared", "internal/platform/statemachine", "internal/platform/eventsurface", "internal/platform/metrics",
					"internal/platform/rlimit", "internal/platform/runtimeenv", "internal/platform/sharedfilefs", "internal/platform/sharedfilegitignore", "internal/platform/sharedfilepath",
					"internal/mcpserver/common",
					"internal/util",
				},
				DisallowedImportPrefixes: []string{
					"internal/app", "cmd/agent-terminal", "cmd/mcp-lsp", "cmd/mcp-ida",
					"internal/platform/rpc/server", "internal/platform/rpc/push", "internal/platform/rpc/notification",
				},
			},
		},
	}
}

func TestBoundaryRegistryValidation(t *testing.T) {
	if violations := validateBoundaryRegistry(defaultBoundaryRegistry()); len(violations) > 0 {
		t.Fatalf("boundary registry is invalid:\n%s", strings.Join(violations, "\n"))
	}
}

func TestBoundaryRegistryRejectsInvalidEntries(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*boundaryRegistry)
		want   string
	}{
		{
			name: "missing owner",
			mutate: func(reg *boundaryRegistry) {
				reg.Rules = append(reg.Rules, boundaryImportRule{ID: "missing_owner", Reason: "has reason", DisallowedImportPrefixes: []string{"internal/store"}})
			},
			want: "owner is empty",
		},
		{
			name: "unknown owner",
			mutate: func(reg *boundaryRegistry) {
				reg.Rules = append(reg.Rules, boundaryImportRule{ID: "unknown_owner", Owner: "missing", Reason: "has reason", DisallowedImportPrefixes: []string{"internal/store"}})
			},
			want: "unknown owner",
		},
		{
			name: "missing prefix",
			mutate: func(reg *boundaryRegistry) {
				reg.Rules = append(reg.Rules, boundaryImportRule{ID: "missing_prefix", Owner: "provider_runtime", Reason: "has reason"})
			},
			want: "must declare allowed or disallowed import prefixes",
		},
		{
			name: "temporary exception missing remove_when",
			mutate: func(reg *boundaryRegistry) {
				reg.Rules[0].Exceptions = append(reg.Rules[0].Exceptions, boundaryException{
					RelPath:      "internal/provider/codexapp/example.go",
					ImportPrefix: "internal/store/thread",
					Reason:       "synthetic temporary exception",
					Temporary:    true,
				})
			},
			want: "temporary exception missing remove_when",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := defaultBoundaryRegistry()
			tc.mutate(&reg)
			violations := strings.Join(validateBoundaryRegistry(reg), "\n")
			if !strings.Contains(violations, tc.want) {
				t.Fatalf("validateBoundaryRegistry() missing %q in:\n%s", tc.want, violations)
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

func mustBoundaryRule(t *testing.T, id string) boundaryImportRule {
	t.Helper()
	for _, rule := range defaultBoundaryRegistry().Rules {
		if rule.ID == id {
			return rule
		}
	}
	t.Fatalf("boundary rule %q is not registered", id)
	return boundaryImportRule{}
}

func mustBoundaryOwner(t *testing.T, id string) boundaryOwner {
	t.Helper()
	for _, owner := range defaultBoundaryRegistry().Owners {
		if owner.ID == id {
			return owner
		}
	}
	t.Fatalf("boundary owner %q is not registered", id)
	return boundaryOwner{}
}

func parseBoundaryRuleFiles(t *testing.T, root string, rule boundaryImportRule) []parsedFile {
	t.Helper()
	owner := mustBoundaryOwner(t, rule.Owner)
	dirs := existingDirs(root, owner.Roots...)
	if len(dirs) == 0 {
		t.Skipf("boundary owner %s roots not yet created", owner.ID)
	}
	files := parseImportFiles(t, root, dirs...)
	if !rule.SkipTestFiles {
		return files
	}
	filtered := files[:0]
	for _, file := range files {
		if !strings.HasSuffix(file.RelPath, "_test.go") {
			filtered = append(filtered, file)
		}
	}
	return filtered
}

func assertBoundaryNoDisallowedImports(t *testing.T, files []parsedFile, rule boundaryImportRule) {
	t.Helper()
	var violations []string
	for _, file := range files {
		violations = append(violations, boundaryDisallowedImportViolations(file, rule)...)
	}
	failIfViolations(t, violations)
}

func boundaryDisallowedImportViolations(file parsedFile, rule boundaryImportRule) []string {
	var violations []string
	for _, imp := range file.Imports {
		for _, prefix := range boundaryImportPrefixes(rule.DisallowedImportPrefixes) {
			if !importPathMatchesPrefix(imp, prefix) {
				continue
			}
			if boundaryExceptionAllows(rule, file, imp) {
				break
			}
			violations = append(violations, fmt.Sprintf("%s imports %s (rule=%s owner=%s)", file.RelPath, imp, rule.ID, rule.Owner))
			break
		}
	}
	return violations
}

func boundaryExceptionAllows(rule boundaryImportRule, file parsedFile, imp string) bool {
	for _, exception := range rule.Exceptions {
		if exception.RelPath != "" && exception.RelPath != file.RelPath {
			continue
		}
		if importPathMatchesPrefix(imp, boundaryImportPrefix(exception.ImportPrefix)) {
			return true
		}
	}
	return false
}

func boundaryImportPrefixes(prefixes []string) []string {
	out := make([]string, 0, len(prefixes))
	for _, prefix := range prefixes {
		out = append(out, boundaryImportPrefix(prefix))
	}
	return out
}

func boundaryImportPrefix(prefix string) string {
	prefix = strings.Trim(prefix, "/")
	switch {
	case strings.HasPrefix(prefix, "internal/"):
		return internalPrefix(prefix)
	case strings.HasPrefix(prefix, "cmd/"):
		return modulePath + "/" + prefix
	default:
		return prefix
	}
}

func importPathMatchesPrefix(path, prefix string) bool {
	trimmed := strings.TrimRight(prefix, "/")
	return path == trimmed || strings.HasPrefix(path, trimmed+"/")
}

func validateBoundaryRegistry(reg boundaryRegistry) []string {
	owners, violations := validateBoundaryOwners(reg.Owners)
	violations = append(violations, validateBoundaryRules(reg.Rules, owners)...)
	return violations
}

func validateBoundaryOwners(regOwners []boundaryOwner) (map[string]boundaryOwner, []string) {
	var violations []string
	owners := map[string]boundaryOwner{}
	for i, owner := range regOwners {
		label := fmt.Sprintf("owner[%d]", i)
		if strings.TrimSpace(owner.ID) == "" {
			violations = append(violations, label+" id is empty")
			continue
		}
		if _, ok := owners[owner.ID]; ok {
			violations = append(violations, label+" duplicate owner "+owner.ID)
		}
		owners[owner.ID] = owner
		if len(owner.Roots) == 0 {
			violations = append(violations, label+" roots are empty")
		}
		if strings.TrimSpace(owner.Reason) == "" {
			violations = append(violations, label+" reason is empty")
		}
		for j, root := range owner.Roots {
			if strings.TrimSpace(root) == "" {
				violations = append(violations, fmt.Sprintf("%s roots[%d] is empty", label, j))
			}
		}
	}
	return owners, violations
}

func validateBoundaryRules(rules []boundaryImportRule, owners map[string]boundaryOwner) []string {
	var violations []string
	ruleIDs := map[string]bool{}
	for i, rule := range rules {
		label := fmt.Sprintf("rule[%d]", i)
		if strings.TrimSpace(rule.ID) == "" {
			violations = append(violations, label+" id is empty")
		}
		if ruleIDs[rule.ID] {
			violations = append(violations, label+" duplicate rule "+rule.ID)
		}
		ruleIDs[rule.ID] = true
		if strings.TrimSpace(rule.Owner) == "" {
			violations = append(violations, label+" owner is empty")
		} else if _, ok := owners[rule.Owner]; !ok {
			violations = append(violations, label+" unknown owner "+rule.Owner)
		}
		if strings.TrimSpace(rule.Reason) == "" {
			violations = append(violations, label+" reason is empty")
		}
		if len(rule.AllowedImportPrefixes) == 0 && len(rule.DisallowedImportPrefixes) == 0 {
			violations = append(violations, label+" must declare allowed or disallowed import prefixes")
		}
		violations = append(violations, validateBoundaryPrefixes(label+".allowed", rule.AllowedImportPrefixes)...)
		violations = append(violations, validateBoundaryPrefixes(label+".disallowed", rule.DisallowedImportPrefixes)...)
		for j, exception := range rule.Exceptions {
			violations = append(violations, validateBoundaryException(fmt.Sprintf("%s.exception[%d]", label, j), exception)...)
		}
	}
	return violations
}

func validateBoundaryPrefixes(label string, prefixes []string) []string {
	var violations []string
	for i, prefix := range prefixes {
		if strings.TrimSpace(prefix) == "" {
			violations = append(violations, fmt.Sprintf("%s[%d] is empty", label, i))
		}
	}
	return violations
}

func validateBoundaryException(label string, exception boundaryException) []string {
	var violations []string
	if strings.TrimSpace(exception.ImportPrefix) == "" {
		violations = append(violations, label+" import_prefix is empty")
	}
	if strings.TrimSpace(exception.Reason) == "" {
		violations = append(violations, label+" reason is empty")
	}
	if exception.Temporary && strings.TrimSpace(exception.RemoveWhen) == "" {
		violations = append(violations, label+" temporary exception missing remove_when")
	}
	return violations
}
