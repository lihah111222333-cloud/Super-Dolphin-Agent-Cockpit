package archtest_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest"
)

func TestLoggerRuntimeOwnersDoNotRegressToPackageGlobals(t *testing.T) {
	if violations := loggerRuntimeOwnerGlobalViolations(repoRoot(t), loggerRuntimeOwnerFiles()); len(violations) != 0 {
		t.Fatalf("logger runtime owner violations: %v", violations)
	}
}

func loggerRuntimeOwnerFiles() []string {
	return []string{
		"pkg/logger/logger.go",
		"pkg/logger/redact.go",
	}
}

func TestLoggerRuntimeOwnerGuardRejectsSyntheticPackageGlobal(t *testing.T) {
	root := t.TempDir()
	relative := "pkg/logger/redact.go"
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create synthetic logger package: %v", err)
	}
	if err := os.WriteFile(path, []byte("package logger\n\nvar logSecretPatterns = []string{\"secret\"}\n"), 0o600); err != nil {
		t.Fatalf("write synthetic logger runtime regression: %v", err)
	}

	want := relative + " has 1 mutable package globals, want 0"
	if violations := loggerRuntimeOwnerGlobalViolations(root, []string{relative}); !slices.Contains(violations, want) {
		t.Fatalf("synthetic package global was not rejected: got %v, want %q", violations, want)
	}
}

func loggerRuntimeOwnerGlobalViolations(root string, relatives []string) []string {
	violations := make([]string, 0)
	for _, relative := range relatives {
		if got := archtest.MeasureFileMetrics(filepath.Join(root, relative)).GlobalVars; got != 0 {
			violations = append(violations, fmt.Sprintf("%s has %d mutable package globals, want 0", relative, got))
		}
	}
	return violations
}
