package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/ctxutil"
	"github.com/lihah111222333-cloud/super-dolphin-agent/scripts/mcp_lsp_workload_catalog"
)

type runnerOptions struct {
	tree              string
	id                string
	receipt           string
	completionReceipt string
	plan              bool
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run 解析 ID、加载目录并执行一个已实现的本地 workload。
func run(args []string) error {
	options, err := parseOptions(args)
	if err != nil {
		return err
	}
	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}
	document, workload, err := loadRunnerWorkload(repoRoot, options.tree, options.id)
	if err != nil {
		return err
	}
	canonicalWorkload, err := document.Find(options.id)
	if err != nil {
		return err
	}
	if err := validateRunnerWorkload(workload); err != nil {
		return err
	}
	if err := validateRunnerCommand(repoRoot, options.tree, workload); err != nil {
		return err
	}
	if options.plan {
		return executeRunnerWorkload(options, repoRoot, document, canonicalWorkload, workload, "")
	}
	head, err := verifyRunnerExecutionSource(repoRoot, options.tree)
	if err != nil {
		return err
	}
	return executeRunnerWorkload(options, repoRoot, document, canonicalWorkload, workload, head)
}

// loadRunnerWorkload 从受信仓库目录加载并解析目标 workload。
func loadRunnerWorkload(repoRoot, tree, id string) (catalog.Catalog, catalog.Workload, error) {
	if strings.TrimSpace(tree) == "" {
		return catalog.Catalog{}, catalog.Workload{}, errors.New("runner candidate tree is required")
	}
	document, err := catalog.LoadAt(repoRoot, tree)
	if err != nil {
		return catalog.Catalog{}, catalog.Workload{}, err
	}
	workload, err := document.Find(id)
	if err != nil {
		return catalog.Catalog{}, catalog.Workload{}, err
	}
	execution := runnerPlatformWorkload(workload)
	if err := validateRunnerPlatformView(workload, execution); err != nil {
		return catalog.Catalog{}, catalog.Workload{}, fmt.Errorf("runner platform adapter changed canonical workload %q", id)
	}
	return document, execution, nil
}

// validateRunnerPlatformView 禁止平台适配器改变任何 catalog 执行或回执字段。
func validateRunnerPlatformView(canonical, execution catalog.Workload) error {
	if !reflect.DeepEqual(canonical, execution) {
		return errors.New("runner platform adapter must preserve the canonical workload")
	}
	return nil
}

// cloneRunnerWorkload 深拷贝 runner 平台视图会触达的引用字段，禁止回写共享 catalog 对象。
func cloneRunnerWorkload(workload catalog.Workload) catalog.Workload {
	cloned := workload
	cloned.Platforms = append([]string(nil), workload.Platforms...)
	cloned.Command = append([]string(nil), workload.Command...)
	if workload.ReceiptRequired != nil {
		receiptRequired := *workload.ReceiptRequired
		cloned.ReceiptRequired = &receiptRequired
	}
	return cloned
}

// validateRunnerWorkload 在任何计划或执行前执行平台、实现和 authority 门禁。
func validateRunnerWorkload(workload catalog.Workload) error {
	if !workload.SupportsCurrentPlatform() {
		return fmt.Errorf("workload %q is not registered for platform %q", workload.ID, runtime.GOOS)
	}
	if workload.ImplementationStatus != "implemented" {
		return fmt.Errorf("workload %q is N/V: implementation_status=%s t6_blocking=%t release_blocking=%t", workload.ID, workload.ImplementationStatus, workload.T6Blocking, workload.ReleaseBlocking)
	}
	if workload.ProducerImplementationStatus != "implemented" && catalog.IsRemoteAuthoritativeWorkload(workload) {
		return fmt.Errorf("workload %q is N/V: producer_implementation_status=%s t6_blocking=%t release_blocking=%t", workload.ID, workload.ProducerImplementationStatus, workload.T6Blocking, workload.ReleaseBlocking)
	}
	return catalog.RequireRemoteCompletionAuthority(workload)
}

// validateRunnerCommand requires every direct runner execution to use the
// catalog parser and exact-tree projection; arbitrary shell commands cannot
// become workload authority merely because a catalog ID is known.
func validateRunnerCommand(repoRoot, tree string, workload catalog.Workload) error {
	parsed, err := catalog.ParseRemoteTestCommand(workload.Command)
	if err != nil {
		return fmt.Errorf("workload %q command: %w", workload.ID, err)
	}
	packages, err := catalog.LoadRemoteTestPackageFilesAt(repoRoot, tree, parsed)
	if err != nil {
		return fmt.Errorf("workload %q command source: %w", workload.ID, err)
	}
	if _, err := catalog.ProjectRemoteTestCommand(parsed, catalog.DefaultRemoteTestEnvironment(), packages); err != nil {
		return fmt.Errorf("workload %q command projection: %w", workload.ID, err)
	}
	return nil
}

// executeRunnerWorkload 解析回执路径并执行已通过门禁的 workload。
func executeRunnerWorkload(options runnerOptions, repoRoot string, document catalog.Catalog, canonicalWorkload, executionWorkload catalog.Workload, head string) error {
	if options.receipt != "" || options.completionReceipt != "" {
		return errors.New("local host workload results cannot produce an authoritative receipt")
	}
	if options.plan {
		return printPlan(document, executionWorkload)
	}
	hostCommand, err := catalog.LocalHostTestCommand(canonicalWorkload)
	if err != nil {
		return fmt.Errorf("workload %q local host command: %w", canonicalWorkload.ID, err)
	}
	return executeLocalHostCommand(repoRoot, canonicalWorkload.ID, hostCommand, head, options.tree)
}

// executeLocalHostCommand 运行唯一 host-test 入口，并在返回 N/V 前复核 immutable source。
func executeLocalHostCommand(repoRoot, workloadID string, hostCommand []string, head, tree string) error {
	name, args, err := localHostInvocationForPlatform(runtime.GOOS, hostCommand)
	if err != nil {
		return err
	}
	ctx, cancel := ctxutil.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = repoRoot
	environment, err := canonicalRunnerEnvironment(os.Environ())
	if err != nil {
		return err
	}
	if err := configureRunnerCommand(command, environment); err != nil {
		return err
	}
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	runErr := runRunnerCommand(ctx, command)
	postHead, sourceErr := verifyRunnerExecutionSource(repoRoot, tree)
	if sourceErr != nil {
		return fmt.Errorf("revalidate runner execution source: %w", sourceErr)
	}
	if postHead != head {
		return errors.New("runner HEAD changed during workload execution")
	}
	if runErr != nil {
		return fmt.Errorf("local host workload %q failed: %w", workloadID, runErr)
	}
	return fmt.Errorf("local host workload %q is LOCAL_NON_AUTHORITATIVE NOT_VERIFIED; trusted four-node Gate evidence is required", workloadID)
}

func configureRunnerCommand(command *exec.Cmd, environment []string) error {
	if command == nil {
		return errors.New("runner command is required")
	}
	command.Env = environment
	return gateprivate.ConfigureBoundedCommand(command)
}

func runRunnerCommand(ctx context.Context, command *exec.Cmd) error {
	runErr := command.Run()
	if ctx.Err() != nil {
		return errors.Join(ctx.Err(), runErr)
	}
	return runErr
}

func localHostInvocationForPlatform(goos string, hostCommand []string) (string, []string, error) {
	if goos == "windows" {
		return "", nil, errors.New("Windows local host workload is NOT_VERIFIED: the accepted host authority has no trusted Windows wrapper")
	}
	if len(hostCommand) == 0 {
		return "", nil, errors.New("local host command is empty")
	}
	return hostCommand[0], hostCommand[1:], nil
}

func canonicalRunnerEnvironment(environment []string) ([]string, error) {
	return canonicalRunnerEnvironmentForPlatform(runtime.GOOS, environment)
}

func canonicalRunnerEnvironmentForPlatform(goos string, environment []string) ([]string, error) {
	allowed := map[string]bool{
		"HOME": true, "LANG": true, "LC_ALL": true, "LOGNAME": true, "PATH": true, "SHELL": true,
		"SYSTEMROOT": true, "TEMP": true, "TMP": true, "TMPDIR": true, "USER": true, "WINDIR": true,
	}
	rejected := map[string]bool{
		"CGO_ENABLED": true, "GOENV": true, "GOFLAGS": true, "GOMODCACHE": true, "GOPATH": true,
		"GOROOT": true, "GOTOOLCHAIN": true, "GOTMPDIR": true, "GOWORK": true,
	}
	result := make([]string, 0, len(allowed)+1)
	seen := make(map[string]struct{}, len(environment))
	for _, entry := range environment {
		name, value, ok := strings.Cut(entry, "=")
		if !ok {
			return nil, errors.New("runner environment entry is invalid")
		}
		canonicalName := name
		if goos == "windows" {
			canonicalName = strings.ToUpper(name)
		}
		if _, duplicate := seen[canonicalName]; duplicate {
			return nil, fmt.Errorf("runner environment contains duplicate %s", canonicalName)
		}
		seen[canonicalName] = struct{}{}
		if rejected[canonicalName] {
			return nil, fmt.Errorf("runner rejects inherited %s", canonicalName)
		}
		if allowed[canonicalName] {
			result = append(result, canonicalName+"="+value)
		}
	}
	return append(result, "GOTOOLCHAIN=local"), nil
}

// verifyRunnerExecutionSource 证明 catalog tree 与可执行工作树完全相同且无 mutable drift。
func verifyRunnerExecutionSource(repoRoot, tree string) (string, error) {
	head, err := runnerGitIdentity(repoRoot, "HEAD^{commit}")
	if err != nil {
		return "", fmt.Errorf("resolve runner HEAD: %w", err)
	}
	actualTree, err := runnerGitIdentity(repoRoot, "HEAD^{tree}")
	if err != nil {
		return "", fmt.Errorf("resolve runner HEAD tree: %w", err)
	}
	if actualTree != tree {
		return "", fmt.Errorf("runner candidate tree mismatch: requested=%s actual=%s", tree, actualTree)
	}
	status, err := catalog.BoundedGitOutput(repoRoot, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return "", fmt.Errorf("inspect runner worktree: %w", err)
	}
	if strings.TrimSpace(string(status)) != "" {
		return "", errors.New("runner worktree is dirty; candidate tree cannot execute mutable source")
	}
	ignored, err := catalog.BoundedGitOutput(repoRoot, "ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--")
	if err != nil {
		return "", fmt.Errorf("inspect ignored runner execution sources: %w", err)
	}
	for path := range strings.SplitSeq(string(ignored), "\x00") {
		if path != "" {
			return "", fmt.Errorf("runner worktree contains ignored execution source %q", path)
		}
	}
	return head, nil
}

func runnerGitIdentity(repoRoot, revision string) (string, error) {
	output, err := catalog.BoundedGitOutput(repoRoot, "rev-parse", "--verify", "--end-of-options", revision)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(output))
	if value == "" {
		return "", errors.New("git identity is empty")
	}
	return value, nil
}

// parseOptions 解析 runner 参数并要求唯一、非空的显式 candidate tree。
func parseOptions(args []string) (runnerOptions, error) {
	canonicalArgs, err := catalog.CanonicalizeAuthorityFlags(args, "tree", "id", "receipt", "completion-receipt", "print-plan")
	if err != nil {
		return runnerOptions{}, err
	}
	args = canonicalArgs
	if err := catalog.ValidateNoDuplicateAuthorityFlags(args, "tree", "id", "receipt", "completion-receipt", "print-plan"); err != nil {
		return runnerOptions{}, err
	}
	fs := flag.NewFlagSet("run_mcp_lsp_workload", flag.ContinueOnError)
	tree := fs.String("tree", "", "resolved candidate Git tree OID")
	id := fs.String("id", "", "catalog workload ID")
	receipt := fs.String("receipt", "", "absolute or repository-relative receipt path")
	completionReceipt := fs.String("completion-receipt", "", "absolute root-cohort completion receipt path (required by default-15m)")
	plan := fs.Bool("print-plan", false, "print the catalog-resolved plan without executing it")
	if err := fs.Parse(args); err != nil {
		return runnerOptions{}, err
	}
	if fs.NArg() != 0 {
		return runnerOptions{}, fmt.Errorf("unexpected runner arguments: %s", strings.Join(fs.Args(), " "))
	}
	if strings.TrimSpace(*id) == "" {
		return runnerOptions{}, errors.New("--id is required; workload commands are catalog-owned")
	}
	if strings.TrimSpace(*tree) == "" {
		return runnerOptions{}, errors.New("--tree is required; runner workload source must be candidate-bound")
	}
	return runnerOptions{tree: *tree, id: *id, receipt: *receipt, completionReceipt: *completionReceipt, plan: *plan}, nil
}

// findRepoRoot 从当前目录向上寻找仓库根目录。
func findRepoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get runner working directory: %w", err)
	}
	for root := filepath.Clean(wd); ; root = filepath.Dir(root) {
		if info, statErr := os.Stat(filepath.Join(root, ".git")); statErr == nil && (info.IsDir() || info.Mode().IsRegular()) {
			return root, nil
		}
		parent := filepath.Dir(root)
		if parent == root {
			return "", fmt.Errorf("repository root not found from %q", wd)
		}
	}
}

func printPlan(document catalog.Catalog, workload catalog.Workload) error {
	plan := struct {
		ID                           string   `json:"id"`
		CatalogDigest                string   `json:"catalog_digest"`
		RunnerTarget                 string   `json:"runner_target"`
		Platform                     string   `json:"platform"`
		Timeout                      int      `json:"timeout_seconds"`
		ReceiptSchema                string   `json:"receipt_schema"`
		ProducerImplementationStatus string   `json:"producer_implementation_status"`
		Command                      []string `json:"command"`
	}{
		ID: workload.ID, CatalogDigest: document.CatalogDigest, RunnerTarget: workload.RunnerTarget,
		Platform: runtime.GOOS, Timeout: workload.TimeoutSeconds, ReceiptSchema: workload.ReceiptSchema,
		ProducerImplementationStatus: workload.ProducerImplementationStatus,
		Command:                      workload.Command,
	}
	encoded, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return fmt.Errorf("encode workload plan: %w", err)
	}
	_, err = fmt.Println(string(encoded))
	return err
}
