package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func TestRemoteCIMissOnlyPlanningAndShardManifestContract(t *testing.T) {
	root := findRepoRoot(t)
	assertRemoteCIMissPlannerBoundary(t, root)
	assertRemoteCIShardManifestBoundary(t, root)
}

// assertRemoteCIMissPlannerBoundary 守护 PASS 先返回且只有冻结 MISS 集可进入拆分器。
func assertRemoteCIMissPlannerBoundary(t *testing.T, root string) {
	t.Helper()
	prepared := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/remoteci/prepared_run.go"))
	assertRemoteCIMarkerOrder(t, prepared, "prepareRemoteWorkloadIdentity(", "prepareRemoteWorkloadReuse(", "remoteCompileGroupInputsForMisses(")
	assertRemoteCIMarkerOrder(t, prepared, "prepareRemoteWorkloadReuse(", "validateAllHitExecutionIdentity(", "remoteCompileGroupInputsForMisses(")
	identityPath := remoteCIFunctionSource(t, filepath.Join(root, "internal/devtools/remoteci/prepared_run.go"), "prepareRemoteWorkloadIdentity")
	assertRemoteCIMarkerOrder(t, identityPath, "remoteWorkloadFingerprintsWithSnapshot(", "workerExecutionDigest(")
	allHitIdentity := remoteCIFunctionSource(t, filepath.Join(root, "internal/devtools/remoteci/prepared_run.go"), "validateAllHitExecutionIdentity")
	for _, marker := range []string{"CandidateGateSourceSHA256", "CandidateGateToolchainSHA256", "ExecutionRunnerImage", "ExecutionImageCacheSnapshotID", "ImageCacheOnly"} {
		if !strings.Contains(allHitIdentity, marker) {
			t.Fatalf("all-hit identity guard is missing MISS-only field %q", marker)
		}
	}
	missPath := remoteCIFunctionSource(t, filepath.Join(root, "internal/devtools/remoteci/prepared_run.go"), "executePreparedWorkloadMisses")
	assertRemoteCIMarkerOrder(t, missPath, "if prepared.allReused", "createRemoteTempRoot(", "runRemoteWorkloadMisses(")
	if !strings.Contains(missPath, "prepared.reuse.cacheMisses") {
		t.Fatal("remote workload splitter input is not the frozen MISS set")
	}
	coordinatorPath := filepath.Join(root, "internal/devtools/remoteci/coordinator.go")
	coordinatorSource := readRemoteCIContractGuardFile(t, coordinatorPath)
	if strings.Contains(coordinatorSource, "func buildRemoteExecutionShardSet(") {
		t.Fatal("remote CI retains a full-catalog shard planner entrypoint")
	}
	coordinator := remoteCIFunctionSource(t, coordinatorPath, "prepareRemoteWorkloadMissInputs")
	assertRemoteCIMarkerOrder(t, coordinator, "buildRemoteExecutionShardSetForWorkloads(", "remoteExecutionShardResources(", "buildShardRequestsWithCompileGroups(")
	assertRemoteCIPlannerCallArgument(t, coordinatorPath, "prepareRemoteWorkloadMissInputs", "buildRemoteExecutionShardSetForWorkloads", 2, "executionIDs")
	assertRemoteCIPlannerCallArgument(t, coordinatorPath, "buildRemoteExecutionShardSetForWorkloads", "BuildWorkloadExecutionPlanForWorkloads", 4, "executionIDs")
	assertRemoteCIPlannerCallArgument(t, coordinatorPath, "buildRemoteExecutionShardSetForWorkloads", "BuildWorkloadExecutionPlanForWorkloadsWithCompileInputs", 4, "executionIDs")
	compilePlanner := remoteCIFunctionSource(t, filepath.Join(root, "internal/devtools/gate/workload_compile_planning.go"), "splitCompilePlanningBucket")
	if !strings.Contains(compilePlanner, "splitCompilePlanningPartitions(bucket)") || !strings.Contains(compilePlanner, "compileGroupFromPartition(partition") {
		t.Fatal("compile planner does not bind each deterministic artifact partition to one compile group")
	}
	if strings.Contains(compilePlanner, "minimumCompilePartitionCount") || strings.Contains(compilePlanner, "distributeSelectorsByBody") {
		t.Fatal("compile planner retains target-driven duplicate artifact partitioning")
	}
}

// assertRemoteCIShardManifestBoundary 守护 MISS 分片请求与 worker manifest 的唯一协议。
func assertRemoteCIShardManifestBoundary(t *testing.T, root string) {
	t.Helper()
	protocol := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/remoteci/protocol_shard_request.go"))
	bootstrap := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/remoteci/accepted_bootstrap_request.go"))
	coordinator := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/remoteci/coordinator.go"))
	requestBuilder := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/remoteci/coordinator_request.go"))
	materializer := readRemoteCIContractGuardFile(t, filepath.Join(root, "cmd/super-dolphin-gate/remote_materialize.go"))
	installer := readRemoteCIContractGuardFile(t, filepath.Join(root, "cmd/super-dolphin-gate/remote_manifest_install.go"))
	worker := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/executor_plan.go")) +
		readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/executor_compile_group.go"))
	for path, markers := range map[string][]string{
		"protocol":     {"CompileGroups", "ShardExecutionManifestDigest", "RemoteShardRequestMaxBytes"},
		"bootstrap":    {"acceptedBootstrapShardRequestSchemaVersion", "acceptedBootstrapCompileGroupSchemaVersion", "DecodeBootstrapShardRequest", "ValidateBootstrapIdentity"},
		"coordinator":  {"EncodeBootstrapShardRequest", "EncodeShardRequest", "bootstrapRequestKey", "requestKey"},
		"materializer": {"DecodeBootstrapShardRequest", "EncodeAcceptedBootstrapManifestForRequest", "handoffRemoteWorkRootWithManifest", "os.Rename"},
		"installer":    {"DecodeBootstrapShardRequest", "DecodeShardRequest", "ValidateBootstrapIdentity", "publishCurrentRemoteManifest", "os.Rename"},
		"worker":       {"--manifest-path", "ExecutorShardExecutionManifestPath", "LoadShardExecutionManifestFile", "ValidateCompileGroupExecutions"},
	} {
		source := map[string]string{
			"protocol": protocol, "bootstrap": bootstrap, "coordinator": coordinator,
			"materializer": materializer, "installer": installer, "worker": worker,
		}[path]
		for _, marker := range markers {
			if !strings.Contains(source, marker) {
				t.Errorf("%s is missing miss-only shard manifest marker %q", path, marker)
			}
		}
	}
	if cicontract.RemoteShardRequestMaxBytes != 1<<20 {
		t.Fatalf("remote shard request byte limit = %d, want 1 MiB", cicontract.RemoteShardRequestMaxBytes)
	}
	if strings.Contains(worker, `"run-plan"`) || strings.Contains(worker, `"--gates"`) {
		t.Fatal("worker parser retains retired run-plan or long --gates entry")
	}
	assertRemoteCIMarkerOrder(t, requestBuilder, `go build`, `"$built_gate" _remote-install-manifest`)
	if strings.Contains(installer, "DisallowUnknownFields") || strings.Contains(installer, "runExecutorProgram") {
		t.Fatal("candidate manifest installer retains a decoder bypass or second executor")
	}
	assertAcceptedGateProjectionBoundary(t, root)
}

// assertAcceptedGateProjectionBoundary 守护 accepted v1 与 current v2 的 GateIDs 投影边界。
func assertAcceptedGateProjectionBoundary(t *testing.T, root string) {
	t.Helper()
	acceptedPath := filepath.Join(root, "internal/devtools/remoteci/accepted_bootstrap_request.go")
	allowedOccurrences := 0
	for _, relRoot := range []string{"internal/devtools/remoteci", "cmd/super-dolphin-gate", "internal/devtools/gate"} {
		count := acceptedGateProjectionOccurrences(t, filepath.Join(root, relRoot), acceptedPath)
		allowedOccurrences += count
	}
	if allowedOccurrences != 5 {
		t.Fatalf("accepted bootstrap ProjectAcceptedGateIDs occurrences = %d, want declaration plus four boundary calls", allowedOccurrences)
	}
}

func acceptedGateProjectionOccurrences(t *testing.T, walkRoot, acceptedPath string) int {
	t.Helper()
	occurrences := 0
	if err := filepath.WalkDir(walkRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		count, err := inspectAcceptedGateProjectionFile(t, path, acceptedPath)
		if err != nil {
			return err
		}
		occurrences += count
		return nil
	}); err != nil {
		t.Fatalf("walk remote CI projection boundary %s: %v", walkRoot, err)
	}
	return occurrences
}

func inspectAcceptedGateProjectionFile(t *testing.T, path, acceptedPath string) (int, error) {
	t.Helper()
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, path, nil, 0)
	if err != nil {
		return 0, err
	}
	occurrences := 0
	ast.Inspect(file, func(node ast.Node) bool {
		ident, ok := node.(*ast.Ident)
		if !ok || ident.Name != "ProjectAcceptedGateIDs" {
			return true
		}
		if filepath.Clean(path) != filepath.Clean(acceptedPath) {
			position := fileSet.Position(ident.Pos())
			t.Errorf("ProjectAcceptedGateIDs escaped accepted bootstrap boundary at %s:%d", path, position.Line)
			return true
		}
		occurrences++
		return true
	})
	return occurrences, nil
}

func assertRemoteCIMarkerOrder(t *testing.T, source string, markers ...string) {
	t.Helper()
	previous := -1
	for _, marker := range markers {
		position := strings.Index(source, marker)
		if position < 0 {
			t.Fatalf("source is missing ordered marker %q", marker)
		}
		if position <= previous {
			t.Fatalf("source marker %q is out of order", marker)
		}
		previous = position
	}
}

// assertRemoteCIPlannerCallArgument 锁定 DCPAP/LPT 调用只接收调用方冻结的 MISS ID 参数。
func assertRemoteCIPlannerCallArgument(t *testing.T, path, functionName, calledName string, argumentIndex int, expected string) {
	t.Helper()
	source := readRemoteCIContractGuardFile(t, path)
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, path, source, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	function := remoteCIFunctionByName(file, functionName)
	if function == nil || function.Body == nil {
		t.Fatalf("%s is missing function %s", path, functionName)
	}
	calls := 0
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || remoteCICallName(call) != calledName {
			return true
		}
		calls++
		if len(call.Args) <= argumentIndex {
			t.Fatalf("%s.%s call has %d arguments, want argument %d to be %q", path, functionName, len(call.Args), argumentIndex+1, expected)
		}
		identifier, ok := call.Args[argumentIndex].(*ast.Ident)
		if !ok || identifier.Name != expected {
			t.Fatalf("%s.%s passes a non-%q expression to %s argument %d", path, functionName, expected, calledName, argumentIndex+1)
		}
		return true
	})
	if calls == 0 {
		t.Fatalf("%s.%s does not call %s", path, functionName, calledName)
	}
}

func remoteCIFunctionSource(t *testing.T, path, functionName string) string {
	t.Helper()
	source := readRemoteCIContractGuardFile(t, path)
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, path, source, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	function := remoteCIFunctionByName(file, functionName)
	if function == nil || function.Body == nil {
		t.Fatalf("%s is missing function %s", path, functionName)
	}
	start := fileSet.Position(function.Body.Pos()).Offset
	end := fileSet.Position(function.Body.End()).Offset
	if start < 0 || end < start || end > len(source) {
		t.Fatalf("%s function %s has invalid source range [%d:%d]", path, functionName, start, end)
	}
	return source[start:end]
}

func TestRemoteCIFunctionSourceScopesToTargetBody(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.go")
	source := "package fixture\n\nfunc target() {\n\tfirstMarker()\n}\n\nfunc later() {\n\tlaterMarker()\n}\n"
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	scoped := remoteCIFunctionSource(t, path, "target")
	if !strings.Contains(scoped, "firstMarker()") {
		t.Fatal("target function body marker is missing")
	}
	if strings.Contains(scoped, "laterMarker()") {
		t.Fatal("target function source includes a later function")
	}
}
