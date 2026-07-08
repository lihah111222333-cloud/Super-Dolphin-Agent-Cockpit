package archtest_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const (
	orchestrationServiceFacadeRelPath = "cmd/mcp-orch/orchestration/service.go"
	orchestrationRPCFacadeRelPath     = "cmd/mcp-orch/orchestration/rpc.go"
)

type orchestrationServiceTemporaryAllowance struct {
	max    int
	reason string
}

type orchestrationServiceAliasSource struct {
	name    string
	relPath string
	line    int
	facade  bool
}

func (src orchestrationServiceAliasSource) isProductionFacadeServiceAlias() bool {
	return src.facade && src.name == "Service" && src.relPath == orchestrationServiceFacadeRelPath
}

type orchestrationServiceUse struct {
	relPath  string
	line     int
	kind     string
	name     string
	function string
	expr     string
	source   orchestrationServiceAliasSource
}

func TestOrchestrationServiceConsumersUseNarrowPorts(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	temporaryAllowances := temporaryOrchestrationServiceConsumers()
	packageAliases := orchestrationServicePackageAliases(t, root)
	var violations []string
	temporaryUses := map[string][]orchestrationServiceUse{}
	for _, absPath := range walkGoFiles(t, root, "cmd", "internal") {
		relPath, err := filepath.Rel(root, absPath)
		if err != nil {
			t.Fatalf("rel path for %s: %v", absPath, err)
		}
		relPath = filepath.ToSlash(relPath)
		uses := orchestrationServiceUses(t, root, absPath, packageAliases[filepath.Dir(absPath)])
		for _, use := range uses {
			if isAllowedOrchestrationServiceSemanticUse(use) {
				continue
			}
			if _, ok := temporaryAllowances[relPath]; ok {
				temporaryUses[relPath] = append(temporaryUses[relPath], use)
				continue
			}
			violations = append(violations, use.violationMessage())
			continue
		}
	}
	for relPath, uses := range temporaryUses {
		allowance := temporaryAllowances[relPath]
		if len(uses) > allowance.max {
			violations = append(violations, temporaryOrchestrationServiceViolationMessage(relPath, uses, allowance))
		}
	}
	failIfViolations(t, violations)
}

func temporaryOrchestrationServiceConsumers() map[string]orchestrationServiceTemporaryAllowance {
	return map[string]orchestrationServiceTemporaryAllowance{}
}

func orchestrationServiceUses(t *testing.T, root string, absPath string, packageAliases map[string]orchestrationServiceAliasSource) []orchestrationServiceUse {
	t.Helper()

	relPath, err := filepath.Rel(root, absPath)
	if err != nil {
		t.Fatalf("rel path for %s: %v", absPath, err)
	}
	relPath = filepath.ToSlash(relPath)
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, absPath, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", absPath, err)
	}
	contractAliases := contractImportAliases(t, absPath, file)
	return collectOrchestrationServiceUses(fset, file, relPath, contractAliases, packageAliases)
}

func orchestrationServicePackageAliases(t *testing.T, root string) map[string]map[string]orchestrationServiceAliasSource {
	t.Helper()

	aliasesByDir := map[string]map[string]orchestrationServiceAliasSource{}
	files := walkGoFiles(t, root, "cmd", "internal")
	for {
		changed := false
		for _, absPath := range files {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, absPath, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatalf("parse %s: %v", absPath, err)
			}
			relPath, err := filepath.Rel(root, absPath)
			if err != nil {
				t.Fatalf("rel path for %s: %v", absPath, err)
			}
			relPath = filepath.ToSlash(relPath)
			dir := filepath.Dir(absPath)
			contractAliases := contractImportAliases(t, absPath, file)
			localAliases := orchestrationServiceLocalAliasSources(fset, file, relPath, contractAliases, aliasesByDir[dir])
			if len(localAliases) == 0 {
				continue
			}
			aliases := aliasesByDir[dir]
			if aliases == nil {
				aliases = map[string]orchestrationServiceAliasSource{}
				aliasesByDir[dir] = aliases
			}
			for name, source := range localAliases {
				if aliases[name] == source {
					continue
				}
				aliases[name] = source
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return aliasesByDir
}

func contractImportAliases(t *testing.T, absPath string, file *ast.File) map[string]bool {
	t.Helper()

	contractAliases := map[string]bool{}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatalf("unquote import %s: %v", spec.Path.Value, err)
		}
		if path != modulePath+"/internal/contract" {
			continue
		}
		if spec.Name == nil {
			contractAliases["contract"] = true
			continue
		}
		switch spec.Name.Name {
		case ".":
			t.Fatalf("%s dot-imports internal/contract; use explicit contract.<Type> imports", absPath)
		case "_":
			continue
		default:
			contractAliases[spec.Name.Name] = true
		}
	}
	return contractAliases
}

func collectOrchestrationServiceUses(
	fset *token.FileSet,
	file *ast.File,
	relPath string,
	contractAliases map[string]bool,
	packageAliases map[string]orchestrationServiceAliasSource,
) []orchestrationServiceUse {
	localAliases := orchestrationServiceLocalAliasSources(fset, file, relPath, contractAliases, packageAliases)
	aliases := mergeOrchestrationServiceAliases(packageAliases, localAliases)
	var uses []orchestrationServiceUse
	for _, decl := range file.Decls {
		collectOrchestrationServiceDeclUses(&uses, fset, decl, relPath, "", contractAliases, aliases)
	}
	return uses
}

func collectOrchestrationServiceDeclUses(
	uses *[]orchestrationServiceUse,
	fset *token.FileSet,
	decl ast.Decl,
	relPath string,
	function string,
	contractAliases map[string]bool,
	aliases map[string]orchestrationServiceAliasSource,
) {
	switch typed := decl.(type) {
	case *ast.GenDecl:
		collectOrchestrationServiceGenDeclUses(uses, fset, typed, relPath, function, contractAliases, aliases)
	case *ast.FuncDecl:
		collectOrchestrationServiceFuncSignatureUses(uses, fset, typed, relPath, contractAliases, aliases)
		if typed.Body != nil {
			collectOrchestrationServiceBlockUses(uses, fset, typed.Body, relPath, typed.Name.Name, contractAliases, aliases)
		}
	}
}

func collectOrchestrationServiceBlockUses(
	uses *[]orchestrationServiceUse,
	fset *token.FileSet,
	block *ast.BlockStmt,
	relPath string,
	function string,
	contractAliases map[string]bool,
	aliases map[string]orchestrationServiceAliasSource,
) {
	ast.Inspect(block, func(n ast.Node) bool {
		switch typed := n.(type) {
		case *ast.TypeSpec:
			collectOrchestrationServiceTypeSpecUses(uses, fset, typed, relPath, function, contractAliases, aliases)
			return false
		case *ast.ValueSpec:
			collectOrchestrationServiceValueSpecUses(uses, fset, typed, relPath, function, contractAliases, aliases)
			return false
		}
		return true
	})
}

func collectOrchestrationServiceGenDeclUses(
	uses *[]orchestrationServiceUse,
	fset *token.FileSet,
	decl *ast.GenDecl,
	relPath string,
	function string,
	contractAliases map[string]bool,
	aliases map[string]orchestrationServiceAliasSource,
) {
	for _, spec := range decl.Specs {
		switch typed := spec.(type) {
		case *ast.TypeSpec:
			collectOrchestrationServiceTypeSpecUses(uses, fset, typed, relPath, function, contractAliases, aliases)
		case *ast.ValueSpec:
			collectOrchestrationServiceValueSpecUses(uses, fset, typed, relPath, function, contractAliases, aliases)
		}
	}
}

func collectOrchestrationServiceTypeSpecUses(
	uses *[]orchestrationServiceUse,
	fset *token.FileSet,
	spec *ast.TypeSpec,
	relPath string,
	function string,
	contractAliases map[string]bool,
	aliases map[string]orchestrationServiceAliasSource,
) {
	kind := "type declaration"
	if spec.Assign.IsValid() {
		kind = "type alias"
	}
	ctx := orchestrationServiceUse{relPath: relPath, kind: kind, name: spec.Name.Name, function: function}
	*uses = append(*uses, collectOrchestrationServiceTypeExprUses(fset, spec.Type, ctx, contractAliases, aliases)...)
}

func collectOrchestrationServiceValueSpecUses(
	uses *[]orchestrationServiceUse,
	fset *token.FileSet,
	spec *ast.ValueSpec,
	relPath string,
	function string,
	contractAliases map[string]bool,
	aliases map[string]orchestrationServiceAliasSource,
) {
	if spec.Type == nil {
		return
	}
	ctx := orchestrationServiceUse{relPath: relPath, kind: "variable", name: valueSpecNames(spec), function: function}
	*uses = append(*uses, collectOrchestrationServiceTypeExprUses(fset, spec.Type, ctx, contractAliases, aliases)...)
}

func collectOrchestrationServiceFuncSignatureUses(
	uses *[]orchestrationServiceUse,
	fset *token.FileSet,
	fn *ast.FuncDecl,
	relPath string,
	contractAliases map[string]bool,
	aliases map[string]orchestrationServiceAliasSource,
) {
	*uses = append(*uses, collectOrchestrationServiceFieldListUses(fset, fn.Type.Params, relPath, fn.Name.Name, "parameter", contractAliases, aliases)...)
	*uses = append(*uses, collectOrchestrationServiceFieldListUses(fset, fn.Type.Results, relPath, fn.Name.Name, "return value", contractAliases, aliases)...)
}

func collectOrchestrationServiceFieldListUses(
	fset *token.FileSet,
	list *ast.FieldList,
	relPath string,
	function string,
	kind string,
	contractAliases map[string]bool,
	aliases map[string]orchestrationServiceAliasSource,
) []orchestrationServiceUse {
	if list == nil {
		return nil
	}
	var uses []orchestrationServiceUse
	for _, field := range list.List {
		ctx := orchestrationServiceUse{relPath: relPath, kind: kind, name: fieldName(field), function: function}
		uses = append(uses, collectOrchestrationServiceTypeExprUses(fset, field.Type, ctx, contractAliases, aliases)...)
	}
	return uses
}

func collectOrchestrationServiceTypeExprUses(
	fset *token.FileSet,
	expr ast.Expr,
	ctx orchestrationServiceUse,
	contractAliases map[string]bool,
	aliases map[string]orchestrationServiceAliasSource,
) []orchestrationServiceUse {
	if expr == nil {
		return nil
	}
	if uses, ok := collectOrchestrationServiceLeafTypeExprUse(fset, expr, ctx, contractAliases, aliases); ok {
		return uses
	}
	if uses, ok := collectOrchestrationServiceUnaryTypeExprUses(fset, expr, ctx, contractAliases, aliases); ok {
		return uses
	}
	return collectOrchestrationServiceCompositeTypeExprUses(fset, expr, ctx, contractAliases, aliases)
}

func collectOrchestrationServiceLeafTypeExprUse(
	fset *token.FileSet,
	expr ast.Expr,
	ctx orchestrationServiceUse,
	contractAliases map[string]bool,
	aliases map[string]orchestrationServiceAliasSource,
) ([]orchestrationServiceUse, bool) {
	switch typed := expr.(type) {
	case *ast.SelectorExpr:
		if isOrchestrationServiceSelector(typed, contractAliases) {
			return []orchestrationServiceUse{ctx.withExprUse(fset, typed.Pos(), selectorExprName(typed), directOrchestrationServiceSource())}, true
		}
		return nil, true
	case *ast.Ident:
		if source, ok := aliases[typed.Name]; ok {
			return []orchestrationServiceUse{ctx.withExprUse(fset, typed.Pos(), typed.Name, source)}, true
		}
		return nil, true
	default:
		return nil, false
	}
}

func collectOrchestrationServiceUnaryTypeExprUses(
	fset *token.FileSet,
	expr ast.Expr,
	ctx orchestrationServiceUse,
	contractAliases map[string]bool,
	aliases map[string]orchestrationServiceAliasSource,
) ([]orchestrationServiceUse, bool) {
	switch typed := expr.(type) {
	case *ast.StarExpr:
		return collectOrchestrationServiceTypeExprUses(fset, typed.X, ctx, contractAliases, aliases), true
	case *ast.ArrayType:
		return collectOrchestrationServiceTypeExprUses(fset, typed.Elt, ctx, contractAliases, aliases), true
	case *ast.ChanType:
		return collectOrchestrationServiceTypeExprUses(fset, typed.Value, ctx, contractAliases, aliases), true
	case *ast.Ellipsis:
		return collectOrchestrationServiceTypeExprUses(fset, typed.Elt, ctx, contractAliases, aliases), true
	case *ast.ParenExpr:
		return collectOrchestrationServiceTypeExprUses(fset, typed.X, ctx, contractAliases, aliases), true
	default:
		return nil, false
	}
}

func collectOrchestrationServiceCompositeTypeExprUses(
	fset *token.FileSet,
	expr ast.Expr,
	ctx orchestrationServiceUse,
	contractAliases map[string]bool,
	aliases map[string]orchestrationServiceAliasSource,
) []orchestrationServiceUse {
	switch typed := expr.(type) {
	case *ast.MapType:
		uses := collectOrchestrationServiceTypeExprUses(fset, typed.Key, ctx, contractAliases, aliases)
		return append(uses, collectOrchestrationServiceTypeExprUses(fset, typed.Value, ctx, contractAliases, aliases)...)
	case *ast.IndexExpr:
		uses := collectOrchestrationServiceTypeExprUses(fset, typed.X, ctx, contractAliases, aliases)
		return append(uses, collectOrchestrationServiceTypeExprUses(fset, typed.Index, ctx, contractAliases, aliases)...)
	case *ast.IndexListExpr:
		uses := collectOrchestrationServiceTypeExprUses(fset, typed.X, ctx, contractAliases, aliases)
		for _, index := range typed.Indices {
			uses = append(uses, collectOrchestrationServiceTypeExprUses(fset, index, ctx, contractAliases, aliases)...)
		}
		return uses
	case *ast.FuncType:
		uses := collectOrchestrationServiceFieldListUses(fset, typed.Params, ctx.relPath, ctx.function, "parameter", contractAliases, aliases)
		return append(uses, collectOrchestrationServiceFieldListUses(fset, typed.Results, ctx.relPath, ctx.function, "return value", contractAliases, aliases)...)
	case *ast.StructType:
		return collectOrchestrationServiceFieldListUses(fset, typed.Fields, ctx.relPath, ctx.function, "field", contractAliases, aliases)
	case *ast.InterfaceType:
		return collectOrchestrationServiceFieldListUses(fset, typed.Methods, ctx.relPath, ctx.function, "field", contractAliases, aliases)
	}
	return nil
}

func (use orchestrationServiceUse) withExprUse(
	fset *token.FileSet,
	pos token.Pos,
	expr string,
	source orchestrationServiceAliasSource,
) orchestrationServiceUse {
	use.line = fset.Position(pos).Line
	use.expr = expr
	use.source = source
	return use
}

func orchestrationServiceLocalAliasSources(
	fset *token.FileSet,
	file *ast.File,
	relPath string,
	contractAliases map[string]bool,
	packageAliases map[string]orchestrationServiceAliasSource,
) map[string]orchestrationServiceAliasSource {
	aliases := map[string]orchestrationServiceAliasSource{}
	for {
		changed := false
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				if _, exists := aliases[typeSpec.Name.Name]; exists {
					continue
				}
				if _, ok := orchestrationServiceAliasSourceForExpr(typeSpec.Type, contractAliases, aliases, packageAliases); !ok {
					continue
				}
				aliases[typeSpec.Name.Name] = orchestrationServiceAliasSource{
					name:    typeSpec.Name.Name,
					relPath: relPath,
					line:    fset.Position(typeSpec.Pos()).Line,
					facade:  isProductionFacadeServiceAliasDeclaration(relPath, typeSpec, contractAliases),
				}
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return aliases
}

func orchestrationServiceAliasSourceForExpr(
	expr ast.Expr,
	contractAliases map[string]bool,
	localAliases map[string]orchestrationServiceAliasSource,
	packageAliases map[string]orchestrationServiceAliasSource,
) (orchestrationServiceAliasSource, bool) {
	switch typed := expr.(type) {
	case *ast.ParenExpr:
		return orchestrationServiceAliasSourceForExpr(typed.X, contractAliases, localAliases, packageAliases)
	case *ast.SelectorExpr:
		if isOrchestrationServiceSelector(typed, contractAliases) {
			return directOrchestrationServiceSource(), true
		}
	case *ast.Ident:
		if source, ok := localAliases[typed.Name]; ok {
			return source, true
		}
		if source, ok := packageAliases[typed.Name]; ok {
			return source, true
		}
	}
	return orchestrationServiceAliasSource{}, false
}

func directOrchestrationServiceSource() orchestrationServiceAliasSource {
	return orchestrationServiceAliasSource{name: "contract.OrchestrationService"}
}

func isProductionFacadeServiceAliasDeclaration(relPath string, spec *ast.TypeSpec, contractAliases map[string]bool) bool {
	return relPath == orchestrationServiceFacadeRelPath &&
		spec.Name.Name == "Service" &&
		spec.Assign.IsValid() &&
		isOrchestrationServiceSelector(spec.Type, contractAliases)
}

func mergeOrchestrationServiceAliases(
	packageAliases map[string]orchestrationServiceAliasSource,
	localAliases map[string]orchestrationServiceAliasSource,
) map[string]orchestrationServiceAliasSource {
	merged := map[string]orchestrationServiceAliasSource{}
	maps.Copy(merged, packageAliases)
	maps.Copy(merged, localAliases)
	return merged
}

func isAllowedOrchestrationServiceSemanticUse(use orchestrationServiceUse) bool {
	return isAllowedOrchestrationServiceFacadeUse(use) || isAllowedOrchestrationRPCFacadeUse(use)
}

func isAllowedOrchestrationServiceFacadeUse(use orchestrationServiceUse) bool {
	if use.relPath != orchestrationServiceFacadeRelPath {
		return false
	}
	if use.kind == "type alias" && use.name == "Service" && use.expr == "contract.OrchestrationService" {
		return true
	}
	return use.kind == "return value" &&
		use.function == "ProvideServiceInterface" &&
		use.expr == "Service" &&
		use.source.isProductionFacadeServiceAlias()
}

func isAllowedOrchestrationRPCFacadeUse(use orchestrationServiceUse) bool {
	if use.relPath != orchestrationRPCFacadeRelPath {
		return false
	}
	if use.kind != "parameter" || use.name != "svc" || use.expr != "Service" || !use.source.isProductionFacadeServiceAlias() {
		return false
	}
	switch use.function {
	case "ProvideRPCFacade", "submissionFromParams", "submissionThreadID":
		return true
	default:
		return false
	}
}

func (use orchestrationServiceUse) violationMessage() string {
	location := fmt.Sprintf("%s:%d", use.relPath, use.line)
	context := use.kind
	if use.name != "" {
		context += " " + use.name
	}
	if use.function != "" {
		context += " in " + use.function
	}
	return fmt.Sprintf("%s %s uses full orchestration service via %s (%s); split it behind a narrow contract port", location, context, use.expr, use.sourceDescription())
}

func (use orchestrationServiceUse) sourceDescription() string {
	if use.source.relPath == "" {
		return use.source.name
	}
	label := "local alias"
	if use.source.relPath != use.relPath {
		label = "package alias"
	}
	if use.source.isProductionFacadeServiceAlias() {
		label = "package facade alias"
	}
	return fmt.Sprintf("%s %s from %s:%d", label, use.source.name, use.source.relPath, use.source.line)
}

func temporaryOrchestrationServiceViolationMessage(
	relPath string,
	uses []orchestrationServiceUse,
	allowance orchestrationServiceTemporaryAllowance,
) string {
	details := make([]string, 0, len(uses))
	for _, use := range uses {
		details = append(details, fmt.Sprintf("%s:%d %s via %s", use.relPath, use.line, use.kind, use.expr))
	}
	return fmt.Sprintf("%s directly consumes contract.OrchestrationService %d time(s), max %d (%s): %s", relPath, len(uses), allowance.max, allowance.reason, strings.Join(details, "; "))
}

func fieldName(field *ast.Field) string {
	if len(field.Names) == 0 {
		return "(anonymous)"
	}
	names := make([]string, 0, len(field.Names))
	for _, name := range field.Names {
		names = append(names, name.Name)
	}
	return strings.Join(names, ", ")
}

func valueSpecNames(spec *ast.ValueSpec) string {
	names := make([]string, 0, len(spec.Names))
	for _, name := range spec.Names {
		names = append(names, name.Name)
	}
	return strings.Join(names, ", ")
}

func selectorExprName(expr *ast.SelectorExpr) string {
	if base, ok := expr.X.(*ast.Ident); ok {
		return base.Name + "." + expr.Sel.Name
	}
	return expr.Sel.Name
}

func orchestrationServiceUsesFromSource(
	t *testing.T,
	relPath string,
	src string,
	packageAliases map[string]orchestrationServiceAliasSource,
) []orchestrationServiceUse {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, relPath, src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	contractAliases := contractImportAliases(t, relPath, file)
	return collectOrchestrationServiceUses(fset, file, relPath, contractAliases, packageAliases)
}

func unexpectedOrchestrationServiceUseMessages(uses []orchestrationServiceUse) []string {
	var violations []string
	for _, use := range uses {
		if isAllowedOrchestrationServiceSemanticUse(use) {
			continue
		}
		violations = append(violations, use.violationMessage())
	}
	return violations
}

type orchestrationServiceSemanticGuardCase struct {
	name         string
	relPath      string
	src          string
	packageAlias map[string]orchestrationServiceAliasSource
	wantContains []string
}

func TestOrchestrationServiceSemanticGuardFixtures(t *testing.T) {
	t.Parallel()

	for _, tt := range orchestrationServiceSemanticGuardCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := unexpectedOrchestrationServiceUseMessages(orchestrationServiceUsesFromSource(t, tt.relPath, tt.src, tt.packageAlias))
			if len(tt.wantContains) == 0 {
				if len(got) > 0 {
					t.Fatalf("unexpected violations:\n%s", strings.Join(got, "\n"))
				}
				return
			}
			for _, want := range tt.wantContains {
				if !containsViolation(got, want) {
					t.Fatalf("missing violation containing %q; got:\n%s", want, strings.Join(got, "\n"))
				}
			}
		})
	}
}

func orchestrationServiceSemanticGuardCases() []orchestrationServiceSemanticGuardCase {
	cases := []orchestrationServiceSemanticGuardCase{allowedServiceFacadeGuardCase()}
	cases = append(cases, rejectedServiceFacadeGuardCases()...)
	cases = append(cases, allowedRPCFacadeGuardCase())
	return append(cases, rejectedRPCFacadeGuardCases()...)
}

func facadeServiceAliases() map[string]orchestrationServiceAliasSource {
	return map[string]orchestrationServiceAliasSource{
		"Service": {name: "Service", relPath: orchestrationServiceFacadeRelPath, line: 39, facade: true},
	}
}

func allowedServiceFacadeGuardCase() orchestrationServiceSemanticGuardCase {
	return orchestrationServiceSemanticGuardCase{
		name:    "service facade allows only alias and interface provider return",
		relPath: orchestrationServiceFacadeRelPath,
		src: `package fixture

import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"

type Service = contract.OrchestrationService

type service struct{}

func ProvideServiceInterface(s *service) Service { return s }
`,
	}
}

func rejectedServiceFacadeGuardCases() []orchestrationServiceSemanticGuardCase {
	return []orchestrationServiceSemanticGuardCase{
		{
			name:    "service facade rejects local alias",
			relPath: orchestrationServiceFacadeRelPath,
			src: `package fixture
import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
type Service = contract.OrchestrationService
type FullService = contract.OrchestrationService
`,
			wantContains: []string{"type alias FullService uses full orchestration service"},
		},
		{
			name:    "service facade rejects field",
			relPath: orchestrationServiceFacadeRelPath,
			src: `package fixture
import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
type Service = contract.OrchestrationService
type holder struct { service Service }
`,
			wantContains: []string{"field service uses full orchestration service"},
		},
		{
			name:    "service facade rejects parameter",
			relPath: orchestrationServiceFacadeRelPath,
			src: `package fixture
import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
type Service = contract.OrchestrationService
func use(svc Service) {}
`,
			wantContains: []string{"parameter svc in use uses full orchestration service"},
		},
		{
			name:    "service facade rejects return",
			relPath: orchestrationServiceFacadeRelPath,
			src: `package fixture
import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
type Service = contract.OrchestrationService
func use() Service { return nil }
`,
			wantContains: []string{"return value (anonymous) in use uses full orchestration service"},
		},
		{
			name:    "service facade rejects variable",
			relPath: orchestrationServiceFacadeRelPath,
			src: `package fixture
import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
type Service = contract.OrchestrationService
var svc Service
`,
			wantContains: []string{"variable svc uses full orchestration service"},
		},
	}
}

func allowedRPCFacadeGuardCase() orchestrationServiceSemanticGuardCase {
	return orchestrationServiceSemanticGuardCase{
		name:    "rpc facade allows service parameter only from package facade alias",
		relPath: orchestrationRPCFacadeRelPath,
		src: `package fixture
func ProvideRPCFacade(svc Service) any { return nil }
func submissionFromParams(ctx any, svc Service, p any) (any, error) { return nil, nil }
func submissionThreadID(ctx any, svc Service, agentID string) string { return "" }
`,
		packageAlias: facadeServiceAliases(),
	}
}

func rejectedRPCFacadeGuardCases() []orchestrationServiceSemanticGuardCase {
	return []orchestrationServiceSemanticGuardCase{
		{
			name:    "rpc facade rejects local alias source",
			relPath: orchestrationRPCFacadeRelPath,
			src: `package fixture
import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
type Service = contract.OrchestrationService
func ProvideRPCFacade(svc Service) any { return nil }
`,
			wantContains: []string{"type alias Service uses full orchestration service", "parameter svc in ProvideRPCFacade uses full orchestration service"},
		},
		{
			name:         "rpc facade rejects field even with package facade alias",
			relPath:      orchestrationRPCFacadeRelPath,
			src:          "package fixture\ntype holder struct { service Service }\n",
			packageAlias: facadeServiceAliases(),
			wantContains: []string{"field service uses full orchestration service"},
		},
		{
			name:         "rpc facade rejects unexpected parameter even with package facade alias",
			relPath:      orchestrationRPCFacadeRelPath,
			src:          "package fixture\nfunc helper(svc Service) {}\n",
			packageAlias: facadeServiceAliases(),
			wantContains: []string{"parameter svc in helper uses full orchestration service"},
		},
		{
			name:         "rpc facade rejects return even with package facade alias",
			relPath:      orchestrationRPCFacadeRelPath,
			src:          "package fixture\nfunc helper() Service { return nil }\n",
			packageAlias: facadeServiceAliases(),
			wantContains: []string{"return value (anonymous) in helper uses full orchestration service"},
		},
		{
			name:         "cross-file package alias does not bypass non-rpc files",
			relPath:      "cmd/mcp-orch/orchestration/other.go",
			src:          "package fixture\nfunc helper(svc Service) {}\n",
			packageAlias: facadeServiceAliases(),
			wantContains: []string{"cmd/mcp-orch/orchestration/other.go:2 parameter svc in helper uses full orchestration service"},
		},
	}
}

func containsViolation(violations []string, want string) bool {
	for _, violation := range violations {
		if strings.Contains(violation, want) {
			return true
		}
	}
	return false
}

func isOrchestrationServiceSelector(expr ast.Expr, contractAliases map[string]bool) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "OrchestrationService" {
		return false
	}
	base, ok := selector.X.(*ast.Ident)
	return ok && contractAliases[base.Name]
}
