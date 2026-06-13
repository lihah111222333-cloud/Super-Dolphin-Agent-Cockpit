package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultReportPathUsesTask14UTF8Location(t *testing.T) {
	want := filepath.FromSlash("docs/cc/数据库切换/sqlite-release-gate-report.md")
	if got := filepath.FromSlash(defaultReportPath); got != want {
		t.Fatalf("defaultReportPath = %q, want %q", got, want)
	}
	if strings.Contains(defaultReportPath, "鏁") || strings.Contains(defaultReportPath, "搴") {
		t.Fatalf("defaultReportPath contains mojibake: %q", defaultReportPath)
	}
}

func TestCleanRequiredFlagPathRejectsBlankBeforeClean(t *testing.T) {
	for _, raw := range []string{"", " \t "} {
		if got, err := cleanRequiredFlagPath("logs", raw); err == nil {
			t.Fatalf("cleanRequiredFlagPath(%q) = %q without error, want fail-fast", raw, got)
		}
	}
}

func TestCleanRequiredFlagPathCleansNonBlankValue(t *testing.T) {
	raw := filepath.FromSlash(".tmp/../sqlite-release-gate-logs")
	got, err := cleanRequiredFlagPath("logs", raw)
	if err != nil {
		t.Fatalf("cleanRequiredFlagPath returned error: %v", err)
	}
	if got != filepath.Clean(raw) {
		t.Fatalf("cleanRequiredFlagPath = %q, want %q", got, filepath.Clean(raw))
	}
}
