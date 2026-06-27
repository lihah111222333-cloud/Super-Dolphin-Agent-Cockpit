package archtest

import (
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
	violations := findNakedGoroutineViolations(t, root, scanRoots, skipDirs, nakedGoroutineAllowedFiles())

	if len(violations) > 0 {
		t.Fatalf("Naked goroutine guard violations (%d):\n  %s",
			len(violations), strings.Join(violations, "\n  "))
	}
}

func nakedGoroutineAllowedFiles() map[string]struct{} {
	return map[string]struct{}{
		filepath.Join("internal", "util", "safego", "safego.go"):      {},
		filepath.Join("internal", "platform", "shared", "safe_go.go"): {},
		filepath.Join("internal", "contract", "contracttest"):         {}, // test helpers
	}
}

func findNakedGoroutineViolations(
	t *testing.T,
	root string,
	scanRoots []string,
	skipDirs map[string]bool,
	allowedFiles map[string]struct{},
) []string {
	t.Helper()
	var violations []string
	for _, sr := range scanRoots {
		rootViolations, err := scanNakedGoroutineRoot(root, sr, skipDirs, allowedFiles)
		if err != nil {
			t.Fatalf("walk %s: %v", sr, err)
		}
		violations = append(violations, rootViolations...)
	}
	return violations
}

func scanNakedGoroutineRoot(root, scanRoot string, skipDirs map[string]bool, allowedFiles map[string]struct{}) ([]string, error) {
	var violations []string
	abs := filepath.Join(root, scanRoot)
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
		violation, ok, err := nakedGoroutineViolationForFile(root, path, allowedFiles)
		if err != nil {
			return err
		}
		if ok {
			violations = append(violations, violation)
		}
		return nil
	})
	return violations, err
}

func nakedGoroutineViolationForFile(root, path string, allowedFiles map[string]struct{}) (string, bool, error) {
	if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
		return "", false, nil
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", false, err
	}
	if isAllowedForNakedGoroutine(rel, allowedFiles) {
		return "", false, nil
	}
	fset := token.NewFileSet()
	node, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if parseErr != nil {
		return "", false, nil
	}
	count := CountNakedGoStmts(node)
	if count == 0 {
		return "", false, nil
	}
	return rel + ": 发现 " + itoa(count) + " 处裸 go func() — 必须使用 safego.Go(ctx, logger, label, fn)", true, nil
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
