package mcplspcatalogguard

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"strings"
)

func hasCopiedGoTestInvocation(file *ast.File) bool {
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || callName(call.Fun) != "runCommand" {
			return true
		}
		arguments := make([]string, 0, len(call.Args))
		for _, argument := range call.Args {
			literal, ok := argument.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				continue
			}
			value, err := strconv.Unquote(literal.Value)
			if err == nil {
				arguments = append(arguments, value)
			}
		}
		if hasField(arguments, "go") && hasField(arguments, "test") && hasFieldPrefix(arguments, "./cmd/mcp-lsp") {
			found = true
		}
		return !found
	})
	return found
}

// validateMakefileConsumerExecution 解析 recipe invocation，拒绝复制 go test 命令。
func validateMakefileConsumerExecution(relative, text string) error {
	if !hasExactCatalogGuardTarget(text) {
		return fmt.Errorf("catalog consumer %q does not define the exact catalog guard target", relative)
	}
	for line := range strings.Lines(text) {
		if !strings.HasPrefix(line, "\t") {
			continue
		}
		fields := strings.Fields(line)
		if err := validateCatalogMakeRecipe(fields, strings.TrimSpace(line)); err != nil {
			return fmt.Errorf("catalog consumer %q: %w", relative, err)
		}
		if hasField(fields, "go") && hasField(fields, "test") && hasFieldPrefix(fields, "./cmd/mcp-lsp") {
			return fmt.Errorf("catalog consumer %q copies mcp-lsp command truth", relative)
		}
		if recipeHasID(fields) && !hasField(fields, "./scripts/run_mcp_lsp_workload.sh") {
			return fmt.Errorf("catalog consumer %q references workload ID without catalog runner", relative)
		}
	}
	return nil
}

func hasExactCatalogGuardTarget(text string) bool {
	lines := strings.Split(text, "\n")
	for index, line := range lines {
		if strings.TrimSpace(line) != "mcp-lsp-workload-catalog-check:" {
			continue
		}
		var recipes []string
		for next := index + 1; next < len(lines); next++ {
			candidate := lines[next]
			if candidate != "" && !strings.HasPrefix(candidate, "\t") {
				break
			}
			if strings.HasPrefix(candidate, "\t") {
				recipes = append(recipes, strings.TrimSpace(candidate))
			}
		}
		return slices.Equal(recipes, []string{
			`@test -n "$(MCP_LSP_CANDIDATE_TREE)" || (echo "MCP_LSP_CANDIDATE_TREE is required" >&2; exit 1)`,
			`@test -n "$(SUPER_DOLPHIN_GATE_SELF)" || (echo "SUPER_DOLPHIN_GATE_SELF is required" >&2; exit 1)`,
			`SUPER_DOLPHIN_GATE_SELF="$(SUPER_DOLPHIN_GATE_SELF)" ./scripts/check_mcp_lsp_workload_catalog.sh --tree "$(MCP_LSP_CANDIDATE_TREE)"`,
			`@echo "LOCAL_NON_AUTHORITATIVE NOT_VERIFIED: direct Make target cannot satisfy release authority" >&2; exit 1`,
		})
	}
	return false
}

func validateCatalogMakeRecipe(fields []string, line string) error {
	if strings.HasSuffix(line, `\`) && (hasField(fields, "./scripts/run_mcp_lsp_workload.sh") || hasField(fields, "./scripts/check_mcp_lsp_workload_catalog.sh")) {
		return errors.New("catalog recipe invocation cannot use continuation indirection")
	}
	if hasField(fields, "./scripts/run_mcp_lsp_workload.sh") && !isExactCatalogRunnerRecipe(fields) {
		return errors.New("catalog runner recipe must use the exact tree and ID argv")
	}
	if hasField(fields, "./scripts/check_mcp_lsp_workload_catalog.sh") && !isExactCatalogGuardRecipe(fields) {
		return errors.New("catalog guard recipe must use the exact tree argv")
	}
	return nil
}

func isExactCatalogRunnerRecipe(fields []string) bool {
	return len(fields) == 5 && fields[0] == "./scripts/run_mcp_lsp_workload.sh" && fields[1] == "--tree" &&
		fields[2] == `"$(MCP_LSP_CANDIDATE_TREE)"` && fields[3] == "--id" && strings.HasPrefix(fields[4], "mcp-lsp-")
}

func isExactCatalogGuardRecipe(fields []string) bool {
	return slices.Equal(fields, []string{`SUPER_DOLPHIN_GATE_SELF="$(SUPER_DOLPHIN_GATE_SELF)"`, "./scripts/check_mcp_lsp_workload_catalog.sh", "--tree", `"$(MCP_LSP_CANDIDATE_TREE)"`})
}

// validateRunnerShellExecution 解析 shell 的 tree 绑定与通用 runner invocation。
func validateRunnerShellExecution(relative, text string) error {
	lines, err := supportedShellWrapperLines(text)
	if err != nil {
		return fmt.Errorf("catalog consumer %q: %w", relative, err)
	}
	for _, line := range lines {
		if !isCanonicalRunnerShellStatement(line.code) {
			return fmt.Errorf("catalog consumer %q: runner wrapper contains unregistered live statement %q", relative, line.code)
		}
	}
	observation := collectRunnerShellObservation(lines)
	if !observation.treeFlag || !observation.treeValue || !observation.forwardedTree {
		return fmt.Errorf("catalog consumer %q does not parse and forward explicit candidate tree", relative)
	}
	if !observation.runnerInvocation {
		return fmt.Errorf("catalog consumer %q does not execute the generic catalog runner", relative)
	}
	return nil
}

func isCanonicalRunnerShellStatement(code string) bool {
	switch code {
	case "set -euo pipefail", `root_dir="$(cd -- "${BASH_SOURCE[0]%/*}/.." && pwd)"`, `cd "$root_dir"`, `tree=""`, "runner_args=()",
		"while (($# > 0)); do", `case "$1" in`, "--)", `runner_args+=("$@")`, "set --", "break", ";;",
		"--tree|-tree)", `if (($# < 2)) || [[ -n "$tree" ]] || [[ -z "${2//[[:space:]]/}" ]]; then`,
		`echo "--tree requires exactly one candidate tree" >&2`, "exit 1", "fi", `tree="$2"`, `runner_args+=("--tree" "$2")`, "shift 2",
		"--tree=*|-tree=*)", `if [[ -n "$tree" ]] || [[ -z "${1#*=}" ]]; then`, `tree="${1#*=}"`, `runner_args+=("--tree" "$tree")`, "shift",
		"--repository|-repository|--repository=*|-repository=*)", `echo "--repository is wrapper-owned and cannot be overridden" >&2`,
		"*)", `runner_args+=("$1")`, "esac", "done", `if [[ -z "$tree" ]]; then`,
		`echo "--tree is required; runner source must be candidate-bound" >&2`, `exec go run ./scripts/mcp_lsp_workload_runner "${runner_args[@]}"`:
		return true
	default:
		return false
	}
}

// validateGuardShellExecution 要求 wrapper 直接执行 canonical guard，并把 wrapper-owned
// repository 与已解析 tree 作为相邻参数传入。
func validateGuardShellExecution(relative, text string) error {
	lines, err := supportedShellWrapperLines(text)
	if err != nil {
		return fmt.Errorf("catalog consumer %q: %w", relative, err)
	}
	if err := validateExactGuardShellStatements(lines); err != nil {
		return fmt.Errorf("catalog consumer %q: %w", relative, err)
	}
	observation := collectGuardShellObservation(lines)
	if !observation.treeFlag || !observation.treeValue || !observation.repositoryRejected || !observation.trustedSelf || !observation.invocation {
		return fmt.Errorf("catalog consumer %q does not execute guard with wrapper-owned repository and explicit candidate tree", relative)
	}
	return nil
}

func validateExactGuardShellStatements(lines []shellSourceLine) error {
	for _, line := range lines {
		if !isCanonicalGuardShellStatement(line.code) {
			return fmt.Errorf("guard wrapper contains unregistered live statement %q", line.code)
		}
	}
	return nil
}

func isCanonicalGuardShellStatement(code string) bool {
	switch code {
	case "set -euo pipefail",
		`root_dir="$(cd -- "${BASH_SOURCE[0]%/*}/.." && pwd)"`, `cd "$root_dir"`, `tree=""`, "guard_args=()",
		"while (($# > 0)); do", `case "$1" in`, "--)", `guard_args+=("$@")`, "set --", "break", ";;",
		"--tree|-tree)", `if (($# < 2)) || [[ -n "$tree" ]] || [[ -z "${2//[[:space:]]/}" ]]; then`,
		`echo "--tree requires exactly one candidate tree" >&2`, "exit 1", "fi", `tree="$2"`, "shift 2",
		"--tree=*|-tree=*)", `if [[ -n "$tree" ]] || [[ -z "${1#*=}" ]]; then`, `tree="${1#*=}"`, "shift",
		"--repository|-repository|--repository=*|-repository=*)", `echo "--repository is wrapper-owned and cannot be overridden" >&2`,
		"*)", `guard_args+=("$1")`, "esac", "done", `if [[ -z "$tree" ]]; then`,
		`echo "--tree is required; guard source must be candidate-bound" >&2`, `gate_self="${SUPER_DOLPHIN_GATE_SELF:-}"`,
		`if [[ -z "$gate_self" || "$gate_self" != /* || ! -f "$gate_self" || ! -x "$gate_self" || -L "$gate_self" ]]; then`,
		`echo "SUPER_DOLPHIN_GATE_SELF must name a trusted absolute executable" >&2`,
		`echo "LOCAL_NON_AUTHORITATIVE NOT_VERIFIED: direct wrapper execution is diagnostic only" >&2`,
		`exec "$gate_self" worker mcp-lsp-catalog-guard --repository "$root_dir" --tree "$tree" ${guard_args[@]+"${guard_args[@]}"}`:
		return true
	default:
		return false
	}
}

type guardShellObservation struct {
	treeFlag, treeValue, repositoryRejected, trustedSelf, invocation bool
}

func collectGuardShellObservation(lines []shellSourceLine) guardShellObservation {
	var result guardShellObservation
	for _, line := range lines {
		observation := inspectGuardShellCode(line)
		result.treeFlag = result.treeFlag || observation.treeFlag
		result.treeValue = result.treeValue || observation.treeValue
		result.repositoryRejected = result.repositoryRejected || observation.repositoryRejected
		result.trustedSelf = result.trustedSelf || observation.trustedSelf
		result.invocation = result.invocation || observation.invocation
	}
	return result
}

func inspectGuardShellCode(line shellSourceLine) guardShellObservation {
	return guardShellObservation{
		treeFlag:           line.code == "--tree|-tree)" || line.code == "--tree=*|-tree=*)",
		treeValue:          line.code == `tree="$2"` || line.code == `tree="${1#*=}"`,
		repositoryRejected: line.code == "--repository|-repository|--repository=*|-repository=*)",
		trustedSelf:        line.code == `gate_self="${SUPER_DOLPHIN_GATE_SELF:-}"`,
		invocation:         line.topLevel && isDirectGuardExec(line.fields),
	}
}

func isDirectGuardExec(fields []string) bool {
	want := []string{"exec", `"$gate_self"`, "worker", "mcp-lsp-catalog-guard", "--repository", `"$root_dir"`, "--tree", `"$tree"`, `${guard_args[@]+"${guard_args[@]}"}`}
	return slices.Equal(fields, want)
}

type runnerShellObservation struct {
	treeFlag         bool
	treeValue        bool
	forwardedTree    bool
	runnerInvocation bool
}

func collectRunnerShellObservation(lines []shellSourceLine) runnerShellObservation {
	var result runnerShellObservation
	for _, line := range lines {
		observation := inspectRunnerShellCode(line)
		result.treeFlag = result.treeFlag || observation.treeFlag
		result.treeValue = result.treeValue || observation.treeValue
		result.forwardedTree = result.forwardedTree || observation.forwardedTree
		result.runnerInvocation = result.runnerInvocation || observation.runnerInvocation
	}
	return result
}

// inspectRunnerShellLine 从一条真实 shell 命令提取 candidate tree 与 runner invocation 事实。
func inspectRunnerShellCode(line shellSourceLine) runnerShellObservation {
	return runnerShellObservation{
		treeFlag:         line.code == "--tree|-tree)" || line.code == "--tree=*|-tree=*)",
		treeValue:        line.code == `tree="$2"` || line.code == `tree="${1#*=}"`,
		forwardedTree:    line.code == `runner_args+=("--tree" "$2")` || line.code == `runner_args+=("--tree" "$tree")`,
		runnerInvocation: line.topLevel && isDirectRunnerExec(line.fields),
	}
}

// isDirectRunnerExec 校验通用 runner 的精确 exec argv。
func isDirectRunnerExec(fields []string) bool {
	return len(fields) == 5 && fields[0] == "exec" && fields[1] == "go" && fields[2] == "run" &&
		fields[3] == "./scripts/mcp_lsp_workload_runner" && fields[4] == `"${runner_args[@]}"`
}

type shellSourceLine struct {
	code     string
	fields   []string
	topLevel bool
}

// supportedShellWrapperLines 只接受当前 wrapper 使用的顶层 shell grammar，
// 并要求唯一 exec 是最后一条可执行语句。无法证明活性的复合结构全部拒绝。
func supportedShellWrapperLines(text string) ([]shellSourceLine, error) {
	lines, err := parseSupportedShellLines(text)
	if err != nil {
		return nil, err
	}
	if err := validateFinalShellExec(lines); err != nil {
		return nil, err
	}
	return lines, nil
}

func parseSupportedShellLines(text string) ([]shellSourceLine, error) {
	lines := make([]shellSourceLine, 0)
	controlDepth := 0
	for raw := range strings.Lines(text) {
		line, nextDepth, include, err := parseSupportedShellLine(raw, controlDepth)
		if err != nil {
			return nil, err
		}
		controlDepth = nextDepth
		if !include {
			continue
		}
		lines = append(lines, line)
	}
	if controlDepth != 0 {
		return nil, errors.New("shell wrapper has unbalanced control flow")
	}
	return lines, nil
}

func parseSupportedShellLine(raw string, controlDepth int) (shellSourceLine, int, bool, error) {
	code, fields := shellCommandLine(raw)
	if code == "" {
		return shellSourceLine{}, controlDepth, false, nil
	}
	if err := validateSupportedShellCode(code, fields, controlDepth); err != nil {
		return shellSourceLine{}, controlDepth, false, err
	}
	if closesShellControl(code) && controlDepth > 0 {
		controlDepth--
	}
	line := shellSourceLine{code: code, fields: fields, topLevel: controlDepth == 0}
	if opensShellControl(code) {
		controlDepth++
	}
	return line, controlDepth, true, nil
}

func validateFinalShellExec(lines []shellSourceLine) error {
	execIndex := -1
	for index, line := range lines {
		if len(line.fields) == 0 || line.fields[0] != "exec" {
			continue
		}
		if execIndex >= 0 {
			return errors.New("shell wrapper must contain exactly one exec")
		}
		execIndex = index
	}
	if execIndex < 0 || execIndex != len(lines)-1 || !lines[execIndex].topLevel {
		return errors.New("shell wrapper exec must be the unique final top-level command")
	}
	return nil
}

func validateSupportedShellCode(code string, fields []string, controlDepth int) error {
	if hasNonLiveShellSyntax(code) {
		return errors.New("shell wrapper contains unsupported or non-live compound syntax")
	}
	if hasUnsupportedShellBlockSyntax(code) {
		return errors.New("shell wrapper contains unsupported compound syntax")
	}
	if len(fields) > 0 && fields[0] == "return" {
		return errors.New("shell wrapper cannot return before its final exec")
	}
	if controlDepth == 0 && len(fields) > 0 && fields[0] == "exit" {
		return errors.New("shell wrapper cannot exit before its final exec")
	}
	return nil
}

func hasNonLiveShellSyntax(code string) bool {
	return code == "if false; then" || strings.Contains(code, "<<") || isShellFunctionDefinition(code)
}

func hasUnsupportedShellBlockSyntax(code string) bool {
	if strings.HasPrefix(code, "function ") || strings.HasPrefix(code, "(") {
		return true
	}
	return code == "{" || code == "}"
}

func isShellFunctionDefinition(code string) bool {
	marker := strings.Index(code, "()")
	return marker > 0 && strings.TrimSpace(code[marker+2:]) == "{"
}

func opensShellControl(code string) bool {
	return strings.HasPrefix(code, "if ") && strings.HasSuffix(code, "; then") ||
		strings.HasPrefix(code, "case ") && strings.HasSuffix(code, " in") ||
		(strings.HasPrefix(code, "for ") || strings.HasPrefix(code, "while ") || strings.HasPrefix(code, "until ")) && strings.HasSuffix(code, "; do")
}

func closesShellControl(code string) bool {
	return code == "fi" || code == "esac" || code == "done"
}

// shellCommandLine 去除注释与空白，只把可执行 shell 行交给 invocation 检查。
func shellCommandLine(line string) (string, []string) {
	code := strings.TrimSpace(line)
	if code == "" || strings.HasPrefix(code, "#") {
		return "", nil
	}
	if comment := strings.Index(code, " #"); comment >= 0 {
		code = strings.TrimSpace(code[:comment])
	}
	return code, strings.Fields(code)
}

// liveConsumerReferences 只从已识别的调用/参数 AST 位置提取 workload ID。
func liveConsumerReferences(relative string, raw []byte) (map[string]int, error) {
	if relative == "Makefile" || relative == "scripts/run_mcp_lsp_workload.sh" || relative == "scripts/check_mcp_lsp_workload_catalog.sh" {
		return makeRecipeWorkloadReferences(raw), nil
	}
	file, err := parser.ParseFile(token.NewFileSet(), relative, raw, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse catalog consumer %q: %w", relative, err)
	}
	switch relative {
	case "scripts/ai_maintenance/owned_gate_execution.go":
		return ownedGateWorkloadReferences(file), nil
	case "scripts/mcp_lsp_workload_runner/platform_windows.go":
		return platformWorkloadReferences(file), nil
	case "cmd/super-dolphin-gate/test_cli.go":
		return workloadIDConstantReferences(file), nil
	default:
		return map[string]int{}, nil
	}
}

// ownedGateWorkloadReferences 只解析 live release-authority lookup 的 workload ID。
func ownedGateWorkloadReferences(file *ast.File) map[string]int {
	references := make(map[string]int)
	function := namedConsumerFunction(file, "runMcpLSPQuickReleaseAuthority")
	if function == nil {
		return references
	}
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || callName(call.Fun) != "Find" {
			return true
		}
		addWorkloadIDCallArguments(references, call.Args)
		return true
	})
	return references
}

func addWorkloadIDCallArguments(references map[string]int, arguments []ast.Expr) {
	for _, argument := range arguments {
		addWorkloadIDLiteral(references, argument)
	}
}

func platformWorkloadReferences(file *ast.File) map[string]int {
	references := make(map[string]int)
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		addWorkloadIDLiteral(references, literal)
		return true
	})
	return references
}

// workloadIDConstantReferences 读取 test_cli 的显式 workload 常量绑定。
func workloadIDConstantReferences(file *ast.File) map[string]int {
	references := make(map[string]int)
	for _, declaration := range file.Decls {
		gen, ok := declaration.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for index, name := range value.Names {
				if !strings.HasSuffix(name.Name, "WorkloadID") || index >= len(value.Values) {
					continue
				}
				addWorkloadIDLiteral(references, value.Values[index])
			}
		}
	}
	return references
}

func addWorkloadIDLiteral(references map[string]int, expression ast.Expr) {
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return
	}
	value, err := strconv.Unquote(literal.Value)
	if err == nil && isWorkloadID(value) {
		references[value]++
	}
}

// isWorkloadID 校验 catalog workload ID 的结构，不扫描任意文本。
func isWorkloadID(value string) bool {
	suffix, ok := strings.CutPrefix(value, "mcp-lsp-")
	if !ok || suffix == "" {
		return false
	}
	for _, char := range suffix {
		if char != '-' && (char < 'a' || char > 'z') && (char < '0' || char > '9') {
			return false
		}
	}
	return true
}

// makeRecipeWorkloadReferences 解析 Make recipe 的 --id 参数。
func makeRecipeWorkloadReferences(raw []byte) map[string]int {
	references := make(map[string]int)
	for line := range strings.Lines(string(raw)) {
		if !strings.HasPrefix(line, "\t") {
			continue
		}
		addRecipeWorkloadReferences(references, strings.Fields(line))
	}
	return references
}

// addRecipeWorkloadReferences 解析单条 Make recipe 的 --id 参数。
func addRecipeWorkloadReferences(references map[string]int, fields []string) {
	for index, field := range fields {
		if field == "--id" && index+1 < len(fields) && isWorkloadID(fields[index+1]) {
			references[fields[index+1]]++
			continue
		}
		if id, ok := strings.CutPrefix(field, "--id="); ok {
			if isWorkloadID(id) {
				references[id]++
			}
		}
	}
}

// recipeHasID 判断 recipe 是否携带 catalog workload ID 参数。
func recipeHasID(fields []string) bool {
	for index, field := range fields {
		if field == "--id" && index+1 < len(fields) && isWorkloadID(fields[index+1]) {
			return true
		}
		if id, ok := strings.CutPrefix(field, "--id="); ok && isWorkloadID(id) {
			return true
		}
	}
	return false
}

func hasField(fields []string, want string) bool {
	return slices.Contains(fields, want)
}

func hasFieldPrefix(fields []string, prefix string) bool {
	for _, field := range fields {
		if strings.HasPrefix(field, prefix) {
			return true
		}
	}
	return false
}

func callName(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		return value.Sel.Name
	default:
		return ""
	}
}

func hasFunction(file *ast.File, name string) bool {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name != nil && function.Name.Name == name {
			return true
		}
	}
	return false
}
