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

// TestNakedGoroutineGuard 禁止裸 go func(){}() 调用。
// 所有 goroutine 必须通过 safego.Go(ctx, logger, label, fn) 启动，
// 确保 panic 可恢复、可追溯。
//
// 豁免：
//   - internal/util/safego/safego.go — SafeGo 实现本身
//   - internal/platform/shared/safe_go.go — 旧包装器（Deprecated）
//   - pkg/logger/safego.go — logger 内部使用
//   - cmd/ 下的 main/fx 入口 — 顶层 goroutine 由 fx/rungroup 管理
func TestNakedGoroutineGuard(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)
	scanRoots := []string{"internal"}
	skipDirs := DefaultSkipDirs()

	allowedFiles := map[string]struct{}{
		filepath.Join("internal", "util", "safego", "safego.go"):      {},
		filepath.Join("internal", "platform", "shared", "safe_go.go"): {},
		filepath.Join("internal", "contract", "contracttest"):         {}, // test helpers
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
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			if isAllowedForNakedGoroutine(rel, allowedFiles) {
				return nil
			}
			fset := token.NewFileSet()
			node, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if parseErr != nil {
				return nil
			}
			count := countNakedGoStmts(node)
			if count > 0 {
				violations = append(violations,
					rel+": 发现 "+itoa(count)+" 处裸 go func() — 必须使用 safego.Go(ctx, logger, label, fn)")
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", sr, err)
		}
	}

	if len(violations) > 0 {
		t.Fatalf("Naked goroutine guard violations (%d):\n  %s",
			len(violations), strings.Join(violations, "\n  "))
	}
}

func isAllowedForNakedGoroutine(rel string, allowedFiles map[string]struct{}) bool {
	if _, ok := allowedFiles[rel]; ok {
		return true
	}
	for prefix := range allowedFiles {
		if strings.HasPrefix(rel, prefix+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// countNakedGoStmts 计算 AST 中 go func(){...}() 形式的语句数量。
func countNakedGoStmts(node *ast.File) int {
	count := 0
	ast.Inspect(node, func(n ast.Node) bool {
		goStmt, ok := n.(*ast.GoStmt)
		if !ok {
			return true
		}
		// go func() { ... }() — 裸 goroutine
		if funcLit, isFuncLit := goStmt.Call.Fun.(*ast.FuncLit); isFuncLit {
			if !hasDeferRecover(funcLit.Body) {
				count++
			}
		}
		return true
	})
	return count
}

// hasDeferRecover 检查代码块的第一条语句是否为 defer func() { ... recover() ... }()
func hasDeferRecover(body *ast.BlockStmt) bool {
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
		if ok {
			if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "recover" {
				foundRecover = true
				return false
			}
		}
		return true
	})
	return foundRecover
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
