package gate

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

const mcpLSPCatalogGuardScript = "./scripts/check_mcp_lsp_workload_catalog.sh"
const mcpLSPCatalogGuardWorker = "mcp-lsp-catalog-guard"

// resolveExecutorCandidateTree 读取不可变 executor snapshot 的 tree；所有 catalog guard
// invocation 都必须显式携带该值。
func resolveExecutorCandidateTree(ctx context.Context, gitBinary, sourceCopy string) (string, error) {
	if ctx == nil || gitBinary == "" || sourceCopy == "" {
		return "", errors.New("executor candidate tree inputs are required")
	}
	output, err := BoundedGitOutput(ctx, gitBinary, sourceCopy, "rev-parse", "--verify", "HEAD^{tree}")
	if err != nil {
		return "", fmt.Errorf("resolve executor candidate tree: %w", err)
	}
	tree := strings.TrimSpace(string(output))
	if !validCalibrationOID(tree) {
		return "", errors.New("executor candidate tree is not a full object id")
	}
	return tree, nil
}

// injectMcpLSPCandidateTree 将 catalog guard/Make 入口绑定到精确 immutable snapshot，
// 已存在的 tree 参数绝不静默覆盖。
func injectMcpLSPCandidateTree(argv []string, candidateTree string) ([]string, error) {
	return injectMcpLSPCandidateTreeAt(argv, candidateTree, "")
}

// injectMcpLSPCandidateTreeAt 将 self guard 同时绑定到 executor 的绝对只读 snapshot。
func injectMcpLSPCandidateTreeAt(argv []string, candidateTree, repository string) ([]string, error) {
	if len(argv) == 0 || strings.TrimSpace(candidateTree) == "" || !validCalibrationOID(candidateTree) {
		return nil, errors.New("catalog guard candidate tree is required")
	}
	if isMcpLSPCatalogGuardWorker(argv) {
		return injectMcpLSPWorkerAuthority(argv, candidateTree, repository)
	}
	switch argv[0] {
	case mcpLSPCatalogGuardScript:
		return injectMcpLSPGuardTree(argv, candidateTree)
	case "make":
		if slices.Contains(argv, "mcp-lsp-workload-catalog-check") {
			return injectMcpLSPMakeTree(argv, candidateTree)
		}
	}
	return slices.Clone(argv), nil
}

func injectMcpLSPWorkerAuthority(argv []string, candidateTree, repository string) ([]string, error) {
	if !filepath.IsAbs(repository) || hasCatalogTreeArgument(argv) || hasCatalogRepositoryArgument(argv) {
		return nil, errors.New("catalog guard worker requires one executor-owned repository and tree")
	}
	return append(slices.Clone(argv), "--repository", repository, "--tree", candidateTree), nil
}

func isMcpLSPCatalogGuardWorker(argv []string) bool {
	return len(argv) == 3 && argv[0] == ExecutorSelfCommandName && argv[1] == "worker" && argv[2] == mcpLSPCatalogGuardWorker
}

// injectMcpLSPGuardTree 向 catalog guard 脚本追加唯一 candidate tree。
func injectMcpLSPGuardTree(argv []string, candidateTree string) ([]string, error) {
	if hasCatalogTreeArgument(argv) || hasCatalogRepositoryArgument(argv) {
		return nil, errors.New("catalog guard command already contains candidate tree")
	}
	return append(slices.Clone(argv), "--tree", candidateTree), nil
}

// hasCatalogRepositoryArgument 检查 wrapper-owned repository 是否被命令参数覆盖。
func hasCatalogRepositoryArgument(argv []string) bool {
	for _, argument := range argv[1:] {
		if argument == "--repository" || strings.HasPrefix(argument, "--repository=") || argument == "-repository" || strings.HasPrefix(argument, "-repository=") {
			return true
		}
	}
	return false
}

// injectMcpLSPMakeTree 向 Make catalog target 追加可信 tree 变量。
func injectMcpLSPMakeTree(argv []string, candidateTree string) ([]string, error) {
	for _, argument := range argv[1:] {
		if strings.HasPrefix(argument, "MCP_LSP_CANDIDATE_TREE=") {
			return nil, errors.New("make catalog guard command already contains candidate tree")
		}
	}
	return append(slices.Clone(argv), "MCP_LSP_CANDIDATE_TREE="+candidateTree), nil
}

// hasCatalogTreeArgument 判断命令是否已携带 tree，拒绝重复 authority。
func hasCatalogTreeArgument(argv []string) bool {
	for _, argument := range argv[1:] {
		if argument == "--tree" || strings.HasPrefix(argument, "--tree=") {
			return true
		}
	}
	return false
}
