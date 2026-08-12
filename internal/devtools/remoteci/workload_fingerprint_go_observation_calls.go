package remoteci

import "go/ast"

// localGoProductionReceiverCall 只递归当前包同名 receiver 方法。仅凭方法名不能
// 在多个导入包之间证明 receiver 类型；此时由独立编译闭包绑定全部候选实现，
// 不得把任意同名方法的运行时观察误扩成整棵仓库。
func (snapshot *remoteGitTreeSnapshot) localGoProductionReceiverCall(directory string, imports map[string]string, index remoteGoProductionIndex, method string) (remoteGoTestScope, []remoteGoProductionCall, error) {
	declarations := append([]remoteGoProductionDeclaration(nil), index.byPackage[directory][method]...)
	if len(declarations) == 0 {
		return remoteGoTestScopeCompileClosure, nil, nil
	}
	return remoteGoProductionCalls(declarations)
}

func remoteGoProductionCalls(declarations []remoteGoProductionDeclaration) (remoteGoTestScope, []remoteGoProductionCall, error) {
	targets := make([]remoteGoProductionCall, 0, len(declarations))
	for _, declaration := range declarations {
		targets = append(targets, remoteGoProductionCall{directory: declaration.directory, filePath: declaration.filePath, file: declaration.file, decl: declaration.decl})
	}
	return remoteGoTestScopeSelector, targets, nil
}

// remoteGoRangeFunctionValueCalls 解析声明内显式函数切片 range 的回调闭包。
func remoteGoRangeFunctionValueCalls(observed ast.Node, directory string, index remoteGoProductionIndex, name string) ([]remoteGoProductionCall, bool) {
	var targets []remoteGoProductionCall
	resolved := false
	ast.Inspect(observed, func(node ast.Node) bool {
		rangeStatement, ok := node.(*ast.RangeStmt)
		if !ok || !remoteGoRangeBindsName(rangeStatement, name) {
			return true
		}
		resolved = true
		values, exact := remoteGoExplicitFunctionValues(rangeStatement.X, directory, index)
		if !exact {
			resolved = false
			return false
		}
		targets = append(targets, values...)
		return false
	})
	return targets, resolved
}

// remoteGoRangeBindsName 判断 range 的键或值是否绑定目标局部名。
func remoteGoRangeBindsName(statement *ast.RangeStmt, name string) bool {
	for _, expression := range []ast.Expr{statement.Key, statement.Value} {
		if identifier, ok := expression.(*ast.Ident); ok && identifier.Name == name {
			return true
		}
	}
	return false
}

// remoteGoExplicitFunctionValues 仅解析显式函数切片中的本地函数或内联函数。
func remoteGoExplicitFunctionValues(expression ast.Expr, directory string, index remoteGoProductionIndex) ([]remoteGoProductionCall, bool) {
	literal, ok := expression.(*ast.CompositeLit)
	if !ok {
		return nil, false
	}
	var targets []remoteGoProductionCall
	for _, element := range literal.Elts {
		if _, ok := element.(*ast.FuncLit); ok {
			continue
		}
		identifier, ok := element.(*ast.Ident)
		if !ok {
			return nil, false
		}
		declarations := remoteGoPackageFunctions(index.byPackage[directory][identifier.Name])
		if len(declarations) == 0 {
			return nil, false
		}
		_, calls, _ := remoteGoProductionCalls(declarations)
		targets = append(targets, calls...)
	}
	return targets, true
}

// remoteGoPackageFunctions 过滤同名类型与 receiver 方法，只保留包函数。
func remoteGoPackageFunctions(declarations []remoteGoProductionDeclaration) []remoteGoProductionDeclaration {
	functions := make([]remoteGoProductionDeclaration, 0, len(declarations))
	for _, declaration := range declarations {
		if function, ok := declaration.decl.(*ast.FuncDecl); ok && function.Recv == nil {
			functions = append(functions, declaration)
		}
	}
	return functions
}

// remoteGoTestSelector 解开泛型调用并解析包选择器。
func remoteGoTestSelector(call *ast.CallExpr, imports map[string]string) (string, string, bool) {
	selector, ok := remoteGoCalledSelector(call.Fun)
	if !ok {
		return "", "", false
	}
	packageName, ok := remoteGoSelectorRootIdentifier(selector.X)
	if !ok {
		return "", "", false
	}
	return imports[packageName.Name], selector.Sel.Name, true
}

// remoteGoSelectorRootIdentifier 取得链式 selector/call 的最左标识符。
func remoteGoSelectorRootIdentifier(expression ast.Expr) (*ast.Ident, bool) {
	for {
		switch value := expression.(type) {
		case *ast.Ident:
			return value, true
		case *ast.SelectorExpr:
			expression = value.X
		case *ast.CallExpr:
			expression = value.Fun
		case *ast.IndexExpr:
			expression = value.X
		case *ast.IndexListExpr:
			expression = value.X
		case *ast.ParenExpr:
			expression = value.X
		case *ast.CompositeLit:
			expression = value.Type
		default:
			return nil, false
		}
	}
}

// remoteGoCalledIdentifier 解开泛型与括号包装并返回本地函数标识符。
func remoteGoCalledIdentifier(expression ast.Expr) (*ast.Ident, bool) {
	for {
		switch value := expression.(type) {
		case *ast.Ident:
			return value, true
		case *ast.IndexExpr:
			expression = value.X
		case *ast.IndexListExpr:
			expression = value.X
		case *ast.ParenExpr:
			expression = value.X
		default:
			return nil, false
		}
	}
}

// remoteGoChainedSelectorCall 区分包函数与包导出值上的 receiver 方法。
func remoteGoChainedSelectorCall(expression ast.Expr) bool {
	selector, ok := remoteGoCalledSelector(expression)
	if !ok {
		return false
	}
	_, direct := selector.X.(*ast.Ident)
	return !direct
}

func remoteGoMissingImportedProductionCallScope(expression ast.Expr) remoteGoTestScope {
	if remoteGoChainedSelectorCall(expression) {
		return remoteGoTestScopeSelector
	}
	return remoteGoTestScopeCompileClosure
}

// remoteGoCalledSelector 解开泛型与括号包装并返回实际选择器。
func remoteGoCalledSelector(expression ast.Expr) (*ast.SelectorExpr, bool) {
	for {
		switch value := expression.(type) {
		case *ast.IndexExpr:
			expression = value.X
		case *ast.IndexListExpr:
			expression = value.X
		case *ast.ParenExpr:
			expression = value.X
		case *ast.SelectorExpr:
			return value, true
		default:
			return nil, false
		}
	}
}

// remoteGoTestProcessOrCWDObservation 判断会逃逸静态文件闭包的进程或工作目录调用。
func remoteGoTestProcessOrCWDObservation(importPath, method string) bool {
	switch importPath {
	case "os/exec", "syscall", "golang.org/x/sys/unix":
		return true
	case "os":
		return remoteGoWholeTreeOSMethod(method)
	default:
		return false
	}
}

// remoteGoTestObservationAlias 识别被赋值为受观察函数的包级或函数级别名。
func remoteGoTestObservationAlias(call *ast.CallExpr, file *ast.File, imports map[string]string) bool {
	identifier, ok := call.Fun.(*ast.Ident)
	if !ok {
		return false
	}
	matched := false
	ast.Inspect(file, func(node ast.Node) bool {
		if matched {
			return false
		}
		switch value := node.(type) {
		case *ast.ValueSpec:
			matched = remoteGoTestObservationAliasValueSpec(identifier.Name, value, imports)
		case *ast.AssignStmt:
			matched = remoteGoTestObservationAliasValue(identifier.Name, value.Lhs, value.Rhs, imports)
		}
		return !matched
	})
	return matched
}

// remoteGoTestObservationAliasValueSpec 将变量声明统一为表达式列表后判断函数别名。
func remoteGoTestObservationAliasValueSpec(name string, value *ast.ValueSpec, imports map[string]string) bool {
	names := make([]ast.Expr, len(value.Names))
	for index, identifier := range value.Names {
		names[index] = identifier
	}
	return remoteGoTestObservationAliasValue(name, names, value.Values, imports)
}

// remoteGoTestObservationAliasValue 判断同位置变量是否接收受观察的函数值。
func remoteGoTestObservationAliasValue(name string, names, values []ast.Expr, imports map[string]string) bool {
	if len(names) != len(values) {
		return false
	}
	for index, candidate := range names {
		identifier, ok := candidate.(*ast.Ident)
		if !ok || identifier.Name != name {
			continue
		}
		selector, ok := values[index].(*ast.SelectorExpr)
		if !ok {
			return false
		}
		packageName, ok := selector.X.(*ast.Ident)
		if !ok {
			return false
		}
		importPath := imports[packageName.Name]
		_, observesPath := remoteGoTestObservationPathIndex(importPath, selector.Sel.Name)
		return observesPath || remoteGoTestProcessOrCWDObservation(importPath, selector.Sel.Name)
	}
	return false
}
