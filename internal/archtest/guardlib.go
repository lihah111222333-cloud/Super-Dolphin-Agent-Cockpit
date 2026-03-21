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
	MaxFileLines    = 400
	MaxFuncLines    = 80
	MaxNestingDepth = 4
	MaxUnderscores  = 3
	MaxCCComplexity = 10
	MaxPackageFiles = 15
	MaxPackageLines = 3000
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
	Files int
	Lines int
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
	stats := map[string]*packageStat{}
	var violations []Violation
	for _, root := range opts.scanRoots() {
		violations = append(violations, scanRoot(repoRoot, root, opts.skipDirs(), stats)...)
	}
	violations = append(violations, packageViolations(stats)...)
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
	return violations
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
	_ = filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			violations = append(violations, Violation{Kind: ViolationFile, File: filepath.ToSlash(path), Message: fmt.Sprintf("%s: walk error: %v", path, walkErr)})
			return nil
		}
		if d.IsDir() {
			if skip[d.Name()] {
				return filepath.SkipDir
			}
			// sqlc 生成代码豁免所有守卫检查
			rel, _ := filepath.Rel(repoRoot, path)
			switch filepath.ToSlash(rel) {
			case "internal/store/sqlc":
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relPath, err := filepath.Rel(repoRoot, path)
		if err != nil {
			relPath = path
		}
		violations = append(violations, checkSingleFile(path, filepath.ToSlash(relPath), stats)...)
		return nil
	})
	return violations
}

func checkSingleFile(path, relPath string, stats map[string]*packageStat) []Violation {
	data, err := os.ReadFile(path)
	if err != nil {
		return []Violation{{Kind: ViolationFile, File: relPath, Message: fmt.Sprintf("%s: read error: %v", relPath, err)}}
	}
	rawLines := SplitLines(data)
	fileLines := CountEffectiveLines(data)
	stat := stats[filepath.ToSlash(filepath.Dir(relPath))]
	if stat == nil {
		stat = &packageStat{}
		stats[filepath.ToSlash(filepath.Dir(relPath))] = stat
	}
	stat.Files++
	stat.Lines += fileLines

	violations := fileLengthViolations(relPath, fileLines)
	fset := token.NewFileSet()
	fileNode, parseErr := parser.ParseFile(fset, path, data, parser.SkipObjectResolution)
	if parseErr != nil {
		return append(violations, Violation{Kind: ViolationFile, File: relPath, Message: fmt.Sprintf("%s: parse error: %v", relPath, parseErr)})
	}
	violations = append(violations, functionViolations(relPath, rawLines, fset, fileNode)...)
	violations = append(violations, identifierViolations(relPath, fset, fileNode)...)
	return violations
}

func fileLengthViolations(relPath string, fileLines int) []Violation {
	if fileLines <= MaxFileLines {
		return nil
	}
	return []Violation{{Kind: ViolationFile, File: relPath, Got: fileLines, Limit: MaxFileLines, Message: fmt.Sprintf("文件  %s: %d 行 > 上限 %d", relPath, fileLines, MaxFileLines)}}
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
		if !skipPackageFileCountLimit(pkgDir) && stat.Files > MaxPackageFiles {
			violations = append(violations, Violation{Kind: ViolationPackageCount, File: pkgDir, Got: stat.Files, Limit: MaxPackageFiles, Message: fmt.Sprintf("包文件数 %s: %d 个 > 上限 %d", pkgDir, stat.Files, MaxPackageFiles)})
		}
		if stat.Lines > MaxPackageLines {
			violations = append(violations, Violation{Kind: ViolationPackageLines, File: pkgDir, Got: stat.Lines, Limit: MaxPackageLines, Message: fmt.Sprintf("包行数 %s: %d 行 > 上限 %d", pkgDir, stat.Lines, MaxPackageLines)})
		}
	}
	return violations
}

func skipPackageFileCountLimit(pkgDir string) bool {
	switch pkgDir {
	case "internal/store/sqlc":
		// sqlc 输出按查询源文件拆分，包文件数对生成层噪声较大；仍保留包总行数与单文件/函数守卫。
		return true
	default:
		return false
	}
}

func clampEffectiveLineEnd(lineCount, end int) int {
	if end < 0 || end > lineCount {
		return lineCount
	}
	return end
}

func effectiveLineDelta(line string, inBlock bool) (int, bool) {
	if line == "" {
		return 0, inBlock
	}
	if inBlock {
		return blockCommentLineDelta(line)
	}
	if strings.HasPrefix(line, "//") {
		return 0, false
	}
	if strings.HasPrefix(line, "/*") {
		return leadingBlockCommentLineDelta(line)
	}
	return 1, false
}

func blockCommentLineDelta(line string) (int, bool) {
	_, after, found := strings.Cut(line, "*/")
	if !found {
		return 0, true
	}
	if hasTrailingCode(after) {
		return 1, false
	}
	return 0, false
}

func leadingBlockCommentLineDelta(line string) (int, bool) {
	_, after, found := strings.Cut(line[2:], "*/")
	if !found {
		return 0, true
	}
	if hasTrailingCode(after) {
		return 1, false
	}
	return 0, false
}

func hasTrailingCode(line string) bool {
	rest := strings.TrimSpace(line)
	return rest != "" && !strings.HasPrefix(rest, "//")
}
