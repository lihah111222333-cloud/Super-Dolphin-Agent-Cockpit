package main

import (
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/scripts/mcp_lsp_workload_catalog"
)

const mcpLSPDefault15mWorkloadID = "mcp-lsp-default-15m"

// runTestInvocation 固定 test 场景，并把所有工作负载交给权威远程 ECI 协调器。
func runTestInvocation(args []string, stdout io.Writer) error {
	if err := requireRemoteCIAgentToken([]string{"test"}, args, stdout); err != nil {
		return err
	}
	options, err := parseAutoTestRunOptions(args)
	if err != nil {
		return err
	}
	result, input, runErr := executeRemoteRun(options)
	if runErr == nil && options.WorkloadID != "" && options.CompletionReceiptPath != "" {
		runErr = fmt.Errorf("workload %q is N/V: remote run/job/artifact authority binding is unavailable", options.WorkloadID)
	}
	return emitRemoteRunResult(stdout, input.LedgerStore, result, runErr)
}

// parseAutoTestRunOptions 只接受测试选择器并固定 test 场景。
func parseAutoTestRunOptions(args []string) (remoteRunOptions, error) {
	options, err := parseRemoteRunOptions(args)
	if err != nil {
		return remoteRunOptions{}, err
	}
	if err := validateAutoTestFlags(options); err != nil {
		return remoteRunOptions{}, err
	}
	if err := bindOrValidateAutoTestSelectors(&options); err != nil {
		return remoteRunOptions{}, err
	}
	options.Scenario = "test"
	return options, nil
}

// validateAutoTestFlags 拒绝 test 命令接管的场景、推送和非绝对回执参数。
func validateAutoTestFlags(options remoteRunOptions) error {
	if options.Scenario != "" || options.Profile != "" || options.Entrypoint != "" ||
		options.LocalRef != "" || options.RemoteRef != "" || options.ObservedRemote != "" ||
		options.UpdateKind != "" {
		return protocolError("test command does not accept scenario, profile, entrypoint, or push flags")
	}
	if options.CompletionReceiptPath != "" && !filepath.IsAbs(options.CompletionReceiptPath) {
		return protocolError("--completion-receipt must be an absolute path")
	}
	return nil
}

// bindOrValidateAutoTestSelectors 绑定 catalog workload 或校验独立 test 选择器。
func bindOrValidateAutoTestSelectors(options *remoteRunOptions) error {
	if options.WorkloadID != "" {
		if err := bindMcpLSPWorkloadSelectors(options); err != nil {
			return protocolError("bind workload %q: %v", options.WorkloadID, err)
		}
		return nil
	}
	return validateStandaloneTestSelectors(*options)
}

// validateStandaloneTestSelectors 校验没有 workload 绑定时的 CLI 选择器边界。
func validateStandaloneTestSelectors(options remoteRunOptions) error {
	if options.CompletionReceiptPath != "" {
		return protocolError("--completion-receipt requires --workload")
	}
	if len(options.Tests) == 0 {
		return protocolError("test command requires at least one --test selector")
	}
	return nil
}

// bindMcpLSPWorkloadSelectors 在请求进入 ECI 前解析目录 workload；实现缺失、
// Windows 不支持和选择器漂移均显式返回 N/V/协议错误，不回退到本地测试。
func bindMcpLSPWorkloadSelectors(options *remoteRunOptions) error {
	if err := validateMcpLSPBindingInput(options); err != nil {
		return err
	}
	repository, err := filepath.Abs(options.RepositoryRoot)
	if err != nil {
		return fmt.Errorf("resolve repository root: %w", err)
	}
	candidateHead, candidateTree, err := resolveMcpLSPCandidateIdentity(repository, *options)
	if err != nil {
		return err
	}
	document, err := catalog.LoadAt(repository, candidateTree)
	if err != nil {
		return err
	}
	workload, err := document.Find(options.WorkloadID)
	if err != nil {
		return err
	}
	if err := validateMcpLSPWorkload(workload, *options); err != nil {
		return err
	}
	selectors, err := catalog.RemoteTestSelectors(workload.Command)
	if err != nil {
		return err
	}
	return applyMcpLSPWorkloadBinding(options, candidateHead, candidateTree, selectors)
}

// validateMcpLSPBindingInput 校验 workload 绑定所需的指针和 ID。
func validateMcpLSPBindingInput(options *remoteRunOptions) error {
	if options == nil || strings.TrimSpace(options.WorkloadID) == "" {
		return fmt.Errorf("workload ID is required")
	}
	return nil
}

// validateMcpLSPWorkload 执行平台、实现状态和远程 completion authority 门禁。
func validateMcpLSPWorkload(workload catalog.Workload, options remoteRunOptions) error {
	if !workload.SupportsCurrentPlatform() {
		return fmt.Errorf("platform %q is N/V for workload (registered platforms=%s)", runtime.GOOS, strings.Join(workload.Platforms, ","))
	}
	if workload.ID == mcpLSPDefault15mWorkloadID && runtime.GOOS == "windows" {
		return fmt.Errorf("default-15m workload is N/V on Windows until native daemon owner receipt is implemented")
	}
	if workload.ImplementationStatus != "implemented" {
		return mcpLSPImplementationStatusError(workload)
	}
	if workload.ProducerImplementationStatus != "implemented" {
		return fmt.Errorf("workload %q is N/V: producer_implementation_status=%s t6_blocking=%t release_blocking=%t", workload.ID, workload.ProducerImplementationStatus, workload.T6Blocking, workload.ReleaseBlocking)
	}
	if err := catalog.RequireRemoteCompletionAuthority(workload); err != nil {
		return err
	}
	if workload.TriggerClass == "default-15m-source-e2e" && options.CompletionReceiptPath == "" {
		return fmt.Errorf("default-15m requires explicit --completion-receipt")
	}
	return nil
}

// mcpLSPImplementationStatusError 保持默认 15m 与其他 workload 的 N/V 文案契约。
func mcpLSPImplementationStatusError(workload catalog.Workload) error {
	if workload.ID == mcpLSPDefault15mWorkloadID {
		return fmt.Errorf("default-15m workload is N/V: implementation_status=%s t6_blocking=%t release_blocking=%t", workload.ImplementationStatus, workload.T6Blocking, workload.ReleaseBlocking)
	}
	return fmt.Errorf("implementation_status=%s t6_blocking=%t release_blocking=%t", workload.ImplementationStatus, workload.T6Blocking, workload.ReleaseBlocking)
}

// applyMcpLSPWorkloadBinding 写入 catalog 选择器并冻结解析后的候选 commit/tree。
func applyMcpLSPWorkloadBinding(options *remoteRunOptions, candidateHead, candidateTree string, selectors []string) error {
	if len(options.Tests) == 0 {
		options.Tests = selectors
	} else if !sameSelectorSet(options.Tests, selectors) {
		return fmt.Errorf("--test selectors do not exactly match catalog workload command")
	}
	// Freeze the resolved candidate so a moving HEAD or symbolic revision cannot
	// change the source between catalog binding and remote execution.
	if options.Tree != "" {
		options.Tree = candidateTree
		options.ParentCommit = candidateHead
	} else {
		options.Commit = candidateHead
	}
	return nil
}

// resolveMcpLSPCandidateIdentity 解析显式 commit/tree 为固定的 Git 对象身份。
func resolveMcpLSPCandidateIdentity(repository string, options remoteRunOptions) (string, string, error) {
	if strings.TrimSpace(repository) == "" {
		return "", "", fmt.Errorf("repository root is required")
	}
	if options.Tree != "" {
		if options.ParentCommit == "" {
			return "", "", fmt.Errorf("workload candidate tree requires --parent commit")
		}
		tree, err := remoteGitOutput(repository, "rev-parse", "--verify", "--end-of-options", options.Tree+"^{tree}")
		if err != nil {
			return "", "", fmt.Errorf("resolve workload candidate tree: %w", err)
		}
		head, err := remoteGitOutput(repository, "rev-parse", "--verify", "--end-of-options", options.ParentCommit+"^{commit}")
		if err != nil {
			return "", "", fmt.Errorf("resolve workload candidate parent commit: %w", err)
		}
		return head, tree, nil
	}
	if options.Commit == "" {
		return "", "", fmt.Errorf("workload candidate commit or tree is required")
	}
	head, err := remoteGitOutput(repository, "rev-parse", "--verify", "--end-of-options", options.Commit+"^{commit}")
	if err != nil {
		return "", "", fmt.Errorf("resolve workload candidate commit: %w", err)
	}
	tree, err := remoteGitOutput(repository, "rev-parse", "--verify", "--end-of-options", head+"^{tree}")
	if err != nil {
		return "", "", fmt.Errorf("resolve workload candidate tree: %w", err)
	}
	return head, tree, nil
}

func sameSelectorSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	leftCopy, rightCopy := append([]string(nil), left...), append([]string(nil), right...)
	slices.Sort(leftCopy)
	slices.Sort(rightCopy)
	return slices.Equal(leftCopy, rightCopy)
}
