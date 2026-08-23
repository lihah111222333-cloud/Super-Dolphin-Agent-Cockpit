package mcplspcatalogguard

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/scripts/mcp_lsp_workload_catalog"
)

func TestValidateRunnerConsumerRejectsDeadCatalogFlow(t *testing.T) {
	source := `package main
import catalog "github.com/lihah111222333-cloud/super-dolphin-agent/scripts/mcp_lsp_workload_catalog"
func loadRunnerWorkload(repoRoot, tree, id string) (catalog.Catalog, catalog.Workload, error) {
 if false { document, _ := catalog.LoadAt(repoRoot, tree); _, _ = document.Find(id) }
 return catalog.Catalog{}, catalog.Workload{}, nil
}
`
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", []byte(source), 0)
	if err != nil {
		t.Fatal(err)
	}
	if hasLiveRunnerCatalogFlow(file) {
		t.Fatal("hasLiveRunnerCatalogFlow() accepted dead catalog flow")
	}
}

func TestValidateOwnedGateConsumerRejectsDeadTreeString(t *testing.T) {
	source := `package main
func run() { runCommand("./scripts/run_mcp_lsp_workload.sh") }
func mcpLSPReceiptGuardArgs(tree, receipt string) []string {
	dead := "--tree"
	_ = dead
	return []string{"./scripts/check_mcp_lsp_workload_catalog.sh", receipt}
}
`
	file, err := parser.ParseFile(token.NewFileSet(), "owned_gate_execution.go", []byte(source), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateOwnedGateConsumer("owned_gate_execution.go", file); err == nil || !strings.Contains(err.Error(), "candidate tree") {
		t.Fatalf("validateOwnedGateConsumer() error = %v, want return-argument tree binding rejection", err)
	}
}

func TestValidateOwnedGateConsumerRejectsWrongTreeDataflow(t *testing.T) {
	source := `package main
func runMcpLSPQuickRoundTrip(plan gatePlan) {
	otherTree := "wrong"
	runCommand("./scripts/run_mcp_lsp_workload.sh", "--tree", otherTree)
	guardArgs := mcpLSPReceiptGuardArgs(otherTree, "receipt")
	runCommand(gateExecutionDir(plan, ""), guardArgs[0], guardArgs[1:]...)
}

func mcpLSPReceiptGuardArgs(tree, receipt string) []string {
	return []string{"./scripts/check_mcp_lsp_workload_catalog.sh", "--tree", tree, receipt}
}
`
	file, err := parser.ParseFile(token.NewFileSet(), "owned_gate_execution.go", []byte(source), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateOwnedGateConsumer("owned_gate_execution.go", file); err == nil || !strings.Contains(err.Error(), "candidate tree") {
		t.Fatalf("validateOwnedGateConsumer() error = %v, want wrong tree dataflow rejection", err)
	}
}

func TestValidateOwnedGateConsumerRejectsWrongCatalogRepository(t *testing.T) {
	raw := string(readGuardCandidateBlob(t, "scripts/ai_maintenance/owned_gate_execution.go"))
	mutated := strings.Replace(raw, `catalog.LoadAt(gateExecutionDir(plan, ""), plan.ReviewObject.CandidateTree)`, `catalog.LoadAt(otherRoot, plan.ReviewObject.CandidateTree)`, 1)
	if mutated == raw {
		t.Fatal("catalog repository mutation anchor not found")
	}
	file, err := parser.ParseFile(token.NewFileSet(), "owned_gate_execution.go", mutated, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateOwnedGateConsumer("owned_gate_execution.go", file); err == nil {
		t.Fatal("owned gate guard accepted catalog loaded from the wrong repository")
	}
}

func TestValidateOwnedGateConsumerAcceptsLiveSource(t *testing.T) {
	raw := readGuardCandidateBlob(t, "scripts/ai_maintenance/owned_gate_execution.go")
	file, err := parser.ParseFile(token.NewFileSet(), "owned_gate_execution.go", raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateOwnedGateConsumer("owned_gate_execution.go", file); err != nil {
		t.Fatalf("validateOwnedGateConsumer() error = %v, want live receipt guard flow", err)
	}
}

func TestValidateOwnedGateConsumerRejectsUntrustedCatalogGuardExecution(t *testing.T) {
	raw := readGuardCandidateBlob(t, "scripts/ai_maintenance/owned_gate_execution.go")
	mutated := strings.Replace(string(raw), "result.BinaryPath, []string{\"LC_ALL=C\"}", `os.Getenv("SUPER_DOLPHIN_GATE_SELF"), gateCommandEnvironment("")`, 1)
	if mutated == string(raw) {
		t.Fatal("catalog guard execution mutation anchor not found")
	}
	file, err := parser.ParseFile(token.NewFileSet(), "owned_gate_execution.go", mutated, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateOwnedGateConsumer("owned_gate_execution.go", file); err == nil {
		t.Fatal("owned gate guard accepted ambient self binary/environment authority")
	}
}

func TestValidateOwnedGateConsumerRejectsEarlySuccessBeforeAuthority(t *testing.T) {
	raw := string(readGuardCandidateBlob(t, "scripts/ai_maintenance/owned_gate_execution.go"))
	for _, declaration := range []string{
		"func runMcpLSPCatalogGuard(plan gatePlan) error {",
		"func runMcpLSPQuickReleaseAuthority(plan gatePlan) error {",
	} {
		mutated := strings.Replace(raw, declaration, declaration+"\n\treturn nil", 1)
		file, err := parser.ParseFile(token.NewFileSet(), "owned_gate_execution.go", mutated, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateOwnedGateConsumer("owned_gate_execution.go", file); err == nil {
			t.Fatalf("owned gate guard accepted early success in %s", declaration)
		}
	}
}

func TestConsumerGuardsRejectNestedConditionalAuthorityBypass(t *testing.T) {
	for _, test := range []struct {
		path        string
		declaration string
		bypass      string
		validate    func(*ast.File) bool
	}{
		{path: "scripts/ai_maintenance/owned_gate_execution.go", declaration: "func runMcpLSPCatalogGuard(plan gatePlan) error {", bypass: "if bypass { return nil }", validate: func(file *ast.File) bool { return validateOwnedGateConsumer("owned_gate_execution.go", file) == nil }},
		{path: "scripts/ai_maintenance/owned_gate_execution.go", declaration: "func runMcpLSPQuickReleaseAuthority(plan gatePlan) error {", bypass: "if bypass { return nil }", validate: func(file *ast.File) bool { return validateOwnedGateConsumer("owned_gate_execution.go", file) == nil }},
		{path: "scripts/ai_maintenance/release_gate_rules.go", declaration: "func (policy gatePlanPolicy) applyOwnedGateRules(file string, gates map[string]bool) {", bypass: "if bypass { return }", validate: func(file *ast.File) bool { return validateSchedulerRouteConsumer("release_gate_rules.go", file) == nil }},
		{path: "scripts/ai_maintenance/main.go", declaration: "func applyFileGateRules(file string, capabilityRules capcontract.PathRules, turnContractPaths, gates, generated map[string]bool, policy gatePlanPolicy, criticalTypecheckContext criticalTypecheckPlanContext) (bool, error) {", bypass: "if bypass { return false, nil }", validate: func(file *ast.File) bool { return validateSchedulerPolicyConsumer("main.go", file) == nil }},
		{path: "scripts/mcp_lsp_workload_runner/main.go", declaration: "func loadRunnerWorkload(repoRoot, tree, id string) (catalog.Catalog, catalog.Workload, error) {", bypass: "if bypass { return catalog.Catalog{}, catalog.Workload{}, nil }", validate: hasLiveRunnerCatalogFlow},
	} {
		raw := string(readGuardCandidateBlob(t, test.path))
		mutated := strings.Replace(raw, test.declaration, test.declaration+"\n\t"+test.bypass, 1)
		if mutated == raw {
			t.Fatalf("mutation declaration %q not found in %s", test.declaration, test.path)
		}
		file, err := parser.ParseFile(token.NewFileSet(), test.path, mutated, 0)
		if err != nil {
			t.Fatal(err)
		}
		if test.validate(file) {
			t.Fatalf("consumer guard accepted nested conditional bypass in %s", test.path)
		}
	}
}

func TestConsumerGuardsRejectDetachedErrorChecks(t *testing.T) {
	for _, test := range []struct {
		path     string
		old      string
		new      string
		validate func(*ast.File) bool
	}{
		{path: "scripts/ai_maintenance/owned_gate_execution.go", old: `return fmt.Errorf("build trusted candidate Gate for mcp-lsp catalog: %w", err)`, new: `return shadowErr`, validate: func(file *ast.File) bool { return validateOwnedGateConsumer("owned_gate_execution.go", file) == nil }},
		{path: "scripts/ai_maintenance/owned_gate_execution.go", old: `return fmt.Errorf("load mcp-lsp quick release authority: %w", err)`, new: `return shadowErr`, validate: func(file *ast.File) bool { return validateOwnedGateConsumer("owned_gate_execution.go", file) == nil }},
		{path: "scripts/mcp_lsp_workload_runner/main.go", old: `if err := validateRunnerWorkload(workload); err != nil {`, new: `if err := validateRunnerWorkload(workload); err == nil {`, validate: hasRunnerExecutionFlow},
	} {
		raw := string(readGuardCandidateBlob(t, test.path))
		mutated := strings.Replace(raw, test.old, test.new, 1)
		if mutated == raw {
			t.Fatalf("error mutation anchor %q not found", test.old)
		}
		file, err := parser.ParseFile(token.NewFileSet(), test.path, mutated, 0)
		if err != nil {
			t.Fatal(err)
		}
		if test.validate(file) {
			t.Fatalf("consumer guard accepted detached error check in %s", test.path)
		}
	}
}

func TestConsumerGuardsRejectDeferredAndNamedResultOverrides(t *testing.T) {
	tests := []struct {
		path             string
		declaration      string
		namedDeclaration string
		deferred         string
		validate         func(*ast.File) bool
	}{
		{path: "scripts/ai_maintenance/release_gate_rules.go", declaration: "func (policy gatePlanPolicy) applyOwnedGateRules(file string, gates map[string]bool) {", namedDeclaration: "func (policy gatePlanPolicy) applyOwnedGateRules(file string, gates map[string]bool) (success bool) {", deferred: `defer delete(gates, "mcp-lsp:catalog")`, validate: func(file *ast.File) bool { return validateSchedulerRouteConsumer("release_gate_rules.go", file) == nil }},
		{path: "scripts/ai_maintenance/main.go", declaration: "func applyFileGateRules(file string, capabilityRules capcontract.PathRules, turnContractPaths, gates, generated map[string]bool, policy gatePlanPolicy, criticalTypecheckContext criticalTypecheckPlanContext) (bool, error) {", namedDeclaration: "func applyFileGateRules(file string, capabilityRules capcontract.PathRules, turnContractPaths, gates, generated map[string]bool, policy gatePlanPolicy, criticalTypecheckContext criticalTypecheckPlanContext) (backendFile bool, err error) {", deferred: `defer delete(gates, "mcp-lsp:idle-quick")`, validate: func(file *ast.File) bool { return validateSchedulerPolicyConsumer("main.go", file) == nil }},
		{path: "scripts/ai_maintenance/gate_execution.go", declaration: "func gateRunners(plan gatePlan) map[string]gateRunner {", namedDeclaration: "func gateRunners(plan gatePlan) (runnersResult map[string]gateRunner) {", deferred: `defer func() { runnersResult = nil }()`, validate: func(file *ast.File) bool { return validateSchedulerExecutionConsumer("gate_execution.go", file) == nil }},
		{path: "scripts/ai_maintenance/owned_gate_execution.go", declaration: "func runMcpLSPCatalogGuard(plan gatePlan) error {", namedDeclaration: "func runMcpLSPCatalogGuard(plan gatePlan) (result error) {", deferred: `defer func() { result = nil }()`, validate: func(file *ast.File) bool { return validateOwnedGateConsumer("owned_gate_execution.go", file) == nil }},
		{path: "scripts/ai_maintenance/owned_gate_execution.go", declaration: "func runMcpLSPQuickReleaseAuthority(plan gatePlan) error {", namedDeclaration: "func runMcpLSPQuickReleaseAuthority(plan gatePlan) (result error) {", deferred: `defer func() { result = nil }()`, validate: func(file *ast.File) bool { return validateOwnedGateConsumer("owned_gate_execution.go", file) == nil }},
		{path: "scripts/mcp_lsp_workload_runner/main.go", declaration: "func run(args []string) error {", namedDeclaration: "func run(args []string) (result error) {", deferred: `defer func() { result = nil }()`, validate: hasRunnerExecutionFlow},
	}
	for _, test := range tests {
		t.Run(test.path+"/defer", func(t *testing.T) {
			assertConsumerMutationRejected(t, test, test.declaration+"\n\t"+test.deferred)
		})
		t.Run(test.path+"/named-result", func(t *testing.T) {
			assertConsumerMutationRejected(t, test, test.namedDeclaration)
		})
	}
}

func assertConsumerMutationRejected(t *testing.T, test struct {
	path             string
	declaration      string
	namedDeclaration string
	deferred         string
	validate         func(*ast.File) bool
}, replacement string) {
	t.Helper()
	raw := string(readGuardCandidateBlob(t, test.path))
	mutated := strings.Replace(raw, test.declaration, replacement, 1)
	if mutated == raw {
		t.Fatalf("mutation declaration %q not found", test.declaration)
	}
	file, err := parser.ParseFile(token.NewFileSet(), test.path, mutated, 0)
	if err != nil {
		t.Fatal(err)
	}
	if test.validate(file) {
		t.Fatalf("consumer guard accepted authority override in %s", test.path)
	}
}

func TestConsumerGuardsRejectErrorCheckInitializers(t *testing.T) {
	for _, test := range []struct {
		path     string
		old      string
		new      string
		validate func(*ast.File) bool
	}{
		{path: "scripts/ai_maintenance/owned_gate_execution.go", old: "if err != nil {\n\t\treturn fmt.Errorf(\"build trusted candidate Gate for mcp-lsp catalog: %w\", err)", new: "if shadowErr := error(nil); err != nil {\n\t\treturn fmt.Errorf(\"build trusted candidate Gate for mcp-lsp catalog: %w\", err)", validate: func(file *ast.File) bool { return validateOwnedGateConsumer("owned_gate_execution.go", file) == nil }},
	} {
		raw := string(readGuardCandidateBlob(t, test.path))
		mutated := strings.Replace(raw, test.old, test.new, 1)
		if mutated == raw {
			t.Fatalf("error initializer mutation anchor %q not found", test.old)
		}
		file, err := parser.ParseFile(token.NewFileSet(), test.path, mutated, 0)
		if err != nil {
			t.Fatal(err)
		}
		if test.validate(file) {
			t.Fatalf("consumer guard accepted error-check initializer in %s", test.path)
		}
	}
}

func TestConsumerGuardsRejectPostAuthorityOverrides(t *testing.T) {
	for _, test := range []struct {
		path     string
		old      string
		new      string
		validate func(*ast.File) bool
	}{
		{path: "scripts/ai_maintenance/main.go", old: `policy.applyOwnedGateRules(file, gates)`, new: "policy.applyOwnedGateRules(file, gates)\n\tdelete(gates, \"mcp-lsp:catalog\")", validate: func(file *ast.File) bool { return validateSchedulerPolicyConsumer("main.go", file) == nil }},
		{path: "scripts/ai_maintenance/release_gate_rules.go", old: `gates["mcp-lsp:idle-quick"] = true`, new: "gates[\"mcp-lsp:idle-quick\"] = true\n\t\tgates[\"mcp-lsp:catalog\"] = false", validate: func(file *ast.File) bool { return validateSchedulerRouteConsumer("release_gate_rules.go", file) == nil }},
		{path: "scripts/ai_maintenance/gate_execution.go", old: `maps.Copy(runners, ownedGateRunners(plan))`, new: "maps.Copy(runners, ownedGateRunners(plan))\n\trunners[\"mcp-lsp:catalog\"] = gateRunner{}", validate: func(file *ast.File) bool { return validateSchedulerExecutionConsumer("gate_execution.go", file) == nil }},
	} {
		raw := string(readGuardCandidateBlob(t, test.path))
		mutated := strings.Replace(raw, test.old, test.new, 1)
		if mutated == raw {
			t.Fatalf("override mutation anchor %q not found", test.old)
		}
		file, err := parser.ParseFile(token.NewFileSet(), test.path, mutated, 0)
		if err != nil {
			t.Fatal(err)
		}
		if test.validate(file) {
			t.Fatalf("consumer guard accepted post-authority override in %s", test.path)
		}
	}
}

func writeGuardTreeFile(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertReceiptOnlyRequestParsing(t *testing.T, receiptPath string) guardRequest {
	t.Helper()
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "missing receipt and id", args: []string{"--receipt-only"}, want: "--receipt-only requires --receipt and --id"},
		{name: "missing id", args: []string{"--receipt-only", "--receipt", receiptPath}, want: "--receipt-only requires --receipt and --id"},
		{name: "missing receipt", args: []string{"--receipt-only", "--id", "mcp-lsp-idle-quick"}, want: "--receipt-only requires --receipt and --id"},
		{name: "relative receipt", args: []string{"--receipt-only", "--receipt", "receipt.json", "--id", "mcp-lsp-idle-quick"}, want: "--receipt-only requires an absolute --receipt"},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{"--repository", "/tmp/repo", "--tree", strings.Repeat("a", 40)}, test.args...)
			if _, err := parseGuardRequest(args); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("parseGuardRequest(%v) error = %v, want %q", test.args, err, test.want)
			}
		})
	}

	request, err := parseGuardRequest([]string{"--repository", "/tmp/repo", "--tree", strings.Repeat("a", 40), "--receipt-only", "--receipt", receiptPath, "--id", "mcp-lsp-idle-quick"})
	if err != nil {
		t.Fatalf("parse receipt-only request: %v", err)
	}
	if !request.receiptOnly || request.receipt != receiptPath || request.id != "mcp-lsp-idle-quick" {
		t.Fatalf("receipt-only request = %#v, want receipt-only absolute request", request)
	}
	return request
}

func assertReceiptOnlyValidationOrder(t *testing.T, request guardRequest) {
	t.Helper()
	document := catalog.Catalog{Workloads: []catalog.Workload{{
		ID: "mcp-lsp-idle-quick", ImplementationStatus: "implemented",
	}}}
	err := validateGuardRequest(t.TempDir(), "0000000000000000000000000000000000000000", document, request)
	if err == nil || !strings.Contains(err.Error(), "read workload receipt") {
		t.Fatalf("receipt-only validation error = %v, want receipt validation after skipping static catalog checks", err)
	}

	request.receiptOnly = false
	err = validateGuardRequest(t.TempDir(), "0000000000000000000000000000000000000000", document, request)
	if err == nil || !strings.Contains(err.Error(), "catalog-owned go test command") {
		t.Fatalf("ordinary validation error = %v, want catalog selector validation before receipt", err)
	}
}

func commitGuardFixture(t *testing.T, root string) string {
	t.Helper()
	runGuardGit(t, root, "init", "--quiet")
	runGuardGit(t, root, "config", "user.name", "guard-test")
	runGuardGit(t, root, "config", "user.email", "guard-test@example.invalid")
	runGuardGit(t, root, "add", ".")
	runGuardGit(t, root, "commit", "--quiet", "-m", "fixture")
	return strings.TrimSpace(runGuardGitOutput(t, root, "rev-parse", "HEAD^{tree}"))
}

func runGuardGit(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func runGuardGitOutput(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(output)
}
