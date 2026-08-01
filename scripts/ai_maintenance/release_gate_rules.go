package main

import "strings"

var nightlyProtocolFiles = map[string]bool{
	"docs/automation/全仓夜间门禁健康巡检协议.md": true,
	"docs/automation/门禁问题台账接管协议.md":   true,
	"docs/automation/授权问题修复与验证协议.md":  true,
}

var archtestNonGoInputFiles = map[string]bool{
	"cmd/mcp-orch/sqlc.yaml":                true,
	"docs/契约/modularity-convention.md":      true,
	"frontend-app/src/App.jsx":              true,
	"frontend-app/src/app/appShellModel.js": true,
	"frontend-app/src/features/slash-commands/adapters/skillInfoFieldRegistry.json": true,
	"go.mod":                                 true,
	"internal/archtest/freeze_baseline.json": true,
	"scripts/ci_cross_platform_smoke.ps1":    true,
	"scripts/ai_maintenance_gates.sh":        true,
	"scripts/codemap_policy.txt":             true,
	"scripts/test_with_guard.ps1":            true,
	"scripts/test_with_guard.sh":             true,
	"sqlc.yaml":                              true,
}

var archtestNonGoInputPrefixes = []string{
	".githooks/",
	"cmd/mcp-orch/sql/queries/",
	"docs/guards/",
	"internal/guards/",
	"internal/platform/db/sqlite/migrations/",
	"internal/platform/shared/builtinprompts/assets/",
	"internal/provider/_template/",
	"migrations/",
	"sql/queries/",
}

func workflowRelevant(file string) bool {
	return strings.HasPrefix(file, ".github/workflows/") &&
		(strings.HasSuffix(file, ".yml") || strings.HasSuffix(file, ".yaml"))
}

// releaseInfrastructureRelevant 识别发布工作流、打包脚本、bundle 准备器及其语义守卫所有者。
func releaseInfrastructureRelevant(file string) bool {
	if workflowRelevant(file) {
		return true
	}
	for _, prefix := range []string{
		"scripts/package_",
		"scripts/prepare_lsp_bundle_",
		"scripts/publish_github_release",
		"scripts/release_rollout_",
		"scripts/update_recovery_release_gate",
		"scripts/verify_packaged_app_",
	} {
		if strings.HasPrefix(file, prefix) {
			return true
		}
	}
	return file == "internal/devtools/sqlitereleasegate/gates.go" ||
		file == "docs/scripts/macos_release_smoke.sh" ||
		file == "scripts/sqlite_release_gate_package_smoke_runtime_test.go"
}

// archtestNonGoInputRelevant 识别会改变架构门禁结论但无法由 Go 包路径捕获的输入。
func archtestNonGoInputRelevant(file string) bool {
	if workflowRelevant(file) || archtestNonGoInputFiles[file] {
		return true
	}
	for _, prefix := range archtestNonGoInputPrefixes {
		if strings.HasPrefix(file, prefix) {
			return true
		}
	}
	return false
}

func nightlyProtocolRelevant(file string) bool {
	return nightlyProtocolFiles[file] ||
		strings.HasPrefix(file, "scripts/nightly_protocol_validator/") ||
		file == "Makefile" ||
		file == ".github/workflows/ci.yml"
}

// applyOwnedGateRules 将各门禁所有者输入路由到不可跳过的语义验证。
func applyOwnedGateRules(file string, gates map[string]bool) {
	if aiMaintenanceRelevant(file) {
		gates["ai-maintenance:self-test"] = true
	}
	if workflowRelevant(file) {
		gates["workflow:actionlint"] = true
		gates["release:semantic-guards"] = true
	}
	if releaseInfrastructureRelevant(file) {
		gates["release:semantic-guards"] = true
	}
	if archtestNonGoInputRelevant(file) {
		gates["backend:archtest"] = true
	}
	if strings.HasSuffix(file, "_test.go") {
		gates["backend:test-integrity"] = true
	}
	if nightlyProtocolRelevant(file) {
		gates["nightly-protocol:check"] = true
	}
}
