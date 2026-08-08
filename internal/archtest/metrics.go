package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"strconv"
	"strings"
)

// MeasureFileMetrics 对单个 Go 文件执行全量 AST 指标测量，返回可用于棘轮比较的 FileMetrics。
// path 必须是可 os.ReadFile 的绝对或相对路径。
func MeasureFileMetrics(path string) FileMetrics {
	return measureFileMetricsFromPath(path, false)
}

// measureBaselineFileMetrics 读取一次文件并补齐 baseline 专用的裸 goroutine 指标。
func measureBaselineFileMetrics(path string) FileMetrics {
	return measureFileMetricsFromPath(path, true)
}

func measureFileMetricsFromPath(path string, includeNakedGoroutines bool) FileMetrics {
	data, err := os.ReadFile(path)
	if err != nil {
		return FileMetrics{}
	}
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, path, data, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return FileMetrics{}
	}
	rawLines := SplitLines(data)
	return measureFileMetricsFromASTWithOptions(rawLines, fset, node, includeNakedGoroutines)
}

// measureFileMetricsFromAST 在已解析的 AST 上执行全量指标采集。
func measureFileMetricsFromAST(rawLines []string, fset *token.FileSet, node *ast.File) FileMetrics {
	return measureFileMetricsFromASTWithOptions(rawLines, fset, node, false)
}

// measureFileMetricsFromASTWithOptions 统一采集指标，并按调用方需要补齐 baseline 质量指标。
func measureFileMetricsFromASTWithOptions(rawLines []string, fset *token.FileSet, node *ast.File, includeNakedGoroutines bool) FileMetrics {
	var m FileMetrics
	ignores := collectArchGuardIgnores(fset, node)

	// 代码生成文件豁免：如果包含标准的 "Code generated ... DO NOT EDIT." 注释，直接当做无违规处理。
	if ast.IsGenerated(node) {
		return m
	}

	m.Lines = countEffectiveLinesFromRaw(rawLines)
	m.GlobalVars = countGlobalVarsV3(fset, node, ignores)
	m.HasInit = hasInitFunc(node)
	m.PanicCount = countPanicCalls(fset, node, ignores)
	m.TodoCount = countTodoComments(node)
	m.MaxStructFields = measureMaxStructFields(node)
	for _, decl := range node.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil || fd.Name == nil {
			continue
		}
		measureFuncMetrics(rawLines, fset, fd, ignores, &m)
	}
	m.MaxUnderscore = measureMaxUnderscore(node)
	m.RawGoroutines = countRawGoroutines(node)
	if includeNakedGoroutines {
		m.NakedGoroutines = CountNakedGoStmts(node)
	}
	m.MissingDocs = countMissingDocComments(node)
	m.MaxStructMethods = measureMaxStructMethods(node)
	return m
}

// measureFuncMetrics 采集单个函数的指标并更新 FileMetrics 中的 max 值。
func measureFuncMetrics(rawLines []string, fset *token.FileSet, fd *ast.FuncDecl, ignores archGuardIgnores, m *FileMetrics) {
	startLine := fset.Position(fd.Pos()).Line
	endLine := fset.Position(fd.End()).Line
	if funcLen := EffectiveLinesInRange(rawLines, startLine, endLine); funcLen > m.MaxFuncLen {
		m.MaxFuncLen = funcLen
	}
	if depth := MeasureMaxNesting(fd.Body, 0); depth > m.MaxNesting {
		m.MaxNesting = depth
	}
	if cc := MeasureCyclomaticComplexity(fd); cc > m.MaxComplexity {
		m.MaxComplexity = cc
	}
	if p := countFuncParams(fd); p > m.MaxParams {
		m.MaxParams = p
	}
	if r := countFuncReturns(fd); r > m.MaxReturns {
		m.MaxReturns = r
	}
	if hasNamedResults(fd) {
		m.NakedReturns += countNakedReturnsInFunc(fset, fd.Body, ignores)
	}
	if len(fd.Body.List) == 0 && fd.Name.Name != "init" {
		m.EmptyFuncs++
	}
}

// countEffectiveLinesFromRaw 计算有效代码行数（排除空行和注释）。
func countEffectiveLinesFromRaw(rawLines []string) int {
	return EffectiveLinesInRange(rawLines, 1, -1)
}

// countGlobalVarsV3 计算包级 var 数量，豁免 V3 特有模式：
//   - var Module = fx.Module(...)
//   - var _ Interface = (*impl)(nil) 接口合规检查
//   - var ErrExample / var errExample 哨兵错误
//   - regexp.MustCompile 已证明只读的查找表
func countGlobalVarsV3(fset *token.FileSet, node *ast.File, ignores archGuardIgnores) int {
	count := 0
	immutableContext := newImmutableGlobalContext(node)
	for _, decl := range node.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			count += countGlobalValueSpec(fset, vs, ignores, immutableContext)
		}
	}
	return count
}

// countGlobalValueSpec 按名称精确匹配 RHS，并对无法证明的共享或错位初始化保守计数。
func countGlobalValueSpec(
	fset *token.FileSet,
	vs *ast.ValueSpec,
	ignores archGuardIgnores,
	immutableContext immutableGlobalContext,
) int {
	count := 0
	for nameIndex, name := range vs.Names {
		if name.Name == "_" || ignores.has(fset.Position(name.Pos()).Line, "global_vars") {
			continue
		}
		initializer, hasInitializer := globalInitializerForName(vs, nameIndex)
		if len(vs.Values) > 0 && !hasInitializer {
			count++
			continue
		}
		if !isExemptGlobalVar(name.Name, vs.Type, initializer, hasInitializer, immutableContext) {
			count++
		}
	}
	return count
}

type immutableGlobalContext struct {
	types     map[string]ast.Expr
	constants map[string]struct{}
	imports   map[string]string
}

// newImmutableGlobalContext 建立当前文件可证明的本地类型和常量索引。
func newImmutableGlobalContext(node *ast.File) immutableGlobalContext {
	context := immutableGlobalContext{
		types:     make(map[string]ast.Expr),
		constants: make(map[string]struct{}),
		imports:   make(map[string]string),
	}
	indexImmutableImports(node.Imports, context.imports)
	indexImmutableDeclarations(node.Decls, context)
	return context
}

// indexImmutableImports 记录当前文件的显式 import 名称和真实路径。
func indexImmutableImports(imports []*ast.ImportSpec, indexed map[string]string) {
	for _, importSpec := range imports {
		importPath, err := strconv.Unquote(importSpec.Path.Value)
		if err != nil {
			continue
		}
		localName := path.Base(importPath)
		if importSpec.Name != nil {
			localName = importSpec.Name.Name
		}
		if localName != "_" && localName != "." {
			indexed[localName] = importPath
		}
	}
}

// indexImmutableDeclarations 记录当前文件的本地类型和常量声明。
func indexImmutableDeclarations(declarations []ast.Decl, context immutableGlobalContext) {
	for _, declaration := range declarations {
		general, ok := declaration.(*ast.GenDecl)
		if !ok {
			continue
		}
		switch general.Tok {
		case token.TYPE:
			indexImmutableTypes(general.Specs, context.types)
		case token.CONST:
			indexImmutableConstants(general.Specs, context.constants)
		}
	}
}

// indexImmutableTypes 记录类型声明的本地底层表达式。
func indexImmutableTypes(specifications []ast.Spec, indexed map[string]ast.Expr) {
	for _, specification := range specifications {
		typeSpec, ok := specification.(*ast.TypeSpec)
		if ok {
			indexed[typeSpec.Name.Name] = typeSpec.Type
		}
	}
}

// indexImmutableConstants 记录当前文件中可用于纯值表达式的常量名称。
func indexImmutableConstants(specifications []ast.Spec, indexed map[string]struct{}) {
	for _, specification := range specifications {
		valueSpec, ok := specification.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for _, name := range valueSpec.Names {
			indexed[name.Name] = struct{}{}
		}
	}
}

// globalInitializerForName 只返回与名称一一对应的初始化表达式。
func globalInitializerForName(vs *ast.ValueSpec, nameIndex int) (ast.Expr, bool) {
	if len(vs.Values) == 0 || len(vs.Values) != len(vs.Names) || nameIndex >= len(vs.Values) {
		return nil, false
	}
	return vs.Values[nameIndex], true
}

// isExemptGlobalVar 判断包级 var 是否属于全局变量 guard 的允许形态。
// 只豁免哨兵错误、fx.Module、接口合规检查和不可变初始化，避免状态型全局变量溜过。
func isExemptGlobalVar(
	name string,
	typeExpr ast.Expr,
	initializer ast.Expr,
	hasInitializer bool,
	context immutableGlobalContext,
) bool {
	// 哨兵错误: ErrExample or errExample
	if strings.HasPrefix(name, "Err") || strings.HasPrefix(name, "err") {
		return true
	}
	if hasInitializer {
		return isImmutableGlobalInit(initializer, context)
	}
	return isExemptZeroValueType(typeExpr, context)
}

// isExemptZeroValueType 检查零值声明是否为只读嵌入文件系统。
func isExemptZeroValueType(typeExpr ast.Expr, context immutableGlobalContext) bool {
	selector, ok := typeExpr.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "FS" {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	return ok && context.imports[packageName.Name] == "embed"
}

// isImmutableGlobalInit 检查初始化表达式是否为不可变全局构造。
// 仅放行可证明的纯值数组、纯标量结构和显式登记的只读构造。
func isImmutableGlobalInit(expr ast.Expr, context immutableGlobalContext) bool {
	switch e := expr.(type) {
	case *ast.CompositeLit:
		return isImmutableCompositeLiteral(e, nil, context, make(map[string]bool))
	case *ast.CallExpr:
		return isImmutableFuncCall(e, context)
	case *ast.BinaryExpr:
		return isConstantExpr(e, context)
	default:
		return false
	}
}

// isImmutableCompositeLiteral 验证复合字面量的类型和每个元素均为纯值。
func isImmutableCompositeLiteral(
	literal *ast.CompositeLit,
	expectedType ast.Expr,
	context immutableGlobalContext,
	visiting map[string]bool,
) bool {
	typeExpr := literal.Type
	if typeExpr == nil {
		typeExpr = expectedType
	}
	switch valueType := typeExpr.(type) {
	case *ast.Ident:
		definition, ok := context.types[valueType.Name]
		if !ok || visiting[valueType.Name] {
			return false
		}
		visiting[valueType.Name] = true
		resolvedLiteral := *literal
		resolvedLiteral.Type = definition
		immutable := isImmutableCompositeLiteral(&resolvedLiteral, nil, context, visiting)
		delete(visiting, valueType.Name)
		return immutable
	case *ast.ArrayType:
		return isImmutableArrayLiteral(literal, valueType, context, visiting)
	case *ast.StructType:
		return isImmutableStructLiteral(literal, valueType, context, visiting)
	default:
		return false
	}
}

// isImmutableArrayLiteral 验证定长数组的键和值均为可证明纯值。
func isImmutableArrayLiteral(
	literal *ast.CompositeLit,
	arrayType *ast.ArrayType,
	context immutableGlobalContext,
	visiting map[string]bool,
) bool {
	if !isImmutableArrayLength(arrayType.Len, context) {
		return false
	}
	for _, element := range literal.Elts {
		value := element
		if keyed, ok := element.(*ast.KeyValueExpr); ok {
			if !isConstantExpr(keyed.Key, context) {
				return false
			}
			value = keyed.Value
		}
		if !isImmutableCompositeValue(value, arrayType.Elt, context, visiting) {
			return false
		}
	}
	return true
}

// isImmutableStructLiteral 验证结构字面量只写入可证明的纯值字段。
func isImmutableStructLiteral(
	literal *ast.CompositeLit,
	structType *ast.StructType,
	context immutableGlobalContext,
	visiting map[string]bool,
) bool {
	fields := flattenImmutableStructFields(structType)
	for elementIndex, element := range literal.Elts {
		fieldIndex := elementIndex
		value := element
		if keyed, ok := element.(*ast.KeyValueExpr); ok {
			fieldName, ok := keyed.Key.(*ast.Ident)
			if !ok {
				return false
			}
			fieldIndex = immutableStructFieldIndex(fields, fieldName.Name)
			value = keyed.Value
		}
		if fieldIndex < 0 || fieldIndex >= len(fields) {
			return false
		}
		if !isImmutableValueType(fields[fieldIndex].typeExpr, context, visiting) ||
			!isImmutableCompositeValue(value, fields[fieldIndex].typeExpr, context, visiting) {
			return false
		}
	}
	for _, field := range fields {
		if !isImmutableValueType(field.typeExpr, context, visiting) {
			return false
		}
	}
	return true
}

type immutableStructField struct {
	name     string
	typeExpr ast.Expr
}

// flattenImmutableStructFields 展开结构中的合并字段声明。
func flattenImmutableStructFields(structType *ast.StructType) []immutableStructField {
	if structType.Fields == nil {
		return nil
	}
	var fields []immutableStructField
	for _, field := range structType.Fields.List {
		if len(field.Names) == 0 {
			fields = append(fields, immutableStructField{typeExpr: field.Type})
			continue
		}
		for _, name := range field.Names {
			fields = append(fields, immutableStructField{name: name.Name, typeExpr: field.Type})
		}
	}
	return fields
}

// immutableStructFieldIndex 查找具名结构字段的位置。
func immutableStructFieldIndex(fields []immutableStructField, name string) int {
	for index, field := range fields {
		if field.name == name {
			return index
		}
	}
	return -1
}

// isImmutableCompositeValue 验证复合字面量中的单个值。
func isImmutableCompositeValue(
	expr ast.Expr,
	expectedType ast.Expr,
	context immutableGlobalContext,
	visiting map[string]bool,
) bool {
	switch value := expr.(type) {
	case *ast.CompositeLit:
		return isImmutableCompositeLiteral(value, expectedType, context, visiting)
	case *ast.BasicLit:
		return true
	case *ast.Ident:
		return isConstantIdentifier(value.Name, context)
	case *ast.ParenExpr:
		return isImmutableCompositeValue(value.X, expectedType, context, visiting)
	case *ast.UnaryExpr, *ast.BinaryExpr:
		return isConstantExpr(value, context)
	default:
		return false
	}
}

// isImmutableValueType 判断类型是否只包含标量、定长数组或纯值结构。
func isImmutableValueType(typeExpr ast.Expr, context immutableGlobalContext, visiting map[string]bool) bool {
	switch valueType := typeExpr.(type) {
	case *ast.Ident:
		if isBuiltinScalarType(valueType.Name) {
			return true
		}
		definition, ok := context.types[valueType.Name]
		if !ok || visiting[valueType.Name] {
			return false
		}
		visiting[valueType.Name] = true
		immutable := isImmutableValueType(definition, context, visiting)
		delete(visiting, valueType.Name)
		return immutable
	case *ast.ArrayType:
		return isImmutableArrayLength(valueType.Len, context) &&
			isImmutableValueType(valueType.Elt, context, visiting)
	case *ast.StructType:
		for _, field := range flattenImmutableStructFields(valueType) {
			if !isImmutableValueType(field.typeExpr, context, visiting) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// isImmutableArrayLength 判断数组长度是否为省略号或当前文件可证明的常量。
func isImmutableArrayLength(length ast.Expr, context immutableGlobalContext) bool {
	if _, ok := length.(*ast.Ellipsis); ok {
		return true
	}
	return length != nil && isConstantExpr(length, context)
}

// isBuiltinScalarType 判断标识符是否为 Go 内建标量类型。
func isBuiltinScalarType(name string) bool {
	switch name {
	case "bool", "string",
		"int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64", "uintptr",
		"byte", "rune", "float32", "float64", "complex64", "complex128":
		return true
	default:
		return false
	}
}

// isConstantExpr 递归检查表达式是否由字面量、本地常量和常量运算组成。
func isConstantExpr(expr ast.Expr, context immutableGlobalContext) bool {
	switch e := expr.(type) {
	case *ast.BasicLit:
		return true
	case *ast.Ident:
		return isConstantIdentifier(e.Name, context)
	case *ast.ParenExpr:
		return isConstantExpr(e.X, context)
	case *ast.UnaryExpr:
		return e.Op != token.AND && e.Op != token.ARROW && isConstantExpr(e.X, context)
	case *ast.BinaryExpr:
		return isConstantExpr(e.X, context) && isConstantExpr(e.Y, context)
	default:
		return false
	}
}

// isConstantIdentifier 判断标识符是否为内建布尔值或当前文件常量。
func isConstantIdentifier(name string, context immutableGlobalContext) bool {
	if name == "true" || name == "false" || name == "iota" {
		return true
	}
	_, ok := context.constants[name]
	return ok
}

// isImmutableFuncCall 仅放行已明确登记为只读声明的构造调用。
func isImmutableFuncCall(call *ast.CallExpr, context immutableGlobalContext) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	if !ok {
		return false
	}
	switch context.imports[packageName.Name] {
	case "go.uber.org/fx":
		return selector.Sel.Name == "Module"
	case "regexp":
		return selector.Sel.Name == "MustCompile"
	default:
		return false
	}
}

// hasInitFunc 检查文件是否包含 init() 函数。
func hasInitFunc(node *ast.File) bool {
	for _, decl := range node.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if ok && fd.Name.Name == "init" && fd.Recv == nil {
			return true
		}
	}
	return false
}

// countPanicCalls 计算文件中 panic() 调用次数。
func countPanicCalls(fset *token.FileSet, node *ast.File, ignores archGuardIgnores) int {
	count := 0
	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "panic" {
			if ignores.has(fset.Position(call.Pos()).Line, "panic_count") {
				return true
			}
			count++
		}
		return true
	})
	return count
}

// 计算文件中的 blocked-work marker 数量。
func countTodoComments(node *ast.File) int {
	count := 0
	for _, cg := range node.Comments {
		for _, c := range cg.List {
			upper := strings.ToUpper(c.Text)
			if strings.Contains(upper, "TODO") ||
				strings.Contains(upper, "FIXME") ||
				strings.Contains(upper, "HACK") ||
				strings.Contains(upper, "XXX") {
				count++
			}
		}
	}
	return count
}

// measureMaxStructFields 返回文件中最大 struct 字段数。
// 展开 Go AST 的合并声明（如 A, B, C int 算 3 个字段）。
func measureMaxStructFields(node *ast.File) int {
	maxFields := 0
	for _, decl := range node.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			if n := countStructFields(spec); n > maxFields {
				maxFields = n
			}
		}
	}
	return maxFields
}

// countStructFields 计算单个 type spec 的 struct 字段数（展开多名称声明）。
func countStructFields(spec ast.Spec) int {
	ts, ok := spec.(*ast.TypeSpec)
	if !ok {
		return 0
	}
	st, ok := ts.Type.(*ast.StructType)
	if !ok || st.Fields == nil {
		return 0
	}
	count := 0
	for _, f := range st.Fields.List {
		if len(f.Names) == 0 {
			count++ // 嵌入字段
		} else {
			count += len(f.Names)
		}
	}
	return count
}

// countFuncParams 统计函数参数个数（展开同类型合并声明）。
func countFuncParams(fd *ast.FuncDecl) int {
	if fd.Type.Params == nil {
		return 0
	}
	count := 0
	for _, field := range fd.Type.Params.List {
		if len(field.Names) == 0 {
			count++ // 匿名参数
		} else {
			count += len(field.Names)
		}
	}
	return count
}

// countFuncReturns 统计函数返回值个数。
func countFuncReturns(fd *ast.FuncDecl) int {
	if fd.Type.Results == nil {
		return 0
	}
	count := 0
	for _, field := range fd.Type.Results.List {
		if len(field.Names) == 0 {
			count++
		} else {
			count += len(field.Names)
		}
	}
	return count
}

// hasNamedResults 检查函数是否有命名返回值。
func hasNamedResults(fd *ast.FuncDecl) bool {
	if fd.Type.Results == nil {
		return false
	}
	for _, field := range fd.Type.Results.List {
		if len(field.Names) > 0 {
			return true
		}
	}
	return false
}

// countNakedReturnsInFunc 计算函数体内 naked return 语句数量。
func countNakedReturnsInFunc(fset *token.FileSet, body *ast.BlockStmt, ignores archGuardIgnores) int {
	count := 0
	ast.Inspect(body, func(n ast.Node) bool {
		ret, ok := n.(*ast.ReturnStmt)
		if ok && len(ret.Results) == 0 && !ignores.has(fset.Position(ret.Pos()).Line, "naked_returns") {
			count++
		}
		return true
	})
	return count
}

// measureMaxUnderscore 返回文件中标识符含下划线的最大计数。
func measureMaxUnderscore(node *ast.File) int {
	maxU := 0
	ast.Inspect(node, func(n ast.Node) bool {
		ident, ok := n.(*ast.Ident)
		if !ok || ident.Name == "_" {
			return true
		}
		if u := strings.Count(ident.Name, "_"); u > maxU {
			maxU = u
		}
		return true
	})
	return maxU
}

// countRawGoroutines 计算 AST 中所有 go 语句的数量。
func countRawGoroutines(node *ast.File) int {
	count := 0
	ast.Inspect(node, func(n ast.Node) bool {
		if _, ok := n.(*ast.GoStmt); ok {
			count++
		}
		return true
	})
	return count
}

// countMissingDocComments 计算所有导出函数、方法和类型中缺少文档注释的数量。
func countMissingDocComments(node *ast.File) int {
	return countMissingFuncDocs(node) + countMissingTypeDocs(node)
}

// countMissingFuncDocs 遍历 AST 并统计缺少文档注释的导出函数与方法。
func countMissingFuncDocs(node *ast.File) int {
	count := 0
	for _, decl := range node.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if ok && fd.Name != nil && fd.Name.IsExported() {
			if fd.Doc == nil || len(strings.TrimSpace(fd.Doc.Text())) == 0 {
				count++
			}
		}
	}
	return count
}

// countMissingTypeDocs 遍历 AST 并统计缺少文档注释的导出类型与结构体。
func countMissingTypeDocs(node *ast.File) int {
	count := 0
	for _, decl := range node.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if ok && ts.Name != nil && ts.Name.IsExported() {
				if !hasDocComment(ts, gd) {
					count++
				}
			}
		}
	}
	return count
}

// hasDocComment 检查类型定义或其所属声明块是否包含有效的中文/英文文档注释。
func hasDocComment(ts *ast.TypeSpec, gd *ast.GenDecl) bool {
	if ts.Doc != nil && len(strings.TrimSpace(ts.Doc.Text())) > 0 {
		return true
	}
	if gd.Doc != nil && len(strings.TrimSpace(gd.Doc.Text())) > 0 {
		return true
	}
	return false
}

// measureMaxStructMethods 计算文件中单个 Receiver 上定义的最大导出方法数。
func measureMaxStructMethods(node *ast.File) int {
	methodsCount := map[string]int{}
	for _, decl := range node.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Recv == nil || fd.Name == nil {
			continue
		}
		if !fd.Name.IsExported() {
			continue
		}
		if typeName, ok := getReceiverTypeName(fd.Recv); ok {
			methodsCount[typeName]++
		}
	}
	maxVal := 0
	for _, count := range methodsCount {
		if count > maxVal {
			maxVal = count
		}
	}
	return maxVal
}

func getReceiverTypeName(recv *ast.FieldList) (string, bool) {
	if recv == nil || len(recv.List) == 0 {
		return "", false
	}
	t := recv.List[0].Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	if ident, ok := t.(*ast.Ident); ok {
		return ident.Name, true
	}
	return "", false
}
