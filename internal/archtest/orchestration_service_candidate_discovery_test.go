package archtest_test

import (
	"fmt"
	"go/ast"
	"go/token"
	"path"
	"slices"
	"sort"
	"strconv"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest/ssaload"
	"golang.org/x/tools/go/packages"
)

type wideOrchestrationMetadataPackage struct {
	pkg     *packages.Package
	types   map[string]ast.Expr
	imports map[string]string
}

type wideOrchestrationMetadataGraph struct {
	packages map[string]*wideOrchestrationMetadataPackage
}

// discoverWideOrchestrationTypeGuardPackagePaths 先用轻量源码/依赖元数据缩小类型守卫候选集。
func discoverWideOrchestrationTypeGuardPackagePaths(t *testing.T, root string) []string {
	t.Helper()
	paths, err := discoverWideOrchestrationTypeGuardPackagePathsWithOverlay(t, root, wideOrchestrationGuardOverlay(root))
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

func discoverWideOrchestrationTypeGuardPackagePathsWithOverlay(
	t *testing.T,
	root string,
	overlay map[string][]byte,
) ([]string, error) {
	t.Helper()
	loaded, err := ssaload.Load(ssaload.Options{
		RepoRoot: root,
		Patterns: []string{"./cmd/...", "./internal/..."},
		Tests:    false,
		Overlay:  overlay,
		LoadMode: packages.LoadFiles | packages.NeedSyntax | packages.NeedImports,
		Include: func(pkg *packages.Package) bool {
			return pkg != nil && isOrchestrationServiceTypeGuardProductionPackagePath(pkg.PkgPath) && len(pkg.GoFiles) > 0
		},
	})
	if err != nil {
		return nil, fmt.Errorf("discover production package paths: %w", err)
	}

	graph := &wideOrchestrationMetadataGraph{packages: make(map[string]*wideOrchestrationMetadataPackage, len(loaded))}
	for _, pkg := range loaded {
		if pkg == nil || !isOrchestrationServiceTypeGuardProductionPackagePath(pkg.PkgPath) {
			continue
		}
		graph.packages[pkg.PkgPath] = &wideOrchestrationMetadataPackage{
			pkg:     pkg,
			types:   metadataPackageTypes(pkg),
			imports: metadataPackageImports(pkg),
		}
	}

	paths := make([]string, 0, len(graph.packages))
	for pkgPath, pkg := range graph.packages {
		if graph.packageContainsWideType(pkgPath, pkg) {
			paths = append(paths, pkgPath)
		}
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("discover production package paths: no wide orchestration candidates")
	}
	sort.Strings(paths)
	return paths, nil
}

// TestWideOrchestrationCandidateDiscoveryPreservesTypedCandidates 对比旧 full typed loader 的候选快照，防止类型守卫静默漏包。
func TestWideOrchestrationCandidateDiscoveryPreservesTypedCandidates(t *testing.T) {
	got := discoverWideOrchestrationTypeGuardPackagePaths(t, repoRoot(t))
	want := []string{
		superAgentModulePath + "/cmd/mcp-orch",
		superAgentModulePath + "/cmd/mcp-orch/orchestration",
		superAgentModulePath + "/internal/module/dashboard",
		superAgentModulePath + "/internal/module/thread",
	}
	if len(got) != len(want) {
		t.Fatalf("wide orchestration candidate count=%d, want %d: %v", len(got), len(want), got)
	}
	for _, required := range want {
		if !slices.Contains(got, required) {
			t.Fatalf("wide orchestration candidate set is missing required package %s: %v", required, got)
		}
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("wide orchestration candidate %d=%q, want %q; got=%v", index, got[index], want[index], got)
		}
	}
	const wantDigest = "71e2f70fd6de52c60965cac4827ec9b0620082ddbf70d87e904d2893bb821790"
	if stablePathDigest(got) != wantDigest {
		t.Fatalf("wide orchestration candidate digest=%s, want %s", stablePathDigest(got), wantDigest)
	}
}

// TestWideOrchestrationCandidateDiscoveryRejectsEmptyOverlay 验证元数据被清空时必须阻断而非回退全量加载。
func TestWideOrchestrationCandidateDiscoveryRejectsEmptyOverlay(t *testing.T) {
	root := repoRoot(t)
	overlay := wideOrchestrationGuardOverlay(root)
	loaded, err := ssaload.Load(ssaload.Options{
		RepoRoot: root,
		Patterns: []string{"./cmd/...", "./internal/..."},
		Tests:    false,
		Overlay:  overlay,
		LoadMode: packages.LoadFiles | packages.NeedSyntax | packages.NeedImports,
		Include: func(pkg *packages.Package) bool {
			return pkg != nil && isOrchestrationServiceTypeGuardProductionPackagePath(pkg.PkgPath) && len(pkg.GoFiles) > 0
		},
	})
	if err != nil {
		t.Fatalf("load metadata for empty-overlay fixture: %v", err)
	}
	for _, pkg := range loaded {
		for _, file := range pkg.GoFiles {
			overlay[file] = []byte("package " + pkg.Name + "\n")
		}
	}
	if _, err := discoverWideOrchestrationTypeGuardPackagePathsWithOverlay(t, root, overlay); err == nil {
		t.Fatal("empty metadata candidate set unexpectedly succeeded")
	}
}

func metadataPackageTypes(pkg *packages.Package) map[string]ast.Expr {
	typesByName := make(map[string]ast.Expr)
	if pkg == nil {
		return typesByName
	}
	for _, file := range pkg.Syntax {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if ok && typeSpec.Name != nil && typeSpec.Type != nil {
					typesByName[typeSpec.Name.Name] = typeSpec.Type
				}
			}
		}
	}
	return typesByName
}

func metadataPackageImports(pkg *packages.Package) map[string]string {
	aliases := make(map[string]string)
	if pkg == nil {
		return aliases
	}
	for _, file := range pkg.Syntax {
		metadataFileImports(pkg, file, aliases)
	}
	return aliases
}

func metadataFileImports(pkg *packages.Package, file *ast.File, aliases map[string]string) {
	if file == nil {
		return
	}
	for _, spec := range file.Imports {
		importPath, alias, ok := metadataImportAlias(pkg, spec)
		if ok {
			aliases[alias] = importPath
		}
	}
}

func metadataImportAlias(pkg *packages.Package, spec *ast.ImportSpec) (string, string, bool) {
	if spec == nil || spec.Path == nil {
		return "", "", false
	}
	importPath, err := strconv.Unquote(spec.Path.Value)
	if err != nil || importPath == "" {
		return "", "", false
	}
	alias := path.Base(importPath)
	if imported := pkg.Imports[importPath]; imported != nil && imported.Name != "" {
		alias = imported.Name
	}
	if spec.Name == nil {
		return importPath, alias, true
	}
	if spec.Name.Name == "_" || spec.Name.Name == "." {
		return "", "", false
	}
	return importPath, spec.Name.Name, true
}

func (g *wideOrchestrationMetadataGraph) packageContainsWideType(pkgPath string, pkg *wideOrchestrationMetadataPackage) bool {
	if pkg == nil || pkg.pkg == nil {
		return false
	}
	for _, file := range pkg.pkg.Syntax {
		if g.fileContainsWideType(pkgPath, file) {
			return true
		}
	}
	return false
}

func (g *wideOrchestrationMetadataGraph) fileContainsWideType(pkgPath string, file *ast.File) bool {
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		if g.nodeContainsWideType(pkgPath, node) {
			found = true
			return false
		}
		return true
	})
	return found
}

func (g *wideOrchestrationMetadataGraph) nodeContainsWideType(pkgPath string, node ast.Node) bool {
	switch typed := node.(type) {
	case *ast.TypeSpec:
		return g.exprIsWide(pkgPath, typed.Type)
	case *ast.FuncType:
		return g.exprIsWide(pkgPath, typed)
	case *ast.Field:
		return g.exprIsWide(pkgPath, typed.Type)
	case *ast.ValueSpec:
		return g.exprIsWide(pkgPath, typed.Type)
	default:
		return false
	}
}

func (g *wideOrchestrationMetadataGraph) exprIsWide(pkgPath string, expr ast.Expr) bool {
	return len(g.exprFamilies(pkgPath, expr, map[string]bool{})) >= wideOrchestrationFamilyThreshold
}

func (g *wideOrchestrationMetadataGraph) exprFamilies(pkgPath string, expr ast.Expr, seen map[string]bool) map[string]bool {
	families := make(map[string]bool)
	if expr == nil {
		return families
	}
	switch typed := expr.(type) {
	case *ast.Ident:
		mergeOrchestrationFamilies(families, g.namedTypeFamilies(pkgPath, typed.Name, seen))
	case *ast.SelectorExpr:
		mergeOrchestrationFamilies(families, g.selectorFamilies(pkgPath, typed, seen))
	case *ast.InterfaceType:
		mergeOrchestrationFamilies(families, g.interfaceFamilies(pkgPath, typed, seen))
	case *ast.FuncType:
		mergeOrchestrationFamilies(families, g.functionFamilies(pkgPath, typed, seen))
	default:
		mergeOrchestrationFamilies(families, g.compositeExprFamilies(pkgPath, expr, seen))
	}
	return families
}

func mergeOrchestrationFamilies(dst, src map[string]bool) {
	for family := range src {
		dst[family] = true
	}
}

func (g *wideOrchestrationMetadataGraph) selectorFamilies(pkgPath string, expr *ast.SelectorExpr, seen map[string]bool) map[string]bool {
	ident, ok := expr.X.(*ast.Ident)
	if !ok {
		return nil
	}
	pkg := g.packages[pkgPath]
	if pkg == nil {
		return nil
	}
	importPath := pkg.imports[ident.Name]
	if importPath == "" {
		return nil
	}
	return g.namedTypeFamilies(importPath, expr.Sel.Name, seen)
}

func (g *wideOrchestrationMetadataGraph) interfaceFamilies(pkgPath string, expr *ast.InterfaceType, seen map[string]bool) map[string]bool {
	families := make(map[string]bool)
	for _, method := range expr.Methods.List {
		for _, name := range method.Names {
			if family := wideOrchestrationMethodFamily(name.Name); family != "" {
				families[family] = true
			}
		}
		mergeOrchestrationFamilies(families, g.exprFamilies(pkgPath, method.Type, seen))
	}
	return families
}

func (g *wideOrchestrationMetadataGraph) functionFamilies(pkgPath string, expr *ast.FuncType, seen map[string]bool) map[string]bool {
	families := make(map[string]bool)
	mergeOrchestrationFamilies(families, g.fieldListFamilies(pkgPath, expr.Params, seen))
	mergeOrchestrationFamilies(families, g.fieldListFamilies(pkgPath, expr.Results, seen))
	return families
}

func (g *wideOrchestrationMetadataGraph) compositeExprFamilies(pkgPath string, expr ast.Expr, seen map[string]bool) map[string]bool {
	switch typed := expr.(type) {
	case *ast.StarExpr:
		return g.exprFamilies(pkgPath, typed.X, seen)
	case *ast.ArrayType:
		return g.exprFamilies(pkgPath, typed.Elt, seen)
	case *ast.MapType:
		families := g.exprFamilies(pkgPath, typed.Key, seen)
		mergeOrchestrationFamilies(families, g.exprFamilies(pkgPath, typed.Value, seen))
		return families
	case *ast.ChanType:
		return g.exprFamilies(pkgPath, typed.Value, seen)
	case *ast.Ellipsis:
		return g.exprFamilies(pkgPath, typed.Elt, seen)
	case *ast.ParenExpr:
		return g.exprFamilies(pkgPath, typed.X, seen)
	case *ast.IndexExpr:
		return g.indexFamilies(pkgPath, typed.X, typed.Index, seen)
	case *ast.IndexListExpr:
		return g.indexListFamilies(pkgPath, typed, seen)
	default:
		return nil
	}
}

func (g *wideOrchestrationMetadataGraph) indexFamilies(pkgPath string, x, index ast.Expr, seen map[string]bool) map[string]bool {
	families := g.exprFamilies(pkgPath, x, seen)
	mergeOrchestrationFamilies(families, g.exprFamilies(pkgPath, index, seen))
	return families
}

func (g *wideOrchestrationMetadataGraph) indexListFamilies(pkgPath string, expr *ast.IndexListExpr, seen map[string]bool) map[string]bool {
	families := g.exprFamilies(pkgPath, expr.X, seen)
	for _, index := range expr.Indices {
		mergeOrchestrationFamilies(families, g.exprFamilies(pkgPath, index, seen))
	}
	return families
}

func (g *wideOrchestrationMetadataGraph) fieldListFamilies(pkgPath string, fields *ast.FieldList, seen map[string]bool) map[string]bool {
	families := make(map[string]bool)
	if fields == nil {
		return families
	}
	for _, field := range fields.List {
		for family := range g.exprFamilies(pkgPath, field.Type, seen) {
			families[family] = true
		}
	}
	return families
}

func (g *wideOrchestrationMetadataGraph) namedTypeFamilies(pkgPath, name string, seen map[string]bool) map[string]bool {
	key := pkgPath + "\x00" + name
	if seen[key] {
		return nil
	}
	seen[key] = true
	pkg := g.packages[pkgPath]
	if pkg == nil {
		return nil
	}
	return g.exprFamilies(pkgPath, pkg.types[name], seen)
}
