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
	"runtime"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/ctxutil"
	"github.com/lihah111222333-cloud/super-dolphin-agent/scripts/mcp_lsp_workload_catalog"
)

type runnerOptions struct {
	id      string
	receipt string
	plan    bool
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
	document, err := catalog.Load(repoRoot)
	if err != nil {
		return err
	}
	workload, err := document.Find(options.id)
	if err != nil {
		return err
	}
	if !workload.SupportsCurrentPlatform() {
		return fmt.Errorf("workload %q is not registered for platform %q", workload.ID, runtime.GOOS)
	}
	if workload.ImplementationStatus != "implemented" {
		return fmt.Errorf("workload %q is N/V: implementation_status=%s t6_blocking=%t release_blocking=%t", workload.ID, workload.ImplementationStatus, workload.T6Blocking, workload.ReleaseBlocking)
	}
	if options.plan {
		return printPlan(document, workload)
	}
	receiptPath, err := resolveReceiptPath(repoRoot, options.receipt, workload.ID)
	if err != nil {
		return err
	}
	return executeWorkload(repoRoot, document, workload, receiptPath)
}

func parseOptions(args []string) (runnerOptions, error) {
	fs := flag.NewFlagSet("run_mcp_lsp_workload", flag.ContinueOnError)
	id := fs.String("id", "", "catalog workload ID")
	receipt := fs.String("receipt", "", "absolute or repository-relative receipt path")
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
	return runnerOptions{id: *id, receipt: *receipt, plan: *plan}, nil
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

// resolveReceiptPath 解析显式、环境变量或默认的本地回执路径。
func resolveReceiptPath(repoRoot, requested, workloadID string) (string, error) {
	if envPath := strings.TrimSpace(os.Getenv("MCP_LSP_WORKLOAD_RECEIPT")); envPath != "" {
		if requested != "" && filepath.Clean(requested) != filepath.Clean(envPath) {
			return "", errors.New("--receipt and MCP_LSP_WORKLOAD_RECEIPT disagree")
		}
		requested = envPath
	}
	if requested == "" {
		requested = filepath.Join(".tmp", "mcp-lsp-workload-receipts", workloadID+".json")
	}
	if !filepath.IsAbs(requested) {
		requested = filepath.Join(repoRoot, requested)
	}
	return filepath.Clean(requested), nil
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

// executeWorkload 按目录命令执行 workload，并原子写入本地回执。
func executeWorkload(repoRoot string, document catalog.Catalog, workload catalog.Workload, receiptPath string) error {
	started := time.Now().UTC()
	timeout, err := catalog.TimeoutDuration(workload.TimeoutSeconds)
	if err != nil {
		return fmt.Errorf("workload %q timeout is invalid: %w", workload.ID, err)
	}
	ctx, cancel := ctxutil.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, workload.Command[0], workload.Command[1:]...)
	command.Dir = repoRoot
	command.Env = append(os.Environ(),
		"MCP_LSP_WORKLOAD_ID="+workload.ID,
		"MCP_LSP_WORKLOAD_CATALOG_DIGEST="+document.CatalogDigest,
	)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	runErr := command.Run()
	finished := time.Now().UTC()
	status, exitCode := workloadResult(ctx, runErr)
	receipt := catalog.Receipt{
		Schema: catalog.ReceiptSchema, WorkloadID: workload.ID, CatalogDigest: document.CatalogDigest,
		RunnerTarget: workload.RunnerTarget, ProducerWorkflowPath: workload.ProducerWorkflowPath,
		ProducerArtifactName: workload.ProducerArtifactName, ProducerImplementationStatus: workload.ProducerImplementationStatus,
		ExecutionOrigin: "local-runner", Platform: runtime.GOOS,
		TimeoutSeconds: workload.TimeoutSeconds, Command: append([]string(nil), workload.Command...),
		StartedAt: started.Format(time.RFC3339Nano), FinishedAt: finished.Format(time.RFC3339Nano),
		Status: status, ExitCode: exitCode,
	}
	if err := writeReceipt(receiptPath, receipt); err != nil {
		return err
	}
	if runErr != nil {
		return fmt.Errorf("workload %q %s: %w", workload.ID, status, runErr)
	}
	return nil
}

func workloadResult(ctx context.Context, runErr error) (string, int) {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timeout", -1
	}
	if runErr == nil {
		return "pass", 0
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](runErr); ok {
		return "failed", exitErr.ExitCode()
	}
	return "failed", -1
}

// writeReceipt 以受限权限原子发布统一 schema 的 workload 回执。
func writeReceipt(path string, receipt catalog.Receipt) error {
	if path == "" || !filepath.IsAbs(path) {
		return errors.New("workload receipt path must be absolute")
	}
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return fmt.Errorf("encode workload receipt: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create workload receipt directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".receipt-*.tmp")
	if err != nil {
		return fmt.Errorf("create workload receipt temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("secure workload receipt temporary file: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write workload receipt: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close workload receipt: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish workload receipt: %w", err)
	}
	return nil
}
