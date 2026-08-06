package main

import "strings"

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
func (policy gatePlanPolicy) archtestNonGoInputRelevant(file string) bool {
	if workflowRelevant(file) || policy.archtestNonGoInputFiles[file] {
		return true
	}
	for _, prefix := range policy.archtestNonGoInputPrefixes {
		if strings.HasPrefix(file, prefix) {
			return true
		}
	}
	return false
}

func (policy gatePlanPolicy) nightlyProtocolRelevant(file string) bool {
	return policy.nightlyProtocolFiles[file] ||
		strings.HasPrefix(file, "scripts/nightly_protocol_validator/") ||
		file == "Makefile" ||
		file == ".github/workflows/ci.yml"
}

// applyOwnedGateRules 将各门禁所有者输入路由到不可跳过的语义验证。
func (policy gatePlanPolicy) applyOwnedGateRules(file string, gates map[string]bool) {
	if policy.aiMaintenanceRelevant(file) {
		gates["ai-maintenance:self-test"] = true
	}
	if workflowRelevant(file) {
		gates["workflow:actionlint"] = true
		gates["release:semantic-guards"] = true
	}
	if releaseInfrastructureRelevant(file) {
		gates["release:semantic-guards"] = true
	}
	if policy.archtestNonGoInputRelevant(file) {
		gates["backend:archtest"] = true
	}
	if strings.HasSuffix(file, "_test.go") {
		gates["backend:test-integrity"] = true
	}
	if policy.nightlyProtocolRelevant(file) {
		gates["nightly-protocol:check"] = true
	}
	if policy.mcpLSPWorkloadRelevant(file) {
		gates["mcp-lsp:catalog"] = true
		gates["mcp-lsp:idle-quick"] = true
	}
}

// mcpLSPWorkloadRelevant 将目录、runner 与 mcp-lsp 源码漂移路由到统一目录守卫。
func (policy gatePlanPolicy) mcpLSPWorkloadRelevant(file string) bool {
	if policy.mcpLSPWorkloadExactFiles[file] {
		return true
	}
	for _, prefix := range policy.mcpLSPWorkloadPrefixes {
		if strings.HasPrefix(file, prefix) {
			return true
		}
	}
	return false
}
