package remotecicli

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

// TestAutomaticRouteCLIIsNotPublic 锁定公开入口只有自动 run，route 不能推进 owner epoch。
func TestAutomaticRouteCLIIsNotPublic(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"route", "--config", "/tmp/config", "--repository", "/tmp/repository"}, &stdout, &stderr)
	if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestAutomaticRunCLIRejectsManualRoutingFields(t *testing.T) {
	for _, forbidden := range []string{"--node", "--node-id", "--tier", "--eci-tier", "--executor"} {
		var stdout, stderr bytes.Buffer
		code := Run([]string{"run", forbidden, "manual"}, &stdout, &stderr)
		if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "automatic run usage") {
			t.Fatalf("flag=%s code=%d stdout=%q stderr=%q", forbidden, code, stdout.String(), stderr.String())
		}
	}
}

func TestAutomaticRunResultRejectsOversizeBeforeStdoutWrite(t *testing.T) {
	var stdout bytes.Buffer
	units := make([]AutomaticRunUnitResult, 20_000)
	for index := range units {
		units[index].UnitID = strings.Repeat("x", 1_024)
	}
	if err := writeAutomaticRunResult(&stdout, AutomaticRunResult{Units: units}); err == nil || stdout.Len() != 0 {
		t.Fatalf("oversize result error=%v stdout_bytes=%d", err, stdout.Len())
	}
	if err := writeAutomaticRunResult(&stdout, AutomaticRunResult{SchemaVersion: "bounded"}); err != nil || stdout.Len() == 0 {
		t.Fatalf("bounded result error=%v stdout_bytes=%d", err, stdout.Len())
	}
}

func TestAutomaticBatchWorkloadMapsEveryManifestKind(t *testing.T) {
	for _, workload := range []remoteci.RepositoryWorkload{
		{ID: "test", Kind: remoteci.RepositoryWorkloadKindGoTest, ModuleRoot: ".", Package: "./pkg", Name: "TestExact", ExecutionPolicy: remoteci.RepositoryWorkloadExecutionPolicyNormal},
		{ID: "package", Kind: remoteci.RepositoryWorkloadKindGoPackage, ModuleRoot: ".", Package: ".", ExecutionPolicy: remoteci.RepositoryWorkloadExecutionPolicyNormal},
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
