package main

import "strings"

const capabilityContractManifest = "docs/doc/codemap/capability-contract/capability_manifest.json"

// capabilityContractProducerInput 是能力契约生成输入路由的单一真源，
// 判断文件是否会改变基于 AST 生成的能力清单。
func capabilityContractProducerInput(file string) bool {
	return strings.HasPrefix(file, "internal/contract/") ||
		strings.HasPrefix(file, "internal/provider/") ||
		strings.HasPrefix(file, "cmd/mcp-orch/orchestration/") ||
		strings.HasPrefix(file, "cmd/mcp-orch/tools/") ||
		strings.HasPrefix(file, "internal/devtools/capcontract/") ||
		file == "scripts/capcontract.go" ||
		strings.HasPrefix(file, "scripts/capcontract/")
}

func backendGoFile(file string) bool {
	if !strings.HasSuffix(file, ".go") {
		return false
	}
	return strings.HasPrefix(file, "cmd/") ||
		strings.HasPrefix(file, "internal/") ||
		strings.HasPrefix(file, "pkg/") ||
		strings.HasPrefix(file, "scripts/")
}
