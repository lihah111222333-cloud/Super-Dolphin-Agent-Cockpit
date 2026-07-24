package main

import (
	"path/filepath"
	"strings"
)

// frontendLintRelevant 只把 ESLint 能读取的前端源码、测试和工具脚本路由到 lint。
func frontendLintRelevant(file string) bool {
	return frontendScriptOrSourceFile(file) &&
		(strings.HasPrefix(file, "frontend-app/src/") ||
			strings.HasPrefix(file, "frontend-app/scripts/") ||
			file == "frontend-app/eslint.config.js" ||
			file == "frontend-app/vite.config.js")
}

// frontendBuildRelevant 只覆盖会进入 Vite bundle 或嵌入产物契约的输入。
func frontendBuildRelevant(file string) bool {
	switch file {
	case "frontend-app/package.json",
		"frontend-app/package-lock.json",
		"frontend-app/vite.config.js",
		"frontend-app/index.html",
		"frontend-app/recovery.html",
		"frontend-app/required-dist-entries.txt",
		"frontend-app/scripts/sync-frontend-dist.mjs":
		return true
	}
	if strings.HasPrefix(file, "frontend-app/public/") {
		return true
	}
	if !strings.HasPrefix(file, "frontend-app/src/") {
		return false
	}
	rel := strings.TrimPrefix(file, "frontend-app/")
	return !isFrontendTestFile(rel)
}

// frontendDiagnosticsRelevant 仅把语言服务器可诊断的前端源码和工具脚本加入证据范围。
func frontendDiagnosticsRelevant(file string) bool {
	return frontendLintRelevant(file)
}

// frontendProductionScriptRelevant 判断文件是否是会参与静态架构约束的生产脚本。
func frontendProductionScriptRelevant(file string) bool {
	if !strings.HasPrefix(file, "frontend-app/src/") || !frontendScriptOrSourceFile(file) {
		return false
	}
	return !isFrontendTestFile(strings.TrimPrefix(file, "frontend-app/"))
}

// frontendPerformanceRelevant 覆盖性能 runner、契约测试、受管 subject 与其执行入口，防止证据或预算变化绕过 verifier。
func frontendPerformanceRelevant(file string) bool {
	switch file {
	case "frontend-app/package.json",
		"frontend-app/vite.config.js",
		"frontend-app/scripts/chat-history-benchmark.mjs",
		"frontend-app/scripts/chat-history-benchmark.test.mjs",
		"frontend-app/scripts/evidence-provenance.mjs",
		"frontend-app/scripts/evidence-provenance.test.mjs",
		"frontend-app/scripts/frontend-maintainability-baseline.json",
		"frontend-app/scripts/frontend-performance-cases.json",
		"frontend-app/scripts/managed-command.mjs",
		"frontend-app/scripts/performance-baseline-provenance.mjs",
		"frontend-app/scripts/performance-baseline-provenance.test.mjs",
		"frontend-app/scripts/performance-budget-config.mjs",
		"frontend-app/scripts/performance-budget-model.mjs",
		"frontend-app/scripts/performance-budget-model.test.mjs",
		"frontend-app/scripts/performance-budget-runner.mjs",
		"frontend-app/scripts/performance-budget-runner.test.mjs",
		"frontend-app/scripts/render-isolation-probe.test.jsx",
		"frontend-app/scripts/resource-budget.mjs",
		"frontend-app/scripts/resource-budget.test.mjs",
		"frontend-app/scripts/stop-feedback-benchmark.mjs",
		"frontend-app/scripts/stop-feedback-benchmark.test.mjs",
		"frontend-app/src/entities/client/model/contractStoreModel.js",
		"frontend-app/src/entities/client/model/threadLifecycleRuntime.js",
		"frontend-app/src/pages/chat/components/ChatActionFeedback.js":
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

// frontendChangedTestDeferredToPerformance 识别被默认 Vitest 配置排除、由 push 性能验证独占的测试闭包。
func frontendChangedTestDeferredToPerformance(file string) bool {
	if !frontendPerformanceRelevant(file) || !frontendScriptOrSourceFile(file) {
		return false
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
