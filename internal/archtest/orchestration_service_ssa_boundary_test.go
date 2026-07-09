package archtest_test

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

type orchestrationServiceSSAUse struct {
	relPath  string
	line     int
	function string
	symbol   string
	kind     string
	detail   string
}

type orchestrationServiceSSAAllowance struct {
	relPath  string
	function string
	symbol   string
	kind     string
	reason   string
}

type orchestrationServiceSSAGuardFixture struct {
	name         string
	files        map[string]string
	wantContains []string
}

func TestOrchestrationServiceSSAConsumersDoNotPropagateFullService(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	target := mustLoadOrchestrationServiceTypeObject(t, root)
	pkgs := loadOrchestrationServiceTypeGuardPackages(t, root, target.Pkg())
	allowances := orchestrationServiceSSAAllowances()
	var violations []string
	for _, pkg := range pkgs {
		if !isOrchestrationServiceTypeGuardProductionPackage(pkg) {
			continue
		}
		for _, use := range collectOrchestrationServiceSSAUses(t, pkg, target) {
			if isAllowedOrchestrationServiceSSAUse(use, allowances) {
				continue
			}
			violations = append(violations, use.violationMessage())
		}
	}
	sort.Strings(violations)
	failIfViolations(t, violations)
}

func TestOrchestrationServiceSSAGuardFixtures(t *testing.T) {
	t.Parallel()

	target := mustLoadOrchestrationServiceTypeObject(t, repoRoot(t))
	for _, tt := range orchestrationServiceSSAGuardFixtures() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pkg := typeCheckOrchestrationServiceFixturePackage(t, target.Pkg(), tt.files)
			got := orchestrationServiceSSAUseMessages(collectOrchestrationServiceSSAUses(t, pkg, target))
			for _, want := range tt.wantContains {
				if !containsViolation(got, want) {
					t.Fatalf("missing violation containing %q; got:\n%s", want, strings.Join(got, "\n"))
				}
			}
		})
	}
}

func orchestrationServiceSSAGuardFixtures() []orchestrationServiceSSAGuardFixture {
	return []orchestrationServiceSSAGuardFixture{
		{
			name: "signature and field propagation",
			files: map[string]string{
				"cmd/mcp-orch/orchestration/semantic.go": `package orchestration

import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"

type holder struct { service contract.OrchestrationService }

func returns(svc contract.OrchestrationService) contract.OrchestrationService { return svc }
`,
			},
			wantContains: []string{
				"field service uses full orchestration service",
				"parameter svc in returns uses full orchestration service",
				"return value (anonymous) in returns uses full orchestration service",
			},
		},
		{
			name: "interface assertion and conversion propagation",
			files: map[string]string{
				"cmd/mcp-orch/orchestration/semantic.go": `package orchestration

import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"

func conversions(value any, svc contract.OrchestrationService) {
	_, _ = value.(contract.OrchestrationService)
	_ = any(svc)
}
`,
			},
			wantContains: []string{
				"interface assertion in conversions uses full orchestration service",
				"type conversion in conversions uses full orchestration service",
			},
		},
		{
			name: "generic constraint propagation",
			files: map[string]string{
				"internal/module/dashboard/semantic.go": `package dashboard

import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"

func generic[T contract.OrchestrationService](value T) {}
`,
			},
			wantContains: []string{
				"generic constraint T in generic uses full orchestration service",
			},
		},
		{
			name: "method value and closure capture propagation",
			files: map[string]string{
				"internal/platform/mcpcontrol/semantic.go": `package mcpcontrol

import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"

func methodValue(svc contract.OrchestrationService) any {
	submit := svc.SubmitTurn
	captured := func() { _ = svc }
	return func(next contract.OrchestrationService) { _ = submit; captured(); _ = next }
}
`,
			},
			wantContains: []string{
				"method value SubmitTurn in methodValue uses full orchestration service",
				"closure capture svc in methodValue uses full orchestration service",
				"function value propagation methodValue$2 in methodValue uses full orchestration service",
			},
		},
		{
			name: "method expression function value propagation",
			files: map[string]string{
				"internal/platform/mcpcontrol/semantic.go": `package mcpcontrol

import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"

func methodExpression() any { return contract.OrchestrationService.SubmitTurn }
`,
			},
			wantContains: []string{
				"method expression SubmitTurn in methodExpression uses full orchestration service",
			},
		},
	}
}

func collectOrchestrationServiceSSAUses(
	t *testing.T,
	pkg *orchestrationServiceCheckedPackage,
	target *types.TypeName,
) []orchestrationServiceSSAUse {
	t.Helper()
	if pkg == nil || pkg.types == nil || pkg.typesInfo == nil || target == nil {
		return nil
	}
	if !orchestrationServiceCheckedPackageMayCarryTarget(pkg, target) {
		return nil
	}

	ssaPkg := buildOrchestrationServiceSSAPackage(t, pkg)
	var uses []orchestrationServiceSSAUse
	uses = append(uses, collectOrchestrationServiceSSAMemberUses(pkg, ssaPkg, target)...)
	for _, fn := range orchestrationServiceSSAFunctions(ssaPkg) {
		uses = append(uses, collectOrchestrationServiceSSAFunctionUses(pkg, fn, target)...)
	}
	sortOrchestrationServiceSSAUses(uses)
	return dedupeOrchestrationServiceSSAUses(uses)
}

func orchestrationServiceCheckedPackageMayCarryTarget(pkg *orchestrationServiceCheckedPackage, target *types.TypeName) bool {
	return orchestrationServiceObjectMapMayCarryTarget(pkg.typesInfo.Defs, target) ||
		orchestrationServiceObjectMapMayCarryTarget(pkg.typesInfo.Uses, target) ||
		orchestrationServiceTypeValueMapMayCarryTarget(pkg.typesInfo.Types, target) ||
		orchestrationServiceSelectionMapMayCarryTarget(pkg.typesInfo.Selections, target)
}

func orchestrationServiceObjectMapMayCarryTarget(objects map[*ast.Ident]types.Object, target *types.TypeName) bool {
	for _, obj := range objects {
		if orchestrationServiceObjectUsesTarget(obj, target) {
			return true
		}
	}
	return false
}

func orchestrationServiceTypeValueMapMayCarryTarget(values map[ast.Expr]types.TypeAndValue, target *types.TypeName) bool {
	for _, typed := range values {
		if orchestrationServiceTypeUsesTarget(typed.Type, target) {
			return true
		}
	}
	return false
}

func orchestrationServiceSelectionMapMayCarryTarget(selections map[*ast.SelectorExpr]*types.Selection, target *types.TypeName) bool {
	for _, selection := range selections {
		if selection == nil {
			continue
		}
		if orchestrationServiceTypeUsesTarget(selection.Recv(), target) ||
			orchestrationServiceObjectUsesTarget(selection.Obj(), target) {
			return true
		}
	}
	return false
}

func buildOrchestrationServiceSSAPackage(t *testing.T, pkg *orchestrationServiceCheckedPackage) *ssa.Package {
	t.Helper()

	prog := ssa.NewProgram(pkg.fset, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
	for _, imported := range pkg.types.Imports() {
		prog.CreatePackage(imported, nil, nil, true)
	}
	ssaPkg := prog.CreatePackage(pkg.types, pkg.syntax, pkg.typesInfo, true)
	var buildErr error
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				buildErr = fmt.Errorf("build SSA for %s: %v", pkg.pkgPath, recovered)
			}
		}()
		ssaPkg.Build()
	}()
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return ssaPkg
}

func collectOrchestrationServiceSSAMemberUses(
	pkg *orchestrationServiceCheckedPackage,
	ssaPkg *ssa.Package,
	target *types.TypeName,
) []orchestrationServiceSSAUse {
	names := make([]string, 0, len(ssaPkg.Members))
	for name := range ssaPkg.Members {
		names = append(names, name)
	}
	sort.Strings(names)

	var uses []orchestrationServiceSSAUse
	for _, name := range names {
		switch member := ssaPkg.Members[name].(type) {
		case *ssa.Global:
			if orchestrationServiceTypeUsesTarget(member.Type(), target) {
				uses = append(uses, newOrchestrationServiceSSAUse(pkg, member.Pos(), nil, "global variable", member.Name(), member.Type().String()))
			}
		case *ssa.Type:
			uses = append(uses, collectOrchestrationServiceSSATypeUses(pkg, member, target)...)
		}
	}
	return uses
}

func collectOrchestrationServiceSSATypeUses(
	pkg *orchestrationServiceCheckedPackage,
	member *ssa.Type,
	target *types.TypeName,
) []orchestrationServiceSSAUse {
	obj, ok := member.Object().(*types.TypeName)
	if !ok {
		return nil
	}
	var uses []orchestrationServiceSSAUse
	if orchestrationServiceTypeUsesTarget(obj.Type(), target) {
		uses = append(uses, newOrchestrationServiceSSAUse(pkg, obj.Pos(), nil, "type declaration", obj.Name(), obj.Type().String()))
	}
	if named, ok := types.Unalias(obj.Type()).(*types.Named); ok {
		uses = append(uses, collectOrchestrationServiceSSATypeParameterUses(pkg, nil, named.TypeParams(), target)...)
		uses = append(uses, collectOrchestrationServiceSSANamedUnderlyingUses(pkg, obj.Name(), named.Underlying(), target)...)
	}
	return uses
}

func collectOrchestrationServiceSSANamedUnderlyingUses(
	pkg *orchestrationServiceCheckedPackage,
	typeName string,
	typ types.Type,
	target *types.TypeName,
) []orchestrationServiceSSAUse {
	switch underlying := typ.(type) {
	case *types.Struct:
		var uses []orchestrationServiceSSAUse
		for field := range underlying.Fields() {
			if orchestrationServiceTypeUsesTarget(field.Type(), target) {
				uses = append(uses, newOrchestrationServiceSSAUse(pkg, field.Pos(), nil, "field", field.Name(), typeName+"."+field.Name()))
			}
		}
		return uses
	case *types.Interface:
		var uses []orchestrationServiceSSAUse
		for embedded := range underlying.EmbeddedTypes() {
			if orchestrationServiceTypeUsesTarget(embedded, target) {
				uses = append(uses, newOrchestrationServiceSSAUse(pkg, token.NoPos, nil, "embedded interface", typeName, embedded.String()))
			}
		}
		for method := range underlying.ExplicitMethods() {
			if sig, ok := method.Type().(*types.Signature); ok && orchestrationServiceSSASignatureUsesTarget(sig, target) {
				uses = append(uses, newOrchestrationServiceSSAUse(pkg, method.Pos(), nil, "interface method", method.Name(), method.Type().String()))
			}
		}
		return uses
	default:
		return nil
	}
}

func collectOrchestrationServiceSSAFunctionUses(
	pkg *orchestrationServiceCheckedPackage,
	fn *ssa.Function,
	target *types.TypeName,
) []orchestrationServiceSSAUse {
	if fn == nil {
		return nil
	}
	var uses []orchestrationServiceSSAUse
	uses = append(uses, collectOrchestrationServiceSSASignatureUses(pkg, fn, target)...)
	for _, freeVar := range fn.FreeVars {
		if orchestrationServiceTypeUsesTarget(freeVar.Type(), target) {
			uses = append(uses, newOrchestrationServiceSSAUse(pkg, freeVar.Pos(), fn, "closure capture", freeVar.Name(), freeVar.Type().String()))
		}
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			uses = append(uses, collectOrchestrationServiceSSAInstructionUses(pkg, fn, instr, target)...)
		}
	}
	return uses
}

func collectOrchestrationServiceSSASignatureUses(
	pkg *orchestrationServiceCheckedPackage,
	fn *ssa.Function,
	target *types.TypeName,
) []orchestrationServiceSSAUse {
	if fn == nil || fn.Signature == nil {
		return nil
	}
	var uses []orchestrationServiceSSAUse
	if recv := fn.Signature.Recv(); recv != nil && orchestrationServiceTypeUsesTarget(recv.Type(), target) {
		uses = append(uses, newOrchestrationServiceSSAUse(pkg, recv.Pos(), fn, "receiver", recv.Name(), recv.Type().String()))
	}
	uses = append(uses, collectOrchestrationServiceSSATupleUses(pkg, fn, fn.Signature.Params(), "parameter", target)...)
	uses = append(uses, collectOrchestrationServiceSSATupleUses(pkg, fn, fn.Signature.Results(), "return value", target)...)
	uses = append(uses, collectOrchestrationServiceSSATypeParameterUses(pkg, fn, fn.Signature.TypeParams(), target)...)
	uses = append(uses, collectOrchestrationServiceSSATypeParameterUses(pkg, fn, fn.Signature.RecvTypeParams(), target)...)
	return uses
}

func collectOrchestrationServiceSSATupleUses(
	pkg *orchestrationServiceCheckedPackage,
	fn *ssa.Function,
	tuple *types.Tuple,
	kind string,
	target *types.TypeName,
) []orchestrationServiceSSAUse {
	if tuple == nil {
		return nil
	}
	var uses []orchestrationServiceSSAUse
	for variable := range tuple.Variables() {
		if orchestrationServiceTypeUsesTarget(variable.Type(), target) {
			uses = append(uses, newOrchestrationServiceSSAUse(pkg, variable.Pos(), fn, kind, orchestrationServiceSSAVarName(variable), variable.Type().String()))
		}
	}
	return uses
}

func collectOrchestrationServiceSSATypeParameterUses(
	pkg *orchestrationServiceCheckedPackage,
	fn *ssa.Function,
	list *types.TypeParamList,
	target *types.TypeName,
) []orchestrationServiceSSAUse {
	if list == nil {
		return nil
	}
	var uses []orchestrationServiceSSAUse
	for param := range list.TypeParams() {
		if orchestrationServiceTypeUsesTarget(param.Constraint(), target) {
			uses = append(uses, newOrchestrationServiceSSAUse(pkg, param.Obj().Pos(), fn, "generic constraint", param.Obj().Name(), param.Constraint().String()))
		}
	}
	return uses
}

func collectOrchestrationServiceSSAInstructionUses(
	pkg *orchestrationServiceCheckedPackage,
	fn *ssa.Function,
	instr ssa.Instruction,
	target *types.TypeName,
) []orchestrationServiceSSAUse {
	for _, collect := range []func(*orchestrationServiceCheckedPackage, *ssa.Function, ssa.Instruction, *types.TypeName) []orchestrationServiceSSAUse{
		collectOrchestrationServiceSSAAssertionUses,
		collectOrchestrationServiceSSAConversionUses,
		collectOrchestrationServiceSSAClosureOrReturnUses,
		collectOrchestrationServiceSSAStorageOrCallUses,
		collectOrchestrationServiceSSAFunctionValueInstructionUses,
	} {
		if uses := collect(pkg, fn, instr, target); len(uses) > 0 {
			return uses
		}
	}
	return nil
}

func collectOrchestrationServiceSSAAssertionUses(
	pkg *orchestrationServiceCheckedPackage,
	fn *ssa.Function,
	instr ssa.Instruction,
	target *types.TypeName,
) []orchestrationServiceSSAUse {
	typed, ok := instr.(*ssa.TypeAssert)
	if !ok || !orchestrationServiceTypeUsesTarget(typed.AssertedType, target) {
		return nil
	}
	return []orchestrationServiceSSAUse{newOrchestrationServiceSSAUse(pkg, typed.Pos(), fn, "interface assertion", "", typed.String())}
}

func collectOrchestrationServiceSSAConversionUses(
	pkg *orchestrationServiceCheckedPackage,
	fn *ssa.Function,
	instr ssa.Instruction,
	target *types.TypeName,
) []orchestrationServiceSSAUse {
	switch typed := instr.(type) {
	case *ssa.ChangeInterface:
		return orchestrationServiceSSAConversionUse(pkg, fn, typed.Pos(), typed.X, typed, target)
	case *ssa.ChangeType:
		return orchestrationServiceSSAConversionUse(pkg, fn, typed.Pos(), typed.X, typed, target)
	case *ssa.Convert:
		return orchestrationServiceSSAConversionUse(pkg, fn, typed.Pos(), typed.X, typed, target)
	case *ssa.MakeInterface:
		if methodName := orchestrationServiceSSAMethodExpressionValueName(typed.X); methodName != "" &&
			orchestrationServiceSSAFunctionTypeUsesTarget(typed.X.Type(), target) {
			return []orchestrationServiceSSAUse{newOrchestrationServiceSSAUse(pkg, typed.Pos(), fn, "method expression", methodName, typed.String())}
		}
		if orchestrationServiceTypeUsesTarget(typed.X.Type(), target) {
			return []orchestrationServiceSSAUse{newOrchestrationServiceSSAUse(pkg, typed.Pos(), fn, "type conversion", "", typed.String())}
		}
	}
	return nil
}

func orchestrationServiceSSAConversionUse(
	pkg *orchestrationServiceCheckedPackage,
	fn *ssa.Function,
	pos token.Pos,
	source ssa.Value,
	result ssa.Value,
	target *types.TypeName,
) []orchestrationServiceSSAUse {
	if !orchestrationServiceTypeUsesTarget(source.Type(), target) && !orchestrationServiceTypeUsesTarget(result.Type(), target) {
		return nil
	}
	return []orchestrationServiceSSAUse{newOrchestrationServiceSSAUse(pkg, pos, fn, "type conversion", "", result.String())}
}

func collectOrchestrationServiceSSAClosureOrReturnUses(
	pkg *orchestrationServiceCheckedPackage,
	fn *ssa.Function,
	instr ssa.Instruction,
	target *types.TypeName,
) []orchestrationServiceSSAUse {
	switch typed := instr.(type) {
	case *ssa.MakeClosure:
		return collectOrchestrationServiceSSAMakeClosureUses(pkg, fn, typed, target)
	case *ssa.Return:
		return collectOrchestrationServiceSSAReturnUses(pkg, fn, typed, target)
	default:
		return nil
	}
}

func collectOrchestrationServiceSSAStorageOrCallUses(
	pkg *orchestrationServiceCheckedPackage,
	fn *ssa.Function,
	instr ssa.Instruction,
	target *types.TypeName,
) []orchestrationServiceSSAUse {
	switch typed := instr.(type) {
	case *ssa.Store:
		if orchestrationServiceTypeUsesTarget(typed.Val.Type(), target) {
			return []orchestrationServiceSSAUse{newOrchestrationServiceSSAUse(pkg, typed.Pos(), fn, "storage propagation", "", typed.String())}
		}
	case *ssa.Call:
		if orchestrationServiceTypeUsesTarget(typed.Type(), target) {
			return []orchestrationServiceSSAUse{newOrchestrationServiceSSAUse(pkg, typed.Pos(), fn, "call result", "", typed.String())}
		}
	}
	return nil
}

func collectOrchestrationServiceSSAFunctionValueInstructionUses(
	pkg *orchestrationServiceCheckedPackage,
	fn *ssa.Function,
	instr ssa.Instruction,
	target *types.TypeName,
) []orchestrationServiceSSAUse {
	if value, ok := instr.(ssa.Value); ok && orchestrationServiceSSAFunctionTypeUsesTarget(value.Type(), target) {
		return []orchestrationServiceSSAUse{newOrchestrationServiceSSAUse(pkg, value.Pos(), fn, "function value propagation", "", value.String())}
	}
	return nil
}

func collectOrchestrationServiceSSAMakeClosureUses(
	pkg *orchestrationServiceCheckedPackage,
	fn *ssa.Function,
	closure *ssa.MakeClosure,
	target *types.TypeName,
) []orchestrationServiceSSAUse {
	var uses []orchestrationServiceSSAUse
	closureName := orchestrationServiceSSAClosureSymbol(closure)
	for _, binding := range closure.Bindings {
		if !orchestrationServiceTypeUsesTarget(binding.Type(), target) {
			continue
		}
		kind := "closure capture"
		if closureName != "" && strings.Contains(closure.String(), "$bound") {
			kind = "method value"
		}
		uses = append(uses, newOrchestrationServiceSSAUse(pkg, closure.Pos(), fn, kind, orchestrationServiceSSASymbolOrValueName(closureName, binding), closure.String()))
	}
	if orchestrationServiceSSAFunctionTypeUsesTarget(closure.Type(), target) {
		uses = append(uses, newOrchestrationServiceSSAUse(pkg, closure.Pos(), fn, "function value propagation", closureName, closure.String()))
	}
	return uses
}

func collectOrchestrationServiceSSAReturnUses(
	pkg *orchestrationServiceCheckedPackage,
	fn *ssa.Function,
	ret *ssa.Return,
	target *types.TypeName,
) []orchestrationServiceSSAUse {
	uses := make([]orchestrationServiceSSAUse, 0, len(ret.Results))
	for _, result := range ret.Results {
		if orchestrationServiceTypeUsesTarget(result.Type(), target) {
			uses = append(uses, newOrchestrationServiceSSAUse(pkg, ret.Pos(), fn, "return value", orchestrationServiceSSAValueName(result), result.String()))
			continue
		}
		if orchestrationServiceSSAFunctionTypeUsesTarget(result.Type(), target) {
			uses = append(uses, newOrchestrationServiceSSAUse(pkg, ret.Pos(), fn, "function value propagation", orchestrationServiceSSAValueName(result), result.String()))
		}
		if fnValue, ok := result.(*ssa.Function); ok && orchestrationServiceSSASignatureUsesTarget(fnValue.Signature, target) {
			uses = append(uses, newOrchestrationServiceSSAUse(pkg, ret.Pos(), fn, "method expression", fnValue.Name(), result.String()))
		}
	}
	return uses
}

func orchestrationServiceSSASignatureUsesTarget(sig *types.Signature, target *types.TypeName) bool {
	if sig == nil {
		return false
	}
	return orchestrationServiceVarUsesTarget(sig.Recv(), target) ||
		orchestrationServiceTupleUsesTarget(sig.Params(), target) ||
		orchestrationServiceTupleUsesTarget(sig.Results(), target) ||
		orchestrationServiceSSATypeParamListUsesTarget(sig.TypeParams(), target) ||
		orchestrationServiceSSATypeParamListUsesTarget(sig.RecvTypeParams(), target)
}

func orchestrationServiceSSAFunctionTypeUsesTarget(typ types.Type, target *types.TypeName) bool {
	sig, ok := types.Unalias(typ).(*types.Signature)
	return ok && orchestrationServiceSSASignatureUsesTarget(sig, target)
}

func orchestrationServiceVarUsesTarget(variable *types.Var, target *types.TypeName) bool {
	return variable != nil && orchestrationServiceTypeUsesTarget(variable.Type(), target)
}

func orchestrationServiceSSATypeParamListUsesTarget(list *types.TypeParamList, target *types.TypeName) bool {
	if list == nil {
		return false
	}
	for param := range list.TypeParams() {
		if orchestrationServiceTypeUsesTarget(param.Constraint(), target) {
			return true
		}
	}
	return false
}

func orchestrationServiceSSAFunctions(pkg *ssa.Package) []*ssa.Function {
	seen := map[*ssa.Function]bool{}
	var functions []*ssa.Function
	var collect func(fn *ssa.Function)
	collect = func(fn *ssa.Function) {
		if fn == nil || seen[fn] {
			return
		}
		seen[fn] = true
		functions = append(functions, fn)
		for _, nested := range fn.AnonFuncs {
			collect(nested)
		}
	}
	for _, member := range pkg.Members {
		if fn, ok := member.(*ssa.Function); ok {
			collect(fn)
		}
	}
	sort.Slice(functions, func(i, j int) bool {
		return orchestrationServiceSSAFunctionSortKey(functions[i]) < orchestrationServiceSSAFunctionSortKey(functions[j])
	})
	return functions
}

func orchestrationServiceSSAFunctionSortKey(fn *ssa.Function) string {
	if fn == nil {
		return ""
	}
	return fmt.Sprintf("%s\x00%09d\x00%s", fn.Pkg.Pkg.Path(), fn.Pos(), fn.String())
}

func newOrchestrationServiceSSAUse(
	pkg *orchestrationServiceCheckedPackage,
	pos token.Pos,
	fn *ssa.Function,
	kind string,
	symbol string,
	detail string,
) orchestrationServiceSSAUse {
	if !pos.IsValid() && fn != nil {
		pos = fn.Pos()
	}
	relPath, line := orchestrationServiceSSAPosition(pkg, pos)
	return orchestrationServiceSSAUse{
		relPath:  relPath,
		line:     line,
		function: orchestrationServiceSSATopFunctionName(fn),
		symbol:   symbol,
		kind:     kind,
		detail:   detail,
	}
}

func orchestrationServiceSSAPosition(pkg *orchestrationServiceCheckedPackage, pos token.Pos) (string, int) {
	if pkg != nil && pos.IsValid() {
		position := pkg.fset.Position(pos)
		if position.Filename != "" {
			return orchestrationServiceTypeRelPath(position.Filename), position.Line
		}
	}
	if pkg == nil || pkg.pkgPath == "" {
		return "(unknown)", 0
	}
	return strings.TrimPrefix(pkg.pkgPath, superAgentModulePath+"/"), 0
}

func orchestrationServiceSSATopFunctionName(fn *ssa.Function) string {
	if fn == nil {
		return ""
	}
	for fn.Parent() != nil {
		fn = fn.Parent()
	}
	return fn.Name()
}

func orchestrationServiceSSAClosureSymbol(closure *ssa.MakeClosure) string {
	fn, ok := closure.Fn.(*ssa.Function)
	if !ok || fn == nil {
		return ""
	}
	name := orchestrationServiceSSACleanFunctionValueName(fn.Name())
	if index := strings.LastIndex(name, "."); index >= 0 && index+1 < len(name) {
		return name[index+1:]
	}
	return name
}

func orchestrationServiceSSAMethodExpressionValueName(value ssa.Value) string {
	fn, ok := value.(*ssa.Function)
	if !ok || fn == nil || !strings.Contains(fn.Name(), "$thunk") {
		return ""
	}
	return orchestrationServiceSSACleanFunctionValueName(fn.Name())
}

func orchestrationServiceSSACleanFunctionValueName(name string) string {
	name = strings.TrimSuffix(name, "$bound")
	name = strings.TrimSuffix(name, "$thunk")
	if index := strings.LastIndex(name, "."); index >= 0 && index+1 < len(name) {
		return name[index+1:]
	}
	return name
}

func orchestrationServiceSSASymbolOrValueName(symbol string, value ssa.Value) string {
	if symbol != "" {
		return symbol
	}
	return orchestrationServiceSSAValueName(value)
}

func orchestrationServiceSSAValueName(value ssa.Value) string {
	if value == nil || value.Name() == "" {
		return "(anonymous)"
	}
	return value.Name()
}

func orchestrationServiceSSAVarName(variable *types.Var) string {
	if variable == nil || variable.Name() == "" {
		return "(anonymous)"
	}
	return variable.Name()
}

func orchestrationServiceSSAAllowances() []orchestrationServiceSSAAllowance {
	return nil
}

func isAllowedOrchestrationServiceSSAUse(use orchestrationServiceSSAUse, allowances []orchestrationServiceSSAAllowance) bool {
	for _, allowance := range allowances {
		if allowance.reason == "" {
			continue
		}
		if allowance.relPath == use.relPath &&
			allowance.function == use.function &&
			allowance.symbol == use.symbol &&
			allowance.kind == use.kind {
			return true
		}
	}
	return false
}

func sortOrchestrationServiceSSAUses(uses []orchestrationServiceSSAUse) {
	sort.Slice(uses, func(i, j int) bool {
		return orchestrationServiceSSAUseSortKey(uses[i]) < orchestrationServiceSSAUseSortKey(uses[j])
	})
}

func dedupeOrchestrationServiceSSAUses(uses []orchestrationServiceSSAUse) []orchestrationServiceSSAUse {
	deduped := uses[:0]
	var last string
	for _, use := range uses {
		key := orchestrationServiceSSAUseSortKey(use)
		if key == last {
			continue
		}
		deduped = append(deduped, use)
		last = key
	}
	return deduped
}

func orchestrationServiceSSAUseSortKey(use orchestrationServiceSSAUse) string {
	return fmt.Sprintf("%s\x00%09d\x00%s\x00%s\x00%s\x00%s", use.relPath, use.line, use.function, use.kind, use.symbol, use.detail)
}

func orchestrationServiceSSAUseMessages(uses []orchestrationServiceSSAUse) []string {
	messages := make([]string, 0, len(uses))
	for _, use := range uses {
		messages = append(messages, use.violationMessage())
	}
	return messages
}

func (use orchestrationServiceSSAUse) violationMessage() string {
	location := fmt.Sprintf("%s:%d", use.relPath, use.line)
	context := use.kind
	if use.symbol != "" {
		context += " " + use.symbol
	}
	if use.function != "" {
		context += " in " + use.function
	}
	message := fmt.Sprintf("%s %s uses full orchestration service", location, context)
	if use.detail != "" {
		message += " via " + use.detail
	}
	return message + "; split it behind a narrow contract port"
}
