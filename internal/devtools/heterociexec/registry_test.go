package heterociexec

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode/utf16"

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
		{
			Kind:     heteroci.ExecutorKindAliyunECI,
			Key:      heterociworkflow.ExecutorKey{Kind: heteroci.ExecutorKindAliyunECI, ECITier: heteroci.ECITier2C4GiB},
			Platform: heteroci.PlatformLinuxAMD64,
			Executor: &testBatchExecutor{id: "eci"},
			Encoder:  encoder,
		},
	}
}

func TestRegistryRequiresExactlyOneBindingPerExecutorKind(t *testing.T) {
	encoder := StrictCommandEncoder{}
	tests := []struct {
		name     string
		bindings []Binding
		want     string
	}{
		{name: "missing eci", bindings: validBindings(encoder)[:1], want: "missing executor kind"},
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
	first, err := registry.Bind(localKey)
	if err != nil {
		t.Fatalf("Bind(local) error = %v", err)
	}
	second, err := registry.Bind(localKey)
	if err != nil {
		t.Fatalf("Bind(local repeat) error = %v", err)
	}
	if first != second {
		t.Fatalf("repeated Bind returned different executor instances: %p vs %p", first, second)
	}
	if _, err := registry.Bind(heterociworkflow.ExecutorKey{Kind: heteroci.ExecutorKindLocalNode}); err == nil {
		t.Fatal("Bind accepted local key without NodeID")
	}
	if _, err := registry.Bind(heterociworkflow.ExecutorKey{Kind: heteroci.ExecutorKindAliyunECI, NodeID: "unexpected", ECITier: heteroci.ECITier2C4GiB}); err == nil {
		t.Fatal("Bind accepted ECI key with NodeID")
	}
	if _, err := registry.Bind(heterociworkflow.ExecutorKey{Kind: heteroci.ExecutorKindAliyunECI, ECITier: heteroci.ECIResourceTier("aliyun-eci/16c32g")}); err == nil {
		t.Fatal("Bind accepted unsupported ECI tier")
	}
	if _, err := registry.Bind(heterociworkflow.ExecutorKey{Kind: heteroci.ExecutorKind("other")}); err == nil {
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
		{Kind: heteroci.ExecutorKindAliyunECI, Key: heterociworkflow.ExecutorKey{Kind: heteroci.ExecutorKindAliyunECI, ECITier: heteroci.ECITier2C4GiB}, Platform: heteroci.PlatformLinuxAMD64, Executor: &testBatchExecutor{id: "eci"}, Encoder: encoder},
	}
	registry, err := NewRegistry(bindings)
	if err != nil {
		t.Fatalf("NewRegistry(two local nodes) error = %v", err)
	}
	gotA, err := registry.Bind(bindings[0].Key)
	if err != nil {
		t.Fatalf("Bind(node-a) error = %v", err)
	}
	gotB, err := registry.Bind(bindings[1].Key)
	if err != nil {
		t.Fatalf("Bind(node-b) error = %v", err)
	}
	if gotA == gotB {
		t.Fatal("distinct local node keys shared one executor")
	}
	if _, err := registry.Bind(heterociworkflow.ExecutorKey{Kind: heteroci.ExecutorKindLocalNode, NodeID: "node-unknown"}); err == nil {
		t.Fatal("Bind accepted an unknown local node key")
	}
	if _, err := registry.EncodeCommand(heteroci.ExecutorKindLocalNode, []string{"tool"}); err == nil {
		t.Fatal("kind-only EncodeCommand silently selected one of multiple local nodes")
	}
	shared := &testBatchExecutor{id: "shared"}
	sharedBindings := []Binding{
		{Kind: heteroci.ExecutorKindLocalNode, Key: heterociworkflow.ExecutorKey{Kind: heteroci.ExecutorKindLocalNode, NodeID: "node-a"}, Platform: heteroci.PlatformLinuxAMD64, Executor: shared, Encoder: encoder},
		{Kind: heteroci.ExecutorKindLocalNode, Key: heterociworkflow.ExecutorKey{Kind: heteroci.ExecutorKindLocalNode, NodeID: "node-b"}, Platform: heteroci.PlatformLinuxAMD64, Executor: shared, Encoder: encoder},
		{Kind: heteroci.ExecutorKindAliyunECI, Key: heterociworkflow.ExecutorKey{Kind: heteroci.ExecutorKindAliyunECI, ECITier: heteroci.ECITier2C4GiB}, Platform: heteroci.PlatformLinuxAMD64, Executor: &testBatchExecutor{id: "eci-shared-check"}, Encoder: encoder},
	}
	if _, err := NewRegistry(sharedBindings); err == nil || !strings.Contains(err.Error(), "reused across keys") {
		t.Fatalf("shared executor error = %v, want reuse rejection", err)
	}
}

func TestRegistryForManifestDisabledAllowsLocalOnly(t *testing.T) {
	local := testLocalBinding("local", heteroci.PlatformLinuxAMD64)
	registry, err := NewRegistryForManifest(testRegistryManifest(false), []Binding{local})
	if err != nil {
		t.Fatalf("disabled cloud_overflow local-only registry error = %v", err)
	}
	if _, err := registry.Bind(local.Key); err != nil {
		t.Fatalf("Bind(disabled local) error = %v", err)
	}
}

func TestRegistryForManifestEnabledRequiresECI(t *testing.T) {
	local := testLocalBinding("local", heteroci.PlatformLinuxAMD64)
	if _, err := NewRegistryForManifest(testRegistryManifest(true), []Binding{local}); err == nil || !strings.Contains(err.Error(), "missing executor kind") {
		t.Fatalf("enabled cloud_overflow missing ECI error = %v, want missing executor kind", err)
	}
	allECI := testECIBindings()
	if _, err := NewRegistryForManifest(testRegistryManifest(true), append([]Binding{local}, allECI...)); err != nil {
		t.Fatalf("enabled cloud_overflow complete registry error = %v", err)
	}
	if _, err := NewRegistryForManifest(testRegistryManifest(true), append([]Binding{local}, allECI[:2]...)); err == nil || !strings.Contains(err.Error(), "missing executor key") {
		t.Fatalf("enabled cloud_overflow missing ECI tier error = %v, want missing executor key", err)
	}
}

func TestRegistryForManifestRejectsExtraECIAndIdentityDrift(t *testing.T) {
	local := testLocalBinding("local", heteroci.PlatformLinuxAMD64)
	extra := append([]Binding{local}, testECIBindings()...)
	if _, err := NewRegistryForManifest(testRegistryManifest(false), extra); err == nil {
		t.Fatal("disabled cloud_overflow accepted an extra ECI binding")
	}
	badAuthority := testRegistryManifest(false)
	badAuthority.Authority = "REMOTE_AUTHORITATIVE"
	if _, err := NewRegistryForManifest(badAuthority, []Binding{local}); err == nil || !strings.Contains(err.Error(), "manifest validation") {
		t.Fatalf("invalid manifest authority error = %v", err)
	}
	badPlatform := testRegistryManifest(false)
	badPlatform.Nodes[0].Platform = heteroci.PlatformWindowsAMD64
	if _, err := NewRegistryForManifest(badPlatform, []Binding{local}); err == nil {
		t.Fatal("manifest/node platform mismatch unexpectedly accepted")
	}
}

func TestRegistryForManifestSupportsWindowsLocalEncoding(t *testing.T) {
	manifest := testRegistryManifest(false)
	manifest.Nodes[0].Platform = heteroci.PlatformWindowsAMD64
	manifest.Nodes[0].Capabilities = []heteroci.CapabilityConfig{{Runtime: heteroci.RuntimeWindowsNative}}
	windowsBinding := testLocalBinding("windows", heteroci.PlatformWindowsAMD64)
	registry, err := NewRegistryForManifest(manifest, []Binding{windowsBinding})
	if err != nil {
		t.Fatalf("Windows local manifest registry error = %v", err)
	}
	encoded, err := registry.EncodeCommandForKey(windowsBinding.Key, []string{"tool.exe", "two words"})
	if err != nil || encoded.CommandBase64 == "" {
		t.Fatalf("Windows registry encoding = %#v, err=%v", encoded, err)
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
	perKey, err := registry.EncodeCommandForKey(heterociworkflow.ExecutorKey{Kind: heteroci.ExecutorKindAliyunECI, ECITier: heteroci.ECITier2C4GiB}, []string{"tool", "arg"})
	if err != nil {
		t.Fatalf("EncodeCommandForKey() error = %v", err)
	}
	if perKey.Platform != heteroci.PlatformLinuxAMD64 || !slices.Equal(perKey.Argv, []string{"tool", "arg"}) {
		t.Fatalf("per-key command = %#v, want exact ECI binding", perKey)
	}
}

func TestStrictCommandEncoderPreservesUnixArgvBoundaries(t *testing.T) {
	argv := []string{"tool", "", "two words", `quote"slash\`, "汉字"}
	for _, platform := range []heteroci.Platform{heteroci.PlatformLinuxAMD64, heteroci.PlatformDarwinAMD64, heteroci.PlatformDarwinARM64} {
		t.Run(string(platform), func(t *testing.T) {
			encoded, err := (StrictCommandEncoder{}).Encode(platform, argv)
			if err != nil {
				t.Fatalf("Encode() error = %v", err)
			}
			if !slices.Equal(encoded.Argv, argv) {
				t.Fatalf("argv = %#v, want %#v", encoded.Argv, argv)
			}
			if encoded.CommandLine != "" || encoded.CommandBase64 != "" {
				t.Fatalf("Unix command unexpectedly has string encoding: %#v", encoded)
			}
			encoded.Argv[1] = "mutated"
			if argv[1] != "" {
				t.Fatal("Encode() did not copy argv")
			}
		})
	}
}

func TestStrictCommandEncoderWindowsUsesUTF16LECommandBase64(t *testing.T) {
	argv := []string{"tool.exe", "", "two words", `a"b`, `C:\path\`, "汉字"}
	wantCommandLine := `tool.exe "" "two words" "a\"b" C:\path\ 汉字`
	encoded, err := (StrictCommandEncoder{}).Encode(heteroci.PlatformWindowsAMD64, argv)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if encoded.CommandLine != wantCommandLine {
		t.Fatalf("CommandLine = %q, want %q", encoded.CommandLine, wantCommandLine)
	}
	wantUTF16 := expectedUTF16LEBytes(wantCommandLine)
	if encoded.CommandBase64 != base64.StdEncoding.EncodeToString(wantUTF16) {
		t.Fatalf("CommandBase64 = %q, want UTF-16LE base64 of canonical command", encoded.CommandBase64)
	}
	if !slices.Equal(encoded.Argv, argv) {
		t.Fatalf("Windows argv = %#v, want %#v", encoded.Argv, argv)
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

func testRegistryManifest(cloudOverflow bool) heteroci.Manifest {
	manifest := heteroci.Manifest{
		SchemaVersion: heteroci.ManifestSchemaVersion,
		Authority:     heteroci.AuthorityLocalNonAuthoritative,
		CloudOverflow: heteroci.CloudOverflowConfig{Enabled: cloudOverflow},
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
	if cloudOverflow {
		manifest.CloudOverflow.Provider = heteroci.CloudProviderAliyunECI
		manifest.CloudOverflow.Platform = heteroci.PlatformLinuxAMD64
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

func testECIBindings() []Binding {
	bindings := make([]Binding, 0, 3)
	for index, tier := range []heteroci.ECIResourceTier{heteroci.ECITier2C4GiB, heteroci.ECITier4C8GiB, heteroci.ECITier8C16GiB} {
		bindings = append(bindings, Binding{
			Kind:     heteroci.ExecutorKindAliyunECI,
			Key:      heterociworkflow.ExecutorKey{Kind: heteroci.ExecutorKindAliyunECI, ECITier: tier},
			Platform: heteroci.PlatformLinuxAMD64,
			Executor: &testBatchExecutor{id: "eci-" + string(rune('a'+index))},
			Encoder:  StrictCommandEncoder{},
		})
	}
	return bindings
}

func expectedUTF16LEBytes(value string) []byte {
	units := utf16.Encode([]rune(value))
	bytes := make([]byte, len(units)*2)
	for index, unit := range units {
		binary.LittleEndian.PutUint16(bytes[index*2:], unit)
	}
	return bytes
}

var _ heterociworkflow.ExecutorBinder = (*Registry)(nil)
var _ CommandEncoder = (StrictCommandEncoder{})
