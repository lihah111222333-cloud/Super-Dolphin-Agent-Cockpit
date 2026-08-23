package catalog

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"strconv"
	"strings"
)

const canonicalCatalogImportPath = "github.com/lihah111222333-cloud/super-dolphin-agent/scripts/mcp_lsp_workload_catalog"
const canonicalCatalogGuardImportPath = "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/mcplspguardcore"

// IsWorkloadConsumerSource 判断生产源码是否消费 catalog authority；调用方负责排除 owner 与测试文件。
func IsWorkloadConsumerSource(relative string, raw []byte, workloadIDs map[string]struct{}) (bool, error) {
	if relative == "Makefile" || strings.HasSuffix(relative, ".sh") {
		if relative == "scripts/run_mcp_lsp_workload.sh" || relative == "scripts/check_mcp_lsp_workload_catalog.sh" {
			return true, nil
		}
		return shellSourceInvokesCatalogConsumer(raw)
	}
	if !strings.HasSuffix(relative, ".go") || strings.HasSuffix(relative, "_test.go") {
		return false, nil
	}
	if strings.HasPrefix(path.Clean(relative), "internal/devtools/mcplspguardcore/") {
		return true, nil
	}
	file, err := parser.ParseFile(token.NewFileSet(), relative, raw, parser.SkipObjectResolution)
	if err != nil {
		return false, err
	}
	if importsCanonicalCatalog(file) && goSourceHasCatalogConsumer(file, workloadIDs) {
		return true, nil
	}
	return goSourceHasCatalogConsumer(file, workloadIDs), nil
}

// importsCanonicalCatalog 检查 Go 文件是否直接依赖唯一 catalog owner。
func importsCanonicalCatalog(file *ast.File) bool {
	for _, importSpec := range file.Imports {
		value, err := strconv.Unquote(importSpec.Path.Value)
		if err == nil && (value == canonicalCatalogImportPath || value == canonicalCatalogGuardImportPath) {
			return true
		}
	}
	return false
}

// goSourceHasCatalogConsumer 从 AST 调用/参数节点识别非 import consumer。
func goSourceHasCatalogConsumer(file *ast.File, workloadIDs map[string]struct{}) bool {
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		if found {
			return false
		}
		found = catalogConsumerLiteral(node, workloadIDs) || catalogConsumerIdentifier(node)
		return !found
	})
	return found
}

// catalogConsumerLiteral 匹配真实脚本入口或当前 catalog 的精确 workload ID。
func catalogConsumerLiteral(node ast.Node, workloadIDs map[string]struct{}) bool {
	literal, ok := node.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return false
	}
	value, err := strconv.Unquote(literal.Value)
	if err != nil {
		return false
	}
	_, workloadID := workloadIDs[value]
	return isCatalogConsumerCommand(value) || workloadID
}

// catalogConsumerIdentifier 匹配无 catalog import 的 executor/平台 adapter 调用链。
func catalogConsumerIdentifier(node ast.Node) bool {
	identifier, ok := node.(*ast.Ident)
	if !ok {
		return false
	}
	switch identifier.Name {
	case "runnerPlatformWorkload",
		"loadRunnerWorkload", "bindMcpLSPWorkloadSelectors", "runMcpLSPCatalogGuard", "runMcpLSPCatalogGuardWorker", "runMcpLSPQuickReleaseAuthority",
		"workloadGateInputFileSet", "mcpLSPWorkloadRelevant", "applyOwnedGateRules", "ownedGateRunners":
		return true
	case "remoteGitOutput":
		return true
	default:
		return false
	}
}

// shellSourceInvokesCatalogConsumer 从可执行 shell/Make 行识别精确 runner/guard 入口。
func shellSourceInvokesCatalogConsumer(raw []byte) (bool, error) {
	found := false
	for line := range strings.Lines(string(raw)) {
		code := strings.TrimSpace(line)
		if code == "" || strings.HasPrefix(code, "#") {
			continue
		}
		if comment := strings.Index(code, " #"); comment >= 0 {
			code = strings.TrimSpace(code[:comment])
		}
		invokes, err := shellCatalogConsumerLine(code)
		if err != nil {
			return false, err
		}
		found = found || invokes
	}
	return found, nil
}

func shellCatalogConsumerLine(code string) (bool, error) {
	if !strings.Contains(code, "run_mcp_lsp_workload.sh") && !strings.Contains(code, "check_mcp_lsp_workload_catalog.sh") {
		return false, nil
	}
	fields := strings.Fields(code)
	commandIndex := 0
	if len(fields) > 0 && fields[0] == "exec" {
		commandIndex = 1
	} else if len(fields) > 0 && fields[0] == `SUPER_DOLPHIN_GATE_SELF="$(SUPER_DOLPHIN_GATE_SELF)"` {
		commandIndex = 1
	}
	if commandIndex >= len(fields) || !isCatalogConsumerCommand(strings.Trim(fields[commandIndex], `"'`)) {
		return false, fmt.Errorf("indirect catalog consumer command is unsupported")
	}
	return true, nil
}

func isCatalogConsumerCommand(value string) bool {
	return value == "./scripts/run_mcp_lsp_workload.sh" || value == "./scripts/check_mcp_lsp_workload_catalog.sh"
}

// IsWorkloadConsumerPathExcluded 排除 canonical owner 自身与测试文件，避免自发现递归。
func IsWorkloadConsumerPathExcluded(relative string) bool {
	clean := path.Clean(relative)
	return strings.HasPrefix(clean, "scripts/mcp_lsp_workload_catalog/") ||
		strings.HasSuffix(clean, "_test.go")
}

// IsWorkloadConsumerCandidatePath 把 registry discovery 限制在可解析生产源码类型。
func IsWorkloadConsumerCandidatePath(relative string) bool {
	if IsWorkloadConsumerPathExcluded(relative) {
		return false
	}
	return relative == "Makefile" || strings.HasSuffix(relative, ".sh") || strings.HasSuffix(relative, ".go")
}
