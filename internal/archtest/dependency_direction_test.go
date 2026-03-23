package archtest_test

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/archtest"
)

const modulePath = "github.com/anthropic-ai/super-agent-v3"

var providerAllowedExternal = map[string]bool{
	"github.com/kelindar/event": true,
	"golang.org/x/net":          true,
}

type parsedFile struct {
	AbsPath string
	RelPath string
	Imports []string
}

func TestDependencyDirection(t *testing.T) {
	root := repoRoot(t)

	t.Run("rule1_contract_dto_no_framework_imports", func(t *testing.T) {
		dirs := existingDirs(root, "internal/contract", "internal/dto")
		if len(dirs) == 0 {
			t.Skip("directory not yet created")
		}
		forbidden := []string{"go.uber.org/fx", "github.com/creachadair/jrpc2", "github.com/jackc/pgx/v5", "github.com/wailsapp/wails"}
		assertNoImportPrefixes(t, parseImportFiles(t, root, dirs...), forbidden)
	})

	t.Run("rule2_module_impls_no_fx", func(t *testing.T) {
		if !dirExists(root, "internal/module") {
			t.Skip("directory not yet created")
		}
		var violations []string
		for _, file := range parseImportFiles(t, root, "internal/module") {
			if filepath.Base(file.RelPath) == "module.go" {
				continue
			}
			if hasImport(file.Imports, "go.uber.org/fx") {
				violations = append(violations, fmt.Sprintf("%s imports go.uber.org/fx outside module.go", file.RelPath))
			}
		}
		failIfViolations(t, violations)
	})

	t.Run("rule3_provider_cannot_import_store", func(t *testing.T) {
		dirs := existingDirs(root, "internal/provider/claudecli", "internal/provider/codexapp")
		if len(dirs) == 0 {
			t.Skip("directory not yet created")
		}
		assertNoImportPrefixes(t, parseImportFiles(t, root, dirs...), []string{internalPrefix("internal/store/")})
	})

	t.Run("rule3b_provider_external_whitelist", func(t *testing.T) {
		dirs := existingDirs(root, "internal/provider/claudecli", "internal/provider/codexapp", "internal/provider/unified")
		if len(dirs) == 0 {
			t.Skip("directory not yet created")
		}
		var violations []string
		for _, file := range parseImportFiles(t, root, dirs...) {
			if filepath.Base(file.RelPath) == "module.go" {
				continue
			}
			for _, imp := range file.Imports {
				if isStdlibImport(imp) || strings.HasPrefix(imp, modulePath+"/") {
					continue
				}
				if !providerAllowedExternal[externalModuleRoot(imp)] {
					violations = append(violations, fmt.Sprintf("%s imports %s outside provider external whitelist", file.RelPath, imp))
				}
			}
		}
		failIfViolations(t, violations)
	})

	t.Run("rule4_platform_cannot_import_module", func(t *testing.T) {
		if !dirExists(root, "internal/platform") || !dirExists(root, "internal/module") {
			t.Skip("directory not yet created")
		}
		assertNoImportPrefixes(t, parseImportFiles(t, root, "internal/platform"), []string{internalPrefix("internal/module/")})
	})

	t.Run("rule5_store_subpackages_boundary", func(t *testing.T) {
		if !dirExists(root, "internal/store") {
			t.Skip("directory not yet created")
		}
		files := parseImportFiles(t, root, "internal/store")
		var storeFiles []parsedFile
		for _, file := range files {
			dir := filepath.ToSlash(filepath.Dir(file.RelPath))
			if dir == "internal/store" || strings.HasPrefix(file.RelPath, "internal/store/sqlc/") {
				continue
			}
			storeFiles = append(storeFiles, file)
		}
		if len(storeFiles) == 0 {
			t.Skip("directory not yet created")
		}
		allowed := []string{
			internalPrefix("internal/platform/db"),
			internalPrefix("internal/store/sqlc"),
			internalPrefix("internal/contract"),
			internalPrefix("internal/dto"),
		}
		var violations []string
		for _, file := range storeFiles {
			for _, imp := range file.Imports {
				if isStdlibImport(imp) || strings.HasPrefix(imp, modulePath+"/internal/store/"+packageSuffix(file.RelPath)) {
					continue
				}
				if filepath.Base(file.RelPath) == "module.go" && imp == "go.uber.org/fx" {
					continue
				}
				if !hasAllowedPrefix(imp, allowed) {
					violations = append(violations, fmt.Sprintf("%s imports %s outside store boundary", file.RelPath, imp))
				}
			}
		}
		failIfViolations(t, violations)
	})

	t.Run("rule6_tool_cannot_import_ui_state_directly", func(t *testing.T) {
		if !dirExists(root, "internal/tool") {
			t.Skip("directory not yet created")
		}
		assertNoImportPrefixes(t, parseImportFiles(t, root, "internal/tool"), []string{internalPrefix("internal/uistate"), internalPrefix("internal/ui/")})
	})

	t.Run("rule7_mcpserver_lsp_family", func(t *testing.T) {
		if !dirExists(root, "internal/mcpserver/lsp") {
			t.Skip("directory not yet created")
		}
		assertNoImportPrefixes(t, parseImportFiles(t, root, "internal/mcpserver/lsp"), []string{internalPrefix("internal/tool/ida"), internalPrefix("internal/tool/orchestration")})
	})

	t.Run("rule8_mcpserver_orch_family", func(t *testing.T) {
		if !dirExists(root, "internal/mcpserver/orch") {
			t.Skip("directory not yet created")
		}
		assertNoImportPrefixes(t, parseImportFiles(t, root, "internal/mcpserver/orch"), []string{internalPrefix("internal/tool/lsp"), internalPrefix("internal/tool/ida")})
	})

	t.Run("rule9_mcpserver_ida_family", func(t *testing.T) {
		if !dirExists(root, "internal/mcpserver/ida") {
			t.Skip("directory not yet created")
		}
		assertNoImportPrefixes(t, parseImportFiles(t, root, "internal/mcpserver/ida"), []string{internalPrefix("internal/tool/lsp"), internalPrefix("internal/tool/orchestration")})
	})

	t.Run("rule10_fx_import_scope", func(t *testing.T) {
		var violations []string
		for _, file := range parseImportFiles(t, root, "internal", "cmd") {
			if !hasImport(file.Imports, "go.uber.org/fx") {
				continue
			}
			if strings.HasPrefix(file.RelPath, "cmd/") || strings.HasPrefix(file.RelPath, "internal/app/") || filepath.Base(file.RelPath) == "module.go" {
				continue
			}
			violations = append(violations, fmt.Sprintf("%s imports go.uber.org/fx outside an assembly entry", file.RelPath))
		}
		failIfViolations(t, violations)
	})

	t.Run("rule13_hooks_no_mcpcontrol", func(t *testing.T) {
		if !dirExists(root, "internal/platform/hooks") {
			t.Skip("directory not yet created")
		}
		assertNoImportPrefixes(t, parseImportFiles(t, root, "internal/platform/hooks"), []string{internalPrefix("internal/platform/mcpcontrol")})
	})

	t.Run("rule14_mcpcontrol_no_hooks", func(t *testing.T) {
		if !dirExists(root, "internal/platform/mcpcontrol") {
			t.Skip("directory not yet created")
		}
		assertNoImportPrefixes(t, parseImportFiles(t, root, "internal/platform/mcpcontrol"), []string{internalPrefix("internal/platform/hooks")})
	})
}

func TestMCPOrchDependencyDirection(t *testing.T) {
	assertMCPOrchDependencyDirection(t, repoRoot(t))
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("go.mod not found from %s: %v", root, err)
	}
	return root
}

func existingDirs(root string, rels ...string) []string {
	out := make([]string, 0, len(rels))
	for _, rel := range rels {
		if dirExists(root, rel) {
			out = append(out, rel)
		}
	}
	return out
}

func dirExists(root, rel string) bool {
	info, err := os.Stat(filepath.Join(root, rel))
	return err == nil && info.IsDir()
}

func parseImportFiles(t *testing.T, root string, relRoots ...string) []parsedFile {
	t.Helper()
	files := walkGoFiles(t, root, relRoots...)
	out := make([]parsedFile, 0, len(files))
	for _, absPath := range files {
		relPath, err := filepath.Rel(root, absPath)
		if err != nil {
			t.Fatalf("rel path for %s: %v", absPath, err)
		}
		out = append(out, parsedFile{AbsPath: absPath, RelPath: filepath.ToSlash(relPath), Imports: parseImports(t, absPath)})
	}
	return out
}

func walkGoFiles(t *testing.T, root string, relRoots ...string) []string {
	t.Helper()
	skip := archtest.DefaultSkipDirs()
	var files []string
	for _, relRoot := range relRoots {
		absRoot := filepath.Join(root, relRoot)
		if !dirExists(root, relRoot) {
			continue
		}
		err := filepath.WalkDir(absRoot, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				if skip[d.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			if filepath.Ext(path) == ".go" && !strings.HasSuffix(path, "_test.go") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", relRoot, err)
		}
	}
	slices.Sort(files)
	return files
}

func parseImports(t *testing.T, absPath string) []string {
	t.Helper()
	fset := token.NewFileSet()
	fileNode, err := parser.ParseFile(fset, absPath, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse imports %s: %v", absPath, err)
	}
	imports := make([]string, 0, len(fileNode.Imports))
	for _, spec := range fileNode.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatalf("unquote import %s: %v", spec.Path.Value, err)
		}
		imports = append(imports, path)
	}
	return imports
}

func failIfViolations(t *testing.T, violations []string) {
	t.Helper()
	if len(violations) == 0 {
		return
	}
	t.Fatalf("violations detected (%d):\n%s", len(violations), strings.Join(violations, "\n"))
}

func assertNoImportPrefixes(t *testing.T, files []parsedFile, prefixes []string) {
	t.Helper()
	var violations []string
	for _, file := range files {
		for _, imp := range file.Imports {
			for _, prefix := range prefixes {
				if imp == prefix || strings.HasPrefix(imp, prefix+"/") {
					violations = append(violations, fmt.Sprintf("%s imports %s", file.RelPath, imp))
					break
				}
			}
		}
	}
	failIfViolations(t, violations)
}

func hasImport(imports []string, target string) bool {
	for _, imp := range imports {
		if imp == target {
			return true
		}
	}
	return false
}

func hasAllowedPrefix(path string, allowed []string) bool {
	for _, prefix := range allowed {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

func isStdlibImport(path string) bool {
	return !strings.Contains(path, ".")
}

func internalPrefix(rel string) string {
	return modulePath + "/" + strings.TrimPrefix(rel, "/")
}

func packageSuffix(relPath string) string {
	dir := filepath.ToSlash(filepath.Dir(relPath))
	return strings.TrimPrefix(dir, "internal/store/")
}

func externalModuleRoot(path string) string {
	parts := strings.Split(path, "/")
	if len(parts) >= 3 {
		return strings.Join(parts[:3], "/")
	}
	return path
}

func goListDeps(t *testing.T, root, relPkg string) []string {
	t.Helper()
	cmd := exec.Command("go", "list", "-deps", "./"+relPkg)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps %s: %v", relPkg, err)
	}
	lines := strings.Fields(string(out))
	slices.Sort(lines)
	return lines
}
