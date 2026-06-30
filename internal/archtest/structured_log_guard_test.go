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

// TestStructuredLogGuard 强制结构化日志规范。
//
// V3 的日志分三层：
//
//	Layer 1: pkg/logger — 全局日志函数（logger.Info/Error/Warn 等），启动层使用。
//	Layer 2: log/slog — 标准结构化日志（pkg/logger.Init 后通过 slog.SetDefault 生效）。
//	Layer 3 (禁止): "log" 标准库 — 无结构化字段、无级别控制、无 relay 管道。
//
// 全面禁止：
//   - import "log" — 标准库 log 包（log.Printf/Println/Fatal 等无结构化字段）
//   - fmt.Fprintf(os.Stderr, ...) — 绕过日志管道，无法被采集
//
// 豁免：
//   - pkg/logger/ 自身
//   - internal/archtest/ 守卫
//   - scripts/ 入口脚本（go:build ignore）
//   - internal/platform/rlimit/ — 系统初始化，logger 尚未就绪
func TestStructuredLogGuard(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)
	scanRoots := []string{"internal", "cmd"}
	skipDirs := DefaultSkipDirs()
	violations := collectStructuredLogViolations(t, root, scanRoots, skipDirs)

	if len(violations) > 0 {
		t.Fatalf("Structured log guard violations (%d):\n  %s",
			len(violations), strings.Join(violations, "\n  "))
	}
}

func collectStructuredLogViolations(t *testing.T, root string, scanRoots []string, skipDirs map[string]bool) []string {
	t.Helper()
	var violations []string
	for _, sr := range scanRoots {
		abs := filepath.Join(root, sr)
		err := filepath.Walk(abs, func(path string, info os.FileInfo, walkErr error) error {
			fileViolations, err := structuredLogPathViolations(root, path, info, walkErr, skipDirs)
			violations = append(violations, fileViolations...)
			return err
		})
		if err != nil {
			t.Fatalf("walk %s: %v", sr, err)
		}
	}
	return violations
}

func structuredLogPathViolations(root, path string, info os.FileInfo, walkErr error, skipDirs map[string]bool) ([]string, error) {
	if walkErr != nil {
		return nil, walkErr
	}
	if info.IsDir() {
		if _, skip := skipDirs[info.Name()]; skip {
			return nil, filepath.SkipDir
		}
		return nil, nil
	}
	if !isStructuredLogGuardTarget(path) {
		return nil, nil
	}
	rel, relErr := filepath.Rel(root, path)
	if relErr != nil {
		return nil, relErr
	}
	relSlash := filepath.ToSlash(rel)
	if structuredLogPathAllowed(relSlash) {
		return nil, nil
	}
	return structuredLogFileViolations(path, relSlash)
}

func isStructuredLogGuardTarget(path string) bool {
	return strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go")
}

func structuredLogPathAllowed(relSlash string) bool {
	allowedDirs := []string{
		"internal/archtest",
		"internal/platform/rlimit",
	}
	for _, dir := range allowedDirs {
		if strings.HasPrefix(relSlash, dir+"/") {
			return true
		}
	}
	return false
}

func structuredLogFileViolations(path, relSlash string) ([]string, error) {
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		return nil, readErr
	}
	fset := token.NewFileSet()
	node, parseErr := parser.ParseFile(fset, path, data, parser.ImportsOnly|parser.SkipObjectResolution)
	if parseErr != nil {
		return nil, parseErr
	}
	violations := structuredLogImportViolations(relSlash, node)
	return append(violations, structuredLogStderrViolations(relSlash, data)...), nil
}

func structuredLogImportViolations(relSlash string, node *ast.File) []string {
	var violations []string
	for _, imp := range node.Imports {
		importPath := strings.Trim(imp.Path.Value, "\"")
		if importPath == "log" {
			violations = append(violations,
				relSlash+": 禁止导入 \"log\" 包 — 使用 slog 或 pkg/logger 代替 log.Printf/Println")
		}
	}
	return violations
}

func structuredLogStderrViolations(relSlash string, data []byte) []string {
	var violations []string
	lines := strings.Split(string(data), "\n")
	for lineNo, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		if isStructuredLogStderrWrite(trimmed) {
			violations = append(violations,
				relSlash+":"+itoa(lineNo+1)+": 禁止 fmt.Fprintf(os.Stderr) — 使用 slog 或 pkg/logger")
		}
	}
	return violations
}

func isStructuredLogStderrWrite(trimmed string) bool {
	return strings.Contains(trimmed, "fmt.Fprintf(os.Stderr") ||
		strings.Contains(trimmed, "fmt.Fprintln(os.Stderr")
}
