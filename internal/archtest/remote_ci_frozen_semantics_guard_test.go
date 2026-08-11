package archtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// TestRemoteCIFrozenProtocolAndPlannerSemantics keeps the accepted request matrix,
// plan identity and PASS environment version aligned with their executable owner.
func TestRemoteCIFrozenProtocolAndPlannerSemantics(t *testing.T) {
	root := findRepoRoot(t)
	if err := cicontract.ValidateRemoteCIProtocolVersions(14, 1, 1, 15, 2); err != nil {
		t.Fatal(err)
	}
	assertRemoteCIFrozenIdentityAliases(t, root)
	assertRemoteCIPlanOwnerCaller(t, root)
	assertRemoteCIPassEnvironmentOwner(t, root)
}

func assertRemoteCIFrozenIdentityAliases(t *testing.T, root string) {
	t.Helper()
	for relative, markers := range map[string][]string{
		"internal/devtools/gate/workload_model.go": {
			"workloadExecutionPlanSchemaVersion = cicontract.WorkloadExecutionPlanSchemaVersion",
			"WorkloadPlanningAlgorithmID      = cicontract.WorkloadPlanningAlgorithmID",
			"workloadPlanningSearchNodeBudget = cicontract.WorkloadPlanningSearchNodeBudget",
			"cicontract.WorkloadEstimationPolicyDigest",
		},
		"internal/devtools/cicontract/contract.go": {
			"WorkloadPlanningAlgorithmID = \"deterministic-critical-path-aware-packing/v3\"",
			"WorkloadPlanningObjective = \"hard constraints>target excess>minimum shards>makespan>setup proxy>canonical layout\"",
			"WorkloadPlanningExactPackableUnitThreshold        = 12",
			"WorkloadPlanningSearchNodeBudget = 1_000_000",
			"WorkloadPlanningHeuristicMaxBeamTransitions       = 128",
		},
		"internal/devtools/remoteci/protocol_shard_request.go": {
			"ShardRequestSchemaVersion uint32 = cicontract.ShardRequestSchemaVersion",
			"ShardRequestMaxBytes = cicontract.RemoteShardRequestMaxBytes",
		},
		"internal/devtools/remoteci/accepted_bootstrap_request.go": {
			"acceptedBootstrapShardRequestSchemaVersion uint32 = cicontract.AcceptedBootstrapRequestSchemaVersion",
			"acceptedBootstrapCompileGroupSchemaVersion uint32 = cicontract.AcceptedCompileGroupSchemaVersion",
			"acceptedBootstrapManifestSchemaVersion uint32 = cicontract.AcceptedBootstrapManifestSchemaVersion",
		},
		"internal/devtools/gate/executor_shard_manifest.go": {
			"ShardExecutionManifestSchemaVersion uint32 = cicontract.ShardExecutionManifestSchemaVersion",
		},
		"internal/devtools/gate/compile_group.go": {
			"CompileGroupSchemaVersion uint32 = cicontract.CompileGroupSchemaVersion",
		},
	} {
		source := readRemoteCIContractGuardFile(t, filepath.Join(root, filepath.FromSlash(relative)))
		for _, marker := range markers {
			if !strings.Contains(source, marker) {
				t.Errorf("%s is missing frozen semantic marker %q", relative, marker)
			}
		}
	}
}

func assertRemoteCIPlanOwnerCaller(t *testing.T, root string) {
	t.Helper()
	planning := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/workload_planning.go"))
	model := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/workload_model.go"))
	for _, marker := range []string{"setupProxy", "score.makespanMS != other.makespanMS", "score.setupProxy", "score.layout < other.layout"} {
		if !strings.Contains(model, marker) {
			t.Fatalf("D-CPAP comparator must retain ordered marker %q", marker)
		}
	}
	if !strings.Contains(planning, "cicontract.ValidateWorkloadPlanContract") {
		t.Fatal("workload plan producer/validator must call the canonical cicontract owner")
	}
	if strings.Contains(planning, "ValidateWorkloadPlanContract(8,") || strings.Contains(planning, "ValidateWorkloadPlanContract(9,") {
		t.Fatal("workload plan production path must not retain literal schema versions")
	}
	durationIndex := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/workload_duration_index.go"))
	for _, forbidden := range []string{
		"workloadExecutionPlanSchemaVersion = 9",
		"WorkloadPlanningAlgorithmID      = \"deterministic-critical-path-aware-packing/v2\"",
		"DurationEstimatorPolicyID = \"deterministic-duration-estimator/v2\"",
	} {
		if strings.Contains(model, forbidden) || strings.Contains(durationIndex, forbidden) {
			t.Errorf("gate retains a copied planner identity literal %q", forbidden)
		}
	}
}

func assertRemoteCIPassEnvironmentOwner(t *testing.T, root string) {
	t.Helper()
	passReuse := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/remoteci/workload_pass_reuse.go"))
	if got, want := cicontract.WorkloadPassEnvironmentSchemaVersion, "remote-workload-pass-environment/v10"; got != want {
		t.Errorf("PASS environment owner schema = %q, want %q", got, want)
	}
	if !strings.Contains(passReuse, "cicontract.WorkloadPassEnvironmentSchemaVersion") {
		t.Error("PASS environment must reference the canonical cicontract schema owner")
	}
	if strings.Contains(passReuse, "remote-workload-pass-environment/v9") {
		t.Error("retired PASS environment schema v9 remains in production reuse path")
	}
}

// TestRemoteCIWorkloadPassIdentityCollisionIsFailFast guards SQLite identity
// collisions and retention proof against silent upsert or floating origins.
func TestRemoteCIWorkloadPassIdentityCollisionIsFailFast(t *testing.T) {
	root := findRepoRoot(t)
	evidence := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/workload_pass_evidence.go"))
	for _, marker := range []string{
		"conflicting workload pass evidence proof",
		"WHERE identity_digest = ? AND accepted_generation = ?",
		"loadWorkloadPassEvidenceProof",
		"stored.matches(",
		"originExecutionJSON",
	} {
		if !strings.Contains(evidence, marker) {
			t.Errorf("SQLite identity collision guard is missing %q", marker)
		}
	}
	for _, forbidden := range []string{"INSERT OR REPLACE", "INSERT OR IGNORE", "ON CONFLICT(identity_digest"} {
		if strings.Contains(evidence, forbidden) {
			t.Errorf("SQLite PASS evidence must fail-fast on identity collision, found %q", forbidden)
		}
	}
	retention := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/ledger_store_sqlite_retention.go"))
	for _, marker := range []string{"requireHistoricallyAcceptedGeneration", "accepted baseline generation authority", "DELETE FROM %s WHERE %s IN", "case cicontract.RemoteRunsTable:"} {
		if !strings.Contains(retention, marker) {
			t.Errorf("retention proof is not anchored in the SQLite authority: missing %q", marker)
		}
	}
}

// TestRemoteCIWorkloadExecutionPlanFieldsAreDynamicallyCovered derives producer
// JSON fields from the Go AST and checks the registered consumer path, avoiding
// a hand-maintained field list that could silently omit new plan identity fields.
func TestRemoteCIWorkloadExecutionPlanFieldsAreDynamicallyCovered(t *testing.T) {
	root := findRepoRoot(t)
	producerPath := filepath.Join(root, "internal/devtools/gate/workload_model.go")
	producerBytes, err := os.ReadFile(producerPath)
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), producerPath, producerBytes, 0)
	if err != nil {
		t.Fatal(err)
	}
	fields := enumerateWorkloadExecutionPlanJSONFields(file)
	if len(fields) == 0 {
		t.Fatal("WorkloadExecutionPlan producer fields could not be enumerated")
	}
	consumer := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/contracts_test.go"))
	if strings.Contains(consumer, "JSONFieldNames(reflect.TypeFor[WorkloadExecutionPlan]())") {
		return
	}
	for _, field := range fields {
		if !strings.Contains(consumer, fmt.Sprintf("%q", field)) {
			t.Errorf("WorkloadExecutionPlan consumer registration is missing dynamically enumerated field %q", field)
		}
	}
}

// enumerateWorkloadExecutionPlanJSONFields 从 producer AST 动态提取计划 JSON 字段。
func enumerateWorkloadExecutionPlanJSONFields(file *ast.File) []string {
	var fields []string
	ast.Inspect(file, func(node ast.Node) bool {
		typeSpec, ok := node.(*ast.TypeSpec)
		if !ok || typeSpec.Name.Name != "WorkloadExecutionPlan" {
			return true
		}
		structType, ok := typeSpec.Type.(*ast.StructType)
		if !ok {
			return false
		}
		fields = append(fields, enumerateStructJSONFields(structType)...)
		return false
	})
	return fields
}

// enumerateStructJSONFields 提取一个结构体字段声明中的 JSON 名称。
func enumerateStructJSONFields(structType *ast.StructType) []string {
	var fields []string
	for _, field := range structType.Fields.List {
		if field.Tag == nil || len(field.Names) == 0 {
			continue
		}
		for part := range strings.SplitSeq(strings.Trim(field.Tag.Value, "`"), " ") {
			if name, ok := parseJSONFieldTag(part); ok {
				fields = append(fields, name)
			}
		}
	}
	return fields
}

// parseJSONFieldTag 解析 struct tag 中的 canonical JSON 名称。
func parseJSONFieldTag(part string) (string, bool) {
	if !strings.HasPrefix(part, "json:\"") {
		return "", false
	}
	name := strings.Trim(strings.TrimPrefix(part, "json:\""), "\"")
	name = strings.Split(name, ",")[0]
	return name, name != "" && name != "-"
}

func TestRemoteCIPlanContractErrorIncludesRetiredVersion(t *testing.T) {
	if err := cicontract.ValidateWorkloadPlanContract(8, cicontract.WorkloadPlanningAlgorithmID, "sha256:"+strings.Repeat("0", 64), "sha256:"+strings.Repeat("0", 64), "sha256:"+strings.Repeat("0", 64), cicontract.WorkloadEstimationPolicyMaterial{}); err == nil || !strings.Contains(fmt.Sprint(err), "accepted schema") {
		t.Fatalf("retired workload plan schema was not rejected with an actionable error: %v", err)
	}
}
