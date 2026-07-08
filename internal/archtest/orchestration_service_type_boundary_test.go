package archtest_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/printer"
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

const superAgentModulePath = "github.com/anthropic-ai/super-agent-v3"
const orchestrationServiceContractPackagePath = superAgentModulePath + "/internal/contract"

type orchestrationServiceTypeUse struct {
	relPath string
	line    int
	kind    string
	name    string
	expr    string
}

type orchestrationServiceTypeGuardFixture struct {
	name         string
	files        map[string]string
	wantContains []string
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

func TestOrchestrationServiceTypeConsumersUseNarrowPorts(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	target := mustLoadOrchestrationServiceTypeObject(t, root)
	pkgs := loadOrchestrationServiceTypeGuardPackages(t, root, target.Pkg())
	var violations []string
	for _, pkg := range pkgs {
		if !isOrchestrationServiceTypeGuardProductionPackage(pkg) {
			continue
		}
		for _, use := range collectOrchestrationServiceTypeUses(pkg, target) {
			if isAllowedOrchestrationServiceTypeUse(use) {
				continue
			}
			violations = append(violations, use.violationMessage())
		}
	}
	sort.Strings(violations)
	failIfViolations(t, violations)
}

func TestOrchestrationServiceTypeGuardFixtures(t *testing.T) {
	t.Parallel()

	target := mustLoadOrchestrationServiceTypeObject(t, repoRoot(t))
	for _, tt := range orchestrationServiceTypeGuardFixtures() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pkg := typeCheckOrchestrationServiceFixturePackage(t, target.Pkg(), tt.files)
			got := orchestrationServiceTypeUseMessages(collectOrchestrationServiceTypeUses(pkg, target))
			for _, want := range tt.wantContains {
				if !containsViolation(got, want) {
					t.Fatalf("missing violation containing %q; got:\n%s", want, strings.Join(got, "\n"))
				}
			}
		})
	}
}

func orchestrationServiceTypeGuardFixtures() []orchestrationServiceTypeGuardFixture {
	return []orchestrationServiceTypeGuardFixture{
		{
			name: "cross-file alias parameter",
			files: map[string]string{
				"cmd/mcp-orch/orchestration/alias.go": `package orchestration

import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"

type Service = contract.OrchestrationService
`,
				"cmd/mcp-orch/orchestration/consumer.go": `package orchestration

func use(svc Service) {}
`,
			},
			wantContains: []string{
				"cmd/mcp-orch/orchestration/alias.go:5 type alias Service uses full orchestration service",
				"cmd/mcp-orch/orchestration/consumer.go:3 parameter svc uses full orchestration service",
			},
		},
		{
			name: "cross-file alias generic constraint",
			files: map[string]string{
				"internal/module/dashboard/alias.go": `package dashboard

import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"

type Service = contract.OrchestrationService
`,
				"internal/module/dashboard/consumer.go": `package dashboard

type holder[T Service] struct{}
`,
			},
			wantContains: []string{
				"internal/module/dashboard/consumer.go:3 type parameter T uses full orchestration service",
			},
		},
		{
			name: "cross-file alias method expression initializer",
			files: map[string]string{
				"internal/platform/mcpcontrol/alias.go": `package mcpcontrol

import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"

type Service = contract.OrchestrationService
`,
				"internal/platform/mcpcontrol/consumer.go": `package mcpcontrol

var submit = Service.SubmitTurn
`,
			},
			wantContains: []string{
				"internal/platform/mcpcontrol/consumer.go:3 method expression submit uses full orchestration service",
			},
		},
	}
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

	loader := newOrchestrationServiceTypeGuardLoader(root, listed, contractPkg)
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
	if pkgPath == "" {
		return false
	}
	if pkgPath == orchestrationServiceContractPackagePath {
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
	root        string
	listed      map[string]orchestrationServiceGoListPackage
	checked     map[string]*orchestrationServiceCheckedPackage
	checking    map[string]bool
	stubs       map[string]*types.Package
	contractPkg *types.Package
	fallback    types.Importer
}

func newOrchestrationServiceTypeGuardLoader(
	root string,
	listed []orchestrationServiceGoListPackage,
	contractPkg *types.Package,
) *orchestrationServiceTypeGuardLoader {
	byImportPath := make(map[string]orchestrationServiceGoListPackage, len(listed))
	for _, pkg := range listed {
		byImportPath[pkg.ImportPath] = pkg
	}
	return &orchestrationServiceTypeGuardLoader{
		root:        root,
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
		return &orchestrationServiceCheckedPackage{
			pkgPath: importPath,
			types:   loader.stubPackage(importPath),
		}, nil
	}

	loader.checking[importPath] = true
	defer delete(loader.checking, importPath)

	fset := token.NewFileSet()
	files := orchestrationServicePackageFiles(listed)
	syntax := make([]*ast.File, 0, len(files))
	for _, filename := range files {
		if !filepath.IsAbs(filename) {
			filename = filepath.Join(listed.Dir, filename)
		}
		file, err := parser.ParseFile(fset, filename, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", filename, err)
		}
		syntax = append(syntax, file)
	}

	info := &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
	conf := types.Config{
		Importer: loader,
		Error:    func(error) {},
	}
	checkedTypes, _ := conf.Check(importPath, fset, syntax, info)
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

func (loader *orchestrationServiceTypeGuardLoader) Import(importPath string) (*types.Package, error) {
	if importPath == orchestrationServiceContractPackagePath {
		return loader.contractPkg, nil
	}
	if checked := loader.checked[importPath]; checked != nil && checked.types != nil {
		return checked.types, nil
	}
	if _, ok := loader.listed[importPath]; ok && !loader.checking[importPath] {
		checked, err := loader.check(importPath)
		if err == nil && checked.types != nil {
			return checked.types, nil
		}
	}
	if loader.fallback != nil {
		if pkg, err := loader.fallback.Import(importPath); err == nil {
			return pkg, nil
		}
	}
	return loader.stubPackage(importPath), nil
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

	info := &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
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

func collectOrchestrationServiceTypeUses(pkg *orchestrationServiceCheckedPackage, target *types.TypeName) []orchestrationServiceTypeUse {
	if pkg == nil || pkg.typesInfo == nil || target == nil {
		return nil
	}

	contexts := orchestrationServiceIdentContexts(pkg.syntax)
	seen := map[string]bool{}
	var uses []orchestrationServiceTypeUse
	for ident, obj := range pkg.typesInfo.Defs {
		if !orchestrationServiceObjectUsesTarget(obj, target) {
			continue
		}
		addOrchestrationServiceTypeUse(&uses, seen, pkg.fset, ident, obj, contexts[ident])
	}
	for ident, obj := range pkg.typesInfo.Uses {
		if !orchestrationServiceObjectUsesTarget(obj, target) {
			continue
		}
		addOrchestrationServiceTypeUse(&uses, seen, pkg.fset, ident, obj, contexts[ident])
	}
	sortOrchestrationServiceTypeUses(uses)
	return uses
}

func sortOrchestrationServiceTypeUses(uses []orchestrationServiceTypeUse) {
	sort.Slice(uses, func(i, j int) bool {
		return orchestrationServiceTypeUseSortKey(uses[i]) < orchestrationServiceTypeUseSortKey(uses[j])
	})
}

func orchestrationServiceTypeUseSortKey(use orchestrationServiceTypeUse) string {
	return fmt.Sprintf("%s\x00%09d\x00%s\x00%s\x00%s", use.relPath, use.line, use.kind, use.name, use.expr)
}

type orchestrationServiceIdentContext struct {
	parents []ast.Node
}

func orchestrationServiceIdentContexts(files []*ast.File) map[*ast.Ident]orchestrationServiceIdentContext {
	contexts := map[*ast.Ident]orchestrationServiceIdentContext{}
	for _, file := range files {
		var stack []ast.Node
		ast.Inspect(file, func(node ast.Node) bool {
			if node == nil {
				stack = stack[:len(stack)-1]
				return true
			}
			if ident, ok := node.(*ast.Ident); ok {
				parents := make([]ast.Node, len(stack))
				copy(parents, stack)
				contexts[ident] = orchestrationServiceIdentContext{parents: parents}
			}
			stack = append(stack, node)
			return true
		})
	}
	return contexts
}

func addOrchestrationServiceTypeUse(
	uses *[]orchestrationServiceTypeUse,
	seen map[string]bool,
	fset *token.FileSet,
	ident *ast.Ident,
	obj types.Object,
	ctx orchestrationServiceIdentContext,
) {
	if ident == nil || obj == nil {
		return
	}
	use := classifyOrchestrationServiceTypeUse(fset, ident, obj, ctx)
	if use.kind == "" {
		return
	}
	key := strings.Join([]string{use.relPath, fmt.Sprint(use.line), use.kind, use.name, use.expr}, "\x00")
	if seen[key] {
		return
	}
	seen[key] = true
	*uses = append(*uses, use)
}

func classifyOrchestrationServiceTypeUse(
	fset *token.FileSet,
	ident *ast.Ident,
	obj types.Object,
	ctx orchestrationServiceIdentContext,
) orchestrationServiceTypeUse {
	position := fset.Position(ident.Pos())
	use := orchestrationServiceTypeUse{
		relPath: orchestrationServiceTypeRelPath(position.Filename),
		line:    position.Line,
		expr:    orchestrationServiceTypeExprString(fset, ident, ctx),
	}
	if kind, name, ok := orchestrationServiceMethodExpressionContext(ident, obj, ctx); ok {
		use.kind = kind
		use.name = name
		return use
	}
	if kind, name, ok := orchestrationServiceFieldContext(ctx); ok {
		use.kind = kind
		use.name = name
		return use
	}
	if kind, name, ok := orchestrationServiceTypeSpecContext(ident, ctx); ok {
		use.kind = kind
		use.name = name
		return use
	}
	if spec := nearestOrchestrationServiceValueSpec(ctx); spec != nil {
		use.kind = "variable"
		use.name = valueSpecNames(spec)
		return use
	}
	use.kind = orchestrationServiceFallbackTypeUseKind(ctx)
	use.name = ident.Name
	return use
}

func orchestrationServiceTypeSpecContext(ident *ast.Ident, ctx orchestrationServiceIdentContext) (string, string, bool) {
	spec := nearestOrchestrationServiceTypeSpec(ctx)
	if spec == nil {
		return "", "", false
	}
	if spec.Name != ident {
		return "type declaration", spec.Name.Name, true
	}
	if spec.Assign.IsValid() {
		return "type alias", ident.Name, true
	}
	return "type declaration", ident.Name, true
}

func orchestrationServiceFallbackTypeUseKind(ctx orchestrationServiceIdentContext) string {
	switch {
	case hasOrchestrationServiceParent[*ast.TypeAssertExpr](ctx):
		return "type assertion"
	case hasOrchestrationServiceParent[*ast.TypeSwitchStmt](ctx):
		return "type switch case"
	case hasOrchestrationServiceParent[*ast.CompositeLit](ctx):
		return "composite literal"
	case hasOrchestrationServiceParent[*ast.CallExpr](ctx):
		return "type conversion"
	default:
		return "type reference"
	}
}

func orchestrationServiceMethodExpressionContext(
	ident *ast.Ident,
	obj types.Object,
	ctx orchestrationServiceIdentContext,
) (string, string, bool) {
	selector, ok := nearestOrchestrationServiceParent[*ast.SelectorExpr](ctx)
	if !ok || selector.X != ident {
		return "", "", false
	}
	if _, ok := obj.(*types.TypeName); !ok {
		return "", "", false
	}
	if spec := nearestOrchestrationServiceValueSpec(ctx); spec != nil {
		return "method expression", valueSpecNames(spec), true
	}
	return "method expression", selector.Sel.Name, true
}

func orchestrationServiceFieldContext(ctx orchestrationServiceIdentContext) (string, string, bool) {
	field, ok := nearestOrchestrationServiceParent[*ast.Field](ctx)
	if !ok {
		return "", "", false
	}
	fieldList, ok := nearestOrchestrationServiceParent[*ast.FieldList](ctx)
	if !ok {
		return "", "", false
	}
	if spec := nearestOrchestrationServiceTypeSpec(ctx); spec != nil && spec.TypeParams == fieldList {
		return "type parameter", fieldName(field), true
	}
	if fn, ok := nearestOrchestrationServiceParent[*ast.FuncType](ctx); ok {
		switch fieldList {
		case fn.TypeParams:
			return "type parameter", fieldName(field), true
		case fn.Params:
			return "parameter", fieldName(field), true
		case fn.Results:
			return "return value", fieldName(field), true
		}
	}
	if _, ok := nearestOrchestrationServiceParent[*ast.StructType](ctx); ok {
		return "field", fieldName(field), true
	}
	return "", "", false
}

func nearestOrchestrationServiceTypeSpec(ctx orchestrationServiceIdentContext) *ast.TypeSpec {
	spec, _ := nearestOrchestrationServiceParent[*ast.TypeSpec](ctx)
	return spec
}

func nearestOrchestrationServiceValueSpec(ctx orchestrationServiceIdentContext) *ast.ValueSpec {
	spec, _ := nearestOrchestrationServiceParent[*ast.ValueSpec](ctx)
	return spec
}

func nearestOrchestrationServiceParent[T ast.Node](ctx orchestrationServiceIdentContext) (T, bool) {
	var zero T
	for i := len(ctx.parents) - 1; i >= 0; i-- {
		if typed, ok := ctx.parents[i].(T); ok {
			return typed, true
		}
	}
	return zero, false
}

func hasOrchestrationServiceParent[T ast.Node](ctx orchestrationServiceIdentContext) bool {
	_, ok := nearestOrchestrationServiceParent[T](ctx)
	return ok
}

func orchestrationServiceObjectUsesTarget(obj types.Object, target *types.TypeName) bool {
	switch typed := obj.(type) {
	case *types.TypeName:
		return orchestrationServiceTypeUsesTarget(typed.Type(), target)
	case *types.Var:
		return orchestrationServiceTypeUsesTarget(typed.Type(), target)
	default:
		return false
	}
}

func orchestrationServiceTypeUsesTarget(typ types.Type, target *types.TypeName) bool {
	if typ == nil || target == nil {
		return false
	}
	unaliased := types.Unalias(typ)
	switch typed := unaliased.(type) {
	case *types.Named:
		return typed.Obj() == target
	case *types.TypeParam:
		return orchestrationServiceTypeUsesTarget(typed.Constraint(), target)
	case *types.Interface:
		return orchestrationServiceInterfaceUsesTarget(typed, target)
	case *types.Signature:
		return orchestrationServiceTupleUsesTarget(typed.Params(), target) ||
			orchestrationServiceTupleUsesTarget(typed.Results(), target)
	case *types.Tuple:
		return orchestrationServiceTupleUsesTarget(typed, target)
	default:
		return orchestrationServiceContainerTypeUsesTarget(unaliased, target)
	}
}

func orchestrationServiceInterfaceUsesTarget(iface *types.Interface, target *types.TypeName) bool {
	for embedded := range iface.EmbeddedTypes() {
		if orchestrationServiceTypeUsesTarget(embedded, target) {
			return true
		}
	}
	return false
}

func orchestrationServiceContainerTypeUsesTarget(typ types.Type, target *types.TypeName) bool {
	switch typed := typ.(type) {
	case *types.Pointer:
		return orchestrationServiceTypeUsesTarget(typed.Elem(), target)
	case *types.Slice:
		return orchestrationServiceTypeUsesTarget(typed.Elem(), target)
	case *types.Array:
		return orchestrationServiceTypeUsesTarget(typed.Elem(), target)
	case *types.Map:
		return orchestrationServiceTypeUsesTarget(typed.Key(), target) ||
			orchestrationServiceTypeUsesTarget(typed.Elem(), target)
	case *types.Chan:
		return orchestrationServiceTypeUsesTarget(typed.Elem(), target)
	}
	return false
}

func orchestrationServiceTupleUsesTarget(tuple *types.Tuple, target *types.TypeName) bool {
	if tuple == nil {
		return false
	}
	for variable := range tuple.Variables() {
		if orchestrationServiceTypeUsesTarget(variable.Type(), target) {
			return true
		}
	}
	return false
}

func orchestrationServiceTypeRelPath(filename string) string {
	normalized := filepath.ToSlash(filename)
	for _, marker := range []string{"/cmd/", "/internal/"} {
		if index := strings.Index(normalized, marker); index >= 0 {
			return normalized[index+1:]
		}
	}
	return normalized
}

func orchestrationServiceTypeExprString(fset *token.FileSet, ident *ast.Ident, ctx orchestrationServiceIdentContext) string {
	if selector, ok := nearestOrchestrationServiceParent[*ast.SelectorExpr](ctx); ok {
		if selector.X == ident || selector.Sel == ident {
			return orchestrationServiceNodeString(fset, selector)
		}
	}
	return ident.Name
}

func orchestrationServiceNodeString(fset *token.FileSet, node any) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, node); err != nil {
		return fmt.Sprint(node)
	}
	return buf.String()
}

func isAllowedOrchestrationServiceTypeUse(_ orchestrationServiceTypeUse) bool {
	return false
}

func orchestrationServiceTypeUseMessages(uses []orchestrationServiceTypeUse) []string {
	messages := make([]string, 0, len(uses))
	for _, use := range uses {
		messages = append(messages, use.violationMessage())
	}
	return messages
}

func (use orchestrationServiceTypeUse) violationMessage() string {
	return fmt.Sprintf("%s:%d %s %s uses full orchestration service via %s; split it behind a narrow contract port", use.relPath, use.line, use.kind, use.name, use.expr)
}
