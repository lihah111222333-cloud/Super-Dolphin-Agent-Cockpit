package main

import (
	"path/filepath"
	"slices"
	"strings"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// frontendLintRelevant 覆盖 ESLint 实际扫描文件和决定命令、依赖的执行种子。
func frontendLintRelevant(file string) bool {
	switch file {
	case "frontend-app/package.json", "frontend-app/package-lock.json", "frontend-app/eslint.config.js":
		return true
	default:
		return frontendLintSourceRelevant(file)
	}
}

func frontendLintSourceRelevant(file string) bool {
	if !strings.HasPrefix(file, "frontend-app/") ||
		strings.HasPrefix(file, "frontend-app/node_modules/") ||
		strings.HasPrefix(file, "frontend-app/dist/") {
		return false
	}
	switch filepath.Ext(file) {
	case ".js", ".jsx", ".mjs":
		return true
	default:
		return false
	}
}

// frontendDiagnosticsRelevant 仅把语言服务器可诊断的前端源码和工具脚本加入证据范围。
func frontendDiagnosticsRelevant(file string) bool {
	return frontendLintSourceRelevant(file)
}

// frontendStaticGuardInputRelevant 覆盖 guard:critical-skip 扫描根和直接读取的 registry/baseline。
func frontendStaticGuardInputRelevant(file string) bool {
	switch file {
	case "frontend-app/package.json",
		"frontend-app/package-lock.json",
		"frontend-app/.frontend_code_size_guard_baseline.json",
		"frontend-app/.frontend_code_size_guard_baseline_test.json",
		"frontend-app/config/action-producer-registry.json",
		"frontend-app/config/action-producer-test-matrix.json":
		return true
	}
	if strings.HasPrefix(file, "frontend-app/src/") {
		return frontendStaticScannedExtension(file, true)
	}
	if strings.HasPrefix(file, "frontend-app/scripts/") {
		return frontendStaticScannedExtension(file, true) || filepath.Ext(file) == ".json"
	}
	if strings.HasPrefix(file, "frontend-app/tests/") {
		return frontendStaticScannedExtension(file, false)
	}
	return false
}

func frontendStaticScannedExtension(file string, includeCSS bool) bool {
	switch filepath.Ext(file) {
	case ".js", ".jsx", ".mjs", ".ts", ".tsx":
		return true
	case ".css":
		return includeCSS
	default:
		return false
	}
}

// frontendPerformanceRelevant 覆盖远端性能 owner、契约测试、受管 subject 与其执行入口，防止证据或预算变化绕过 verifier。
func frontendPerformanceRelevant(file string) bool {
	if slices.Contains(gatecontract.FrontendPerformanceInputPaths(), file) {
		return true
	}
	switch file {
	case "frontend-app/scripts/chat-history-benchmark.test.mjs",
		"frontend-app/scripts/evidence-provenance.test.mjs",
		"frontend-app/scripts/performance-baseline-provenance.test.mjs",
		"frontend-app/scripts/performance-budget-model.test.mjs",
		"frontend-app/scripts/performance-budget-runner.test.mjs",
		"frontend-app/scripts/resource-budget.test.mjs",
		"frontend-app/scripts/stop-feedback-benchmark.test.mjs":
		return true
	default:
		return false
	}
}

// frontendChangedTestRelevant 只把 commit-time 前端测试路由到 diff 命中的 Vitest 目标。
func frontendChangedTestRelevant(file string) bool {
	if frontendPreCommitSmokeOrE2EFile(file) {
		return false
	}
	if frontendChangedTestDeferredToPerformance(file) {
		return false
	}
	if strings.HasPrefix(file, "frontend-app/src/") {
		return frontendScriptOrSourceFile(file)
	}
	if strings.HasPrefix(file, "frontend-app/scripts/") {
		return frontendScriptOrSourceFile(file)
	}
	return false
}

// frontendChangedTestDeferredToPerformance 识别被默认 Vitest 配置排除、由远端性能 owner 独占的测试闭包。
func frontendChangedTestDeferredToPerformance(file string) bool {
	if !frontendPerformanceRelevant(file) || !frontendScriptOrSourceFile(file) {
		return false
	}
	if file == "frontend-app/scripts/runtime/git-environment.mjs" {
		return true
	}
	rel := strings.TrimPrefix(file, "frontend-app/")
	if isFrontendTestFile(rel) {
		return frontendVitestDefaultExcludedTestFile(rel)
	}
	candidate := pairedFrontendTestFile(rel)
	return candidate != "" && frontendVitestDefaultExcludedTestFile(candidate)
}

// frontendPreCommitSmokeOrE2EFile 排除会启动桌面 smoke/E2E 进程的前端测试入口。
func frontendPreCommitSmokeOrE2EFile(file string) bool {
	switch file {
	case "frontend-app/scripts/delivery-smoke-runner.mjs",
		"frontend-app/scripts/delivery-smoke-runner.test.mjs":
		return true
	default:
		return false
	}
}

// frontendScriptOrSourceFile 判断路径是否是可由 Vitest 同名配对覆盖的前端脚本或源码。
func frontendScriptOrSourceFile(file string) bool {
	switch filepath.Ext(file) {
	case ".js", ".jsx", ".mjs", ".ts", ".tsx":
		return true
	default:
		return false
	}
}
