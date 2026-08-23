package mcplspcatalogguard

import (
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/scripts/mcp_lsp_workload_catalog"
)

type guardRequest struct {
	repoRoot    string
	tree        string
	receipt     string
	id          string
	receiptOnly bool
}

// Run 执行目录校验并按需验证单个工作负载回执。
func Run(args []string, stdout io.Writer) error {
	if stdout == nil {
		return errors.New("catalog guard stdout is required")
	}
	request, err := parseGuardRequest(args)
	if err != nil {
		return err
	}
	repoRoot := request.repoRoot
	tree, err := resolveGuardTree(repoRoot, request.tree)
	if err != nil {
		return err
	}
	document, err := catalog.LoadAt(repoRoot, tree)
	if err != nil {
		return err
	}
	if err := validateGuardRequest(repoRoot, tree, document, request); err != nil {
		return err
	}
	if request.receiptOnly {
		_, err := fmt.Fprintf(stdout, "mcp-lsp workload receipt PASS id=%s\n", request.id)
		return err
	}
	return writeCatalogPass(stdout, document)
}

// writeCatalogPass emits the bounded success line after all catalog checks pass.
func writeCatalogPass(stdout io.Writer, document catalog.Catalog) error {
	_, err := fmt.Fprintf(stdout, "mcp-lsp workload catalog PASS schema=%s digest=%s workloads=%d\n", document.Schema, document.CatalogDigest, len(document.Workloads))
	return err
}

// resolveGuardTree 只接受绝对仓库和完整 tree OID，并验证 Git 返回值没有漂移。
func resolveGuardTree(repoRoot, requested string) (string, error) {
	if !filepath.IsAbs(repoRoot) || strings.TrimSpace(requested) == "" {
		return "", errors.New("catalog guard requires absolute repository and explicit candidate tree")
	}
	output, err := catalog.BoundedGitOutput(repoRoot, "rev-parse", "--verify", "--end-of-options", requested+"^{tree}")
	if err != nil {
		return "", fmt.Errorf("resolve catalog guard candidate tree: %w", err)
	}
	tree := strings.TrimSpace(string(output))
	if tree == "" || tree != requested {
		return "", fmt.Errorf("candidate tree is not a resolved tree object: requested=%q resolved=%q", requested, tree)
	}
	return tree, nil
}

// parseGuardRequest 解析守卫参数并要求回执选项成对出现。
func parseGuardRequest(args []string) (guardRequest, error) {
	canonicalArgs, err := catalog.CanonicalizeAuthorityFlags(args, "repository", "tree", "receipt", "id", "receipt-only")
	if err != nil {
		return guardRequest{}, err
	}
	args = canonicalArgs
	if err := catalog.ValidateNoDuplicateAuthorityFlags(args, "repository", "tree", "receipt", "id", "receipt-only"); err != nil {
		return guardRequest{}, err
	}
	fs := flag.NewFlagSet("check_mcp_lsp_workload_catalog", flag.ContinueOnError)
	repoRoot := fs.String("repository", "", "absolute repository root")
	tree := fs.String("tree", "", "resolved candidate Git tree OID")
	receipt := fs.String("receipt", "", "optional absolute receipt path to verify")
	id := fs.String("id", "", "catalog workload ID for --receipt")
	receiptOnly := fs.Bool("receipt-only", false, "verify only the requested receipt after catalog.Load")
	if err := fs.Parse(args); err != nil {
		return guardRequest{}, err
	}
	request := guardRequest{repoRoot: *repoRoot, tree: *tree, receipt: *receipt, id: *id, receiptOnly: *receiptOnly}
	if err := validateParsedGuardRequest(fs, request); err != nil {
		return guardRequest{}, err
	}
	request.repoRoot = filepath.Clean(request.repoRoot)
	return request, nil
}

// validateParsedGuardRequest 校验 flag 组合，避免 parseGuardRequest 混入执行逻辑。
func validateParsedGuardRequest(flags *flag.FlagSet, request guardRequest) error {
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected workload guard arguments: %s", strings.Join(flags.Args(), " "))
	}
	if strings.TrimSpace(request.repoRoot) == "" || strings.TrimSpace(request.tree) == "" {
		return errors.New("--repository and --tree are required")
	}
	if !filepath.IsAbs(request.repoRoot) {
		return errors.New("--repository must be absolute")
	}
	if request.receiptOnly {
		return validateReceiptOnlyGuardRequest(request)
	}
	if (request.receipt == "") != (request.id == "") {
		return fmt.Errorf("--receipt and --id must be supplied together")
	}
	return nil
}

// validateReceiptOnlyGuardRequest 校验仅回执模式的绝对路径与身份配对。
func validateReceiptOnlyGuardRequest(request guardRequest) error {
	if request.receipt == "" || request.id == "" {
		return fmt.Errorf("--receipt-only requires --receipt and --id")
	}
	if !filepath.IsAbs(request.receipt) {
		return fmt.Errorf("--receipt-only requires an absolute --receipt")
	}
	return nil
}

// validateGuardRequest 让 receipt-only 保持失败即阻断，同时避免重复执行静态目录校验。
func validateGuardRequest(repoRoot, tree string, document catalog.Catalog, request guardRequest) error {
	if request.receiptOnly {
		if request.receipt == "" || request.id == "" {
			return fmt.Errorf("--receipt-only requires --receipt and --id")
		}
		return validateRequestedReceipt(repoRoot, tree, document, request)
	}
	if err := validateCatalogTestSelectors(repoRoot, tree, document); err != nil {
		return err
	}
	if err := validateConsumers(repoRoot, tree, document); err != nil {
		return err
	}
	return validateRequestedReceipt(repoRoot, tree, document, request)
}

// validateRequestedReceipt 校验命令行指定的回执路径、候选 tree 和内容。
func validateRequestedReceipt(repoRoot, tree string, document catalog.Catalog, request guardRequest) error {
	if request.receipt == "" {
		return nil
	}
	if !filepath.IsAbs(request.receipt) {
		return fmt.Errorf("--receipt must be absolute")
	}
	return catalog.ValidateReceiptAtCandidate(document, repoRoot, tree, request.id, request.receipt)
}

// validateCatalogTestSelectors 校验目录 -run 选择器中的测试符号真实存在。
func validateCatalogTestSelectors(repoRoot, tree string, document catalog.Catalog) error {
	for _, workload := range document.Workloads {
		if err := validateWorkloadTestSelectors(repoRoot, tree, workload); err != nil {
			return err
		}
	}
	return nil
}

// validateWorkloadTestSelectors 校验一个已实现工作负载的具名测试集合。
func validateWorkloadTestSelectors(repoRoot, tree string, workload catalog.Workload) error {
	if workload.ImplementationStatus != "implemented" {
		return nil
	}
	command, err := catalog.ParseRemoteTestCommand(workload.Command)
	if err != nil {
		return fmt.Errorf("workload %q: %w", workload.ID, err)
	}
	packages, err := catalog.LoadRemoteTestPackageFilesAt(repoRoot, tree, command)
	if err != nil {
		return fmt.Errorf("workload %q: %w", workload.ID, err)
	}
	if _, err := catalog.ProjectRemoteTestCommand(command, catalog.DefaultRemoteTestEnvironment(), packages); err != nil {
		return fmt.Errorf("workload %q platform %s/%s: %w", workload.ID, catalog.RemoteTestGOOS, catalog.RemoteTestGOARCH, err)
	}
	return nil
}

// validateConsumers 从 exact tree 读取真实 runner/consumer，并以 catalog ID 集合做双向未知引用检查。
func validateConsumers(repoRoot, tree string, document catalog.Catalog) error {
	knownIDs, err := catalogWorkloadIDs(document)
	if err != nil {
		return err
	}
	registered := catalog.WorkloadConsumerPaths()
	if err := validateConsumerRegistryClosure(repoRoot, tree, registered, knownIDs); err != nil {
		return err
	}
	return validateRegisteredConsumers(repoRoot, tree, registered, knownIDs)
}

func catalogWorkloadIDs(document catalog.Catalog) (map[string]struct{}, error) {
	knownIDs := make(map[string]struct{}, len(document.Workloads))
	for _, workload := range document.Workloads {
		if workload.ID == "" {
			return nil, errors.New("catalog contains an empty workload ID")
		}
		if _, duplicate := knownIDs[workload.ID]; duplicate {
			return nil, fmt.Errorf("catalog repeats workload ID %q", workload.ID)
		}
		knownIDs[workload.ID] = struct{}{}
	}
	return knownIDs, nil
}

// validateRegisteredConsumers 从 exact tree 校验 registry 中每个 consumer 的调用与 ID。
func validateRegisteredConsumers(repoRoot, tree string, registered []string, knownIDs map[string]struct{}) error {
	for _, relative := range registered {
		raw, err := catalog.ReadTreeFileAt(repoRoot, tree, relative)
		if err != nil {
			return fmt.Errorf("read catalog consumer %q from exact tree: %w", relative, err)
		}
		actual, err := liveConsumerReferences(relative, raw)
		if err != nil {
			return err
		}
		for id := range actual {
			if _, known := knownIDs[id]; !known {
				return fmt.Errorf("catalog consumer %q references unknown workload ID %q", relative, id)
			}
		}
		if err := validateConsumerExecution(relative, raw); err != nil {
			return err
		}
	}
	return nil
}

// validateConsumerRegistryClosure 从 candidate tree 发现 consumer 并与显式 registry 双向比对。
func validateConsumerRegistryClosure(repoRoot, tree string, registered []string, knownIDs map[string]struct{}) error {
	dynamic, err := discoverCatalogConsumers(repoRoot, tree, knownIDs)
	if err != nil {
		return err
	}
	return compareConsumerRegistry(dynamic, registered)
}

// discoverCatalogConsumers 从 exact tree 的可解析生产源码动态发现 consumer。
func discoverCatalogConsumers(repoRoot, tree string, knownIDs map[string]struct{}) (map[string]struct{}, error) {
	paths, err := catalog.ListTreePathsAt(repoRoot, tree)
	if err != nil {
		return nil, fmt.Errorf("discover catalog consumers from exact tree: %w", err)
	}
	dynamic := make(map[string]struct{})
	for _, relative := range paths {
		if !catalog.IsWorkloadConsumerCandidatePath(relative) {
			continue
		}
		raw, err := catalog.ReadTreeFileAt(repoRoot, tree, relative)
		if err != nil {
			return nil, fmt.Errorf("read catalog consumer candidate %q from exact tree: %w", relative, err)
		}
		candidate, err := catalog.IsWorkloadConsumerSource(relative, raw, knownIDs)
		if err != nil {
			return nil, fmt.Errorf("parse catalog consumer candidate %q: %w", relative, err)
		}
		if candidate {
			dynamic[relative] = struct{}{}
		}
	}
	return dynamic, nil
}

// compareConsumerRegistry 双向拒绝 registry 的遗漏、重复与陈旧路径。
func compareConsumerRegistry(dynamic map[string]struct{}, registered []string) error {
	registeredSet := make(map[string]struct{}, len(registered))
	for _, relative := range registered {
		if _, duplicate := registeredSet[relative]; duplicate {
			return fmt.Errorf("catalog consumer registry repeats %q", relative)
		}
		registeredSet[relative] = struct{}{}
		if _, discovered := dynamic[relative]; !discovered {
			return fmt.Errorf("catalog consumer registry contains stale or undiscovered path %q", relative)
		}
	}
	for relative := range dynamic {
		if _, registered := registeredSet[relative]; !registered {
			return fmt.Errorf("catalog consumer registry omitted discovered path %q", relative)
		}
	}
	return nil
}

// validateConsumerExecution 校验每类 consumer 的真实 invocation 结构。
func validateConsumerExecution(relative string, raw []byte) error {
	if relative == "Makefile" {
		return validateMakefileConsumerExecution(relative, string(raw))
	}
	if relative == "scripts/run_mcp_lsp_workload.sh" {
		return validateRunnerShellExecution(relative, string(raw))
	}
	if relative == "scripts/check_mcp_lsp_workload_catalog.sh" {
		return validateGuardShellExecution(relative, string(raw))
	}
	file, err := parser.ParseFile(token.NewFileSet(), relative, raw, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return fmt.Errorf("parse catalog consumer %q: %w", relative, err)
	}
	return validateGoConsumerExecution(relative, file)
}

// validateGoConsumerExecution 检查 Go consumer 的真实 import/call 结构。
func validateGoConsumerExecution(relative string, file *ast.File) error {
	switch relative {
	case "scripts/ai_maintenance/owned_gate_execution.go":
		return validateOwnedGateConsumer(relative, file)
	case "scripts/ai_maintenance/main.go":
		return validateSchedulerPolicyConsumer(relative, file)
	case "scripts/ai_maintenance/release_gate_rules.go":
		return validateSchedulerRouteConsumer(relative, file)
	case "scripts/ai_maintenance/gate_execution.go":
		return validateSchedulerExecutionConsumer(relative, file)
	case "scripts/mcp_lsp_workload_runner/main.go":
		return validateRunnerCatalogConsumer(relative, file)
	case "scripts/mcp_lsp_workload_runner/platform_windows.go", "scripts/mcp_lsp_workload_runner/platform_nonwindows.go":
		return validateRunnerPlatformConsumer(relative, file)
	default:
		return validateCompiledGuardInfrastructure(relative, file)
	}
}

func validateCompiledGuardInfrastructure(relative string, file *ast.File) error {
	switch relative {
	case "cmd/super-dolphin-gate/main.go":
		if !hasLiveGuardWorkerBinding(file) {
			return fmt.Errorf("catalog consumer %q does not execute the compiled guard owner", relative)
		}
		return nil
	default:
		return nil
	}
}

// validateRunnerCatalogConsumer 校验 runner 的 live catalog/tree/dataflow 入口。
func validateRunnerCatalogConsumer(relative string, file *ast.File) error {
	if !hasLiveRunnerCatalogFlow(file) || !hasLiveLocalHostExecutionFlow(file) {
		return fmt.Errorf("catalog consumer %q does not resolve workload IDs through catalog.LoadAt/Find with candidate tree", relative)
	}
	return nil
}

// validateRunnerPlatformConsumer 校验平台适配器只能返回 canonical workload 深拷贝。
func validateRunnerPlatformConsumer(relative string, file *ast.File) error {
	if relative == "scripts/mcp_lsp_workload_runner/platform_windows.go" && hasCanonicalWindowsRunnerPlatformAdapter(file) {
		return nil
	}
	if !hasCanonicalRunnerPlatformAdapter(file) {
		return fmt.Errorf("catalog consumer %q changes or obscures the canonical runner workload", relative)
	}
	return nil
}

// hasCanonicalWindowsRunnerPlatformAdapter accepts the explicit Windows host
// projection while keeping the source workload immutable and clone-bound.
func hasCanonicalWindowsRunnerPlatformAdapter(file *ast.File) bool {
	function := namedConsumerFunction(file, "runnerPlatformWorkload")
	if function == nil || function.Body == nil || hasConditionalSuccessBypass(function) {
		return false
	}
	cloned, switched, returned := false, false, false
	validCase := map[string]bool{"mcp-lsp-idle-quick": false, "mcp-lsp-default-15m": false}
	ast.Inspect(function, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.AssignStmt:
			if assignmentBindsName(value, "view") && assignmentCalls(value, "cloneRunnerWorkload", func(call *ast.CallExpr) bool {
				return hasExactIdentifierArguments(call, "workload")
			}) {
				cloned = true
			}
			if len(value.Lhs) == 1 {
				if selector, ok := value.Lhs[0].(*ast.SelectorExpr); ok && isIdentifier(selector.X, "workload") {
					return false
				}
			}
		case *ast.SwitchStmt:
			selector, ok := value.Tag.(*ast.SelectorExpr)
			if !ok || !isIdentifier(selector.X, "view") || selector.Sel.Name != "ID" {
				return true
			}
			switched = true
			for _, statement := range value.Body.List {
				clause, ok := statement.(*ast.CaseClause)
				if !ok || len(clause.List) != 1 {
					continue
				}
				literal, ok := clause.List[0].(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					continue
				}
				caseName, err := strconv.Unquote(literal.Value)
				if err != nil {
					continue
				}
				if caseName == "mcp-lsp-idle-quick" && containsIdentifier(clause, "windowsIdleQuickHostCommand") {
					validCase[caseName] = true
				}
				if caseName == "mcp-lsp-default-15m" && containsIdentifier(clause, "windowsDefault15mSourceCommand") {
					validCase[caseName] = true
				}
			}
		case *ast.ReturnStmt:
			if len(value.Results) == 1 && isIdentifier(value.Results[0], "view") {
				returned = true
			}
		}
		return true
	})
	return cloned && switched && returned && validCase["mcp-lsp-idle-quick"] && validCase["mcp-lsp-default-15m"]
}

func containsIdentifier(node ast.Node, name string) bool {
	found := false
	ast.Inspect(node, func(current ast.Node) bool {
		identifier, ok := current.(*ast.Ident)
		if ok && identifier.Name == name {
			found = true
			return false
		}
		return !found
	})
	return found
}

func hasCanonicalRunnerPlatformAdapter(file *ast.File) bool {
	function := namedConsumerFunction(file, "runnerPlatformWorkload")
	if function == nil || function.Body == nil || len(function.Body.List) != 1 {
		return false
	}
	result, ok := function.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(result.Results) != 1 {
		return false
	}
	call, ok := result.Results[0].(*ast.CallExpr)
	return ok && callName(call.Fun) == "cloneRunnerWorkload" && hasExactIdentifierArguments(call, "workload")
}

func validateOwnedGateConsumer(relative string, file *ast.File) error {
	if hasCopiedGoTestInvocation(file) {
		return fmt.Errorf("catalog consumer %q copies mcp-lsp command truth", relative)
	}
	if !hasLiveCatalogGuardBuildFlow(file) || !hasLiveReleaseAuthorityFlow(file) {
		return fmt.Errorf("catalog consumer %q does not bind live release authority flow to candidate tree", relative)
	}
	references := ownedGateWorkloadReferences(file)
	if len(references) != 1 || references["mcp-lsp-idle-quick"] != 1 {
		return fmt.Errorf("catalog consumer %q changes the idle-quick workload role binding", relative)
	}
	return nil
}

// hasCopiedGoTestInvocation 在 AST 参数中检测真实 runCommand go test 调用。
