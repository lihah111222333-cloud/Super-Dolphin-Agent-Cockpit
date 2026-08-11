package archtest

import (
	"fmt"
	"go/ast"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestRemoteCIHundredSecondTargetCannotBecomeTermination rejects the precise
// target-duration spellings when they are wired to timeout/cancel/kill paths;
// the only allowed 100-second behavior is cicontract's warn-and-continue rule.
func TestRemoteCIHundredSecondTargetCannotBecomeTermination(t *testing.T) {
	root := findRepoRoot(t)
	for _, file := range remoteCIProductionFiles(t, root) {
		parsed := parseRemoteCIContractGuardFile(t, file)
		for _, violation := range remoteCIHundredSecondTerminationViolations(parsed) {
			t.Errorf("%s turns the 100-second target into terminating call %s", relativeRemoteCIContractPath(t, root, file), violation)
		}
	}
}

// TestRemoteCITestCommandHasOnlyTheRemoteECIPath prevents the test command
// from recreating a host executor or treating a coordinator cache probe as an
// authoritative result. Cache reuse remains an internal coordinator concern.
func TestRemoteCITestCommandHasOnlyTheRemoteECIPath(t *testing.T) {
	root := findRepoRoot(t)
	for _, relative := range []string{
		"cmd/super-dolphin-gate/test_local_exec.go",
		"internal/devtools/remoteci/local_test_policy.go",
		"internal/devtools/remoteci/local_test_policy_test.go",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(relative))); !os.IsNotExist(err) {
			t.Errorf("remote CI must not retain local test executor artifact %s", relative)
		}
	}

	path := filepath.Join(root, "cmd", "super-dolphin-gate", "test_cli.go")
	parsed := parseRemoteCIContractGuardFile(t, path)
	for _, identifier := range []string{
		"autoTestBackend",
		"selectAutoTestBackend",
		"runLockedLocalLightTests",
		"executeLocalLightTests",
		"localSourceMatchesTree",
	} {
		if remoteCIForbiddenIdentifiers(parsed)[identifier] {
			t.Errorf("cmd/super-dolphin-gate/test_cli.go retains local test routing %s", identifier)
		}
	}
	for _, literal := range remoteCIStringLiterals(parsed) {
		if strings.EqualFold(literal, "local-light") || strings.EqualFold(literal, "remote-cache") {
			t.Errorf("cmd/super-dolphin-gate/test_cli.go retains non-ECI backend %q", literal)
		}
	}
	for functionName, executionOwner := range map[string]string{
		"runFullRemoteTestInvocation":         "executeRemoteRun",
		"runExplicitRemoteGateWorkloadSubset": "executeRemoteRunSubset",
	} {
		for _, violation := range remoteCITestInvocationOwnerViolations(
			parsed, functionName, executionOwner,
		) {
			t.Errorf("test command remote result owner: %s", violation)
		}
	}
}

// remoteCITestInvocationOwnerViolations keeps full and explicit-subset test
// entries on the one finalized ECI result chain. The entries may share the
// result emitter, but neither may create a second executor, call a coordinator
// directly, or render a result that did not come from its finalized owner.
func remoteCITestInvocationOwnerViolations(
	file *ast.File,
	functionName, executionOwner string,
) []string {
	function := remoteCIFunctionByName(file, functionName)
	if function == nil {
		return []string{fmt.Sprintf("%s is missing", functionName)}
	}
	violations := make([]string, 0)
	executors := remoteCITestInvocationExecutionCalls(function)
	if !slices.Equal(executors, []string{executionOwner}) {
		violations = append(violations, fmt.Sprintf(
			"%s remote execution calls = %v, want [%s]",
			functionName, executors, executionOwner,
		))
	}
	functionFile := &ast.File{Decls: []ast.Decl{function}}
	if calls := remoteCIFunctionCallCount(functionFile, "emitRemoteRunResult"); calls != 1 {
		violations = append(violations, fmt.Sprintf(
			"%s emitRemoteRunResult calls = %d, want 1", functionName, calls,
		))
	}
	if !remoteCITestInvocationReturnsEmitter(function) {
		violations = append(violations, fmt.Sprintf(
			"%s must return emitRemoteRunResult", functionName,
		))
	}
	for _, forbidden := range []string{
		"NewCoordinator", "Prepare", "PrepareSubset", "Run", "RunPrepared",
	} {
		if calls := remoteCIFunctionCallCount(functionFile, forbidden); calls != 0 {
			violations = append(violations, fmt.Sprintf(
				"%s directly calls coordinator %s", functionName, forbidden,
			))
		}
	}
	return violations
}

func remoteCITestInvocationExecutionCalls(function *ast.FuncDecl) []string {
	calls := make([]string, 0)
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := remoteCITestInvocationCallName(call.Fun)
		if strings.HasPrefix(name, "execute") &&
			strings.Contains(strings.ToLower(name), "remote") {
			calls = append(calls, name)
		}
		return true
	})
	return calls
}

func remoteCITestInvocationCallName(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		return value.Sel.Name
	default:
		return ""
	}
}

func remoteCITestInvocationReturnsEmitter(function *ast.FuncDecl) bool {
	found := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		statement, ok := node.(*ast.ReturnStmt)
		if !ok || len(statement.Results) != 1 {
			return true
		}
		call, ok := statement.Results[0].(*ast.CallExpr)
		if ok && remoteCITestInvocationCallName(call.Fun) == "emitRemoteRunResult" {
			found = true
			return false
		}
		return true
	})
	return found
}

func TestRemoteCITestInvocationOwnerCounterexamples(t *testing.T) {
	tests := []struct {
		name           string
		source         string
		executionOwner string
		wantViolation  bool
	}{
		{
			name: "shared finalized emitter is allowed",
			source: `package fixture
func runFullRemoteTestInvocation() error {
 result, input, runErr := executeRemoteRun()
 return emitRemoteRunResult(stdout, input.LedgerStore, result, runErr)
}
func runExplicitRemoteGateWorkloadSubset() error {
 result, input, runErr := executeRemoteRunSubset()
 return emitRemoteRunResult(stdout, input.LedgerStore, result, runErr)
}`,
			executionOwner: "executeRemoteRun",
		},
		{
			name: "second remote executor is rejected",
			source: `package fixture
func runFullRemoteTestInvocation() error {
 result, input, runErr := executeRemoteRun()
 result, input, runErr = executeReplicaRemoteRun()
 return emitRemoteRunResult(stdout, input.LedgerStore, result, runErr)
}`,
			executionOwner: "executeRemoteRun",
			wantViolation:  true,
		},
		{
			name: "direct coordinator is rejected",
			source: `package fixture
func runFullRemoteTestInvocation() error {
 result, input, runErr := coordinator.Run()
 return emitRemoteRunResult(stdout, input.LedgerStore, result, runErr)
}`,
			executionOwner: "executeRemoteRun",
			wantViolation:  true,
		},
		{
			name: "unfinalized result is rejected",
			source: `package fixture
func runFullRemoteTestInvocation() error {
 result := remoteRunResult{}
 return emitRemoteRunResult(stdout, ledgerStore, result, nil)
}`,
			executionOwner: "executeRemoteRun",
			wantViolation:  true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed := remoteCIParseGuardFixture(t, test.source)
			violations := remoteCITestInvocationOwnerViolations(
				parsed, "runFullRemoteTestInvocation", test.executionOwner,
			)
			if got := len(violations) != 0; got != test.wantViolation {
				t.Fatalf("violations = %v, want violation = %t", violations, test.wantViolation)
			}
		})
	}
}

func TestRemoteCIHooksRejectConcurrencyCaps(t *testing.T) {
	root := findRepoRoot(t)
	for _, hook := range []string{"pre-commit", "pre-push"} {
		contents := readRemoteCIContractGuardFile(t, filepath.Join(root, ".githooks", hook))
		for _, forbidden := range []string{"max_shards", "max-shards", "max_concurrency", "max-concurrency", "flock", "global_hook_lock", "global-hook-lock", "active_job_lock", "active-job-lock", "shared_raw_token", "shared-raw-token"} {
			if strings.Contains(strings.ToLower(contents), forbidden) {
				t.Errorf(".githooks/%s retains forbidden remote CI concurrency cap %q", hook, forbidden)
			}
		}
	}
}
