package archtest_test

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest"
)

// TestGuardFreezeBaselineIsCanonicalAuthority 锁定冻结债务唯一由版本化 JSON 承载。
func TestGuardFreezeBaselineIsCanonicalAuthority(t *testing.T) {
	root := repoRoot(t)
	archtestDir := filepath.Join(root, "internal", "archtest")
	freezePath := filepath.Join(archtestDir, "freeze_baseline.json")
	assertCanonicalGuardFreeze(t, freezePath)
	assertNoCompetingFreezeFiles(t, archtestDir)
}

func assertCanonicalGuardFreeze(t *testing.T, freezePath string) {
	t.Helper()
	fileInfo, err := os.Lstat(freezePath)
	if err != nil {
		t.Fatalf("canonical guard freeze %s is unavailable: %v", freezePath, err)
	}
	if !fileInfo.Mode().IsRegular() {
		t.Fatalf("canonical guard freeze %s must be a regular file", freezePath)
	}
	if _, err := archtest.LoadGuardFreeze(freezePath); err != nil {
		t.Fatalf("canonical guard freeze is invalid: %v", err)
	}
}

func assertNoCompetingFreezeFiles(t *testing.T, archtestDir string) {
	t.Helper()
	entries, err := os.ReadDir(archtestDir)
	if err != nil {
		t.Fatalf("read archtest directory: %v", err)
	}
	competingFiles := findCompetingFreezeFiles(entries)
	sort.Strings(competingFiles)
	if len(competingFiles) != 0 {
		t.Fatalf("competing freeze authority files under %s: %s; use freeze_baseline.json",
			archtestDir, strings.Join(competingFiles, ", "))
	}
}

func findCompetingFreezeFiles(entries []os.DirEntry) []string {
	var competingFiles []string
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "freeze_baseline.json" {
			continue
		}
		lowerName := strings.ToLower(entry.Name())
		if strings.Contains(lowerName, "freeze_registry") || strings.Contains(lowerName, "freeze_autofix") {
			competingFiles = append(competingFiles, entry.Name())
			continue
		}
		if filepath.Ext(lowerName) == ".json" &&
			(strings.Contains(lowerName, "freeze") || strings.Contains(lowerName, "baseline")) {
			competingFiles = append(competingFiles, entry.Name())
		}
	}
	return competingFiles
}

// TestGuardFreezeBaselineRejectsLegacyRegistryOwners 防止空 registry 或自动修复器重新成为第二冻结权威。
func TestGuardFreezeBaselineRejectsLegacyRegistryOwners(t *testing.T) {
	root := repoRoot(t)
	archtestDir := filepath.Join(root, "internal", "archtest")
	owners := findLegacyFreezeOwners(t, archtestDir)
	if len(owners) != 0 {
		t.Fatalf("legacy freeze registry owner(s) found: %s; freeze_baseline.json must remain the sole authority",
			strings.Join(owners, ", "))
	}
}

func findLegacyFreezeOwners(t *testing.T, archtestDir string) []string {
	t.Helper()
	forbidden := map[string]bool{
		"AutoRepairFreezeRegistry": true,
		"explicitFreezeRegistry":   true,
	}
	var owners []string

	entries, err := os.ReadDir(archtestDir)
	if err != nil {
		t.Fatalf("read archtest directory: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		owners = append(owners, freezeOwnersInSource(t, filepath.Join(archtestDir, entry.Name()), entry.Name(), forbidden)...)
	}
	sort.Strings(owners)
	return owners
}

func freezeOwnersInSource(t *testing.T, path, name string, forbidden map[string]bool) []string {
	t.Helper()
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	owners := make([]string, 0, len(forbidden))
	for symbol := range forbidden {
		if bytes.Contains(source, []byte(symbol)) {
			owners = append(owners, filepath.Join("internal", "archtest", name)+":"+symbol)
		}
	}
	return owners
}
