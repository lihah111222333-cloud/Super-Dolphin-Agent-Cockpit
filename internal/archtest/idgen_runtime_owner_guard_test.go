package archtest_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest"
)

func TestIDGeneratorRuntimeOwnerIsExplicit(t *testing.T) {
	root := repoRoot(t)
	if violations := idgenRuntimeOwnerViolations(root, []string{"internal/util/idgen/idgen.go"}); len(violations) != 0 {
		t.Fatalf("id generator runtime owner violations: %v", violations)
	}
	for _, relative := range []string{
		"internal/module/thread/module.go",
		"cmd/mcp-orch/fx_orchestration_execution.go",
	} {
		data, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil {
			t.Fatalf("read %s: %v", relative, err)
		}
		if !strings.Contains(string(data), "idgen.NewGenerator") {
			t.Fatalf("%s must provide the application-owned id generator", relative)
		}
	}
}

func TestIDGeneratorRuntimeOwnerGuardRejectsSyntheticPackageGlobal(t *testing.T) {
	root := t.TempDir()
	relative := "internal/util/idgen/idgen.go"
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create synthetic idgen package: %v", err)
	}
	if err := os.WriteFile(path, []byte("package idgen\n\nvar lastAgentIDValue uint64\n"), 0o600); err != nil {
		t.Fatalf("write synthetic idgen regression: %v", err)
	}
	want := relative + " has 1 mutable package globals, want 0"
	if violations := idgenRuntimeOwnerViolations(root, []string{relative}); !slices.Contains(violations, want) {
		t.Fatalf("synthetic package global was not rejected: got %v, want %q", violations, want)
	}
}

func idgenRuntimeOwnerViolations(root string, relatives []string) []string {
	violations := make([]string, 0)
	for _, relative := range relatives {
		if got := archtest.MeasureFileMetrics(filepath.Join(root, relative)).GlobalVars; got != 0 {
			violations = append(violations, fmt.Sprintf("%s has %d mutable package globals, want 0", relative, got))
		}
	}
	return violations
}
