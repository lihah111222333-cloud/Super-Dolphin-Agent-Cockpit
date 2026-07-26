package archtest_test

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// TestCIEntrypointsRequireCoordinatorCLI executes the executable hook surfaces
// against a fake installed coordinator. The candidate CI script must become the
// same thin coordinator entrypoint; it must never be an authority-bearing host
// gate runner.
func TestCIEntrypointsRequireCoordinatorCLI(t *testing.T) {
	root := coordinatorContractRepoRoot(t)
	logPath, fakeBin := writeCoordinatorCLIForContractGuard(t)
	path := filepath.Dir(fakeBin) + string(os.PathListSeparator) + os.Getenv("PATH")
	configureCoordinatorContractGuardLauncher(t, root, fakeBin)

	for _, test := range []struct {
		name          string
		path          string
		args          []string
		want          []string
		treeBoundHook bool
	}{
		{name: "pre-commit", path: ".githooks/pre-commit", treeBoundHook: true},
		{name: "pre-push", path: ".githooks/pre-push", args: []string{"origin", "https://example.invalid/repository.git"}, want: []string{"hook pre-push origin https://example.invalid/repository.git"}},
		{name: "codex-stop", path: "scripts/codex_stop_gate.sh", want: []string{"hook codex"}},
		{name: "candidate-ci", path: "scripts/ci_truth_image_gate.sh", want: []string{"workflow-host"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(logPath, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			command := exec.Command("bash", append([]string{filepath.Join(root, filepath.FromSlash(test.path))}, test.args...)...)
			command.Dir = root
			command.Env = coordinatorContractEnv(path)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("%s must fail closed only through the trusted coordinator: %v\\n%s", test.path, err, output)
			}
			got := contractGuardCommandLog(t, logPath)
			if test.treeBoundHook {
				assertTreeBoundPreCommitCoordinatorCommands(t, root, got)
				return
			}
			if !slices.Equal(got, test.want) {
				t.Fatalf("%s coordinator argv = %#v, want %#v", test.path, got, test.want)
			}
		})
	}
}

const queuedCoordinatorJobID = "job-00000000000000000000000000000000"

func assertTreeBoundPreCommitCoordinatorCommands(t *testing.T, root string, got []string) {
	t.Helper()
	if len(got) != 3 {
		t.Fatalf("pre-commit coordinator argv = %#v, want closure, hook, and wait", got)
	}
	tree := closureTreeForCoordinatorContractGuard(t, root, strings.Fields(got[0]))
	assertCoordinatorCommandBindsTree(t, "hook", strings.Fields(got[1]), []string{"hook", "pre-commit", "--tree"}, tree)
	assertCoordinatorCommandBindsTree(t, "wait", strings.Fields(got[2]), []string{"wait", "--job", queuedCoordinatorJobID, "--tree"}, tree)
}

func closureTreeForCoordinatorContractGuard(t *testing.T, root string, closure []string) string {
	t.Helper()
	if len(closure) != 4 {
		t.Fatalf("closure coordinator argv = %#v, want closure check --tree <tree>", closure)
	}
	if !slices.Equal(closure[:3], []string{"closure", "check", "--tree"}) {
		t.Fatalf("closure coordinator argv = %#v, want closure check --tree <tree>", closure)
	}
	tree := closure[3]
	if tree != stagedTreeForCoordinatorContractGuard(t, root) {
		t.Fatalf("closure tree = %q, want current staged tree", tree)
	}
	return tree
}

func assertCoordinatorCommandBindsTree(t *testing.T, name string, command, wantPrefix []string, tree string) {
	t.Helper()
	if len(command) != len(wantPrefix)+1 {
		t.Fatalf("%s coordinator argv = %#v, want %#v followed by tree %q", name, command, wantPrefix, tree)
	}
	if !slices.Equal(command[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("%s coordinator argv = %#v, want %#v followed by tree %q", name, command, wantPrefix, tree)
	}
	if command[len(wantPrefix)] != tree {
		t.Fatalf("%s coordinator tree = %q, want %q", name, command[len(wantPrefix)], tree)
	}
}

func stagedTreeForCoordinatorContractGuard(t *testing.T, root string) string {
	t.Helper()
	command := exec.Command("git", "write-tree")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		t.Fatalf("capture staged tree: %v", err)
	}
	return strings.TrimSpace(string(output))
}

// TestCIWorkflowBootstrapsOnlyTheImmutableCoordinator keeps the exceptional
// Docker boundary narrow: the protected workflow may start its immutable
// bootstrap coordinator, but candidate checkout scripts may not run Docker.
func TestCIWorkflowBootstrapsOnlyTheImmutableCoordinator(t *testing.T) {
	root := coordinatorContractRepoRoot(t)
	workflow := readCoordinatorContractFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	if strings.Count(workflow, "docker run") != 1 || !strings.Contains(workflow, "workflow-host") {
		t.Fatalf("protected workflow must have exactly one immutable coordinator bootstrap: %q", workflow)
	}
	candidate := readCoordinatorContractFile(t, filepath.Join(root, "scripts", "ci_truth_image_gate.sh"))
	if strings.Contains(candidate, "docker run") || strings.Contains(candidate, "go test") || strings.Contains(candidate, "make guard") {
		t.Fatal("candidate CI script must delegate to the coordinator instead of running canonical gates on the host")
	}
}

func TestMakefileCIEntrypointsDelegateToCoordinator(t *testing.T) {
	makefile := readCoordinatorContractFile(t, filepath.Join(coordinatorContractRepoRoot(t), "Makefile"))
	recipes := makefileCITargetRecipes(makefile)
	wantProfiles := map[string]string{
		"ci-l0":         "local-fast",
		"ci-l1":         "push",
		"ci-l2-claude":  "remote-required",
		"ci-l3-release": "release",
	}
	if len(recipes) != len(wantProfiles) {
		t.Fatalf("Makefile CI targets = %#v, want exact L0-L3 coordinator entrypoints", recipes)
	}
	for target, profile := range wantProfiles {
		recipe, ok := recipes[target]
		if !ok {
			t.Errorf("Makefile is missing coordinator target %q", target)
			continue
		}
		joined := strings.Join(recipe, "\n")
		if strings.Contains(joined, "go test") || strings.Contains(joined, "docker run") || strings.Contains(joined, "make guard") || strings.Contains(joined, "$(TEST_WITH_GUARD)") {
			t.Errorf("Makefile target %q runs a canonical gate outside the coordinator: %q", target, joined)
		}
		want := "./scripts/ci_truth_image_gate.sh " + profile
		if len(recipe) != 1 || !strings.Contains(joined, want) {
			t.Errorf("Makefile target %q recipe = %q, want one %q delegation", target, joined, want)
		}
	}
}

func TestManualAndReleaseEntrypointsUseCoordinator(t *testing.T) {
	root := coordinatorContractRepoRoot(t)
	cli := parseContractGuardFile(t, filepath.Join(root, "cmd", "super-dolphin-gate", "coordinator_cli.go"))
	for _, requirement := range []struct{ function, call string }{
		{function: "connectProductionCoordinator", call: "ProbeDockerSchedulerAuthorityWithCapacity"},
		{function: "runSubmit", call: "runSubmitWithConnector"},
		{function: "runSubmitWithConnector", call: "withCoordinator"},
		{function: "runProductionReleaseSubmitPlanWithWaitConnector", call: "withCoordinator"},
		{function: "runProductionReleaseSubmitPlanWithWaitConnector", call: "submitAuthoritativeRelease"},
	} {
		if !contractGuardFunctionCalls(cli, requirement.function, requirement.call) {
			t.Errorf("%s must reach the coordinator through %s", requirement.function, requirement.call)
		}
	}
}

// TestProductionCoordinatorUsesDynamicContainerShardProtocol 用 go/parser 读取生产代码，
// 排除测试夹具后证明 owner 确实构造、调度、执行、聚合并完成动态分片组。
func TestProductionCoordinatorUsesDynamicContainerShardProtocol(t *testing.T) {
	evidence := coordinatorASTEvidence(t, coordinatorContractRepoRoot(t))
	for _, required := range []string{
		"BuildContainerShardSetWithCount",
		"RunContainerShards",
		"AggregateContainerShards",
		"ReportShardFailure",
		"CompleteGroup",
		"WorkloadKindShard",
		"GroupSize",
		"ShardIdentities",
	} {
		if !evidence.names[required] {
			t.Errorf("production coordinator is missing required dynamic shard protocol symbol %q", required)
		}
	}
	if len(evidence.planExecutionFallbacks) != 0 {
		t.Errorf("production coordinator retains forbidden PlanExecution=true single-container CI fallback at %s", strings.Join(evidence.planExecutionFallbacks, ", "))
	}
}

// TestShardResourceAndAggregationContract fixes the contract at the dynamically
// parsed producer source: each worker is 4 CPU / 8 GiB / 512 PIDs, with at most
// 64 workers, and the coordinator budgets remain 10m normal / 30m release.
func TestShardResourceAndAggregationContract(t *testing.T) {
	root := coordinatorContractRepoRoot(t)
	shards := parseContractGuardFile(t, filepath.Join(root, "internal", "devtools", "gate", "container_shards.go"))
	consts := contractGuardConsts(t, shards)
	for name, want := range map[string]string{
		"MaxContainerShards":          "64",
		"legacyContainerShardCount":   "3",
		"containerShardSchemaVersion": "2",
		"containerShardCPUNanos":      "4000000000",
		"containerShardMemoryBytes":   "8 << 30",
		"containerShardPIDs":          "512",
	} {
		if got := strings.ReplaceAll(consts[name], "_", ""); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if !contractGuardFunctionCalls(shards, "AggregateContainerShards", "aggregateContainerShardsWithClock") ||
		!contractGuardFunctionCalls(shards, "aggregateContainerShardsWithClock", "appendReleaseShardAggregation") ||
		!contractGuardFunctionCalls(shards, "appendReleaseShardAggregation", "executeReleaseLayerAttestation") {
		t.Error("release:ci-l3 must be generated only by AggregateContainerShards")
	}
	if callers := contractGuardCallers(shards, "executeReleaseLayerAttestation"); !slices.Equal(callers, []string{"appendReleaseShardAggregation"}) {
		t.Errorf("release:ci-l3 attestation callers = %#v, want only the aggregation helper", callers)
	}

	runtime := parseContractGuardFile(t, filepath.Join(root, "cmd", "super-dolphin-gate", "coordinator_runtime.go"))
	timeouts := contractGuardConsts(t, runtime)
	for name, want := range map[string]string{
		"coordinatorNormalTimeout":  "10 * time.Minute",
		"coordinatorReleaseTimeout": "30 * time.Minute",
	} {
		if got := timeouts[name]; got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func writeCoordinatorCLIForContractGuard(t *testing.T) (logPath, binaryPath string) {
	t.Helper()
	dir := t.TempDir()
	logPath = filepath.Join(dir, "coordinator.log")
	binaryPath = filepath.Join(dir, "super-dolphin-gate")
	script := fmt.Sprintf("#!/usr/bin/env bash\nset -euo pipefail\nprintf '%%s\\n' \"$*\" >> %q\nif [[ \"$1\" == \"hook\" && \"$2\" == \"pre-commit\" ]]; then\n  printf 'job=%s\\n'\n  exit 13\nfi\n", logPath, queuedCoordinatorJobID)
	if err := os.WriteFile(binaryPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return logPath, binaryPath
}
func configureCoordinatorContractGuardLauncher(t *testing.T, root, launcher string) {
	t.Helper()
	current := exec.Command("git", "config", "--local", "--get", "superdolphin.gateLauncher")
	current.Dir = root
	output, err := current.Output()
	previous := strings.TrimSpace(string(output))
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			t.Fatalf("read trusted launcher config: %v", err)
		}
	}
	set := exec.Command("git", "config", "--local", "superdolphin.gateLauncher", launcher)
	set.Dir = root
	if output, err := set.CombinedOutput(); err != nil {
		t.Fatalf("configure trusted launcher: %v\n%s", err, output)
	}
	t.Cleanup(func() {
		var restore *exec.Cmd
		if previous == "" {
			restore = exec.Command("git", "config", "--local", "--unset", "superdolphin.gateLauncher")
		} else {
			restore = exec.Command("git", "config", "--local", "superdolphin.gateLauncher", previous)
		}
		restore.Dir = root
		if output, err := restore.CombinedOutput(); err != nil {
			t.Errorf("restore trusted launcher config: %v\n%s", err, output)
		}
	})
}

func contractGuardCommandLog(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

func coordinatorContractEnv(path string) []string {
	environment := make([]string, 0, len(os.Environ())+1)
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "PATH=") {
			environment = append(environment, item)
		}
	}
	return append(environment, "PATH="+path)
}

func coordinatorContractRepoRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(directory, "..", ".."))
}

func readCoordinatorContractFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func makefileCITargetRecipes(makefile string) map[string][]string {
	recipes := make(map[string][]string)
	current := ""
	for line := range strings.SplitSeq(makefile, "\n") {
		if strings.HasPrefix(line, "\t") && current != "" {
			recipes[current] = append(recipes[current], strings.TrimSpace(line))
			continue
		}
		current = ""
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "#") {
			continue
		}
		target, _, found := strings.Cut(line, ":")
		if !found || (target != "ci" && !strings.HasPrefix(target, "ci-")) {
			continue
		}
		current = target
		recipes[current] = nil
	}
	return recipes
}

type coordinatorContractEvidence struct {
	names                  map[string]bool
	planExecutionFallbacks []string
}

func coordinatorASTEvidence(t *testing.T, root string) coordinatorContractEvidence {
	t.Helper()
	directory := filepath.Join(root, "cmd", "super-dolphin-gate")
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	evidence := coordinatorContractEvidence{names: make(map[string]bool)}
	for _, entry := range entries {
		if !isProductionCoordinatorGoFile(entry) {
			continue
		}
		collectCoordinatorASTFileEvidence(t, directory, entry.Name(), &evidence)
	}
	sort.Strings(evidence.planExecutionFallbacks)
	return evidence
}

func isProductionCoordinatorGoFile(entry os.DirEntry) bool {
	return !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") && !strings.HasSuffix(entry.Name(), "_test.go")
}

func collectCoordinatorASTFileEvidence(t *testing.T, directory, entryName string, evidence *coordinatorContractEvidence) {
	t.Helper()
	path := filepath.Join(directory, entryName)
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	ast.Inspect(file, func(node ast.Node) bool {
		recordCoordinatorASTNode(evidence, fset, entryName, node)
		return true
	})
}

func recordCoordinatorASTNode(evidence *coordinatorContractEvidence, fset *token.FileSet, entryName string, node ast.Node) {
	switch node := node.(type) {
	case *ast.Ident:
		recordCoordinatorProtocolIdentifier(evidence, node)
	case *ast.CallExpr:
		recordCoordinatorProtocolCall(evidence, node)
	case *ast.KeyValueExpr:
		recordCoordinatorPlanExecutionFallback(evidence, fset, entryName, node)
	}
}

func recordCoordinatorProtocolIdentifier(evidence *coordinatorContractEvidence, node *ast.Ident) {
	switch node.Name {
	case "WorkloadKindShard", "GroupSize", "ShardIdentities":
		evidence.names[node.Name] = true
	}
}

func recordCoordinatorProtocolCall(evidence *coordinatorContractEvidence, node *ast.CallExpr) {
	switch name := contractGuardCalledName(node.Fun); name {
	case "BuildContainerShardSetWithCount", "RunContainerShards", "AggregateContainerShards", "ReportShardFailure", "CompleteGroup":
		evidence.names[name] = true
	}
}

func recordCoordinatorPlanExecutionFallback(evidence *coordinatorContractEvidence, fset *token.FileSet, entryName string, node *ast.KeyValueExpr) {
	key, ok := node.Key.(*ast.Ident)
	if !ok || key.Name != "PlanExecution" || !contractGuardBool(node.Value, true) {
		return
	}
	position := fset.Position(node.Pos())
	evidence.planExecutionFallbacks = append(evidence.planExecutionFallbacks, fmt.Sprintf("%s:%d", filepath.ToSlash(filepath.Join("cmd", "super-dolphin-gate", entryName)), position.Line))
}

func contractGuardCalledName(expr ast.Expr) string {
	switch expression := expr.(type) {
	case *ast.Ident:
		return expression.Name
	case *ast.SelectorExpr:
		return expression.Sel.Name
	default:
		return ""
	}
}

func contractGuardBool(expr ast.Expr, want bool) bool {
	identifier, ok := expr.(*ast.Ident)
	return ok && identifier.Name == fmt.Sprint(want)
}

func parseContractGuardFile(t *testing.T, path string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return file
}

func contractGuardConsts(t *testing.T, file *ast.File) map[string]string {
	t.Helper()
	values := make(map[string]string)
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.CONST {
			continue
		}
		for _, specification := range general.Specs {
			valueSpec, ok := specification.(*ast.ValueSpec)
			if !ok || len(valueSpec.Names) != 1 || len(valueSpec.Values) != 1 {
				continue
			}
			values[valueSpec.Names[0].Name] = contractGuardExpr(t, valueSpec.Values[0])
		}
	}
	return values
}

func contractGuardFunctionCalls(file *ast.File, functionName, calledName string) bool {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != functionName {
			continue
		}
		found := false
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if ok && contractGuardCalledName(call.Fun) == calledName {
				found = true
				return false
			}
			return !found
		})
		return found
	}
	return false
}

func contractGuardCallers(file *ast.File, calledName string) []string {
	var callers []string
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || !contractGuardFunctionCalls(file, function.Name.Name, calledName) {
			continue
		}
		callers = append(callers, function.Name.Name)
	}
	sort.Strings(callers)
	return callers
}

func contractGuardExpr(t *testing.T, expression ast.Expr) string {
	t.Helper()
	var buffer bytes.Buffer
	if err := format.Node(&buffer, token.NewFileSet(), expression); err != nil {
		t.Fatal(err)
	}
	return buffer.String()
}
