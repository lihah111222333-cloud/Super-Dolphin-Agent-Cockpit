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
	// 2026-04-17 默认守卫放宽：单文件 400→600、包文件数 15→25、包有效行 4500→10000。
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
	MaxPackageLines         = 10000
	MaxCorePackageLines     = 10000
)

type ViolationKind int

const (
	ViolationFile ViolationKind = iota
	ViolationFunc
	ViolationNesting
	ViolationCC
	ViolationIdentifier
	ViolationPackageCount
	ViolationPackageLines
	ViolationDeadKey
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

func (v Violation) String() string {
	if v.Message != "" {
		return v.Message
	}
	return fmt.Sprintf("%s:%d %s got=%d limit=%d", v.File, v.Line, v.Func, v.Got, v.Limit)
}

type CheckOptions struct {
	RepoRoot  string
	ScanRoots []string
	SkipDirs  map[string]bool
}

type packageStat struct {
	Files        int
	Lines        int
	MaxFileLines int
}

func DefaultScanRoots() []string {
	return []string{"internal", "cmd", "scripts"}
}

func DefaultSkipDirs() map[string]bool {
	return map[string]bool{
		".git":         true,
		"node_modules": true,
		"vendor":       true,
	}
}

func CheckAll(opts CheckOptions) []Violation {
	repoRoot := opts.RepoRoot
	if repoRoot == "" {
		repoRoot = "."
	}
	scanRoots := opts.scanRoots()
	stats := map[string]*packageStat{}
	violations := freezeRegistryIntegrityViolations()
	for _, root := range scanRoots {
		violations = append(violations, scanRoot(repoRoot, root, opts.skipDirs(), stats)...)
	}
	violations = append(violations, packageViolations(stats)...)
	violations = append(violations, postScanViolations(repoRoot, scanRoots, stats)...)
	sortViolations(violations)
	return violations
}

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
		violations = append(violations, checkSingleFile(absPath, relPath, stats)...)
	}
	sortViolations(violations)
	return violations
}

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

func SplitLines(data []byte) []string {
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		out = append(out, sc.Text())
	}
	return out
}

func CountEffectiveLines(data []byte) int {
	return EffectiveLinesInRange(SplitLines(data), 1, -1)
}

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

func scanRoot(repoRoot, root string, skip map[string]bool, stats map[string]*packageStat) []Violation {
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
		violations = append(violations, checkSingleFile(path, filepath.ToSlash(relPath), stats)...)
		return nil
	}); err != nil {
		violations = append(violations, Violation{Kind: ViolationFile, File: filepath.ToSlash(root), Message: fmt.Sprintf("%s: walk root error: %v", root, err)})
	}
	return violations
}

func checkSingleFile(path, relPath string, stats map[string]*packageStat) []Violation {
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
	// 测试文件不计入包文件数/行数统计（逻辑上属于独立的 _test 包），但仍参与单文件级检查。
	if !factory && !isTest {
		stat.Files++
		stat.Lines += fileLines
		if fileLines > stat.MaxFileLines {
			stat.MaxFileLines = fileLines
		}
	}

	violations := fileLengthViolationsForFile(relPath, fileLines, factory)
	fset := token.NewFileSet()
	fileNode, parseErr := parser.ParseFile(fset, path, data, parser.SkipObjectResolution)
	if parseErr != nil {
		return append(violations, Violation{Kind: ViolationFile, File: relPath, Message: fmt.Sprintf("%s: parse error: %v", relPath, parseErr)})
	}
	violations = append(violations, functionViolations(relPath, rawLines, fset, fileNode)...)
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
	// 保留的原因：与 `MaxCorePackageFiles (30)` / `MaxCorePackageLines (10000)` 一同承担 core package
	// 路由语义，供 freeze registry / 未来再次调整用；勿自行删除。
	if isCorePackageDir(pkgDir) {
		return MaxCorePackageFileLines
	}
	return MaxFileLines
}

func functionViolations(relPath string, rawLines []string, fset *token.FileSet, fileNode *ast.File) []Violation {
	var violations []Violation
	for _, decl := range fileNode.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil || fd.Name == nil {
			continue
		}
		startLine := fset.Position(fd.Pos()).Line
		endLine := fset.Position(fd.End()).Line
		funcName := fd.Name.Name
		funcLines := EffectiveLinesInRange(rawLines, startLine, endLine)
		if funcLines > MaxFuncLines {
			violations = append(violations, Violation{Kind: ViolationFunc, File: relPath, Func: funcName, Line: startLine, Got: funcLines, Limit: MaxFuncLines, Message: fmt.Sprintf("函数  %s:%d  %s(): %d 行 > 上限 %d", relPath, startLine, funcName, funcLines, MaxFuncLines)})
		}
		if depth := MeasureMaxNesting(fd.Body, 0); depth > MaxNestingDepth {
			violations = append(violations, Violation{Kind: ViolationNesting, File: relPath, Func: funcName, Line: startLine, Got: depth, Limit: MaxNestingDepth, Message: fmt.Sprintf("嵌套  %s:%d  %s(): 深度 %d > 上限 %d", relPath, startLine, funcName, depth, MaxNestingDepth)})
		}
		if cc := MeasureCyclomaticComplexity(fd); cc > MaxCCComplexity {
			violations = append(violations, Violation{Kind: ViolationCC, File: relPath, Func: funcName, Line: startLine, Got: cc, Limit: MaxCCComplexity, Message: fmt.Sprintf("复杂度 %s:%d  %s(): CC %d > 上限 %d", relPath, startLine, funcName, cc, MaxCCComplexity)})
		}
	}
	return violations
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
		lineLimit := packageLineLimit(pkgDir)
		if lineLimit > 0 && stat.Lines > lineLimit {
			violations = append(violations, Violation{Kind: ViolationPackageLines, File: pkgDir, Got: stat.Lines, Limit: lineLimit, Message: fmt.Sprintf("包行数 %s: %d 行 > 上限 %d", pkgDir, stat.Lines, lineLimit)})
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

func packageLineLimit(pkgDir string) int {
	if limit, ok := frozenLimit(pkgDir, ViolationPackageLines); ok {
		return limit
	}
	return MaxPackageLines
}

const sqlcGeneratedPrefixStr = "// Code generated by sqlc. DO NOT EDIT."

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
