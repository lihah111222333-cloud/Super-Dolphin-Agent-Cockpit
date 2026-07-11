package archtest_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const appModulesFile = "internal/app/modules.go"

var requiredAppAdapterDirs = []string{
	"internal/app/storeadapter/cron",
	"internal/app/storeadapter/dashboard",
	"internal/app/storeadapter/datasourcev2",
	"internal/app/storeadapter/feedback",
	"internal/app/storeadapter/insight",
	"internal/app/storeadapter/memory",
	"internal/app/storeadapter/personalization",
	"internal/app/storeadapter/prompt",
	"internal/app/storeadapter/skill",
	"internal/app/storeadapter/thread",
	"internal/app/storeadapter/turn",
	"internal/app/storeadapter/uistate",
	"internal/app/runtimeadapter/mcpcontrol",
	"internal/app/runtimeadapter/toolbridge",
	"internal/app/runtimeadapter/cachekeepalive",
	"internal/app/runtimeadapter/builtintools",
}

// TestAppRootDoesNotOwnLeafStoreAdapters 固化 app root 只装配两个 adapter aggregator 的边界。
func TestAppRootDoesNotOwnLeafStoreAdapters(t *testing.T) {
	root := repoRoot(t)
	files := appRootProductionImportFiles(t, root)
	violations := validateAppRootStoreImports(files)
	violations = append(violations, validateAppRootAggregatorImports(files)...)
	violations = append(violations, missingAppAdapterDirs(root)...)
	failIfViolations(t, violations)
}

func appRootProductionImportFiles(t *testing.T, root string) []parsedFile {
	t.Helper()
	relRoot := "internal/app"
	entries, err := os.ReadDir(filepath.Join(root, relRoot))
	if err != nil {
		t.Fatalf("read %s: %v", relRoot, err)
	}
	files := make([]parsedFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		relPath := filepath.ToSlash(filepath.Join(relRoot, entry.Name()))
		absPath := filepath.Join(root, relPath)
		files = append(files, parsedFile{AbsPath: absPath, RelPath: relPath, Imports: parseImports(t, absPath)})
	}
	return files
}

func validateAppRootStoreImports(files []parsedFile) []string {
	storeRoot := internalPrefix("internal/store")
	var canonicalImporters []string
	var violations []string
	for _, file := range files {
		for _, imported := range file.Imports {
			switch {
			case imported == storeRoot:
				canonicalImporters = append(canonicalImporters, file.RelPath)
				if file.RelPath != appModulesFile {
					violations = append(violations, fmt.Sprintf("%s imports canonical store root", file.RelPath))
				}
			case strings.HasPrefix(imported, storeRoot+"/"):
				violations = append(violations, fmt.Sprintf("%s imports leaf store %s", file.RelPath, imported))
			}
		}
	}
	if strings.Join(canonicalImporters, ",") != appModulesFile {
		violations = append(violations, fmt.Sprintf("canonical store root importers=%v, want [%s]", canonicalImporters, appModulesFile))
	}
	return violations
}

func validateAppRootAggregatorImports(files []parsedFile) []string {
	storeAggregator := internalPrefix("internal/app/storeadapter")
	runtimeAggregator := internalPrefix("internal/app/runtimeadapter")
	seenStoreAggregator := false
	seenRuntimeAggregator := false
	var violations []string
	for _, file := range files {
		for _, imported := range file.Imports {
			switch {
			case imported == storeAggregator:
				seenStoreAggregator = true
			case imported == runtimeAggregator:
				seenRuntimeAggregator = true
			case strings.HasPrefix(imported, storeAggregator+"/") || strings.HasPrefix(imported, runtimeAggregator+"/"):
				violations = append(violations, fmt.Sprintf("%s imports adapter child %s", file.RelPath, imported))
			}
		}
	}
	if !seenStoreAggregator {
		violations = append(violations, "app root does not import storeadapter aggregator")
	}
	if !seenRuntimeAggregator {
		violations = append(violations, "app root does not import runtimeadapter aggregator")
	}
	return violations
}

func missingAppAdapterDirs(root string) []string {
	var violations []string
	for _, relPath := range requiredAppAdapterDirs {
		if !dirExists(root, relPath) {
			violations = append(violations, "required adapter directory missing: "+relPath)
		}
	}
	return violations
}
