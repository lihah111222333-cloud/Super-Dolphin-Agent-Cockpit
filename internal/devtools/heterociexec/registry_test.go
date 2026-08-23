package heterociexec

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociworkflow"
)

type testBatchExecutor struct{ id string }

func (executor *testBatchExecutor) ExecuteBatch(context.Context, heterociworkflow.ExecutionBatch) (heterociworkflow.BatchResult, error) {
	return heterociworkflow.BatchResult{}, nil
}

type recordingEncoder struct {
	mu       sync.Mutex
	platform heteroci.Platform
	argv     []string
}

func (encoder *recordingEncoder) Encode(platform heteroci.Platform, argv []string) (Command, error) {
	encoder.mu.Lock()
	defer encoder.mu.Unlock()
	encoder.platform = platform
	encoder.argv = slices.Clone(argv)
	return Command{Platform: platform, Argv: slices.Clone(argv)}, nil
}

func validBindings(encoder CommandEncoder) []Binding {
	return []Binding{
		{
			Kind:     heteroci.ExecutorKindLocalNode,
			Key:      heterociworkflow.ExecutorKey{Kind: heteroci.ExecutorKindLocalNode, NodeID: "node-a"},
			Platform: heteroci.PlatformLinuxAMD64,
			Executor: &testBatchExecutor{id: "local"},
			Encoder:  encoder,
		},
	}
}

func registryBindingPlan(keys ...heterociworkflow.ExecutorKey) heterociworkflow.ExecutorBindingPlan {
	bindings := make([]heterociworkflow.ExecutorBinding, 0, len(keys))
	for _, key := range keys {
		bindings = append(bindings, heterociworkflow.ExecutorBinding{NodeID: key.NodeID, ExecutorKind: key.Kind, BatchIDs: []heterociworkflow.BatchID{heterociworkflow.BatchID("batch-" + key.NodeID)}})
	}
	return heterociworkflow.ExecutorBindingPlan{Bindings: bindings}
}

func TestRegistryRequiresValidLocalBindings(t *testing.T) {
	encoder := StrictCommandEncoder{}
	tests := []struct {
		name     string
		bindings []Binding
		want     string
	}{
		{name: "missing binding", bindings: nil, want: "at least one binding"},
		{name: "duplicate local", bindings: append(validBindings(encoder), validBindings(encoder)[0]), want: "duplicate executor key"},
		{name: "unknown kind", bindings: append(validBindings(encoder), Binding{Kind: heteroci.ExecutorKind("other"), Platform: heteroci.PlatformLinuxAMD64, Executor: &testBatchExecutor{id: "other"}, Encoder: encoder}), want: "unsupported executor kind"},
		{name: "unsupported platform", bindings: func() []Binding {
			bindings := validBindings(encoder)
			bindings[0].Platform = heteroci.Platform("freebsd/amd64")
			return bindings
		}(), want: "unsupported platform"},
		{name: "nil executor", bindings: func() []Binding {
			bindings := validBindings(encoder)
			bindings[0].Executor = (*testBatchExecutor)(nil)
			return bindings
		}(), want: "executor is required"},
		{name: "missing encoder", bindings: func() []Binding {
			bindings := validBindings(encoder)
			bindings[0].Encoder = nil
			return bindings
		}(), want: "encoder is required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewRegistry(test.bindings)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("NewRegistry error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestRegistryBindsOneExecutorPerKindAndRejectsInvalidKeys(t *testing.T) {
	registry, err := NewRegistry(validBindings(StrictCommandEncoder{}))
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	localKey := heterociworkflow.ExecutorKey{Kind: heteroci.ExecutorKindLocalNode, NodeID: "node-a"}
	bound, err := registry.Bind(registryBindingPlan(localKey))
	if err != nil {
		t.Fatalf("Bind(local) error = %v", err)
	}
	first := bound[localKey]
	if first == nil {
		t.Fatal("Bind(local) omitted the requested executor")
	}
	if _, err := registry.Bind(registryBindingPlan(heterociworkflow.ExecutorKey{Kind: heteroci.ExecutorKindLocalNode})); err == nil {
		t.Fatal("Bind accepted local key without NodeID")
	}
	if _, err := registry.Bind(registryBindingPlan(heterociworkflow.ExecutorKey{Kind: heteroci.ExecutorKind("other"), NodeID: "node-other"})); err == nil {
		t.Fatal("Bind accepted unsupported executor kind")
	}
}

func TestRegistryRequiresDistinctExecutorPerLocalNode(t *testing.T) {
	encoder := StrictCommandEncoder{}
	firstExecutor := &testBatchExecutor{id: "node-a"}
	secondExecutor := &testBatchExecutor{id: "node-b"}
	bindings := []Binding{
		{Kind: heteroci.ExecutorKindLocalNode, Key: heterociworkflow.ExecutorKey{Kind: heteroci.ExecutorKindLocalNode, NodeID: "node-a"}, Platform: heteroci.PlatformLinuxAMD64, Executor: firstExecutor, Encoder: encoder},
		{Kind: heteroci.ExecutorKindLocalNode, Key: heterociworkflow.ExecutorKey{Kind: heteroci.ExecutorKindLocalNode, NodeID: "node-b"}, Platform: heteroci.PlatformLinuxAMD64, Executor: secondExecutor, Encoder: encoder},
	}
	registry, err := NewRegistry(bindings)
	if err != nil {
		t.Fatalf("NewRegistry(two local nodes) error = %v", err)
	}
	bound, err := registry.Bind(registryBindingPlan(bindings[0].Key, bindings[1].Key))
	if err != nil {
		t.Fatalf("Bind(nodes) error = %v", err)
	}
	gotA, gotB := bound[bindings[0].Key], bound[bindings[1].Key]
	if gotA == gotB {
		t.Fatal("distinct local node keys shared one executor")
	}
	if _, err := registry.Bind(registryBindingPlan(heterociworkflow.ExecutorKey{Kind: heteroci.ExecutorKindLocalNode, NodeID: "node-unknown"})); err == nil {
		t.Fatal("Bind accepted an unknown local node key")
	}
	if _, err := registry.EncodeCommand(heteroci.ExecutorKindLocalNode, []string{"tool"}); err == nil {
		t.Fatal("kind-only EncodeCommand silently selected one of multiple local nodes")
	}
	shared := &testBatchExecutor{id: "shared"}
	sharedBindings := []Binding{
		{Kind: heteroci.ExecutorKindLocalNode, Key: heterociworkflow.ExecutorKey{Kind: heteroci.ExecutorKindLocalNode, NodeID: "node-a"}, Platform: heteroci.PlatformLinuxAMD64, Executor: shared, Encoder: encoder},
		{Kind: heteroci.ExecutorKindLocalNode, Key: heterociworkflow.ExecutorKey{Kind: heteroci.ExecutorKindLocalNode, NodeID: "node-b"}, Platform: heteroci.PlatformLinuxAMD64, Executor: shared, Encoder: encoder},
	}
	if _, err := NewRegistry(sharedBindings); err == nil || !strings.Contains(err.Error(), "reused across keys") {
		t.Fatalf("shared executor error = %v, want reuse rejection", err)
	}
}

func TestRegistryForManifestAllowsLocalOnly(t *testing.T) {
	local := testLocalBinding("local", heteroci.PlatformLinuxAMD64)
	registry, err := NewRegistryForManifest(testRegistryManifest(), []Binding{local})
	if err != nil {
		t.Fatalf("local-only registry error = %v", err)
	}
	if _, err := registry.Bind(registryBindingPlan(local.Key)); err != nil {
		t.Fatalf("Bind(disabled local) error = %v", err)
	}
}

func TestRegistryForManifestRejectsExtraBindingAndIdentityDrift(t *testing.T) {
	local := testLocalBinding("local", heteroci.PlatformLinuxAMD64)
	extra := append([]Binding{local}, Binding{
		Kind:     heteroci.ExecutorKindLocalNode,
		Key:      heterociworkflow.ExecutorKey{Kind: heteroci.ExecutorKindLocalNode, NodeID: "node-extra"},
		Platform: heteroci.PlatformLinuxAMD64,
		Executor: &testBatchExecutor{id: "extra"},
		Encoder:  StrictCommandEncoder{},
	})
	if _, err := NewRegistryForManifest(testRegistryManifest(), extra); err == nil {
		t.Fatal("manifest accepted an extra local binding")
	}
	badAuthority := testRegistryManifest()
	badAuthority.Authority = "REMOTE_AUTHORITATIVE"
	if _, err := NewRegistryForManifest(badAuthority, []Binding{local}); err == nil || !strings.Contains(err.Error(), "manifest validation") {
		t.Fatalf("invalid manifest authority error = %v", err)
	}
	badPlatform := testRegistryManifest()
	badPlatform.Nodes[0].Platform = heteroci.PlatformWindowsAMD64
	if _, err := NewRegistryForManifest(badPlatform, []Binding{local}); err == nil {
		t.Fatal("manifest/node platform mismatch unexpectedly accepted")
	}
}

func TestRegistryCommandEncodingIsExplicitAndNoFallback(t *testing.T) {
	encoder := &recordingEncoder{}
	registry, err := NewRegistry(validBindings(encoder))
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	argv := []string{"tool", "two words"}
	got, err := registry.EncodeCommand(heteroci.ExecutorKindLocalNode, argv)
	if err != nil {
		t.Fatalf("EncodeCommand() error = %v", err)
	}
	if got.Platform != heteroci.PlatformLinuxAMD64 || !slices.Equal(got.Argv, argv) {
		t.Fatalf("encoded command = %#v, want platform and argv preserved", got)
	}
	if encoder.platform != heteroci.PlatformLinuxAMD64 || !slices.Equal(encoder.argv, argv) {
		t.Fatalf("injected encoder observed platform=%q argv=%#v", encoder.platform, encoder.argv)
	}
	if _, err := registry.EncodeCommand(heteroci.ExecutorKind("other"), argv); err == nil {
		t.Fatal("EncodeCommand accepted an unregistered kind")
	}
}

func TestRegistryCommandEncodingForExactKey(t *testing.T) {
	encoder := &recordingEncoder{}
	registry, err := NewRegistry(validBindings(encoder))
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	perKey, err := registry.EncodeCommandForKey(heterociworkflow.ExecutorKey{Kind: heteroci.ExecutorKindLocalNode, NodeID: "node-a"}, []string{"tool", "arg"})
	if err != nil {
		t.Fatalf("EncodeCommandForKey() error = %v", err)
	}
	if perKey.Platform != heteroci.PlatformLinuxAMD64 || !slices.Equal(perKey.Argv, []string{"tool", "arg"}) {
		t.Fatalf("per-key command = %#v, want exact local binding", perKey)
	}
}

func TestStrictCommandEncoderPreservesUnixArgvBoundaries(t *testing.T) {
	argv := []string{"tool", "", "two words", `quote"slash\`, "汉字"}
	for _, platform := range []heteroci.Platform{heteroci.PlatformLinuxAMD64, heteroci.PlatformWindowsAMD64} {
		t.Run(string(platform), func(t *testing.T) {
			encoded, err := (StrictCommandEncoder{}).Encode(platform, argv)
			if err != nil {
				t.Fatalf("Encode() error = %v", err)
			}
			if !slices.Equal(encoded.Argv, argv) {
				t.Fatalf("argv = %#v, want %#v", encoded.Argv, argv)
			}
			encoded.Argv[1] = "mutated"
			if argv[1] != "" {
				t.Fatal("Encode() did not copy argv")
			}
		})
	}
}

func TestStrictCommandEncoderRejectsUnsupportedAndUnsafeArgv(t *testing.T) {
	tests := []struct {
		name     string
		platform heteroci.Platform
		argv     []string
		want     string
	}{
		{name: "unsupported platform", platform: heteroci.Platform("freebsd/amd64"), argv: []string{"tool"}, want: "unsupported platform"},
		{name: "empty argv", platform: heteroci.PlatformLinuxAMD64, argv: nil, want: "argv must not be empty"},
		{name: "empty executable", platform: heteroci.PlatformLinuxAMD64, argv: []string{""}, want: "executable argument must not be empty"},
		{name: "nul", platform: heteroci.PlatformWindowsAMD64, argv: []string{"tool", "bad\x00arg"}, want: "NUL"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := (StrictCommandEncoder{}).Encode(test.platform, test.argv)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Encode() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestCommandEncoderFuncRejectsNil(t *testing.T) {
	var encoder CommandEncoderFunc
	if _, err := encoder.Encode(heteroci.PlatformLinuxAMD64, []string{"tool"}); !errors.Is(err, ErrCommandEncoding) {
		t.Fatalf("nil CommandEncoderFunc error = %v, want ErrCommandEncoding", err)
	}
}

func testRegistryManifest() heteroci.Manifest {
	manifest := heteroci.Manifest{
		SchemaVersion: heteroci.ManifestSchemaVersion,
		Authority:     heteroci.AuthorityLocalNonAuthoritative,
		Nodes: []heteroci.NodeConfig{{
			NodeID:        "node-a",
			Endpoint:      "https://node-a.invalid/runner",
			ProbeIdentity: "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			Platform:      heteroci.PlatformLinuxAMD64,
			Capabilities: []heteroci.CapabilityConfig{{
				Runtime:     heteroci.RuntimeUbuntuContainer,
				Image:       "super-dolphin-ci-runner:ubuntu",
				ImageDigest: "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			}},
		}},
	}
	return manifest
}

func testLocalBinding(id string, platform heteroci.Platform) Binding {
	return Binding{
		Kind:     heteroci.ExecutorKindLocalNode,
		Key:      heterociworkflow.ExecutorKey{Kind: heteroci.ExecutorKindLocalNode, NodeID: "node-a"},
		Platform: platform,
		Executor: &testBatchExecutor{id: id},
		Encoder:  StrictCommandEncoder{},
	}
}

var _ heterociworkflow.ExecutorBinder = (*Registry)(nil)
var _ CommandEncoder = (StrictCommandEncoder{})
