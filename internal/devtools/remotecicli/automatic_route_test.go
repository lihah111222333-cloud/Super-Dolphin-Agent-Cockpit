package remotecicli

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociworkflow"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteportable"
)

// TestAutomaticRouteCLIIsNotPublic 锁定 route 不能推进 owner epoch。
func TestAutomaticRouteCLIIsNotPublic(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"route", "--config", "/tmp/config", "--repository", "/tmp/repository"}, &stdout, &stderr)
	if code != 1 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

// TestAutomaticRunCLIIsNotPublic 锁定任何 run 参数都不能抵达本地 workflow。
func TestAutomaticRunCLIIsNotPublic(t *testing.T) {
	for _, forbidden := range []string{"--node", "--node-id", "--tier", "--eci-tier", "--executor"} {
		var stdout, stderr bytes.Buffer
		code := Run([]string{"run", forbidden, "manual"}, &stdout, &stderr)
		if code != 1 || stdout.Len() != 0 || stderr.Len() != 0 {
			t.Fatalf("flag=%s code=%d stdout=%q stderr=%q", forbidden, code, stdout.String(), stderr.String())
		}
	}
}

// TestAutomaticRunResultRejectsContractDriftBeforeStdoutWrite 逐字段锁定非权威结果契约。
func TestAutomaticRunResultRejectsContractDriftBeforeStdoutWrite(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*AutomaticRunResult)
	}{
		{name: "missing schema", mutate: func(result *AutomaticRunResult) { result.SchemaVersion = "" }},
		{name: "unknown schema", mutate: func(result *AutomaticRunResult) { result.SchemaVersion = "automatic-heteroci-run-result/v2" }},
		{name: "missing authority", mutate: func(result *AutomaticRunResult) { result.Authority = "" }},
		{name: "remote authority", mutate: func(result *AutomaticRunResult) { result.Authority = "REMOTE_AUTHORITATIVE" }},
		{name: "missing status", mutate: func(result *AutomaticRunResult) { result.Status = "" }},
		{name: "verified status", mutate: func(result *AutomaticRunResult) { result.Status = "PASSED" }},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			result := validAutomaticRunResultFixture()
			testCase.mutate(&result)
			var stdout bytes.Buffer
			if err := writeAutomaticRunResult(&stdout, result); err == nil || stdout.Len() != 0 {
				t.Fatalf("write result error=%v stdout=%q", err, stdout.String())
			}
		})
	}
	var stdout bytes.Buffer
	if err := writeAutomaticRunResult(&stdout, validAutomaticRunResultFixture()); err != nil || stdout.Len() == 0 {
		t.Fatalf("write canonical result error=%v stdout=%q", err, stdout.String())
	}
}

// validAutomaticRunResultFixture 返回冻结字段完整的最小非权威结果。
func validAutomaticRunResultFixture() AutomaticRunResult {
	return AutomaticRunResult{
		SchemaVersion: "automatic-heteroci-run-result/v1",
		Authority:     heteroci.AuthorityLocalNonAuthoritative,
		Status:        heterociworkflow.ResultStatusNotVerified,
	}
}

// TestAutomaticRunResultRejectsOversizeBeforeStdoutWrite 锁定合法结果仍受字节上限保护。
func TestAutomaticRunResultRejectsOversizeBeforeStdoutWrite(t *testing.T) {
	var stdout bytes.Buffer
	units := make([]AutomaticRunUnitResult, 20_000)
	for index := range units {
		units[index].UnitID = strings.Repeat("x", 1_024)
	}
	oversize := validAutomaticRunResultFixture()
	oversize.Units = units
	if err := writeAutomaticRunResult(&stdout, oversize); err == nil || stdout.Len() != 0 {
		t.Fatalf("oversize result error=%v stdout_bytes=%d", err, stdout.Len())
	}
	if err := writeAutomaticRunResult(&stdout, validAutomaticRunResultFixture()); err != nil || stdout.Len() == 0 {
		t.Fatalf("bounded result error=%v stdout_bytes=%d", err, stdout.Len())
	}
}

func TestAutomaticBatchWorkloadMapsEveryManifestKind(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	for _, workload := range []remoteportable.PortableManifestWorkload{
		{UnitID: "test", CommandDigest: digest, ExecutionDigest: digest, TargetPlatform: string(remoteportable.RepositoryWorkloadPlatformLinuxAMD64), RuntimeCapability: string(remoteportable.RepositoryWorkloadRuntimeUbuntuContainer), CPUMilli: 1_000, MemoryBytes: 1, HistoricalEstimateMS: 1, Kind: remoteportable.RepositoryWorkloadKindGoTest, ModuleRoot: ".", Package: "./pkg", Name: "TestExact", ExecutionPolicy: string(remoteportable.RepositoryWorkloadExecutionPolicyNormal)},
		{UnitID: "package", CommandDigest: digest, ExecutionDigest: digest, TargetPlatform: string(remoteportable.RepositoryWorkloadPlatformLinuxAMD64), RuntimeCapability: string(remoteportable.RepositoryWorkloadRuntimeUbuntuContainer), CPUMilli: 1_000, MemoryBytes: 1, HistoricalEstimateMS: 1, Kind: remoteportable.RepositoryWorkloadKindGoPackage, ModuleRoot: ".", Package: ".", ExecutionPolicy: string(remoteportable.RepositoryWorkloadExecutionPolicyNormal)},
	} {
		mapped := automaticBatchWorkload(workload)
		if mapped.Kind != string(workload.Kind) {
			t.Fatalf("mapped kind = %q, want %q", mapped.Kind, workload.Kind)
		}
		if err := mapped.Validate(); err != nil {
			t.Fatalf("mapped workload %q: %v", workload.Kind, err)
		}
	}
}

// TestAutomaticRouteConfigFieldGuard 从生产类型动态枚举 wire 字段并锁定消费集合。
func TestAutomaticRouteConfigFieldGuard(t *testing.T) {
	typeOf := reflect.TypeFor[AutomaticRouteConfig]()
	want := []string{"schema_version", "topology_manifest_path", "owner_db_path", "receipt_db_path", "owner_namespace", "ssh_executable", "probe_timeout_ms", "probe_wait_delay_ms", "heartbeat_ttl_ms", "run_timeout_ms", "cleanup_timeout_ms", "nodes"}
	got := make([]string, 0, typeOf.NumField())
	for field := range typeOf.Fields() {
		got = append(got, field.Tag.Get("json"))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("automatic route config fields = %v, want %v", got, want)
	}
}

func TestAutomaticProbeNodeFieldGuard(t *testing.T) {
	typeOf := reflect.TypeFor[AutomaticProbeNode]()
	want := []string{"node_id", "probe_executable", "probe_config_path", "runner_executable", "worker_executable", "worker_identity", "repository_base_path", "cache_root", "go_executable", "toolchain_identity", "shared_root", "docker_image", "run_as_user", "docker_group"}
	got := make([]string, 0, typeOf.NumField())
	for field := range typeOf.Fields() {
		got = append(got, field.Tag.Get("json"))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("automatic node fields = %v, want %v", got, want)
	}
}

// TestAutomaticRouteOwnerScopeSeparatesRepositories 锁定短命 CLI owner 不会跨仓互相 takeover。
func TestAutomaticRouteOwnerScopeSeparatesRepositories(t *testing.T) {
	manifest := heteroci.Manifest{SchemaVersion: "heteroci-manifest/v1", Authority: heteroci.AuthorityLocalNonAuthoritative, Nodes: []heteroci.NodeConfig{}}
	first, err := deriveAutomaticRouteOwnerScope("local-nodes", DetectedRepository{RepositoryID: "repo-a", TreeSHA: strings.Repeat("a", 40)}, manifest)
	if err != nil {
		t.Fatal(err)
	}
	second, err := deriveAutomaticRouteOwnerScope("local-nodes", DetectedRepository{RepositoryID: "repo-b", TreeSHA: strings.Repeat("b", 40)}, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("different repositories received the same short-lived owner scope")
	}
}

// TestBindAutomaticProbeCommandsRequiresExactManifestNodeSet 锁定 node binding 双向差集。
func TestBindAutomaticProbeCommandsRequiresExactManifestNodeSet(t *testing.T) {
	manifest := heteroci.Manifest{Nodes: []heteroci.NodeConfig{{NodeID: "node-a", Platform: heteroci.PlatformLinuxAMD64}, {NodeID: "node-b", Platform: heteroci.PlatformWindowsAMD64}}}
	config := AutomaticRouteConfig{Nodes: []AutomaticProbeNode{
		{NodeID: "node-a", ProbeExecutable: "/probe", ProbeConfigPath: "/a.json", RunAsUser: "ci-runner", DockerGroup: "docker", SharedRoot: "/shared", DockerImage: "sha256:image"},
		{NodeID: "node-b", ProbeExecutable: "C:/probe.exe", ProbeConfigPath: "C:/b.json"},
	}}
	commands, err := bindAutomaticProbeCommands(config, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if got := commands["node-b"]; !reflect.DeepEqual(got, []string{"C:/probe.exe", "probe", "--config", "C:/b.json"}) {
		t.Fatalf("node-b command = %v", got)
	}
	missing := config
	missing.Nodes = missing.Nodes[:1]
	if _, err := bindAutomaticProbeCommands(missing, manifest); err == nil {
		t.Fatal("missing manifest node was accepted")
	}
	stale := config
	stale.Nodes = append(stale.Nodes, AutomaticProbeNode{NodeID: "node-c", ProbeExecutable: "/probe", ProbeConfigPath: "/c.json", RunAsUser: "ci-runner", DockerGroup: "docker", SharedRoot: "/shared", DockerImage: "sha256:image"})
	if _, err := bindAutomaticProbeCommands(stale, manifest); err == nil {
		t.Fatal("stale node binding was accepted")
	}
}
