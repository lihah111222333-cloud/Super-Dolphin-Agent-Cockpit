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
	assertJSONFields(t, mustJSONNode(t, encoded, runtimeNodeID), NodeBindingFields())
	assertJSONFields(t, mustJSONObjectField(t, encoded, runtimeNodeID, "linux"), LinuxContainerFields())
	assertJSONFields(t, mustJSONObjectField(t, encoded, runtimeNodeID, "wine"), WineContainerFields())
	if _, err := PolicyDigest(config); err != nil {
		t.Fatalf("PolicyDigest() error = %v", err)
	}
}

func TestRuntimePolicyDigestBindsCallerSuppliedDockerHost(t *testing.T) {
	config := runtimeFixture()
	base, err := PolicyDigest(config)
	if err != nil {
		t.Fatal(err)
	}
	config.Nodes[0].Linux.DockerHost = "unix:///var/run/docker.sock"
	changed, err := PolicyDigest(config)
	if err != nil {
		t.Fatal(err)
	}
	if base == changed {
		t.Fatalf("PolicyDigest() did not bind DockerHost: %q", base)
	}
}

func TestRuntimeConfigRejectsNonCanonicalAndUnknownFields(t *testing.T) {
	encoded, err := EncodeConfig(runtimeFixture())
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"leading whitespace":      append([]byte{' '}, encoded...),
		"trailing whitespace":     append(append([]byte(nil), encoded...), '\n'),
		"trailing value":          append(append([]byte(nil), encoded...), []byte("{}")...),
		"unknown field":           append(append(encoded[:len(encoded)-1], ','), []byte(`"sqlite_path":"bad"}`)...),
		"duplicate field":         duplicateRuntimeField(encoded),
		"null field":              bytes.Replace(encoded, []byte(`"owner_namespace":"remote-ci-fixture"`), []byte(`"owner_namespace":null`), 1),
		"missing heartbeat TTL":   bytes.Replace(encoded, []byte(`,"heartbeat_ttl_ms":30000`), nil, 1),
		"missing node cgo":        bytes.Replace(encoded, []byte(`"cgo_enabled":"0",`), nil, 1),
		"missing request timeout": bytes.Replace(encoded, []byte(`,"request_timeout_ms":120000`), nil, 1),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeConfig(data); err == nil {
				t.Fatalf("DecodeConfig() accepted %s", name)
			}
		})
	}
}

func TestRuntimeConfigRequiresExplicitPolicyTimeouts(t *testing.T) {
	encoded, err := EncodeConfig(runtimeFixture())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"heartbeat_ttl_ms":30000`)) {
		t.Fatalf("canonical config omitted caller-supplied heartbeat TTL: %s", encoded)
	}
	if !bytes.Contains(encoded, []byte(`"request_timeout_ms":120000`)) {
		t.Fatalf("canonical config omitted caller-supplied request timeout: %s", encoded)
	}
	for _, value := range []int64{0, -1, int64(^uint64(0) >> 1)} {
		config := runtimeFixture()
		config.HeartbeatTTLMS = value
		if _, err := EncodeConfig(config); err == nil {
			t.Fatalf("EncodeConfig() accepted heartbeat_ttl_ms=%d", value)
		}
	}
	for _, value := range []int64{0, -1, int64(^uint64(0) >> 1)} {
		config := runtimeFixture()
		config.RequestTimeoutMS = value
		if _, err := EncodeConfig(config); err == nil {
			t.Fatalf("EncodeConfig() accepted request_timeout_ms=%d", value)
		}
	}
	for _, value := range []int64{1699, 44999, 3099, 89999} {
		config := runtimeFixture()
		config.RequestTimeoutMS = value
		if _, err := EncodeConfig(config); err == nil {
			t.Fatalf("EncodeConfig() accepted request timeout below operation timeout: %d", value)
		}
	}
}

func TestRuntimeConfigTopologyRules(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*RuntimeConfig)
	}{
		{name: "zero nodes", mutate: func(config *RuntimeConfig) { config.Nodes = nil }},
		{name: "multiple nodes", mutate: func(config *RuntimeConfig) { config.Nodes = append(config.Nodes, config.Nodes[0]) }},
		{name: "physical Windows platform", mutate: func(config *RuntimeConfig) { config.Nodes[0].Platform = WindowsTargetPlatform }},
		{name: "Linux binding missing", mutate: func(config *RuntimeConfig) { config.Nodes[0].Linux = nil }},
		{name: "Wine binding missing", mutate: func(config *RuntimeConfig) { config.Nodes[0].Wine = nil }},
		{name: "Wine prefix overlaps worktree", mutate: func(config *RuntimeConfig) { config.Nodes[0].Wine.WinePrefixRoot = config.Nodes[0].WorktreeRoot }},
		{name: "physical Linux root uses Windows path", mutate: func(config *RuntimeConfig) { config.Nodes[0].WorktreeRoot = "C:/ci/worktree" }},
		{name: "Wine image identity is not sha256", mutate: func(config *RuntimeConfig) { config.Nodes[0].Wine.DockerImageIdentity = "wine-image" }},
		{name: "Wine image is mutable", mutate: func(config *RuntimeConfig) { config.Nodes[0].Wine.DockerImage = "ubuntu:latest" }},
		{name: "Docker host is not unix", mutate: func(config *RuntimeConfig) { config.Nodes[0].Linux.DockerHost = "tcp://127.0.0.1:2375" }},
		{name: "Docker host is relative", mutate: func(config *RuntimeConfig) { config.Nodes[0].Linux.DockerHost = "unix://run/docker.sock" }},
		{name: "Docker host is not clean", mutate: func(config *RuntimeConfig) { config.Nodes[0].Linux.DockerHost = "unix:///run/./docker.sock" }},
		{name: "Docker host has a symlink-like path", mutate: func(config *RuntimeConfig) { config.Nodes[0].Linux.DockerHost = "unix:///run/../var/docker.sock" }},
		{name: "Docker server ID is unsafe", mutate: func(config *RuntimeConfig) { config.Nodes[0].Linux.DockerServerID = "server id" }},
		{name: "runner identity is not sha256", mutate: func(config *RuntimeConfig) { config.Nodes[0].RunnerIdentity = "runner-identity" }},
		{name: "worker identity is not sha256", mutate: func(config *RuntimeConfig) { config.Nodes[0].WorkerIdentity = "worker-identity" }},
		{name: "toolchain identity is not sha256", mutate: func(config *RuntimeConfig) { config.Nodes[0].ToolchainIdentity = "toolchain-identity" }},
		{name: "Docker image is mutable", mutate: func(config *RuntimeConfig) { config.Nodes[0].Linux.DockerImage = "ubuntu:latest" }},
		{name: "Wine executable is relative", mutate: func(config *RuntimeConfig) { config.Nodes[0].Wine.WineExecutable = "wine" }},
		{name: "probe identity is not sha256", mutate: func(config *RuntimeConfig) { config.Nodes[0].ProbeIdentity = "probe-identity" }},
		{name: "cgo flag is not canonical", mutate: func(config *RuntimeConfig) { config.Nodes[0].CGOEnabled = "true" }},
		{name: "Wine cgo flag is not zero", mutate: func(config *RuntimeConfig) { config.Nodes[0].Wine.CGOEnabled = "1" }},
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

func TestBoundTargetReusesAcrossNodeExecutionPlatforms(t *testing.T) {
	bound, err := BindPortableRun(runtimeFixture(), targetFixture("target"))
	if err != nil {
		t.Fatal(err)
	}
	if err := executionFixture(UbuntuPlatform).ValidateFor(UbuntuPlatform); err != nil {
		t.Fatal(err)
	}
	if err := executionFixture(WindowsTargetPlatform).ValidateFor(WindowsTargetPlatform); err != nil {
		t.Fatal(err)
	}
	if bound.Namespace == "" {
		t.Fatal("bound namespace is empty")
	}
}

func TestTargetExecutionFieldBoundary(t *testing.T) {
	assertGoFieldNames(t, reflect.TypeFor[TargetFacts](), []string{"RepositoryID", "ObjectFormat", "CommitSHA", "TreeSHA"})
	assertGoFieldNames(t, reflect.TypeFor[ExecutionFacts](), []string{"GOOS", "GOARCH", "CGOEnabled", "TargetPlatform", "RuntimeCapability", "ModuleIdentity", "WorkloadIdentity", "CPUMilli", "MemoryBytes"})
	assertGoFieldNames(t, reflect.TypeFor[LinuxContainerBinding](), []string{"DockerExecutable", "DockerHost", "DockerServerID", "DockerImage", "DockerImageIdentity", "CgroupRoot", "RunAsUser", "DockerGroup"})
	assertGoFieldNames(t, reflect.TypeFor[WineContainerBinding](), []string{"CGOEnabled", "DockerImage", "DockerImageIdentity", "WineExecutable", "WineExecutableIdentity", "WineServerExecutable", "WineServerExecutableIdentity", "WinePrefixRoot"})
}

func TestExecutionFactsValidateTargetRuntimeAgainstSingleLinuxNode(t *testing.T) {
	node := runtimeFixture().Nodes[0]
	for _, platform := range []Platform{UbuntuPlatform, WindowsTargetPlatform} {
		facts := executionFixture(platform)
		if err := facts.ValidateFor(platform); err != nil {
			t.Fatalf("ValidateFor(%s) error = %v", platform, err)
		}
		if err := facts.ValidateForNode(node); err != nil {
			t.Fatalf("ValidateForNode(%s) error = %v", platform, err)
		}
	}
	invalidRuntime := executionFixture(WindowsTargetPlatform)
	invalidRuntime.RuntimeCapability = RuntimeUbuntuContainer
	if err := invalidRuntime.ValidateForNode(node); err == nil {
		t.Fatal("Windows target with ubuntu_container unexpectedly validated")
	}
	invalidTarget := executionFixture(UbuntuPlatform)
	invalidTarget.TargetPlatform = WindowsTargetPlatform
	if err := invalidTarget.ValidateForNode(node); err == nil {
		t.Fatal("Linux runtime with Windows target unexpectedly validated")
	}
	physicalWindows := node
	physicalWindows.Platform = WindowsTargetPlatform
	if err := executionFixture(WindowsTargetPlatform).ValidateForNode(physicalWindows); err == nil {
		t.Fatal("Wine execution unexpectedly validated on physical Windows node")
	}
	cgoMismatch := executionFixture(UbuntuPlatform)
	cgoMismatch.CGOEnabled = "1"
	if err := cgoMismatch.ValidateForNode(node); err == nil {
		t.Fatal("execution facts with mismatched cgo unexpectedly validated")
	}
	wineCgoMismatch := executionFixture(WindowsTargetPlatform)
	wineCgoMismatch.CGOEnabled = "1"
	if err := wineCgoMismatch.ValidateForNode(node); err == nil {
		t.Fatal("Wine execution facts with mismatched cgo unexpectedly validated")
	}
}

func TestExecutionBaseFactsUsesExplicitTargetRuntimeAndRequiresBatchIdentities(t *testing.T) {
	node := runtimeFixture().Nodes[0]
	for _, target := range []struct {
		platform Platform
		runtime  string
		cgo      string
	}{{UbuntuPlatform, RuntimeUbuntuContainer, node.CGOEnabled}, {WindowsTargetPlatform, RuntimeUbuntuWineContainer, node.Wine.CGOEnabled}} {
		base, err := node.ExecutionBaseFacts(target.platform, target.runtime)
		if err != nil {
			t.Fatal(err)
		}
		if base.TargetPlatform != target.platform || base.RuntimeCapability != target.runtime || base.CGOEnabled != target.cgo || base.ModuleIdentity != "" || base.WorkloadIdentity != "" {
			t.Fatalf("base facts leaked/defaulted target or batch identity: node=%+v base=%+v", node, base)
		}
		if err := base.ValidateForNode(node); err == nil {
			t.Fatal("base facts unexpectedly validated without batch identities")
		}
		complete := base
		complete.ModuleIdentity = "sha256:" + strings.Repeat("1", 64)
		complete.WorkloadIdentity = "sha256:" + strings.Repeat("2", 64)
		complete.CPUMilli = 1_000
		complete.MemoryBytes = 1 << 30
		if err := complete.ValidateForNode(node); err != nil {
			t.Fatalf("caller-completed execution facts rejected: %v", err)
		}
	}
}

func TestExecutionFactsRejectMissingResourceBudget(t *testing.T) {
	node := runtimeFixture().Nodes[0]
	for name, mutate := range map[string]func(*ExecutionFacts){
		"zero cpu":        func(facts *ExecutionFacts) { facts.CPUMilli = 0 },
		"negative cpu":    func(facts *ExecutionFacts) { facts.CPUMilli = -1 },
		"zero memory":     func(facts *ExecutionFacts) { facts.MemoryBytes = 0 },
		"negative memory": func(facts *ExecutionFacts) { facts.MemoryBytes = -1 },
	} {
		t.Run(name, func(t *testing.T) {
			facts := executionFixture(UbuntuPlatform)
			mutate(&facts)
			if err := facts.ValidateForNode(node); err == nil {
				t.Fatal("ValidateForNode unexpectedly accepted invalid resource budget")
			}
		})
	}
}

func runtimeFixture() RuntimeConfig {
	return RuntimeConfig{
		SchemaVersion: ConfigSchemaVersion, TopologyManifestPath: "/etc/super-dolphin/topology.json",
		OwnerDSNEnv: "REMOTE_CI_OWNER_DSN", ReceiptDSNEnv: "REMOTE_CI_RECEIPT_DSN",
		OwnerNamespace: "remote-ci-fixture", SSHExecutable: "/usr/bin/ssh",
		ProbeTimeoutMS: 1700, HeartbeatTTLMS: 30000, RequestTimeoutMS: 120000, RunTimeoutMS: 45000, CleanupTimeoutMS: 3100, WorkerTimeoutMS: 90000,
		Nodes: []NodeBinding{runtimeUbuntuWineNode(runtimeNodeID, "/srv/ci/ubuntu-physical")},
	}
}

const runtimeNodeID = "ubuntu-physical-1"

func runtimeUbuntuWineNode(nodeID, privateRoot string) NodeBinding {
	return NodeBinding{
		NodeID: nodeID, Platform: UbuntuPlatform, CGOEnabled: "0", Endpoint: "ssh://" + strings.ToLower(nodeID),
		ProbeExecutable: "/opt/super-dolphin/probe", ProbeConfigPath: "/etc/super-dolphin/probe.json", ProbeIdentity: runtimeProbeIdentity(nodeID),
		RunnerExecutable: "/opt/super-dolphin/linux_runner_daemon.sh", RunnerIdentity: "sha256:" + strings.Repeat("5", 64),
		WorkerExecutable: "/opt/super-dolphin/worker", WorkerIdentity: "sha256:" + strings.Repeat("6", 64),
		RepositoryRoot: privateRoot + "/worktree", ObjectStoreRoot: "/srv/ci/shared/object-store",
		WorktreeRoot: privateRoot + "/worktree", CachePublicationRoot: "/srv/ci/shared/cache-publication",
		PrivateOverlayRoot: privateRoot + "/overlay", TempRoot: privateRoot + "/temp",
		GoExecutable: "/usr/local/go/bin/go", ToolchainIdentity: "sha256:" + strings.Repeat("c", 64),
		Linux: &LinuxContainerBinding{DockerExecutable: "/usr/bin/docker", DockerHost: "unix:///run/docker.sock", DockerServerID: "docker-server-fixture", DockerImage: "sha256:" + strings.Repeat("d", 64), DockerImageIdentity: "sha256:" + strings.Repeat("e", 64), CgroupRoot: "/sys/fs/cgroup/super-dolphin", RunAsUser: "ci", DockerGroup: "docker"},
		Wine: &WineContainerBinding{
			CGOEnabled: "0", DockerImage: "sha256:" + strings.Repeat("1", 64), DockerImageIdentity: "sha256:" + strings.Repeat("2", 64),
			WineExecutable: "/usr/lib/wine/wine64", WineExecutableIdentity: "sha256:" + strings.Repeat("3", 64),
			WineServerExecutable: "/usr/lib/wine/wineserver64", WineServerExecutableIdentity: "sha256:" + strings.Repeat("4", 64),
			WinePrefixRoot: privateRoot + "/wine-prefix",
		},
	}
}

func runtimeProbeIdentity(_ string) string {
	return "sha256:" + strings.Repeat("a", 64)
}

func duplicateRuntimeField(encoded []byte) []byte {
	return bytes.Replace(encoded, []byte(`"owner_namespace":"remote-ci-fixture"`), []byte(`"owner_namespace":"remote-ci-fixture","owner_namespace":"remote-ci-fixture"`), 1)
}

func targetFixture(repositoryID string) TargetFacts {
	return TargetFacts{RepositoryID: repositoryID, ObjectFormat: "sha1", CommitSHA: strings.Repeat("a", 40), TreeSHA: strings.Repeat("b", 40)}
}

func executionFixture(platform Platform) ExecutionFacts {
	goos := "linux"
	runtime := RuntimeUbuntuContainer
	if platform == WindowsTargetPlatform {
		goos = "windows"
		runtime = RuntimeUbuntuWineContainer
	}
	return ExecutionFacts{GOOS: goos, GOARCH: "amd64", CGOEnabled: "0", TargetPlatform: platform, RuntimeCapability: runtime, ModuleIdentity: "sha256:" + strings.Repeat("1", 64), WorkloadIdentity: "sha256:" + strings.Repeat("2", 64), CPUMilli: 1_000, MemoryBytes: 1 << 30}
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

func mustJSONObjectField(t *testing.T, encoded []byte, nodeID, field string) []byte {
	t.Helper()
	var node map[string]json.RawMessage
	if err := json.Unmarshal(mustJSONNode(t, encoded, nodeID), &node); err != nil {
		t.Fatal(err)
	}
	value, ok := node[field]
	if !ok {
		t.Fatalf("node %q field %q missing", nodeID, field)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(value, &object); err != nil {
		t.Fatalf("node %q field %q is not an object: %v", nodeID, field, err)
	}
	data, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	return data
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

func assertGoFieldNames(t *testing.T, typ reflect.Type, expected []string) {
	t.Helper()
	actual := make([]string, typ.NumField())
	for index := range actual {
		actual[index] = typ.Field(index).Name
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("%s fields = %v, want %v", typ.Name(), actual, expected)
	}
}

func TestRuntimeConfigABIRejectsForbiddenLegacyNames(t *testing.T) {
	fields := append(ConfigFields(), NodeBindingFields()...)
	fields = append(fields, WineContainerFields()...)
	for _, field := range fields {
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
