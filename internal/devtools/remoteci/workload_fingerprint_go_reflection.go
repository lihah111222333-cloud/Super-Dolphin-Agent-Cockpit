package remoteci

import "go/ast"

// remoteGoTestUsesUnresolvedReflection 仅在选中声明实际调用无法静态解析的反射操作时回退整树。
func remoteGoTestUsesUnresolvedReflection(declaration ast.Decl, file *ast.File, packageReflectValues map[string]struct{}) bool {
	reflectNames := remoteGoTestReflectImportNames(file)
	if len(reflectNames) == 0 && len(packageReflectValues) == 0 {
		return false
	}
	reflectValues := make(map[string]struct{}, len(packageReflectValues))
	for name := range packageReflectValues {
		reflectValues[name] = struct{}{}
	}
	return remoteGoTestReflectScanDeclaration(declaration, reflectNames, reflectValues)
}

// remoteGoTestReflectImportNames 返回当前测试文件中 reflect 包的显式别名。
func remoteGoTestReflectImportNames(file *ast.File) map[string]struct{} {
	reflectNames := make(map[string]struct{})
	for name, importPath := range remoteGoTestImports(file) {
		if importPath == "reflect" {
			reflectNames[name] = struct{}{}
		}
	}
	return reflectNames
}

// remoteGoTestReflectScanDeclaration 扫描声明内的简单别名和动态调用。
func remoteGoTestReflectScanDeclaration(declaration ast.Decl, reflectNames, reflectValues map[string]struct{}) bool {
	usesUnresolvedReflection := false
	ast.Inspect(declaration, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.ValueSpec:
			remoteGoTestReflectValueSpecAliases(value, reflectNames, reflectValues)
		case *ast.AssignStmt:
			remoteGoTestReflectAssignAliases(value, reflectNames, reflectValues)
		case *ast.CallExpr:
			usesUnresolvedReflection = remoteGoTestReflectDynamicCall(value, reflectNames, reflectValues)
		}
		return !usesUnresolvedReflection
	})
	return usesUnresolvedReflection
}

// remoteGoTestReflectValueSpecAliases 收集 var 声明中的简单反射值别名。
func remoteGoTestReflectValueSpecAliases(value *ast.ValueSpec, reflectNames, reflectValues map[string]struct{}) {
	for index, name := range value.Names {
		if index < len(value.Values) && remoteGoTestReflectValueExpression(value.Values[index], reflectNames) {
			reflectValues[name.Name] = struct{}{}
		}
	}
}

// remoteGoTestReflectAssignAliases 收集局部短声明中的简单反射值别名。
func remoteGoTestReflectAssignAliases(value *ast.AssignStmt, reflectNames, reflectValues map[string]struct{}) {
	for index, name := range value.Lhs {
		identifier, ok := name.(*ast.Ident)
		if !ok || index >= len(value.Rhs) {
			continue
		}
		if remoteGoTestReflectValueExpression(value.Rhs[index], reflectNames) {
			reflectValues[identifier.Name] = struct{}{}
		}
	}
}

// remoteGoTestPackageReflectValueNames 收集包级变量直接持有的 reflect.Value/Type。
func remoteGoTestPackageReflectValueNames(files []remoteGoTestFile) map[string]struct{} {
	values := make(map[string]struct{})
	for _, file := range files {
		for name := range remoteGoTestPackageReflectValueFile(file) {
			values[name] = struct{}{}
		}
	}
	return values
}

// remoteGoTestPackageReflectValueFile 收集单个测试文件的包级反射值别名。
func remoteGoTestPackageReflectValueFile(file remoteGoTestFile) map[string]struct{} {
	reflectNames := remoteGoTestReflectImportNames(file.file)
	if len(reflectNames) == 0 {
		return nil
	}
	values := make(map[string]struct{})
	for _, declaration := range file.file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok {
			continue
		}
		for name := range remoteGoTestPackageReflectValueDeclaration(general, reflectNames) {
			values[name] = struct{}{}
		}
	}
	return values
}

// remoteGoTestPackageReflectValueDeclaration 收集 var 声明中直接构造的反射值。
func remoteGoTestPackageReflectValueDeclaration(declaration *ast.GenDecl, reflectNames map[string]struct{}) map[string]struct{} {
	values := make(map[string]struct{})
	for _, spec := range declaration.Specs {
		value, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		remoteGoTestReflectValueSpecAliases(value, reflectNames, values)
	}
	return values
}

// remoteGoTestReflectDynamicCall 识别可能执行未知函数或访问未知字段的反射调用链。
func remoteGoTestReflectDynamicCall(call *ast.CallExpr, reflectNames, reflectValues map[string]struct{}) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	switch selector.Sel.Name {
	case "Call", "CallSlice", "MethodByName", "FieldByName", "FieldByIndex":
		return remoteGoTestReflectExpression(selector.X, reflectNames, reflectValues)
	default:
		return false
	}
}

// remoteGoTestReflectExpression 判断表达式是否根于 reflect 包或已知反射值。
func remoteGoTestReflectExpression(expression ast.Expr, reflectNames, reflectValues map[string]struct{}) bool {
	switch value := expression.(type) {
	case *ast.Ident:
		_, reflectImport := reflectNames[value.Name]
		_, reflectValue := reflectValues[value.Name]
		return reflectImport || reflectValue
	case *ast.SelectorExpr:
		return remoteGoTestReflectExpression(value.X, reflectNames, reflectValues)
	case *ast.CallExpr:
		return remoteGoTestReflectExpression(value.Fun, reflectNames, reflectValues)
	case *ast.ParenExpr:
		return remoteGoTestReflectExpression(value.X, reflectNames, reflectValues)
	default:
		return false
	}
}

// remoteGoTestReflectValueExpression 判断表达式是否直接构造 reflect.Value/Type。
func remoteGoTestReflectValueExpression(expression ast.Expr, reflectNames map[string]struct{}) bool {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	switch selector.Sel.Name {
	case "ValueOf", "TypeOf", "New", "NewAt", "Indirect":
	default:
		return false
	}
	identifier, ok := selector.X.(*ast.Ident)
	if !ok {
		return false
	}
	_, ok = reflectNames[identifier.Name]
	return ok
}
