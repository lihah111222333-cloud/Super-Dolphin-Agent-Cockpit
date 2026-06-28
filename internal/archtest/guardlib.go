package archtest

import (
	"bufio"
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	// 2026-04-17 默认守卫放宽：单文件 400→600、包文件数 15→25。
	// 2026-04-22 全仓再放宽：包文件数 25→30；核心包例外失去意义（Core* 常量保留仅为向后兼容，
	// 值与默认等同，不再构成差异）。
	// 函数 ≤80、CC ≤10、嵌套 ≤4、标识符下划线 ≤3 保持不变。
	MaxFileLines            = 600
	MaxCorePackageFileLines = 600
	MaxFactoryFileLines     = 800
	MaxFuncLines            = 80
	MaxNestingDepth         = 4
	MaxUnderscores          = 3
	MaxCCComplexity         = 10
	MaxPackageFiles         = 30
	MaxCorePackageFiles     = 30
	FuncCommentMinLines     = 30
	FuncCommentMinDepth     = 3
	FuncCommentMinCC        = 6
)

type ViolationKind int

const (
	ViolationFile ViolationKind = iota
	ViolationFunc
	ViolationNesting
	ViolationCC
	ViolationIdentifier
	ViolationPackageCount
	ViolationDeadKey
	ViolationFuncComment
)

type Violation struct {
	Kind    ViolationKind
	File    string
	Func    string
	Line    int
	Got     int
	Limit   int
	Message string
}

// String 输出守卫违规的可读文本。
func (v Violation) String() string {
	if v.Message != "" {
		return v.Message
	}
	return fmt.Sprintf("%s:%d %s got=%d limit=%d", v.File, v.Line, v.Func, v.Got, v.Limit)
}

type CheckOptions struct {
	RepoRoot            string
	ScanRoots           []string
	SkipDirs            map[string]bool
	EnforceFuncComments bool
	BaselineTestsOnly   bool
}

type packageStat struct {
	Files        int
	MaxFileLines int
}

// DefaultScanRoots 返回代码守卫默认扫描的源码入口。
func DefaultScanRoots() []string {
	return []string{"internal", "cmd", "pkg", "scripts"}
}

// DefaultSkipDirs 返回扫描时固定跳过的目录。
func DefaultSkipDirs() map[string]bool {
	return map[string]bool{
		".git":         true,
		"node_modules": true,
		"vendor":       true,
	}
}

// CheckAll 按仓库维度运行完整代码守卫。
func CheckAll(opts CheckOptions) []Violation {
	repoRoot := opts.RepoRoot
	if repoRoot == "" {
		repoRoot = "."
	}
	scanRoots := opts.scanRoots()
	stats := map[string]*packageStat{}
	violations := freezeRegistryIntegrityViolations()
	for _, root := range scanRoots {
		violations = append(violations, scanRoot(repoRoot, root, opts.skipDirs(), stats, opts.EnforceFuncComments)...)
	}
	violations = append(violations, packageViolations(stats)...)
	violations = append(violations, postScanViolations(repoRoot, scanRoots, stats)...)
	sortViolations(violations)
	return violations
}

// CheckFiles 只检查传入的 Go 文件，供单文件守卫快速反馈。
func CheckFiles(opts CheckOptions, paths []string) []Violation {
	repoRoot := opts.RepoRoot
	if repoRoot == "" {
		repoRoot = "."
	}
	stats := map[string]*packageStat{}
	var violations []Violation
	for _, path := range paths {
		absPath := path
		if !filepath.IsAbs(absPath) {
			absPath = filepath.Join(repoRoot, path)
		}
		relPath := displayGuardPath(repoRoot, absPath)
		if filepath.Ext(absPath) != ".go" {
			violations = append(violations, Violation{
				Kind:    ViolationFile,
				File:    relPath,
				Message: fmt.Sprintf("%s: expected .go file", relPath),
			})
			continue
		}
		violations = append(violations, checkSingleFile(absPath, relPath, stats, opts.EnforceFuncComments)...)
	}
	sortViolations(violations)
	return violations
}

// displayGuardPath 把绝对路径整理成守卫报告里稳定的仓库相对路径。
func displayGuardPath(repoRoot, absPath string) string {
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return filepath.ToSlash(absPath)
	}
	absFile, err := filepath.Abs(absPath)
	if err != nil {
		return filepath.ToSlash(absPath)
	}
	relPath, err := filepath.Rel(absRoot, absFile)
	if err == nil && relPath != ".." && !strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(relPath)
	}
	return filepath.ToSlash(absFile)
}

func sortViolations(violations []Violation) {
	slices.SortFunc(violations, func(a, b Violation) int {
		if c := strings.Compare(a.File, b.File); c != 0 {
			return c
		}
		if a.Line != b.Line {
			return a.Line - b.Line
		}
		if c := strings.Compare(a.Func, b.Func); c != 0 {
			return c
		}
		if a.Kind != b.Kind {
			return int(a.Kind - b.Kind)
		}
		return strings.Compare(a.Message, b.Message)
	})
}

// SplitLines 把文件内容拆成原始行，保留后续行号计算需要的形态。
func SplitLines(data []byte) []string {
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		out = append(out, sc.Text())
	}
	return out
}

// CountEffectiveLines 统计去掉空行和注释后的有效行数。
func CountEffectiveLines(data []byte) int {
	return EffectiveLinesInRange(SplitLines(data), 1, -1)
}

// EffectiveLinesInRange 统计指定行段内真正参与代码预算的行数。
func EffectiveLinesInRange(rawLines []string, start, end int) int {
	if len(rawLines) == 0 || start > len(rawLines) {
		return 0
	}
	end = clampEffectiveLineEnd(len(rawLines), end)
	count := 0
	inBlock := false
	idx := start - 1
	if idx < 0 {
		idx = 0
	}
	for i := idx; i < end; i++ {
		delta, nextInBlock := effectiveLineDelta(strings.TrimSpace(rawLines[i]), inBlock)
		count += delta
		inBlock = nextInBlock
	}
	return count
}

// MeasureMaxNesting 计算控制流块的最大嵌套深度。
func MeasureMaxNesting(node ast.Node, depth int) int {
	maxDepth := depth
	ast.Inspect(node, func(n ast.Node) bool {
		if n == nil || n == node {
			return true
		}
		switch n.(type) {
		case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
			if got := MeasureMaxNesting(n, depth+1); got > maxDepth {
				maxDepth = got
			}
			return false
		}
		return true
	})
	return maxDepth
}

// MeasureCyclomaticComplexity 计算函数的圈复杂度。
func MeasureCyclomaticComplexity(fd *ast.FuncDecl) int {
	cc := 1
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt:
			cc++
		case *ast.CaseClause:
			if v.List != nil {
				cc++
			}
		case *ast.CommClause:
			if v.Comm != nil {
				cc++
			}
		case *ast.BinaryExpr:
			if v.Op.String() == "&&" || v.Op.String() == "||" {
				cc++
			}
		}
		return true
	})
	return cc
}

// CountNakedGoStmts 计算 AST 中 go func(){...}() 形式的裸 goroutine 数量。
func CountNakedGoStmts(node *ast.File) int {
	count := 0
	ast.Inspect(node, func(n ast.Node) bool {
		goStmt, ok := n.(*ast.GoStmt)
		if !ok {
			return true
		}
		funcLit, isFuncLit := goStmt.Call.Fun.(*ast.FuncLit)
		if isFuncLit && !hasLeadingDeferRecover(funcLit.Body) {
			count++
		}
		return true
	})
	return count
}

// hasLeadingDeferRecover 识别 goroutine 函数字面量首条 defer recover 保护。
func hasLeadingDeferRecover(body *ast.BlockStmt) bool {
	if body == nil || len(body.List) == 0 {
		return false
	}
	deferStmt, ok := body.List[0].(*ast.DeferStmt)
	if !ok {
		return false
	}
	foundRecover := false
	ast.Inspect(deferStmt, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "recover" {
			foundRecover = true
			return false
		}
		return true
	})
	return foundRecover
}

func (o CheckOptions) scanRoots() []string {
	if len(o.ScanRoots) > 0 {
		return o.ScanRoots
	}
	return DefaultScanRoots()
}

func (o CheckOptions) skipDirs() map[string]bool {
	if len(o.SkipDirs) > 0 {
		return o.SkipDirs
	}
	return DefaultSkipDirs()
}

// scanRoot 遍历一个源码根目录，并把文件级结果累加到包统计里。
func scanRoot(repoRoot, root string, skip map[string]bool, stats map[string]*packageStat, enforceFuncComments bool) []Violation {
	absRoot := filepath.Join(repoRoot, root)
	if _, err := os.Stat(absRoot); os.IsNotExist(err) {
		return nil
	}
	var violations []Violation
	if err := filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			violations = append(violations, Violation{Kind: ViolationFile, File: filepath.ToSlash(path), Message: fmt.Sprintf("%s: walk error: %v", path, walkErr)})
			return walkErr
		}
		if d.IsDir() {
			if skip[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		relPath, err := filepath.Rel(repoRoot, path)
		if err != nil {
			relPath = path
		}
		violations = append(violations, checkSingleFile(path, filepath.ToSlash(relPath), stats, enforceFuncComments)...)
		return nil
	}); err != nil {
		violations = append(violations, Violation{Kind: ViolationFile, File: filepath.ToSlash(root), Message: fmt.Sprintf("%s: walk root error: %v", root, err)})
	}
	return violations
}

// checkSingleFile 检查单个 Go 文件，同时维护所在包的行数和文件数统计。
func checkSingleFile(path, relPath string, stats map[string]*packageStat, enforceFuncComments bool) []Violation {
	data, err := os.ReadFile(path)
	if err != nil {
		return []Violation{{Kind: ViolationFile, File: relPath, Message: fmt.Sprintf("%s: read error: %v", relPath, err)}}
	}
	if IsGeneratedSQLCFile(relPath, data) {
		return nil
	}
	rawLines := SplitLines(data)
	fileLines := CountEffectiveLines(data)
	stat := stats[filepath.ToSlash(filepath.Dir(relPath))]
	if stat == nil {
		stat = &packageStat{}
		stats[filepath.ToSlash(filepath.Dir(relPath))] = stat
	}
	factory := isFactoryFile(relPath)
	isTest := strings.HasSuffix(relPath, "_test.go")
	// 测试文件不计入包文件数统计（逻辑上属于独立的 _test 包），但仍参与单文件级检查。
	if !factory && !isTest {
		stat.Files++
		if fileLines > stat.MaxFileLines {
			stat.MaxFileLines = fileLines
		}
	}

	violations := fileLengthViolationsForFile(relPath, fileLines, factory)
	fset := token.NewFileSet()
	fileNode, parseErr := parser.ParseFile(fset, path, data, parser.ParseComments|parser.SkipObjectResolution)
	if parseErr != nil {
		return append(violations, Violation{Kind: ViolationFile, File: relPath, Message: fmt.Sprintf("%s: parse error: %v", relPath, parseErr)})
	}
	violations = append(violations, functionViolations(relPath, rawLines, fset, fileNode, enforceFuncComments && !isTest)...)
	violations = append(violations, identifierViolations(relPath, fset, fileNode)...)
	return violations
}

func isFactoryFile(relPath string) bool {
	return filepath.Base(relPath) == "factory.go"
}

func fileLengthViolationsForFile(relPath string, fileLines int, factory bool) []Violation {
	limit := fileLineLimit(relPath, factory)
	if fileLines <= limit {
		return nil
	}
	return []Violation{{Kind: ViolationFile, File: relPath, Got: fileLines, Limit: limit, Message: fmt.Sprintf("文件  %s: %d 行 > 上限 %d", relPath, fileLines, limit)}}
}

func fileLineLimit(relPath string, factory bool) int {
	if factory {
		return MaxFactoryFileLines
	}
	pkgDir := filepath.ToSlash(filepath.Dir(relPath))
	if limit, ok := frozenLimit(pkgDir, ViolationFile); ok {
		return limit
	}
	// 2026-04-17 守卫放宽后 MaxCorePackageFileLines == MaxFileLines == 600，此分支在文件行数维度已归一。
	// 保留的原因：与 `MaxCorePackageFiles (30)` 一同承担 core package 路由语义，供 freeze registry / 未来再次调整用；勿自行删除。
	if isCorePackageDir(pkgDir) {
		return MaxCorePackageFileLines
	}
	return MaxFileLines
}

// functionViolations 汇总函数长度、嵌套、复杂度和中文说明的违规项。
func functionViolations(relPath string, rawLines []string, fset *token.FileSet, fileNode *ast.File, enforceFuncComments bool) []Violation {
	var violations []Violation
	ignores := collectArchGuardIgnores(fset, fileNode)
	for _, decl := range fileNode.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil || fd.Name == nil {
			continue
		}
		metrics := measureGuardFuncMetrics(rawLines, fset, fd)
		if violation, ok := funcCommentViolation(relPath, fd, metrics, enforceFuncComments, ignores); ok {
			violations = append(violations, violation)
		}
		if metrics.lines > MaxFuncLines {
			violations = append(violations, Violation{Kind: ViolationFunc, File: relPath, Func: metrics.name, Line: metrics.startLine, Got: metrics.lines, Limit: MaxFuncLines, Message: fmt.Sprintf("函数  %s:%d  %s(): %d 行 > 上限 %d", relPath, metrics.startLine, metrics.name, metrics.lines, MaxFuncLines)})
		}
		if metrics.depth > MaxNestingDepth {
			violations = append(violations, Violation{Kind: ViolationNesting, File: relPath, Func: metrics.name, Line: metrics.startLine, Got: metrics.depth, Limit: MaxNestingDepth, Message: fmt.Sprintf("嵌套  %s:%d  %s(): 深度 %d > 上限 %d", relPath, metrics.startLine, metrics.name, metrics.depth, MaxNestingDepth)})
		}
		if metrics.cc > MaxCCComplexity {
			violations = append(violations, Violation{Kind: ViolationCC, File: relPath, Func: metrics.name, Line: metrics.startLine, Got: metrics.cc, Limit: MaxCCComplexity, Message: fmt.Sprintf("复杂度 %s:%d  %s(): CC %d > 上限 %d", relPath, metrics.startLine, metrics.name, metrics.cc, MaxCCComplexity)})
		}
	}
	return violations
}

type funcMetrics struct {
	name      string
	startLine int
	lines     int
	depth     int
	cc        int
}

func measureGuardFuncMetrics(rawLines []string, fset *token.FileSet, fd *ast.FuncDecl) funcMetrics {
	startLine := fset.Position(fd.Pos()).Line
	endLine := fset.Position(fd.End()).Line
	return funcMetrics{
		name:      fd.Name.Name,
		startLine: startLine,
		lines:     EffectiveLinesInRange(rawLines, startLine, endLine),
		depth:     MeasureMaxNesting(fd.Body, 0),
		cc:        MeasureCyclomaticComplexity(fd),
	}
}

func funcCommentViolation(relPath string, fd *ast.FuncDecl, metrics funcMetrics, enforce bool, ignores archGuardIgnores) (Violation, bool) {
	if !enforce {
		return Violation{}, false
	}
	if !requiresFuncComment(fd, metrics.lines, metrics.depth, metrics.cc) {
		return Violation{}, false
	}
	if hasUsefulChineseFuncDoc(fd) {
		return Violation{}, false
	}
	if ignores.has(metrics.startLine, "func_comment") {
		return Violation{}, false
	}
	return Violation{Kind: ViolationFuncComment, File: relPath, Func: metrics.name, Line: metrics.startLine, Message: fmt.Sprintf("注释  %s:%d  %s(): 导出或复杂函数需要中文函数级说明", relPath, metrics.startLine, metrics.name)}, true
}

// requiresFuncComment 判断函数是否到了需要给维护者留一句说明的程度。
func requiresFuncComment(fd *ast.FuncDecl, lines, depth, cc int) bool {
	if fd.Name == nil {
		return false
	}
	if fd.Name.Name == "init" {
		return false
	}
	if ast.IsExported(fd.Name.Name) {
		return true
	}
	if lines >= FuncCommentMinLines {
		return true
	}
	if depth >= FuncCommentMinDepth {
		return true
	}
	return cc >= FuncCommentMinCC
}

// hasUsefulChineseFuncDoc 判断函数前置注释里是否有面向维护者的中文说明。
func hasUsefulChineseFuncDoc(fd *ast.FuncDecl) bool {
	if fd.Doc == nil {
		return false
	}
	for _, comment := range fd.Doc.List {
		text := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(comment.Text, "//"), "/*"))
		text = strings.TrimSpace(strings.TrimSuffix(text, "*/"))
		if text == "" || strings.Contains(text, archGuardIgnorePrefix) {
			continue
		}
		if containsChineseRune(text) {
			return true
		}
	}
	return false
}

func containsChineseRune(text string) bool {
	for _, r := range text {
		if r >= '\u4e00' && r <= '\u9fff' {
			return true
		}
	}
	return false
}

func identifierViolations(relPath string, fset *token.FileSet, fileNode *ast.File) []Violation {
	var violations []Violation
	ast.Inspect(fileNode, func(n ast.Node) bool {
		ident, ok := n.(*ast.Ident)
		if !ok || ident.Name == "_" {
			return true
		}
		if underscores := strings.Count(ident.Name, "_"); underscores > MaxUnderscores {
			line := fset.Position(ident.Pos()).Line
			violations = append(violations, Violation{Kind: ViolationIdentifier, File: relPath, Line: line, Got: underscores, Limit: MaxUnderscores, Message: fmt.Sprintf("命名  %s:%d  '%s' 下划线超过 %d 个", relPath, line, ident.Name, MaxUnderscores)})
		}
		return true
	})
	return violations
}

// packageViolations 根据前面累积的包统计生成包级预算违规。
func packageViolations(stats map[string]*packageStat) []Violation {
	keys := make([]string, 0, len(stats))
	for key := range stats {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	var violations []Violation
	for _, pkgDir := range keys {
		stat := stats[pkgDir]
		fileLimit := packageFileCountLimit(pkgDir)
		if fileLimit > 0 && stat.Files > fileLimit {
			violations = append(violations, Violation{Kind: ViolationPackageCount, File: pkgDir, Got: stat.Files, Limit: fileLimit, Message: fmt.Sprintf("包文件数 %s: %d 个 > 上限 %d", pkgDir, stat.Files, fileLimit)})
		}
	}
	return violations
}

func packageFileCountLimit(pkgDir string) int {
	if limit, ok := frozenLimit(pkgDir, ViolationPackageCount); ok {
		return limit
	}
	return MaxPackageFiles
}

const sqlcGeneratedPrefixStr = "// Code generated by sqlc. DO NOT EDIT."

// IsGeneratedSQLCFile 判断文件是否是 sqlc 生成代码。
func IsGeneratedSQLCFile(relPath string, data []byte) bool {
	if !isSQLCPackageDir(filepath.ToSlash(filepath.Dir(relPath))) {
		return false
	}
	return strings.HasPrefix(string(data), sqlcGeneratedPrefixStr)
}

func isSQLCPackageDir(pkgDir string) bool {
	switch pkgDir {
	case "internal/store/sqlc", "cmd/mcp-orch/store/sqlc":
		return true
	default:
		return false
	}
}

func isCorePackageDir(pkgDir string) bool {
	switch pkgDir {
	case "internal/module/memory",
		"internal/module/prompt",
		"internal/module/thread",
		"internal/module/turn":
		return true
	default:
		return false
	}
}
