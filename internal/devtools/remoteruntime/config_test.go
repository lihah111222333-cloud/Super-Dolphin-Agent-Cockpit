package remoteruntime

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestRuntimeConfigCanonicalRoundTripAndABI(t *testing.T) {
	config := runtimeFixture()
	encoded, err := EncodeConfig(config)
	if err != nil {
		t.Fatalf("EncodeConfig() error = %v", err)
	}
	decoded, err := DecodeConfig(encoded)
	if err != nil {
		t.Fatalf("DecodeConfig() error = %v", err)
	}
	second, err := EncodeConfig(decoded)
	if err != nil {
		t.Fatalf("re-encode error = %v", err)
	}
	if !bytes.Equal(encoded, second) {
		t.Fatalf("canonical round trip changed bytes: %s != %s", encoded, second)
	}
	assertJSONFields(t, encoded, ConfigFields())
	assertJSONFields(t, mustJSONNode(t, encoded, "Ubuntu-01"), NodeBindingFields())
	if _, err := PolicyDigest(config); err != nil {
		t.Fatalf("PolicyDigest() error = %v", err)
	}
}

func TestRuntimeConfigRejectsNonCanonicalAndUnknownFields(t *testing.T) {
	encoded, err := EncodeConfig(runtimeFixture())
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"leading whitespace":  append([]byte{' '}, encoded...),
		"trailing whitespace": append(append([]byte(nil), encoded...), '\n'),
		"trailing value":      append(append([]byte(nil), encoded...), []byte("{}")...),
		"unknown field":       append(append(encoded[:len(encoded)-1], ','), []byte(`"sqlite_path":"bad"}`)...),
		"duplicate field":     duplicateRuntimeField(encoded),
		"null field":          bytes.Replace(encoded, []byte(`"owner_namespace":"remote-ci-fixture"`), []byte(`"owner_namespace":null`), 1),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeConfig(data); err == nil {
				t.Fatalf("DecodeConfig() accepted %s", name)
			}
		})
	}
}

func TestRuntimeConfigTopologyRules(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*RuntimeConfig)
	}{
		{name: "macOS platform", mutate: func(config *RuntimeConfig) { config.Nodes[0].Platform = Platform("macos") }},
		{name: "node order", mutate: func(config *RuntimeConfig) { config.Nodes[1], config.Nodes[2] = config.Nodes[2], config.Nodes[1] }},
		{name: "Ubuntu object roots differ", mutate: func(config *RuntimeConfig) { config.Nodes[1].ObjectStoreRoot = "/srv/ci/other-object" }},
		{name: "Ubuntu worktrees overlap", mutate: func(config *RuntimeConfig) { config.Nodes[1].WorktreeRoot = config.Nodes[0].WorktreeRoot + "/nested" }},
		{name: "Windows roots overlap", mutate: func(config *RuntimeConfig) { config.Nodes[3].TempRoot = config.Nodes[2].TempRoot }},
		{name: "platform fields mixed", mutate: func(config *RuntimeConfig) { config.Nodes[2].Linux = &LinuxContainerBinding{} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config := runtimeFixture()
			tc.mutate(&config)
			if err := config.Validate(); err == nil {
				t.Fatalf("Validate() accepted %s", tc.name)
			}
		})
	}
}

func TestRuntimeConfigV3WJFixturesSharePolicy(t *testing.T) {
	config := runtimeFixture()
	v3, err := BindPortableRun(config, targetFixture("super-agent-v3"))
	if err != nil {
		t.Fatal(err)
	}
	wj, err := BindPortableRun(config, targetFixture("wjboot-v2"))
	if err != nil {
		t.Fatal(err)
	}
	v3Policy, err := EncodeConfig(v3.RuntimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	wjPolicy, err := EncodeConfig(wj.RuntimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	if v3.Namespace == wj.Namespace || !bytes.Equal(v3Policy, wjPolicy) {
		t.Fatalf("V3/WJ target binding did not preserve policy while changing namespace: %+v %+v", v3, wj)
	}
}

func runtimeFixture() RuntimeConfig {
	return RuntimeConfig{
		SchemaVersion: ConfigSchemaVersion, TopologyManifestPath: "/etc/super-dolphin/topology.json",
		OwnerDSNEnv: "REMOTE_CI_OWNER_DSN", ReceiptDSNEnv: "REMOTE_CI_RECEIPT_DSN",
		OwnerNamespace: "remote-ci-fixture", SSHExecutable: "/usr/bin/ssh",
		ProbeTimeoutMS: 1700, RunTimeoutMS: 45000, CleanupTimeoutMS: 3100, WorkerTimeoutMS: 90000,
		Nodes: []NodeBinding{
			runtimeUbuntuNode(Ubuntu01NodeID, "/srv/ci/u01"),
			runtimeUbuntuNode(Ubuntu02NodeID, "/srv/ci/u02"),
			runtimeWindowsNode(Windows01NodeID, "C:/ci/w01"),
			runtimeWindowsNode(Windows02NodeID, "C:/ci/w02"),
		},
	}
}

func runtimeUbuntuNode(nodeID, privateRoot string) NodeBinding {
	return NodeBinding{
		NodeID: nodeID, Platform: UbuntuPlatform, Endpoint: "ssh://" + strings.ToLower(nodeID),
		ProbeExecutable: "/opt/super-dolphin/probe", ProbeConfigPath: "/etc/super-dolphin/probe.json", ProbeIdentity: nodeID + "-probe",
		RunnerExecutable: "/opt/super-dolphin/linux_runner_daemon.sh", RunnerIdentity: nodeID + "-runner",
		WorkerExecutable: "/opt/super-dolphin/worker", WorkerIdentity: nodeID + "-worker",
		RepositoryRoot: privateRoot + "/worktree", ObjectStoreRoot: "/srv/ci/shared/object-store",
		WorktreeRoot: privateRoot + "/worktree", CachePublicationRoot: "/srv/ci/shared/cache-publication",
		PrivateOverlayRoot: privateRoot + "/overlay", TempRoot: privateRoot + "/temp",
		GoExecutable: "/usr/local/go/bin/go", ToolchainIdentity: "sha256:" + strings.Repeat("c", 64),
		Linux: &LinuxContainerBinding{DockerExecutable: "/usr/bin/docker", DockerImage: "sha256:" + strings.Repeat("d", 64), DockerImageIdentity: "sha256:" + strings.Repeat("e", 64), CgroupRoot: "/sys/fs/cgroup/super-dolphin", RunAsUser: "ci", DockerGroup: "docker"},
	}
}

func runtimeWindowsNode(nodeID, privateRoot string) NodeBinding {
	return NodeBinding{
		NodeID: nodeID, Platform: WindowsPlatform, Endpoint: "ssh://" + strings.ToLower(nodeID),
		ProbeExecutable: "C:/super-dolphin/probe.exe", ProbeConfigPath: "C:/super-dolphin/probe.json", ProbeIdentity: nodeID + "-probe",
		RunnerExecutable: "C:/super-dolphin/windows_runner.ps1", RunnerIdentity: nodeID + "-runner",
		WorkerExecutable: "C:/super-dolphin/worker.exe", WorkerIdentity: nodeID + "-worker",
		RepositoryRoot: privateRoot + "/worktree", ObjectStoreRoot: privateRoot + "/objects", WorktreeRoot: privateRoot + "/worktree",
		CachePublicationRoot: privateRoot + "/cache-publication", PrivateOverlayRoot: privateRoot + "/overlay", TempRoot: privateRoot + "/temp",
		GoExecutable: "C:/Go/bin/go.exe", ToolchainIdentity: "sha256:" + strings.Repeat("f", 64),
	}
}

func duplicateRuntimeField(encoded []byte) []byte {
	return bytes.Replace(encoded, []byte(`"owner_namespace":"remote-ci-fixture"`), []byte(`"owner_namespace":"remote-ci-fixture","owner_namespace":"remote-ci-fixture"`), 1)
}

func targetFixture(repositoryID string) TargetFacts {
	return TargetFacts{RepositoryID: repositoryID, ObjectFormat: "sha1", CommitSHA: strings.Repeat("a", 40), TreeSHA: strings.Repeat("b", 40), GOOS: "linux", GOARCH: "amd64", CGOEnabled: "0", ModuleIdentity: "sha256:" + strings.Repeat("1", 64), WorkloadIdentity: "sha256:" + strings.Repeat("2", 64)}
}

func mustJSONNode(t *testing.T, encoded []byte, nodeID string) []byte {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	var nodes []json.RawMessage
	if err := json.Unmarshal(object["nodes"], &nodes); err != nil {
		t.Fatal(err)
	}
	for _, raw := range nodes {
		var node map[string]json.RawMessage
		if err := json.Unmarshal(raw, &node); err != nil {
			t.Fatal(err)
		}
		var id string
		if err := json.Unmarshal(node["node_id"], &id); err != nil {
			t.Fatal(err)
		}
		if id == nodeID {
			return raw
		}
	}
	t.Fatalf("node %q missing", nodeID)
	return nil
}

func assertJSONFields(t *testing.T, data []byte, expected []string) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	actual := make([]string, 0, len(fields))
	for field := range fields {
		actual = append(actual, field)
	}
	slices.Sort(actual)
	want := slices.Clone(expected)
	slices.Sort(want)
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("JSON fields = %v, want %v", actual, want)
	}
}

func TestRuntimeConfigABIRejectsForbiddenLegacyNames(t *testing.T) {
	for _, field := range append(ConfigFields(), NodeBindingFields()...) {
		lower := strings.ToLower(field)
		if strings.Contains(lower, "sqlite") || strings.Contains(lower, "aliyun") || strings.Contains(lower, "owner_db") {
			t.Fatalf("runtime ABI contains forbidden field %q", field)
		}
	}
	if err := (RuntimeConfig{}).Validate(); err == nil {
		t.Fatal("zero runtime config unexpectedly validates")
	}
}

func TestLoadConfigRejectsHandleReplacementAfterRead(t *testing.T) {
	encoded, err := EncodeConfig(runtimeFixture())
	if err != nil {
		t.Fatal(err)
	}
	root := resolvedTestTempDir(t)
	path := filepath.Join(root, "runtime.json")
	replacement := filepath.Join(root, "replacement.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replacement, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	initial, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	final, err := os.Lstat(replacement)
	if err != nil {
		t.Fatal(err)
	}
	handle := &scriptedConfigHandle{reader: bytes.NewReader(encoded), stats: []os.FileInfo{initial, final}}
	loaded, err := loadConfigWithOps(path, configFileOps{
		lstat: os.Lstat,
		open:  func(string) (configHandle, error) { return handle, nil },
	})
	if err == nil {
		t.Fatalf("loadConfigWithOps() accepted replacement: loaded=%+v err=%v", loaded, err)
	}
	if !handle.closed {
		t.Fatal("loadConfigWithOps() did not close replaced handle")
	}
}

func TestLoadConfigRejectsSymlinkComponent(t *testing.T) {
	encoded, err := EncodeConfig(runtimeFixture())
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	target := filepath.Join(root, "target")
	link := filepath.Join(root, "link")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "runtime.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := LoadConfig(filepath.Join(link, "runtime.json")); err == nil {
		t.Fatal("LoadConfig() accepted a symlink component")
	}
}

func resolvedTestTempDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

type scriptedConfigHandle struct {
	reader *bytes.Reader
	stats  []os.FileInfo
	index  int
	closed bool
}

func (handle *scriptedConfigHandle) Read(data []byte) (int, error) {
	return handle.reader.Read(data)
}

func (handle *scriptedConfigHandle) Stat() (os.FileInfo, error) {
	if handle.index >= len(handle.stats) {
		return nil, io.ErrUnexpectedEOF
	}
	stat := handle.stats[handle.index]
	handle.index++
	return stat, nil
}

func (handle *scriptedConfigHandle) Close() error {
	handle.closed = true
	return nil
}
