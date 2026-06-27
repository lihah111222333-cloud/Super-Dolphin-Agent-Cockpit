package archtest_test

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var moduleStoreImportAllowlist = map[string]map[string]string{}

type moduleStoreImportCollection struct {
	Legacy         []string
	Unknown        []string
	StaleAllowlist []string
}

func TestCollectModuleStoreImports(t *testing.T) {
	legacyFile := "internal/module/thread/service.go"
	legacyImport := internalPrefix("internal/store/thread")
	unknownImport := internalPrefix("internal/store/feedback")
	allowlist := map[string]map[string]string{
		legacyFile: {
			legacyImport: "legacy fixture",
		},
	}

	t.Run("stale_when_legacy_import_removed_but_allowlist_kept", func(t *testing.T) {
		got := collectModuleStoreImports([]parsedFile{
			{RelPath: legacyFile, Imports: nil},
		}, allowlist)

		if len(got.Legacy) != 0 || len(got.Unknown) != 0 {
			t.Fatalf("unexpected non-stale results: legacy=%v unknown=%v", got.Legacy, got.Unknown)
		}
		want := moduleStoreImportDescription(legacyFile, legacyImport)
		if !slices.Contains(got.StaleAllowlist, want) {
			t.Fatalf("stale allowlist missing %q in %v", want, got.StaleAllowlist)
		}
	})

	t.Run("unknown_and_stale_survive_net_count_replacement", func(t *testing.T) {
		got := collectModuleStoreImports([]parsedFile{
			{RelPath: legacyFile, Imports: []string{unknownImport}},
		}, allowlist)

		wantUnknown := moduleStoreImportDescription(legacyFile, unknownImport)
		if !slices.Contains(got.Unknown, wantUnknown) {
			t.Fatalf("unknown import missing %q in %v", wantUnknown, got.Unknown)
		}
		wantStale := moduleStoreImportDescription(legacyFile, legacyImport)
		if !slices.Contains(got.StaleAllowlist, wantStale) {
			t.Fatalf("stale allowlist missing %q in %v", wantStale, got.StaleAllowlist)
		}
		if len(got.Legacy) != 0 {
			t.Fatalf("replacement should not leave legacy imports: %v", got.Legacy)
		}
	})
}

// assertModuleNoStoreImports 阻断 module 非装配文件继续直接 import store。
// 未登记 import 与过期 allowlist 都会失败；默认空 allowlist 代表目标违规面为零。
func assertModuleNoStoreImports(t *testing.T, files []parsedFile) {
	t.Helper()
	collected := collectModuleStoreImports(files, moduleStoreImportAllowlist)
	var violations []string
	if len(collected.Unknown) > 0 {
		violations = append(violations, fmt.Sprintf("unknown module store imports (%d):\n%s", len(collected.Unknown), strings.Join(collected.Unknown, "\n")))
	}
	if len(collected.StaleAllowlist) > 0 {
		violations = append(violations, fmt.Sprintf("stale moduleStoreImportAllowlist entries (%d):\n%s", len(collected.StaleAllowlist), strings.Join(collected.StaleAllowlist, "\n")))
	}
	failIfViolations(t, violations)
}

// collectModuleStoreImports 按 file + import path 精确区分 legacy、unknown 与 stale allowlist。
// 该 collector 显式跳过测试文件与 module.go，避免 assembly/test import 污染解耦预算。
func collectModuleStoreImports(files []parsedFile, allowlist map[string]map[string]string) moduleStoreImportCollection {
	seen := make(map[string]map[string]bool)
	var collected moduleStoreImportCollection
	for _, file := range files {
		collectModuleStoreImportsFromFile(file, allowlist, seen, &collected)
	}
	appendStaleModuleStoreAllowlist(allowlist, seen, &collected)
	slices.Sort(collected.Legacy)
	slices.Sort(collected.Unknown)
	slices.Sort(collected.StaleAllowlist)
	return collected
}

func collectModuleStoreImportsFromFile(file parsedFile, allowlist map[string]map[string]string, seen map[string]map[string]bool, collected *moduleStoreImportCollection) {
	if skipModuleStoreImportFile(file.RelPath) {
		return
	}
	for _, imp := range file.Imports {
		collectModuleStoreImport(file.RelPath, imp, allowlist, seen, collected)
	}
}

func collectModuleStoreImport(relPath, imp string, allowlist map[string]map[string]string, seen map[string]map[string]bool, collected *moduleStoreImportCollection) {
	if !strings.HasPrefix(imp, moduleStoreImportPrefix) {
		return
	}
	if seen[relPath] == nil {
		seen[relPath] = make(map[string]bool)
	}
	seen[relPath][imp] = true
	if moduleStoreImportAllowed(relPath, imp, allowlist) {
		collected.Legacy = append(collected.Legacy, moduleStoreImportDescription(relPath, imp))
		return
	}
	collected.Unknown = append(collected.Unknown, moduleStoreImportDescription(relPath, imp))
}

func skipModuleStoreImportFile(relPath string) bool {
	if strings.HasSuffix(relPath, "_test.go") || filepath.Base(relPath) == "module.go" {
		return true
	}
	return !strings.HasPrefix(filepath.ToSlash(relPath), "internal/module/")
}

func moduleStoreImportAllowed(relPath, imp string, allowlist map[string]map[string]string) bool {
	allowedImports, ok := allowlist[relPath]
	if !ok {
		return false
	}
	_, ok = allowedImports[imp]
	return ok
}

func appendStaleModuleStoreAllowlist(allowlist map[string]map[string]string, seen map[string]map[string]bool, collected *moduleStoreImportCollection) {
	for relPath, imports := range allowlist {
		for imp := range imports {
			if seen[relPath] != nil && seen[relPath][imp] {
				continue
			}
			collected.StaleAllowlist = append(collected.StaleAllowlist, moduleStoreImportDescription(relPath, imp))
		}
	}
}

func moduleStoreImportDescription(relPath, imp string) string {
	return fmt.Sprintf("%s imports %s", relPath, imp)
}
