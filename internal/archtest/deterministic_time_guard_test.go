package archtest

import (
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
	scanRoots := []string{"internal"}
	skipDirs := DefaultSkipDirs()

	allowedDirs := []string{
		"internal/util/idgen",
		"internal/platform",
		"internal/ui",
		"internal/archtest",
		"internal/store",
		"internal/provider",
		"internal/module",
		"internal/mcpserver",
	}

	var violations []string
	for _, sr := range scanRoots {
		abs := filepath.Join(root, sr)
		err := filepath.Walk(abs, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if info.IsDir() {
				if _, skip := skipDirs[info.Name()]; skip {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			relSlash := filepath.ToSlash(rel)
			for _, dir := range allowedDirs {
				if strings.HasPrefix(relSlash, dir+"/") || relSlash == dir {
					return nil
				}
			}
			fset := token.NewFileSet()
			node, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if parseErr != nil {
				return nil
			}
			// 如果文件已有 nowFunc 注入点，则豁免
			if hasNowFuncInjection(node) {
				return nil
			}
			count := countTimeNowCalls(node)
			if count > 0 {
				violations = append(violations,
					relSlash+": 发现 "+itoa(count)+" 处 time.Now() 直接调用 — 应注入 nowFunc 或使用可测试时间源")
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", sr, err)
		}
	}

	if len(violations) > 0 {
		t.Fatalf("Deterministic time guard violations (%d):\n  %s",
			len(violations), strings.Join(violations, "\n  "))
	}
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
		switch x := n.(type) {
		case *ast.Field:
			// struct 字段: nowFunc func() time.Time
			for _, name := range x.Names {
				if strings.Contains(strings.ToLower(name.Name), "now") &&
					strings.Contains(strings.ToLower(name.Name), "func") {
					found = true
					return false
				}
				if name.Name == "now" || name.Name == "nowFn" || name.Name == "clock" {
					found = true
					return false
				}
			}
		case *ast.ValueSpec:
			// var nowFunc = ...
			for _, name := range x.Names {
				if name.Name == "nowFunc" || name.Name == "now" || name.Name == "defaultNow" {
					found = true
					return false
				}
			}
		}
		return true
	})
	return found
}
