// Package typednil provides a Go analyzer that rejects definitely nil concrete values converted to interfaces.
package typednil

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

// Analyzer reports definitely nil pointers, maps, slices, channels, or functions converted to interfaces.
var Analyzer = &analysis.Analyzer{
	Name:     "typednil",
	Doc:      "reject definitely nil concrete values converted to interfaces",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
}

// run 检查显式 typed-nil 和从未改写的确定 nil 变量是否流入接口边界。
func run(pass *analysis.Pass) (any, error) {
	files := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	definitelyNilVariables := collectDefinitelyNilVariables(pass, files)
	files.WithStack([]ast.Node{new(ast.CallExpr), new(ast.Ident)}, func(node ast.Node, push bool, stack []ast.Node) bool {
		if !push {
			return true
		}
		expression := node.(ast.Expr)
		if isDefinitelyNilValue(pass, expression, definitelyNilVariables) && flowsToInterface(pass, expression, stack) {
			pass.Reportf(expression.Pos(), "typed nil %s converted to interface", pass.TypesInfo.TypeOf(expression))
		}
		return true
	})
	return nil, nil
}

// collectDefinitelyNilVariables 仅保留以 nil 初始化且在当前包语法中从未被重新赋值的变量。
func collectDefinitelyNilVariables(pass *analysis.Pass, files *inspector.Inspector) map[types.Object]bool {
	candidates := map[types.Object]bool{}
	mutated := map[types.Object]bool{}
	files.Preorder([]ast.Node{new(ast.ValueSpec), new(ast.AssignStmt)}, func(node ast.Node) {
		switch typed := node.(type) {
		case *ast.ValueSpec:
			collectValueSpecNilCandidates(pass, typed, candidates)
		case *ast.AssignStmt:
			collectAssignmentNilCandidates(pass, typed, candidates, mutated)
		}
	})
	for object := range mutated {
		delete(candidates, object)
	}
	for object := range candidates {
		if countObjectUses(pass, object) != 1 {
			delete(candidates, object)
		}
	}
	return candidates
}

// countObjectUses 保守要求候选变量只有接口流向这一处使用，避免遗漏经指针参数产生的间接改写。
func countObjectUses(pass *analysis.Pass, target types.Object) int {
	uses := 0
	for _, object := range pass.TypesInfo.Uses {
		if object == target {
			uses++
		}
	}
	return uses
}

// collectValueSpecNilCandidates 收集无初始化或以确定 nil 初始化的变量声明。
func collectValueSpecNilCandidates(pass *analysis.Pass, spec *ast.ValueSpec, candidates map[types.Object]bool) {
	for index, name := range spec.Names {
		object := pass.TypesInfo.Defs[name]
		if object == nil || !isNilableConcrete(object.Type()) {
			continue
		}
		if len(spec.Values) == 0 || index < len(spec.Values) && isNilSourceExpression(pass, spec.Values[index]) {
			candidates[object] = true
		}
	}
}

// collectAssignmentNilCandidates 收集短声明 nil 候选并标记后续普通赋值。
func collectAssignmentNilCandidates(pass *analysis.Pass, assignment *ast.AssignStmt, candidates, mutated map[types.Object]bool) {
	for index, target := range assignment.Lhs {
		identifier, ok := target.(*ast.Ident)
		if !ok || identifier.Name == "_" {
			continue
		}
		if assignment.Tok == token.DEFINE {
			object := pass.TypesInfo.Defs[identifier]
			if object != nil && isNilableConcrete(object.Type()) && index < len(assignment.Rhs) && isNilSourceExpression(pass, assignment.Rhs[index]) {
				candidates[object] = true
			}
			continue
		}
		if object := pass.TypesInfo.Uses[identifier]; object != nil {
			mutated[object] = true
		}
	}
}

func isDefinitelyNilValue(pass *analysis.Pass, expression ast.Expr, variables map[types.Object]bool) bool {
	if call, ok := expression.(*ast.CallExpr); ok {
		return isTypedNilConversion(pass, call)
	}
	identifier, ok := expression.(*ast.Ident)
	return ok && variables[pass.TypesInfo.Uses[identifier]]
}

func isNilSourceExpression(pass *analysis.Pass, expression ast.Expr) bool {
	if identifier, ok := expression.(*ast.Ident); ok {
		return identifier.Name == "nil"
	}
	call, ok := expression.(*ast.CallExpr)
	return ok && isTypedNilConversion(pass, call)
}

func isTypedNilConversion(pass *analysis.Pass, call *ast.CallExpr) bool {
	if len(call.Args) != 1 || !isNilSourceExpression(pass, call.Args[0]) {
		return false
	}
	target := pass.TypesInfo.TypeOf(call.Fun)
	return target != nil && types.Identical(target, pass.TypesInfo.TypeOf(call)) && isNilableConcrete(target)
}

func isNilableConcrete(valueType types.Type) bool {
	if valueType == nil {
		return false
	}
	if _, ok := valueType.Underlying().(*types.Interface); ok {
		return false
	}
	switch valueType.Underlying().(type) {
	case *types.Pointer, *types.Map, *types.Slice, *types.Chan, *types.Signature:
		return true
	default:
		return false
	}
}

// flowsToInterface 判断表达式的直接语法消费位置是否要求接口类型。
func flowsToInterface(pass *analysis.Pass, expression ast.Expr, stack []ast.Node) bool {
	if len(stack) < 2 {
		return false
	}
	parent := stack[len(stack)-2]
	switch typed := parent.(type) {
	case *ast.ReturnStmt:
		return returnTargetIsInterface(pass, expression, typed, stack)
	case *ast.AssignStmt:
		return indexedTargetIsInterface(pass, expression, typed.Rhs, typed.Lhs)
	case *ast.ValueSpec:
		return indexedTargetIsInterface(pass, expression, typed.Values, identifiersAsExpressions(typed.Names))
	case *ast.CallExpr:
		return callTargetIsInterface(pass, expression, typed)
	case *ast.CompositeLit:
		return compositeElementIsInterface(pass, expression, typed)
	case *ast.KeyValueExpr:
		return keyedCompositeElementIsInterface(pass, expression, typed, stack)
	default:
		return false
	}
}

func returnTargetIsInterface(pass *analysis.Pass, expression ast.Expr, statement *ast.ReturnStmt, stack []ast.Node) bool {
	index := expressionIndex(expression, statement.Results)
	if index < 0 {
		return false
	}
	signature := enclosingSignature(pass, stack)
	return signature != nil && index < signature.Results().Len() && isInterface(signature.Results().At(index).Type())
}

func enclosingSignature(pass *analysis.Pass, stack []ast.Node) *types.Signature {
	for index := len(stack) - 1; index >= 0; index-- {
		switch typed := stack[index].(type) {
		case *ast.FuncDecl:
			signature, _ := pass.TypesInfo.TypeOf(typed.Name).(*types.Signature)
			return signature
		case *ast.FuncLit:
			signature, _ := pass.TypesInfo.TypeOf(typed.Type).(*types.Signature)
			return signature
		}
	}
	return nil
}

func indexedTargetIsInterface(pass *analysis.Pass, expression ast.Expr, sources, targets []ast.Expr) bool {
	index := expressionIndex(expression, sources)
	if index < 0 || index >= len(targets) || isBlankIdentifier(targets[index]) {
		return false
	}
	return isInterface(pass.TypesInfo.TypeOf(targets[index]))
}

// callTargetIsInterface 同时处理固定参数和可变接口参数。
func callTargetIsInterface(pass *analysis.Pass, expression ast.Expr, call *ast.CallExpr) bool {
	index := expressionIndex(expression, call.Args)
	if index < 0 {
		return false
	}
	if isInterface(pass.TypesInfo.TypeOf(call.Fun)) {
		return true
	}
	signature, _ := pass.TypesInfo.TypeOf(call.Fun).Underlying().(*types.Signature)
	if signature == nil || signature.Params().Len() == 0 {
		return false
	}
	if signature.Variadic() && index >= signature.Params().Len()-1 {
		slice, _ := signature.Params().At(signature.Params().Len() - 1).Type().Underlying().(*types.Slice)
		return slice != nil && isInterface(slice.Elem())
	}
	return index < signature.Params().Len() && isInterface(signature.Params().At(index).Type())
}

// compositeElementIsInterface 解析数组、切片和位置结构体字面量的目标元素类型。
func compositeElementIsInterface(pass *analysis.Pass, expression ast.Expr, literal *ast.CompositeLit) bool {
	index := expressionIndex(expression, literal.Elts)
	if index < 0 {
		return false
	}
	switch underlying := pass.TypesInfo.TypeOf(literal).Underlying().(type) {
	case *types.Array:
		return isInterface(underlying.Elem())
	case *types.Slice:
		return isInterface(underlying.Elem())
	case *types.Struct:
		return index < underlying.NumFields() && isInterface(underlying.Field(index).Type())
	default:
		return false
	}
}

// keyedCompositeElementIsInterface 解析 map 和具名结构体字面量的键值目标类型。
func keyedCompositeElementIsInterface(pass *analysis.Pass, expression ast.Expr, element *ast.KeyValueExpr, stack []ast.Node) bool {
	if len(stack) < 3 {
		return false
	}
	literal, ok := stack[len(stack)-3].(*ast.CompositeLit)
	if !ok {
		return false
	}
	switch underlying := pass.TypesInfo.TypeOf(literal).Underlying().(type) {
	case *types.Map:
		return mapElementIsInterface(expression, element, underlying)
	case *types.Struct:
		return structElementIsInterface(expression, element, underlying)
	}
	return false
}

func mapElementIsInterface(expression ast.Expr, element *ast.KeyValueExpr, valueType *types.Map) bool {
	if expression == element.Key {
		return isInterface(valueType.Key())
	}
	return expression == element.Value && isInterface(valueType.Elem())
}

func structElementIsInterface(expression ast.Expr, element *ast.KeyValueExpr, valueType *types.Struct) bool {
	name, ok := element.Key.(*ast.Ident)
	if !ok || expression != element.Value {
		return false
	}
	for index := 0; index < valueType.NumFields(); index++ {
		if valueType.Field(index).Name() == name.Name {
			return isInterface(valueType.Field(index).Type())
		}
	}
	return false
}

func isBlankIdentifier(expression ast.Expr) bool {
	identifier, ok := expression.(*ast.Ident)
	return ok && identifier.Name == "_"
}

func identifiersAsExpressions(identifiers []*ast.Ident) []ast.Expr {
	expressions := make([]ast.Expr, 0, len(identifiers))
	for _, identifier := range identifiers {
		expressions = append(expressions, identifier)
	}
	return expressions
}

func expressionIndex(target ast.Expr, expressions []ast.Expr) int {
	for index, expression := range expressions {
		if expression == target {
			return index
		}
	}
	return -1
}

func isInterface(valueType types.Type) bool {
	if valueType == nil {
		return false
	}
	_, ok := valueType.Underlying().(*types.Interface)
	return ok
}
