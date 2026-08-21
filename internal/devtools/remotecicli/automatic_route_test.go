package remotecicli

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

// TestAutomaticRouteCLIRejectsManualRoutingFields 锁定调用者不能选择 node、tier 或 executor。
func TestAutomaticRouteCLIRejectsManualRoutingFields(t *testing.T) {
	for _, forbidden := range []string{"--node", "--node-id", "--tier", "--eci-tier", "--executor"} {
		t.Run(strings.TrimPrefix(forbidden, "--"), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Run([]string{"route", forbidden, "manual"}, &stdout, &stderr)
			if code != 1 || stdout.Len() != 0 {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if strings.Contains(stderr.String(), "unknown command") || !strings.Contains(stderr.String(), "automatic route usage") {
				t.Fatalf("manual routing field returned wrong boundary: %q", stderr.String())
			}
		})
	}
}

// TestAutomaticRouteConfigFieldGuard 从生产类型动态枚举 wire 字段并锁定消费集合。
func TestAutomaticRouteConfigFieldGuard(t *testing.T) {
	typeOf := reflect.TypeFor[AutomaticRouteConfig]()
	want := []string{"schema_version", "topology_manifest_path", "owner_db_path", "owner_scope", "ssh_executable", "probe_timeout_ms", "probe_wait_delay_ms", "heartbeat_ttl_ms", "nodes"}
	got := make([]string, 0, typeOf.NumField())
	for index := range typeOf.NumField() {
		got = append(got, typeOf.Field(index).Tag.Get("json"))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("automatic route config fields = %v, want %v", got, want)
	}
}

// TestBindAutomaticProbeCommandsRequiresExactManifestNodeSet 锁定 node binding 双向差集。
func TestBindAutomaticProbeCommandsRequiresExactManifestNodeSet(t *testing.T) {
	manifest := heteroci.Manifest{Nodes: []heteroci.NodeConfig{{NodeID: "node-a"}, {NodeID: "node-b"}}}
	config := AutomaticRouteConfig{Nodes: []AutomaticProbeNode{
		{NodeID: "node-a", ProbeExecutable: "/probe", ProbeConfigPath: "/a.json"},
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
	stale.Nodes = append(stale.Nodes, AutomaticProbeNode{NodeID: "node-c", ProbeExecutable: "/probe", ProbeConfigPath: "/c.json"})
	if _, err := bindAutomaticProbeCommands(stale, manifest); err == nil {
		t.Fatal("stale node binding was accepted")
	}
}
