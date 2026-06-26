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
	"github.com/BurntSushi/toml":   true,
	"github.com/gorilla/websocket": true,
	"github.com/kelindar/event":    true,
	"go.uber.org/fx":               true,
	"golang.org/x/net":             true,
	"golang.org/x/sync":            true,
	"golang.org/x/sys":             true,
}

var moduleDBImportAllowlist = map[string]string{
	"internal/module/skill/module.go":          "skill 模块启动期仍需注入 legacy tool store 的数据库句柄",
	"internal/module/skill/service.go":         "skill service 暂时承载 tool store 构造边界",
	"internal/module/skill/toolstore/store.go": "toolstore 是 skill_tools 表的既有持久化子包",
}

type parsedFile struct {
	AbsPath string
	RelPath string
	Imports []string
}

func TestDependencyDirection(t *testing.T) {
	root := repoRoot(t)
	assertCoreDependencyRules(t, root)
	assertStoreAndToolDependencyRules(t, root)
	assertMCPServerDependencyRules(t, root)
	assertPlatformIsolationRules(t, root)
	assertModuleSiblingDependencyRules(t, root)
	assertModuleDBIsolationRules(t, root)
}

func assertCoreDependencyRules(t *testing.T, root string) {
	t.Helper()
	t.Run("rule1_contract_dto_no_framework_imports", func(t *testing.T) {
		assertNoFrameworkImportsInContractDTO(t, root)
	})

	t.Run("rule2_module_impls_no_fx", func(t *testing.T) {
		assertModuleImplsNoFX(t, root)
	})

	t.Run("rule3_provider_cannot_import_store", func(t *testing.T) {
		assertProviderCannotImportStore(t, root)
	})

	t.Run("rule3b_provider_external_whitelist", func(t *testing.T) {
		assertProviderExternalWhitelist(t, root)
	})

	t.Run("rule4_platform_cannot_import_module", func(t *testing.T) {
		assertPlatformCannotImportModule(t, root)
	})
}

func assertNoFrameworkImportsInContractDTO(t *testing.T, root string) {
	t.Helper()

	dirs := existingDirs(root, "internal/contract", "internal/dto")
	if len(dirs) == 0 {
		t.Skip("directory not yet created")
	}
	forbidden := []string{"go.uber.org/fx", "github.com/creachadair/jrpc2", "github.com/jackc/pgx/v5", "github.com/wailsapp/wails"}
	assertNoImportPrefixes(t, parseImportFiles(t, root, dirs...), forbidden)
}

func assertModuleImplsNoFX(t *testing.T, root string) {
	t.Helper()

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
}

func assertProviderCannotImportStore(t *testing.T, root string) {
	t.Helper()

	dirs := existingDirs(root, "internal/provider/claudecli", "internal/provider/codexapp", "internal/provider/unified")
	if len(dirs) == 0 {
		t.Skip("directory not yet created")
	}
	assertNoImportPrefixes(t, parseImportFiles(t, root, dirs...), []string{internalPrefix("internal/store")})
}

func assertProviderExternalWhitelist(t *testing.T, root string) {
	t.Helper()

	dirs := existingDirs(root, "internal/provider/claudecli", "internal/provider/codexapp", "internal/provider/unified")
	if len(dirs) == 0 {
		t.Skip("directory not yet created")
	}
	var violations []string
	for _, file := range parseImportFiles(t, root, dirs...) {
		if filepath.Base(file.RelPath) == "module.go" {
			continue
		}
		violations = append(violations, providerExternalWhitelistViolations(file)...)
	}
	failIfViolations(t, violations)
}

func providerExternalWhitelistViolations(file parsedFile) []string {
	var violations []string
	for _, imp := range file.Imports {
		if isStdlibImport(imp) || strings.HasPrefix(imp, modulePath+"/") {
			continue
		}
		if !providerAllowedExternal[externalModuleRoot(imp)] {
			violations = append(violations, fmt.Sprintf("%s imports %s outside provider external whitelist", file.RelPath, imp))
		}
	}
	return violations
}

func assertPlatformCannotImportModule(t *testing.T, root string) {
	t.Helper()

	if !dirExists(root, "internal/platform") || !dirExists(root, "internal/module") {
		t.Skip("directory not yet created")
	}
	assertNoImportPrefixes(t, parseImportFiles(t, root, "internal/platform"), []string{internalPrefix("internal/module")})
}

func assertModuleSiblingDependencyRules(t *testing.T, root string) {
	t.Helper()
	t.Run("rule16_module_siblings_no_concrete_imports", func(t *testing.T) {
		assertModuleSiblingsNoConcreteImports(t, root)
	})
}

func assertModuleSiblingsNoConcreteImports(t *testing.T, root string) {
	t.Helper()

	if !dirExists(root, "internal/module") {
		t.Skip("directory not yet created")
	}
	var violations []string
	for _, file := range parseImportFiles(t, root, "internal/module") {
		owner, ok := moduleOwnerForImportCheck(file.RelPath)
		if !ok {
			continue
		}
		violations = append(violations, moduleSiblingImportViolations(file, owner)...)
	}
	failIfViolations(t, violations)
}

func moduleOwnerForImportCheck(relPath string) (string, bool) {
	if strings.HasSuffix(relPath, "_test.go") || filepath.Base(relPath) == "module.go" {
		return "", false
	}
	parts := strings.Split(filepath.ToSlash(relPath), "/")
	if len(parts) < 3 || parts[0] != "internal" || parts[1] != "module" {
		return "", false
	}
	return parts[2], true
}

func moduleSiblingImportViolations(file parsedFile, owner string) []string {
	var violations []string
	for _, imp := range file.Imports {
		importModule, ok := importedModuleName(imp)
		if ok && importModule != owner {
			violations = append(violations, fmt.Sprintf("%s imports sibling module %s", file.RelPath, imp))
		}
	}
	return violations
}

func importedModuleName(imp string) (string, bool) {
	if !strings.HasPrefix(imp, internalPrefix("internal/module/")) {
		return "", false
	}
	importRel := strings.TrimPrefix(imp, internalPrefix("internal/module/"))
	return strings.Split(importRel, "/")[0], true
}

func assertModuleDBIsolationRules(t *testing.T, root string) {
	t.Helper()
	t.Run("rule17_module_cannot_import_sql", func(t *testing.T) {
		if !dirExists(root, "internal/module") {
			t.Skip("directory not yet created")
		}
		forbidden := []string{
			"database/sql",
			"github.com/jackc/pgx/v5",
			"github.com/jackc/pgx/v5/pgxpool",
			"github.com/jackc/pgx/v5/pgconn",
		}
		assertModuleNoDirectDBImports(t, parseImportFiles(t, root, "internal/module"), forbidden)
	})
}

// assertModuleNoDirectDBImports 拦截 module 层新增数据库依赖。
// allowlist 只覆盖 skill/toolstore 的既有持久化边界，后续新增文件仍会失败。
func assertModuleNoDirectDBImports(t *testing.T, files []parsedFile, prefixes []string) {
	t.Helper()
	var violations []string
	for _, file := range files {
		if _, ok := moduleDBImportAllowlist[file.RelPath]; ok {
			continue
		}
		violations = append(violations, importPrefixViolations(file, prefixes)...)
	}
	failIfViolations(t, violations)
}

func assertStoreAndToolDependencyRules(t *testing.T, root string) {
	t.Helper()
	t.Run("rule5_store_subpackages_boundary", func(t *testing.T) {
		assertStoreSubpackagesBoundary(t, root)
	})

	t.Run("rule6_tooling_runtime_cannot_import_ui_state_directly", func(t *testing.T) {
		assertToolingRuntimeCannotImportUIStateDirectly(t, root)
	})

	t.Run("rule10_fx_import_scope", func(t *testing.T) {
		assertFXImportScope(t, root)
	})
}

func assertStoreSubpackagesBoundary(t *testing.T, root string) {
	t.Helper()

	if !dirExists(root, "internal/store") {
		t.Skip("directory not yet created")
	}
	storeFiles := parseImportFiles(t, root, "internal/store")
	if len(storeFiles) == 0 {
		t.Skip("directory not yet created")
	}
	failIfViolations(t, storeBoundaryViolations(storeFiles))
}

func storeBoundaryViolations(storeFiles []parsedFile) []string {
	allowed := []string{
		internalPrefix("internal/platform/config"),
		internalPrefix("internal/platform/db"),
		internalPrefix("internal/platform/sharedfilefs"),
		internalPrefix("internal/platform/sharedfilegitignore"),
		internalPrefix("internal/platform/sharedfilepath"),
		internalPrefix("internal/store/sqlc"),
		internalPrefix("internal/contract"),
		internalPrefix("internal/dto"),
	}
	var violations []string
	for _, file := range storeFiles {
		for _, imp := range file.Imports {
			if storeImportAllowed(file, imp, allowed) {
				continue
			}
			violations = append(violations, fmt.Sprintf("%s imports %s outside store boundary", file.RelPath, imp))
		}
	}
	return violations
}

func storeImportAllowed(file parsedFile, imp string, allowed []string) bool {
	if isStdlibImport(imp) {
		return true
	}
	if strings.HasPrefix(imp, modulePath+"/internal/store/"+packageSuffix(file.RelPath)) {
		return true
	}
	if file.RelPath == "internal/store/module.go" {
		return storeRootModuleImportAllowed(imp)
	}
	if storePackageModuleImportAllowed(file.RelPath, imp) {
		return true
	}
	if imp == "github.com/jackc/pgx/v5/pgtype" {
		return true
	}
	return hasAllowedPrefix(imp, allowed)
}

func storeRootModuleImportAllowed(imp string) bool {
	return imp == "go.uber.org/fx" ||
		imp == "github.com/jackc/pgx/v5/pgxpool" ||
		hasAllowedPrefix(imp, []string{internalPrefix("internal/store")})
}

func storePackageModuleImportAllowed(relPath, imp string) bool {
	if filepath.Base(relPath) != "module.go" {
		return false
	}
	return imp == "go.uber.org/fx" || imp == "github.com/jackc/pgx/v5/pgxpool"
}

func assertToolingRuntimeCannotImportUIStateDirectly(t *testing.T, root string) {
	t.Helper()

	dirs := existingDirs(root, "cmd/mcp-lsp", "cmd/mcp-orch", "cmd/mcp-ida", "internal/mcpserver/common")
	if len(dirs) == 0 {
		t.Skip("tooling runtime directories not yet created")
	}
	assertNoImportPrefixes(t, parseImportFiles(t, root, dirs...), []string{
		internalPrefix("internal/uistate"),
		internalPrefix("internal/module/uistate"),
		internalPrefix("internal/ui/"),
	})
}

func assertFXImportScope(t *testing.T, root string) {
	t.Helper()

	var violations []string
	for _, file := range parseImportFiles(t, root, "internal", "cmd") {
		if !hasImport(file.Imports, "go.uber.org/fx") {
			continue
		}
		if fxImportAllowed(file.RelPath) {
			continue
		}
		violations = append(violations, fmt.Sprintf("%s imports go.uber.org/fx outside an assembly entry", file.RelPath))
	}
	failIfViolations(t, violations)
}

func fxImportAllowed(relPath string) bool {
	if strings.HasPrefix(relPath, "internal/app/") {
		return true
	}
	if filepath.Base(relPath) == "module.go" {
		return true
	}
	if strings.HasPrefix(relPath, "cmd/mcp-orch/") || strings.HasPrefix(relPath, "cmd/mcp-ida/") {
		return true
	}
	if rel, ok := strings.CutPrefix(relPath, "cmd/"); ok {
		return len(strings.Split(rel, "/")) == 2
	}
	return strings.HasPrefix(relPath, "cmd/mcp-lsp/") && filepath.Base(relPath) == "fx.go"
}

func assertMCPServerDependencyRules(t *testing.T, root string) {
	t.Helper()
	t.Run("rule7_cmd_mcp_lsp_family", func(t *testing.T) {
		if !dirExists(root, "cmd/mcp-lsp") {
			t.Skip("directory not yet created")
		}
		assertNoImportPrefixes(t, parseImportFiles(t, root, "cmd/mcp-lsp"), []string{
			internalPrefix("cmd/mcp-orch"),
			internalPrefix("cmd/mcp-ida"),
			internalPrefix("internal/app"),
			internalPrefix("internal/ui/"),
		})
	})

	t.Run("rule7b_cmd_mcp_lsp_cannot_import_module", func(t *testing.T) {
		if !dirExists(root, "cmd/mcp-lsp") {
			t.Skip("directory not yet created")
		}
		assertNoImportPrefixes(t, parseImportFiles(t, root, "cmd/mcp-lsp"), []string{internalPrefix("internal/module/")})
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
}
func assertPlatformIsolationRules(t *testing.T, root string) {
	t.Helper()
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
	t.Run("rule15_hooks_no_platform_db", func(t *testing.T) {
		if !dirExists(root, "internal/platform/hooks") {
			t.Skip("directory not yet created")
		}
		forbidden := []string{internalPrefix("internal/platform/db")}
		assertNoImportPrefixes(t, parseImportFiles(t, root, "internal/platform/hooks"), forbidden)
		var testFiles []parsedFile
		err := filepath.WalkDir(filepath.Join(root, "internal/platform/hooks"), func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil || d.IsDir() || filepath.Ext(path) != ".go" || !strings.HasSuffix(path, "_test.go") {
				return walkErr
			}
			relPath, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			testFiles = append(testFiles, parsedFile{AbsPath: path, RelPath: filepath.ToSlash(relPath), Imports: parseImports(t, path)})
			return nil
		})
		if err != nil {
			t.Fatalf("walk internal/platform/hooks tests: %v", err)
		}
		assertNoImportPrefixes(t, testFiles, forbidden)
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
		relPath = filepath.ToSlash(relPath)
		data, err := os.ReadFile(absPath)
		if err != nil {
			t.Fatalf("read %s: %v", absPath, err)
		}
		if archtest.IsGeneratedSQLCFile(relPath, data) {
			continue
		}
		out = append(out, parsedFile{AbsPath: absPath, RelPath: relPath, Imports: parseImports(t, absPath)})
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
		violations = append(violations, importPrefixViolations(file, prefixes)...)
	}
	failIfViolations(t, violations)
}

func importPrefixViolations(file parsedFile, prefixes []string) []string {
	var violations []string
	for _, imp := range file.Imports {
		for _, prefix := range prefixes {
			if imp == prefix || strings.HasPrefix(imp, prefix+"/") {
				violations = append(violations, fmt.Sprintf("%s imports %s", file.RelPath, imp))
				break
			}
		}
	}
	return violations
}

func hasImport(imports []string, target string) bool { return slices.Contains(imports, target) }

func hasAllowedPrefix(path string, allowed []string) bool {
	return slices.ContainsFunc(allowed, func(prefix string) bool {
		return path == prefix || strings.HasPrefix(path, prefix+"/")
	})
}

func isStdlibImport(path string) bool { return !strings.Contains(path, ".") }

func internalPrefix(rel string) string { return modulePath + "/" + strings.TrimPrefix(rel, "/") }

func packageSuffix(relPath string) string {
	return strings.TrimPrefix(filepath.ToSlash(filepath.Dir(relPath)), "internal/store/")
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
