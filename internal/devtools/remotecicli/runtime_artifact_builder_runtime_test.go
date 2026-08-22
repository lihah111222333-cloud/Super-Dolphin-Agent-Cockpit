package remotecicli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

func TestRuntimeArtifactBuilderRuntimeUsesSingleActorReadbackAndCleanup(t *testing.T) {
	input := newRuntimeArtifactRuntimeInput(t)
	artifact := fakeRuntimeExecutableBytes()
	artifactDigest := digestRuntimeTestBytes(artifact)
	binding := remoteci.PortableExecutionArtifact{
		ContractID: remoteci.PortableExecutionRuntimeArtifactContractID, Kind: remoteci.PortableExecutionRuntimeArtifactKind,
		ArtifactSHA256: artifactDigest, ArtifactSizeBytes: int64(len(artifact)),
		BuilderDigest: input.ExpectedBuilderDigest, ToolchainDigest: input.ExpectedToolchainDigest,
		ProtocolABIDigest: input.ProtocolABIDigest, WorkloadManifestABIDigest: input.WorkloadManifestABIDigest,
	}
	identity, err := remoteci.PortableExecutionRuntimeArtifactIdentityDigest(input.RepositoryID, input.ObjectFormat, input.CommitSHA, input.TreeSHA, binding)
	requireNoErrorRuntimeTest(t, "runtime identity", err)
	artifactKey, receiptKey, err := remoteci.CanonicalRuntimeArtifactBuilderKeys(input.ArtifactPrefix, input.RepositoryID, input.ObjectFormat, input.TreeSHA, identity)
	requireNoErrorRuntimeTest(t, "runtime keys", err)
	receipt := remoteci.RuntimeArtifactProducerReceipt{
		SchemaVersion: remoteci.RuntimeArtifactBuilderReceiptSchema, ContractID: binding.ContractID, ArtifactKind: binding.Kind,
		ExecutionProvider: remoteci.PortableExecutionProvider, RepositoryID: input.RepositoryID, ObjectFormat: input.ObjectFormat,
		SourceCommitSHA: input.CommitSHA, SourceTreeSHA: input.TreeSHA,
		InputSHA256: input.InputSHA256, InputSizeBytes: input.InputSizeBytes,
		ObservedBuilderDigest: binding.BuilderDigest, ObservedToolchainDigest: binding.ToolchainDigest,
		ProtocolABIDigest: binding.ProtocolABIDigest, WorkloadManifestABIDigest: binding.WorkloadManifestABIDigest,
		ArtifactKey: artifactKey, ArtifactSHA256: artifactDigest, ArtifactSizeBytes: int64(len(artifact)), ArtifactIdentityDigest: identity,
	}
	receiptBytes, receiptDigest, err := remoteci.EncodeRuntimeArtifactProducerReceipt(receipt)
	requireNoErrorRuntimeTest(t, "encode receipt", err)
	inputDigest, err := remoteci.RuntimeArtifactBuilderInputDigest(input)
	requireNoErrorRuntimeTest(t, "input digest", err)
	logs, err := boundedRuntimeArtifactBuildJSON(remoteci.RuntimeArtifactBuilderWorkerResult{
		ArtifactKey: artifactKey, ReceiptKey: receiptKey, ArtifactSHA256: artifactDigest,
		ArtifactSizeBytes: int64(len(artifact)), ReceiptSHA256: receiptDigest,
		ArtifactIdentityDigest: identity, InputDigest: inputDigest,
	})
	requireNoErrorRuntimeTest(t, "runtime worker output", err)
	request, err := runtimeArtifactRequestForTest(input)
	requireNoErrorRuntimeTest(t, "runtime request", err)
	pending := runtimeGroup(request, eci.ArtifactBuilderGroupPending, "", nil)
	running := runtimeGroup(request, eci.ArtifactBuilderGroupRunning, "Running", nil)
	exit := int64(0)
	succeeded := runtimeGroup(request, eci.ArtifactBuilderGroupSucceeded, "Terminated", &exit)
	succeeded.Container.StartTime = time.Unix(1, 0)
	succeeded.Container.FinishTime = time.Unix(2, 0)
	actor := &scriptedArtifactBuilderActor{groups: []eci.ArtifactBuilderGroup{pending, running, succeeded}, logs: string(logs)}
	store := &scriptedArtifactBuilderStore{receipt: receiptBytes, artifact: artifact, receiptKey: receiptKey, artifactKey: artifactKey}
	runtime, err := NewRuntimeArtifactBuilderRuntime(RuntimeArtifactBuilderRuntimeDependencies{
		Actor: actor, Store: store, RequestFactory: func(got remoteci.RuntimeArtifactBuildInput) (eci.ArtifactBuilderRequest, error) {
			if got != input {
				t.Fatalf("request input drifted: %#v", got)
			}
			return request, nil
		}, Clock: time.Now, PollInterval: time.Millisecond, PollTimeout: time.Second, CleanupTimeout: time.Second,
	})
	requireNoErrorRuntimeTest(t, "NewRuntimeArtifactBuilderRuntime", err)
	evidence, err := runtime.Build(context.Background(), input)
	requireNoErrorRuntimeTest(t, "Build", err)
	if evidence.Artifact.ArtifactKey != artifactKey || evidence.Artifact.ReceiptKey != receiptKey || evidence.Artifact.ReceiptSHA256 != receiptDigest {
		t.Fatalf("evidence artifact binding = %#v", evidence.Artifact)
	}
	if actor.createCalls != 1 || actor.deleteCalls != 1 || store.deleteCalls != 1 || actor.absentCalls == 0 || store.absentCalls == 0 {
		t.Fatalf("lifecycle calls create=%d delete=%d/%d absent=%d/%d", actor.createCalls, actor.deleteCalls, store.deleteCalls, actor.absentCalls, store.absentCalls)
	}
}

func newRuntimeArtifactRuntimeInput(t *testing.T) remoteci.RuntimeArtifactBuildInput {
	t.Helper()
	protocolABI, err := remoteci.PortableExecutionProtocolABIDigest()
	requireNoErrorRuntimeTest(t, "portable execution ABI", err)
	workloadABI, err := remoteci.RepositoryWorkloadManifestABIDigest()
	requireNoErrorRuntimeTest(t, "workload ABI", err)
	return remoteci.RuntimeArtifactBuildInput{
		SchemaVersion: remoteci.RuntimeArtifactBuilderInputSchema, ContractID: remoteci.PortableExecutionRuntimeArtifactContractID,
		ArtifactKind: remoteci.PortableExecutionRuntimeArtifactKind, ExecutionProvider: remoteci.PortableExecutionProvider,
		ArtifactPrefix: "runtime/", RepositoryID: "engine-1", ObjectFormat: "sha1",
		CommitSHA: strings.Repeat("a", 40), TreeSHA: strings.Repeat("b", 40), InputObjectKey: "runtime/inputs/engine.bundle",
		InputSHA256: "sha256:" + strings.Repeat("c", 64), InputSizeBytes: 123,
		ExpectedBuilderDigest: "sha256:" + strings.Repeat("d", 64), ExpectedToolchainDigest: "sha256:" + strings.Repeat("e", 64),
		ProtocolABIDigest: protocolABI, WorkloadManifestABIDigest: workloadABI,
	}
}

func runtimeArtifactRequestForTest(input remoteci.RuntimeArtifactBuildInput) (eci.ArtifactBuilderRequest, error) {
	return eci.NewRuntimeArtifactBuilderRequest(eci.ArtifactBuilderConfig{
		Provider: eci.ArtifactBuilderProvider, RegionID: "region-1", ContainerGroupName: "runtime-builder-1", ContainerName: "runtime-worker-1",
		CompilerPath: "/usr/local/go/bin/go", Image: "registry.example/runtime@sha256:" + strings.Repeat("a", 64), ImageSnapshotID: "snapshot-1",
		EphemeralStorageGiB: eci.ArtifactBuilderEphemeralStorageGiB, CPU: eci.ArtifactBuilderCPU, MemoryGiB: eci.ArtifactBuilderMemoryGiB,
		VSwitches: []cicontract.ECIVSwitch{{ID: "vsw-zone-a", ZoneID: "cn-test-a"}, {ID: "vsw-zone-b", ZoneID: "cn-test-b"}}, SecurityGroupID: "sg-1", RAMRoleName: "role-1", OSSBucket: "bucket-1", OSSInternalEndpoint: "https://oss-cn-test-internal.aliyuncs.com",
		Input: eci.ArtifactBuilderInput{ArtifactPrefix: input.ArtifactPrefix, Key: input.InputObjectKey, SHA256: input.InputSHA256, SizeBytes: input.InputSizeBytes,
			RepositoryID: input.RepositoryID, ObjectFormat: input.ObjectFormat, SourceCommitSHA: input.CommitSHA, SourceTreeSHA: input.TreeSHA,
			ExpectedBuilderDigest: input.ExpectedBuilderDigest, ExpectedToolchainDigest: input.ExpectedToolchainDigest,
			ProtocolABIDigest: input.ProtocolABIDigest, WorkloadManifestABIDigest: input.WorkloadManifestABIDigest},
	})
}

func fakeRuntimeExecutableBytes() []byte {
	data := make([]byte, 64)
	copy(data, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
	data[16], data[17], data[18], data[19] = 2, 0, 62, 0
	return data
}

func digestRuntimeTestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func requireNoErrorRuntimeTest(t *testing.T, scope string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", scope, err)
	}
}
