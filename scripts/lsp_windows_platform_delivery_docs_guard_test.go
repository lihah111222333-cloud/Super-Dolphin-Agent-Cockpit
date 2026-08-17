package main

// 本静态交付守卫故意不加 windows build tag：Linux/macOS CI 也必须检查 Windows
// 二进制命名、文档、技能和平台标签，避免跨平台分支悄悄漂移。

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestLSPPlatformDedicatedFilesHaveExplicitBuildTags 从源码层证明平台隔离：Windows、Linux、
// Darwin、Unix/POSIX 与补集实现必须显式带标签；平台专用 E2E 还必须带 e2e 标签。
// 无标签文件只能登记为可跨平台静态/协议实现，并用中文解释共享原因。
func TestLSPPlatformDedicatedFilesHaveExplicitBuildTags(t *testing.T) {
	t.Parallel()
	repoRoot := filepath.Clean("..")
	commonWithoutBuildTag := map[string]struct{}{
		"cmd/mcp-lsp/installer/windows_asset_archive_shared.go":          {},
		"cmd/mcp-lsp/installer/windows_asset_cache_shared.go":            {},
		"cmd/mcp-lsp/installer/windows_asset_cache_shared_test.go":       {},
		"cmd/mcp-lsp/installer/windows_cleanup_failfast_test.go":         {},
		"cmd/mcp-lsp/installer/windows_cleanup_helpers.go":               {},
		"cmd/mcp-lsp/installer/windows_hidden_process_guard_test.go":     {},
		"cmd/mcp-lsp/installer/windows_host_platform_shared.go":          {},
		"cmd/mcp-lsp/installer/windows_host_platform_shared_test.go":     {},
		"cmd/mcp-lsp/installer/windows_locked_asset_shared.go":           {},
		"cmd/mcp-lsp/installer/windows_public_api_comment_guard_test.go": {},
		"cmd/mcp-lsp/multilsp/windows_hidden_process_guard_test.go":      {},
		"cmd/mcp-lsp/search/windows_hidden_process_guard_test.go":        {},
		"cmd/mcp-lsp/internal/hiddenexec/windows_job_policy.go":          {},
		"internal/platform/securefs/windows_errors.go":                   {},
		"internal/platform/securefs/windows_errors_test.go":              {},
		"scripts/ai_maintenance/deferred_e2e.go":                         {},
		"scripts/lsp_windows_platform_delivery_docs_guard_test.go":       {},
		"scripts/mcp_lsp_resource_cohort_e2e_gate_guard_test.go":         {},
		"scripts/package_macos_compat_guard_test.go":                     {},
		"scripts/package_macos_ffmpeg_guard_test.go":                     {},
		"scripts/package_macos_git_hardlink_guard_test.go":               {},
		"scripts/package_macos_guard_test.go":                            {},
		"scripts/package_macos_lsp_prebuild_guard_test.go":               {},
		"scripts/package_macos_release_guard_test.go":                    {},
		"scripts/package_macos_video_guard_test.go":                      {},
		"scripts/package_linux_guard_test.go":                            {},
		"scripts/package_windows_guard_test.go":                          {},
		"scripts/rpc_e2e_coverage_guard_test.go":                         {},
	}
	roots := []string{
		filepath.Join(repoRoot, "cmd", "mcp-lsp"),
		filepath.Join(repoRoot, "internal", "platform"),
		filepath.Join(repoRoot, "scripts"),
	}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".go") {
				return nil
			}
			source, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(repoRoot, path)
			if err != nil {
				return err
			}
			relative = filepath.ToSlash(relative)
			base := strings.ToLower(entry.Name())
			scriptSource := strings.HasPrefix(relative, "scripts/")
			tag := lspGoBuildTag(string(source))
			if lspHasLegacyBuildConstraintWithoutGoBuild(string(source)) {
				t.Errorf("%s 只有旧式 // +build 声明；平台源码必须有精确的 //go:build 声明", relative)
			}
			platform, _, platformDedicated := lspPlatformFileConstraint(base)
			architecture, requiredArchitectureTag, architectureDedicated := lspArchitectureFileConstraint(base)
			namedE2E := lspFileNameHasE2E(base)
			var expression constraint.Expr
			if tag != "" {
				expression, err = constraint.Parse("//go:build " + tag)
				if err != nil {
					t.Errorf("%s 的 build tag %q 无法解析：%v", relative, tag, err)
					return nil
				}
			}
			platformImports, importErr := lspPlatformImportsWithoutMatchingBuildTag(path, source, tag)
			if importErr != nil {
				t.Errorf("解析 %s 的平台专用 import：%v", relative, importErr)
			} else {
				for _, importPath := range platformImports {
					t.Errorf("%s 在没有平台 build tag 的源码中直接导入平台专用包 %q；必须拆到平台可见文件并加精确标签", relative, importPath)
				}
			}
			if !scriptSource && strings.HasPrefix(relative, "cmd/mcp-lsp/") {
				functionViolations, functionErr := lspWindowsE2EFunctionNameViolations(relative, source, tag)
				if functionErr != nil {
					t.Errorf("解析 %s 的平台函数名：%v", relative, functionErr)
				} else {
					for _, violation := range functionViolations {
						t.Errorf("%s 的平台函数名约束：%s", relative, violation)
					}
				}
			}
			if lspBuildTagHasPlatformOrArchitectureAtom(tag) && !platformDedicated && !architectureDedicated {
				t.Errorf("%s 含平台/架构 build tag %q，但文件名没有专用平台/架构标识", relative, tag)
			}
			// LSP、平台公共边界与 workload runner 不得用运行时分支替代平台文件选源。
			// 普通 *_test.go 也进入同一 AST 审计；只有不参与控制流的宿主事实读取/记录
			// 保留为公共测试语义，平台断言与 t.Skip 必须拆到可见文件名和显式 build tag。
			// 守卫本身跨平台运行，确保非 Windows CI 也能发现整个 internal/platform 树的泄漏。
			runtimePlatformControlScope := lspRuntimePlatformControlScope(relative)
			runtimePlatformControlSource := runtimePlatformControlScope && !lspScriptRuntimePlatformControlIsExempt(relative)
			if runtimePlatformControlSource && !lspBuildTagHasPlatformOrArchitectureAtom(tag) {
				controlDetector := lspHasRuntimePlatformControlFlow
				if scriptSource {
					controlDetector = lspHasScriptRuntimePlatformControlFlow
				}
				hasRuntimePlatformControl, err := controlDetector(path, source)
				if err != nil {
					t.Errorf("解析 %s 的运行时平台控制流：%v", relative, err)
				} else if hasRuntimePlatformControl {
					t.Errorf("%s 在无平台 build tag 的公共源码中用 runtime.GOOS/GOARCH 控制行为；必须拆到平台可见文件名和显式 build tag", relative)
				}
			}
			if runtimePlatformControlSource && strings.HasSuffix(relative, "_test.go") &&
				!lspBuildTagHasPlatformOrArchitectureAtom(tag) {
				hasRuntimePlatformSkip, err := lspTestHasRuntimePlatformSkip(path, source)
				if err != nil {
					t.Errorf("解析 %s 的运行时平台 gate：%v", relative, err)
				} else if hasRuntimePlatformSkip {
					t.Errorf("%s 通过 runtime.GOOS/GOARCH + t.Skip 隐藏平台专用测试；必须拆到平台可见文件名并添加显式平台 build tag", relative)
				}
			}
			if tag != "" {
				for _, violation := range lspBuildFileConstraintMatrixViolationsAtPath(path, base, tag, expression) {
					t.Errorf("%s 的 build tag %q：%s", relative, tag, violation)
				}
				for _, violation := range lspFileNameConstraintViolations(base, tag) {
					t.Errorf("%s 的文件名/ build tag 约束：%s", relative, violation)
				}
				for _, violation := range lspUnixPosixFamilyConstraintViolationsAtPath(path, base, tag, expression) {
					t.Errorf("%s 的 unix/posix family 约束：%s", relative, violation)
				}
			}
			switch {
			case tag == "":
				_, registeredCommon := commonWithoutBuildTag[relative]
				if platformDedicated || architectureDedicated {
					if !registeredCommon || architectureDedicated {
						t.Errorf("%s 含平台/架构专用命名（%s/%s）但没有显式 build tag，也未登记为共享实现", relative, platform, architecture)
						return nil
					}
					if !strings.Contains(string(source), "故意不加 windows build tag") && !strings.Contains(string(source), "公共跨平台") {
						t.Errorf("%s 是共享实现，但缺少解释为何不加 build tag 的中文注释", relative)
					}
				}
				if namedE2E {
					if !registeredCommon {
						t.Errorf("%s 文件名含 E2E，但没有显式 e2e build tag，也未登记为公共跨平台守卫/策略", relative)
					} else if !strings.Contains(string(source), "公共跨平台") {
						t.Errorf("%s 是不带 e2e build tag 的公共跨平台守卫/策略，但缺少中文共享语义注释", relative)
					}
				}
				return nil
			default:
				hasE2ETag := lspBuildTagHasPositiveAtom(tag, "e2e")
				if namedE2E && !hasE2ETag {
					t.Errorf("%s 文件名含 E2E，但 build tag %q 没有正向 e2e 原子", relative, tag)
				}
				if hasE2ETag && !namedE2E {
					t.Errorf("%s build tag %q 含正向 e2e 原子，但文件名没有 e2e 标识", relative, tag)
				}
				if platform == "Windows" && architectureDedicated && requiredArchitectureTag == "arm64" && namedE2E {
					for _, violation := range lspWindowsARM64E2EConstraintViolations(tag, expression) {
						t.Errorf("%s 的 ARM64 E2E 约束：%s", relative, violation)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("扫描 LSP 平台源码 %s：%v", root, err)
		}
	}
}

// lspRuntimePlatformControlScope 定义必须接受运行时平台控制流审计的公共源码范围。
// internal/platform 与 scripts 下所有子树都进入审计；scripts 的既有宿主事实 helper
// 只能通过明确登记的边界说明保留，不能让新普通测试绕过包装/派生值检查。
func lspRuntimePlatformControlScope(relative string) bool {
	return strings.HasPrefix(relative, "cmd/mcp-lsp/") ||
		strings.HasPrefix(relative, "scripts/") ||
		strings.HasPrefix(relative, "scripts/mcp_lsp_workload_runner/") ||
		strings.HasPrefix(relative, "internal/platform/")
}

// TestLSPRuntimePlatformControlScopeCoversInternalPlatform 锁定整棵 internal/platform
// 树都进入运行时平台控制审计，避免 observability、sharedfilefs 或新增子目录回归漏检。
func TestLSPRuntimePlatformControlScopeCoversInternalPlatform(t *testing.T) {
	t.Parallel()
	cases := []struct {
		relative string
		want     bool
	}{
		{relative: "cmd/mcp-lsp/runtime.go", want: true},
		{relative: "scripts/sqlite_release_gate_package_smoke_runtime_test.go", want: true},
		{relative: "scripts/mcp_lsp_workload_runner/main.go", want: true},
		{relative: "internal/platform/config/selector.go", want: true},
		{relative: "internal/platform/observability/jsonl_sink.go", want: true},
		{relative: "internal/platform/observability/jsonl_sink_test.go", want: true},
		{relative: "internal/platform/sharedfilefs/disk.go", want: true},
		{relative: "internal/platform/new_boundary/selector.go", want: true},
		{relative: "docs/platform.go", want: false},
		{relative: "internal/provider/platform.go", want: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.relative, func(t *testing.T) {
			if got := lspRuntimePlatformControlScope(testCase.relative); got != testCase.want {
				t.Fatalf("runtime platform control scope(%q) = %t, want %t", testCase.relative, got, testCase.want)
			}
		})
	}
}

// lspScriptRuntimePlatformControlExemptions 记录脚本目录中已有的宿主事实/fixture 辅助，
// 它们不是本轮 LSP package smoke 的平台 launcher；新脚本没有登记就必须接受同一 AST 审计。
// 这些例外只跳过运行时平台控制检查，不跳过 Go 语法、平台 import 或其他源码守卫。
var lspScriptRuntimePlatformControlExemptions = map[string]string{
	"scripts/bash_test_helpers_test.go":                     "WSL/Git 路径 fixture 辅助",
	"scripts/lsp_diagnostics_gate/main_test.go":             "诊断 gate 的宿主矩阵 fixture",
	"scripts/mcp_lsp_workload_catalog/catalog_receipt.go":   "本地回执宿主平台事实校验",
	"scripts/mcp_lsp_workload_catalog/catalog_test.go":      "catalog symlink fixture 与宿主回执测试",
	"scripts/mcp_lsp_workload_catalog_guard_test.go":        "workload catalog 宿主 gate 测试",
	"scripts/package_guard_helpers_test.go":                 "package guard 宿主 helper",
	"scripts/remote_ci_imagecache_refresh_dispatch_test.go": "macOS hook 调度 fixture",
	"scripts/trusted_launcher_test_helpers_test.go":         "系统账户 launcher fixture",
}

func lspScriptRuntimePlatformControlIsExempt(relative string) bool {
	_, exempt := lspScriptRuntimePlatformControlExemptions[relative]
	return exempt
}

// TestLSPScriptRuntimePlatformControlExemptionsReferenceExistingFiles 保证例外表与源码同生共死；
// 已删除或改名的文件不能留下永久豁免，避免未来同名文件绕过公共平台控制流审计。
func TestLSPScriptRuntimePlatformControlExemptionsReferenceExistingFiles(t *testing.T) {
	t.Parallel()
	for relative, reason := range lspScriptRuntimePlatformControlExemptions {
		relative, reason := relative, reason
		t.Run(relative, func(t *testing.T) {
			t.Parallel()
			if strings.TrimSpace(reason) == "" {
				t.Fatal("script runtime platform exemption reason must not be empty")
			}
			if _, err := os.Stat(filepath.Join("..", filepath.FromSlash(relative))); err != nil {
				t.Fatalf("script runtime platform exemption references missing source: %v", err)
			}
		})
	}
}

// lspHasScriptRuntimePlatformControlFlow 在脚本目录补充跨函数不可见的最小数据流：
// runtime.GOOS/GOARCH 或 runtimeGOOS/runtimeGOARCH 的结果先写入 goos/goarch，再参与
// 平台分支或 Skip 时仍必须拆到 tagged 文件；单纯写回执/日志不构成控制流。
func lspHasScriptRuntimePlatformControlFlow(path string, source []byte) (bool, error) {
	direct, err := lspHasRuntimePlatformControlFlow(path, source)
	if err != nil || direct {
		return direct, err
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), path, source, parser.SkipObjectResolution|parser.ParseComments)
	if err != nil {
		return false, err
	}
	runtimeAliases := make(map[string]struct{})
	for _, importSpec := range parsed.Imports {
		importPath, err := strconv.Unquote(importSpec.Path.Value)
		if err != nil {
			return false, fmt.Errorf("解析 import %s: %w", importSpec.Path.Value, err)
		}
		if importPath != "runtime" {
			continue
		}
		alias := "runtime"
		if importSpec.Name != nil {
			alias = importSpec.Name.Name
		}
		if alias != "_" && alias != "." {
			runtimeAliases[alias] = struct{}{}
		}
	}
	platformValues := make(map[string]struct{})
	functionName := func(expression ast.Expr) string {
		switch value := expression.(type) {
		case *ast.Ident:
			return value.Name
		case *ast.SelectorExpr:
			return value.Sel.Name
		default:
			return ""
		}
	}
	containsPlatformSource := func(node ast.Node) bool {
		if node == nil {
			return false
		}
		found := false
		ast.Inspect(node, func(current ast.Node) bool {
			if current == nil || found {
				return false
			}
			if selector, ok := current.(*ast.SelectorExpr); ok && (selector.Sel.Name == "GOOS" || selector.Sel.Name == "GOARCH") {
				identifier, ok := selector.X.(*ast.Ident)
				if ok {
					_, found = runtimeAliases[identifier.Name]
				}
				return !found
			}
			if call, ok := current.(*ast.CallExpr); ok {
				name := strings.ToLower(functionName(call.Fun))
				if name == "runtimegoos" || name == "runtimegoarch" {
					found = true
					return false
				}
			}
			return true
		})
		return found
	}
	collectAssignedPlatformValues := func(node ast.Node) bool {
		switch statement := node.(type) {
		case *ast.AssignStmt:
			for index, value := range statement.Rhs {
				if !containsPlatformSource(value) || index >= len(statement.Lhs) {
					continue
				}
				if name, ok := statement.Lhs[index].(*ast.Ident); ok {
					platformValues[name.Name] = struct{}{}
				}
			}
		case *ast.ValueSpec:
			for index, value := range statement.Values {
				if !containsPlatformSource(value) || index >= len(statement.Names) {
					continue
				}
				platformValues[statement.Names[index].Name] = struct{}{}
			}
		}
		return true
	}
	ast.Inspect(parsed, collectAssignedPlatformValues)
	containsDerivedPlatform := func(node ast.Node) bool {
		if node == nil {
			return false
		}
		if containsPlatformSource(node) {
			return true
		}
		found := false
		ast.Inspect(node, func(current ast.Node) bool {
			if identifier, ok := current.(*ast.Ident); ok {
				_, found = platformValues[identifier.Name]
				return !found
			}
			return true
		})
		return found
	}
	containsPlatformLiteral := func(node ast.Node) bool {
		if node == nil {
			return false
		}
		found := false
		ast.Inspect(node, func(current ast.Node) bool {
			literal, ok := current.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(literal.Value)
			if err == nil && lspIsPlatformOrArchitectureAtom(strings.ToLower(strings.TrimSpace(value))) {
				found = true
				return false
			}
			return true
		})
		return found
	}
	containsTestingSkip := func(node ast.Node) bool {
		if node == nil {
			return false
		}
		found := false
		ast.Inspect(node, func(current ast.Node) bool {
			call, ok := current.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if ok && (selector.Sel.Name == "Skip" || selector.Sel.Name == "Skipf" || selector.Sel.Name == "SkipNow") {
				found = true
				return false
			}
			return true
		})
		return found
	}
	found := false
	ast.Inspect(parsed, func(node ast.Node) bool {
		if node == nil || found {
			return false
		}
		switch current := node.(type) {
		case *ast.IfStmt:
			found = containsDerivedPlatform(current.Cond) && containsPlatformLiteral(current.Cond) &&
				(containsTestingSkip(current.Body) || containsTestingSkip(current.Else) || current.Else == nil)
		case *ast.ForStmt:
			found = containsDerivedPlatform(current.Cond) && containsPlatformLiteral(current.Cond)
		case *ast.SwitchStmt:
			if containsDerivedPlatform(current.Tag) {
				found = true
				break
			}
			for _, statement := range current.Body.List {
				clause, ok := statement.(*ast.CaseClause)
				if !ok {
					continue
				}
				for _, expression := range clause.List {
					if containsDerivedPlatform(expression) || (containsPlatformLiteral(expression) && containsDerivedPlatform(current.Tag)) {
						found = true
						break
					}
				}
				if found {
					break
				}
			}
		}
		return !found
	})
	return found, nil
}

// TestLSPScriptRuntimePlatformControlFlowCoversWrappersAndDerivedValues 锁定普通 scripts
// 的包装函数与派生 goos/goarch 仍受源码门禁约束，同时保留纯宿主事实读取。
func TestLSPScriptRuntimePlatformControlFlowCoversWrappersAndDerivedValues(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
		want   bool
	}{
		{
			name: "wrapper_skip",
			source: `package fixture
import "testing"
func TestPlatform(t *testing.T) {
	if runtimeGOOS() == "windows" { t.Skip("platform fixture") }
}`,
			want: true,
		},
		{
			name: "derived_goos_branch",
			source: `package fixture
import "runtime"
func current() string {
	goos := runtime.GOOS
	if goos == "darwin" { return "app" }
	return "plain"
}`,
			want: true,
		},
		{
			name: "derived_goarch_switch",
			source: `package fixture
import "runtime"
func current() string {
	goarch := runtime.GOARCH
	switch goarch { case "arm64": return "native" }
	return "other"
}`,
			want: true,
		},
		{
			name: "host_fact_only",
			source: `package fixture
func current() string { return runtimeGOOS() }
`,
			want: false,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := lspHasScriptRuntimePlatformControlFlow(testCase.name+".go", []byte(testCase.source))
			if err != nil {
				t.Fatalf("parse script runtime platform fixture: %v", err)
			}
			if got != testCase.want {
				t.Fatalf("script runtime platform control = %t, want %t", got, testCase.want)
			}
		})
	}
}

// lspPlatformImportsWithoutMatchingBuildTag 用 Go AST 检查源码是否没有相应平台标签就
// 直接依赖已知的平台专用包；只检查明确的 x/sys 包，避免把普通 syscall import 误报。
func lspPlatformImportsWithoutMatchingBuildTag(path string, source []byte, tag string) ([]string, error) {
	parsed, err := parser.ParseFile(token.NewFileSet(), path, source, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	knownPlatformImports := map[string]struct{}{
		"golang.org/x/sys/unix":    {},
		"golang.org/x/sys/windows": {},
	}
	imports := make([]string, 0, 1)
	for _, importSpec := range parsed.Imports {
		importPath, err := strconv.Unquote(importSpec.Path.Value)
		if err != nil {
			return nil, fmt.Errorf("解析 import %s：%w", importSpec.Path.Value, err)
		}
		if _, known := knownPlatformImports[importPath]; known && !lspPlatformImportHasMatchingBuildTag(importPath, tag) {
			imports = append(imports, importPath)
		}
	}
	return imports, nil
}

// lspWindowsE2EFunctionNameViolations 防止 E2E 函数名宣称某个 Windows 架构，
// 却在可被其他架构选中的源码文件中实现。静态资产矩阵可以保留 Windows 公共文件，
// 但宿主架构行为必须由精确的 windows && <arch> && e2e 文件承载。
func lspWindowsE2EFunctionNameViolations(path string, source []byte, tag string) ([]string, error) {
	if !strings.HasSuffix(strings.ToLower(filepath.ToSlash(path)), "_e2e_test.go") {
		return nil, nil
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), path, source, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	violations := make([]string, 0)
	base := strings.ToLower(filepath.Base(path))
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv != nil || function.Name == nil {
			continue
		}
		name := strings.ToLower(function.Name.Name)
		if !strings.Contains(name, "windows") {
			continue
		}
		architectureTag := ""
		architectureMarkers := []string(nil)
		switch {
		case strings.Contains(name, "arm64"):
			architectureTag = "windows && arm64 && e2e"
			architectureMarkers = []string{"windows", "arm64"}
		case strings.Contains(name, "amd64") || strings.Contains(name, "x64"):
			architectureTag = "windows && amd64 && e2e"
			architectureMarkers = []string{"windows", "amd64", "x64"}
		case strings.Contains(name, "386") || strings.Contains(name, "x86"):
			architectureTag = "windows && 386 && e2e"
			architectureMarkers = []string{"windows", "386", "x86"}
		default:
			continue
		}
		if tag != architectureTag {
			violations = append(violations, fmt.Sprintf("测试函数 %s 含架构标识，但源码 gate=%q；必须使用精确 %s", function.Name.Name, tag, architectureTag))
		}
		if !strings.Contains(base, architectureMarkers[0]) {
			violations = append(violations, fmt.Sprintf("测试函数 %s 含 Windows 架构标识，但文件名 %q 缺少 windows 标识", function.Name.Name, base))
		}
		architectureNamed := false
		for _, marker := range architectureMarkers[1:] {
			if strings.Contains(base, marker) {
				architectureNamed = true
				break
			}
		}
		if !architectureNamed {
			violations = append(violations, fmt.Sprintf("测试函数 %s 含架构标识，但文件名 %q 未显示对应架构", function.Name.Name, base))
		}
	}
	return violations, nil
}

func TestLSPWindowsARM64E2EFunctionNamesRequireExactSourceGate(t *testing.T) {
	cases := []struct {
		name       string
		path       string
		source     string
		tag        string
		wantErrors bool
	}{
		{
			name:       "ARM64 test in broad Windows E2E is rejected",
			path:       "windows_arm64_e2e_test.go",
			source:     "package main\nfunc TestWindowsARM64Download(t *testing.T) {}\n",
			tag:        "windows && e2e",
			wantErrors: true,
		},
		{
			name:   "ARM64 test has exact gate",
			path:   "windows_arm64_e2e_test.go",
			source: "package main\nfunc TestWindowsARM64Download(t *testing.T) {}\n",
			tag:    "windows && arm64 && e2e",
		},
		{
			name:   "all native static matrix has neutral name",
			path:   "windows_e2e_test.go",
			source: "package main\nfunc TestWindowsAllNativeArchitectureAssets(t *testing.T) {}\n",
			tag:    "windows && e2e",
		},
		{
			name:       "x64 requires amd64 gate",
			path:       "windows_x64_e2e_test.go",
			source:     "package main\nfunc TestWindowsX64Runtime(t *testing.T) {}\n",
			tag:        "windows && e2e",
			wantErrors: true,
		},
		{
			name:   "x86 requires 386 gate",
			path:   "windows_386_e2e_test.go",
			source: "package main\nfunc TestWindowsX86Runtime(t *testing.T) {}\n",
			tag:    "windows && 386 && e2e",
		},
		{
			name:   "Linux ARM64 is outside Windows rule",
			path:   "linux_arm64_e2e_test.go",
			source: "package main\nfunc TestLinuxARM64Runtime(t *testing.T) {}\n",
			tag:    "linux && arm64 && e2e",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := lspWindowsE2EFunctionNameViolations(testCase.path, []byte(testCase.source), testCase.tag)
			if err != nil {
				t.Fatalf("lspWindowsE2EFunctionNameViolations() error = %v", err)
			}
			if (len(got) != 0) != testCase.wantErrors {
				t.Fatalf("violations=%v, wantErrors=%t", got, testCase.wantErrors)
			}
		})
	}
}

// lspHasAtLeast15MinuteLifecycleConstant 通过 AST 查找与 time.Minute 相乘的整数常量，
// 避免仅凭注释或 receipt 文本把短诊断误报为正式生命周期证明。
func lspHasAtLeast15MinuteLifecycleConstant(path string, source []byte) (bool, error) {
	parsed, err := parser.ParseFile(token.NewFileSet(), path, source, parser.SkipObjectResolution)
	if err != nil {
		return false, err
	}
	found := false
	ast.Inspect(parsed, func(node ast.Node) bool {
		if found || node == nil {
			return !found
		}
		binary, ok := node.(*ast.BinaryExpr)
		if !ok || binary.Op != token.MUL {
			return true
		}
		leftMinute := lspIsTimeMinute(binary.X)
		rightMinute := lspIsTimeMinute(binary.Y)
		if !leftMinute && !rightMinute {
			return true
		}
		multiplier := binary.Y
		if rightMinute {
			multiplier = binary.X
		}
		literal, ok := multiplier.(*ast.BasicLit)
		if !ok || literal.Kind != token.INT {
			return true
		}
		value, parseErr := strconv.Atoi(literal.Value)
		if parseErr == nil && value >= 15 {
			found = true
		}
		return !found
	})
	return found, nil
}

func lspIsTimeMinute(expression ast.Expr) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	return ok && selector.Sel != nil && selector.Sel.Name == "Minute" &&
		selector.X != nil && selectorIdentName(selector.X) == "time"
}

func selectorIdentName(expression ast.Expr) string {
	ident, ok := expression.(*ast.Ident)
	if !ok {
		return ""
	}
	return ident.Name
}

// lspSourceContainsNonPassStringLiteral 只接受源码 AST 中真实字符串字面量的 NON_PASS，
// 不接受注释、函数名、变量名或任意未解析文本，确保短分支的 receipt/log 不能靠注释造证据。
func lspSourceContainsNonPassStringLiteral(path string, source []byte) (bool, error) {
	parsed, err := parser.ParseFile(token.NewFileSet(), path, source, parser.SkipObjectResolution)
	if err != nil {
		return false, err
	}
	found := false
	ast.Inspect(parsed, func(node ast.Node) bool {
		if found || node == nil {
			return !found
		}
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		value, unquoteErr := strconv.Unquote(literal.Value)
		if unquoteErr == nil && strings.Contains(value, "NON_PASS") {
			found = true
		}
		return !found
	})
	return found, nil
}

// lspSourceForcesGOWORKOff 在任意宿主上静态检查正式 E2E 源码；这个公共守卫故意
// 不带平台 build tag，确保 Linux、macOS 与 Windows 的普通 CI 都能阻止证明代码
// 通过 GOWORK=off 绕过真实工作区语义。
func lspSourceForcesGOWORKOff(path string, source []byte) (bool, error) {
	parsed, err := parser.ParseFile(token.NewFileSet(), path, source, parser.SkipObjectResolution)
	if err != nil {
		return false, err
	}
	literalString := func(expression ast.Expr) (string, bool) {
		literal, ok := expression.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return "", false
		}
		value, unquoteErr := strconv.Unquote(literal.Value)
		return value, unquoteErr == nil
	}
	forced := false
	ast.Inspect(parsed, func(node ast.Node) bool {
		if forced || node == nil {
			return !forced
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		for index, argument := range call.Args {
			value, literal := literalString(argument)
			if literal && strings.EqualFold(strings.TrimSpace(value), "GOWORK=off") {
				forced = true
				return false
			}
			if index+1 >= len(call.Args) || !literal || !strings.EqualFold(strings.TrimSpace(value), "GOWORK") {
				continue
			}
			next, nextLiteral := literalString(call.Args[index+1])
			if nextLiteral && strings.EqualFold(strings.TrimSpace(next), "off") {
				forced = true
				return false
			}
		}
		return true
	})
	return forced, nil
}

// TestLSPWindowsARM64E2EProofContracts 锁定正式生命周期与短诊断的证据边界：正式
// 文件必须有 AST 可见的至少 15 分钟常量；所有 precheck/targeted/cache-only 路径必须
// 用 NON_PASS 标记，避免 receipt 的 complete/ready 文本被误认成正式 PASS。
func TestLSPWindowsARM64E2EProofContracts(t *testing.T) {
	contracts := []struct {
		path           string
		formal         bool
		requireNonPass bool
	}{
		{path: "cmd/mcp-lsp/installer/host_platform_windows_arm64_e2e_test.go", requireNonPass: true},
		{path: "cmd/mcp-lsp/lsp_binary_real_prisma_windows_arm64_raw_protocol_matrix_e2e_test.go", requireNonPass: true},
		{path: "cmd/mcp-lsp/lsp_binary_windows_arm64_emmylua_e2e_test.go", formal: true, requireNonPass: true},
		{path: "cmd/mcp-lsp/windows_arm64_process_arm64_csharp_mcp_36_soak_e2e_test.go", formal: true, requireNonPass: true},
		{path: "cmd/mcp-lsp/windows_arm64_process_arm64_go_sqls_36_action_e2e_test.go", formal: true, requireNonPass: true},
		{path: "cmd/mcp-lsp/windows_arm64_process_arm64_gopls_4x36_soak_e2e_test.go", formal: true, requireNonPass: true},
		{path: "cmd/mcp-lsp/windows_arm64_process_arm64_java_mcp_36_soak_e2e_test.go", formal: true, requireNonPass: true},
		{path: "cmd/mcp-lsp/windows_arm64_process_arm64_markdown_soak_15m_e2e_test.go", formal: true},
		{path: "cmd/mcp-lsp/windows_arm64_process_arm64_native_catalog_15x36_soak_e2e_test.go", formal: true, requireNonPass: true},
		{path: "cmd/mcp-lsp/windows_arm64_process_arm64_native_catalog_clangd_c_cold_diagnostic_e2e_test.go", requireNonPass: true},
		{path: "cmd/mcp-lsp/windows_arm64_process_arm64_native_catalog_resource_diagnostics_e2e_test.go", requireNonPass: true},
		{path: "cmd/mcp-lsp/windows_arm64_process_arm64_node_17x36_soak_e2e_test.go", formal: true, requireNonPass: true},
		{path: "cmd/mcp-lsp/windows_arm64_process_arm64_ruby_36_soak_e2e_test.go", formal: true, requireNonPass: true},
		{path: "cmd/mcp-lsp/windows_arm64_process_arm64_swift_mcp_36_soak_e2e_test.go", formal: true, requireNonPass: true},
		{path: "cmd/mcp-lsp/windows_arm64_process_arm64_vue_cache_prep_e2e_test.go", requireNonPass: true},
		{path: "cmd/mcp-lsp/windows_arm64_process_arm64_vue_mcp_36_soak_e2e_test.go", formal: true},
		{path: "cmd/mcp-lsp/installer/windows_arm64_process_arm64_rustfmt_local_import_e2e_test.go", requireNonPass: true},
		{path: "cmd/mcp-lsp/installer/windows_arm64_process_arm64_terraform_cli_install_e2e_test.go", requireNonPass: true},
	}
	knownContracts := make(map[string]struct{}, len(contracts))
	for _, contract := range contracts {
		knownContracts[contract.path] = struct{}{}
	}
	for _, contract := range contracts {
		contract := contract
		t.Run(contract.path, func(t *testing.T) {
			source := readRepoFile(t, filepath.Join("..", filepath.FromSlash(contract.path)))
			base := strings.ToLower(filepath.Base(contract.path))
			for _, fragment := range []string{"windows", "arm64", "e2e"} {
				if !strings.Contains(base, fragment) {
					t.Fatalf("%s filename missing platform/lifecycle marker %q", contract.path, fragment)
				}
			}
			if got := lspGoBuildTag(source); got != "windows && arm64 && e2e" {
				t.Fatalf("%s source gate=%q, want exact windows && arm64 && e2e", contract.path, got)
			}
			if contract.formal {
				ok, err := lspHasAtLeast15MinuteLifecycleConstant(contract.path, []byte(source))
				if err != nil {
					t.Fatalf("parse %s lifecycle constants: %v", contract.path, err)
				}
				if !ok {
					t.Fatalf("%s has no AST-visible lifecycle duration >= 15*time.Minute", contract.path)
				}
				forcesGOWORKOff, err := lspSourceForcesGOWORKOff(contract.path, []byte(source))
				if err != nil {
					t.Fatalf("parse %s GOWORK policy: %v", contract.path, err)
				}
				if forcesGOWORKOff {
					t.Fatalf("%s forces GOWORK=off; formal proof must exercise the inherited real workspace semantics", contract.path)
				}
			}
			if contract.requireNonPass {
				hasNonPass, err := lspSourceContainsNonPassStringLiteral(contract.path, []byte(source))
				if err != nil {
					t.Fatalf("parse %s NON_PASS markers: %v", contract.path, err)
				}
				if !hasNonPass {
					t.Fatalf("%s short/precheck evidence has no NON_PASS string-literal status marker", contract.path)
				}
			}
			if strings.HasSuffix(contract.path, "vue_cache_prep_e2e_test.go") && !strings.Contains(source, "NON_PASS_cache_only_ready") {
				t.Fatalf("%s cache-only receipt lacks explicit NON_PASS_cache_only_ready status", contract.path)
			}
		})
	}
	arm64Root := filepath.Join("..", "cmd", "mcp-lsp")
	if err := filepath.WalkDir(arm64Root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".go") {
			return nil
		}
		base := strings.ToLower(entry.Name())
		if !strings.Contains(base, "windows") || !strings.Contains(base, "arm64") || !strings.Contains(base, "e2e") {
			return nil
		}
		relative, err := filepath.Rel("..", path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if _, ok := knownContracts[relative]; !ok {
			t.Errorf("未登记 Windows ARM64 E2E 证据契约：%s；必须明确是 >=15m formal 或含 NON_PASS 的 targeted/precheck", relative)
		}
		return nil
	}); err != nil {
		t.Fatalf("扫描 Windows ARM64 E2E 证据契约：%v", err)
	}
}

func TestLSPFormalE2EGOWORKOffDetection(t *testing.T) {
	cases := []struct {
		name   string
		source string
		want   bool
	}{
		{name: "combined environment entry", source: `package p; func f() { println("GOWORK=off") }`, want: true},
		{name: "key and value arguments", source: `package p; func f() { setenv("GOWORK", "off") }`, want: true},
		{name: "unrelated off value", source: `package p; func f() { setenv("FEATURE", "off") }`, want: false},
		{name: "gowork language label", source: `package p; const language = "gowork"`, want: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := lspSourceForcesGOWORKOff("synthetic.go", []byte(testCase.source))
			if err != nil {
				t.Fatalf("lspSourceForcesGOWORKOff() error = %v", err)
			}
			if got != testCase.want {
				t.Fatalf("lspSourceForcesGOWORKOff() = %t, want %t", got, testCase.want)
			}
		})
	}
}

func TestLSPWindowsARM64LifecycleMinimumASTContract(t *testing.T) {
	cases := []struct {
		name   string
		source string
		want   bool
	}{
		{name: "exact fifteen minutes", source: "package p; import \"time\"; const idle = 15 * time.Minute", want: true},
		{name: "longer than fifteen minutes", source: "package p; import \"time\"; const idle = 20 * time.Minute", want: true},
		{name: "fourteen minutes is short", source: "package p; import \"time\"; const idle = 14 * time.Minute", want: false},
		{name: "comment is not proof", source: "package p; // 15 * time.Minute\nconst idle = 30", want: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := lspHasAtLeast15MinuteLifecycleConstant("synthetic.go", []byte(testCase.source))
			if err != nil {
				t.Fatalf("lspHasAtLeast15MinuteLifecycleConstant() error = %v", err)
			}
			if got != testCase.want {
				t.Fatalf("lspHasAtLeast15MinuteLifecycleConstant() = %t, want %t", got, testCase.want)
			}
		})
	}
}

func TestLSPWindowsARM64NonPassMarkerRequiresStringLiteral(t *testing.T) {
	cases := []struct {
		name   string
		source string
		want   bool
	}{
		{name: "status string", source: "package p; const status = \"NON_PASS_precheck\"", want: true},
		{name: "log string", source: "package p; func f() { println(\"NON_PASS targeted\") }", want: true},
		{name: "comment only", source: "package p; // NON_PASS\nconst status = \"ready\"", want: false},
		{name: "identifier only", source: "package p; const NON_PASS = 1", want: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := lspSourceContainsNonPassStringLiteral("synthetic.go", []byte(testCase.source))
			if err != nil {
				t.Fatalf("lspSourceContainsNonPassStringLiteral() error = %v", err)
			}
			if got != testCase.want {
				t.Fatalf("lspSourceContainsNonPassStringLiteral() = %t, want %t", got, testCase.want)
			}
		})
	}
}

// TestLSPE2EShortIdleOverridesRequireTaggedNonPassBuilder 扫描所有 mcp-lsp E2E，
// 不限 Windows：小于十五分钟的 idle 只能由专用 build tag 构建，并由公共 helper
// 在运行日志中明确写出 NON_PASS，不能让一次快速机制测试冒充正式生命周期证明。
func TestLSPE2EShortIdleOverridesRequireTaggedNonPassBuilder(t *testing.T) {
	helperContracts := []struct {
		path   string
		helper string
	}{
		{path: "cmd/mcp-lsp/lsp_binary_completion_e2e_test.go", helper: "buildMcpLSPShortIdlePrecheckBinaryForTest"},
		{path: "cmd/mcp-lsp/lsp_binary_windows_gopls_test_support_e2e_test.go", helper: "buildWindowsGoplsShortIdlePrecheckTestInstall"},
	}
	for _, contract := range helperContracts {
		source := readRepoFile(t, filepath.Join("..", filepath.FromSlash(contract.path)))
		for _, marker := range []string{contract.helper, "mcp_lsp_short_idle_precheck", "NON_PASS_PRECHECK_ONLY"} {
			if !strings.Contains(source, marker) {
				t.Errorf("%s 的 short-idle helper 缺少 %q；专用产物必须带 tag 并在日志标记 NON_PASS", contract.path, marker)
			}
		}
	}

	root := filepath.Join("..", "cmd", "mcp-lsp")
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), "_e2e_test.go") {
			return nil
		}
		source, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		durations, parseErr := lspE2EIdleTimeoutDurations(path, source)
		if parseErr != nil {
			return parseErr
		}
		for _, duration := range durations {
			if duration >= 15*time.Minute {
				continue
			}
			text := string(source)
			usesLoggedHelper := strings.Contains(text, "buildMcpLSPShortIdlePrecheckBinaryForTest") ||
				strings.Contains(text, "buildWindowsGoplsShortIdlePrecheckTestInstall")
			localTaggedNonPass := strings.Contains(text, "mcp_lsp_short_idle_precheck") &&
				strings.Contains(text, "NON_PASS")
			if !usesLoggedHelper && !localTaggedNonPass {
				relative, relErr := filepath.Rel("..", path)
				if relErr != nil {
					return relErr
				}
				t.Errorf("%s 注入 short idle=%s，却没有使用带 NON_PASS 日志的专用 precheck build；普通生产二进制最低为 15m", filepath.ToSlash(relative), duration)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("扫描 mcp-lsp E2E short idle：%v", err)
	}
}

func TestLSPE2EIdleTimeoutDurationExtraction(t *testing.T) {
	source := []byte(`package fixture
import "time"
const short = 6 * time.Minute
const formal = 15 * time.Minute
var values = []string{
	"MCP_LSP_IDLE_TIMEOUT=1s",
	"MCP_LSP_IDLE_TIMEOUT=" + short.String(),
	"MCP_LSP_IDLE_TIMEOUT=" + formal.String(),
}`)
	durations, err := lspE2EIdleTimeoutDurations("synthetic_e2e_test.go", source)
	if err != nil {
		t.Fatal(err)
	}
	want := []time.Duration{time.Second, 6 * time.Minute, 15 * time.Minute}
	if len(durations) != len(want) {
		t.Fatalf("idle durations=%v, want %v", durations, want)
	}
	for index := range want {
		if durations[index] != want[index] {
			t.Fatalf("idle durations[%d]=%s, want %s", index, durations[index], want[index])
		}
	}
}

func lspE2EIdleTimeoutDurations(path string, source []byte) ([]time.Duration, error) {
	parsed, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil {
		return nil, err
	}
	constants := make(map[string]time.Duration)
	for _, declaration := range parsed.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.CONST {
			continue
		}
		for _, spec := range general.Specs {
			values, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for index, name := range values.Names {
				if index >= len(values.Values) {
					continue
				}
				if duration, ok := lspStaticDuration(values.Values[index], constants); ok {
					constants[name.Name] = duration
				}
			}
		}
	}

	var durations []time.Duration
	ast.Inspect(parsed, func(node ast.Node) bool {
		switch expression := node.(type) {
		case *ast.BasicLit:
			if expression.Kind != token.STRING {
				return true
			}
			value, unquoteErr := strconv.Unquote(expression.Value)
			if unquoteErr != nil {
				return true
			}
			if duration, ok := lspShortIdleDurationFromLiteral(value); ok {
				durations = append(durations, duration)
			}
		case *ast.BinaryExpr:
			if expression.Op != token.ADD || !lspIdleTimeoutPrefixExpression(expression.X) {
				return true
			}
			call, ok := expression.Y.(*ast.CallExpr)
			if !ok || len(call.Args) != 0 {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "String" {
				return true
			}
			if duration, ok := lspStaticDuration(selector.X, constants); ok {
				durations = append(durations, duration)
			}
		}
		return true
	})
	return durations, nil
}

func lspShortIdleDurationFromLiteral(value string) (time.Duration, bool) {
	for _, key := range []string{"MCP_LSP_IDLE_TIMEOUT=", "MCP_LSP_GOPLS_DAEMON_IDLE_TIMEOUT="} {
		if raw, ok := strings.CutPrefix(value, key); ok {
			duration, err := time.ParseDuration(strings.TrimSpace(raw))
			return duration, err == nil
		}
	}
	return 0, false
}

func lspIdleTimeoutPrefixExpression(expression ast.Expr) bool {
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return false
	}
	value, err := strconv.Unquote(literal.Value)
	if err != nil {
		return false
	}
	return value == "MCP_LSP_IDLE_TIMEOUT=" || value == "MCP_LSP_GOPLS_DAEMON_IDLE_TIMEOUT="
}

func lspStaticDuration(expression ast.Expr, constants map[string]time.Duration) (time.Duration, bool) {
	switch typed := expression.(type) {
	case *ast.ParenExpr:
		return lspStaticDuration(typed.X, constants)
	case *ast.Ident:
		duration, ok := constants[typed.Name]
		return duration, ok
	case *ast.BinaryExpr:
		if typed.Op != token.MUL {
			return 0, false
		}
		left, leftOK := lspStaticInteger(typed.X)
		right, rightOK := lspTimeDurationUnit(typed.Y)
		if !leftOK || !rightOK {
			left, leftOK = lspStaticInteger(typed.Y)
			right, rightOK = lspTimeDurationUnit(typed.X)
		}
		if leftOK && rightOK {
			return time.Duration(left) * right, true
		}
	}
	return 0, false
}

func lspStaticInteger(expression ast.Expr) (int64, bool) {
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.INT {
		return 0, false
	}
	value, err := strconv.ParseInt(literal.Value, 0, 64)
	return value, err == nil
}

func lspTimeDurationUnit(expression ast.Expr) (time.Duration, bool) {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok {
		return 0, false
	}
	packageName, ok := selector.X.(*ast.Ident)
	if !ok || packageName.Name != "time" {
		return 0, false
	}
	switch selector.Sel.Name {
	case "Nanosecond":
		return time.Nanosecond, true
	case "Microsecond":
		return time.Microsecond, true
	case "Millisecond":
		return time.Millisecond, true
	case "Second":
		return time.Second, true
	case "Minute":
		return time.Minute, true
	case "Hour":
		return time.Hour, true
	default:
		return 0, false
	}
}

func lspPlatformImportHasMatchingBuildTag(importPath, tag string) bool {
	if strings.TrimSpace(tag) == "" {
		return false
	}
	expression, err := constraint.Parse("//go:build " + tag)
	if err != nil {
		return false
	}
	selected, err := lspBuildConstraintSelectedContexts(expression)
	if err != nil || len(selected) == 0 {
		return false
	}
	switch importPath {
	case "golang.org/x/sys/windows":
		for _, candidate := range selected {
			if candidate.goos != "windows" {
				return false
			}
		}
		return true
	case "golang.org/x/sys/unix":
		for _, candidate := range selected {
			if candidate.goos == "windows" {
				return false
			}
		}
		return true
	}
	return false
}

// TestLSPPlatformOnlyImportsRequireBuildTag 锁定 AST import 守卫：未加平台标签的
// windows/unix 直接 import 必须失败，而带精确标签、字符串文字和普通 syscall 不得误报。
func TestLSPPlatformOnlyImportsRequireBuildTag(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		tag    string
		source string
		want   []string
	}{
		{
			name: "untagged_windows_import",
			source: `package fixture
import "golang.org/x/sys/windows"
`,
			want: []string{"golang.org/x/sys/windows"},
		},
		{
			name: "untagged_unix_import_alias",
			source: `package fixture
import platformUnix "golang.org/x/sys/unix"
`,
			want: []string{"golang.org/x/sys/unix"},
		},
		{
			name: "tagged_windows_import",
			tag:  "windows",
			source: `//go:build windows

package fixture
import "golang.org/x/sys/windows"
`,
		},
		{
			name: "architecture_only_is_not_platform_tag",
			tag:  "arm64",
			source: `//go:build arm64

package fixture
import "golang.org/x/sys/windows"
`,
			want: []string{"golang.org/x/sys/windows"},
		},
		{
			name: "tagged_unix_import",
			tag:  "darwin || linux",
			source: `//go:build darwin || linux

package fixture
			import "golang.org/x/sys/unix"
`,
		},
		{
			name: "windows_or_linux_windows_import_is_not_narrow",
			tag:  "windows || linux",
			source: `//go:build windows || linux

package fixture
import "golang.org/x/sys/windows"
`,
			want: []string{"golang.org/x/sys/windows"},
		},
		{
			name: "windows_or_e2e_windows_import_is_not_narrow",
			tag:  "windows || e2e",
			source: `//go:build windows || e2e

package fixture
import "golang.org/x/sys/windows"
`,
			want: []string{"golang.org/x/sys/windows"},
		},
		{
			name: "not_windows_unix_import_is_narrow",
			tag:  "!windows",
			source: `//go:build !windows

package fixture
import "golang.org/x/sys/unix"
`,
		},
		{
			name: "not_windows_windows_import_is_wrong",
			tag:  "!windows",
			source: `//go:build !windows

package fixture
import "golang.org/x/sys/windows"
`,
			want: []string{"golang.org/x/sys/windows"},
		},
		{
			name: "wrong_platform_for_windows_import",
			tag:  "linux",
			source: `//go:build linux

package fixture
import "golang.org/x/sys/windows"
`,
			want: []string{"golang.org/x/sys/windows"},
		},
		{
			name: "wrong_platform_for_unix_import",
			tag:  "windows",
			source: `//go:build windows

package fixture
import "golang.org/x/sys/unix"
`,
			want: []string{"golang.org/x/sys/unix"},
		},
		{
			name: "ordinary_syscall_import",
			source: `package fixture
import "syscall"
`,
		},
		{
			name: "string_literal_is_not_import",
			source: `package fixture
const platformImport = "golang.org/x/sys/windows"
`,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := lspPlatformImportsWithoutMatchingBuildTag(testCase.name+".go", []byte(testCase.source), testCase.tag)
			if err != nil {
				t.Fatalf("parse platform imports: %v", err)
			}
			if strings.Join(got, "\x00") != strings.Join(testCase.want, "\x00") {
				t.Fatalf("platform imports without build tag = %v, want %v", got, testCase.want)
			}
		})
	}
}

// lspHasRuntimePlatformControlFlow 用 Go AST 识别未加平台 build tag 的源码是否直接用
// runtime.GOOS/GOARCH 控制 if/switch，或把宿主事实传给名称/注释明确的平台 selector。
// 纯映射必须在函数注释中说明只按显式参数、无系统调用；日志、回执和普通事实转发不属于平台实现。
func lspHasRuntimePlatformControlFlow(path string, source []byte) (bool, error) {
	parsed, err := parser.ParseFile(token.NewFileSet(), path, source, parser.SkipObjectResolution|parser.ParseComments)
	if err != nil {
		return false, err
	}
	runtimeAliases := make(map[string]struct{})
	for _, importSpec := range parsed.Imports {
		importPath, err := strconv.Unquote(importSpec.Path.Value)
		if err != nil {
			return false, fmt.Errorf("解析 import %s: %w", importSpec.Path.Value, err)
		}
		if importPath != "runtime" {
			continue
		}
		alias := "runtime"
		if importSpec.Name != nil {
			alias = importSpec.Name.Name
		}
		if alias != "_" && alias != "." {
			runtimeAliases[alias] = struct{}{}
		}
	}
	if len(runtimeAliases) == 0 {
		return false, nil
	}
	containsRuntimePlatform := func(node ast.Node) bool {
		if node == nil {
			return false
		}
		found := false
		ast.Inspect(node, func(current ast.Node) bool {
			if current == nil || found {
				return false
			}
			selector, ok := current.(*ast.SelectorExpr)
			if !ok || (selector.Sel.Name != "GOOS" && selector.Sel.Name != "GOARCH") {
				return true
			}
			identifier, ok := selector.X.(*ast.Ident)
			if !ok {
				return true
			}
			_, found = runtimeAliases[identifier.Name]
			return !found
		})
		return found
	}
	containsPlatformLiteral := func(node ast.Node) bool {
		if node == nil {
			return false
		}
		found := false
		ast.Inspect(node, func(current ast.Node) bool {
			if current == nil || found {
				return false
			}
			literal, ok := current.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(literal.Value)
			if err != nil {
				return true
			}
			value = strings.ToLower(strings.TrimSpace(value))
			if lspIsPlatformOrArchitectureAtom(value) {
				found = true
				return false
			}
			return true
		})
		return found
	}
	pureMappingFunctions := make(map[string]struct{})
	commentedPlatformSelectors := make(map[string]struct{})
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name == nil || function.Doc == nil {
			continue
		}
		comment := strings.ToLower(function.Doc.Text())
		if strings.Contains(comment, "纯映射") ||
			strings.Contains(comment, "只按显式参数") ||
			strings.Contains(comment, "无系统调用") {
			pureMappingFunctions[function.Name.Name] = struct{}{}
		}
		if strings.Contains(comment, "选择器") ||
			strings.Contains(comment, "platform selector") ||
			strings.Contains(comment, "平台 selector") ||
			strings.Contains(comment, "selector") {
			commentedPlatformSelectors[function.Name.Name] = struct{}{}
		}
	}
	platformSelectorCallName := func(name string) bool {
		if name == "" {
			return false
		}
		if _, ok := commentedPlatformSelectors[name]; ok {
			return true
		}
		lowerName := strings.ToLower(name)
		if strings.Contains(lowerName, "forplatform") ||
			strings.Contains(lowerName, "fortarget") ||
			strings.Contains(lowerName, "forproduction") ||
			strings.Contains(lowerName, "architecture") {
			return true
		}
		// ForOS/OnOS/CurrentPlatform 是平台选择器的常见命名，即使函数名没有 runtime
		// 前缀也必须纳入守卫；只有显式文档标记为纯映射的函数才能在公共源码保留。
		return strings.Contains(lowerName, "foros") ||
			strings.Contains(lowerName, "onos") ||
			strings.Contains(lowerName, "currentplatform")
	}
	platformSelectorName := func(function ast.Expr) string {
		switch expression := function.(type) {
		case *ast.Ident:
			return expression.Name
		case *ast.SelectorExpr:
			return expression.Sel.Name
		default:
			return ""
		}
	}
	containsRuntimePlatformSelectorCall := func(node ast.Node) bool {
		if node == nil {
			return false
		}
		found := false
		ast.Inspect(node, func(current ast.Node) bool {
			if current == nil || found {
				return false
			}
			call, ok := current.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := platformSelectorName(call.Fun)
			if _, pure := pureMappingFunctions[name]; pure || !platformSelectorCallName(name) {
				return true
			}
			for _, argument := range call.Args {
				if containsRuntimePlatform(argument) {
					found = true
					return false
				}
			}
			return true
		})
		return found
	}
	isRuntimePlatformCondition := func(node ast.Node) bool {
		return containsRuntimePlatform(node) && containsPlatformLiteral(node)
	}
	found := false
	ast.Inspect(parsed, func(node ast.Node) bool {
		if node == nil || found {
			return false
		}
		switch current := node.(type) {
		case *ast.IfStmt:
			found = isRuntimePlatformCondition(current.Cond)
		case *ast.ForStmt:
			found = isRuntimePlatformCondition(current.Cond)
		case *ast.SwitchStmt:
			if containsRuntimePlatform(current.Tag) {
				found = true
				break
			}
			for _, statement := range current.Body.List {
				clause, ok := statement.(*ast.CaseClause)
				if !ok {
					continue
				}
				for _, expression := range clause.List {
					if isRuntimePlatformCondition(expression) {
						found = true
						break
					}
				}
				if found {
					break
				}
			}
		case *ast.CallExpr:
			found = containsRuntimePlatformSelectorCall(current)
		}
		return !found
	})
	return found, nil
}

// lspTestHasRuntimePlatformSkip 用 Go AST 识别 runtime.GOOS/GOARCH 控制的测试跳过。
// 它理解 runtime import alias，避免依赖易漏报的文本正则；平台已由 build tag 收窄的
// 文件可继续在内部细化同一矩阵，通用 e2e 文件则不得用运行时 Skip 替代源码选源。
func lspTestHasRuntimePlatformSkip(path string, source []byte) (bool, error) {
	parsed, err := parser.ParseFile(token.NewFileSet(), path, source, parser.SkipObjectResolution)
	if err != nil {
		return false, err
	}
	runtimeAliases := make(map[string]struct{})
	for _, importSpec := range parsed.Imports {
		importPath, err := strconv.Unquote(importSpec.Path.Value)
		if err != nil {
			return false, fmt.Errorf("解析 import %s: %w", importSpec.Path.Value, err)
		}
		if importPath != "runtime" {
			continue
		}
		alias := "runtime"
		if importSpec.Name != nil {
			alias = importSpec.Name.Name
		}
		if alias != "_" && alias != "." {
			runtimeAliases[alias] = struct{}{}
		}
	}
	if len(runtimeAliases) == 0 {
		return false, nil
	}
	containsRuntimePlatform := func(node ast.Node) bool {
		if node == nil {
			return false
		}
		found := false
		ast.Inspect(node, func(current ast.Node) bool {
			selector, ok := current.(*ast.SelectorExpr)
			if !ok || (selector.Sel.Name != "GOOS" && selector.Sel.Name != "GOARCH") {
				return true
			}
			identifier, ok := selector.X.(*ast.Ident)
			if !ok {
				return true
			}
			_, found = runtimeAliases[identifier.Name]
			return !found
		})
		return found
	}
	containsTestingSkip := func(node ast.Node) bool {
		if node == nil {
			return false
		}
		found := false
		ast.Inspect(node, func(current ast.Node) bool {
			call, ok := current.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if ok && (selector.Sel.Name == "Skip" || selector.Sel.Name == "Skipf" || selector.Sel.Name == "SkipNow") {
				found = true
				return false
			}
			return true
		})
		return found
	}
	found := false
	ast.Inspect(parsed, func(node ast.Node) bool {
		if found {
			return false
		}
		switch current := node.(type) {
		case *ast.IfStmt:
			if containsRuntimePlatform(current.Cond) && (containsTestingSkip(current.Body) || containsTestingSkip(current.Else)) {
				found = true
				return false
			}
		case *ast.SwitchStmt:
			if containsRuntimePlatform(current.Tag) && containsTestingSkip(current.Body) {
				found = true
				return false
			}
		}
		return true
	})
	return found, nil
}

// TestLSPRuntimePlatformSkipCannotReplaceBuildTag 锁定 AST 守卫对 runtime alias、
// GOARCH 和 switch gate 的识别，避免以后再次用运行时跳过冒充编译期平台隔离。
func TestLSPRuntimePlatformSkipCannotReplaceBuildTag(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
		want   bool
	}{
		{
			name: "runtime_alias_goos_skip",
			source: `package fixture
import (
	gostdruntime "runtime"
	"testing"
)
func TestPlatform(t *testing.T) {
	if gostdruntime.GOOS == "windows" { t.Skip("not selected") }
}`,
			want: true,
		},
		{
			name: "goarch_switch_skip",
			source: `package fixture
import (
	"runtime"
	"testing"
)
func TestPlatform(t *testing.T) {
	switch runtime.GOARCH { case "386": t.SkipNow() }
}`,
			want: true,
		},
		{
			name: "ordinary_unit_runtime_goos_skip",
			source: `package fixture
import (
	"runtime"
	"testing"
)
func TestOrdinaryUnit(t *testing.T) {
	if runtime.GOOS == "windows" { t.Skip("platform fixture is not applicable") }
}`,
			want: true,
		},
		{
			name: "cross_platform_runtime_observation_without_skip",
			source: `package fixture
import (
	"runtime"
	"testing"
)
func TestPlatform(t *testing.T) { t.Log(runtime.GOOS) }
`,
			want: false,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := lspTestHasRuntimePlatformSkip(testCase.name+".go", []byte(testCase.source))
			if err != nil {
				t.Fatalf("parse runtime platform skip fixture: %v", err)
			}
			if got != testCase.want {
				t.Fatalf("runtime platform skip = %t, want %t", got, testCase.want)
			}
		})
	}
}

// TestLSPRuntimePlatformControlFlowRequiresBuildTag 锁定更严格的源码门禁：宿主事实可以
// 用于日志、回执和纯映射入参，但不能在无平台 build tag 的文件中直接选择执行分支。
func TestLSPRuntimePlatformControlFlowRequiresBuildTag(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
		want   bool
	}{
		{
			name: "goos_if_selects_windows_branch",
			source: `package fixture
import "runtime"
func platform() string { if runtime.GOOS == "windows" { return "exe" }; return "plain" }
`,
			want: true,
		},
		{
			name: "goarch_switch_selects_binary",
			source: `package fixture
import "runtime"
func platform() string { switch runtime.GOARCH { case "arm64": return "arm64" }; return "unknown" }
`,
			want: true,
		},
		{
			name: "tagless_switch_case_selects_linux",
			source: `package fixture
import "runtime"
func platform() string { switch { case runtime.GOOS == "linux": return "linux" }; return "unknown" }
`,
			want: true,
		},
		{
			name: "runtime_platform_selector_argument_selects_windows",
			source: `package fixture
import "runtime"
func current() string { return runtimeNPMCommandForPlatform(runtime.GOOS) }
func runtimeNPMCommandForPlatform(goos string) string { if goos == "windows" { return "npm.cmd" }; return "npm" }
`,
			want: true,
		},
		{
			name: "runtime_architecture_selector_argument_selects_native_binary",
			source: `package fixture
import "runtime"
func current() string { return runtimeWindowsArchitecture(runtime.GOARCH) }
func runtimeWindowsArchitecture(goarch string) string { return goarch }
`,
			want: true,
		},
		{
			name: "for_os_selector_argument_selects_platform",
			source: `package fixture
import "runtime"
func patchEditTimeoutTierForOS(goos string) string { return goos }
func current() string { return patchEditTimeoutTierForOS(runtime.GOOS) }
`,
			want: true,
		},
		{
			name: "on_os_selector_argument_selects_platform",
			source: `package fixture
import "runtime"
func fileToolTimeoutTierOnOS(goos string) string { return goos }
func current() string { return fileToolTimeoutTierOnOS(runtime.GOOS) }
`,
			want: true,
		},
		{
			name: "current_platform_selector_argument_selects_platform",
			source: `package fixture
import "runtime"
func currentPlatform(goarch string) string { return goarch }
func current() string { return currentPlatform(runtime.GOARCH) }
`,
			want: true,
		},
		{
			name: "commented_platform_selector_argument_selects_platform",
			source: `package fixture
import "runtime"
// 平台选择器决定安装执行分支。
func selectInstall(goos string) string { return goos }
func current() string { return selectInstall(runtime.GOOS) }
`,
			want: true,
		},
		{
			name: "explicitly_documented_pure_runtime_mapping_is_common",
			source: `package fixture
import "runtime"
// 纯映射：只按显式参数计算结果，无系统调用。
func runtimeNPMCommandForPlatform(goos string) string { return goos }
func current() string { return runtimeNPMCommandForPlatform(runtime.GOOS) }
`,
			want: false,
		},
		{
			name: "host_fact_assertion_is_common",
			source: `package fixture
import "runtime"
func same(got string) bool { return got == runtime.GOOS }
`,
			want: false,
		},
		{
			name: "host_fact_passed_to_pure_mapping_is_common",
			source: `package fixture
import "runtime"
func current() string { return forTarget(runtime.GOOS) }
// 纯映射：只按显式参数计算结果，无系统调用。
func forTarget(goos string) string { return goos }
`,
			want: false,
		},
		{
			name: "undocumented_target_mapping_is_platform_control",
			source: `package fixture
import "runtime"
func current() string { return forTarget(runtime.GOOS) }
func forTarget(goos string) string { return goos }
`,
			want: true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := lspHasRuntimePlatformControlFlow(testCase.name+".go", []byte(testCase.source))
			if err != nil {
				t.Fatalf("parse runtime platform control fixture: %v", err)
			}
			if got != testCase.want {
				t.Fatalf("runtime platform control = %t, want %t", got, testCase.want)
			}
		})
	}
}

type lspBuildConstraintContext struct {
	goos   string
	goarch string
	e2e    bool
}

type lspBuildConstraintAtoms struct {
	positive map[string]struct{}
	negative map[string]struct{}
}

var (
	lspSupportedGOOS   = []string{"windows", "linux", "darwin", "freebsd"}
	lspSupportedGOARCH = []string{"arm64", "amd64", "386"}
)

// lspBuildTagAtoms 按 Go build 表达式的原子边界提取正负标签，避免字符串包含误把 !windows 当成 windows。
func lspBuildTagAtoms(tag string) lspBuildConstraintAtoms {
	atoms := lspBuildConstraintAtoms{
		positive: make(map[string]struct{}),
		negative: make(map[string]struct{}),
	}
	negated := false
	for index := 0; index < len(tag); {
		switch tag[index] {
		case '!':
			negated = !negated
			index++
		case ' ', '\t', '\r', '\n', '(', ')', '&', '|':
			negated = false
			index++
		default:
			start := index
			for index < len(tag) {
				character := tag[index]
				if (character >= 'a' && character <= 'z') ||
					(character >= 'A' && character <= 'Z') ||
					(character >= '0' && character <= '9') || character == '_' {
					index++
					continue
				}
				break
			}
			if start == index {
				negated = false
				index++
				continue
			}
			atom := tag[start:index]
			if negated {
				atoms.negative[atom] = struct{}{}
			} else {
				atoms.positive[atom] = struct{}{}
			}
			negated = false
		}
	}
	return atoms
}

func lspBuildTagHasPositiveAtom(tag, atom string) bool {
	_, ok := lspBuildTagAtoms(tag).positive[atom]
	return ok
}

func lspBuildTagHasPlatformOrArchitectureAtom(tag string) bool {
	atoms := lspBuildTagAtoms(tag)
	for atom := range atoms.positive {
		if lspIsPlatformOrArchitectureAtom(atom) {
			return true
		}
	}
	for atom := range atoms.negative {
		if lspIsPlatformOrArchitectureAtom(atom) {
			return true
		}
	}
	return false
}

func lspIsPlatformOrArchitectureAtom(atom string) bool {
	switch atom {
	case "windows", "linux", "darwin", "freebsd", "unix", "arm64", "amd64", "386":
		return true
	default:
		return false
	}
}

func lspFileNameHasE2E(base string) bool {
	return strings.Contains(base, "e2e")
}

func lspBuildConstraintMatrixContexts() []lspBuildConstraintContext {
	contexts := make([]lspBuildConstraintContext, 0, len(lspSupportedGOOS)*len(lspSupportedGOARCH)*2)
	for _, goos := range lspSupportedGOOS {
		for _, goarch := range lspSupportedGOARCH {
			contexts = append(contexts,
				lspBuildConstraintContext{goos: goos, goarch: goarch, e2e: false},
				lspBuildConstraintContext{goos: goos, goarch: goarch, e2e: true},
			)
		}
	}
	return contexts
}

const lspMaxCustomBuildAtoms = 8

func lspIsKnownBuildConstraintAtom(atom string) bool {
	switch atom {
	case "windows", "linux", "darwin", "freebsd", "arm64", "amd64", "386", "e2e", "unix":
		return true
	default:
		return false
	}
}

func lspBuildConstraintCustomAtoms(expression constraint.Expr) []string {
	seen := make(map[string]struct{})
	var atoms []string
	var collect func(constraint.Expr)
	collect = func(current constraint.Expr) {
		switch current := current.(type) {
		case *constraint.AndExpr:
			collect(current.X)
			collect(current.Y)
		case *constraint.OrExpr:
			collect(current.X)
			collect(current.Y)
		case *constraint.NotExpr:
			collect(current.X)
		case *constraint.TagExpr:
			if lspIsKnownBuildConstraintAtom(current.Tag) {
				return
			}
			if _, ok := seen[current.Tag]; ok {
				return
			}
			seen[current.Tag] = struct{}{}
			atoms = append(atoms, current.Tag)
		}
	}
	collect(expression)
	return atoms
}

func lspEvalBuildConstraintWithCustomAtoms(
	expression constraint.Expr,
	candidate lspBuildConstraintContext,
	customValues map[string]bool,
) bool {
	return expression.Eval(func(tag string) bool {
		switch tag {
		case candidate.goos, candidate.goarch:
			return true
		case "e2e":
			return candidate.e2e
		case "unix":
			return candidate.goos != "windows"
		default:
			return customValues[tag]
		}
	})
}

// lspBuildConstraintSelectedContexts 枚举自定义标签真假并合并所有实际支持上下文。
// 组合数有硬上限，避免守卫因供应链注入的任意标签表达式无界膨胀。
func lspBuildConstraintSelectedContexts(expression constraint.Expr) ([]lspBuildConstraintContext, error) {
	return lspBuildConstraintSelectedContextsForFileName("", expression)
}

// lspBuildConstraintSelectedContextsForFileName 合并显式表达式与 Go 文件名隐式 GOOS/GOARCH 约束。
// filename 为空时只求显式表达式；否则每个上下文必须同时满足两者。
func lspBuildConstraintSelectedContextsForFileName(filename string, expression constraint.Expr) ([]lspBuildConstraintContext, error) {
	customAtoms := lspBuildConstraintCustomAtoms(expression)
	if len(customAtoms) > lspMaxCustomBuildAtoms {
		return nil, fmt.Errorf("自定义 build tag 原子数 %d 超过硬上限 %d", len(customAtoms), lspMaxCustomBuildAtoms)
	}
	contexts := lspBuildConstraintMatrixContexts()
	selected := make([]lspBuildConstraintContext, 0, len(contexts))
	seen := make(map[string]struct{}, len(contexts))
	combinations := 1 << len(customAtoms)
	for mask := 0; mask < combinations; mask++ {
		customValues := make(map[string]bool, len(customAtoms))
		for index, atom := range customAtoms {
			customValues[atom] = mask&(1<<index) != 0
		}
		for _, candidate := range contexts {
			if !lspEvalBuildConstraintWithCustomAtoms(expression, candidate, customValues) {
				continue
			}
			if filename != "" && !lspFilenameImplicitConstraintMatches(filename, candidate) {
				continue
			}
			key := candidate.goos + "/" + candidate.goarch + "/" + fmt.Sprint(candidate.e2e)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			selected = append(selected, candidate)
		}
	}
	return selected, nil
}

// lspFilenameImplicitConstraintMatches 精确模拟 Go 文件名末尾的 GOOS/GOARCH 隐式约束。
// _test.go 会先剥离 _test；中性 token（例如 _platform）用于避免多平台文件被 Go 隐式收窄。
func lspFilenameImplicitConstraintMatches(filename string, candidate lspBuildConstraintContext) bool {
	base := strings.ToLower(filepath.Base(filename))
	base = strings.TrimSuffix(base, ".go")
	base = strings.TrimSuffix(base, "_test")
	parts := strings.Split(base, "_")
	if len(parts) == 0 {
		return true
	}
	last := parts[len(parts)-1]
	if len(parts) >= 2 && lspKnownGOOS(parts[len(parts)-2]) && lspKnownGOARCH(last) {
		return candidate.goos == parts[len(parts)-2] && candidate.goarch == last
	}
	if lspKnownGOOS(last) {
		return candidate.goos == last
	}
	if lspKnownGOARCH(last) {
		return candidate.goarch == last
	}
	return true
}

func lspKnownGOOS(value string) bool {
	switch value {
	case "aix", "android", "darwin", "dragonfly", "freebsd", "illumos", "ios", "js", "linux", "netbsd", "openbsd", "plan9", "solaris", "wasip1", "windows":
		return true
	default:
		return false
	}
}

func lspKnownGOARCH(value string) bool {
	switch value {
	case "386", "amd64", "arm", "arm64", "mips", "mips64", "mips64le", "mipsle", "ppc64", "ppc64le", "riscv64", "s390x", "wasm":
		return true
	default:
		return false
	}
}

// lspBuildFileConstraintMatrixViolations 同时核对显式 build 表达式和文件名隐式约束。
func lspBuildFileConstraintMatrixViolations(filename, tag string, expression constraint.Expr) []string {
	return lspBuildFileConstraintMatrixViolationsAtPath("", filename, tag, expression)
}

// lspBuildFileConstraintMatrixViolationsAtPath 用真实 go/build.Context.MatchFile 求文件最终选源。
// 只有无真实文件的纯 helper 回归才使用手写 filename 模拟；生产扫描一律走 MatchFile。
func lspBuildFileConstraintMatrixViolationsAtPath(path, filename, tag string, expression constraint.Expr) []string {
	violations := lspBuildConstraintMatrixViolations(tag, expression)
	explicit, explicitErr := lspBuildConstraintSelectedContexts(expression)
	if explicitErr != nil {
		return append(violations, explicitErr.Error())
	}
	var effective []lspBuildConstraintContext
	var effectiveErr error
	if path != "" {
		effective, effectiveErr = lspBuildConstraintSelectedContextsFromGoBuild(path, expression)
	} else {
		effective, effectiveErr = lspBuildConstraintSelectedContextsForFileName(filename, expression)
	}
	if effectiveErr != nil {
		return append(violations, effectiveErr.Error())
	}
	if !lspBuildConstraintContextSetsEqual(explicit, effective) {
		violations = append(violations, "文件名隐式 GOOS/GOARCH 约束与显式 build 表达式选择矩阵不一致")
	}
	return violations
}

func lspBuildConstraintSelectedContextsFromGoBuild(path string, expression constraint.Expr) ([]lspBuildConstraintContext, error) {
	customAtoms := lspBuildConstraintCustomAtoms(expression)
	if len(customAtoms) > lspMaxCustomBuildAtoms {
		return nil, fmt.Errorf("自定义 build tag 原子数 %d 超过硬上限 %d", len(customAtoms), lspMaxCustomBuildAtoms)
	}
	contexts := lspBuildConstraintMatrixContexts()
	selected := make([]lspBuildConstraintContext, 0, len(contexts))
	seen := make(map[string]struct{}, len(contexts))
	combinations := 1 << len(customAtoms)
	for mask := 0; mask < combinations; mask++ {
		customValues := make(map[string]bool, len(customAtoms))
		for index, atom := range customAtoms {
			customValues[atom] = mask&(1<<index) != 0
		}
		for _, candidate := range contexts {
			buildContext := build.Context{
				GOOS:       candidate.goos,
				GOARCH:     candidate.goarch,
				Compiler:   "gc",
				CgoEnabled: customValues["cgo"],
				BuildTags:  lspBuildContextTags(candidate, customValues),
			}
			matched, err := buildContext.MatchFile(filepath.Dir(path), filepath.Base(path))
			if err != nil {
				return nil, fmt.Errorf("go/build MatchFile %s/%s: %w", filepath.Dir(path), filepath.Base(path), err)
			}
			if !matched {
				continue
			}
			key := candidate.goos + "/" + candidate.goarch + "/" + fmt.Sprint(candidate.e2e)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			selected = append(selected, candidate)
		}
	}
	return selected, nil
}

func lspBuildContextTags(candidate lspBuildConstraintContext, customValues map[string]bool) []string {
	tags := make([]string, 0, len(customValues)+2)
	if candidate.e2e {
		tags = append(tags, "e2e")
	}
	if candidate.goos != "windows" {
		tags = append(tags, "unix")
	}
	for atom, enabled := range customValues {
		if enabled && atom != "cgo" && atom != "unix" && atom != "e2e" {
			tags = append(tags, atom)
		}
	}
	return tags
}

func lspBuildConstraintContextSetsEqual(left, right []lspBuildConstraintContext) bool {
	leftSet := make(map[string]struct{}, len(left))
	rightSet := make(map[string]struct{}, len(right))
	for _, context := range left {
		leftSet[context.goos+"/"+context.goarch+"/"+fmt.Sprint(context.e2e)] = struct{}{}
	}
	for _, context := range right {
		rightSet[context.goos+"/"+context.goarch+"/"+fmt.Sprint(context.e2e)] = struct{}{}
	}
	if len(leftSet) != len(rightSet) {
		return false
	}
	for key := range leftSet {
		if _, ok := rightSet[key]; !ok {
			return false
		}
	}
	return true
}

// lspFileNameConstraintViolations 要求表达式中的平台/架构原子在文件名中可见。
// 负向约束使用 non* 或 except_* 命名，避免读者误以为是相反的隐式后缀约束。
func lspFileNameConstraintViolations(base, tag string) []string {
	atoms := lspBuildTagAtoms(tag)
	var violations []string
	familyName := lspFilenameHasConstraintAtom(base, "unix", false) || lspFilenameHasConstraintAtom(base, "posix", false)
	if lspFilenameHasConstraintAtom(base, "other", false) {
		violations = append(violations, "平台专用文件名不得使用含糊的 _other；请列出实际 non* 平台原子")
	}
	for _, atom := range []string{"windows", "linux", "darwin", "freebsd", "unix", "arm64", "amd64", "386"} {
		if familyName && (atom == "windows" || atom == "linux" || atom == "darwin" || atom == "freebsd" || atom == "unix") {
			continue
		}
		if _, ok := atoms.positive[atom]; ok && !lspFilenameHasConstraintAtom(base, atom, false) {
			violations = append(violations, "正向原子 "+atom+" 未在文件名中显式出现")
		}
		if _, ok := atoms.negative[atom]; ok && !lspFilenameHasConstraintAtom(base, atom, true) {
			violations = append(violations, "负向原子 !"+atom+" 未在文件名中显式出现")
		}
	}
	return violations
}

func lspFilenameHasConstraintAtom(base, atom string, negative bool) bool {
	lower := strings.ToLower(base)
	parts := strings.FieldsFunc(lower, func(r rune) bool {
		return r == '_' || r == '-' || r == '.'
	})
	if negative {
		hasExcept := false
		for _, part := range parts {
			if part == "non"+atom {
				return true
			}
			if part == "except" {
				hasExcept = true
			}
		}
		if hasExcept && lspFilenameHasConstraintAtom(base, atom, false) {
			return true
		}
		return false
	}
	for _, part := range parts {
		if part == atom || (atom == "amd64" && part == "x64") || (atom == "386" && part == "x86") ||
			(atom == "darwin" && (part == "macos" || part == "osx")) {
			return true
		}
	}
	return false
}

// lspUnixPosixFamilyConstraintViolationsAtPath 要求 unix/posix 文件实际覆盖 Linux、Darwin、FreeBSD，
// 生产扫描必须通过 go/build.MatchFile 取得最终选源，避免仅凭字符串放行缺失 FreeBSD 的子集表达式。
func lspUnixPosixFamilyConstraintViolationsAtPath(path, base, tag string, expression constraint.Expr) []string {
	if !lspFilenameHasConstraintAtom(base, "unix", false) && !lspFilenameHasConstraintAtom(base, "posix", false) {
		return nil
	}
	violations := make([]string, 0, 4)
	atoms := lspBuildTagAtoms(tag)
	_, excludesOnlyWindows := atoms.negative["windows"]
	if excludesOnlyWindows && !lspBuildTagHasPositiveAtom(tag, "unix") &&
		!lspBuildTagHasPositiveAtom(tag, "linux") && !lspBuildTagHasPositiveAtom(tag, "darwin") &&
		!lspBuildTagHasPositiveAtom(tag, "freebsd") {
		violations = append(violations, "unix/posix family 不能只用 !windows 扩大到非 Unix 平台；请使用 unix 或列出精确 Unix GOOS")
	}
	var (
		selected []lspBuildConstraintContext
		err      error
	)
	if path != "" {
		selected, err = lspBuildConstraintSelectedContextsFromGoBuild(path, expression)
	} else {
		selected, err = lspBuildConstraintSelectedContextsForFileName(base, expression)
	}
	if err != nil {
		return append(violations, fmt.Sprintf("解析 unix/posix 实际选源失败: %v", err))
	}
	present := map[string]bool{"linux": false, "darwin": false, "freebsd": false}
	for _, candidate := range selected {
		if _, ok := present[candidate.goos]; ok {
			present[candidate.goos] = true
		}
		if candidate.goos == "windows" {
			return append(violations, "unix/posix family 不得选择 Windows")
		}
	}
	for _, goos := range []string{"linux", "darwin", "freebsd"} {
		if !present[goos] {
			violations = append(violations, fmt.Sprintf("unix/posix family 未覆盖 %s", goos))
		}
	}
	return violations
}

// lspBuildConstraintMatrixViolations 在完整 GOOS/GOARCH/e2e 矩阵上做语义求值。
// 自定义标签逐一枚举真假；平台、架构与 e2e 的隔离不能靠未建模标签隐藏泄漏。
func lspBuildConstraintMatrixViolations(tag string, expression constraint.Expr) []string {
	atoms := lspBuildTagAtoms(tag)
	hasRelevant := lspBuildTagHasPositiveAtom(tag, "e2e") || lspBuildTagHasPositiveAtom(tag, "windows") ||
		lspBuildTagHasPositiveAtom(tag, "linux") || lspBuildTagHasPositiveAtom(tag, "darwin") ||
		lspBuildTagHasPositiveAtom(tag, "freebsd") || lspBuildTagHasPositiveAtom(tag, "unix") ||
		lspBuildTagHasPositiveAtom(tag, "arm64") || lspBuildTagHasPositiveAtom(tag, "amd64") ||
		lspBuildTagHasPositiveAtom(tag, "386")
	if !hasRelevant {
		for atom := range atoms.negative {
			if lspIsPlatformOrArchitectureAtom(atom) || atom == "e2e" {
				hasRelevant = true
				break
			}
		}
	}
	if !hasRelevant {
		return nil
	}

	positivePlatforms := make(map[string]struct{})
	for _, platform := range []string{"windows", "linux", "darwin", "freebsd", "unix"} {
		if _, ok := atoms.positive[platform]; ok {
			positivePlatforms[platform] = struct{}{}
		}
	}
	positiveArchitectures := make(map[string]struct{})
	for _, architecture := range []string{"arm64", "amd64", "386"} {
		if _, ok := atoms.positive[architecture]; ok {
			positiveArchitectures[architecture] = struct{}{}
		}
	}

	selected, selectionErr := lspBuildConstraintSelectedContexts(expression)
	if selectionErr != nil {
		return []string{selectionErr.Error()}
	}
	platformLeak := false
	architectureLeak := false
	e2eLeak := false
	for _, candidate := range selected {
		if _, positive := atoms.positive["e2e"]; positive && !candidate.e2e {
			e2eLeak = true
		}
		if _, negative := atoms.negative["e2e"]; negative && candidate.e2e {
			e2eLeak = true
		}
		if len(positivePlatforms) > 0 && !lspConstraintContextMatchesPlatform(candidate, positivePlatforms) {
			platformLeak = true
		}
		if len(positiveArchitectures) > 0 {
			if _, ok := positiveArchitectures[candidate.goarch]; !ok {
				architectureLeak = true
			}
		}
	}

	violations := make([]string, 0, 4)
	if len(selected) == len(lspBuildConstraintMatrixContexts()) {
		violations = append(violations, "平台/架构/e2e 原子组合在支持矩阵中未形成任何隔离")
	}
	if platformLeak {
		violations = append(violations, "正向平台原子通过 OR 组合泄漏到未声明 GOOS")
	}
	if architectureLeak {
		violations = append(violations, "正向架构原子通过 OR 组合泄漏到未声明 GOARCH")
	}
	if e2eLeak {
		violations = append(violations, "e2e 原子未形成默认构建与 e2e 构建的严格隔离")
	}
	return violations
}

func lspConstraintContextMatchesPlatform(candidate lspBuildConstraintContext, positive map[string]struct{}) bool {
	if _, ok := positive[candidate.goos]; ok {
		return true
	}
	if _, ok := positive["unix"]; ok {
		return candidate.goos != "windows"
	}
	return false
}

func lspWindowsARM64E2EConstraintViolations(tag string, expression constraint.Expr) []string {
	violations := make([]string, 0, 2)
	for _, atom := range []string{"windows", "arm64", "e2e"} {
		if !lspBuildTagHasPositiveAtom(tag, atom) {
			violations = append(violations, "必须显式包含正向 "+atom+" 原子")
		}
	}
	selectedContexts, selectionErr := lspBuildConstraintSelectedContexts(expression)
	if selectionErr != nil {
		return append(violations, selectionErr.Error())
	}
	for _, candidate := range selectedContexts {
		if candidate.goos != "windows" || candidate.goarch != "arm64" || !candidate.e2e {
			violations = append(violations, "实际选择泄漏到非 windows/arm64/e2e 上下文")
			break
		}
	}
	if len(selectedContexts) != 1 {
		violations = append(violations, "必须且只能选择一个 windows/arm64/e2e 上下文")
	}
	return violations
}

// lspArchitectureFileConstraint 将交付文件名中的用户可见架构映射到 Go 原生架构标签。
func lspArchitectureFileConstraint(base string) (architecture, requiredTag string, dedicated bool) {
	parts := strings.FieldsFunc(strings.ToLower(base), func(r rune) bool {
		return r == '_' || r == '-' || r == '.'
	})
	for _, part := range parts {
		switch part {
		case "arm64":
			return "ARM64", "arm64", true
		case "amd64", "x64":
			return "x64", "amd64", true
		case "386", "x86":
			return "x86", "386", true
		}
	}
	return "", "", false
}

// lspPlatformFileConstraint 依据文件名返回平台名称和必须出现的 build tag 原子。
// Unix/POSIX 家族的补集表达式由实际 GOOS 矩阵守卫验证，禁止使用含糊的 other 名称。
func lspPlatformFileConstraint(base string) (platform, requiredTag string, dedicated bool) {
	switch {
	case strings.Contains(base, "nonwindows"):
		return "非 Windows", "!windows", true
	case strings.Contains(base, "nondarwin"):
		return "非 Darwin", "!darwin", true
	case strings.Contains(base, "nonlinux"):
		return "非 Linux", "!linux", true
	case strings.Contains(base, "nonfreebsd"):
		return "非 FreeBSD", "!freebsd", true
	case strings.HasSuffix(base, "_linux.go"), strings.HasSuffix(base, "_linux_test.go"):
		return "Linux", "linux", true
	case strings.HasSuffix(base, "_darwin.go"), strings.HasSuffix(base, "_darwin_test.go"):
		return "Darwin", "darwin", true
	case strings.HasSuffix(base, "_freebsd.go"), strings.HasSuffix(base, "_freebsd_test.go"):
		return "FreeBSD", "freebsd", true
	case strings.Contains(base, "windows"):
		return "Windows", "windows", true
	case strings.Contains(base, "linux"):
		return "Linux", "linux", true
	case strings.Contains(base, "darwin"):
		return "Darwin", "darwin", true
	case strings.Contains(base, "freebsd"):
		return "FreeBSD", "freebsd", true
	case strings.Contains(base, "macos"), strings.Contains(base, "osx"):
		return "Darwin", "darwin", true
	case strings.Contains(base, "nonunix"):
		return "非 Unix", "", true
	case strings.Contains(base, "unix"):
		return "Unix", "", true
	case strings.Contains(base, "posix"):
		return "POSIX", "", true
	default:
		return "", "", false
	}
}

// lspGoBuildTag 读取 package 声明前的显式 Go build constraint。
func lspGoBuildTag(source string) string {
	for _, line := range strings.Split(source, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "//go:build ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "//go:build "))
		}
		if strings.HasPrefix(line, "package ") {
			break
		}
	}
	return ""
}

// lspHasLegacyBuildConstraintWithoutGoBuild 拒绝仅有旧式 // +build 的文件；
// gofmt 生成的 //go:build 与兼容性 // +build 成对出现时仍然合法。
func lspHasLegacyBuildConstraintWithoutGoBuild(source string) bool {
	hasLegacy, hasGoBuild := false, false
	for _, line := range strings.Split(source, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "package ") {
			break
		}
		switch {
		case strings.HasPrefix(line, "//go:build "):
			hasGoBuild = true
		case strings.HasPrefix(line, "// +build "):
			hasLegacy = true
		}
	}
	return hasLegacy && !hasGoBuild
}

// TestLSPBuildConstraintMatrixRejectsORLeaks 固定回归 windows || e2e/arm64 的跨平台与跨架构泄漏。
func TestLSPBuildConstraintMatrixRejectsORLeaks(t *testing.T) {
	for _, testCase := range []struct {
		name string
		tag  string
	}{
		{name: "platform_or_e2e", tag: "windows || e2e"},
		{name: "platform_or_architecture", tag: "windows || arm64"},
		{name: "platform_or_custom", tag: "windows || lsp_integration"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			expression, err := constraint.Parse("//go:build " + testCase.tag)
			if err != nil {
				t.Fatalf("parse %q: %v", testCase.tag, err)
			}
			if violations := lspBuildConstraintMatrixViolations(testCase.tag, expression); len(violations) == 0 {
				t.Fatalf("build constraint %q unexpectedly passed the full context matrix", testCase.tag)
			}
		})
	}

	for _, testCase := range []struct {
		name string
		tag  string
	}{
		{name: "platform_and_custom", tag: "windows && lsp_integration"},
		{name: "platform_architecture_e2e", tag: "windows && arm64 && e2e"},
		{name: "darwin_architecture_e2e", tag: "darwin && arm64 && e2e"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			expression, err := constraint.Parse("//go:build " + testCase.tag)
			if err != nil {
				t.Fatalf("parse valid tag %q: %v", testCase.tag, err)
			}
			if violations := lspBuildConstraintMatrixViolations(testCase.tag, expression); len(violations) != 0 {
				t.Fatalf("valid build constraint %q rejected: %v", testCase.tag, violations)
			}
			if testCase.tag == "windows && arm64 && e2e" {
				if violations := lspWindowsARM64E2EConstraintViolations(testCase.tag, expression); len(violations) != 0 {
					t.Fatalf("valid ARM64 E2E build constraint %q rejected: %v", testCase.tag, violations)
				}
			}
		})
	}

	t.Run("custom_atom_hard_limit", func(t *testing.T) {
		const tag = "windows && tag_a && tag_b && tag_c && tag_d && tag_e && tag_f && tag_g && tag_h && tag_i"
		expression, err := constraint.Parse("//go:build " + tag)
		if err != nil {
			t.Fatalf("parse custom-tag limit regression %q: %v", tag, err)
		}
		violations := lspBuildConstraintMatrixViolations(tag, expression)
		if len(violations) == 0 || !strings.Contains(violations[0], "硬上限") {
			t.Fatalf("custom-tag enumeration did not fail fast at the hard limit: %v", violations)
		}
	})

	t.Run("other_filename_is_rejected", func(t *testing.T) {
		violations := lspFileNameConstraintViolations("x_other.go", "!windows")
		found := false
		for _, violation := range violations {
			if strings.Contains(violation, "_other") {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("ambiguous _other platform filename was accepted: %v", violations)
		}
	})

	t.Run("macos_and_osx_are_visible_darwin_aliases", func(t *testing.T) {
		for _, base := range []string{"x_macos_platform.go", "x_osx_platform_test.go"} {
			platform, requiredTag, dedicated := lspPlatformFileConstraint(base)
			if !dedicated || platform != "Darwin" || requiredTag != "darwin" {
				t.Fatalf("platform alias %q = (%q, %q, %t), want Darwin/darwin/true", base, platform, requiredTag, dedicated)
			}
			if violations := lspFileNameConstraintViolations(base, "darwin"); len(violations) != 0 {
				t.Fatalf("visible Darwin alias %q rejected: %v", base, violations)
			}
		}
	})

	t.Run("unix_posix_family_requires_full_nonwindows_matrix", func(t *testing.T) {
		cases := []struct {
			name    string
			base    string
			tag     string
			invalid bool
		}{
			{name: "unix_exact_tag", base: "x_unix_platform.go", tag: "unix"},
			{name: "unix_nonwindows_is_too_broad", base: "x_unix_platform.go", tag: "!windows", invalid: true},
			{name: "posix_explicit_three_goos", base: "x_posix_platform.go", tag: "darwin || linux || freebsd"},
			{name: "unix_missing_freebsd", base: "x_unix_platform.go", tag: "darwin || linux", invalid: true},
			{name: "posix_windows_leak", base: "x_posix_platform.go", tag: "windows", invalid: true},
		}
		for _, testCase := range cases {
			t.Run(testCase.name, func(t *testing.T) {
				root := t.TempDir()
				path := filepath.Join(root, testCase.base)
				if err := os.WriteFile(path, []byte("//go:build "+testCase.tag+"\n\npackage fixture\n"), 0o600); err != nil {
					t.Fatalf("write unix/posix MatchFile fixture: %v", err)
				}
				expression, err := constraint.Parse("//go:build " + testCase.tag)
				if err != nil {
					t.Fatalf("parse unix/posix test tag %q: %v", testCase.tag, err)
				}
				violations := lspUnixPosixFamilyConstraintViolationsAtPath(path, strings.ToLower(testCase.base), testCase.tag, expression)
				if testCase.invalid && len(violations) == 0 {
					t.Fatalf("invalid unix/posix matrix unexpectedly passed: tag=%q", testCase.tag)
				}
				if !testCase.invalid && len(violations) != 0 {
					t.Fatalf("valid unix/posix matrix rejected: tag=%q violations=%v", testCase.tag, violations)
				}
			})
		}
	})

	t.Run("filename_implicit_platform_constraint", func(t *testing.T) {
		const tag = "darwin || linux || windows"
		expression, err := constraint.Parse("//go:build " + tag)
		if err != nil {
			t.Fatalf("parse filename platform regression %q: %v", tag, err)
		}
		if violations := lspBuildFileConstraintMatrixViolations("x_darwin_linux_windows_test.go", tag, expression); len(violations) == 0 {
			t.Fatal("GOOS suffix before _test unexpectedly matched the full multi-platform expression")
		}
		if violations := lspBuildFileConstraintMatrixViolations("x_darwin_linux_windows_platform_test.go", tag, expression); len(violations) != 0 {
			t.Fatalf("neutral platform token still changed the multi-platform selection: %v", violations)
		}
	})

	t.Run("filename_implicit_architecture_constraint", func(t *testing.T) {
		const tag = "linux"
		expression, err := constraint.Parse("//go:build " + tag)
		if err != nil {
			t.Fatalf("parse filename architecture regression %q: %v", tag, err)
		}
		if violations := lspBuildFileConstraintMatrixViolations("x_linux_amd64_test.go", tag, expression); len(violations) == 0 {
			t.Fatal("linux/amd64 suffix unexpectedly matched the linux-only expression")
		}
		const exactTag = "linux && amd64"
		exactExpression, err := constraint.Parse("//go:build " + exactTag)
		if err != nil {
			t.Fatalf("parse exact filename architecture regression %q: %v", exactTag, err)
		}
		if violations := lspBuildFileConstraintMatrixViolations("x_linux_amd64_test.go", exactTag, exactExpression); len(violations) != 0 {
			t.Fatalf("linux/amd64 filename did not match the explicit expression: %v", violations)
		}
	})

	t.Run("go_build_matchfile_is_source_of_truth", func(t *testing.T) {
		const tag = "darwin || linux || windows"
		expression, err := constraint.Parse("//go:build " + tag)
		if err != nil {
			t.Fatalf("parse MatchFile regression %q: %v", tag, err)
		}
		root := t.TempDir()
		bad := filepath.Join(root, "x_darwin_linux_windows_test.go")
		good := filepath.Join(root, "x_darwin_linux_windows_platform_test.go")
		contents := []byte("//go:build " + tag + "\n\npackage fixture\n")
		if err := os.WriteFile(bad, contents, 0o600); err != nil {
			t.Fatalf("write implicit MatchFile fixture: %v", err)
		}
		if err := os.WriteFile(good, contents, 0o600); err != nil {
			t.Fatalf("write neutral MatchFile fixture: %v", err)
		}
		if violations := lspBuildFileConstraintMatrixViolationsAtPath(bad, filepath.Base(bad), tag, expression); len(violations) == 0 {
			t.Fatal("go/build.MatchFile did not expose the implicit Windows suffix")
		}
		if violations := lspBuildFileConstraintMatrixViolationsAtPath(good, filepath.Base(good), tag, expression); len(violations) != 0 {
			t.Fatalf("go/build.MatchFile rejected the neutral multi-platform suffix: %v", violations)
		}
	})
}

func TestLSPBuildConstraintDeclarationRequiresModernTag(t *testing.T) {
	legacyOnly := "// +build windows\n\npackage fixture\n"
	if !lspHasLegacyBuildConstraintWithoutGoBuild(legacyOnly) {
		t.Fatal("legacy-only // +build declaration was not rejected")
	}
	modernWithCompatibility := "//go:build windows\n// +build windows\n\npackage fixture\n"
	if lspHasLegacyBuildConstraintWithoutGoBuild(modernWithCompatibility) {
		t.Fatal("modern //go:build plus compatibility // +build was rejected")
	}
}

type lspWindowsProductionRootContract struct {
	name          string
	path          string
	prefixes      []string
	functionNames []string
}

// TestLSPWindowsProductionProductRootLifecycleContract 锁定正式 Windows LSP E2E 的产品根边界：
// 自建根必须使用可审计的 sd-<product>-production-windows- 前缀，创建后先注册
// removeRealWindowsProductRoot，再设置 owner-only ACL；fixture 的 t.TempDir 不能冒充产品根。
// 该守卫故意位于无标签 guard 中，Linux/macOS CI 也必须检查 Windows 源码合同。
func TestLSPWindowsProductionProductRootLifecycleContract(t *testing.T) {
	contracts := []lspWindowsProductionRootContract{
		{name: "C#", path: "cmd/mcp-lsp/windows_arm64_process_arm64_csharp_mcp_36_soak_e2e_test.go", prefixes: []string{"sd-csharp-production-windows-arm64-"}},
		{name: "Java", path: "cmd/mcp-lsp/windows_arm64_process_arm64_java_mcp_36_soak_e2e_test.go", prefixes: []string{"sd-java-production-windows-arm64-"}},
		{name: "native15x36", path: "cmd/mcp-lsp/windows_arm64_process_arm64_native_catalog_15x36_soak_e2e_test.go", prefixes: []string{"sd-node-production-windows-native-catalog-15x36-"}},
		{name: "Node17x36", path: "cmd/mcp-lsp/windows_arm64_process_arm64_node_17x36_soak_e2e_test.go", prefixes: []string{"sd-node-production-windows-arm64-process-arm64-17x36-"}},
		{name: "Markdown", path: "cmd/mcp-lsp/windows_arm64_process_arm64_markdown_soak_15m_e2e_test.go", prefixes: []string{"sd-node-production-windows-arm64-process-arm64-markdown-soak-15m-"}},
		{name: "GoSQLS", path: "cmd/mcp-lsp/windows_arm64_process_arm64_go_sqls_36_action_e2e_test.go", prefixes: []string{"sd-node-production-windows-gosqls-"}},
		{name: "EmmyLua", path: "cmd/mcp-lsp/lsp_binary_windows_arm64_emmylua_e2e_test.go", prefixes: []string{"sd-emmylua-production-windows-arm64-"}},
		{name: "Ruby", path: "cmd/mcp-lsp/windows_arm64_process_arm64_ruby_36_soak_e2e_test.go", prefixes: []string{"sd-ruby-production-windows-arm64-"}, functionNames: []string{"windowsRubyProductionProductRoot"}},
		{name: "tools-list", path: "cmd/mcp-lsp/lsp_binary_windows_all_native_architectures_production_auto_installer_tools_list_e2e_test.go", prefixes: []string{"sd-node-production-windows-"}},
		{name: "real-node", path: "cmd/mcp-lsp/lsp_binary_real_node_all_languages_windows_e2e_test.go", prefixes: []string{"sd-node-production-windows-", "sd-node-production-windows-mcp-"}, functionNames: []string{
			"prepareRealNodeProductionVueCohort",
			"TestRealNodeProductionEnsureInstalledFromEmptyWindowsAssetCacheE2E",
			"startRealMcpLSPBinary",
		}},
	}
	for _, contract := range contracts {
		contract := contract
		t.Run(contract.name, func(t *testing.T) {
			source := readRepoFile(t, filepath.Join("..", filepath.FromSlash(contract.path)))
			tag := lspGoBuildTag(source)
			if !strings.Contains(tag, "windows") || !strings.Contains(tag, "e2e") {
				t.Fatalf("%s build tag=%q, want Windows E2E source gate", contract.path, tag)
			}
			for _, prefix := range contract.prefixes {
				if !lspWindowsSourceHasMkdirTempPrefix(source, prefix) {
					t.Errorf("%s missing os.MkdirTemp production prefix %q", contract.path, prefix)
				}
			}
			for _, violation := range lspWindowsProductionRootContractViolations(source, contract) {
				t.Errorf("%s product-root contract: %s", contract.path, violation)
			}
		})
	}
}

// TestLSPWindowsGoplsProductRootCleanupContract 锁定 gopls 的不同失败路径：它在 ACL
// 设置失败时显式清理并返回，在正式生命周期 defer 中再次汇总受控清理错误；因此不强迫
// gopls 使用普通 t.Cleanup，但仍必须保留同一产品前缀和 removeRealWindowsProductRoot。
func TestLSPWindowsGoplsProductRootCleanupContract(t *testing.T) {
	source := readRepoFile(t, "../cmd/mcp-lsp/windows_arm64_process_arm64_gopls_4x36_soak_e2e_test.go")
	if got := lspGoBuildTag(source); got != "windows && arm64 && e2e" {
		t.Fatalf("gopls source gate=%q, want windows && arm64 && e2e", got)
	}
	for _, required := range []string{
		`os.MkdirTemp("", "sd-gopls-production-windows-arm64-")`,
		"windowsARM64ProcessARM64GoplsCleanupSetupRoots(productRoot, \"\")",
		"if err := removeRealWindowsProductRoot(productRoot)",
		"cleanupErrors := make([]error, 0, 2)",
		"gopls Windows ARM64 lifecycle cleanup failed",
	} {
		if !strings.Contains(source, required) {
			t.Errorf("gopls source missing explicit cleanup contract %q", required)
		}
	}
}

// TestLSPWindowsProductionProductRootContractRejectsUnsafeFixtures 先用合成源码证明守卫
// 能拒绝 t.TempDir 产品根、ACL 后才注册清理和普通 os.RemoveAll，再依赖上面的真实源码扫描。
func TestLSPWindowsProductionProductRootContractRejectsUnsafeFixtures(t *testing.T) {
	contract := lspWindowsProductionRootContract{name: "synthetic", prefixes: []string{"sd-demo-production-windows-"}}
	cases := []struct {
		name   string
		source string
	}{
		{
			name: "tempdir_product_root",
			source: `package fixture
import "testing"
func setup(t *testing.T) {
	productRoot := t.TempDir()
	t.Cleanup(func() { _ = removeRealWindowsProductRoot(productRoot) })
	if err := securefs.RestrictPrivateOwnerOnly(productRoot, 0o700); err != nil { t.Fatal(err) }
}`,
		},
		{
			name: "cleanup_after_acl",
			source: `package fixture
import "testing"
func setup(t *testing.T) {
	productRoot, _ := os.MkdirTemp("", "sd-demo-production-windows-")
	if err := securefs.RestrictPrivateOwnerOnly(productRoot, 0o700); err != nil { t.Fatal(err) }
	t.Cleanup(func() { _ = removeRealWindowsProductRoot(productRoot) })
}`,
		},
		{
			name: "ordinary_remove_all",
			source: `package fixture
import "testing"
func setup(t *testing.T) {
	productRoot, _ := os.MkdirTemp("", "sd-demo-production-windows-")
	t.Cleanup(func() { _ = os.RemoveAll(productRoot) })
	if err := securefs.RestrictPrivateOwnerOnly(productRoot, 0o700); err != nil { t.Fatal(err) }
}`,
		},
		{
			name: "cleanup_outside_callback",
			source: `package fixture
import "testing"
func setup(t *testing.T) {
	productRoot, _ := os.MkdirTemp("", "sd-demo-production-windows-")
	t.Cleanup(func() {})
	removeRealWindowsProductRoot(productRoot)
	if err := securefs.RestrictPrivateOwnerOnly(productRoot, 0o700); err != nil { t.Fatal(err) }
}`,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			violations := lspWindowsProductionRootContractViolations(testCase.source, contract)
			if len(violations) == 0 {
				t.Fatalf("unsafe product-root fixture unexpectedly passed")
			}
		})
	}

	valid := `package fixture
import "testing"
func setup(t *testing.T) {
	productRoot, _ := os.MkdirTemp("", "sd-demo-production-windows-")
	t.Cleanup(func() { _ = removeRealWindowsProductRoot(productRoot) })
	if err := securefs.RestrictPrivateOwnerOnly(productRoot, 0o700); err != nil { t.Fatal(err) }
}`
	if violations := lspWindowsProductionRootContractViolations(valid, contract); len(violations) != 0 {
		t.Fatalf("valid product-root fixture rejected: %v", violations)
	}
}

// lspWindowsSourceHasMkdirTempPrefix 只确认源码中的真实 os.MkdirTemp 调用携带受控前缀，
// 不把注释或普通 fixture 目录当成产品根证明。
func lspWindowsSourceHasMkdirTempPrefix(source, prefix string) bool {
	for _, line := range strings.Split(source, "\n") {
		if strings.Contains(line, "os.MkdirTemp") && strings.Contains(line, prefix) {
			return true
		}
	}
	return false
}

// lspWindowsProductionRootContractViolations 用函数级 AST 范围检查产品根的创建、清理和
// ACL 顺序，避免把 fixtureRoot 的 t.TempDir 或拒绝 reparse 的负测试误判为产品根。
func lspWindowsProductionRootContractViolations(source string, contract lspWindowsProductionRootContract) []string {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, "product-root-contract.go", []byte(source), parser.SkipObjectResolution)
	if err != nil {
		return []string{fmt.Sprintf("parse source: %v", err)}
	}
	wanted := make(map[string]struct{}, len(contract.functionNames))
	for _, name := range contract.functionNames {
		wanted[name] = struct{}{}
	}
	violations := make([]string, 0, 4)
	checked := 0
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name == nil {
			continue
		}
		start := fset.Position(function.Pos()).Offset
		end := fset.Position(function.End()).Offset
		if start < 0 || end < start || end > len(source) {
			violations = append(violations, "无法确定函数源码范围")
			continue
		}
		body := source[start:end]
		_, named := wanted[function.Name.Name]
		unsafeTempRoot := lspWindowsProductionRootUsesTempDir(body)
		productionMkdir := strings.Contains(body, "os.MkdirTemp") && strings.Contains(body, "production-windows-")
		if len(wanted) > 0 {
			if !named {
				continue
			}
		} else if !productionMkdir && !unsafeTempRoot {
			continue
		}
		checked++
		if unsafeTempRoot {
			violations = append(violations, function.Name.Name+" 使用 t.TempDir() 作为 productRoot/productHome")
		}
		create := strings.Index(body, "os.MkdirTemp")
		if create < 0 {
			violations = append(violations, function.Name.Name+" 必须用 os.MkdirTemp 创建产品根")
			continue
		}
		restrict := strings.Index(body, "securefs.RestrictPrivateOwnerOnly(productRoot")
		if restrict < 0 {
			restrict = strings.Index(body, "securefs.RestrictPrivateOwnerOnly(productHome")
		}
		cleanupSearchEnd := len(body)
		if restrict >= 0 {
			cleanupSearchEnd = restrict
		}
		cleanupRegistration := strings.LastIndex(body[:cleanupSearchEnd], "t.Cleanup")
		cleanup := -1
		if cleanupRegistration >= 0 {
			if relative := strings.Index(body[cleanupRegistration:cleanupSearchEnd], "removeRealWindowsProductRoot"); relative >= 0 {
				cleanup = cleanupRegistration + relative
			}
		}
		if cleanupRegistration < 0 || cleanup < 0 {
			violations = append(violations, function.Name.Name+" 创建后缺少 t.Cleanup(removeRealWindowsProductRoot)")
		} else if !lspFunctionRegistersRealWindowsProductRootCleanup(function) {
			violations = append(violations, function.Name.Name+" 的 t.Cleanup 回调没有调用 removeRealWindowsProductRoot")
		}
		if function.Name.Name == "windowsRubyProductionProductRoot" {
			if cleanupRegistration >= 0 && cleanup >= 0 && cleanup < create {
				violations = append(violations, function.Name.Name+" 清理注册早于产品根创建")
			}
		} else if restrict < 0 {
			violations = append(violations, function.Name.Name+" 创建产品根后缺少 RestrictPrivateOwnerOnly")
		} else if cleanupRegistration < create || cleanup < create || cleanup > restrict {
			violations = append(violations, function.Name.Name+" 必须先注册受控清理，再调用 RestrictPrivateOwnerOnly")
		}
		for _, line := range strings.Split(body, "\n") {
			if strings.Contains(line, "os.RemoveAll(") && (strings.Contains(line, "productRoot") || strings.Contains(line, "productHome")) {
				violations = append(violations, function.Name.Name+" 使用普通 os.RemoveAll 清理产品根")
			}
		}
	}
	if checked == 0 {
		violations = append(violations, "未找到自建 production-windows 产品根函数")
	}
	if strings.Contains(source, "windowsRubyProductionProductRoot(t)") {
		calls := strings.Count(source, "productRoot := windowsRubyProductionProductRoot(t)")
		restrictions := strings.Count(source, "securefs.RestrictPrivateOwnerOnly(productRoot")
		if calls == 0 || restrictions < calls {
			violations = append(violations, "Ruby helper 的每个调用点都必须在返回后设置 productRoot owner-only ACL")
		}
	}
	return violations
}

// lspFunctionRegistersRealWindowsProductRootCleanup 用 AST 确认清理注册的回调实际调用
// 受控删除器，避免仅凭同一函数中出现两个无关字符串形成假阳性。
func lspFunctionRegistersRealWindowsProductRootCleanup(function *ast.FuncDecl) bool {
	if function == nil || function.Body == nil {
		return false
	}
	registered := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		if registered || node == nil {
			return !registered
		}
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Cleanup" {
			return true
		}
		callback, ok := call.Args[0].(*ast.FuncLit)
		if !ok || callback.Body == nil {
			return true
		}
		ast.Inspect(callback.Body, func(callbackNode ast.Node) bool {
			if registered || callbackNode == nil {
				return !registered
			}
			cleanupCall, ok := callbackNode.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch functionExpr := cleanupCall.Fun.(type) {
			case *ast.Ident:
				registered = functionExpr.Name == "removeRealWindowsProductRoot"
			case *ast.SelectorExpr:
				registered = functionExpr.Sel.Name == "removeRealWindowsProductRoot"
			}
			return !registered
		})
		return !registered
	})
	return registered
}

// lspWindowsProductionRootUsesTempDir 只关注 productRoot/productHome 变量，允许同一测试
// 继续使用 t.TempDir 创建 fixture、日志和工作区。
func lspWindowsProductionRootUsesTempDir(source string) bool {
	for _, line := range strings.Split(source, "\n") {
		if strings.Contains(line, "t.TempDir(") && (strings.Contains(line, "productRoot") || strings.Contains(line, "productHome")) {
			return true
		}
	}
	return false
}

// TestWindowsPlatformVisibleDeliveryNamesStaySynchronized 保证 Windows 三种原生架构的交付名、文档和配置技能保持一致，旧的含糊文件名不能重新进入公共说明。
// TestLSPPlatformE2EFilesUseSourceLevelGates 锁定平台专用 E2E 的源码级入口。
// runtime.GOOS/GOARCH Skip 只能细化同一平台矩阵，不能替代 build tag。
func TestLSPPlatformE2EFilesUseSourceLevelGates(t *testing.T) {
	cases := []struct {
		path string
		tag  string
	}{
		{path: "cmd/mcp-lsp/lsp_binary_autoinstall_nonwindows_e2e_test.go", tag: "!windows && e2e"},
		{path: "cmd/mcp-lsp/lsp_binary_real_tools_nonwindows_e2e_test.go", tag: "!windows && e2e"},
		{path: "cmd/mcp-lsp/lsp_binary_gopls_recovery_darwin_e2e_test.go", tag: "darwin && e2e"},
		{path: "cmd/mcp-lsp/installer/host_platform_windows_arm64_e2e_test.go", tag: "windows && arm64 && e2e"},
		{path: "cmd/mcp-lsp/lsp_binary_real_node_all_languages_windows_e2e_test.go", tag: "windows && e2e"},
		{path: "cmd/mcp-lsp/lsp_binary_real_prisma_windows_arm64_raw_protocol_matrix_e2e_test.go", tag: "windows && arm64 && e2e"},
		{path: "cmd/mcp-lsp/lsp_binary_windows_arm64_emmylua_e2e_test.go", tag: "windows && arm64 && e2e"},
		{path: "cmd/mcp-lsp/windows_arm64_process_arm64_csharp_mcp_36_soak_e2e_test.go", tag: "windows && arm64 && e2e"},
		{path: "cmd/mcp-lsp/windows_arm64_process_arm64_go_sqls_36_action_e2e_test.go", tag: "windows && arm64 && e2e"},
		{path: "cmd/mcp-lsp/windows_arm64_process_arm64_gopls_4x36_soak_e2e_test.go", tag: "windows && arm64 && e2e"},
		{path: "cmd/mcp-lsp/windows_arm64_process_arm64_java_mcp_36_soak_e2e_test.go", tag: "windows && arm64 && e2e"},
		{path: "cmd/mcp-lsp/windows_arm64_process_arm64_markdown_soak_15m_e2e_test.go", tag: "windows && arm64 && e2e"},
		{path: "cmd/mcp-lsp/windows_arm64_process_arm64_native_catalog_15x36_soak_e2e_test.go", tag: "windows && arm64 && e2e"},
		{path: "cmd/mcp-lsp/windows_arm64_process_arm64_node_17x36_soak_e2e_test.go", tag: "windows && arm64 && e2e"},
		{path: "cmd/mcp-lsp/windows_arm64_process_arm64_ruby_36_soak_e2e_test.go", tag: "windows && arm64 && e2e"},
		{path: "cmd/mcp-lsp/windows_arm64_process_arm64_swift_mcp_36_soak_e2e_test.go", tag: "windows && arm64 && e2e"},
		{path: "cmd/mcp-lsp/windows_arm64_process_arm64_vue_cache_prep_e2e_test.go", tag: "windows && arm64 && e2e"},
		{path: "cmd/mcp-lsp/windows_arm64_process_arm64_vue_mcp_36_soak_e2e_test.go", tag: "windows && arm64 && e2e"},
		{path: "internal/platform/appupdaterecovery/artifact_darwin_arm64_e2e_test.go", tag: "darwin && arm64 && e2e"},
	}
	for _, testCase := range cases {
		t.Run(testCase.path, func(t *testing.T) {
			source := readRepoFile(t, filepath.Join("..", filepath.FromSlash(testCase.path)))
			if got := lspGoBuildTag(source); got != testCase.tag {
				t.Fatalf("%s source gate=%q, want %q; runtime Skip cannot replace this gate", testCase.path, got, testCase.tag)
			}
			expression, err := constraint.Parse("//go:build " + testCase.tag)
			if err != nil {
				t.Fatalf("parse %s source gate %q: %v", testCase.path, testCase.tag, err)
			}
			if violations := lspBuildConstraintMatrixViolations(testCase.tag, expression); len(violations) != 0 {
				t.Fatalf("%s source gate leaks its platform matrix: %v", testCase.path, violations)
			}
		})
	}
}

// TestLSPSQLitePackageSmokeCompanionSourceGates 锁定普通 scripts 新拆出的平台 companion
// 也必须用精确源码 gate；它们不是 E2E，但不能退回 runtime.GOOS 分支。
func TestLSPSQLitePackageSmokeCompanionSourceGates(t *testing.T) {
	cases := []struct {
		path string
		tag  string
	}{
		{path: "scripts/sqlite_release_gate_package_smoke_windows_test.go", tag: "windows"},
		{path: "scripts/sqlite_release_gate_package_smoke_darwin_test.go", tag: "darwin"},
		{path: "scripts/sqlite_release_gate_package_smoke_nonwindows_test.go", tag: "!windows"},
		{path: "scripts/sqlite_release_gate_package_smoke_nonwindows_nondarwin_platform_test.go", tag: "!windows && !darwin"},
	}
	for _, testCase := range cases {
		t.Run(testCase.path, func(t *testing.T) {
			source := readRepoFile(t, filepath.Join("..", filepath.FromSlash(testCase.path)))
			if got := lspGoBuildTag(source); got != testCase.tag {
				t.Fatalf("%s source gate=%q, want %q", testCase.path, got, testCase.tag)
			}
			expression, err := constraint.Parse("//go:build " + testCase.tag)
			if err != nil {
				t.Fatalf("parse %s source gate %q: %v", testCase.path, testCase.tag, err)
			}
			if violations := lspBuildConstraintMatrixViolations(testCase.tag, expression); len(violations) != 0 {
				t.Fatalf("%s source gate leaks its supported platform matrix: %v", testCase.path, violations)
			}
		})
	}
}

func TestWindowsPlatformVisibleDeliveryNamesStaySynchronized(t *testing.T) {
	documents := map[string][]string{
		"../bin/LSP/README.md": {
			"mcp-lsp-windows-arm64.exe | Windows ARM64",
			"mcp-lsp-windows-x64.exe | Windows x64",
			"mcp-lsp-windows-x86.exe | Windows x86",
			"OSArchitecture",
			"ProcessArchitecture",
			"最低为 15 分钟",
			"authorization_required",
		},
		"../bin/LSP/codex-lsp-config.example.toml": {
			"Windows ARM64       -> bin/LSP/mcp-lsp-windows-arm64.exe",
			"Windows x64         -> bin/LSP/mcp-lsp-windows-x64.exe",
			"Windows x86         -> bin/LSP/mcp-lsp-windows-x86.exe",
			"OSArchitecture",
			"ProcessArchitecture",
			"cannot be configured below 15m",
		},
		"../bin/LSP/mcp-lsp-project-config-skill/SKILL.md": {
			"mcp-lsp-windows-arm64.exe | windows/arm64",
			"mcp-lsp-windows-x64.exe | windows/amd64",
			"mcp-lsp-windows-x86.exe | windows/386",
			"OSVersion",
			"OSArchitecture",
			"ProcessArchitecture",
			"最低为 15 分钟",
			"authorization_required",
		},
		"../bin/LSP/mcp-lsp-project-config-skill/references/provider-configs.md": {
			"Windows | ARM64 | bin/LSP/mcp-lsp-windows-arm64.exe",
			"Windows | x64 | bin/LSP/mcp-lsp-windows-x64.exe",
			"Windows | x86 | bin/LSP/mcp-lsp-windows-x86.exe",
			"OSArchitecture",
			"ProcessArchitecture",
			"硬下限",
			"authorization_required",
		},
	}

	for path, required := range documents {
		content := readRepoFile(t, path)
		for _, fragment := range required {
			if !strings.Contains(content, fragment) {
				t.Errorf("%s missing Windows platform delivery contract %q", path, fragment)
			}
		}
		for _, forbidden := range []string{
			"mcp-lsp-windows-arm.exe",
			"Windows x86-64 | bin/LSP/mcp-lsp-windows-x86.exe",
		} {
			if strings.Contains(content, forbidden) {
				t.Errorf("%s contains stale Windows delivery name %q", path, forbidden)
			}
		}
	}
}
