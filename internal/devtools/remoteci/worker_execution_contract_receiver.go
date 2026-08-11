package remoteci

import (
	"go/ast"
	"sort"
)

// 已绑定到局部变量的同包 factory 结果可按显式具体返回类型解析；直接链式调用仍 fail-closed。
func (closure *workerExecutionGoClosure) workerExecutionAssignedExpressionType(unit *workerExecutionGoUnit, expression ast.Expr) (string, string, bool) {
	switch value := expression.(type) {
	case *ast.CallExpr:
		identifier, localFactory := value.Fun.(*ast.Ident)
		if !localFactory {
			return closure.workerExecutionAssignmentType(unit, expression)
		}
		candidates := closure.index.symbols[unit.directory][identifier.Name]
		if len(candidates) != 1 {
			return "", "", false
		}
		return workerExecutionFunctionResultReceiverType(unit, unit.directory, candidates[0].signature, true)
	case *ast.UnaryExpr:
		return closure.workerExecutionAssignedExpressionType(unit, value.X)
	case *ast.ParenExpr:
		return closure.workerExecutionAssignedExpressionType(unit, value.X)
	default:
		return closure.workerExecutionAssignmentType(unit, expression)
	}
}

// 解析单个赋值表达式，仅接受 imported-local factory 或本地 composite literal。
func (closure *workerExecutionGoClosure) workerExecutionAssignmentType(unit *workerExecutionGoUnit, expression ast.Expr) (string, string, bool) {
	switch value := expression.(type) {
	case *ast.CallExpr:
		return closure.workerExecutionImportedFactoryType(unit, value)
	case *ast.CompositeLit:
		return workerExecutionLocalCompositeType(unit, value.Type)
	case *ast.UnaryExpr:
		return closure.workerExecutionAssignmentType(unit, value.X)
	case *ast.ParenExpr:
		return closure.workerExecutionAssignmentType(unit, value.X)
	case *ast.SelectorExpr:
		return closure.workerExecutionFieldAssignmentType(unit, value)
	case *ast.IndexExpr:
		return closure.workerExecutionRangeElementReceiverType(unit, value.X)
	default:
		return "", "", false
	}
}

// 从本地 struct 字段读取赋值表达式的静态 receiver 类型。
func (closure *workerExecutionGoClosure) workerExecutionFieldAssignmentType(unit *workerExecutionGoUnit, selector *ast.SelectorExpr) (string, string, bool) {
	directory, receiver, resolved := closure.resolveWorkerSelectorReceiverType(unit, selector.X)
	if !resolved {
		return "", "", false
	}
	fieldUnit, fieldType := closure.workerExecutionReceiverFieldType(directory, receiver, selector.Sel.Name)
	if fieldUnit == nil || fieldType == nil {
		return "", "", false
	}
	return workerExecutionDeclaredReceiverType(fieldUnit, directory, fieldType)
}

// 从 imported-local factory 的唯一函数声明读取首个返回值 receiver 类型。
func (closure *workerExecutionGoClosure) workerExecutionImportedFactoryType(unit *workerExecutionGoUnit, call *ast.CallExpr) (string, string, bool) {
	if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
		identifier, ok := selector.X.(*ast.Ident)
		if !ok {
			return "", "", false
		}
		imported, ok := unit.imports[identifier.Name]
		if !ok || !imported.local {
			return "", "", false
		}
		candidates := closure.index.symbols[imported.directory][selector.Sel.Name]
		if len(candidates) != 1 {
			return "", "", false
		}
		if _, conversion := candidates[0].node.(*ast.TypeSpec); conversion {
			return imported.directory, selector.Sel.Name, true
		}
		return workerExecutionFunctionResultReceiverType(unit, imported.directory, candidates[0].signature, true)
	}
	identifier, ok := call.Fun.(*ast.Ident)
	if !ok {
		return "", "", false
	}
	candidates := closure.index.symbols[unit.directory][identifier.Name]
	if len(candidates) != 1 {
		return "", "", false
	}
	if _, conversion := candidates[0].node.(*ast.TypeSpec); conversion {
		return unit.directory, identifier.Name, true
	}
	return workerExecutionFunctionResultReceiverType(unit, unit.directory, candidates[0].signature, false)
}

// 读取函数首个结果的本地 receiver 类型，其他返回值属于错误或状态信息。
func workerExecutionFunctionResultReceiverType(unit *workerExecutionGoUnit, directory string, signature ast.Node, allowLocalIdent bool) (string, string, bool) {
	expression, ok := workerExecutionFirstFunctionResultType(signature)
	if !ok {
		return "", "", false
	}
	expression = workerExecutionUnwrapPointerType(expression)
	switch value := expression.(type) {
	case *ast.Ident:
		if !allowLocalIdent {
			return "", "", false
		}
		return directory, value.Name, true
	case *ast.SelectorExpr:
		return workerExecutionImportedReceiverType(unit, value)
	default:
		return "", "", false
	}
}

// 读取函数签名的首个结果类型。
func workerExecutionFirstFunctionResultType(signature ast.Node) (ast.Expr, bool) {
	results, ok := signature.(*ast.FieldList)
	if !ok || results == nil || len(results.List) == 0 {
		return nil, false
	}
	return results.List[0].Type, true
}

// 展开任意层级的指针结果类型。
func workerExecutionUnwrapPointerType(expression ast.Expr) ast.Expr {
	for {
		pointer, ok := expression.(*ast.StarExpr)
		if !ok {
			return expression
		}
		expression = pointer.X
	}
}

// 将本地 import selector 解析为 receiver 目录和类型。
func workerExecutionImportedReceiverType(unit *workerExecutionGoUnit, selector *ast.SelectorExpr) (string, string, bool) {
	if unit == nil {
		return "", "", false
	}
	identifier, ok := selector.X.(*ast.Ident)
	if !ok {
		return "", "", false
	}
	imported, ok := unit.imports[identifier.Name]
	if !ok || !imported.local {
		return "", "", false
	}
	return imported.directory, selector.Sel.Name, true
}

// 解析字段声明中的本地 receiver 类型，兼容指针和 imported-local selector。
func workerExecutionDeclaredReceiverType(unit *workerExecutionGoUnit, directory string, expression ast.Expr) (string, string, bool) {
	switch value := expression.(type) {
	case *ast.StarExpr:
		return workerExecutionDeclaredReceiverType(unit, directory, value.X)
	case *ast.Ident:
		return directory, value.Name, true
	case *ast.SelectorExpr:
		identifier, ok := value.X.(*ast.Ident)
		if !ok {
			return "", "", false
		}
		imported, ok := unit.imports[identifier.Name]
		if !ok || !imported.local {
			return "", "", false
		}
		return imported.directory, value.Sel.Name, true
	default:
		return "", "", false
	}
}

// 显式类型声明必须与赋值推导出的 imported-local receiver 一致。
func workerExecutionReceiverDeclarationType(unit *workerExecutionGoUnit, declarations []ast.Expr, directory, receiver string) (string, string, bool) {
	for _, declaration := range declarations {
		declDirectory, declReceiver, local, external := workerExecutionReceiverType(unit, declaration)
		if !local || external || declDirectory != directory || declReceiver != receiver {
			return "", "", false
		}
	}
	return directory, receiver, directory != "" && receiver != ""
}

// 本地 composite literal 的类型仅在当前包中可静态闭包解析。
func workerExecutionLocalCompositeType(unit *workerExecutionGoUnit, expression ast.Expr) (string, string, bool) {
	directory, receiver, local, external := workerExecutionReceiverType(unit, expression)
	return directory, receiver, local && !external
}

// 判断本地 receiver 字段是否返回明确的外部类型，供后续方法链跳过动态拒绝。
func workerExecutionExternalField(closure *workerExecutionGoClosure, unit *workerExecutionGoUnit, selector *ast.SelectorExpr) bool {
	directory, receiver, resolved := closure.resolveWorkerSelectorReceiverType(unit, selector.X)
	if !resolved {
		return false
	}
	fieldUnit, fieldType := closure.workerExecutionReceiverFieldType(directory, receiver, selector.Sel.Name)
	return fieldUnit != nil && fieldType != nil && workerExecutionExternalFieldType(closure, fieldUnit, fieldType)
}

// 从已知 localTypes 或 imported-local factory 解析 selector receiver 类型。
func (closure *workerExecutionGoClosure) resolveWorkerSelectorReceiverType(unit *workerExecutionGoUnit, expression ast.Expr) (string, string, bool) {
	identifier, ok := expression.(*ast.Ident)
	if ok {
		if localType, exists := unit.localTypes[identifier.Name]; exists {
			directory, receiver, local, external := workerExecutionReceiverType(unit, localType)
			return directory, receiver, local && !external
		}
		return closure.resolveWorkerAssignedReceiverType(unit, identifier.Name)
	}
	return closure.workerExecutionAssignmentType(unit, expression)
}

// 查找本地 struct 字段的声明类型，未声明字段保持未知。
func (closure *workerExecutionGoClosure) workerExecutionReceiverFieldType(directory, receiver, name string) (*workerExecutionGoUnit, ast.Expr) {
	for _, unit := range closure.index.symbols[directory][receiver] {
		typeSpec, ok := unit.node.(*ast.TypeSpec)
		if !ok {
			continue
		}
		structure, ok := typeSpec.Type.(*ast.StructType)
		if !ok || structure.Fields == nil {
			continue
		}
		for _, field := range structure.Fields.List {
			for _, fieldName := range field.Names {
				if fieldName.Name == name {
					return unit, field.Type
				}
			}
		}
	}
	return nil, nil
}

// 函数字段只在至少一个返回类型明确来自外部 import 时判定为外部链。
func workerExecutionExternalFieldType(closure *workerExecutionGoClosure, unit *workerExecutionGoUnit, expression ast.Expr) bool {
	function, ok := expression.(*ast.FuncType)
	if !ok {
		return workerExecutionExternalExpression(closure, unit, expression, make(map[string]struct{}))
	}
	if function.Results == nil {
		return false
	}
	for _, result := range function.Results.List {
		if workerExecutionExternalExpression(closure, unit, result.Type, make(map[string]struct{})) {
			return true
		}
	}
	return false
}

// 识别本地 struct 的函数字段调用；字段实现由构造函数及其闭包单独纳入。
func (closure *workerExecutionGoClosure) workerExecutionReceiverField(directory, receiver, name string) bool {
	for _, unit := range closure.index.symbols[directory][receiver] {
		typeSpec, ok := unit.node.(*ast.TypeSpec)
		if !ok {
			continue
		}
		structure, ok := typeSpec.Type.(*ast.StructType)
		if !ok || structure.Fields == nil {
			continue
		}
		for _, field := range structure.Fields.List {
			for _, fieldName := range field.Names {
				if fieldName.Name == name {
					return true
				}
			}
		}
	}
	return false
}

// 将已声明的本地接口方法映射到所有同包具体实现，保持接口分派的精确方法边界。
func (closure *workerExecutionGoClosure) enqueueWorkerInterfaceMethod(directory, receiver, name string) (bool, error) {
	if !closure.workerExecutionInterfaceDeclares(directory, receiver, name) {
		return false, nil
	}
	receivers := make([]string, 0, len(closure.index.receiverMethods[directory]))
	for candidate := range closure.index.receiverMethods[directory] {
		receivers = append(receivers, candidate)
	}
	sort.Strings(receivers)
	for _, candidate := range receivers {
		for _, method := range closure.index.receiverMethods[directory][candidate] {
			if len(method.names) == 1 && method.names[0] == name {
				closure.enqueue(method)
			}
		}
	}
	return true, nil
}

// 检查本地接口声明是否包含目标方法；匿名或未知接口不作放行。
func (closure *workerExecutionGoClosure) workerExecutionInterfaceDeclares(directory, receiver, name string) bool {
	for _, unit := range closure.index.symbols[directory][receiver] {
		typeSpec, ok := unit.node.(*ast.TypeSpec)
		if !ok {
			continue
		}
		interfaceType, ok := typeSpec.Type.(*ast.InterfaceType)
		if !ok || interfaceType.Methods == nil {
			continue
		}
		for _, field := range interfaceType.Methods.List {
			for _, method := range field.Names {
				if method.Name == name {
					return true
				}
			}
		}
	}
	return false
}
