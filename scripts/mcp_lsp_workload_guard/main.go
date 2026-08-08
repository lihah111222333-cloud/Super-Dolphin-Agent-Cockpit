package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/scripts/mcp_lsp_workload_catalog"
)

var workloadIDPattern = regexp.MustCompile(`mcp-lsp-[a-z0-9-]+`)

var testSelectorNamePattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

const legacyResourceCohortCommand = "$(TEST_WITH_GUARD) --quick-guard -tags=e2e ./cmd/mcp-lsp -run '^TestMcpLSPBinary(LinkedWorktreesResourceCohortRecycleAndRecover|ResourceCohortMalformedReportQuarantine)_E2E$$' -v -timeout 240s -count=1"

type guardRequest struct {
	receipt string
	id      string
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run 执行目录校验并按需验证单个工作负载回执。
func run(args []string) error {
	request, err := parseGuardRequest(args)
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
	if err := validateCanonicalIDs(document); err != nil {
		return err
	}
	if err := validateCatalogTestSelectors(repoRoot, document); err != nil {
		return err
	}
	if err := validateConsumers(repoRoot, document); err != nil {
		return err
	}
	if err := validateRequestedReceipt(repoRoot, document, request); err != nil {
		return err
	}
	fmt.Printf("mcp-lsp workload catalog PASS schema=%s digest=%s workloads=%d\n", document.Schema, document.CatalogDigest, len(document.Workloads))
	return nil
}

// parseGuardRequest 解析守卫参数并要求回执选项成对出现。
func parseGuardRequest(args []string) (guardRequest, error) {
	fs := flag.NewFlagSet("check_mcp_lsp_workload_catalog", flag.ContinueOnError)
	receipt := fs.String("receipt", "", "optional absolute receipt path to verify")
	id := fs.String("id", "", "catalog workload ID for --receipt")
	if err := fs.Parse(args); err != nil {
		return guardRequest{}, err
	}
	if fs.NArg() != 0 {
		return guardRequest{}, fmt.Errorf("unexpected workload guard arguments: %s", strings.Join(fs.Args(), " "))
	}
	if (*receipt == "") != (*id == "") {
		return guardRequest{}, fmt.Errorf("--receipt and --id must be supplied together")
	}
	return guardRequest{receipt: *receipt, id: *id}, nil
}

// validateRequestedReceipt 校验命令行指定的回执路径和内容。
func validateRequestedReceipt(repoRoot string, document catalog.Catalog, request guardRequest) error {
	if request.receipt == "" {
		return nil
	}
	if !filepath.IsAbs(request.receipt) {
		return fmt.Errorf("--receipt must be absolute")
	}
	return catalog.ValidateReceiptAt(document, repoRoot, request.id, request.receipt)
}

func findRepoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get workload guard working directory: %w", err)
	}
	for root := filepath.Clean(wd); ; root = filepath.Dir(root) {
		if _, statErr := os.Stat(filepath.Join(root, ".git")); statErr == nil {
			return root, nil
		}
		parent := filepath.Dir(root)
		if parent == root {
			return "", fmt.Errorf("repository root not found from %q", wd)
		}
	}
}

func validateCanonicalIDs(document catalog.Catalog) error {
	canonicalIDs := []string{
		"mcp-lsp-idle-quick",
		"mcp-lsp-native-process-tree",
		"mcp-lsp-default-15m",
		"mcp-lsp-100-workspace-soak",
		"mcp-lsp-release-artifact",
	}
	if len(document.Workloads) != len(canonicalIDs) {
		return fmt.Errorf("catalog workload count=%d, want exactly %d canonical IDs", len(document.Workloads), len(canonicalIDs))
	}
	for index, want := range canonicalIDs {
		if document.Workloads[index].ID != want {
			return fmt.Errorf("catalog workload[%d]=%q, want %q", index, document.Workloads[index].ID, want)
		}
	}
	return nil
}

// validateCatalogTestSelectors 校验目录 -run 选择器中的测试符号真实存在。
func validateCatalogTestSelectors(repoRoot string, document catalog.Catalog) error {
	for _, workload := range document.Workloads {
		if err := validateWorkloadTestSelectors(repoRoot, workload); err != nil {
			return err
		}
	}
	return nil
}

// validateWorkloadTestSelectors 校验一个已实现工作负载的具名测试集合。
func validateWorkloadTestSelectors(repoRoot string, workload catalog.Workload) error {
	if workload.ImplementationStatus != "implemented" {
		return nil
	}
	packagePaths, selector, ok, err := extractTestSelector(workload.Command)
	if err != nil {
		return fmt.Errorf("workload %q: %w", workload.ID, err)
	}
	if !ok {
		return nil
	}
	names, err := namedTestSymbols(selector)
	if err != nil {
		return fmt.Errorf("workload %q: %w", workload.ID, err)
	}
	declared, err := collectDeclaredTests(repoRoot, packagePaths)
	if err != nil {
		return fmt.Errorf("workload %q: %w", workload.ID, err)
	}
	for _, name := range names {
		if _, found := declared[name]; !found {
			return fmt.Errorf("workload %q -run selector references missing test %q", workload.ID, name)
		}
	}
	return nil
}

// extractTestSelector 提取 go test 命令中的包路径和 -run 选择器。
func extractTestSelector(command []string) ([]string, string, bool, error) {
	if len(command) < 3 || command[0] != "go" || command[1] != "test" {
		return nil, "", false, nil
	}
	packageEnd := len(command)
	for index := 2; index < len(command); index++ {
		if !strings.HasPrefix(command[index], "-") {
			continue
		}
		packageEnd = index
		if command[index] != "-run" {
			continue
		}
		if index+1 >= len(command) {
			return nil, "", false, fmt.Errorf("-run selector is missing")
		}
		if index == 2 {
			return nil, "", false, fmt.Errorf("-run selector has no package path")
		}
		return command[2:packageEnd], command[index+1], true, nil
	}
	return nil, "", false, nil
}

// namedTestSymbols 将受限的目录选择器转换为完整 Test 函数名。
func namedTestSymbols(selector string) ([]string, error) {
	if !strings.HasPrefix(selector, "^Test(") || !strings.HasSuffix(selector, ")$") {
		return nil, fmt.Errorf("-run selector %q must use ^Test(... )$ form", selector)
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(selector, "^Test("), ")$")
	parts := strings.Split(inner, "|")
	names := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || !testSelectorNamePattern.MatchString(part) {
			return nil, fmt.Errorf("-run selector %q contains an unsafe test name", selector)
		}
		names = append(names, "Test"+part)
	}
	return names, nil
}

// collectDeclaredTests 收集目录命令涉及的所有包测试函数声明。
func collectDeclaredTests(repoRoot string, packagePaths []string) (map[string]string, error) {
	declared := make(map[string]string)
	for _, packagePath := range packagePaths {
		packageTests, err := collectPackageTests(repoRoot, packagePath)
		if err != nil {
			return nil, err
		}
		for name := range packageTests {
			declared[name] = packagePath
		}
	}
	return declared, nil
}

// collectPackageTests 解析一个包目录中的 _test.go 文件并收集 Test 函数。
func collectPackageTests(repoRoot, packagePath string) (map[string]bool, error) {
	directory, err := resolvePackageDirectory(repoRoot, packagePath)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("read test package %q: %w", packagePath, err)
	}
	declared := make(map[string]bool)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		names, parseErr := parseDeclaredTests(filepath.Join(directory, entry.Name()))
		if parseErr != nil {
			return nil, parseErr
		}
		for name := range names {
			declared[name] = true
		}
	}
	return declared, nil
}

// resolvePackageDirectory 将目录命令中的相对包路径解析到仓库内目录。
func resolvePackageDirectory(repoRoot, packagePath string) (string, error) {
	if !strings.HasPrefix(packagePath, "./") || strings.Contains(filepath.ToSlash(packagePath), "../") {
		return "", fmt.Errorf("test package path %q is not a repository-relative directory", packagePath)
	}
	directory := filepath.Join(repoRoot, filepath.FromSlash(strings.TrimPrefix(packagePath, "./")))
	info, err := os.Stat(directory)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("test package directory %q is unavailable", packagePath)
	}
	return directory, nil
}

// parseDeclaredTests 解析单个测试文件中的 Test 函数声明。
func parseDeclaredTests(path string) (map[string]bool, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse test file %q: %w", path, err)
	}
	declared := make(map[string]bool)
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name == nil || !strings.HasPrefix(function.Name.Name, "Test") {
			continue
		}
		declared[function.Name.Name] = true
	}
	return declared, nil
}

// validateConsumers 校验所有消费方只引用目录 ID，不复制命令真相。
func validateConsumers(repoRoot string, document catalog.Catalog) error {
	consumerPaths := []string{
		"Makefile",
		"cmd/super-dolphin-gate/test_cli.go",
		"scripts/ai_maintenance/main.go",
		"scripts/ai_maintenance/owned_gate_execution.go",
		"scripts/ai_maintenance/evidence.go",
		".githooks/README.md",
	}
	for _, relative := range consumerPaths {
		if err := validateConsumerFile(repoRoot, relative, document); err != nil {
			return err
		}
	}
	return requireQuickConsumerReferences(repoRoot, document.Workloads[0].ID)
}

// validateConsumerFile 校验单个消费方的目录 ID 和命令边界。
func validateConsumerFile(repoRoot, relative string, document catalog.Catalog) error {
	path := filepath.Join(repoRoot, filepath.FromSlash(relative))
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read catalog consumer %q: %w", relative, err)
	}
	text := string(raw)
	if hasCopiedMcpLSPCommand(relative, text) {
		return fmt.Errorf("catalog consumer %q copies mcp-lsp command truth", relative)
	}
	for _, id := range workloadIDPattern.FindAllString(text, -1) {
		if !containsID(document, id) && !isLegacyConsumerAlias(id) {
			return fmt.Errorf("catalog consumer %q references unknown workload ID %q", relative, id)
		}
	}
	return nil
}

func isLegacyConsumerAlias(id string) bool {
	switch id {
	case "mcp-lsp-resource-cohort", "mcp-lsp-workload-catalog-check":
		return true
	default:
		return false
	}
}

// hasCopiedMcpLSPCommand 仅允许既有 resource-cohort 独立门禁保留原命令。
func hasCopiedMcpLSPCommand(relative, text string) bool {
	if relative == "Makefile" {
		text = strings.ReplaceAll(text, legacyResourceCohortCommand, "")
	}
	return strings.Contains(text, "-tags=e2e ./cmd/mcp-lsp") || strings.Contains(text, "go test ./cmd/mcp-lsp")
}

// requireQuickConsumerReferences 检查快速门禁 ID 已连接到全部预期消费方。
func requireQuickConsumerReferences(repoRoot, quickID string) error {
	if err := requireConsumerID(repoRoot, "Makefile", quickID); err != nil {
		return err
	}
	for _, relative := range []string{"scripts/ai_maintenance/owned_gate_execution.go", "scripts/ai_maintenance/evidence.go"} {
		if err := requireConsumerID(repoRoot, relative, quickID); err != nil {
			return err
		}
	}
	if err := requireConsumerID(repoRoot, "cmd/super-dolphin-gate/test_cli.go", "mcp-lsp-default-15m"); err != nil {
		return err
	}
	return requireConsumerID(repoRoot, ".githooks/README.md", quickID)
}

func containsID(document catalog.Catalog, id string) bool {
	for _, workload := range document.Workloads {
		if workload.ID == id {
			return true
		}
	}
	return false
}

func requireConsumerID(repoRoot, relative, id string) error {
	raw, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(relative)))
	if err != nil {
		return fmt.Errorf("read catalog consumer %q: %w", relative, err)
	}
	if !strings.Contains(string(raw), id) {
		return fmt.Errorf("catalog consumer %q does not reference workload ID %q", relative, id)
	}
	return nil
}
