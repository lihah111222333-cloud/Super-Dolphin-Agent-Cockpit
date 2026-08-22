package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

func TestHeterociAcceptedContractDefinesFourNodePostgreSQLBoundary(t *testing.T) {
	root := findRepoRoot(t)
	contract := readRemoteCIContractGuardFile(t, filepath.Join(root, "docs", "契约", "remote-ci-eci-imagecache-contract.md"))
	for _, marker := range []string{
		"状态：Accepted",
		"契约身份：remote-ci-four-node/v1",
		"请求协议：remote-ci-run-request/v1",
		"结果协议：remote-ci-result/v1",
		"事件协议：remote-ci-event/v1",
		"远程权威：remote-ci-postgresql-primary/v1",
		"唯一执行池：四台节点，Ubuntu-01、Ubuntu-02、Windows-01、Windows-02",
		"协调端：macOS 只编排、提交请求、读取状态和展示结果，不执行任何 workload。",
		"heteroci_owner schema 与 heteroci_owner_writer role",
		"heteroci_receipt schema 与 heteroci_receipt_writer role",
		"SERIALIZABLE transaction",
		"canonical owner scope 的 transaction advisory lock",
		"synchronous_commit=remote_apply",
		"external_binding_digest",
		"manifest_blob_sha256",
		"runtime_policy_digest",
		"本地 SQLite、JSON 文件和内存状态都不能成为读写权威",
		"不得双写、交叉提交或互相降级",
		"一次请求只允许一次 fresh snapshot、一次 reservation 和一次 executor binding",
		"request aggregate terminal result",
		"executor_binding_digest、node_set_digest",
		"按 node_id 排序的 shards",
		"CapacityBlocker",
		"不得等待空闲容量",
		"不得自动重试",
		"不得迁移、重试、fallback",
		"第二 PostgreSQL writer",
	} {
		if !strings.Contains(contract, marker) {
			t.Errorf("Accepted four-node contract is missing %q", marker)
		}
	}
	for _, retired := range []string{
		"权威远程 ECI 目标平台：",
		"本地非权威异构拓扑平台：",
		"heteroci-postgresql-local-authorities-protocol/v1",
		"NOT_VERIFIED/not_wired",
		"owner_db_path",
		"receipt_db_path",
	} {
		if strings.Contains(contract, retired) {
			t.Errorf("Accepted four-node contract retains retired marker %q", retired)
		}
	}
}

func TestHeterociFixedNodeAndMacOSExecutionBoundary(t *testing.T) {
	root := findRepoRoot(t)
	manifestPath := filepath.Join(root, "internal", "devtools", "heteroci", "manifest.go")
	manifestSource := readRemoteCIContractGuardFile(t, manifestPath)
	manifestFile := parseRemoteCIContractGuardFile(t, manifestPath)
	for _, marker := range []string{
		"AuthorityLocalNonAuthoritative",
		"ResultStatusNotVerified",
		"ValidateFourNodeExecutionPool",
		"expectedFourNodePlatform",
		"Ubuntu-01",
		"Ubuntu-02",
		"Windows-01",
		"Windows-02",
	} {
		if !strings.Contains(manifestSource, marker) {
			t.Errorf("heteroci manifest is missing fixed-topology marker %q", marker)
		}
	}
	if remoteCIFunctionContainsIdentifier(manifestFile, "expectedFourNodePlatform", "PlatformDarwinAMD64") ||
		remoteCIFunctionContainsIdentifier(manifestFile, "expectedFourNodePlatform", "PlatformDarwinARM64") {
		t.Fatal("heteroci fixed execution pool maps a Darwin platform into the executor pool")
	}
	valid := newHeterogeneousCIManifest(4)
	for index := 2; index < 4; index++ {
		valid.Nodes[index].Platform = heteroci.PlatformWindowsAMD64
		valid.Nodes[index].Capabilities = []heteroci.CapabilityConfig{{Runtime: heteroci.RuntimeWindowsNative}}
	}
	if err := valid.ValidateFourNodeExecutionPool(); err != nil {
		t.Fatalf("ValidateFourNodeExecutionPool(valid): %v", err)
	}
	for _, nodeCount := range []int{1, 3, 5} {
		if err := newHeterogeneousCIManifest(nodeCount).ValidateFourNodeExecutionPool(); err == nil {
			t.Fatalf("ValidateFourNodeExecutionPool(%d nodes) unexpectedly succeeded", nodeCount)
		}
	}
	darwin := cloneHeterogeneousCIManifest(valid)
	darwin.Nodes[0].Platform = heteroci.PlatformDarwinAMD64
	darwin.Nodes[0].Capabilities = []heteroci.CapabilityConfig{{Runtime: heteroci.RuntimeDarwinNative}}
	if err := darwin.ValidateFourNodeExecutionPool(); err == nil {
		t.Fatal("ValidateFourNodeExecutionPool accepted a Darwin executor node")
	}
}

func TestHeterociCapacitySnapshotReservationAndBindingBoundary(t *testing.T) {
	root := findRepoRoot(t)
	contract := readRemoteCIContractGuardFile(t, filepath.Join(root, "docs", "契约", "remote-ci-eci-imagecache-contract.md"))
	for _, marker := range []string{
		"deterministic capacity sharding planner",
		"fresh snapshot、一次 reservation 和一次 executor binding",
		"CapacityBlocker 必须包含稳定 unit_id",
		"required capability、reason code 和可重放的 plan/manifest digest",
		"不得加入隐式队列",
		"不得切换备用节点集合",
		"不得双跑或投机复制",
		"再次执行同一 operation_id",
	} {
		if !strings.Contains(contract, marker) {
			t.Errorf("capacity contract is missing %q", marker)
		}
	}
	sources := map[string][]string{
		"internal/devtools/heteroci/anchor_wave.go": {
			"BuildAnchorWavePlan", "chooseAnchorCandidate", "CapacityBlockerError", "NewCapacityBlockerError", "UnassignedUnitIDs", "sort.Slice",
		},
		"internal/devtools/heteroci/router.go": {
			"BuildRoutingDecision", "SnapshotCapturedAtUnixMilli", "CapacityBlockerError", "NewCapacityBlockerError", "PackingPlanDigest", "ReasonCode",
		},
		"internal/devtools/heteroci/routing_types.go": {
			"CapacityBlockerProjection", "ValidateAgainstPlan", "ManifestDigest", "ReasonCode", "PackingPlanDigest", "func (projection CapacityBlockerProjection) Validate",
		},
		"internal/devtools/heterociworkflow/graph.go": {
			"rejectUnassignedUnits", "NewCapacityBlockerError", "PackingPlanDigest",
		},
		"internal/devtools/heteroci/routing_lease_book.go": {
			"PlanAndReserve", "SourceSnapshot", "EffectiveSnapshot", "commitRoutingLeaseReservation",
		},
		"internal/devtools/heterociowner/postgresql_owner_plan.go": {
			"PlanAndReserve", "request.OperationID", "readPostgreSQLOperation", "planPostgreSQLTopology", "persistPostgreSQLPlan",
		},
	}
	for relative, markers := range sources {
		source := readRemoteCIContractGuardFile(t, filepath.Join(root, filepath.FromSlash(relative)))
		for _, marker := range markers {
			if !strings.Contains(source, marker) {
				t.Errorf("%s is missing capacity/binding marker %q", relative, marker)
			}
		}
	}
}

func TestHeterociPostgreSQLOwnerCoreBoundary(t *testing.T) {
	root := findRepoRoot(t)
	schema := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal", "devtools", "heterociowner", "postgresql_owner_schema.go"))
	owner := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal", "devtools", "heterociowner", "postgresql_owner.go"))
	plan := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal", "devtools", "heterociowner", "postgresql_owner_plan.go"))
	receipt := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal", "devtools", "heterociexec", "postgresql_receipt_schema.go"))
	terminalReceipt := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal", "devtools", "heterociexec", "postgresql_terminal_receipt.go"))
	terminalSchema := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal", "devtools", "heterociexec", "postgresql_terminal_receipt_schema.go"))
	assertHeterociSourceMarkers(t, "PostgreSQL receipt authority", receipt, []string{
		"PostgreSQLReceiptSchema",
		"PostgreSQLReceiptWriterRole",
		"CREATE SCHEMA heteroci_receipt AUTHORIZATION heteroci_receipt_writer",
		"CREATE TABLE heteroci_receipt.batch_receipts",
		"PostgreSQLReceiptStore",
		"sql.LevelSerializable",
		"remote_apply",
	})
	assertHeterociSourceMarkers(t, "PostgreSQL aggregate receipt", terminalReceipt, []string{
		"type PostgreSQLTerminalShard struct",
		"ExecutorBindingDigest",
		"NodeSetDigest",
		"PostgreSQLTerminalNodeSetDigest",
	})
	assertHeterociSourceMarkers(t, "PostgreSQL aggregate receipt schema", terminalSchema, []string{
		"CREATE TABLE heteroci_receipt.terminal_shards",
		"CREATE TABLE heteroci_receipt.receipt_event_shards",
		"executor_binding_digest",
		"node_set_digest",
		"GRANT SELECT, INSERT ON TABLE heteroci_receipt.terminal_shards",
		"GRANT SELECT, INSERT ON TABLE heteroci_receipt.receipt_event_shards",
	})
	assertHeterociSourceMarkers(t, "PostgreSQL owner schema", schema, []string{
		"PostgreSQLOwnerRuntimeSchemaVersion",
		"PostgreSQLOwnerSchema",
		"PostgreSQLOwnerRole",
		"postgresqlOwnerSchemaStatements",
		"REVOKE ALL ON SCHEMA",
		"CREATE TABLE heteroci_owner.operation_journal",
		"external_binding_digest TEXT",
		"manifest_blob_sha256 TEXT",
		"runtime_policy_digest TEXT",
	})
	assertHeterociSourceMarkers(t, "PostgreSQL owner runtime", owner, []string{
		"sql.LevelSerializable",
		"pg_advisory_xact_lock",
		"current_setting('synchronous_commit')",
		"PostgreSQLOwnerRole",
		"remote_apply",
		"ErrPostgreSQLOwnerCommitUncertain",
	})
	assertHeterociSourceMarkers(t, "PostgreSQL plan owner", plan, []string{
		"PlanAndReserve",
		"request.OperationID",
		"readPostgreSQLOperation",
		"planPostgreSQLTopology",
		"persistPostgreSQLPlan",
		"request.ExternalBindingDigest",
		"request.ManifestBlobSHA256",
		"request.RuntimePolicyDigest",
		"validatePostgreSQLPlanRequestAudit",
	})
	assertHeterociNoLocalPostgreSQLAuthority(t, map[string]string{
		"postgresql_owner.go":                   owner,
		"postgresql_owner_plan.go":              plan,
		"postgresql_receipt_schema.go":          receipt,
		"postgresql_terminal_receipt.go":        terminalReceipt,
		"postgresql_terminal_receipt_schema.go": terminalSchema,
	})
}

func TestHeterociExternalBindingFieldsStayOnEveryIdentityBoundary(t *testing.T) {
	root := findRepoRoot(t)
	paths := []string{
		"internal/devtools/heterociworkflow/types.go",
		"internal/devtools/heterociworkflow/executor_binding.go",
		"internal/devtools/heteroci/topology_session.go",
		"internal/devtools/heteroci/executor_binding.go",
		"internal/devtools/remotecicli/execute.go",
		"internal/devtools/heterociowner/postgresql_owner_plan.go",
	}
	for _, relative := range paths {
		source := readRemoteCIContractGuardFile(t, filepath.Join(root, filepath.FromSlash(relative)))
		for _, field := range []string{"ExternalBindingDigest", "ManifestBlobSHA256", "RuntimePolicyDigest"} {
			if !strings.Contains(source, field) {
				t.Errorf("%s is missing strict external binding field %q", relative, field)
			}
		}
	}
}

func TestHeterociEngineProtocolCompositeOwnerGuard(t *testing.T) {
	root := findRepoRoot(t)
	source := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal", "devtools", "remotecicli", "execute.go"))
	for _, marker := range []string{
		"RemoteCIEngineProtocolABIDigest",
		"RemoteCIEngineProtocolABIDigestDomain",
		"remoteCIEngineABIComponents",
		"remote-ci-engine-protocol-abi/v1",
		"reflect.TypeFor[heteroci.ProbeRequest]()",
		"reflect.TypeFor[heterociworkflow.RunRequest]()",
		"reflect.TypeFor[heterocinode.BatchRequest]()",
		"reflect.TypeFor[heterociexec.PostgreSQLReceiptEvent]()",
		"reflect.TypeFor[remoteruntime.RuntimeConfig]()",
		"remoteportable.PortableBindingProtocolABIDigest",
		"protocolDigest, err := RemoteCIEngineProtocolABIDigest()",
	} {
		if !strings.Contains(source, marker) {
			t.Errorf("remote-ci engine ABI composite owner is missing %q", marker)
		}
	}
}

func TestRemoteCIValidationResultBoundaryGuard(t *testing.T) {
	root := findRepoRoot(t)
	portable := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal", "devtools", "remoteportable", "portable_descriptor.go"))
	codec := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal", "devtools", "remoteportable", "codec_helpers.go"))
	abi := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal", "devtools", "remoteportable", "binding_abi.go"))
	cli := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal", "devtools", "remotecicli", "portable_validate.go"))
	contract := readRemoteCIContractGuardFile(t, filepath.Join(root, "docs", "契约", "remote-ci-eci-imagecache-contract.md"))
	assertHeterociSourceMarkers(t, "PortableValidationResult", portable, []string{
		"type PortableValidationResult struct",
		"PortableValidationResultSchema",
		"PortableValidationResultStatusValidated",
		"PortableValidationResultAuthority",
		"EngineFacts           PortableEngineFacts",
	})
	assertHeterociSourceMarkers(t, "PortableValidationResult codec", codec, []string{
		"EncodePortableValidationResult",
		"DecodePortableValidationResult",
		"func (result PortableValidationResult) Validate() error",
		"DisallowUnknownFields",
		"portable validation result is not canonical",
	})
	assertHeterociSourceMarkers(t, "PortableValidationResult ABI", abi, []string{
		"PortableValidationResultSchema",
		"reflect.TypeFor[PortableValidationResult]()",
	})
	assertHeterociSourceMarkers(t, "validate stdout boundary", cli, []string{
		"remoteportable.PortableValidationResult",
		"remoteportable.EncodePortableValidationResult",
		"writeValidationResult",
		"len(data)+1 > outputMaxBytes",
	})
	if strings.Contains(cli, "writePortableValidationEvent") || strings.Contains(cli, "writePortableValidationStages") {
		t.Fatal("validate retains retired lifecycle event output")
	}
	for _, marker := range []string{"validate 的 stderr 必须为空", "未取得 PostgreSQL authority generation 前不得输出 remote-ci-event/v1", "只有 execute terminal 才输出 remote-ci-result/v1"} {
		if !strings.Contains(contract, marker) {
			t.Errorf("remote-ci contract is missing validate boundary %q", marker)
		}
	}
}

// assertHeterociSourceMarkers keeps each production boundary tied to its own source file.
func assertHeterociSourceMarkers(t *testing.T, sourceName, source string, markers []string) {
	t.Helper()
	for _, marker := range markers {
		if !strings.Contains(source, marker) {
			t.Errorf("%s is missing %q", sourceName, marker)
		}
	}
}

// assertHeterociNoLocalPostgreSQLAuthority rejects local fallback or second-writer markers.
func assertHeterociNoLocalPostgreSQLAuthority(t *testing.T, sources map[string]string) {
	t.Helper()
	for sourceName, source := range sources {
		for _, forbidden := range []string{"modernc.org/sqlite", "sql.Open(", "PRAGMA ", "SQLiteFallback", "DualWrite"} {
			if strings.Contains(source, forbidden) {
				t.Errorf("%s retains forbidden PG-path local authority marker %q", sourceName, forbidden)
			}
		}
	}
}

func TestHeterociSingleBindingAndMutationFailFastBoundary(t *testing.T) {
	root := findRepoRoot(t)
	workflow := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal", "devtools", "heterociworkflow", "run.go"))
	types := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal", "devtools", "heterociworkflow", "types.go"))
	registry := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal", "devtools", "heterociexec", "registry.go"))
	command := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal", "devtools", "heterociexec", "command_executor.go"))
	sources := readHeterociMutationGuardSources(t, root)
	assertHeterociSingleBindingGuard(t, workflow, types, registry)
	assertHeterociNoMutationRetryGuard(t, sources)
	assertHeterociNoCommandWaitGuard(t, command)
}

func readHeterociMutationGuardSources(t *testing.T, root string) map[string]string {
	t.Helper()
	paths := []string{"internal/devtools/heterociowner/postgresql_owner.go", "internal/devtools/heterociowner/postgresql_owner_plan.go", "internal/devtools/heterociowner/postgresql_owner_lifecycle.go", "internal/devtools/heterociexec/postgresql_receipt_store.go", "internal/devtools/heterociexec/postgresql_terminal_receipt_store.go"}
	sources := make(map[string]string, len(paths))
	for _, relative := range paths {
		sources[relative] = readRemoteCIContractGuardFile(t, filepath.Join(root, filepath.FromSlash(relative)))
	}
	return sources
}

func assertHeterociSingleBindingGuard(t *testing.T, workflow, types, registry string) {
	t.Helper()
	for _, marker := range []string{"ExecutorBindingPlan", "Bind(plan ExecutorBindingPlan)", "validateBoundExecutors", "sortExecutorBindingPlan"} {
		if !strings.Contains(types+workflow, marker) {
			t.Errorf("single executor binding boundary is missing %q", marker)
		}
	}
	if strings.Count(workflow, "Executors.Bind(") != 1 || strings.Contains(workflow, "Bind(batch.Batch.Key)") || !strings.Contains(registry, "Bind(plan heterociworkflow.ExecutorBindingPlan)") {
		t.Error("executor binding is not a single exact plan call")
	}
}

func assertHeterociNoMutationRetryGuard(t *testing.T, sources map[string]string) {
	t.Helper()
	for name, source := range sources {
		for _, forbidden := range []string{"MutationAttempts", "for range postgresqlOwnerMutationAttempts", "for range postgreSQLReceiptMutationAttempts", "isRetryablePostgreSQLError", "postgreSQLReceiptRetryable", "after retries"} {
			if strings.Contains(source, forbidden) {
				t.Errorf("%s retains automatic mutation retry marker %q", name, forbidden)
			}
		}
	}
}

func assertHeterociNoCommandWaitGuard(t *testing.T, command string) {
	t.Helper()
	for _, forbidden := range []string{"waitExistingExecution", "waitCommandExecution", "waiting for existing batch execution"} {
		if strings.Contains(command, forbidden) {
			t.Errorf("command executor retains implicit queue/wait marker %q", forbidden)
		}
	}
	if !strings.Contains(command, "ErrCommandInFlight") || strings.Count(command, "runner.Run(") != 1 {
		t.Error("command executor does not prove immediate in-flight conflict and one runner call")
	}
	claimAt := strings.Index(command, "executor.claim(")
	readAt := strings.Index(command, "executor.readValidatedReceipt(")
	if claimAt < 0 || readAt < 0 || claimAt > readAt {
		t.Error("command executor performs receipt read before local in-flight claim")
	}
}

func readRemoteCIContractGuardFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func parseRemoteCIContractGuardFile(t *testing.T, path string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse source %s: %v", path, err)
	}
	return file
}

func remoteCIFunctionByName(file *ast.File, want string) *ast.FuncDecl {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == want {
			return function
		}
	}
	return nil
}

func remoteCIFunctionContainsIdentifier(file *ast.File, functionName, want string) bool {
	function := remoteCIFunctionByName(file, functionName)
	if function == nil {
		return false
	}
	found := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if ok && identifier.Name == want {
			found = true
			return false
		}
		return true
	})
	return found
}

func remoteCIStructTypeByName(file *ast.File, typeName string) *ast.StructType {
	var structure *ast.StructType
	ast.Inspect(file, func(node ast.Node) bool {
		typeSpec, ok := node.(*ast.TypeSpec)
		if !ok || typeSpec.Name.Name != typeName {
			return true
		}
		structure, _ = typeSpec.Type.(*ast.StructType)
		return false
	})
	return structure
}

func remoteCIViolationList(violations map[string]bool) []string {
	result := make([]string, 0, len(violations))
	for violation := range violations {
		result = append(result, violation)
	}
	slices.Sort(result)
	return result
}

func relativeRemoteCIContractPath(t *testing.T, root, path string) string {
	t.Helper()
	relative, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.ToSlash(relative)
}

func localPassFunctionHasIdentifier(function *ast.FuncDecl, names []string) bool {
	if function == nil {
		return false
	}
	for _, name := range names {
		found := false
		ast.Inspect(function.Body, func(node ast.Node) bool {
			identifier, ok := node.(*ast.Ident)
			if ok && identifier.Name == name {
				found = true
				return false
			}
			return true
		})
		if found {
			return true
		}
	}
	return false
}
func remoteCICallName(call *ast.CallExpr) string {
	if identifier, ok := call.Fun.(*ast.Ident); ok {
		return identifier.Name
	}
	if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
		return selector.Sel.Name
	}
	return "call"
}

func remoteCIFunctionCalls(file *ast.File, functionName, calledName string) bool {
	function := remoteCIFunctionByName(file, functionName)
	if function == nil {
		return false
	}
	found := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if ok && remoteCICallName(call) == calledName {
			found = true
			return false
		}
		return !found
	})
	return found
}

func remoteCIParseGuardFixture(t *testing.T, source string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "remote_ci_fixture.go", source, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse guard fixture: %v", err)
	}
	return file
}
func enumerateStructJSONFields(structure *ast.StructType) []string {
	if structure == nil {
		return nil
	}
	fields := make([]string, 0)
	for _, field := range structure.Fields.List {
		if field.Tag == nil || len(field.Names) == 0 {
			continue
		}
		for part := range strings.FieldsSeq(strings.Trim(field.Tag.Value, "`")) {
			if !strings.HasPrefix(part, "json:") {
				continue
			}
			_, raw, _ := strings.Cut(part, "json:")
			value, err := strconv.Unquote(raw)
			if err != nil {
				continue
			}
			name, _, _ := strings.Cut(value, ",")
			if name != "" && name != "-" {
				fields = append(fields, name)
			}
		}
	}
	return fields
}
func enumerateWorkloadExecutionPlanJSONFields(file *ast.File) []string {
	return enumerateStructJSONFields(remoteCIStructTypeByName(file, "WorkloadExecutionPlan"))
}

func parseJSONFieldTag(part string) (string, bool) {
	if !strings.HasPrefix(part, "json:") {
		return "", false
	}
	_, raw, _ := strings.Cut(part, "json:")
	value, err := strconv.Unquote(raw)
	if err != nil {
		return "", false
	}
	name, _, _ := strings.Cut(value, ",")
	return name, name != "" && name != "-"
}

func remoteCIProductionFiles(t *testing.T, root string) []string {
	t.Helper()
	paths := make([]string, 0)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(paths)
	return paths
}
func remoteCIForbiddenIdentifiers(file *ast.File) map[string]bool {
	identifiers := make(map[string]bool)
	ast.Inspect(file, func(node ast.Node) bool {
		if identifier, ok := node.(*ast.Ident); ok {
			identifiers[identifier.Name] = true
		}
		return true
	})
	return identifiers
}

func remoteCIStringLiterals(file *ast.File) []string {
	literals := make([]string, 0)
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(literal.Value)
		if err == nil {
			literals = append(literals, value)
		}
		return true
	})
	return literals
}

func remoteCIUnregisteredSQLSchemaTables(source string) []string {
	const marker = "ci_unregistered_"
	values := make([]string, 0)
	for _, token := range strings.FieldsFunc(source, func(r rune) bool { return r == ' ' || r == '(' || r == ')' || r == '"' || r == '\\' }) {
		if strings.HasPrefix(token, marker) {
			values = append(values, token)
		}
	}
	slices.Sort(values)
	return slices.Compact(values)
}
