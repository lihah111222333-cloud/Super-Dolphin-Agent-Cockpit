package remoteciruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/shardresource"
)

type PortableExecutionRequest = remoteci.PortableExecutionRequest
type PortableExecutionArtifact = remoteci.PortableExecutionArtifact
type PortableExecutionEngine = remoteci.PortableExecutionEngine
type PortableExecutionTarget = remoteci.PortableExecutionTarget
type PortableWorkloadManifest = remoteci.PortableWorkloadManifest
type PortableExecutionAuthority = remoteci.PortableExecutionAuthority
type PortableExecutionAudit = remoteci.PortableExecutionAudit
type RuntimeArtifactProducerReceipt = remoteci.RuntimeArtifactProducerReceipt
type RepositoryWorkloadManifest = remoteci.RepositoryWorkloadManifest
type RepositoryWorkloadModule = remoteci.RepositoryWorkloadModule
type RepositoryWorkloadProfile = remoteci.RepositoryWorkloadProfile
type RepositoryWorkload = remoteci.RepositoryWorkload
type GitTreeBlob = remoteci.GitTreeBlob

const (
	PortableExecutionRuntimeArtifactContractID = remoteci.PortableExecutionRuntimeArtifactContractID
	PortableExecutionRuntimeArtifactKind       = remoteci.PortableExecutionRuntimeArtifactKind
	PortableExecutionRequestSchema             = remoteci.PortableExecutionRequestSchema
	PortableExecutionContractID                = remoteci.PortableExecutionContractID
	PortableWorkloadManifestSchemaVersion      = remoteci.PortableWorkloadManifestSchemaVersion
	RuntimeArtifactBuilderReceiptSchema        = remoteci.RuntimeArtifactBuilderReceiptSchema
	RepositoryWorkloadKindGoTest               = remoteci.RepositoryWorkloadKindGoTest
	RepositoryWorkloadPlatformLinuxAMD64       = remoteci.RepositoryWorkloadPlatformLinuxAMD64
	RepositoryWorkloadRuntimeUbuntuContainer   = remoteci.RepositoryWorkloadRuntimeUbuntuContainer
	RepositoryWorkloadExecutionPolicyNormal    = remoteci.RepositoryWorkloadExecutionPolicyNormal
)

// TestPortableExecutionPreflight 聚合 side-effect-free binder 的 host RED/GREEN 证据。
func TestPortableExecutionPreflight(t *testing.T) {
	t.Run("binds_read_only_snapshots_in_order", testPortableExecutionPreflightBindsReadOnlySnapshotsInOrder)
	t.Run("invalid_request_performs_no_IO", testPortableExecutionPreflightInvalidRequestPerformsNoIO)
	t.Run("rejects_external_identity_drift", testPortableExecutionPreflightRejectsExternalIdentityDrift)
	t.Run("dynamic_binding_field_guard", testPortableExecutionPreflightDynamicBindingFieldGuard)
	t.Run("preserves_read_and_close_errors", testPortableExecutionPreflightPreservesReadAndCloseErrors)
}

func testPortableExecutionPreflightBindsReadOnlySnapshotsInOrder(t *testing.T) {
	fixture := newPortableExecutionPreflightFixture(t)
	binding, err := BindPortableExecutionPreflight(context.Background(), fixture.request, fixture.dependencies())
	if err != nil {
		t.Fatalf("BindPortableExecutionPreflight() error = %v", err)
	}
	assertPortableExecutionPreflightBinding(t, binding, fixture)
	assertPortableExecutionPreflightOrder(t, fixture)
}

func assertPortableExecutionPreflightBinding(t *testing.T, binding PortableExecutionPreflightBinding, fixture *portableExecutionPreflightFixture) {
	t.Helper()
	if binding.BindingDigest == "" || !validPortableDigest(binding.BindingDigest) {
		t.Fatalf("binding digest = %q", binding.BindingDigest)
	}
	if binding.ProfileCatalog.CatalogDigest == "" || binding.Manifest.RepositoryID != fixture.request.Target.RepositoryID {
		t.Fatalf("binding omitted mapped manifest facts: %#v", binding)
	}
	if binding.RuntimeArtifact.InputSHA256 != fixture.receipt.InputSHA256 || binding.RuntimeArtifact.InputSizeBytes != fixture.receipt.InputSizeBytes {
		t.Fatalf("runtime input provenance = %#v, want receipt provenance", binding.RuntimeArtifact)
	}
}

func assertPortableExecutionPreflightOrder(t *testing.T, fixture *portableExecutionPreflightFixture) {
	t.Helper()
	wantOrder := []string{
		"config.open", "config.read", "config.close",
		"ledger.open", "ledger.identity", "ledger.schema", "ledger.accepted", "ledger.close",
		"git.resolve", "git.blob",
		"receipt.open", "receipt.read", "receipt.close",
		"artifact.open", "artifact.read", "artifact.close",
	}
	if !reflect.DeepEqual(fixture.order, wantOrder) {
		t.Fatalf("preflight order = %v, want %v", fixture.order, wantOrder)
	}
	for name, opens := range map[string]int{
		"config": fixture.config.opens, "ledger": fixture.ledger.opens,
		"receipt":  fixture.artifacts.opensByReference[fixture.request.Engine.Artifact.ReceiptKey],
		"artifact": fixture.artifacts.opensByReference[fixture.request.Engine.Artifact.ArtifactKey],
	} {
		if opens != 1 {
			t.Fatalf("%s opens = %d, want exactly one", name, opens)
		}
	}
}

func testPortableExecutionPreflightInvalidRequestPerformsNoIO(t *testing.T) {
	request := portableExecutionPreflightRequestFixture(t)
	assertEveryInvalidPortableExecutionLeafHasNoPreflightIO(t, request, reflect.ValueOf(&request).Elem(), nil)
}

func testPortableExecutionPreflightRejectsExternalIdentityDrift(t *testing.T) {
	assertPortableExecutionPreflightRejectsDrifts(t, portableExecutionAuthorityDrifts())
	assertPortableExecutionPreflightRejectsDrifts(t, portableExecutionArtifactDrifts())
}

func portableExecutionAuthorityDrifts() map[string]func(*portableExecutionPreflightFixture) {
	return map[string]func(*portableExecutionPreflightFixture){
		"config bytes": func(value *portableExecutionPreflightFixture) { value.config.data = append(value.config.data, '\n') },
		"config accepted region": func(value *portableExecutionPreflightFixture) {
			value.config.data = []byte(strings.Replace(string(value.config.data), `"region_id":"cn-shenzhen"`, `"region_id":"cn-other"`, 1))
			value.request.Authority.ConfigSHA256 = portableExecutionPreflightBytesDigest(value.config.data)
		},
		"ledger identity":     func(value *portableExecutionPreflightFixture) { value.ledger.identity = "other-ledger" },
		"ledger schema":       func(value *portableExecutionPreflightFixture) { value.ledger.schema = 22 },
		"accepted generation": func(value *portableExecutionPreflightFixture) { value.ledger.accepted.Generation++ },
		"accepted authority": func(value *portableExecutionPreflightFixture) {
			value.ledger.accepted.StateSHA256 = portableExecutionDigest("9")
		},
	}
}

func portableExecutionArtifactDrifts() map[string]func(*portableExecutionPreflightFixture) {
	return map[string]func(*portableExecutionPreflightFixture){
		"target commit tree": func(value *portableExecutionPreflightFixture) { value.git.treeSHA = strings.Repeat("9", 40) },
		"manifest mode":      func(value *portableExecutionPreflightFixture) { value.git.blob.Mode = "120000" },
		"manifest bytes": func(value *portableExecutionPreflightFixture) {
			value.git.blob.Data = append(value.git.blob.Data, '\n')
		},
		"receipt bytes": func(value *portableExecutionPreflightFixture) {
			value.artifacts.dataByReference[value.request.Engine.Artifact.ReceiptKey] = []byte("{}")
		},
		"artifact bytes": func(value *portableExecutionPreflightFixture) {
			value.artifacts.dataByReference[value.request.Engine.Artifact.ArtifactKey] = []byte("drift")
		},
	}
}

func assertPortableExecutionPreflightRejectsDrifts(t *testing.T, drifts map[string]func(*portableExecutionPreflightFixture)) {
	t.Helper()
	for name, mutate := range drifts {
		t.Run(name, func(t *testing.T) {
			fixture := newPortableExecutionPreflightFixture(t)
			mutate(fixture)
			if _, err := BindPortableExecutionPreflight(context.Background(), fixture.request, fixture.dependencies()); err == nil {
				t.Fatal("BindPortableExecutionPreflight() accepted external identity drift")
			}
		})
	}
}

func testPortableExecutionPreflightDynamicBindingFieldGuard(t *testing.T) {
	producer := portableExecutionPreflightJSONFields(reflect.TypeFor[portableExecutionPreflightBindingMaterial]())
	consumer := portableExecutionPreflightBindingConsumers()
	if err := ValidatePortableExecutionPreflightBindingConsumerFields(consumer); err != nil {
		t.Fatalf("ValidatePortableExecutionPreflightBindingConsumerFields() error = %v", err)
	}
	if len(producer) != len(consumer) {
		t.Fatalf("binding producer/consumer fields = %v/%v", producer, consumer)
	}
	if err := ValidatePortableExecutionPreflightBindingConsumerFields(consumer[:len(consumer)-1]); err == nil {
		t.Fatal("binding field guard accepted missing consumer")
	}
	if err := ValidatePortableExecutionPreflightBindingConsumerFields(append(consumer, "stale")); err == nil {
		t.Fatal("binding field guard accepted stale consumer")
	}
}

func testPortableExecutionPreflightPreservesReadAndCloseErrors(t *testing.T) {
	readErr := errors.New("read failed")
	closeErr := errors.New("close failed")
	order := []string{}
	opener := &preflightBlobOpener{label: "config", data: []byte("x"), readErr: readErr, closeErr: closeErr, opensByReference: map[string]int{}, order: &order}
	if _, err := readPortableExecutionSnapshot(context.Background(), opener, "/config", 16); !errors.Is(err, readErr) || !errors.Is(err, closeErr) {
		t.Fatalf("read handle error = %v, want both causes", err)
	}
	fixture := newPortableExecutionPreflightFixture(t)
	fixture.ledger.acceptedErr = readErr
	fixture.ledger.closeErr = closeErr
	if _, err := bindPortableExecutionLedger(context.Background(), fixture.ledger, fixture.request.Authority); !errors.Is(err, readErr) || !errors.Is(err, closeErr) {
		t.Fatalf("ledger handle error = %v, want both causes", err)
	}
}

type portableExecutionPreflightFixture struct {
	request   PortableExecutionRequest
	receipt   RuntimeArtifactProducerReceipt
	order     []string
	config    *preflightBlobOpener
	ledger    *preflightLedgerOpener
	git       *preflightGitReader
	artifacts *preflightBlobOpener
}

func newPortableExecutionPreflightFixture(t *testing.T) *portableExecutionPreflightFixture {
	t.Helper()
	manifest := portableExecutionPreflightManifestFixture(t)
	manifestBytes, err := manifest.CanonicalEncode()
	if err != nil {
		t.Fatal(err)
	}
	configBytes := portableExecutionPreflightConfigBytes(t)
	accepted := portableExecutionPreflightAcceptedAuthority(t, 22)
	request := portableExecutionPreflightRequestFixture(t)
	request.Audit.Profile = gate.ProfileLocalFast
	request.Target.RepositoryID = manifest.RepositoryID
	request.Target.WorkloadManifest.RepositoryID = manifest.RepositoryID
	request.Target.WorkloadManifest.SHA256 = portableExecutionPreflightBytesDigest(manifestBytes)
	request.Authority.ConfigSHA256 = portableExecutionPreflightBytesDigest(configBytes)
	request.Authority.AcceptedGeneration = accepted.Generation
	request.Authority.AcceptedAuthoritySHA256 = accepted.StateSHA256

	artifactBytes := []byte("portable-runtime-artifact-bytes")
	receipt := portableExecutionPreflightReceipt(t, request, artifactBytes)
	receiptBytes, receiptSHA, err := remoteci.EncodeRuntimeArtifactProducerReceipt(receipt)
	if err != nil {
		t.Fatal(err)
	}
	request.Engine.Artifact = portableExecutionArtifactFromReceipt(receipt, request.Engine.Artifact.ReceiptKey, receiptSHA)
	request.Engine.Artifact.ReceiptKey = portableExecutionPreflightReceiptKey(t, receipt)
	request.Engine.Artifact.ReceiptSHA256 = receiptSHA
	if err := request.Validate(); err != nil {
		t.Fatalf("fixture request: %v", err)
	}

	fixture := &portableExecutionPreflightFixture{request: request, receipt: receipt}
	fixture.config = &preflightBlobOpener{label: "config", data: configBytes, opensByReference: map[string]int{}, order: &fixture.order}
	fixture.ledger = &preflightLedgerOpener{reference: request.Authority.LedgerPath, identity: request.Authority.LedgerIdentity, schema: cicontract.DurationLedgerSQLiteSchemaVersion, accepted: accepted, order: &fixture.order}
	fixture.git = &preflightGitReader{treeSHA: request.Target.TreeSHA, blob: GitTreeBlob{Mode: "100644", Data: manifestBytes}, order: &fixture.order}
	fixture.artifacts = &preflightBlobOpener{labelByReference: map[string]string{request.Engine.Artifact.ReceiptKey: "receipt", request.Engine.Artifact.ArtifactKey: "artifact"}, dataByReference: map[string][]byte{request.Engine.Artifact.ReceiptKey: receiptBytes, request.Engine.Artifact.ArtifactKey: artifactBytes}, opensByReference: map[string]int{}, order: &fixture.order}
	return fixture
}

func (fixture *portableExecutionPreflightFixture) dependencies() PortableExecutionPreflightDependencies {
	return PortableExecutionPreflightDependencies{ConfigFiles: fixture.config, Ledger: fixture.ledger, TargetGit: fixture.git, Artifacts: fixture.artifacts}
}

func portableExecutionPreflightReceipt(t *testing.T, request PortableExecutionRequest, artifactBytes []byte) RuntimeArtifactProducerReceipt {
	t.Helper()
	receipt := RuntimeArtifactProducerReceipt{
		SchemaVersion: RuntimeArtifactBuilderReceiptSchema, ContractID: PortableExecutionRuntimeArtifactContractID,
		ArtifactKind: PortableExecutionRuntimeArtifactKind, ExecutionProvider: cicontract.ExecutionProviderID,
		RepositoryID: request.Engine.RepositoryID, ObjectFormat: request.Engine.ObjectFormat,
		SourceCommitSHA: request.Engine.CommitSHA, SourceTreeSHA: request.Engine.TreeSHA,
		InputSHA256: portableExecutionDigest("7"), InputSizeBytes: 8192,
		ObservedBuilderDigest: request.Engine.Artifact.BuilderDigest, ObservedToolchainDigest: request.Engine.Artifact.ToolchainDigest,
		ProtocolABIDigest: request.Engine.Artifact.ProtocolABIDigest, WorkloadManifestABIDigest: request.Engine.Artifact.WorkloadManifestABIDigest,
		ArtifactSHA256: portableExecutionPreflightBytesDigest(artifactBytes), ArtifactSizeBytes: int64(len(artifactBytes)),
	}
	identity, err := remoteci.PortableExecutionRuntimeArtifactIdentityDigest(receipt.RepositoryID, receipt.ObjectFormat, receipt.SourceCommitSHA, receipt.SourceTreeSHA, portableExecutionArtifactFromReceipt(receipt, "", ""))
	if err != nil {
		t.Fatal(err)
	}
	receipt.ArtifactIdentityDigest = identity
	artifactKey, _, err := remoteci.CanonicalRuntimeArtifactBuilderKeys("portable-engine/", receipt.RepositoryID, receipt.ObjectFormat, receipt.SourceTreeSHA, identity)
	if err != nil {
		t.Fatal(err)
	}
	receipt.ArtifactKey = artifactKey
	return receipt
}

func portableExecutionPreflightReceiptKey(t *testing.T, receipt RuntimeArtifactProducerReceipt) string {
	t.Helper()
	_, receiptKey, err := remoteci.CanonicalRuntimeArtifactBuilderKeys("portable-engine/", receipt.RepositoryID, receipt.ObjectFormat, receipt.SourceTreeSHA, receipt.ArtifactIdentityDigest)
	if err != nil {
		t.Fatal(err)
	}
	return receiptKey
}

func portableExecutionPreflightConfigBytes(t *testing.T) []byte {
	t.Helper()
	config := Config{
		SchemaVersion: ConfigSchemaVersion, AliyunCLI: "aliyun", CredentialProfile: "profile",
		RegionID: "cn-shenzhen", VSwitches: []cicontract.ECIVSwitch{{ID: "vsw-a123", ZoneID: "cn-shenzhen-a"}, {ID: "vsw-b456", ZoneID: "cn-shenzhen-b"}},
		SecurityGroupID: "sg-test", WorkerRoleName: "role-test",
		OSS: OSSConfig{Bucket: "bucket", Endpoint: "https://oss.example", InternalEndpoint: "https://oss-internal.example", SourcePrefix: "source/"},
		Capacity: CapacityConfig{ResourcePolicy: shardresource.Policy{
			Classes:                   []shardresource.Class{{ID: "small", VCPU: 2, MemoryGiB: 4}, {ID: "medium", VCPU: 4, MemoryGiB: 8}, {ID: "maximum", VCPU: 8, MemoryGiB: 16}},
			Bootstrap:                 shardresource.BootstrapClasses{Guard: "small", NodeTest: "small", GoTest: "small"},
			CalibrationResource:       shardresource.Class{ID: "calibration", VCPU: 4, MemoryGiB: 8},
			FastWorkloadMaxDurationMS: 5000, MediumWorkloadMaxDurationMS: 70000, HeadroomPercent: 25, MinSamplesToDownsize: 5,
		}},
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func portableExecutionPreflightAcceptedAuthority(t *testing.T, generation uint64) PortableExecutionAcceptedAuthority {
	t.Helper()
	digest := func(value string) string { return "sha256:" + strings.Repeat(value, 64) }
	state := cicontract.AcceptedBaselineState{
		SchemaVersion: cicontract.BaselineStateSchemaVersion, Generation: generation,
		ExecutionProvider: cicontract.ExecutionProviderID, RegionID: "cn-shenzhen",
		MainCommit: strings.Repeat("a", 40), MainTree: strings.Repeat("b", 40), Platform: cicontract.TargetPlatform,
		PolicyDigest: digest("c"), ToolchainDigest: digest("d"), RuntimeImage: "registry.example/runner@" + digest("e"),
		ImageCacheID: "imc-test", ImageCacheSnapshotID: "snapshot-test", ImageCacheReady: true, ImageDigest: digest("e"),
		GateBinarySHA256: digest("f"), RuntimeSeedSHA256: digest("1"), BaselineManifestDigest: digest("2"),
		CreatedAt: time.Unix(1_700_000_000, 0).UTC(), AcceptedAt: time.Unix(1_700_000_001, 0).UTC(), RenewedAt: time.Unix(1_700_000_002, 0).UTC(),
		OCIProjectCache: &cicontract.AcceptedBaselineOCIProjectCache{Image: "registry.example/runner@" + digest("e"), ContentManifestSHA256: digest("0"), MainTree: strings.Repeat("b", 40), ToolchainDigest: digest("d"), Platform: cicontract.TargetPlatform, CachePath: cicontract.AcceptedBaselineOCIProjectCachePath},
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return PortableExecutionAcceptedAuthority{Generation: generation, StateJSON: data, StateSHA256: portableExecutionPreflightBytesDigest(data)}
}

func portableExecutionPreflightRequestFixture(t *testing.T) PortableExecutionRequest {
	t.Helper()
	protocolABI, err := remoteci.PortableExecutionProtocolABIDigest()
	if err != nil {
		t.Fatal(err)
	}
	workloadABI, err := remoteci.RepositoryWorkloadManifestABIDigest()
	if err != nil {
		t.Fatal(err)
	}
	artifact := PortableExecutionArtifact{
		ContractID: PortableExecutionRuntimeArtifactContractID, Kind: PortableExecutionRuntimeArtifactKind,
		ArtifactKey: "portable-engine/artifacts/engine.runtime", ArtifactSHA256: portableExecutionDigest("a"), ArtifactSizeBytes: 4096,
		ReceiptKey: "portable-engine/receipts/engine.json", ReceiptSHA256: portableExecutionDigest("f"),
		BuilderDigest: portableExecutionDigest("b"), ToolchainDigest: portableExecutionDigest("c"),
		ProtocolABIDigest: protocolABI, WorkloadManifestABIDigest: workloadABI,
	}
	identity, err := remoteci.PortableExecutionRuntimeArtifactIdentityDigest("remote-ci-engine", "sha1", strings.Repeat("d", 40), strings.Repeat("e", 40), artifact)
	if err != nil {
		t.Fatal(err)
	}
	artifact.IdentityDigest = identity
	return PortableExecutionRequest{
		SchemaVersion: PortableExecutionRequestSchema, ContractID: PortableExecutionContractID, ExecutionProvider: cicontract.ExecutionProviderID,
		Engine:    PortableExecutionEngine{RepositoryID: "remote-ci-engine", ObjectFormat: "sha1", CommitSHA: strings.Repeat("d", 40), TreeSHA: strings.Repeat("e", 40), Artifact: artifact},
		Target:    PortableExecutionTarget{RepositoryID: "target-repository", ObjectFormat: "sha1", CommitSHA: strings.Repeat("1", 40), TreeSHA: strings.Repeat("2", 40), MountPath: "targets/target-repository", WorkloadManifest: PortableWorkloadManifest{RepoRelativePath: ".super-dolphin/remote-ci-workloads.json", SHA256: portableExecutionDigest("3"), ABIDigest: workloadABI, SchemaVersion: PortableWorkloadManifestSchemaVersion, RepositoryID: "target-repository"}},
		Authority: PortableExecutionAuthority{ConfigPath: "/authority/remote-ci.json", ConfigSHA256: portableExecutionDigest("4"), LedgerPath: "/authority/duration-ledger.sqlite", LedgerIdentity: "ledger-file-identity-1", AcceptedGeneration: 22, AcceptedAuthoritySHA256: portableExecutionDigest("5")},
		Audit:     PortableExecutionAudit{AgentTokenDigest: portableExecutionDigest("6"), Profile: gate.ProfileRemoteRequired, Entrypoint: gate.CIEntrypointManualCLI},
	}
}

func portableExecutionPreflightManifestFixture(t *testing.T) RepositoryWorkloadManifest {
	t.Helper()
	manifest := RepositoryWorkloadManifest{
		SchemaVersion: PortableWorkloadManifestSchemaVersion, RepositoryID: "example.test/portable-target",
		Modules:               []RepositoryWorkloadModule{{Root: ".", ModulePath: "example.test/portable-target"}},
		ExcludedNestedModules: []string{},
		Profiles:              []RepositoryWorkloadProfile{{Profile: gate.ProfileLocalFast, RequiredWorkloadIDs: []string{"portable-test"}}},
		Workloads: []RepositoryWorkload{{
			ID: "portable-test", Kind: RepositoryWorkloadKindGoTest, ModuleRoot: ".", Package: "./internal/check", Name: "TestPortable",
			Platform: RepositoryWorkloadPlatformLinuxAMD64, Runtime: RepositoryWorkloadRuntimeUbuntuContainer,
			BootstrapEstimateMS: 1500, CPUMilli: 1250, MemoryBytes: 2 << 30, ExecutionPolicy: RepositoryWorkloadExecutionPolicyNormal,
		}},
	}
	encoded, err := manifest.CanonicalEncode()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := remoteci.DecodeRepositoryWorkloadManifest(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func portableExecutionArtifactFromReceipt(receipt RuntimeArtifactProducerReceipt, receiptKey, receiptSHA string) PortableExecutionArtifact {
	return PortableExecutionArtifact{
		ContractID: receipt.ContractID, Kind: receipt.ArtifactKind, ArtifactKey: receipt.ArtifactKey,
		ArtifactSHA256: receipt.ArtifactSHA256, ArtifactSizeBytes: receipt.ArtifactSizeBytes,
		ReceiptKey: receiptKey, ReceiptSHA256: receiptSHA, IdentityDigest: receipt.ArtifactIdentityDigest,
		BuilderDigest: receipt.ObservedBuilderDigest, ToolchainDigest: receipt.ObservedToolchainDigest,
		ProtocolABIDigest: receipt.ProtocolABIDigest, WorkloadManifestABIDigest: receipt.WorkloadManifestABIDigest,
	}
}

func portableExecutionPreflightJSONFields(value reflect.Type) []string {
	fields := make([]string, 0, value.NumField())
	for field := range value.Fields() {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		fields = append(fields, name)
	}
	slices.Sort(fields)
	return fields
}

func portableExecutionPreflightBytesDigest(data []byte) string {
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func portableExecutionDigest(hexDigit string) string { return "sha256:" + strings.Repeat(hexDigit, 64) }

func validPortableDigest(value string) bool {
	encoded, ok := strings.CutPrefix(value, "sha256:")
	return ok && len(encoded) == 64 && strings.Trim(encoded, "0123456789abcdef") == ""
}

func assertEveryInvalidPortableExecutionLeafHasNoPreflightIO(t *testing.T, base PortableExecutionRequest, value reflect.Value, path []string) {
	t.Helper()
	for index := 0; index < value.NumField(); index++ {
		field := value.Field(index)
		info := value.Type().Field(index)
		fieldPath := append(append([]string(nil), path...), info.Name)
		if field.Kind() == reflect.Struct {
			assertEveryInvalidPortableExecutionLeafHasNoPreflightIO(t, base, field, fieldPath)
			continue
		}
		if field.Kind() == reflect.Bool {
			continue
		}
		candidate := base
		leaf := reflect.ValueOf(&candidate).Elem()
		for _, name := range fieldPath {
			leaf = leaf.FieldByName(name)
		}
		leaf.SetZero()
		spy := &preflightNoIO{}
		_, err := BindPortableExecutionPreflight(context.Background(), candidate, PortableExecutionPreflightDependencies{ConfigFiles: spy, Ledger: spy, TargetGit: spy, Artifacts: spy})
		if err == nil {
			t.Fatalf("preflight accepted invalid request leaf %s", strings.Join(fieldPath, "."))
		}
		if spy.calls != 0 {
			t.Fatalf("invalid request leaf %s performed %d I/O calls", strings.Join(fieldPath, "."), spy.calls)
		}
	}
}

type preflightBlobOpener struct {
	label, dataLabel  string
	labelByReference  map[string]string
	data              []byte
	dataByReference   map[string][]byte
	readErr, closeErr error
	opens             int
	opensByReference  map[string]int
	order             *[]string
}

func (opener *preflightBlobOpener) OpenReadOnly(_ context.Context, reference string) (PortableExecutionReadHandle, error) {
	opener.opens++
	opener.opensByReference[reference]++
	label := opener.label
	if opener.labelByReference != nil {
		label = opener.labelByReference[reference]
	}
	data := opener.data
	if opener.dataByReference != nil {
		data = opener.dataByReference[reference]
	}
	*opener.order = append(*opener.order, label+".open")
	return &preflightReadHandle{reference: reference, label: label, data: append([]byte(nil), data...), readErr: opener.readErr, closeErr: opener.closeErr, order: opener.order}, nil
}

type preflightReadHandle struct {
	reference, label  string
	data              []byte
	readErr, closeErr error
	order             *[]string
}

func (handle *preflightReadHandle) Reference() string { return handle.reference }
func (handle *preflightReadHandle) ReadAll(context.Context, int64) ([]byte, error) {
	*handle.order = append(*handle.order, handle.label+".read")
	return append([]byte(nil), handle.data...), handle.readErr
}
func (handle *preflightReadHandle) Close() error {
	*handle.order = append(*handle.order, handle.label+".close")
	return handle.closeErr
}

type preflightLedgerOpener struct {
	reference, identity   string
	schema                uint32
	accepted              PortableExecutionAcceptedAuthority
	acceptedErr, closeErr error
	opens                 int
	order                 *[]string
}

func (opener *preflightLedgerOpener) OpenLedgerReadOnly(_ context.Context, reference string) (PortableExecutionLedgerHandle, error) {
	opener.opens++
	*opener.order = append(*opener.order, "ledger.open")
	return &preflightLedgerHandle{opener: opener, reference: reference}, nil
}

type preflightLedgerHandle struct {
	opener    *preflightLedgerOpener
	reference string
}

func (handle *preflightLedgerHandle) Reference() string { return handle.reference }
func (handle *preflightLedgerHandle) FileIdentity(context.Context) (string, error) {
	*handle.opener.order = append(*handle.opener.order, "ledger.identity")
	return handle.opener.identity, nil
}
func (handle *preflightLedgerHandle) PhysicalSchemaVersion(context.Context) (uint32, error) {
	*handle.opener.order = append(*handle.opener.order, "ledger.schema")
	return handle.opener.schema, nil
}
func (handle *preflightLedgerHandle) ReadAcceptedAuthority(context.Context) (PortableExecutionAcceptedAuthority, error) {
	*handle.opener.order = append(*handle.opener.order, "ledger.accepted")
	return handle.opener.accepted, handle.opener.acceptedErr
}
func (handle *preflightLedgerHandle) Close() error {
	*handle.opener.order = append(*handle.opener.order, "ledger.close")
	return handle.opener.closeErr
}

type preflightGitReader struct {
	treeSHA string
	blob    GitTreeBlob
	order   *[]string
}

func (reader *preflightGitReader) ResolveCommitTree(context.Context, string, string, string) (string, error) {
	*reader.order = append(*reader.order, "git.resolve")
	return reader.treeSHA, nil
}
func (reader *preflightGitReader) ReadTreeBlob(context.Context, string, string, string, string) (GitTreeBlob, error) {
	*reader.order = append(*reader.order, "git.blob")
	return GitTreeBlob{Mode: reader.blob.Mode, Data: append([]byte(nil), reader.blob.Data...)}, nil
}

type preflightNoIO struct{ calls int }

func (spy *preflightNoIO) OpenReadOnly(context.Context, string) (PortableExecutionReadHandle, error) {
	spy.calls++
	return nil, errors.New("unexpected I/O")
}
func (spy *preflightNoIO) OpenLedgerReadOnly(context.Context, string) (PortableExecutionLedgerHandle, error) {
	spy.calls++
	return nil, errors.New("unexpected I/O")
}
func (spy *preflightNoIO) ResolveCommitTree(context.Context, string, string, string) (string, error) {
	spy.calls++
	return "", errors.New("unexpected I/O")
}
func (spy *preflightNoIO) ReadTreeBlob(context.Context, string, string, string, string) (GitTreeBlob, error) {
	spy.calls++
	return GitTreeBlob{}, errors.New("unexpected I/O")
}

var _ PortableExecutionReadOpener = (*preflightNoIO)(nil)
var _ PortableExecutionLedgerOpener = (*preflightNoIO)(nil)
var _ PortableExecutionGitReader = (*preflightNoIO)(nil)
