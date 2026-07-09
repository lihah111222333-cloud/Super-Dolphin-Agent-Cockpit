package archtest

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/ssa"
)

type prioritySSAWideUse struct {
	relPath  string
	line     int
	function string
	symbol   string
	kind     string
	detail   string
}

// prioritySSAPackageMayCarryTarget 先用类型信息快速判断包内是否可能传播目标宽端口。
func prioritySSAPackageMayCarryTarget(pkg *prioritySSAPackage, target *types.TypeName) bool {
	if pkg == nil || pkg.typesInfo == nil || target == nil {
		return false
	}
	return prioritySSAObjectMapMayCarryTarget(pkg.typesInfo.Defs, target) ||
		prioritySSAObjectMapMayCarryTarget(pkg.typesInfo.Uses, target) ||
		prioritySSATypeValueMapMayCarryTarget(pkg.typesInfo.Types, target) ||
		prioritySSASelectionMapMayCarryTarget(pkg.typesInfo.Selections, target)
}

func collectPrioritySSAWidePortViolations(
	pkg *prioritySSAPackage,
	ssaPkg *ssa.Package,
	target *types.TypeName,
) []PrioritySSAViolation {
	uses := collectPrioritySSAWidePortUses(pkg, ssaPkg, target)
	label := prioritySSATargetLabel(target)
	violations := make([]PrioritySSAViolation, 0, len(uses))
	for _, use := range uses {
		violations = append(violations, PrioritySSAViolation{
			Rule:   PrioritySSAWidePortRule,
			File:   use.relPath,
			Line:   use.line,
			Detail: prioritySSAWideUseDetail(use, "uses broad port "+label),
		})
	}
	return violations
}

func collectPrioritySSAWidePortUses(
	pkg *prioritySSAPackage,
	ssaPkg *ssa.Package,
	target *types.TypeName,
) []prioritySSAWideUse {
	if pkg == nil || ssaPkg == nil || target == nil {
		return nil
	}
	var uses []prioritySSAWideUse
	uses = append(uses, collectPrioritySSAWideMemberUses(pkg, ssaPkg, target)...)
	for _, fn := range prioritySSAFunctions(ssaPkg) {
		uses = append(uses, collectPrioritySSAWideFunctionUses(pkg, fn, target)...)
	}
	sortPrioritySSAWideUses(uses)
	return dedupePrioritySSAWideUses(uses)
}

// collectPrioritySSAWideMemberUses 扫描包级 SSA 成员中的宽端口类型传播。
func collectPrioritySSAWideMemberUses(
	pkg *prioritySSAPackage,
	ssaPkg *ssa.Package,
	target *types.TypeName,
) []prioritySSAWideUse {
	names := make([]string, 0, len(ssaPkg.Members))
	for name := range ssaPkg.Members {
		names = append(names, name)
	}
	sort.Strings(names)

	var uses []prioritySSAWideUse
	for _, name := range names {
		switch member := ssaPkg.Members[name].(type) {
		case *ssa.Global:
			if prioritySSATypeUsesTarget(member.Type(), target) {
				uses = append(uses, newPrioritySSAWideUse(pkg, member.Pos(), nil, "global variable", member.Name(), member.Type().String()))
			}
		case *ssa.Type:
			uses = append(uses, collectPrioritySSAWideTypeUses(pkg, member, target)...)
		}
	}
	return uses
}

func collectPrioritySSAWideTypeUses(
	pkg *prioritySSAPackage,
	member *ssa.Type,
	target *types.TypeName,
) []prioritySSAWideUse {
	obj, ok := member.Object().(*types.TypeName)
	if !ok {
		return nil
	}
	var uses []prioritySSAWideUse
	if prioritySSATypeUsesTarget(obj.Type(), target) {
		uses = append(uses, newPrioritySSAWideUse(pkg, obj.Pos(), nil, "type declaration", obj.Name(), obj.Type().String()))
	}
	if named, ok := types.Unalias(obj.Type()).(*types.Named); ok {
		uses = append(uses, collectPrioritySSAWideTypeParamUses(pkg, nil, named.TypeParams(), target)...)
		uses = append(uses, collectPrioritySSAWideUnderlyingUses(pkg, obj.Name(), named.Underlying(), target)...)
	}
	return uses
}

func collectPrioritySSAWideUnderlyingUses(
	pkg *prioritySSAPackage,
	typeName string,
	typ types.Type,
	target *types.TypeName,
) []prioritySSAWideUse {
	switch underlying := typ.(type) {
	case *types.Struct:
		return collectPrioritySSAWideStructUses(pkg, typeName, underlying, target)
	case *types.Interface:
		return collectPrioritySSAWideInterfaceMethodUses(pkg, typeName, underlying, target)
	default:
		return nil
	}
}

func collectPrioritySSAWideStructUses(
	pkg *prioritySSAPackage,
	typeName string,
	strct *types.Struct,
	target *types.TypeName,
) []prioritySSAWideUse {
	var uses []prioritySSAWideUse
	for field := range strct.Fields() {
		if prioritySSATypeUsesTarget(field.Type(), target) {
			uses = append(uses, newPrioritySSAWideUse(pkg, field.Pos(), nil, "field", field.Name(), typeName+"."+field.Name()))
		}
	}
	return uses
}

// collectPrioritySSAWideInterfaceMethodUses 扫描接口嵌入和方法签名中的宽端口使用。
func collectPrioritySSAWideInterfaceMethodUses(
	pkg *prioritySSAPackage,
	typeName string,
	iface *types.Interface,
	target *types.TypeName,
) []prioritySSAWideUse {
	var uses []prioritySSAWideUse
	for embedded := range iface.EmbeddedTypes() {
		if prioritySSATypeUsesTarget(embedded, target) {
			uses = append(uses, newPrioritySSAWideUse(pkg, token.NoPos, nil, "embedded interface", typeName, embedded.String()))
		}
	}
	for method := range iface.ExplicitMethods() {
		sig, ok := method.Type().(*types.Signature)
		if ok && prioritySSASignatureUsesTarget(sig, target) {
			uses = append(uses, newPrioritySSAWideUse(pkg, method.Pos(), nil, "interface method", method.Name(), method.Type().String()))
		}
	}
	return uses
}

// collectPrioritySSAWideFunctionUses 扫描函数签名、闭包捕获和指令中的宽端口传播。
func collectPrioritySSAWideFunctionUses(
	pkg *prioritySSAPackage,
	fn *ssa.Function,
	target *types.TypeName,
) []prioritySSAWideUse {
	if fn == nil {
		return nil
	}
	var uses []prioritySSAWideUse
	uses = append(uses, collectPrioritySSAWideSignatureUses(pkg, fn, target)...)
	for _, freeVar := range fn.FreeVars {
		if prioritySSATypeUsesTarget(freeVar.Type(), target) {
			uses = append(uses, newPrioritySSAWideUse(pkg, freeVar.Pos(), fn, "closure capture", freeVar.Name(), freeVar.Type().String()))
		}
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			uses = append(uses, collectPrioritySSAWideInstructionUses(pkg, fn, instr, target)...)
		}
	}
	return uses
}

func collectPrioritySSAWideSignatureUses(
	pkg *prioritySSAPackage,
	fn *ssa.Function,
	target *types.TypeName,
) []prioritySSAWideUse {
	if fn == nil || fn.Signature == nil {
		return nil
	}
	var uses []prioritySSAWideUse
	if recv := fn.Signature.Recv(); prioritySSAVarUsesTarget(recv, target) {
		uses = append(uses, newPrioritySSAWideUse(pkg, recv.Pos(), fn, "receiver", recv.Name(), recv.Type().String()))
	}
	uses = append(uses, collectPrioritySSAWideTupleUses(pkg, fn, fn.Signature.Params(), "parameter", target)...)
	uses = append(uses, collectPrioritySSAWideTupleUses(pkg, fn, fn.Signature.Results(), "return value", target)...)
	uses = append(uses, collectPrioritySSAWideTypeParamUses(pkg, fn, fn.Signature.TypeParams(), target)...)
	uses = append(uses, collectPrioritySSAWideTypeParamUses(pkg, fn, fn.Signature.RecvTypeParams(), target)...)
	return uses
}

func collectPrioritySSAWideTupleUses(
	pkg *prioritySSAPackage,
	fn *ssa.Function,
	tuple *types.Tuple,
	kind string,
	target *types.TypeName,
) []prioritySSAWideUse {
	if tuple == nil {
		return nil
	}
	var uses []prioritySSAWideUse
	for variable := range tuple.Variables() {
		if prioritySSATypeUsesTarget(variable.Type(), target) {
			uses = append(uses, newPrioritySSAWideUse(pkg, variable.Pos(), fn, kind, prioritySSAVarName(variable), variable.Type().String()))
		}
	}
	return uses
}

func collectPrioritySSAWideTypeParamUses(
	pkg *prioritySSAPackage,
	fn *ssa.Function,
	list *types.TypeParamList,
	target *types.TypeName,
) []prioritySSAWideUse {
	if list == nil {
		return nil
	}
	var uses []prioritySSAWideUse
	for param := range list.TypeParams() {
		if prioritySSATypeUsesTarget(param.Constraint(), target) {
			uses = append(uses, newPrioritySSAWideUse(pkg, param.Obj().Pos(), fn, "generic constraint", param.Obj().Name(), param.Constraint().String()))
		}
	}
	return uses
}

func collectPrioritySSAWideInstructionUses(
	pkg *prioritySSAPackage,
	fn *ssa.Function,
	instr ssa.Instruction,
	target *types.TypeName,
) []prioritySSAWideUse {
	for _, collect := range []func(*prioritySSAPackage, *ssa.Function, ssa.Instruction, *types.TypeName) []prioritySSAWideUse{
		collectPrioritySSAWideAssertionUses,
		collectPrioritySSAWideConversionUses,
		collectPrioritySSAWideClosureOrReturnUses,
		collectPrioritySSAWideStorageOrCallUses,
		collectPrioritySSAWideFunctionValueInstructionUses,
	} {
		if uses := collect(pkg, fn, instr, target); len(uses) > 0 {
			return uses
		}
	}
	return nil
}

func collectPrioritySSAWideAssertionUses(
	pkg *prioritySSAPackage,
	fn *ssa.Function,
	instr ssa.Instruction,
	target *types.TypeName,
) []prioritySSAWideUse {
	typed, ok := instr.(*ssa.TypeAssert)
	if !ok || !prioritySSATypeUsesTarget(typed.AssertedType, target) {
		return nil
	}
	return []prioritySSAWideUse{newPrioritySSAWideUse(pkg, typed.Pos(), fn, "interface assertion", "", typed.String())}
}

// collectPrioritySSAWideConversionUses 捕获类型转换和接口装箱中的宽端口传播。
func collectPrioritySSAWideConversionUses(
	pkg *prioritySSAPackage,
	fn *ssa.Function,
	instr ssa.Instruction,
	target *types.TypeName,
) []prioritySSAWideUse {
	switch typed := instr.(type) {
	case *ssa.ChangeInterface:
		return prioritySSAWideConversionUse(pkg, fn, typed.Pos(), typed.X, typed, target)
	case *ssa.ChangeType:
		return prioritySSAWideConversionUse(pkg, fn, typed.Pos(), typed.X, typed, target)
	case *ssa.Convert:
		return prioritySSAWideConversionUse(pkg, fn, typed.Pos(), typed.X, typed, target)
	case *ssa.MakeInterface:
		if methodName := prioritySSAMethodExpressionValueName(typed.X); methodName != "" &&
			prioritySSAFunctionTypeUsesTarget(typed.X.Type(), target) {
			return []prioritySSAWideUse{newPrioritySSAWideUse(pkg, typed.Pos(), fn, "method expression", methodName, typed.String())}
		}
		if prioritySSATypeUsesTarget(typed.X.Type(), target) {
			return []prioritySSAWideUse{newPrioritySSAWideUse(pkg, typed.Pos(), fn, "type conversion", "", typed.String())}
		}
	}
	return nil
}

func prioritySSAWideConversionUse(
	pkg *prioritySSAPackage,
	fn *ssa.Function,
	pos token.Pos,
	source ssa.Value,
	result ssa.Value,
	target *types.TypeName,
) []prioritySSAWideUse {
	if !prioritySSATypeUsesTarget(source.Type(), target) && !prioritySSATypeUsesTarget(result.Type(), target) {
		return nil
	}
	return []prioritySSAWideUse{newPrioritySSAWideUse(pkg, pos, fn, "type conversion", "", result.String())}
}

func collectPrioritySSAWideClosureOrReturnUses(
	pkg *prioritySSAPackage,
	fn *ssa.Function,
	instr ssa.Instruction,
	target *types.TypeName,
) []prioritySSAWideUse {
	switch typed := instr.(type) {
	case *ssa.MakeClosure:
		return collectPrioritySSAWideMakeClosureUses(pkg, fn, typed, target)
	case *ssa.Return:
		return collectPrioritySSAWideReturnUses(pkg, fn, typed, target)
	default:
		return nil
	}
}

func collectPrioritySSAWideStorageOrCallUses(
	pkg *prioritySSAPackage,
	fn *ssa.Function,
	instr ssa.Instruction,
	target *types.TypeName,
) []prioritySSAWideUse {
	switch typed := instr.(type) {
	case *ssa.Store:
		if prioritySSATypeUsesTarget(typed.Val.Type(), target) {
			return []prioritySSAWideUse{newPrioritySSAWideUse(pkg, typed.Pos(), fn, "storage propagation", "", typed.String())}
		}
	case *ssa.Call:
		if prioritySSATypeUsesTarget(typed.Type(), target) {
			return []prioritySSAWideUse{newPrioritySSAWideUse(pkg, typed.Pos(), fn, "call result", "", typed.String())}
		}
	}
	return nil
}

func collectPrioritySSAWideFunctionValueInstructionUses(
	pkg *prioritySSAPackage,
	fn *ssa.Function,
	instr ssa.Instruction,
	target *types.TypeName,
) []prioritySSAWideUse {
	value, ok := instr.(ssa.Value)
	if ok && prioritySSAFunctionTypeUsesTarget(value.Type(), target) {
		return []prioritySSAWideUse{newPrioritySSAWideUse(pkg, value.Pos(), fn, "function value propagation", "", value.String())}
	}
	return nil
}

// collectPrioritySSAWideMakeClosureUses 捕获闭包绑定和方法值携带的宽端口。
func collectPrioritySSAWideMakeClosureUses(
	pkg *prioritySSAPackage,
	fn *ssa.Function,
	closure *ssa.MakeClosure,
	target *types.TypeName,
) []prioritySSAWideUse {
	var uses []prioritySSAWideUse
	closureName := prioritySSAClosureSymbol(closure)
	for _, binding := range closure.Bindings {
		if !prioritySSATypeUsesTarget(binding.Type(), target) {
			continue
		}
		kind := "closure capture"
		if closureName != "" && strings.Contains(closure.String(), "$bound") {
			kind = "method value"
		}
		symbol := prioritySSASymbolOrValueName(closureName, binding)
		uses = append(uses, newPrioritySSAWideUse(pkg, closure.Pos(), fn, kind, symbol, closure.String()))
	}
	if prioritySSAFunctionTypeUsesTarget(closure.Type(), target) {
		uses = append(uses, newPrioritySSAWideUse(pkg, closure.Pos(), fn, "function value propagation", closureName, closure.String()))
	}
	return uses
}

// collectPrioritySSAWideReturnUses 捕获 return 指令直接或函数值间接传播的宽端口。
func collectPrioritySSAWideReturnUses(
	pkg *prioritySSAPackage,
	fn *ssa.Function,
	ret *ssa.Return,
	target *types.TypeName,
) []prioritySSAWideUse {
	uses := make([]prioritySSAWideUse, 0, len(ret.Results))
	for _, result := range ret.Results {
		if prioritySSATypeUsesTarget(result.Type(), target) {
			uses = append(uses, newPrioritySSAWideUse(pkg, ret.Pos(), fn, "return value", prioritySSAValueName(result), result.String()))
			continue
		}
		if prioritySSAFunctionTypeUsesTarget(result.Type(), target) {
			uses = append(uses, newPrioritySSAWideUse(pkg, ret.Pos(), fn, "function value propagation", prioritySSAValueName(result), result.String()))
		}
		if fnValue, ok := result.(*ssa.Function); ok && prioritySSASignatureUsesTarget(fnValue.Signature, target) {
			uses = append(uses, newPrioritySSAWideUse(pkg, ret.Pos(), fn, "method expression", fnValue.Name(), result.String()))
		}
	}
	return uses
}

func prioritySSAObjectMapMayCarryTarget(objects map[*ast.Ident]types.Object, target *types.TypeName) bool {
	for _, obj := range objects {
		if prioritySSAObjectUsesTarget(obj, target) {
			return true
		}
	}
	return false
}

func prioritySSATypeValueMapMayCarryTarget(values map[ast.Expr]types.TypeAndValue, target *types.TypeName) bool {
	for _, typed := range values {
		if prioritySSATypeUsesTarget(typed.Type, target) {
			return true
		}
	}
	return false
}

func prioritySSASelectionMapMayCarryTarget(selections map[*ast.SelectorExpr]*types.Selection, target *types.TypeName) bool {
	for _, selection := range selections {
		if selection == nil {
			continue
		}
		if prioritySSATypeUsesTarget(selection.Recv(), target) ||
			prioritySSAObjectUsesTarget(selection.Obj(), target) {
			return true
		}
	}
	return false
}

func prioritySSAObjectUsesTarget(obj types.Object, target *types.TypeName) bool {
	switch typed := obj.(type) {
	case *types.TypeName:
		return prioritySSATypeUsesTarget(typed.Type(), target)
	case *types.Var:
		return prioritySSATypeUsesTarget(typed.Type(), target)
	default:
		return false
	}
}

// prioritySSATypeUsesTarget 递归判断任意 Go 类型是否包含目标宽端口类型。
func prioritySSATypeUsesTarget(typ types.Type, target *types.TypeName) bool {
	if typ == nil || target == nil {
		return false
	}
	unaliased := types.Unalias(typ)
	switch typed := unaliased.(type) {
	case *types.Named:
		return typed.Obj() == target
	case *types.TypeParam:
		return prioritySSATypeUsesTarget(typed.Constraint(), target)
	case *types.Interface:
		return prioritySSAInterfaceUsesTarget(typed, target)
	case *types.Signature:
		return prioritySSASignatureUsesTarget(typed, target)
	case *types.Tuple:
		return prioritySSATupleUsesTarget(typed, target)
	default:
		return prioritySSAContainerTypeUsesTarget(unaliased, target)
	}
}

// prioritySSAInterfaceUsesTarget 检查接口嵌入和显式方法是否携带目标宽端口。
func prioritySSAInterfaceUsesTarget(iface *types.Interface, target *types.TypeName) bool {
	for embedded := range iface.EmbeddedTypes() {
		if prioritySSATypeUsesTarget(embedded, target) {
			return true
		}
	}
	for method := range iface.ExplicitMethods() {
		if sig, ok := method.Type().(*types.Signature); ok && prioritySSASignatureUsesTarget(sig, target) {
			return true
		}
	}
	return false
}

// prioritySSAContainerTypeUsesTarget 检查指针、切片、map、chan 等容器元素类型。
func prioritySSAContainerTypeUsesTarget(typ types.Type, target *types.TypeName) bool {
	switch typed := typ.(type) {
	case *types.Pointer:
		return prioritySSATypeUsesTarget(typed.Elem(), target)
	case *types.Slice:
		return prioritySSATypeUsesTarget(typed.Elem(), target)
	case *types.Array:
		return prioritySSATypeUsesTarget(typed.Elem(), target)
	case *types.Map:
		return prioritySSATypeUsesTarget(typed.Key(), target) ||
			prioritySSATypeUsesTarget(typed.Elem(), target)
	case *types.Chan:
		return prioritySSATypeUsesTarget(typed.Elem(), target)
	default:
		return false
	}
}

func prioritySSASignatureUsesTarget(sig *types.Signature, target *types.TypeName) bool {
	if sig == nil {
		return false
	}
	return prioritySSATupleUsesTarget(sig.Params(), target) ||
		prioritySSATupleUsesTarget(sig.Results(), target) ||
		prioritySSATypeParamListUsesTarget(sig.TypeParams(), target) ||
		prioritySSATypeParamListUsesTarget(sig.RecvTypeParams(), target)
}

func prioritySSAFunctionTypeUsesTarget(typ types.Type, target *types.TypeName) bool {
	sig, ok := types.Unalias(typ).(*types.Signature)
	return ok && prioritySSASignatureUsesTarget(sig, target)
}

func prioritySSAVarUsesTarget(variable *types.Var, target *types.TypeName) bool {
	return variable != nil && prioritySSATypeUsesTarget(variable.Type(), target)
}

func prioritySSATupleUsesTarget(tuple *types.Tuple, target *types.TypeName) bool {
	if tuple == nil {
		return false
	}
	for variable := range tuple.Variables() {
		if prioritySSATypeUsesTarget(variable.Type(), target) {
			return true
		}
	}
	return false
}

func prioritySSATypeParamListUsesTarget(list *types.TypeParamList, target *types.TypeName) bool {
	if list == nil {
		return false
	}
	for param := range list.TypeParams() {
		if prioritySSATypeUsesTarget(param.Constraint(), target) {
			return true
		}
	}
	return false
}

func newPrioritySSAWideUse(
	pkg *prioritySSAPackage,
	pos token.Pos,
	fn *ssa.Function,
	kind string,
	symbol string,
	detail string,
) prioritySSAWideUse {
	if !pos.IsValid() && fn != nil {
		pos = fn.Pos()
	}
	relPath, line := prioritySSAPosition(pkg, pos)
	return prioritySSAWideUse{
		relPath:  relPath,
		line:     line,
		function: prioritySSATopFunctionName(fn),
		symbol:   symbol,
		kind:     kind,
		detail:   detail,
	}
}

func prioritySSATopFunctionName(fn *ssa.Function) string {
	if fn == nil {
		return ""
	}
	for fn.Parent() != nil {
		fn = fn.Parent()
	}
	return fn.Name()
}

func prioritySSAWideUseDetail(use prioritySSAWideUse, suffix string) string {
	context := use.kind
	if use.symbol != "" {
		context += " " + use.symbol
	}
	if use.function != "" {
		context += " in " + use.function
	}
	return context + " " + suffix
}

func prioritySSATargetLabel(target *types.TypeName) string {
	if target == nil {
		return "(unknown)"
	}
	if target.Pkg() == nil {
		return target.Name()
	}
	return target.Pkg().Name() + "." + target.Name()
}

func sortPrioritySSAWideUses(uses []prioritySSAWideUse) {
	sort.Slice(uses, func(i, j int) bool {
		return prioritySSAWideUseSortKey(uses[i]) < prioritySSAWideUseSortKey(uses[j])
	})
}

func dedupePrioritySSAWideUses(uses []prioritySSAWideUse) []prioritySSAWideUse {
	deduped := uses[:0]
	var last string
	for _, use := range uses {
		key := prioritySSAWideUseSortKey(use)
		if key == last {
			continue
		}
		deduped = append(deduped, use)
		last = key
	}
	return deduped
}

func prioritySSAWideUseSortKey(use prioritySSAWideUse) string {
	return fmt.Sprintf("%s\x00%09d\x00%s\x00%s\x00%s\x00%s",
		use.relPath, use.line, use.function, use.kind, use.symbol, use.detail)
}
