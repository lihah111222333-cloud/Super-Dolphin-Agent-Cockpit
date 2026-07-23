package main

import (
	"path/filepath"
	"strings"
)

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
	if strings.HasPrefix(file, "frontend-app/src/") {
		return frontendScriptOrSourceFile(file)
	}
	if strings.HasPrefix(file, "frontend-app/scripts/") {
		return frontendScriptOrSourceFile(file)
	}
	return false
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
