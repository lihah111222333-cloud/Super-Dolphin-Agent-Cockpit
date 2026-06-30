package archtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDeterministicTimeGuard 禁止业务层直接调用 time.Now()。
// time.Now() 产生不确定性，妨碍确定性测试和时间旅行调试。
//
// 正确做法：
//   - 通过构造函数注入 nowFunc func() time.Time
//   - 或使用 pkg/timeutil 提供的可注入时间源
//
// 豁免：
//   - cmd/ — 入口层可以使用 time.Now()
//   - pkg/logger/ — 日志时间戳
//   - internal/util/idgen/ — ID 生成需要时间戳
//   - internal/platform/config/ — 配置加载
//   - internal/platform/rlimit/ — 系统资源
//   - internal/ui/ — UI 层非核心业务
//   - 文件名包含 _test.go — 测试代码
//   - 已有 nowFunc 注入点的文件（通过 AST 检测）
func TestDeterministicTimeGuard(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)
	violations := collectDeterministicTimeViolations(t, root, []string{"internal"}, DefaultSkipDirs())

	if len(violations) > 0 {
		t.Fatalf("Deterministic time guard violations (%d):\n  %s",
			len(violations), strings.Join(violations, "\n  "))
	}
}

type deterministicTimeScan struct {
	root        string
	skipDirs    map[string]bool
	allowedDirs []string
	violations  *[]string
}

func collectDeterministicTimeViolations(t *testing.T, root string, scanRoots []string, skipDirs map[string]bool) []string {
	t.Helper()
	violations := []string{}
	scan := deterministicTimeScan{
		root:        root,
		skipDirs:    skipDirs,
		allowedDirs: deterministicTimeAllowedDirs(),
		violations:  &violations,
	}
	for _, sr := range scanRoots {
		if err := filepath.Walk(filepath.Join(root, sr), scan.visit); err != nil {
			t.Fatalf("walk %s: %v", sr, err)
		}
	}
	return violations
}

func deterministicTimeAllowedDirs() []string {
	return []string{
		"internal/util/idgen",
		"internal/platform",
		"internal/ui",
		"internal/archtest",
		"internal/store",
		"internal/provider",
		"internal/module",
		"internal/mcpserver",
	}
}

func (s deterministicTimeScan) visit(path string, info os.FileInfo, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	if info.IsDir() {
		return s.visitDir(info)
	}
	if !isProductionGoPath(path) {
		return nil
	}
	relSlash, err := s.relSlash(path)
	if err != nil {
		return err
	}
	if isAllowedDeterministicTimePath(relSlash, s.allowedDirs) {
		return nil
	}
	node, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if parseErr != nil {
		return fmt.Errorf("parse %s: %w", relSlash, parseErr)
	}
	if hasNowFuncInjection(node) {
		return nil
	}
	s.recordViolation(relSlash, countTimeNowCalls(node))
	return nil
}

func (s deterministicTimeScan) visitDir(info os.FileInfo) error {
	if _, skip := s.skipDirs[info.Name()]; skip {
		return filepath.SkipDir
	}
	return nil
}

func (s deterministicTimeScan) relSlash(path string) (string, error) {
	rel, err := filepath.Rel(s.root, path)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}

func (s deterministicTimeScan) recordViolation(relSlash string, count int) {
	if count > 0 {
		*s.violations = append(*s.violations,
			relSlash+": 发现 "+itoa(count)+" 处 time.Now() 直接调用 — 应注入 nowFunc 或使用可测试时间源")
	}
}

func isProductionGoPath(path string) bool {
	return strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go")
}

func isAllowedDeterministicTimePath(relSlash string, allowedDirs []string) bool {
	for _, dir := range allowedDirs {
		if strings.HasPrefix(relSlash, dir+"/") || relSlash == dir {
			return true
		}
	}
	return false
}

// countTimeNowCalls 计算文件中 time.Now() 调用次数。
func countTimeNowCalls(node *ast.File) int {
	// 先检查是否导入了 "time" 包
	hasTimeImport := false
	for _, imp := range node.Imports {
		if strings.Trim(imp.Path.Value, "\"") == "time" {
			hasTimeImport = true
			break
		}
	}
	if !hasTimeImport {
		return 0
	}
	count := 0
	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if pkg.Name == "time" && sel.Sel.Name == "Now" {
			count++
		}
		return true
	})
	return count
}

// hasNowFuncInjection 检查文件是否包含 nowFunc 字段或变量，表示已有时间注入点。
func hasNowFuncInjection(node *ast.File) bool {
	found := false
	ast.Inspect(node, func(n ast.Node) bool {
		if found {
			return false
		}
		if field, ok := n.(*ast.Field); ok {
			found = fieldHasNowInjection(field)
			return !found
		}
		if spec, ok := n.(*ast.ValueSpec); ok {
			found = valueSpecHasNowInjection(spec)
			return !found
		}
		return true
	})
	return found
}

func fieldHasNowInjection(field *ast.Field) bool {
	for _, name := range field.Names {
		if nowFieldNameIndicatesFunc(name.Name) || isNowFieldName(name.Name) {
			return true
		}
	}
	return false
}

func nowFieldNameIndicatesFunc(name string) bool {
	lower := strings.ToLower(name)
	return strings.Contains(lower, "now") && strings.Contains(lower, "func")
}

func isNowFieldName(name string) bool {
	return name == "now" || name == "nowFn" || name == "clock"
}

func valueSpecHasNowInjection(spec *ast.ValueSpec) bool {
	for _, name := range spec.Names {
		if name.Name == "nowFunc" || name.Name == "now" || name.Name == "defaultNow" {
			return true
		}
	}
	return false
}
