package remoteci

import (
	"go/ast"
	"maps"
)

type workerExecutionExternalAssignmentSource struct {
	expression  ast.Expr
	resultIndex int
	rangeValue  bool
}

// 判断 selector 是否明确来自外部包，避免把外部链式调用误报为动态本地方法。
func workerExecutionExternalSelector(unit *workerExecutionGoUnit, expression ast.Expr) bool {
	return workerExecutionExternalSelectorWithClosure(nil, unit, expression)
}

// 绑定 closure 索引后解析本地函数型字段的外部返回类型。
func workerExecutionExternalSelectorWithClosure(closure *workerExecutionGoClosure, unit *workerExecutionGoUnit, expression ast.Expr) bool {
	return workerExecutionExternalExpression(closure, unit, expression, make(map[string]struct{}))
}

// 解析外部表达式及其局部变量 provenance，未知或混合来源一律返回 false。
func workerExecutionExternalExpression(closure *workerExecutionGoClosure, unit *workerExecutionGoUnit, expression ast.Expr, seen map[string]struct{}) bool {
	if unit == nil {
		return false
	}
	if selector, ok := expression.(*ast.SelectorExpr); ok {
		return workerExecutionExternalSelectorExpression(closure, unit, selector, seen)
	}
	if identifier, ok := expression.(*ast.Ident); ok {
		return workerExecutionExternalIdentifierExpression(closure, unit, identifier.Name, seen)
	}
	return workerExecutionExternalWrappedExpression(closure, unit, expression, seen)
}

// selector 仅在外部类型、外部字段或 imported-local 外部返回链得到静态证明时放行。
func workerExecutionExternalSelectorExpression(closure *workerExecutionGoClosure, unit *workerExecutionGoUnit, selector *ast.SelectorExpr, seen map[string]struct{}) bool {
	if closure != nil && (workerExecutionExternalTypedReceiver(unit, selector.X) || workerExecutionExternalField(closure, unit, selector)) {
		return true
	}
	if closure != nil && workerExecutionExternalImportedSelector(closure, unit, selector, seen) {
		return true
	}
	return workerExecutionExternalExpression(closure, unit, selector.X, seen)
}

// 标识符按预声明类型、显式外部类型、import 或唯一赋值 provenance 解析。
func workerExecutionExternalIdentifierExpression(closure *workerExecutionGoClosure, unit *workerExecutionGoUnit, name string, seen map[string]struct{}) bool {
	if name == "error" {
		return true
	}
	if localType, exists := unit.localTypes[name]; exists && workerExecutionExternalDeclaredType(unit, localType) {
		return true
	}
	if imported, exists := unit.imports[name]; exists {
		return !imported.local && !workerExecutionLocalName(unit, name)
	}
	if !workerExecutionLocalName(unit, name) {
		return workerExecutionExternalUnknownIdentifier(closure, unit, name, seen)
	}
	if _, repeated := seen[name]; repeated {
		return false
	}
	seen[name] = struct{}{}
	return workerExecutionExternalAssignment(closure, unit, name, seen)
}

// 未在局部声明的标识符只接受外部匿名函数参数或唯一外部全局声明。
func workerExecutionExternalUnknownIdentifier(closure *workerExecutionGoClosure, unit *workerExecutionGoUnit, name string, seen map[string]struct{}) bool {
	if workerExecutionExternalFunctionLiteralParameter(unit, name) {
		return true
	}
	return closure != nil && workerExecutionExternalLocalDeclaration(closure, unit, name, seen)
}

// 包装表达式递归剥离调用、断言、索引、指针和括号；其他 AST 节点保持未知。
func workerExecutionExternalWrappedExpression(closure *workerExecutionGoClosure, unit *workerExecutionGoUnit, expression ast.Expr, seen map[string]struct{}) bool {
	switch value := expression.(type) {
	case *ast.CallExpr:
		return workerExecutionExternalExpression(closure, unit, value.Fun, seen)
	case *ast.BinaryExpr:
		return workerExecutionExternalBinaryExpression(closure, unit, value, seen)
	case *ast.TypeAssertExpr:
		return workerExecutionExternalExpression(closure, unit, value.Type, seen)
	case *ast.IndexExpr:
		return workerExecutionExternalExpression(closure, unit, value.X, seen)
	case *ast.IndexListExpr:
		return workerExecutionExternalExpression(closure, unit, value.X, seen)
	case *ast.StarExpr:
		return workerExecutionExternalExpression(closure, unit, value.X, seen)
	case *ast.ParenExpr:
		return workerExecutionExternalExpression(closure, unit, value.X, seen)
	default:
		return false
	}
}

// 匿名函数参数只有在同一声明函数内唯一且显式为外部类型时才可作为外部 receiver。
func workerExecutionExternalFunctionLiteralParameter(unit *workerExecutionGoUnit, name string) bool {
	found, external := false, true
	ast.Inspect(unit.dependencies, func(node ast.Node) bool {
		literal, ok := node.(*ast.FuncLit)
		if !ok || literal.Type == nil || literal.Type.Params == nil {
			return true
		}
		for _, field := range literal.Type.Params.List {
			for _, identifier := range field.Names {
				if identifier.Name != name {
					continue
				}
				if found || !workerExecutionExternalDeclaredType(unit, field.Type) {
					external = false
				}
				found = true
			}
		}
		return true
	})
	return found && external
}

// 二元常量表达式仅在类型来源明确来自外部、另一侧为字面量或同一外部链时接受。
func workerExecutionExternalBinaryExpression(closure *workerExecutionGoClosure, unit *workerExecutionGoUnit, expression *ast.BinaryExpr, seen map[string]struct{}) bool {
	leftExternal := workerExecutionExternalExpression(closure, unit, expression.X, seen)
	rightExternal := workerExecutionExternalExpression(closure, unit, expression.Y, seen)
	if leftExternal && rightExternal {
		return true
	}
	_, leftLiteral := expression.X.(*ast.BasicLit)
	_, rightLiteral := expression.Y.(*ast.BasicLit)
	return (leftExternal && rightLiteral) || (rightExternal && leftLiteral)
}

// 解析 imported-local selector 的显式声明类型或函数结果；未能唯一证明时保持本地闭包语义。
func workerExecutionExternalImportedSelector(closure *workerExecutionGoClosure, unit *workerExecutionGoUnit, selector *ast.SelectorExpr, seen map[string]struct{}) bool {
	identifier, ok := selector.X.(*ast.Ident)
	if !ok {
		return false
	}
	imported, ok := unit.imports[identifier.Name]
	if !ok || !imported.local {
		return false
	}
	candidates := closure.index.symbols[imported.directory][selector.Sel.Name]
	if len(candidates) != 1 {
		return false
	}
	candidate := candidates[0]
	key := "local-selector:" + candidate.key
	if _, repeated := seen[key]; repeated {
		return false
	}
	seen[key] = struct{}{}
	return workerExecutionExternalImportedDeclaration(closure, candidate, selector.Sel.Name, seen)
}

// 从单一 imported-local 声明读取外部类型、值或函数结果，拒绝不完整的声明信息。
func workerExecutionExternalImportedDeclaration(closure *workerExecutionGoClosure, unit *workerExecutionGoUnit, name string, seen map[string]struct{}) bool {
	switch declaration := unit.node.(type) {
	case *ast.ValueSpec:
		if declaration.Type != nil && workerExecutionExternalExpression(closure, unit, declaration.Type, seen) {
			return true
		}
		for index, identifier := range declaration.Names {
			if identifier.Name == name && index < len(declaration.Values) && workerExecutionExternalExpression(closure, unit, declaration.Values[index], seen) {
				return true
			}
		}
	case *ast.TypeSpec:
		return workerExecutionExternalExpression(closure, unit, declaration.Type, seen)
	case *ast.FuncDecl:
		return workerExecutionExternalFunctionResult(closure, unit, declaration.Type.Results, seen)
	}
	return false
}

// 函数结果的显式外部类型可以安全跳过本地 receiver 方法闭包。
func workerExecutionExternalFunctionResult(closure *workerExecutionGoClosure, unit *workerExecutionGoUnit, results *ast.FieldList, seen map[string]struct{}) bool {
	if results == nil {
		return false
	}
	for _, result := range results.List {
		if workerExecutionExternalExpression(closure, unit, result.Type, seen) {
			return true
		}
	}
	return false
}

// 识别参数或显式局部变量声明中的标准库/外部 receiver 类型。
func workerExecutionExternalTypedReceiver(unit *workerExecutionGoUnit, expression ast.Expr) bool {
	identifier, ok := expression.(*ast.Ident)
	if !ok {
		return false
	}
	localType, exists := unit.localTypes[identifier.Name]
	if !exists {
		return false
	}
	return workerExecutionExternalDeclaredType(unit, localType)
}

// 递归识别容器、指针和函数结果中的外部声明类型。
func workerExecutionExternalDeclaredType(unit *workerExecutionGoUnit, expression ast.Expr) bool {
	switch value := expression.(type) {
	case *ast.FuncType:
		return workerExecutionExternalFunctionResultType(unit, value.Results)
	case *ast.SelectorExpr:
		return workerExecutionExternalSelectorType(unit, value)
	case *ast.Ident:
		return value.Name == "error"
	default:
		return workerExecutionExternalNestedType(unit, expression)
	}
}

// 判断函数结果中是否至少包含一个外部声明类型。
func workerExecutionExternalFunctionResultType(unit *workerExecutionGoUnit, results *ast.FieldList) bool {
	if results == nil {
		return false
	}
	for _, result := range results.List {
		if workerExecutionExternalDeclaredType(unit, result.Type) {
			return true
		}
	}
	return false
}

// 判断 selector 声明类型是否来自外部 import。
func workerExecutionExternalSelectorType(unit *workerExecutionGoUnit, selector *ast.SelectorExpr) bool {
	identifier, ok := selector.X.(*ast.Ident)
	if !ok || unit == nil {
		return false
	}
	imported, ok := unit.imports[identifier.Name]
	return ok && !imported.local
}

// 递归展开指针和容器声明类型。
func workerExecutionExternalNestedType(unit *workerExecutionGoUnit, expression ast.Expr) bool {
	switch value := expression.(type) {
	case *ast.StarExpr:
		return workerExecutionExternalDeclaredType(unit, value.X)
	case *ast.ArrayType:
		return workerExecutionExternalDeclaredType(unit, value.Elt)
	case *ast.MapType:
		return workerExecutionExternalDeclaredType(unit, value.Value)
	case *ast.ChanType:
		return workerExecutionExternalDeclaredType(unit, value.Value)
	case *ast.Ellipsis:
		return workerExecutionExternalDeclaredType(unit, value.Elt)
	default:
		return false
	}
}

// 仅接受局部变量的单一、明确外部赋值来源，拒绝本地 factory、接口和混合重赋值。
func workerExecutionExternalAssignment(closure *workerExecutionGoClosure, unit *workerExecutionGoUnit, name string, seen map[string]struct{}) bool {
	if unit.dependencies == nil {
		return false
	}
	sources := workerExecutionExternalAssignmentSources(unit.dependencies, name)
	if len(sources) > 0 {
		return workerExecutionExternalAssignmentSourcesValid(closure, unit, sources, seen)
	}
	assignments := workerExecutionExternalAssignments(unit.dependencies, name)
	if len(assignments) == 0 {
		return workerExecutionExternalDeclaration(closure, unit, name, seen)
	}
	return workerExecutionExternalAssignmentValuesValid(closure, unit, assignments, seen)
}

// 校验带结果下标的赋值来源均能静态证明为外部值。
func workerExecutionExternalAssignmentSourcesValid(closure *workerExecutionGoClosure, unit *workerExecutionGoUnit, sources []workerExecutionExternalAssignmentSource, seen map[string]struct{}) bool {
	for _, source := range sources {
		branchSeen := maps.Clone(seen)
		call, callResult := source.expression.(*ast.CallExpr)
		if callResult && source.resultIndex >= 0 && workerExecutionExternalCallResult(closure, unit, call, source.resultIndex, branchSeen) {
			continue
		}
		if !workerExecutionExternalExpression(closure, unit, source.expression, branchSeen) {
			return false
		}
	}
	return true
}

// 校验普通赋值的每个来源均能静态证明为外部值。
func workerExecutionExternalAssignmentValuesValid(closure *workerExecutionGoClosure, unit *workerExecutionGoUnit, assignments []ast.Expr, seen map[string]struct{}) bool {
	for _, assignment := range assignments {
		if !workerExecutionExternalExpression(closure, unit, assignment, seen) {
			return false
		}
	}
	return true
}

// 从显式类型或可解析的本地声明恢复变量来源。
func workerExecutionExternalDeclaration(closure *workerExecutionGoClosure, unit *workerExecutionGoUnit, name string, seen map[string]struct{}) bool {
	if workerExecutionExternalTypeDeclaration(closure, unit, unit.dependencies, name, seen) {
		return true
	}
	return closure != nil && workerExecutionExternalLocalDeclaration(closure, unit, name, seen)
}

// 收集赋值表达式及其多返回值位置，避免把同一调用的本地结果误认成 error 等外部类型。
func workerExecutionExternalAssignmentSources(node ast.Node, name string) []workerExecutionExternalAssignmentSource {
	var sources []workerExecutionExternalAssignmentSource
	ast.Inspect(node, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.AssignStmt:
			sources = append(sources, workerExecutionExternalAssignmentStatementSources(value.Lhs, value.Rhs, name)...)
		case *ast.ValueSpec:
			lhs := make([]ast.Expr, 0, len(value.Names))
			for _, identifier := range value.Names {
				lhs = append(lhs, identifier)
			}
			sources = append(sources, workerExecutionExternalAssignmentStatementSources(lhs, value.Values, name)...)
		case *ast.RangeStmt:
			identifier, ok := value.Value.(*ast.Ident)
			if ok && identifier.Name == name {
				sources = append(sources, workerExecutionExternalAssignmentSource{expression: value.X, resultIndex: -1, rangeValue: true})
			}
		}
		return true
	})
	return sources
}

// 将一个赋值中的目标标识符映射到普通 RHS 或单调用的精确返回值位置。
func workerExecutionExternalAssignmentStatementSources(lhs, rhs []ast.Expr, name string) []workerExecutionExternalAssignmentSource {
	for index, expression := range lhs {
		identifier, ok := expression.(*ast.Ident)
		if !ok || identifier.Name != name {
			continue
		}
		if len(rhs) == 1 {
			resultIndex := -1
			if _, call := rhs[0].(*ast.CallExpr); call {
				resultIndex = index
			}
			return []workerExecutionExternalAssignmentSource{{expression: rhs[0], resultIndex: resultIndex}}
		}
		if index < len(rhs) {
			return []workerExecutionExternalAssignmentSource{{expression: rhs[index], resultIndex: -1}}
		}
	}
	return nil
}

// 校验本地或 imported-local 调用的指定返回值确属外部类型；未知签名保持 fail-closed。
func workerExecutionExternalCallResult(closure *workerExecutionGoClosure, unit *workerExecutionGoUnit, call *ast.CallExpr, resultIndex int, seen map[string]struct{}) bool {
	if workerExecutionExternalExpression(closure, unit, call.Fun, seen) {
		return true
	}
	declaration := workerExecutionCallDeclaration(closure, unit, call.Fun)
	if declaration == nil {
		return false
	}
	function, ok := declaration.node.(*ast.FuncDecl)
	if !ok {
		return false
	}
	resultType, ok := workerExecutionFunctionResultTypeAt(function.Type.Results, resultIndex)
	return ok && workerExecutionExternalExpression(closure, declaration, resultType, seen)
}

// 解析当前包或 imported-local 包中的唯一函数声明。
func workerExecutionCallDeclaration(closure *workerExecutionGoClosure, unit *workerExecutionGoUnit, expression ast.Expr) *workerExecutionGoUnit {
	directory, name := unit.directory, ""
	switch value := expression.(type) {
	case *ast.Ident:
		name = value.Name
	case *ast.SelectorExpr:
		identifier, ok := value.X.(*ast.Ident)
		if !ok {
			return nil
		}
		imported, ok := unit.imports[identifier.Name]
		if !ok || !imported.local {
			return nil
		}
		directory, name = imported.directory, value.Sel.Name
	default:
		return nil
	}
	candidates := closure.index.symbols[directory][name]
	if len(candidates) != 1 {
		return nil
	}
	return candidates[0]
}

// 展开命名结果字段并返回指定调用结果的声明类型。
func workerExecutionFunctionResultTypeAt(results *ast.FieldList, resultIndex int) (ast.Expr, bool) {
	if results == nil || resultIndex < 0 {
		return nil, false
	}
	current := 0
	for _, field := range results.List {
		width := len(field.Names)
		if width == 0 {
			width = 1
		}
		if resultIndex >= current && resultIndex < current+width {
			return field.Type, true
		}
		current += width
	}
	return nil, false
}

// 从当前包唯一的全局声明解析外部类型和值，局部未声明标识符不得静默放行。
func workerExecutionExternalLocalDeclaration(closure *workerExecutionGoClosure, unit *workerExecutionGoUnit, name string, seen map[string]struct{}) bool {
	candidates := closure.index.symbols[unit.directory][name]
	if len(candidates) != 1 {
		return false
	}
	candidate := candidates[0]
	key := "local-declaration:" + candidate.key
	if _, repeated := seen[key]; repeated {
		return false
	}
	seen[key] = struct{}{}
	return workerExecutionExternalImportedDeclaration(closure, candidate, name, seen)
}

// 识别带显式外部类型的局部变量声明，例如 strings.Builder 或 fs.FileInfo。
func workerExecutionExternalTypeDeclaration(closure *workerExecutionGoClosure, unit *workerExecutionGoUnit, node ast.Node, name string, seen map[string]struct{}) bool {
	external := false
	ast.Inspect(node, func(node ast.Node) bool {
		value, ok := node.(*ast.ValueSpec)
		if !ok || value.Type == nil {
			return true
		}
		for _, identifier := range value.Names {
			if identifier.Name == name {
				external = workerExecutionExternalExpression(closure, unit, value.Type, seen)
				return false
			}
		}
		return true
	})
	return external
}

// 收集局部变量的所有显式赋值表达式，多个来源必须全部可证明来自外部。
func workerExecutionExternalAssignments(node ast.Node, name string) []ast.Expr {
	var assignments []ast.Expr
	ast.Inspect(node, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.AssignStmt:
			assignments = append(assignments, workerExecutionExternalAssignmentValues(value.Lhs, value.Rhs, name)...)
		case *ast.ValueSpec:
			for index, identifier := range value.Names {
				if identifier.Name != name || index >= len(value.Values) {
					continue
				}
				assignments = append(assignments, value.Values[index])
			}
		case *ast.RangeStmt:
			identifier, ok := value.Value.(*ast.Ident)
			if ok && identifier.Name == name {
				assignments = append(assignments, value.X)
			}
		}
		return true
	})
	return assignments
}

// 将 assignment 的 LHS 与 RHS 配对；多返回值表达式的每个绑定都沿用同一外部来源。
func workerExecutionExternalAssignmentValues(lhs, rhs []ast.Expr, name string) []ast.Expr {
	if len(rhs) == 0 {
		return nil
	}
	for index, expression := range lhs {
		identifier, ok := expression.(*ast.Ident)
		if !ok || identifier.Name != name {
			continue
		}
		if len(rhs) == 1 || index < len(rhs) {
			if len(rhs) == 1 {
				return []ast.Expr{rhs[0]}
			}
			return []ast.Expr{rhs[index]}
		}
		return nil
	}
	return nil
}

// 解析局部变量由 imported-local factory 返回的具体 receiver 类型。
func (closure *workerExecutionGoClosure) resolveWorkerAssignedReceiverType(unit *workerExecutionGoUnit, name string) (string, string, bool) {
	return closure.resolveWorkerAssignedReceiverTypeSeen(unit, name, make(map[string]struct{}))
}

// 沿局部别名追踪 receiver，循环赋值或混合类型立即保持未知。
func (closure *workerExecutionGoClosure) resolveWorkerAssignedReceiverTypeSeen(unit *workerExecutionGoUnit, name string, seen map[string]struct{}) (string, string, bool) {
	key := unit.directory + ":" + name
	if _, repeated := seen[key]; repeated {
		return "", "", false
	}
	seen[key] = struct{}{}
	defer delete(seen, key)
	assignments := workerExecutionExternalAssignmentSources(unit.dependencies, name)
	declarations := workerExecutionReceiverDeclarations(unit.dependencies, name)
	if len(assignments) == 0 && len(declarations) == 0 {
		return closure.workerExecutionRangeReceiverType(unit, name)
	}
	if len(assignments) == 0 {
		return workerExecutionReceiverDeclarationOnly(unit, declarations)
	}
	directory, receiver, ok := workerExecutionReceiverAssignmentType(closure, unit, assignments, seen)
	if !ok {
		return "", "", false
	}
	return workerExecutionReceiverDeclarationType(unit, declarations, directory, receiver)
}

// 收集局部变量的显式类型声明；赋值来源由带返回值位置的统一 collector 负责。
func workerExecutionReceiverDeclarations(node ast.Node, name string) []ast.Expr {
	var declarations []ast.Expr
	ast.Inspect(node, func(node ast.Node) bool {
		declaration, ok := node.(*ast.ValueSpec)
		if !ok || declaration.Type == nil {
			return true
		}
		for _, identifier := range declaration.Names {
			if identifier.Name == name {
				declarations = append(declarations, declaration.Type)
			}
		}
		return true
	})
	return declarations
}

// range value 仅在容器元素类型可静态证明为同一本地 receiver 时解析。
func (closure *workerExecutionGoClosure) workerExecutionRangeReceiverType(unit *workerExecutionGoUnit, name string) (string, string, bool) {
	var directory, receiver string
	found := false
	valid := true
	ast.Inspect(unit.dependencies, func(node ast.Node) bool {
		statement, ok := node.(*ast.RangeStmt)
		if !ok {
			return true
		}
		identifier, ok := statement.Value.(*ast.Ident)
		if !ok || identifier.Name != name {
			return true
		}
		currentDirectory, currentReceiver, resolved := closure.workerExecutionRangeElementReceiverType(unit, statement.X)
		if !resolved || (found && (directory != currentDirectory || receiver != currentReceiver)) {
			valid = false
		}
		directory, receiver, found = currentDirectory, currentReceiver, true
		return true
	})
	return directory, receiver, found && valid
}

// 从显式容器参数或局部声明读取 range value 的本地元素类型。
func (closure *workerExecutionGoClosure) workerExecutionRangeElementReceiverType(unit *workerExecutionGoUnit, expression ast.Expr) (string, string, bool) {
	if identifier, ok := expression.(*ast.Ident); ok {
		if directory, receiver, resolved := closure.workerExecutionAssignedContainerElementType(unit, identifier.Name); resolved {
			return directory, receiver, true
		}
	}
	directory, container, ok := closure.workerExecutionRangeContainerType(unit, expression)
	if !ok {
		return "", "", false
	}
	return workerExecutionContainerElementReceiverType(unit, directory, container)
}

// 从局部容器的所有赋值来源解析统一元素类型；同变量重切片不改变元素类型。
func (closure *workerExecutionGoClosure) workerExecutionAssignedContainerElementType(unit *workerExecutionGoUnit, name string) (string, string, bool) {
	return closure.workerExecutionAssignedContainerElementTypeSeen(unit, name, make(map[string]struct{}))
}

// 容器别名递归追踪带循环检测，所有非自身投影来源必须收敛到同一元素类型。
func (closure *workerExecutionGoClosure) workerExecutionAssignedContainerElementTypeSeen(unit *workerExecutionGoUnit, name string, seen map[string]struct{}) (string, string, bool) {
	key := unit.directory + ":container:" + name
	if _, repeated := seen[key]; repeated {
		return "", "", false
	}
	seen[key] = struct{}{}
	defer delete(seen, key)
	sources := workerExecutionExternalAssignmentSources(unit.dependencies, name)
	var directory, receiver string
	found := false
	for _, source := range sources {
		if workerExecutionSelfContainerProjection(source.expression, name) {
			continue
		}
		currentDirectory, currentReceiver, resolved := closure.workerExecutionAssignedContainerSourceElementType(unit, source, seen)
		if !resolved || (found && (directory != currentDirectory || receiver != currentReceiver)) {
			return "", "", false
		}
		directory, receiver, found = currentDirectory, currentReceiver, true
	}
	return directory, receiver, found
}

// 自身重切片只改变长度或容量，不改变容器的静态元素类型。
func workerExecutionSelfContainerProjection(expression ast.Expr, name string) bool {
	slice, ok := expression.(*ast.SliceExpr)
	if !ok {
		return false
	}
	identifier, ok := slice.X.(*ast.Ident)
	return ok && identifier.Name == name
}

// 单个赋值来源只接受可精确定位的函数返回容器或显式容器表达式。
func (closure *workerExecutionGoClosure) workerExecutionAssignedContainerSourceElementType(unit *workerExecutionGoUnit, source workerExecutionExternalAssignmentSource, seen map[string]struct{}) (string, string, bool) {
	call, ok := source.expression.(*ast.CallExpr)
	if ok {
		if directory, receiver, resolved := workerExecutionBuiltinContainerElementType(unit, call); resolved {
			return directory, receiver, true
		}
		if source.resultIndex >= 0 {
			owner, resultType := closure.workerExecutionCallResultType(unit, call, source.resultIndex)
			if owner == nil || resultType == nil {
				return "", "", false
			}
			return workerExecutionContainerElementReceiverType(owner, owner.directory, resultType)
		}
		return workerExecutionBuiltinContainerElementType(unit, call)
	}
	switch value := source.expression.(type) {
	case *ast.CompositeLit:
		return workerExecutionContainerElementReceiverType(unit, unit.directory, value.Type)
	case *ast.Ident:
		if container, exists := unit.localTypes[value.Name]; exists {
			return workerExecutionContainerElementReceiverType(unit, unit.directory, container)
		}
		return closure.workerExecutionAssignedContainerElementTypeSeen(unit, value.Name, seen)
	default:
		return "", "", false
	}
}

// make 仅以显式容器类型作为元素类型来源，其他内建或动态调用保持未知。
func workerExecutionBuiltinContainerElementType(unit *workerExecutionGoUnit, call *ast.CallExpr) (string, string, bool) {
	identifier, ok := call.Fun.(*ast.Ident)
	if !ok || identifier.Name != "make" || len(call.Args) == 0 {
		return "", "", false
	}
	return workerExecutionContainerElementReceiverType(unit, unit.directory, call.Args[0])
}

// 函数调用按精确结果位置返回声明 owner 与类型，兼容局部命名函数值。
func (closure *workerExecutionGoClosure) workerExecutionCallResultType(unit *workerExecutionGoUnit, call *ast.CallExpr, resultIndex int) (*workerExecutionGoUnit, ast.Expr) {
	declaration := workerExecutionCallDeclaration(closure, unit, call.Fun)
	if declaration != nil {
		function, ok := declaration.node.(*ast.FuncDecl)
		if !ok {
			return nil, nil
		}
		resultType, ok := workerExecutionFunctionResultTypeAt(function.Type.Results, resultIndex)
		if !ok {
			return nil, nil
		}
		return declaration, resultType
	}
	identifier, ok := call.Fun.(*ast.Ident)
	if !ok {
		return nil, nil
	}
	expression, ok := unit.localTypes[identifier.Name]
	if !ok {
		return nil, nil
	}
	owner, function := closure.workerExecutionFunctionType(unit, expression)
	if owner == nil || function == nil {
		return nil, nil
	}
	resultType, ok := workerExecutionFunctionResultTypeAt(function.Results, resultIndex)
	if !ok {
		return nil, nil
	}
	return owner, resultType
}

// 解析 range 容器自身的显式参数类型或本地 struct 字段类型。
func (closure *workerExecutionGoClosure) workerExecutionRangeContainerType(unit *workerExecutionGoUnit, expression ast.Expr) (string, ast.Expr, bool) {
	if identifier, ok := expression.(*ast.Ident); ok {
		container, exists := unit.localTypes[identifier.Name]
		return unit.directory, container, exists
	}
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok {
		return "", nil, false
	}
	directory, receiver, resolved := closure.resolveWorkerSelectorReceiverType(unit, selector.X)
	if !resolved {
		return "", nil, false
	}
	fieldUnit, fieldType := closure.workerExecutionReceiverFieldType(directory, receiver, selector.Sel.Name)
	if fieldUnit == nil || fieldType == nil {
		return "", nil, false
	}
	return fieldUnit.directory, fieldType, true
}

// 从 slice、array、map 或 channel 的 value 声明读取本地元素 receiver。
func workerExecutionContainerElementReceiverType(unit *workerExecutionGoUnit, directory string, container ast.Expr) (string, string, bool) {
	switch value := container.(type) {
	case *ast.ArrayType:
		return workerExecutionDeclaredReceiverType(unit, directory, value.Elt)
	case *ast.MapType:
		return workerExecutionDeclaredReceiverType(unit, directory, value.Value)
	case *ast.ChanType:
		return workerExecutionDeclaredReceiverType(unit, directory, value.Value)
	default:
		return "", "", false
	}
}

// 无赋值时直接校验显式 imported-local 类型声明的一致性。
func workerExecutionReceiverDeclarationOnly(unit *workerExecutionGoUnit, declarations []ast.Expr) (string, string, bool) {
	var directory, receiver string
	for _, declaration := range declarations {
		currentDirectory, currentReceiver, local, external := workerExecutionReceiverType(unit, declaration)
		if !local || external || (directory != "" && (directory != currentDirectory || receiver != currentReceiver)) {
			return "", "", false
		}
		directory, receiver = currentDirectory, currentReceiver
	}
	return directory, receiver, directory != "" && receiver != ""
}

// 所有局部赋值都必须解析为同一个 imported-local receiver。
func workerExecutionReceiverAssignmentType(closure *workerExecutionGoClosure, unit *workerExecutionGoUnit, assignments []workerExecutionExternalAssignmentSource, seen map[string]struct{}) (string, string, bool) {
	if len(assignments) == 0 {
		return "", "", true
	}
	var directory, receiver string
	for _, assignment := range assignments {
		currentDirectory, currentReceiver, ok := closure.workerExecutionAssignedSourceType(unit, assignment, seen)
		if !ok || (directory != "" && (directory != currentDirectory || receiver != currentReceiver)) {
			return "", "", false
		}
		directory, receiver = currentDirectory, currentReceiver
	}
	return directory, receiver, directory != "" && receiver != ""
}

// 多返回值赋值按精确结果位置解析 receiver，避免把后续结果误当成首个返回类型。
func (closure *workerExecutionGoClosure) workerExecutionAssignedSourceType(unit *workerExecutionGoUnit, source workerExecutionExternalAssignmentSource, seen map[string]struct{}) (string, string, bool) {
	if source.rangeValue {
		return closure.workerExecutionRangeElementReceiverType(unit, source.expression)
	}
	if source.resultIndex < 0 {
		if identifier, ok := source.expression.(*ast.Ident); ok {
			return closure.resolveWorkerAssignedReceiverTypeSeen(unit, identifier.Name, seen)
		}
		return closure.workerExecutionAssignedExpressionType(unit, source.expression)
	}
	call, ok := source.expression.(*ast.CallExpr)
	if !ok {
		return "", "", false
	}
	declaration := workerExecutionCallDeclaration(closure, unit, call.Fun)
	if declaration == nil {
		return closure.workerExecutionLocalCallResultReceiverType(unit, call, source.resultIndex)
	}
	function, ok := declaration.node.(*ast.FuncDecl)
	if !ok {
		return closure.workerExecutionAssignedExpressionType(unit, source.expression)
	}
	resultType, ok := workerExecutionFunctionResultTypeAt(function.Type.Results, source.resultIndex)
	if !ok {
		return "", "", false
	}
	return workerExecutionDeclaredReceiverType(declaration, declaration.directory, resultType)
}

// 局部函数值调用按其显式 func 类型或命名函数类型解析指定结果。
func (closure *workerExecutionGoClosure) workerExecutionLocalCallResultReceiverType(unit *workerExecutionGoUnit, call *ast.CallExpr, resultIndex int) (string, string, bool) {
	identifier, ok := call.Fun.(*ast.Ident)
	if !ok {
		return "", "", false
	}
	expression, ok := unit.localTypes[identifier.Name]
	if !ok {
		return "", "", false
	}
	owner, function := closure.workerExecutionFunctionType(unit, expression)
	if owner == nil || function == nil {
		return "", "", false
	}
	resultType, ok := workerExecutionFunctionResultTypeAt(function.Results, resultIndex)
	if !ok {
		return "", "", false
	}
	return workerExecutionDeclaredReceiverType(owner, owner.directory, resultType)
}

// 解析显式 func 类型或当前模块内唯一的命名函数类型。
func (closure *workerExecutionGoClosure) workerExecutionFunctionType(unit *workerExecutionGoUnit, expression ast.Expr) (*workerExecutionGoUnit, *ast.FuncType) {
	switch value := expression.(type) {
	case *ast.FuncType:
		return unit, value
	case *ast.Ident:
		return closure.workerExecutionNamedFunctionType(unit.directory, value.Name)
	case *ast.SelectorExpr:
		identifier, ok := value.X.(*ast.Ident)
		if !ok {
			return nil, nil
		}
		imported, ok := unit.imports[identifier.Name]
		if !ok || !imported.local {
			return nil, nil
		}
		return closure.workerExecutionNamedFunctionType(imported.directory, value.Sel.Name)
	case *ast.ParenExpr:
		return closure.workerExecutionFunctionType(unit, value.X)
	default:
		return nil, nil
	}
}

// 从当前模块内唯一类型声明读取命名函数类型。
func (closure *workerExecutionGoClosure) workerExecutionNamedFunctionType(directory, name string) (*workerExecutionGoUnit, *ast.FuncType) {
	candidates := closure.index.symbols[directory][name]
	if len(candidates) != 1 {
		return nil, nil
	}
	typeSpec, ok := candidates[0].node.(*ast.TypeSpec)
	if !ok {
		return nil, nil
	}
	function, ok := typeSpec.Type.(*ast.FuncType)
	if !ok {
		return nil, nil
	}
	return candidates[0], function
}
