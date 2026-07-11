package archtest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	archtest "github.com/anthropic-ai/super-agent-v3/internal/archtest"
)

const moduleNoStoreImportsRuleID archtest.BoundaryRuleID = "module_no_store_imports"

func TestModuleNoStoreImportsUsesCanonicalRule(t *testing.T) {
	rule, ok := archtest.DefaultBackendBoundaryRegistry().Rule(moduleNoStoreImportsRuleID)
	if !ok {
		t.Fatalf("canonical rule %q is missing", moduleNoStoreImportsRuleID)
	}
	assertModuleNoStoreRuleDescriptor(t, rule)
	assertModuleNoStoreRuleDenyPolicy(t, rule)
}

func assertModuleNoStoreRuleDescriptor(t *testing.T, rule archtest.BackendBoundaryRule) {
	t.Helper()
	if rule.Owner != "module_boundary" {
		t.Fatalf("rule owner = %q, want module_boundary", rule.Owner)
	}
	if rule.Kind != archtest.BoundaryRuleDenyImports {
		t.Fatalf("rule kind = %q, want %q", rule.Kind, archtest.BoundaryRuleDenyImports)
	}
	if len(rule.FilePatterns) != 1 || rule.FilePatterns[0] != "internal/module/**/*.go" {
		t.Fatalf("rule file patterns = %v", rule.FilePatterns)
	}
	if len(rule.Exceptions) != 0 {
		t.Fatalf("rule exceptions = %v, want none", rule.Exceptions)
	}
	if !rule.SkipTestFiles {
		t.Fatal("rule must skip test files")
	}
}

func assertModuleNoStoreRuleDenyPolicy(t *testing.T, rule archtest.BackendBoundaryRule) {
	t.Helper()
	if len(rule.Deny) != 1 ||
		rule.Deny[0].Owner != "module_boundary" ||
		rule.Deny[0].FilePattern != "internal/module/**/*.go" ||
		rule.Deny[0].ImportPrefix != "internal/store" {
		t.Fatalf("rule deny policies = %v", rule.Deny)
	}
}

func TestModuleNoStoreImportsRejectsFixture(t *testing.T) {
	root := t.TempDir()
	relPath := "internal/module/example/leak.go"
	absPath := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatal(err)
	}
	const source = "package example\n\nimport _ \"github.com/anthropic-ai/super-agent-v3/internal/store/thread\"\n"
	if err := os.WriteFile(absPath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	violations, err := archtest.EvaluateBackendBoundaryFile(
		absPath,
		relPath,
		archtest.DefaultBackendBoundaryRegistry(),
		moduleNoStoreImportsRuleID,
	)
	if err != nil {
		t.Fatalf("canonical rule %q missing or invalid: %v", moduleNoStoreImportsRuleID, err)
	}
	if len(violations) == 0 {
		t.Fatalf("canonical rule %q returned no violation", moduleNoStoreImportsRuleID)
	}
}

func TestModuleNoStoreImportsCoversProductionTree(t *testing.T) {
	evaluation, err := archtest.EvaluateBackendBoundary(
		repoRoot(t),
		archtest.DefaultBackendBoundaryRegistry(),
		moduleNoStoreImportsRuleID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.MatchedFiles == 0 || evaluation.ByRule[moduleNoStoreImportsRuleID] == 0 {
		t.Fatalf("canonical rule matched zero production files: %#v", evaluation)
	}
	if len(evaluation.Violations) != 0 {
		t.Fatalf("module production imports Store implementations:\n%s", strings.Join(evaluation.Violations, "\n"))
	}
}

func TestModuleNoStoreImportsCoversFutureModulesWithoutMatchingStorex(t *testing.T) {
	root := t.TempDir()
	relPath := "internal/module/future/service.go"
	absPath := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatal(err)
	}
	const source = "package future\n\nimport _ \"github.com/anthropic-ai/super-agent-v3/internal/storex/cache\"\n"
	if err := os.WriteFile(absPath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	evaluation, err := archtest.EvaluateBackendBoundary(
		root,
		archtest.DefaultBackendBoundaryRegistry(),
		moduleNoStoreImportsRuleID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.MatchedFiles != 1 || evaluation.ByRule[moduleNoStoreImportsRuleID] != 1 {
		t.Fatalf("future module candidate coverage = %#v", evaluation)
	}
	if len(evaluation.Violations) != 0 {
		t.Fatalf("internal/storex sibling was misclassified as internal/store: %v", evaluation.Violations)
	}
}
