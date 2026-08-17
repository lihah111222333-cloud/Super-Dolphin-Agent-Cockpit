package sqlcconfig

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// IsManualExtension reports whether a repository-relative file is the exact registered manual extension.
func IsManualExtension(unit Unit, relative string) bool {
	relative = path.Clean(filepath.ToSlash(relative))
	return slices.Contains(manualExtensionPaths(unit), relative)
}

// ManualExtensionPaths derives every required exact manual extension path from validated units.
func ManualExtensionPaths(units []Unit) []string {
	var paths []string
	for _, unit := range units {
		paths = append(paths, manualExtensionPaths(unit)...)
	}
	return paths
}

func verifyRequiredManualExtensions(repoRoot string, units []Unit) error {
	for _, relative := range ManualExtensionPaths(units) {
		absolute := filepath.Join(repoRoot, filepath.FromSlash(relative))
		info, err := os.Lstat(absolute)
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("required manual extension is missing: %s", relative)
		}
		if err != nil {
			return fmt.Errorf("inspect required manual extension %s: %w", relative, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("required manual extension is not a regular file: %s", relative)
		}
		data, err := os.ReadFile(absolute)
		if err != nil {
			return fmt.Errorf("read required manual extension %s: %w", relative, err)
		}
		marker := ManualExtensionMarker
		if strings.HasSuffix(relative, "_test.go") {
			marker = ManualTestExtensionMarker
		}
		if !bytes.HasPrefix(data, []byte(marker)) {
			return fmt.Errorf("required manual extension has non-canonical marker: %s", relative)
		}
	}
	return nil
}

func manualExtensionPaths(unit Unit) []string {
	names := []string{}
	switch unit.ConfigPath {
	case RootConfigPath:
		names = []string{"hook_pending_review_idempotency_sqlite_test.go", "prompt_template_sections_scope_sqlite_test.go", "prompt_template_sections_sql_test.go", "prompt_template_sql_test.go", "sqlite_runtime_sql_test.go"}
	case OrchestratorConfigPath:
		names = []string{"task_dag_run_guard_test.go", "types_ext.go"}
	}
	paths := make([]string, 0, len(names))
	for _, name := range names {
		paths = append(paths, path.Join(unit.Output, name))
	}
	return paths
}

func verifyManualTestExtension(repoRoot string, unit Unit, relative string) error {
	data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(relative)))
	if err != nil {
		return fmt.Errorf("read SQLC output test file %s: %w", relative, err)
	}
	if !IsManualExtension(unit, relative) || !bytes.HasPrefix(data, []byte(ManualTestExtensionMarker)) {
		return fmt.Errorf("SQLC output test file is not an approved marked extension: %s", relative)
	}
	return nil
}
