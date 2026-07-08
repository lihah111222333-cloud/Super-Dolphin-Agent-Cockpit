package archtest_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestOrchestrationServiceTargetTypeCheckErrorFailsTargetMentions(t *testing.T) {
	_, syntax := parseOrchestrationServiceTypeGuardFixture(t, map[string]string{
		"cmd/mcp-orch/orchestration/broken.go": `package orchestration

import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"

type Service = contract.OrchestrationService
var _ = missingSymbol
`,
	})

	err := orchestrationServiceTargetTypeCheckError(
		superAgentModulePath+"/cmd/mcp-orch/orchestration",
		syntax,
		nil,
		[]string{"undefined: missingSymbol"},
	)
	if err == nil || !strings.Contains(err.Error(), "undefined: missingSymbol") {
		t.Fatalf("target type-check error = %v, want fail-fast error", err)
	}
}

func TestOrchestrationServiceTargetTypeCheckErrorIgnoresUnrelatedTypeErrors(t *testing.T) {
	_, syntax := parseOrchestrationServiceTypeGuardFixture(t, map[string]string{
		"cmd/mcp-lsp/multilsp/broken.go": `package multilsp

import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"

var _ = contract.LSPConfig{}
`,
	})

	err := orchestrationServiceTargetTypeCheckError(
		superAgentModulePath+"/cmd/mcp-lsp/multilsp",
		syntax,
		nil,
		[]string{"undefined: contract.LSPConfig"},
	)
	if err != nil {
		t.Fatalf("unrelated type-check error = %v, want ignored", err)
	}
}

type orchestrationServiceCheckedPackage struct {
	pkgPath   string
	fset      *token.FileSet
	syntax    []*ast.File
	types     *types.Package
	typesInfo *types.Info
}

type orchestrationServiceGoListPackage struct {
	Dir             string
	ImportPath      string
	Name            string
	GoFiles         []string
	CompiledGoFiles []string
	Error           *orchestrationServiceGoListError
}

type orchestrationServiceGoListError struct {
	Err string
}

func mustLoadOrchestrationServiceTypeObject(t *testing.T, root string) *types.TypeName {
	t.Helper()

	_, target, err := newOrchestrationServiceContractPackage(root)
	if err != nil {
		t.Fatalf("load internal/contract.OrchestrationService type object: %v", err)
	}
	return target
}

func loadOrchestrationServiceTypeGuardPackages(
	t *testing.T,
	root string,
	contractPkg *types.Package,
) []*orchestrationServiceCheckedPackage {
	t.Helper()

	listed, err := listOrchestrationServiceTypeGuardPackages(t, root)
	if err != nil {
		t.Fatalf("list production packages: %v", err)
	}
	if errors := packageListErrors(listed); len(errors) > 0 {
		t.Fatalf("list production packages returned errors:\n%s", strings.Join(errors, "\n"))
	}

	loader := newOrchestrationServiceTypeGuardLoader(listed, contractPkg)
	checked := make([]*orchestrationServiceCheckedPackage, 0, len(listed))
	for _, pkg := range listed {
		if !isOrchestrationServiceTypeGuardProductionPackagePath(pkg.ImportPath) || len(orchestrationServicePackageFiles(pkg)) == 0 {
			continue
		}
		checkedPkg, err := loader.check(pkg.ImportPath)
		if err != nil {
			t.Fatalf("type check %s: %v", pkg.ImportPath, err)
		}
		checked = append(checked, checkedPkg)
	}
	sort.Slice(checked, func(i, j int) bool {
		return checked[i].pkgPath < checked[j].pkgPath
	})
	return checked
}

func listOrchestrationServiceTypeGuardPackages(t *testing.T, root string) ([]orchestrationServiceGoListPackage, error) {
	t.Helper()

	overlayPath, err := writeOrchestrationServiceTypeGuardOverlay(t, root)
	if err != nil {
		return nil, err
	}
	args := []string{"list", "-overlay", overlayPath, "-json", "-e", "-compiled", "./cmd/...", "./internal/..."}
	cmd := exec.Command("go", args...)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("go %s: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return decodeOrchestrationServiceGoListPackages(output)
}

func decodeOrchestrationServiceGoListPackages(output []byte) ([]orchestrationServiceGoListPackage, error) {
	decoder := json.NewDecoder(bytes.NewReader(output))
	var pkgs []orchestrationServiceGoListPackage
	for {
		var pkg orchestrationServiceGoListPackage
		if err := decoder.Decode(&pkg); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("decode go list output: %w", err)
		}
		pkgs = append(pkgs, pkg)
	}
	sort.Slice(pkgs, func(i, j int) bool {
		return pkgs[i].ImportPath < pkgs[j].ImportPath
	})
	return pkgs, nil
}

func writeOrchestrationServiceTypeGuardOverlay(t *testing.T, root string) (string, error) {
	t.Helper()

	tempDir := t.TempDir()
	replacement := filepath.Join(tempDir, "agent-terminal-index.html")
	if err := os.WriteFile(replacement, []byte("<!doctype html><title>archtest</title>\n"), 0o600); err != nil {
		return "", err
	}
	overlay := struct {
		Replace map[string]string `json:"Replace"`
	}{
		Replace: map[string]string{
			filepath.Join(root, "cmd", "agent-terminal", "web-dist", "index.html"): replacement,
		},
	}
	data, err := json.Marshal(overlay)
	if err != nil {
		return "", err
	}
	overlayPath := filepath.Join(tempDir, "overlay.json")
	if err := os.WriteFile(overlayPath, data, 0o600); err != nil {
		return "", err
	}
	return overlayPath, nil
}

func packageListErrors(pkgs []orchestrationServiceGoListPackage) []string {
	var out []string
	for _, pkg := range pkgs {
		if pkg.Error != nil {
			out = append(out, fmt.Sprintf("%s: %s", pkg.ImportPath, pkg.Error.Err))
		}
	}
	sort.Strings(out)
	return out
}

func isOrchestrationServiceTypeGuardProductionPackage(pkg *orchestrationServiceCheckedPackage) bool {
	if pkg == nil {
		return false
	}
	return isOrchestrationServiceTypeGuardProductionPackagePath(pkg.pkgPath)
}

func isOrchestrationServiceTypeGuardProductionPackagePath(pkgPath string) bool {
	if pkgPath == "" || pkgPath == orchestrationServiceContractPackagePath {
		return false
	}
	if strings.HasPrefix(pkgPath, superAgentModulePath+"/internal/archtest") {
		return false
	}
	return strings.HasPrefix(pkgPath, superAgentModulePath+"/cmd/") ||
		strings.HasPrefix(pkgPath, superAgentModulePath+"/internal/")
}

func orchestrationServicePackageFiles(pkg orchestrationServiceGoListPackage) []string {
	if len(pkg.CompiledGoFiles) > 0 {
		return pkg.CompiledGoFiles
	}
	return pkg.GoFiles
}

func newOrchestrationServiceContractPackage(root string) (*types.Package, *types.TypeName, error) {
	pkg := types.NewPackage(orchestrationServiceContractPackagePath, "contract")
	methodNames, err := orchestrationServiceContractMethodNames(root)
	if err != nil {
		return nil, nil, err
	}
	methods := make([]*types.Func, 0, len(methodNames))
	for _, methodName := range methodNames {
		signature := types.NewSignatureType(nil, nil, nil, types.NewTuple(), types.NewTuple(), false)
		methods = append(methods, types.NewFunc(token.NoPos, pkg, methodName, signature))
	}
	iface := types.NewInterfaceType(methods, nil).Complete()
	target := types.NewTypeName(token.NoPos, pkg, "OrchestrationService", nil)
	types.NewNamed(target, iface, nil)
	if inserted := pkg.Scope().Insert(target); inserted != nil {
		return nil, nil, fmt.Errorf("insert OrchestrationService: duplicate %s", inserted.Name())
	}
	pkg.MarkComplete()
	return pkg, target, nil
}

func orchestrationServiceContractMethodNames(root string) ([]string, error) {
	fset := token.NewFileSet()
	filename := filepath.Join(root, "internal", "contract", "orchestration.go")
	file, err := parser.ParseFile(fset, filename, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		spec, ok := node.(*ast.TypeSpec)
		if !ok {
			return true
		}
		iface, ok := spec.Type.(*ast.InterfaceType)
		if !ok {
			return true
		}
		for _, method := range iface.Methods.List {
			for _, name := range method.Names {
				seen[name.Name] = true
			}
		}
		return false
	})
	methodNames := make([]string, 0, len(seen))
	for methodName := range seen {
		methodNames = append(methodNames, methodName)
	}
	sort.Strings(methodNames)
	return methodNames, nil
}

type orchestrationServiceTypeGuardLoader struct {
	listed      map[string]orchestrationServiceGoListPackage
	checked     map[string]*orchestrationServiceCheckedPackage
	checking    map[string]bool
	stubs       map[string]*types.Package
	contractPkg *types.Package
	fallback    types.Importer
}

func newOrchestrationServiceTypeGuardLoader(
	listed []orchestrationServiceGoListPackage,
	contractPkg *types.Package,
) *orchestrationServiceTypeGuardLoader {
	byImportPath := make(map[string]orchestrationServiceGoListPackage, len(listed))
	for _, pkg := range listed {
		byImportPath[pkg.ImportPath] = pkg
	}
	return &orchestrationServiceTypeGuardLoader{
		listed:      byImportPath,
		checked:     map[string]*orchestrationServiceCheckedPackage{},
		checking:    map[string]bool{},
		stubs:       map[string]*types.Package{},
		contractPkg: contractPkg,
		fallback:    importer.Default(),
	}
}

func (loader *orchestrationServiceTypeGuardLoader) check(importPath string) (*orchestrationServiceCheckedPackage, error) {
	if checked := loader.checked[importPath]; checked != nil {
		return checked, nil
	}
	listed, ok := loader.listed[importPath]
	if !ok {
		return nil, fmt.Errorf("package %s was not returned by go list", importPath)
	}
	if loader.checking[importPath] {
		return loader.stubCheckedPackage(importPath), nil
	}

	loader.checking[importPath] = true
	defer delete(loader.checking, importPath)

	fset, syntax, err := parseOrchestrationServicePackageFiles(listed)
	if err != nil {
		return nil, err
	}
	info := newOrchestrationServiceTypesInfo()
	checkedTypes, typeErrors := loader.checkSyntax(importPath, fset, syntax, info)
	if err := orchestrationServiceTargetTypeCheckError(importPath, syntax, checkedTypes, typeErrors); err != nil {
		return nil, err
	}
	if checkedTypes == nil {
		checkedTypes = loader.stubPackage(importPath)
	}
	checked := &orchestrationServiceCheckedPackage{
		pkgPath:   importPath,
		fset:      fset,
		syntax:    syntax,
		types:     checkedTypes,
		typesInfo: info,
	}
	loader.checked[importPath] = checked
	return checked, nil
}

func parseOrchestrationServicePackageFiles(pkg orchestrationServiceGoListPackage) (*token.FileSet, []*ast.File, error) {
	fset := token.NewFileSet()
	files := orchestrationServicePackageFiles(pkg)
	syntax := make([]*ast.File, 0, len(files))
	for _, filename := range files {
		if !filepath.IsAbs(filename) {
			filename = filepath.Join(pkg.Dir, filename)
		}
		file, err := parser.ParseFile(fset, filename, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, nil, fmt.Errorf("parse %s: %w", filename, err)
		}
		syntax = append(syntax, file)
	}
	return fset, syntax, nil
}

func newOrchestrationServiceTypesInfo() *types.Info {
	return &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
}

func (loader *orchestrationServiceTypeGuardLoader) checkSyntax(
	importPath string,
	fset *token.FileSet,
	syntax []*ast.File,
	info *types.Info,
) (*types.Package, []string) {
	var typeErrors []string
	conf := types.Config{
		Importer: loader,
		Error: func(err error) {
			typeErrors = append(typeErrors, err.Error())
		},
	}
	checkedTypes, err := conf.Check(importPath, fset, syntax, info)
	if err != nil && len(typeErrors) == 0 {
		typeErrors = append(typeErrors, err.Error())
	}
	return checkedTypes, typeErrors
}

func orchestrationServiceTargetTypeCheckError(
	importPath string,
	syntax []*ast.File,
	checkedTypes *types.Package,
	typeErrors []string,
) error {
	if !orchestrationServicePackageMentionsTarget(syntax) {
		return nil
	}
	if len(typeErrors) > 0 {
		sort.Strings(typeErrors)
		return fmt.Errorf("%s", strings.Join(typeErrors, "\n"))
	}
	if checkedTypes == nil {
		return fmt.Errorf("type check produced no package for %s", importPath)
	}
	return nil
}

func (loader *orchestrationServiceTypeGuardLoader) Import(importPath string) (*types.Package, error) {
	if importPath == orchestrationServiceContractPackagePath {
		return loader.contractPkg, nil
	}
	if pkg := loader.cachedPackage(importPath); pkg != nil {
		return pkg, nil
	}
	if pkg := loader.checkedListedPackage(importPath); pkg != nil {
		return pkg, nil
	}
	if pkg := loader.fallbackPackage(importPath); pkg != nil {
		return pkg, nil
	}
	return loader.stubPackage(importPath), nil
}

func (loader *orchestrationServiceTypeGuardLoader) cachedPackage(importPath string) *types.Package {
	if checked := loader.checked[importPath]; checked != nil {
		return checked.types
	}
	return nil
}

func (loader *orchestrationServiceTypeGuardLoader) checkedListedPackage(importPath string) *types.Package {
	_, ok := loader.listed[importPath]
	if !ok || loader.checking[importPath] || !isOrchestrationServiceTypeGuardProductionPackagePath(importPath) {
		return nil
	}
	checked, err := loader.check(importPath)
	if err != nil {
		return nil
	}
	return checked.types
}

func (loader *orchestrationServiceTypeGuardLoader) fallbackPackage(importPath string) *types.Package {
	if loader.fallback == nil {
		return nil
	}
	pkg, err := loader.fallback.Import(importPath)
	if err != nil {
		return nil
	}
	return pkg
}

func (loader *orchestrationServiceTypeGuardLoader) stubCheckedPackage(importPath string) *orchestrationServiceCheckedPackage {
	return &orchestrationServiceCheckedPackage{
		pkgPath: importPath,
		types:   loader.stubPackage(importPath),
	}
}

func (loader *orchestrationServiceTypeGuardLoader) stubPackage(importPath string) *types.Package {
	if pkg := loader.stubs[importPath]; pkg != nil {
		return pkg
	}
	name := pathpkg.Base(importPath)
	if listed, ok := loader.listed[importPath]; ok && listed.Name != "" {
		name = listed.Name
	}
	pkg := types.NewPackage(importPath, name)
	pkg.MarkComplete()
	loader.stubs[importPath] = pkg
	return pkg
}

func orchestrationServicePackageMentionsTarget(files []*ast.File) bool {
	found := false
	for _, file := range files {
		ast.Inspect(file, func(node ast.Node) bool {
			if found {
				return false
			}
			if selector, ok := node.(*ast.SelectorExpr); ok && selector.Sel.Name == "OrchestrationService" {
				found = true
				return false
			}
			return true
		})
	}
	return found
}

type orchestrationServiceFixtureImporter struct {
	contractPkg *types.Package
	fallback    types.Importer
}

func (i orchestrationServiceFixtureImporter) Import(path string) (*types.Package, error) {
	if path == orchestrationServiceContractPackagePath {
		return i.contractPkg, nil
	}
	return i.fallback.Import(path)
}

func typeCheckOrchestrationServiceFixturePackage(
	t *testing.T,
	contractPkg *types.Package,
	files map[string]string,
) *orchestrationServiceCheckedPackage {
	t.Helper()

	fset, syntax := parseOrchestrationServiceTypeGuardFixture(t, files)
	info := newOrchestrationServiceTypesInfo()
	conf := types.Config{
		Importer: orchestrationServiceFixtureImporter{
			contractPkg: contractPkg,
			fallback:    importer.Default(),
		},
	}
	checked, err := conf.Check(superAgentModulePath+"/cmd/mcp-orch/orchestration", fset, syntax, info)
	if err != nil {
		t.Fatalf("type check fixture: %v", err)
	}
	return &orchestrationServiceCheckedPackage{
		pkgPath:   superAgentModulePath + "/cmd/mcp-orch/orchestration",
		fset:      fset,
		syntax:    syntax,
		types:     checked,
		typesInfo: info,
	}
}

func parseOrchestrationServiceTypeGuardFixture(t *testing.T, files map[string]string) (*token.FileSet, []*ast.File) {
	t.Helper()

	fset := token.NewFileSet()
	relPaths := make([]string, 0, len(files))
	for relPath := range files {
		relPaths = append(relPaths, relPath)
	}
	sort.Strings(relPaths)

	syntax := make([]*ast.File, 0, len(relPaths))
	for _, relPath := range relPaths {
		file, err := parser.ParseFile(fset, relPath, files[relPath], parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse fixture %s: %v", relPath, err)
		}
		syntax = append(syntax, file)
	}
	return fset, syntax
}
