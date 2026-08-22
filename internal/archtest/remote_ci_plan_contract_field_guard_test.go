package archtest

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// TestRemoteCIPlanContractFieldChainGuard keeps the plan wire schema dynamic at
// the producer boundary while requiring every downstream authority to bind the
// plan digest. The plan fields are not copied into PASS identity: SQLite and
// worker report consumers bind the canonical plan through plan_digest, while
// receipt finalization binds its check set to that same run record.
func TestRemoteCIPlanContractFieldChainGuard(t *testing.T) {
	root := findRepoRoot(t)
	producerPath := root + "/internal/devtools/gate/workload_model.go"
	producer := parseRemoteCIContractGuardFile(t, producerPath)
	astFields := enumerateWorkloadExecutionPlanJSONFields(producer)
	if len(astFields) == 0 {
		t.Fatal("WorkloadExecutionPlan has no producer JSON fields")
	}
	reflectionFields, err := wireDTOMapperJSONFields(reflect.TypeFor[gate.WorkloadExecutionPlan]())
	if err != nil {
		t.Fatalf("reflect WorkloadExecutionPlan fields: %v", err)
	}
	assertRemoteCIPlanFieldSetsEqual(t, astFields, reflectionFields)
	assertRemoteCIPlanJSONMarshalFields(t, astFields)
	assertRemoteCIPackingEvidenceFieldUsage(t, root, producer)

	consumer := readRemoteCIContractGuardFile(t, root+"/internal/devtools/gate/contracts_test.go")
	if !strings.Contains(consumer, "JSONFieldNames(reflect.TypeFor[WorkloadExecutionPlan]())") {
		t.Fatal("WorkloadExecutionPlan field registration must derive fields dynamically from reflection")
	}
	assertRemoteCIPlanContractConsumerMatrix(t, root)
}

// assertRemoteCIPackingEvidenceFieldUsage derives evidence fields from the
// producer AST and checks that the validation chain reads every field.
func assertRemoteCIPackingEvidenceFieldUsage(t *testing.T, root string, producer *ast.File) {
	t.Helper()
	structure := remoteCIStructTypeByName(producer, "WorkloadPackingEvidence")
	if structure == nil {
		t.Fatal("WorkloadPackingEvidence producer type is missing")
	}
	validation := parseRemoteCIContractGuardFile(t, root+"/internal/devtools/gate/workload_packing_evidence_validation.go")
	selectors := remoteCISelectorNameSet(validation)
	for _, field := range structure.Fields.List {
		for _, name := range field.Names {
			if _, ok := selectors[name.Name]; !ok {
				t.Errorf("packing evidence field %s is not read by the validation chain", name.Name)
			}
		}
	}
}

// assertRemoteCIPlanFieldSetsEqual 拒绝 producer AST 与 Go reflection 对 JSON 字段的分叉。
func assertRemoteCIPlanFieldSetsEqual(t *testing.T, astFields []string, reflected []wireDTOJSONField) {
	t.Helper()
	astSet := make(map[string]struct{}, len(astFields))
	for _, field := range astFields {
		if _, duplicate := astSet[field]; duplicate {
			t.Fatalf("WorkloadExecutionPlan producer JSON field %q is duplicated", field)
		}
		astSet[field] = struct{}{}
	}
	reflectionSet := make(map[string]struct{}, len(reflected))
	for _, field := range reflected {
		reflectionSet[field.jsonName] = struct{}{}
	}
	if len(astSet) != len(reflectionSet) {
		t.Fatalf("WorkloadExecutionPlan AST/reflection field count differs: ast=%d reflection=%d", len(astSet), len(reflectionSet))
	}
	for field := range astSet {
		if _, ok := reflectionSet[field]; !ok {
			t.Errorf("WorkloadExecutionPlan reflection is missing AST field %q", field)
		}
	}
	for field := range reflectionSet {
		if _, ok := astSet[field]; !ok {
			t.Errorf("WorkloadExecutionPlan AST is missing reflection field %q", field)
		}
	}
}

// assertRemoteCIPlanJSONMarshalFields 证明零值计划仍输出全部必需的 wire 字段。
func assertRemoteCIPlanJSONMarshalFields(t *testing.T, fields []string) {
	t.Helper()
	encoded, err := json.Marshal(gate.WorkloadExecutionPlan{})
	if err != nil {
		t.Fatalf("marshal zero WorkloadExecutionPlan: %v", err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatalf("decode zero WorkloadExecutionPlan: %v", err)
	}
	for _, field := range fields {
		if _, ok := object[field]; !ok {
			t.Errorf("WorkloadExecutionPlan JSON marshal omitted required field %q", field)
		}
	}
}

// assertRemoteCIPlanContractConsumerMatrix 绑定计划校验、packing proof、请求、报告、SQLite 与 receipt 边界。
func assertRemoteCIPlanContractConsumerMatrix(t *testing.T, root string) {
	t.Helper()
	rows := []struct {
		name     string
		relative string
		markers  []string
	}{
		{
			name:     "plan producer owner",
			relative: "internal/devtools/gate/workload_model.go",
			markers: []string{
				"cicontract.WorkloadPlanningAlgorithmID",
				"PlanningPolicyDigest",
				"EstimationPolicyDigest",
				"workloadEstimationPolicyMaterial",
			},
		},
		{
			name:     "plan validator and packing evidence",
			relative: "internal/devtools/gate/workload_planning.go",
			markers: []string{
				"cicontract.ValidateWorkloadPlanContract",
				"validateWorkloadPackingEvidence",
				"plan.digest()",
			},
		},
		{
			name:     "SQLite run projection",
			relative: "internal/devtools/gate/ci_query_store_remote_run_write.go",
			markers:  []string{"plan_digest", "catalog_digest", "source_tree_sha"},
		},
		{
			name:     "SQLite PASS identity projection",
			relative: "internal/devtools/gate/workload_pass_evidence.go",
			markers:  []string{"identity_digest", "execution_digest", "input_digest", "environment_digest", "origin_source_tree_sha"},
		},
		{
			name:     "receipt authority",
			relative: "internal/devtools/gate/remote_ci_authority_records.go",
			markers:  []string{"CandidateTreeSHA", "CheckReceiptSHA256"},
		},
		{
			name:     "receipt finalizer plan binding",
			relative: "internal/devtools/gate/remote_ci_authority_finalize.go",
			markers:  []string{"PlanDigest", "validatePassingCheckReceiptCollection"},
		},
	}
	for _, row := range rows {
		source := readRemoteCIContractGuardFile(t, root+"/"+row.relative)
		for _, marker := range row.markers {
			if !strings.Contains(source, marker) {
				t.Errorf("%s is missing consumer marker %q", row.name, marker)
			}
		}
	}
}

// TestRemoteCIStrictPlanAndProtocolDecoderGuard locks strict decoding and the
// accepted 14/1/1/15/2 version matrix at every production JSON boundary.
func TestRemoteCIStrictPlanAndProtocolDecoderGuard(t *testing.T) {
	root := findRepoRoot(t)
	rows := []struct {
		name, relative, function, decoder string
	}{
		{"workload plan clone", "internal/devtools/gate/workload_container_shards.go", "cloneWorkloadExecutionPlan", "decodeStrictJSON"},
		{"worker manifest", "internal/devtools/gate/executor_shard_manifest.go", "LoadShardExecutionManifest", "decodeStrictJSON"},
	}
	for _, row := range rows {
		file := parseRemoteCIContractGuardFile(t, root+"/"+row.relative)
		function := remoteCIFunctionByName(file, row.function)
		if function == nil {
			t.Fatalf("%s decoder %s is missing", row.name, row.function)
		}
		if !remoteCIFunctionCalls(file, row.function, row.decoder) {
			t.Errorf("%s decoder %s must call strict decoder %s", row.name, row.function, row.decoder)
		}
		if remoteCIFunctionCalls(file, row.function, "Unmarshal") {
			t.Errorf("%s decoder %s retains a permissive json.Unmarshal path", row.name, row.function)
		}
	}
	assertRemoteCIProtocolVersionAliases(t, root)
}

// assertRemoteCIProtocolVersionAliases 锁定协议版本只能由 cicontract 提供，禁止旧版本或协商回退。
func assertRemoteCIProtocolVersionAliases(t *testing.T, root string) {
	t.Helper()
	markers := map[string][]string{
		"internal/devtools/gate/executor_shard_manifest.go": {
			"ShardExecutionManifestSchemaVersion uint32 = cicontract.ShardExecutionManifestSchemaVersion",
		},
	}
	for relative, required := range markers {
		source := readRemoteCIContractGuardFile(t, root+"/"+relative)
		for _, marker := range required {
			if !strings.Contains(source, marker) {
				t.Errorf("%s is missing strict version owner marker %q", relative, marker)
			}
		}
		for _, retired := range []string{"schema_version = 8", "schema_version = 9", "v2\"", "v9\""} {
			if strings.Contains(source, retired) {
				t.Errorf("%s retains retired protocol marker %q", relative, retired)
			}
		}
	}
}

// TestRemoteCIEnvironmentV10AndDynamicFieldsGuard keeps retained local Gate
// environment replay identity bound to the cicontract owner.
func TestRemoteCIEnvironmentV10AndDynamicFieldsGuard(t *testing.T) {
	root := findRepoRoot(t)
	source := readRemoteCIContractGuardFile(t, root+"/internal/devtools/gate/workload_pass_environment_replay.go")
	for _, marker := range []string{"WorkloadPassEnvironmentReplayDomain", "SHA256"} {
		if !strings.Contains(source, marker) {
			t.Errorf("local environment replay is missing canonical marker %q", marker)
		}
	}
	contract := readRemoteCIContractGuardFile(t, root+"/internal/devtools/cicontract/local_pass_contract.go")
	if !strings.Contains(contract, "LocalWorkloadPassEnvironmentSchemaVersion") {
		t.Error("local environment replay is missing cicontract schema owner")
	}
	if err := cicontract.ValidateWorkloadPassEnvironmentSchema(cicontract.WorkloadPassEnvironmentSchemaVersion); err != nil {
		t.Fatalf("canonical PASS environment schema rejected: %v", err)
	}
	if err := cicontract.ValidateWorkloadPassEnvironmentSchema("remote-workload-pass-environment/v9"); err == nil {
		t.Fatal("retired PASS environment v9 was accepted")
	}
}

// TestRemoteCIPassIdentityTreeExclusionGuard separates correctness identity
// from source-tree/run-audit provenance and rejects identity drift by AST.
func TestRemoteCIPassIdentityTreeExclusionGuard(t *testing.T) {
	root := findRepoRoot(t)
	evidencePath := root + "/internal/devtools/gate/workload_pass_evidence.go"
	evidenceFile := parseRemoteCIContractGuardFile(t, evidencePath)
	assertRemoteCIPassIdentityPayloadExcludesTree(t, evidenceFile)
	assertRemoteCIPassIdentityHasherExcludesTree(t, evidenceFile)
	assertRemoteCIPassEvidenceHasherIncludesTree(t, evidenceFile)
	assertRemoteCIPassReplayIncludesTree(t, root)
	assertRemoteCIRunAuditTreeFields(t, root)
}

func assertRemoteCIPassIdentityPayloadExcludesTree(t *testing.T, file *ast.File) {
	t.Helper()
	for _, field := range remoteCIJSONFieldNames(file, "workloadPassIdentityPayload") {
		lower := strings.ToLower(field)
		if strings.Contains(lower, "tree") || strings.Contains(lower, "commit") || strings.Contains(lower, "worktree") {
			t.Errorf("PASS identity payload includes run-audit source field %q", field)
		}
	}
}

func assertRemoteCIPassIdentityHasherExcludesTree(t *testing.T, file *ast.File) {
	t.Helper()
	function := remoteCIFunctionByName(file, "WorkloadPassIdentitySHA256")
	if function == nil {
		t.Fatal("WorkloadPassIdentitySHA256 is missing")
	}
	if !remoteCIPlanFunctionHasSelector(function, "WorkloadPassIdentityDomain") {
		t.Fatal("WorkloadPassIdentitySHA256 must use cicontract.WorkloadPassIdentityDomain")
	}
	for _, forbidden := range []string{"SourceTreeSHA", "CandidateTreeSHA", "Commit", "Worktree"} {
		if remoteCIPlanFunctionHasSelector(function, forbidden) {
			t.Errorf("PASS identity hash includes run-audit selector %q", forbidden)
		}
	}
}

func assertRemoteCIPassEvidenceHasherIncludesTree(t *testing.T, file *ast.File) {
	t.Helper()
	function := remoteCIFunctionByName(file, "WorkloadPassEvidenceSHA256")
	if function == nil || !remoteCIPlanFunctionHasSelector(function, "OriginSourceTreeSHA") {
		t.Fatal("PASS evidence hash must retain origin source tree for provenance")
	}
}

func assertRemoteCIPassReplayIncludesTree(t *testing.T, root string) {
	t.Helper()
	file := parseRemoteCIContractGuardFile(t, root+"/internal/devtools/gate/workload_pass_source_replay.go")
	if !remoteCIJSONFieldSetContains(remoteCIJSONFieldNames(file, "workloadPassSourceReplayPayload"), "source_tree_sha") {
		t.Fatal("source replay proof must retain source tree provenance")
	}
}

// assertRemoteCIRunAuditTreeFields ensures tree/commit identity exists in the
// run/report authority while remaining absent from PASS identity payloads.
func assertRemoteCIRunAuditTreeFields(t *testing.T, root string) {
	t.Helper()
	for _, row := range []struct {
		name, relative, typeName, field string
	}{
		{"run record", "internal/devtools/gate/ci_query_store.go", "RemoteCIRunRecord", "SourceTreeSHA"},
		{"check receipt", "internal/devtools/gate/remote_ci_authority_records.go", "CheckReceiptRecord", "CandidateTreeSHA"},
	} {
		file := parseRemoteCIContractGuardFile(t, root+"/"+row.relative)
		if !remoteCITypeHasField(file, row.typeName, row.field) {
			t.Errorf("%s %s must retain %s as run-audit provenance", row.name, row.typeName, row.field)
		}
	}
	for relative, marker := range map[string]string{
		"internal/devtools/gate/ci_query_store_remote_run_write.go": "source_tree_sha",
		"internal/devtools/gate/remote_ci_authority_records.go":     "CandidateTreeSHA",
	} {
		if !strings.Contains(readRemoteCIContractGuardFile(t, root+"/"+relative), marker) {
			t.Errorf("run-audit persistence %s is missing %q", relative, marker)
		}
	}
}

// TestRemoteCIPlanContractFieldGuardRejectsCounterexamples proves the guard
// catches manual field arrays, permissive decoders and tree-bearing identities.
func TestRemoteCIPlanContractFieldGuardRejectsCounterexamples(t *testing.T) {
	manual := parseRemoteCIContractSource(t, `package gate
import "reflect"
func register() []string { return []string{"schema_version"} }
var _ = reflect.TypeFor[int]
`)
	if fields := remoteCIJSONFieldNames(manual, "WorkloadExecutionPlan"); len(fields) != 0 {
		t.Fatal("manual field-array counterexample unexpectedly exposed a producer type")
	}
	permissive := parseRemoteCIContractSource(t, `package gate
import "encoding/json"
func DecodeShardRequest(data []byte) error { var value any; return json.Unmarshal(data, &value) }
`)
	if remoteCIFunctionCalls(permissive, "DecodeShardRequest", "DecodeStrictJSON") || remoteCIFunctionCalls(permissive, "DecodeShardRequest", "DisallowUnknownFields") {
		t.Fatal("permissive decoder counterexample was misclassified as strict")
	}
	identity := parseRemoteCIContractSource(t, `package gate
type workloadPassIdentityPayload struct { SourceTreeSHA string `+"`json:\"source_tree_sha\"`"+` }
`)
	fields := remoteCIJSONFieldNames(identity, "workloadPassIdentityPayload")
	if !remoteCIJSONFieldSetContains(fields, "source_tree_sha") {
		t.Fatal("tree-bearing identity counterexample was not observable")
	}
}

// remoteCIJSONFieldNames 枚举指定 AST struct 的 JSON tag，不依赖人工字段数组。
func remoteCIJSONFieldNames(file *ast.File, typeName string) []string {
	structure := remoteCIStructTypeByName(file, typeName)
	if structure == nil {
		return nil
	}
	fields := enumerateStructJSONFields(structure)
	sort.Strings(fields)
	return fields
}

// remoteCITypeHasField checks a Go field name on an AST type.
func remoteCITypeHasField(file *ast.File, typeName, fieldName string) bool {
	structure := remoteCIStructTypeByName(file, typeName)
	if structure == nil {
		return false
	}
	for _, field := range structure.Fields.List {
		for _, name := range field.Names {
			if name.Name == fieldName {
				return true
			}
		}
	}
	return false
}

func remoteCISelectorNameSet(file *ast.File) map[string]struct{} {
	selectors := make(map[string]struct{})
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if ok && selector.Sel != nil {
			selectors[selector.Sel.Name] = struct{}{}
		}
		return true
	})
	return selectors
}

func remoteCIPlanFunctionHasSelector(function *ast.FuncDecl, name string) bool {
	if function == nil {
		return false
	}
	found := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if ok && selector.Sel != nil && selector.Sel.Name == name {
			found = true
			return false
		}
		return !found
	})
	return found
}

func remoteCIJSONFieldSetContains(fields []string, want string) bool {
	return slices.Contains(fields, want)
}

// parseRemoteCIContractSource returns a synthetic AST used only for guard red tests.
func parseRemoteCIContractSource(t *testing.T, source string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "remote-ci-counterexample.go", source, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse counterexample: %v", err)
	}
	return file
}
