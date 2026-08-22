package remotecicli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociworkflow"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteportable"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteruntime"
)

// TestValidateRequiresExplicitDescriptor 锁定 validate 的显式绝对 descriptor 边界。
func TestValidateRequiresExplicitDescriptor(t *testing.T) {
	t.Parallel()

	descriptorPath := filepath.Join(t.TempDir(), "descriptor.json")
	if err := os.WriteFile(descriptorPath, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	nonCleanPath := filepath.Dir(descriptorPath) + string(os.PathSeparator) + "." + string(os.PathSeparator) + "descriptor.json"
	cases := []struct {
		name string
		args []string
	}{
		{name: "missing command", args: nil},
		{name: "missing descriptor flag", args: []string{"validate"}},
		{name: "missing descriptor value", args: []string{"validate", "--descriptor"}},
		{name: "relative descriptor", args: []string{"validate", "--descriptor", "descriptor.json"}},
		{name: "non-clean descriptor", args: []string{"validate", "--descriptor", nonCleanPath}},
		{name: "extra argument", args: []string{"validate", "--descriptor", descriptorPath, "unexpected"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, _ := executeCLI(tc.args)
			if code == 0 {
				t.Fatalf("Run(%q) unexpectedly succeeded", tc.args)
			}
			if stdout != "" {
				t.Fatalf("Run(%q) wrote stdout on rejection: %q", tc.args, stdout)
			}
		})
	}
}

// TestValidateRejectsSymlinkDescriptor 确认 descriptor 文件自身不能是符号链接。
func TestValidateRejectsSymlinkDescriptor(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "target.json")
	link := filepath.Join(root, "descriptor.json")
	if err := os.WriteFile(target, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	code, stdout, stderr := executeCLI([]string{"validate", "--descriptor", link})
	if code == 0 {
		t.Fatal("validate accepted a symbolic-link descriptor")
	}
	if stdout != "" {
		t.Fatalf("validate wrote stdout for a symbolic-link descriptor: %q", stdout)
	}
	if strings.Contains(stderr, string(mustReadFile(t, target))) {
		t.Fatalf("diagnostic leaked descriptor contents: %q", stderr)
	}
}

// TestValidateRejectsDirectoryDescriptor 确认 descriptor 必须是 regular file。
func TestValidateRejectsDirectoryDescriptor(t *testing.T) {
	t.Parallel()

	code, stdout, _ := executeCLI([]string{"validate", "--descriptor", t.TempDir()})
	if code == 0 || stdout != "" {
		t.Fatalf("directory descriptor result = code %d stdout %q", code, stdout)
	}
}

// TestValidateBindsExactRepositoryObjects 通过真实 Git ODB 验证独立 composition root 的成功路径。
func TestValidateBindsExactRepositoryObjects(t *testing.T) {
	t.Parallel()

	descriptorPath := portableCLIRealDescriptor(t)
	code, stdout, stderr := executeCLI([]string{"validate", "--descriptor", descriptorPath, "--repository", filepath.Dir(descriptorPath)})
	if code != 0 {
		t.Fatalf("validate result = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	assertPortableValidationOutput(t, stdout)
	assertPortableProgressStages(t, stderr)
}

func assertPortableValidationOutput(t *testing.T, stdout string) {
	t.Helper()
	for _, required := range []string{`"schema":"remote-ci-validation-result/v1"`, `"binding_sha256":"sha256:`, `"contract_id":"remote-ci-four-node/v1"`, `"authority":"LOCAL_NON_AUTHORITATIVE"`, `"execution_pool_contract":"remote-ci-four-node/v1"`, `"repository_id":"fixture"`, `"status":"validated"`} {
		if !strings.Contains(stdout, required) {
			t.Fatalf("validation output omitted %q: %q", required, stdout)
		}
	}
	for _, forbidden := range []string{`"schema":"remote-ci-result/v1"`, `"node_id"`, "repository_root", "config_path", "ledger_path"} {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("validation output leaked or claimed %q: %q", forbidden, stdout)
		}
	}
}

func TestValidateOutputDeclaresMacOSNonExecutor(t *testing.T) {
	descriptorPath := portableCLIRealDescriptor(t)
	code, stdout, _ := executeCLI([]string{"validate", "--descriptor", descriptorPath, "--repository", filepath.Dir(descriptorPath)})
	if code != 0 || !strings.Contains(stdout, `"authority":"LOCAL_NON_AUTHORITATIVE"`) || strings.Contains(stdout, `"node_id"`) || strings.Contains(stdout, `"schema":"remote-ci-result/v1"`) {
		t.Fatalf("validate output incorrectly claims execution: code=%d stdout=%q", code, stdout)
	}
}

// TestPrepareExecuteInputsBindsExactFourNodeRunWithoutNetwork 验证 execute 的纯离线 preflight。
func TestPrepareExecuteInputsBindsExactFourNodeRunWithoutNetwork(t *testing.T) {
	descriptorPath := portableCLIRealDescriptor(t)
	repositoryRoot := filepath.Dir(descriptorPath)
	runtimePath, topology := writeExecuteRuntimeFixture(t, repositoryRoot)
	prepared, err := prepareExecuteInputs(context.Background(), []string{"--request", descriptorPath, "--repository", repositoryRoot, "--runtime-config", runtimePath})
	if err != nil {
		t.Fatalf("prepareExecuteInputs() error = %v", err)
	}
	assertPreparedExecuteIdentity(t, prepared)
	assertPreparedExecutePlatforms(t, prepared)
	assertPreparedExecuteRequestDigest(t, prepared)
	assertExecuteTopologyRejectsDrift(t, prepared.Runtime, topology)
}

func assertPreparedExecuteIdentity(t *testing.T, prepared executePreparedInputs) {
	t.Helper()
	if prepared.Portable.RepositoryID != "fixture" || prepared.RunRequest.RepositoryID != "fixture" || prepared.RunRequest.RunID != strings.Repeat("a", 64) {
		t.Fatalf("execute target identity drifted: portable=%#v request=%#v", prepared.Portable, prepared.RunRequest)
	}
	if prepared.Runtime.HeartbeatTTLMS != 30_000 || prepared.RunRequest.HeartbeatTTL != 30*time.Second || len(prepared.Topology.Nodes) != 4 {
		t.Fatalf("execute runtime binding drifted: runtime=%#v topology=%#v", prepared.Runtime, prepared.Topology)
	}
	protocolDigest, err := RemoteCIEngineProtocolABIDigest()
	if err != nil || prepared.Portable.EngineFacts.ProtocolABIDigest != protocolDigest {
		t.Fatalf("execute engine protocol ABI drifted: got=%q want=%q err=%v", prepared.Portable.EngineFacts.ProtocolABIDigest, protocolDigest, err)
	}
}

func assertPreparedExecutePlatforms(t *testing.T, prepared executePreparedInputs) {
	t.Helper()
	platforms := map[heteroci.Platform]int{}
	for _, unit := range prepared.RunRequest.Units {
		platforms[unit.TargetPlatform]++
	}
	if platforms[heteroci.PlatformLinuxAMD64] == 0 || platforms[heteroci.PlatformWindowsAMD64] == 0 {
		t.Fatalf("execute catalog omitted a platform: %v", platforms)
	}
}

func assertPreparedExecuteRequestDigest(t *testing.T, prepared executePreparedInputs) {
	t.Helper()
	wantDigest, err := heterociworkflow.DeriveRunRequestDigest(prepared.RunRequest)
	if err != nil || wantDigest != prepared.RunRequest.RequestDigest {
		t.Fatalf("execute request digest = %q, %v; want %q", prepared.RunRequest.RequestDigest, err, wantDigest)
	}
}

func assertExecuteTopologyRejectsDrift(t *testing.T, runtimeConfig remoteruntime.RuntimeConfig, topology heteroci.Manifest) {
	t.Helper()
	drifted := topology
	drifted.Nodes = append([]heteroci.NodeConfig(nil), topology.Nodes...)
	drifted.Nodes[0].Endpoint = "ssh://drifted.invalid"
	if err := validateExecuteRuntimeTopology(runtimeConfig, drifted); err == nil {
		t.Fatal("validateExecuteRuntimeTopology() accepted endpoint drift")
	}
}

func writeExecuteRuntimeFixture(t *testing.T, root string) (string, heteroci.Manifest) {
	t.Helper()
	topology := executeTopologyFixture()
	topologyBytes, err := json.Marshal(topology)
	if err != nil {
		t.Fatal(err)
	}
	topologyPath := filepath.Join(root, "topology.json")
	mustWriteTestFile(t, topologyPath, topologyBytes)
	config := executeRuntimeFixture(topologyPath, topology)
	runtimeBytes, err := remoteruntime.EncodeConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	runtimePath := filepath.Join(root, "runtime.json")
	mustWriteTestFile(t, runtimePath, runtimeBytes)
	return runtimePath, topology
}

func executeTopologyFixture() heteroci.Manifest {
	manifest := heteroci.Manifest{SchemaVersion: heteroci.ManifestSchemaVersion, Authority: heteroci.AuthorityLocalNonAuthoritative}
	for index, nodeID := range []string{remoteruntime.Ubuntu01NodeID, remoteruntime.Ubuntu02NodeID, remoteruntime.Windows01NodeID, remoteruntime.Windows02NodeID} {
		platform := heteroci.Platform(heteroci.PlatformLinuxAMD64)
		capability := heteroci.CapabilityConfig{Runtime: heteroci.RuntimeUbuntuContainer, Image: "fixture/ubuntu", ImageDigest: digestBytes([]byte("fixture-image"))}
		if index >= 2 {
			platform = heteroci.Platform(heteroci.PlatformWindowsAMD64)
			capability = heteroci.CapabilityConfig{Runtime: heteroci.RuntimeWindowsNative}
		}
		manifest.Nodes = append(manifest.Nodes, heteroci.NodeConfig{
			NodeID: nodeID, Endpoint: "ssh://" + strings.ToLower(nodeID), ProbeIdentity: digestBytes([]byte(nodeID + "-probe")),
			Platform: platform, Capabilities: []heteroci.CapabilityConfig{capability},
		})
	}
	return manifest
}

func executeRuntimeFixture(topologyPath string, topology heteroci.Manifest) remoteruntime.RuntimeConfig {
	config := remoteruntime.RuntimeConfig{
		SchemaVersion: remoteruntime.ConfigSchemaVersion, TopologyManifestPath: topologyPath,
		OwnerDSNEnv: "REMOTE_CI_OWNER_DSN", ReceiptDSNEnv: "REMOTE_CI_RECEIPT_DSN", OwnerNamespace: "remote-ci-fixture", SSHExecutable: "/usr/bin/ssh",
		ProbeTimeoutMS: 1_700, HeartbeatTTLMS: 30_000, RequestTimeoutMS: 180_000, RunTimeoutMS: 45_000, CleanupTimeoutMS: 3_100, WorkerTimeoutMS: 90_000,
	}
	for index, node := range topology.Nodes {
		config.Nodes = append(config.Nodes, executeRuntimeNode(node, index))
	}
	return config
}

func executeRuntimeNode(node heteroci.NodeConfig, index int) remoteruntime.NodeBinding {
	probeIdentity := node.ProbeIdentity
	if node.Platform == heteroci.PlatformLinuxAMD64 {
		privateRoot := "/srv/ci/u0" + strconv.Itoa(index+1)
		return remoteruntime.NodeBinding{
			NodeID: node.NodeID, Platform: remoteruntime.UbuntuPlatform, Endpoint: node.Endpoint, CGOEnabled: "0",
			ProbeExecutable: "/opt/super-dolphin/probe", ProbeConfigPath: "/etc/super-dolphin/probe.json", ProbeIdentity: probeIdentity,
			RunnerExecutable: "/opt/super-dolphin/linux_runner_daemon.sh", RunnerIdentity: digestBytes([]byte(node.NodeID + "-runner")), WorkerExecutable: "/opt/super-dolphin/worker", WorkerIdentity: digestBytes([]byte(node.NodeID + "-worker")),
			RepositoryRoot: privateRoot + "/worktree", ObjectStoreRoot: "/srv/ci/shared/object-store", WorktreeRoot: privateRoot + "/worktree", CachePublicationRoot: "/srv/ci/shared/cache-publication", PrivateOverlayRoot: privateRoot + "/overlay", TempRoot: privateRoot + "/temp",
			GoExecutable: "/usr/local/go/bin/go", ToolchainIdentity: digestBytes([]byte("linux-toolchain")),
			Linux: &remoteruntime.LinuxContainerBinding{DockerExecutable: "/usr/bin/docker", DockerImage: "fixture/ubuntu", DockerImageIdentity: digestBytes([]byte("fixture-image")), CgroupRoot: "/sys/fs/cgroup/super-dolphin", RunAsUser: "ci", DockerGroup: "docker"},
		}
	}
	privateRoot := "C:/ci/w0" + strconv.Itoa(index-1)
	return remoteruntime.NodeBinding{
		NodeID: node.NodeID, Platform: remoteruntime.WindowsPlatform, Endpoint: node.Endpoint, CGOEnabled: "0",
		ProbeExecutable: "C:/super-dolphin/probe.exe", ProbeConfigPath: "C:/super-dolphin/probe.json", ProbeIdentity: probeIdentity,
		RunnerExecutable: "C:/super-dolphin/windows_runner.ps1", RunnerIdentity: digestBytes([]byte(node.NodeID + "-runner")), WorkerExecutable: "C:/super-dolphin/worker.exe", WorkerIdentity: digestBytes([]byte(node.NodeID + "-worker")),
		RepositoryRoot: privateRoot + "/worktree", ObjectStoreRoot: privateRoot + "/objects", WorktreeRoot: privateRoot + "/worktree", CachePublicationRoot: privateRoot + "/cache-publication", PrivateOverlayRoot: privateRoot + "/overlay", TempRoot: privateRoot + "/temp",
		GoExecutable: "C:/Go/bin/go.exe", ToolchainIdentity: digestBytes([]byte("windows-toolchain")),
	}
}

// portableCLIRealDescriptor 建立真实 Git ODB 与 authority 文件并返回 canonical descriptor。
func portableCLIRealDescriptor(t *testing.T) string {
	t.Helper()
	repository := t.TempDir()
	runGitFixture(t, repository, "init")
	runGitFixture(t, repository, "config", "user.email", "portable@example.invalid")
	runGitFixture(t, repository, "config", "user.name", "Portable Test")
	manifestPath := filepath.Join(repository, "ci", "workloads.json")
	mustMkdirAll(t, filepath.Dir(manifestPath))
	manifest := portableCLIManifest(t)
	mustWriteTestFile(t, manifestPath, manifest)
	mustWriteTestFile(t, filepath.Join(repository, "go.mod"), []byte("module example.invalid/fixture\n\ngo 1.26.0\n"))
	mustMkdirAll(t, filepath.Join(repository, "example"))
	mustWriteTestFile(t, filepath.Join(repository, "example", "example_test.go"), []byte("package example\n\nimport \"testing\"\n\nfunc TestExample(t *testing.T) {}\n"))
	runGitFixture(t, repository, "add", ".")
	runGitFixture(t, repository, "commit", "-m", "测试 portable manifest")
	commit := runGitFixture(t, repository, "rev-parse", "HEAD")
	tree := runGitFixture(t, repository, "rev-parse", "HEAD^{tree}")

	input := portableCLIFixtureInput(t, repository, commit, tree, manifest)
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	descriptorPath := filepath.Join(repository, "descriptor.json")
	mustWriteTestFile(t, descriptorPath, raw)
	resolvedRepository, err := filepath.EvalSymlinks(repository)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(resolvedRepository, "descriptor.json")
}

// portableCLIManifest 生成 target tree 所拥有的 top-level portable manifest。
func portableCLIManifest(t *testing.T) []byte {
	t.Helper()
	digest := digestBytes([]byte("portable-fixture"))
	engineFacts, err := currentPortableEngineFacts()
	if err != nil {
		t.Fatal(err)
	}
	workload := remoteportable.PortableManifestWorkload{UnitID: "go-test/example", CommandDigest: digest, TargetPlatform: string(remoteportable.RepositoryWorkloadPlatformLinuxAMD64), RuntimeCapability: string(remoteportable.RepositoryWorkloadRuntimeUbuntuContainer), CPUMilli: 1_000, MemoryBytes: 1 << 30, HistoricalEstimateMS: 1_000, Kind: remoteportable.RepositoryWorkloadKindGoTest, ModuleRoot: ".", Package: "./example", Name: "TestExample", ExecutionPolicy: string(remoteportable.RepositoryWorkloadExecutionPolicyNormal)}
	workload.ExecutionDigest, err = remoteportable.DerivePortableWorkloadExecutionDigest(workload)
	if err != nil {
		t.Fatal(err)
	}
	manifest := remoteportable.PortableManifest{
		SchemaVersion: remoteportable.PortableManifestSchemaVersion, ContractID: remoteportable.PortableManifestContractID,
		Engine:    remoteportable.PortableManifestEngine{RequiredProtocolABIDigest: engineFacts.ProtocolABIDigest, RequiredToolchainDigest: engineFacts.ToolchainDigest, RequiredCapabilities: []string{"go"}},
		Target:    remoteportable.PortableManifestTarget{RepositoryID: "fixture", ObjectFormat: "sha1", WorktreeMount: "worktree"},
		Workloads: []remoteportable.PortableManifestWorkload{workload},
		CatalogPolicies: []remoteportable.PortableManifestCatalogPolicy{
			{Platform: string(remoteportable.RepositoryWorkloadPlatformLinuxAMD64), Runtime: string(remoteportable.RepositoryWorkloadRuntimeUbuntuContainer), CPUMilli: 500, MemoryBytes: 512 << 20, HistoricalEstimateMS: 500, ExecutionPolicy: string(remoteportable.RepositoryWorkloadExecutionPolicyNormal)},
			{Platform: string(remoteportable.RepositoryWorkloadPlatformWindowsAMD64), Runtime: string(remoteportable.RepositoryWorkloadRuntimeWindowsNative), CPUMilli: 500, MemoryBytes: 512 << 20, HistoricalEstimateMS: 750, ExecutionPolicy: string(remoteportable.RepositoryWorkloadExecutionPolicyNormal)},
		},
		Profile: remoteportable.ProfileLocalFast, CapacityPolicyDigest: "", RequiredNodeCapabilities: []string{"go1.26.5"},
		LogPolicy: remoteportable.PortableManifestLogPolicy{ResultSchema: "remote-ci-result/v1", EventSchema: "remote-ci-event/v1", MaxResultBytes: 1 << 20, MaxEventBytes: 64 << 10},
		Authority: remoteportable.PortableManifestAuthority{ContractID: remoteportable.PortableManifestContractID, PostgreSQLIdentity: "remote-ci-postgresql-primary/v1"},
		Modules:   []remoteportable.RepositoryWorkloadModule{{Root: ".", ModulePath: "example.invalid/fixture"}}, ExcludedNestedModules: []string{},
	}
	manifest.CapacityPolicyDigest = derivePortableCapacityPolicyDigestFixture(t, manifest)
	encoded, err := remoteportable.PortableManifestCanonicalEncode(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func derivePortableCapacityPolicyDigestFixture(t testing.TB, manifest remoteportable.PortableManifest) string {
	t.Helper()
	digest, err := remoteportable.DerivePortableCapacityPolicyDigest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

// assertPortableProgressStages 验证 validate 的 bounded stage 日志完整可观察。
func assertPortableProgressStages(t *testing.T, stderr string) {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	if len(lines) != 4 {
		t.Fatalf("validate event count = %d, stderr=%q", len(lines), stderr)
	}
	previousSeq := 0
	for _, line := range lines {
		var event struct {
			Schema         string `json:"schema"`
			OperationID    string `json:"operation_id"`
			ManifestDigest string `json:"manifest_digest"`
			Seq            int    `json:"seq"`
			Time           string `json:"time"`
			NodeIDDigest   string `json:"node_id_digest"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		if event.Schema != "remote-ci-event/v1" || event.OperationID == "" || event.ManifestDigest == "" || event.NodeIDDigest == "" || event.Seq != previousSeq+1 {
			t.Fatalf("invalid validation event: %q", line)
		}
		if _, err := time.Parse(time.RFC3339Nano, event.Time); err != nil {
			t.Fatalf("invalid event time: %q", event.Time)
		}
		previousSeq = event.Seq
	}
}

// mustMkdirAll 创建测试目录，失败时终止当前测试。
func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

// mustWriteTestFile 写入测试 regular file，失败时终止当前测试。
func mustWriteTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// portableCLIFixtureInput 构造 locator-only 的 exact tree binding descriptor。
func portableCLIFixtureInput(t *testing.T, _ string, commit, tree string, manifest []byte) remoteportable.PortableRunInput {
	t.Helper()
	return remoteportable.PortableRunInput{SchemaVersion: remoteportable.PortableRunRequestSchemaVersion, ContractID: remoteportable.PortableManifestContractID, OperationID: strings.Repeat("a", 64), RepositoryID: "fixture", ObjectFormat: "sha1", CommitSHA: commit, TreeSHA: tree, ManifestPath: "ci/workloads.json", ManifestBlobSHA256: digestBytes(manifest)}
}

// runGitFixture 执行测试仓库内的只读或 fixture Git 命令并返回单行输出。
func runGitFixture(t *testing.T, repository string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repository}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

// TestExecuteRequiresExplicitProductionArguments 锁定 execute 不接受隐式仓库或配置。
func TestExecuteRequiresExplicitProductionArguments(t *testing.T) {
	t.Parallel()

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	err := dispatchCLI([]string{"execute"}, stdout, stderr)
	if err == nil {
		t.Fatal("dispatchCLI(execute) accepted missing production arguments")
	}
	if stdout.Len() != 0 {
		t.Fatalf("execute wrote stdout: %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("dispatchCLI should leave rendering to Run, wrote stderr: %q", stderr.String())
	}
}

// TestRetiredCommandsDoNotCreateExecutionPaths 确认旧命令不会获得兼容入口。
func TestRetiredCommandsDoNotCreateExecutionPaths(t *testing.T) {
	t.Parallel()

	cases := []struct {
		command string
		args    []string
	}{
		{command: "plan", args: []string{"plan"}},
		{command: "run", args: []string{"run", "--config", "/tmp/config", "--repository", "/tmp/repository", "--run-id", "retired-run"}},
		{command: "route", args: []string{"route", "--config", "/tmp/config", "--repository", "/tmp/repository"}},
		{command: "status", args: []string{"status"}},
		{command: "worker", args: []string{"worker"}},
		{command: "remote", args: []string{"remote"}},
		{command: "_artifact-build", args: []string{"_artifact-build"}},
		{command: "_runtime-artifact-build", args: []string{"_runtime-artifact-build"}},
		{command: "_artifact-identity", args: []string{"_artifact-identity"}},
		{command: "builder-simulate", args: []string{"builder-simulate"}},
	}
	for _, testCase := range cases {
		code, stdout, stderr := executeCLI(testCase.args)
		if code != 1 || stdout != "" || stderr != "" {
			t.Fatalf("command %q result = code %d stdout %q stderr %q", testCase.command, code, stdout, stderr)
		}
	}
}

// TestRemoteCIPublicDispatchASTGuard 静态锁定公开分派不会重新接入本地 workflow。
func TestRemoteCIPublicDispatchASTGuard(t *testing.T) {
	t.Parallel()

	cases := parseRemoteCIDispatchCases(t)
	assertRemoteCIDispatchAllowlist(t, cases)
	assertExecuteProductionDispatchCase(t, cases["execute"])
	assertRemoteCIUsage(t)
}

// assertRemoteCIDispatchAllowlist 拒绝未知或缺失的生产命令 case。
func assertRemoteCIDispatchAllowlist(t *testing.T, cases map[string]*ast.CaseClause) {
	t.Helper()

	want := map[string]bool{"validate": true, "execute": true}
	if len(cases) != len(want) {
		t.Fatalf("dispatch command count = %d, want %d: %v", len(cases), len(want), cases)
	}
	for command := range cases {
		if !want[command] {
			t.Fatalf("dispatch exposes forbidden command %q", command)
		}
	}
}

// assertRemoteCIUsage 锁定 usage 只宣传获准的公开命令。
func assertRemoteCIUsage(t *testing.T) {
	t.Helper()

	for _, retired := range []string{" route ", " run "} {
		if strings.Contains(usageText, retired) {
			t.Fatalf("usageText advertises retired local command %q: %q", retired, usageText)
		}
	}
	for _, public := range []string{" validate ", " execute"} {
		if !strings.Contains(" "+usageText, public) {
			t.Fatalf("usageText is missing public command %q: %q", public, usageText)
		}
	}
}

// parseRemoteCIDispatchCases 从生产 switch 动态枚举全部命令 case。
func parseRemoteCIDispatchCases(t *testing.T) map[string]*ast.CaseClause {
	t.Helper()

	parsed, err := parser.ParseFile(token.NewFileSet(), "portable_validate.go", nil, 0)
	if err != nil {
		t.Fatalf("parse portable_validate.go: %v", err)
	}
	function := findRemoteCIDispatchFunction(t, parsed)
	cases := make(map[string]*ast.CaseClause)
	ast.Inspect(function.Body, func(node ast.Node) bool {
		clause, ok := node.(*ast.CaseClause)
		if !ok || len(clause.List) == 0 {
			return true
		}
		recordRemoteCIDispatchClause(t, cases, clause)
		return false
	})
	return cases
}

// findRemoteCIDispatchFunction 定位生产 dispatchCLI 声明。
func findRemoteCIDispatchFunction(t *testing.T, parsed *ast.File) *ast.FuncDecl {
	t.Helper()

	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if function.Name.Name == "dispatchCLI" {
			return function
		}
	}
	t.Fatal("dispatchCLI declaration is missing")
	return nil
}

// recordRemoteCIDispatchClause 登记一个 case 的全部字符串命令并拒绝歧义。
func recordRemoteCIDispatchClause(t *testing.T, cases map[string]*ast.CaseClause, clause *ast.CaseClause) {
	t.Helper()

	for _, expression := range clause.List {
		literal, ok := expression.(*ast.BasicLit)
		if !ok {
			t.Fatalf("dispatch case is not a string literal: %T", expression)
		}
		if literal.Kind != token.STRING {
			t.Fatalf("dispatch case literal kind = %s, want string", literal.Kind)
		}
		command, err := strconv.Unquote(literal.Value)
		if err != nil {
			t.Fatalf("unquote dispatch command: %v", err)
		}
		if cases[command] != nil {
			t.Fatalf("dispatch command %q is duplicated", command)
		}
		cases[command] = clause
	}
}

// assertExecuteProductionDispatchCase 锁定 execute 只进入唯一 production composition。
func assertExecuteProductionDispatchCase(t *testing.T, clause *ast.CaseClause) {
	t.Helper()

	if clause == nil || len(clause.Body) != 1 {
		t.Fatalf("execute case body is not one direct return: %#v", clause)
	}
	statement, ok := clause.Body[0].(*ast.ReturnStmt)
	if !ok || len(statement.Results) != 1 {
		t.Fatalf("execute case is not one direct return: %T", clause.Body[0])
	}
	call, ok := statement.Results[0].(*ast.CallExpr)
	if !ok {
		t.Fatalf("execute case returns %T, want dispatchExecute call", statement.Results[0])
	}
	identifier, callOK := call.Fun.(*ast.Ident)
	if !callOK || identifier.Name != "dispatchExecute" || len(call.Args) != 3 {
		t.Fatalf("execute case returns %T %v, want dispatchExecute call", statement.Results[0], statement.Results[0])
	}
}

// TestParseGitTreeEntryRejectsNonRegularOrAmbiguousEntries 锁定 exact-tree manifest 的 mode 边界。
func TestParseGitTreeEntryRejectsNonRegularOrAmbiguousEntries(t *testing.T) {
	t.Parallel()

	path := "ci/workloads.json"
	mode, objectType, err := parseGitTreeEntry([]byte("100644 blob 0123456789012345678901234567890123456789\t"+path+"\x00"), path)
	if err != nil || mode != "100644" || objectType != "blob" {
		t.Fatalf("parseGitTreeEntry() = %q, %q, %v", mode, objectType, err)
	}
	for _, raw := range [][]byte{
		[]byte("120000 blob 0123456789012345678901234567890123456789\t" + path),
		[]byte("100644 blob 0123456789012345678901234567890123456789\tother\x00"),
		[]byte("100644 blob a\t" + path + "\x00extra\x00"),
	} {
		parsedMode, parsedType, parseErr := parseGitTreeEntry(raw, path)
		if parseErr == nil && parsedMode == "100644" && parsedType == "blob" {
			t.Fatalf("parseGitTreeEntry() accepted ambiguous entry %q", raw)
		}
	}
}

// TestValidateAbsolutePathComponentsRejectsParentSymlink 确认 host authority 不跟随父目录别名。
func TestValidateAbsolutePathComponentsRejectsParentSymlink(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(root, "link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	filePath := filepath.Join(realDir, "authority.json")
	if err := os.WriteFile(filePath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateAbsolutePathComponents(filepath.Join(linkDir, "authority.json"), false); err == nil {
		t.Fatal("validateAbsolutePathComponents() accepted a parent symlink")
	}
}

// executeCLI 执行测试用的 CLI 并返回标准化输出。
func executeCLI(args []string) (int, string, string) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := Run(args, stdout, stderr)
	return code, stdout.String(), stderr.String()
}

// mustReadFile 读取测试 fixture，失败时立即终止当前测试。
func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

type failingEventWriter struct{}

func (failingEventWriter) Write([]byte) (int, error) {
	return 0, errors.New("event writer failure")
}

func TestValidationEventWriterFailureBlocksPublication(t *testing.T) {
	if err := writePortableValidationEvent(failingEventWriter{}, strings.Repeat("a", 64), "sha256:"+strings.Repeat("b", 64), 1, "manifest_binding", "portable_run_bound"); err == nil {
		t.Fatal("validation event writer failure was swallowed")
	}
}

func TestValidateEventFailureBlocksStdoutPublication(t *testing.T) {
	descriptorPath := portableCLIRealDescriptor(t)
	var stdout bytes.Buffer
	err := dispatchCLI([]string{"validate", "--descriptor", descriptorPath, "--repository", filepath.Dir(descriptorPath)}, &stdout, failingEventWriter{})
	if err == nil || stdout.Len() != 0 {
		t.Fatalf("validation event failure published result: err=%v stdout=%q", err, stdout.String())
	}
}

func TestRemoteCIEngineProtocolABIDigestIsStableAndBindsComponentDrift(t *testing.T) {
	first, err := RemoteCIEngineProtocolABIDigest()
	if err != nil {
		t.Fatalf("RemoteCIEngineProtocolABIDigest: %v", err)
	}
	second, err := RemoteCIEngineProtocolABIDigest()
	if err != nil || first != second || !strings.HasPrefix(first, "sha256:") {
		t.Fatalf("composite ABI digest is unstable or malformed: first=%q second=%q err=%v", first, second, err)
	}
	components := remoteCIEngineABIComponents()
	if len(components) != 6 {
		t.Fatalf("ABI component registry count = %d, want 6", len(components))
	}
	for _, test := range []struct {
		name   string
		mutate func([]remoteCIEngineABIComponent)
	}{
		{name: "schema constant", mutate: func(value []remoteCIEngineABIComponent) {
			value[0].Constants = append(value[0].Constants, "schema-drift/v1")
		}},
		{name: "wire field", mutate: func(value []remoteCIEngineABIComponent) {
			drifted := reflect.StructOf([]reflect.StructField{{Name: "SchemaVersion", Type: reflect.TypeFor[string](), Tag: `json:"schema_version"`}, {Name: "NewField", Type: reflect.TypeFor[string](), Tag: `json:"new_field"`}})
			value[0].Types = append(value[0].Types, drifted)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			mutated := cloneRemoteCIEngineABIComponents(components)
			test.mutate(mutated)
			drifted, err := deriveRemoteCIEngineProtocolABIDigest(mutated)
			if err != nil {
				t.Fatalf("derive drifted ABI: %v", err)
			}
			if drifted == first {
				t.Fatal("ABI drift did not change composite digest")
			}
		})
	}
}

func cloneRemoteCIEngineABIComponents(source []remoteCIEngineABIComponent) []remoteCIEngineABIComponent {
	clone := make([]remoteCIEngineABIComponent, len(source))
	for index, component := range source {
		clone[index] = component
		clone[index].Constants = append([]string(nil), component.Constants...)
		clone[index].Types = append([]reflect.Type(nil), component.Types...)
	}
	return clone
}
