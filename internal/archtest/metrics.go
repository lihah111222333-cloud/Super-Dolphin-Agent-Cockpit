package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
)

// MeasureFileMetrics 对单个 Go 文件执行全量 AST 指标测量，返回可用于棘轮比较的 FileMetrics。
// path 必须是可 os.ReadFile 的绝对或相对路径。
func MeasureFileMetrics(path string) FileMetrics {
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
	return measureFileMetricsFromAST(rawLines, fset, node)
}

// measureFileMetricsFromAST 在已解析的 AST 上执行全量指标采集。
func measureFileMetricsFromAST(rawLines []string, fset *token.FileSet, node *ast.File) FileMetrics {
	var m FileMetrics

	// 代码生成文件豁免：如果包含标准的 "Code generated ... DO NOT EDIT." 注释，直接当做无违规处理。
	if ast.IsGenerated(node) {
		return m
	}

	m.Lines = countEffectiveLinesFromRaw(rawLines)
	m.GlobalVars = countGlobalVarsV3(node)
	m.HasInit = hasInitFunc(node)
	m.PanicCount = countPanicCalls(node)
	m.TodoCount = countTodoComments(node)
	m.MaxStructFields = measureMaxStructFields(node)
	for _, decl := range node.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil || fd.Name == nil {
			continue
		}
		measureFuncMetrics(rawLines, fset, fd, &m)
	}
	m.MaxUnderscore = measureMaxUnderscore(node)
	return m
}

// measureFuncMetrics 采集单个函数的指标并更新 FileMetrics 中的 max 值。
func measureFuncMetrics(rawLines []string, fset *token.FileSet, fd *ast.FuncDecl, m *FileMetrics) {
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
		m.NakedReturns += countNakedReturnsInFunc(fd.Body)
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
//   - regexp.MustCompile / event.New 不可变全局
func countGlobalVarsV3(node *ast.File) int {
	count := 0
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
			for _, name := range vs.Names {
				if name.Name == "_" {
					continue
				}
				if isExemptGlobalVar(name.Name, vs) {
					continue
				}
				count++
			}
		}
	}
	return count
}

// isExemptGlobalVar 判断包级 var 是否属于 V3 豁免模式。
func isExemptGlobalVar(name string, vs *ast.ValueSpec) bool {
	// 哨兵错误: ErrExample or errExample
	if strings.HasPrefix(name, "Err") || strings.HasPrefix(name, "err") {
		return true
	}
	// fx.Module 声明: var Module = fx.Module(...)
	if name == "Module" {
		return true
	}
	// 接口合规检查: var _ Interface = (*)
	if name == "_" {
		return true
	}
	// 检查右值是否为不可变全局构造
	if len(vs.Values) > 0 {
		if isImmutableGlobalInit(vs.Values[0]) {
			return true
		}
	}
	// 无右值时检查类型：embed.FS 和 atomic.* 零值声明
	return len(vs.Values) == 0 && isExemptZeroValueType(vs.Type)
}

// isExemptZeroValueType 检查零值声明的类型是否属于豁免类型（embed.FS、atomic.*）。
func isExemptZeroValueType(typeExpr ast.Expr) bool {
	switch t := typeExpr.(type) {
	case *ast.SelectorExpr:
		if t.Sel.Name == "FS" {
			return true
		}
		pkg, ok := t.X.(*ast.Ident)
		return ok && pkg.Name == "atomic"
	case *ast.IndexExpr:
		// atomic.Pointer[T] — generic atomic type
		sel, ok := t.X.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		pkg, ok := sel.X.(*ast.Ident)
		return ok && pkg.Name == "atomic"
	}
	return false
}

// isImmutableGlobalInit 检查初始化表达式是否为不可变全局构造。
// 覆盖：函数调用类（regexp.MustCompile, promauto.NewCounter 等）、复合字面量（slice/map/struct literal）、
// 函数字面量（no-op 占位符，如 test hook 默认值）、flag 包注册、常量乘法表达式（如 5 * time.Minute）。
func isImmutableGlobalInit(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.CompositeLit:
		return true
	case *ast.FuncLit:
		return true
	case *ast.CallExpr:
		return isImmutableFuncCall(e)
	case *ast.UnaryExpr:
		if _, ok := e.X.(*ast.CompositeLit); ok {
			return true
		}
		return false
	case *ast.BinaryExpr:
		return isConstantExpr(e)
	default:
		return false
	}
}

// isConstantExpr 递归检查表达式是否为纯常量（字面量、包选择器或其乘积），
// 用于豁免如 5 * time.Minute 这类 const-like 全局变量。
func isConstantExpr(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.BasicLit:
		return true
	case *ast.Ident:
		return true
	case *ast.SelectorExpr:
		return true
	case *ast.BinaryExpr:
		return isConstantExpr(e.X) && isConstantExpr(e.Y)
	default:
		return false
	}
}

func isImmutableFuncCall(call *ast.CallExpr) bool {
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr:
		return isImmutableSelectorCall(fn)
	case *ast.Ident:
		return strings.HasPrefix(fn.Name, "New") || strings.HasPrefix(fn.Name, "Must")
	}
	return false
}

func isImmutableSelectorCall(sel *ast.SelectorExpr) bool {
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	name := sel.Sel.Name
	switch pkg.Name {
	case "fx":
		return name == "Module"
	case "sync":
		return name == "OnceValue"
	case "flag":
		return true
	}
	return strings.HasPrefix(name, "New") || strings.HasPrefix(name, "Must")
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
func countPanicCalls(node *ast.File) int {
	count := 0
	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "panic" {
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
func countNakedReturnsInFunc(body *ast.BlockStmt) int {
	count := 0
	ast.Inspect(body, func(n ast.Node) bool {
		ret, ok := n.(*ast.ReturnStmt)
		if ok && len(ret.Results) == 0 {
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
