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

func TestStructuredLogsRejectRawRuntimeFields(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "unsafe.go")
	source := []byte(`package sample

import (
	"encoding/json"
	"log/slog"

	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

func unsafe(raw json.RawMessage, cwd, home string) {
	pkglogger.Info("unsafe cwd", "cwd", cwd)
	pkglogger.Warn("unsafe payload", "payload", string(raw))
	pkglogger.Warn("unsafe codex home", "codex_home", home)
	pkglogger.Warn("unsafe camel codex home", "codexHome", home)
	pkglogger.Warn("unsafe provider home", "home", home)
	pkglogger.Warn("unsafe work dir", "work_dir", home)
	fields := []any{"args_cwd", cwd}
	pkglogger.Warn("unsafe spread cwd", fields...)
	moreFields := []any{"agent_id", "agent-1"}
	moreFields = append(moreFields, "launch_cwd", cwd)
	pkglogger.Warn("unsafe append cwd", moreFields...)
	attrFields := []any{slog.String("attr_cwd", cwd)}
	pkglogger.Warn("unsafe attr spread cwd", attrFields...)
	safeFields := []any{"agent_id", "agent-1"}
	safeFields = append(safeFields, platformshared.SafePathLogFields("args_cwd", cwd)...)
	pkglogger.Warn("safe spread cwd", safeFields...)
	pkglogger.Info("safe cwd", platformshared.SafePathLogFields("cwd", cwd)...)
	pkglogger.Warn("safe payload", platformshared.SafePayloadLogFields("payload", raw)...)
	pkglogger.Warn("safe codex home", platformshared.SafePathLogFields("codex_home", home)...)
	pkglogger.Warn("safe work dir", platformshared.SafePathLogFields("work_dir", home)...)
}
`)
	if err := os.WriteFile(path, source, 0o644); err != nil {
		t.Fatalf("write temp source: %v", err)
	}

	violations, err := structuredLogFileViolations(path, "cmd/mcp-lsp/fx.go")
	if err != nil {
		t.Fatalf("structuredLogFileViolations: %v", err)
	}

	joined := strings.Join(violations, "\n")
	for _, want := range []string{
		"raw runtime log field \"cwd\"",
		"raw runtime log field \"payload\"",
		"raw runtime log field \"codex_home\"",
		"raw runtime log field \"codexHome\"",
		"raw runtime log field \"home\"",
		"raw runtime log field \"work_dir\"",
		"raw runtime log field \"args_cwd\"",
		"raw runtime log field \"launch_cwd\"",
		"raw runtime log field \"attr_cwd\"",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("violations = %q, want %s", joined, want)
		}
	}
	if count := strings.Count(joined, "raw runtime log field"); count != 9 {
		t.Fatalf("violations = %q, want exactly nine raw runtime field violations", joined)
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
	node, parseErr := parser.ParseFile(fset, path, data, parser.SkipObjectResolution)
	if parseErr != nil {
		return nil, parseErr
	}
	violations := structuredLogImportViolations(relSlash, node)
	violations = append(violations, structuredLogStderrViolations(relSlash, data)...)
	violations = append(violations, structuredLogRuntimeFieldViolations(relSlash, fset, node)...)
	return violations, nil
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

func structuredLogRuntimeFieldViolations(relSlash string, fset *token.FileSet, node *ast.File) []string {
	if !structuredLogRuntimeFieldGuardTarget(relSlash) {
		return nil
	}
	var violations []string
	spreadNames := map[string]bool{}
	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !isProductionLogCall(call) {
			return true
		}
		for _, name := range runtimeLogSpreadArgNames(call) {
			spreadNames[name] = true
		}
		violations = append(violations, runtimeLogCallViolations(relSlash, fset, call)...)
		return true
	})
	violations = append(violations, runtimeLogFieldListViolations(relSlash, fset, node, spreadNames)...)
	return violations
}

func structuredLogRuntimeFieldGuardTarget(relSlash string) bool {
	relSlash = filepath.ToSlash(relSlash)
	if strings.HasPrefix(relSlash, "internal/provider/codexapp/") {
		return true
	}
	if strings.HasPrefix(relSlash, "internal/module/thread/") {
		return true
	}
	switch relSlash {
	case "cmd/mcp-lsp/fx.go",
		"cmd/mcp-lsp/tools/tool_file.go",
		"internal/platform/toolbridge/handler_managed_launch.go":
		return true
	default:
		return false
	}
}

func isProductionLogCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	switch sel.Sel.Name {
	case "Debug", "Info", "Warn", "Error":
		return true
	default:
		return false
	}
}

func runtimeLogCallViolations(relSlash string, fset *token.FileSet, call *ast.CallExpr) []string {
	if len(call.Args) < 2 {
		return nil
	}
	var violations []string
	for i, arg := range call.Args[1:] {
		if isSafeLogHelperArg(arg) {
			continue
		}
		if key, ok := rawRuntimeLogFieldFromAttrArg(arg); ok {
			line := fset.Position(arg.Pos()).Line
			violations = append(violations, relSlash+":"+itoa(line)+": raw runtime log field \""+key+"\" must use platform/shared safe log helpers")
			continue
		}
		if i%2 != 0 {
			continue
		}
		if key, ok := stringLiteralValue(arg); ok && isRawRuntimeLogFieldKey(key) {
			line := fset.Position(arg.Pos()).Line
			violations = append(violations, relSlash+":"+itoa(line)+": raw runtime log field \""+key+"\" must use platform/shared safe log helpers")
		}
	}
	return violations
}

func runtimeLogSpreadArgNames(call *ast.CallExpr) []string {
	if !call.Ellipsis.IsValid() || len(call.Args) == 0 {
		return nil
	}
	ident, ok := call.Args[len(call.Args)-1].(*ast.Ident)
	if !ok {
		return nil
	}
	return []string{ident.Name}
}

func runtimeLogFieldListViolations(relSlash string, fset *token.FileSet, node *ast.File, spreadNames map[string]bool) []string {
	if len(spreadNames) == 0 {
		return nil
	}
	var violations []string
	ast.Inspect(node, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, lhs := range assign.Lhs {
			ident, ok := lhs.(*ast.Ident)
			if !ok || !spreadNames[ident.Name] || i >= len(assign.Rhs) {
				continue
			}
			violations = append(violations, runtimeLogFieldExprViolations(relSlash, fset, assign.Rhs[i])...)
		}
		return true
	})
	return violations
}

func runtimeLogFieldExprViolations(relSlash string, fset *token.FileSet, expr ast.Expr) []string {
	switch typed := expr.(type) {
	case *ast.CompositeLit:
		if isRuntimeLogFieldSliceComposite(typed) {
			return rawRuntimeFieldPairViolations(relSlash, fset, typed.Elts...)
		}
	case *ast.CallExpr:
		if isAppendCall(typed) && len(typed.Args) > 1 {
			return rawRuntimeFieldPairViolations(relSlash, fset, typed.Args[1:]...)
		}
	}
	return nil
}

func rawRuntimeFieldPairViolations(relSlash string, fset *token.FileSet, args ...ast.Expr) []string {
	var violations []string
	for i, arg := range args {
		if isSafeLogHelperArg(arg) {
			continue
		}
		if key, ok := rawRuntimeLogFieldFromAttrArg(arg); ok {
			line := fset.Position(arg.Pos()).Line
			violations = append(violations, relSlash+":"+itoa(line)+": raw runtime log field \""+key+"\" must use platform/shared safe log helpers")
			continue
		}
		if i%2 != 0 {
			continue
		}
		if key, ok := stringLiteralValue(arg); ok && isRawRuntimeLogFieldKey(key) {
			line := fset.Position(arg.Pos()).Line
			violations = append(violations, relSlash+":"+itoa(line)+": raw runtime log field \""+key+"\" must use platform/shared safe log helpers")
		}
	}
	return violations
}

func isRuntimeLogFieldSliceComposite(expr *ast.CompositeLit) bool {
	array, ok := expr.Type.(*ast.ArrayType)
	if !ok {
		return false
	}
	if ident, ok := array.Elt.(*ast.Ident); ok {
		return ident.Name == "any"
	}
	if iface, ok := array.Elt.(*ast.InterfaceType); ok {
		return len(iface.Methods.List) == 0
	}
	if sel, ok := array.Elt.(*ast.SelectorExpr); ok {
		if pkg, ok := sel.X.(*ast.Ident); ok {
			return pkg.Name == "slog" && sel.Sel.Name == "Attr"
		}
	}
	return false
}

func isAppendCall(call *ast.CallExpr) bool {
	ident, ok := call.Fun.(*ast.Ident)
	return ok && ident.Name == "append"
}

func rawRuntimeLogFieldFromAttrArg(arg ast.Expr) (string, bool) {
	call, ok := arg.(*ast.CallExpr)
	if !ok || len(call.Args) == 0 {
		return "", false
	}
	key, ok := stringLiteralValue(call.Args[0])
	if ok && isRawRuntimeLogFieldKey(key) {
		return key, true
	}
	return "", false
}

func isSafeLogHelperArg(arg ast.Expr) bool {
	if ellipsis, ok := arg.(*ast.Ellipsis); ok {
		arg = ellipsis.Elt
	}
	call, ok := arg.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	switch sel.Sel.Name {
	case "SafePathLogFields", "SafePayloadLogFields", "SafeRuntimeLogFields":
		return true
	default:
		return false
	}
}

func stringLiteralValue(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	return strings.Trim(lit.Value, "`\""), true
}

func isRawRuntimeLogFieldKey(key string) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	if isSafeRuntimeSummaryKey(lower) {
		return false
	}
	switch lower {
	case "cwd", "root", "path", "payload", "prompt", "config", "config_body", "instructions", "sandbox_policy",
		"stored_cwd", "event_cwd", "session_cwd", "rollout_path", "file_path", "file_paths",
		"fallback_root", "effective_root", "meta_cwd", "first_tool_json",
		"home", "codex_home", "codexhome", "provider_home", "providerhome", "claude_home", "claudehome",
		"work_dir", "workdir":
		return true
	default:
		return strings.HasSuffix(lower, "_cwd") ||
			strings.HasSuffix(lower, "_root") ||
			strings.HasSuffix(lower, "_path") ||
			strings.HasSuffix(lower, "_payload") ||
			strings.HasSuffix(lower, "_home") ||
			strings.HasSuffix(lower, "home")
	}
}

func isSafeRuntimeSummaryKey(key string) bool {
	return strings.HasPrefix(key, "has_") ||
		strings.Contains(key, "_has_") ||
		strings.HasSuffix(key, "_present") ||
		strings.HasSuffix(key, "_bytes") ||
		strings.HasSuffix(key, "_sha256") ||
		strings.HasSuffix(key, "_display_class")
}
