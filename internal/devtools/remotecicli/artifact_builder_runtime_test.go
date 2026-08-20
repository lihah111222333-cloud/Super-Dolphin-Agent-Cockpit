package remotecicli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/oss"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

func TestArtifactBuilderRuntimeScriptedSuccessCleansExactlyOnce(t *testing.T) {
	fixture := newRuntimeFixture(t)
	actor := &scriptedArtifactBuilderActor{groups: []eci.ArtifactBuilderGroup{fixture.pending, fixture.running, fixture.succeeded}, logs: fixture.output, absentPending: 1}
	store := &scriptedArtifactBuilderStore{receipt: fixture.receiptBytes, artifact: fixture.bundle, receiptKey: fixture.receiptKey, artifactKey: fixture.artifactKey, absentPending: 1}
	var events []ArtifactBuilderRuntimeEvent
	runtime, err := NewArtifactBuilderRuntime(ArtifactBuilderRuntimeDependencies{
		Actor: actor, Store: store, RequestFactory: func(remoteci.SourceBaselineBuildInput) (eci.ArtifactBuilderRequest, error) {
			return fixture.request, nil
		},
		Clock: time.Now, PollInterval: time.Millisecond, PollTimeout: time.Second, CleanupTimeout: time.Second,
		Observer: func(event ArtifactBuilderRuntimeEvent) { events = append(events, event) },
	})
	if err != nil {
		t.Fatalf("NewArtifactBuilderRuntime() error = %v", err)
	}
	evidence, err := runtime.Build(context.Background(), fixture.input)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if evidence.ArtifactKey != fixture.artifactKey || evidence.ReceiptKey != fixture.receiptKey {
		t.Fatalf("evidence keys = %q/%q", evidence.ArtifactKey, evidence.ReceiptKey)
	}
	assertRuntimeActorCalls(t, actor)
	assertRuntimeStoreCleanup(t, store, events)
	assertRuntimeEventsSafe(t, events)
}

func assertRuntimeActorCalls(t *testing.T, actor *scriptedArtifactBuilderActor) {
	t.Helper()
	if actor.createCalls != 1 || actor.describeCalls != 2 || actor.logsCalls != 1 || actor.deleteCalls != 1 || actor.absentCalls != 2 {
		t.Fatalf("actor calls = create:%d describe:%d logs:%d delete:%d absent:%d", actor.createCalls, actor.describeCalls, actor.logsCalls, actor.deleteCalls, actor.absentCalls)
	}
}

func assertRuntimeStoreCleanup(t *testing.T, store *scriptedArtifactBuilderStore, events []ArtifactBuilderRuntimeEvent) {
	t.Helper()
	if store.deleteCalls != 1 || store.absentCalls != 2 || len(events) == 0 || !events[len(events)-1].Cleanup {
		t.Fatalf("store/events cleanup evidence = delete:%d absent:%d events:%#v", store.deleteCalls, store.absentCalls, events)
	}
}

func assertRuntimeEventsSafe(t *testing.T, events []ArtifactBuilderRuntimeEvent) {
	t.Helper()
	for _, event := range events {
		if strings.Contains(event.Key, "secret") || strings.Contains(event.Digest, "input-1") {
			t.Fatalf("runtime event leaked path/secret material: %#v", event)
		}
	}
}

func TestArtifactBuilderRuntimeFailureDoesNotRecreateAndStillCleans(t *testing.T) {
	fixture := newRuntimeFixture(t)
	failed := fixture.succeeded
	failed.Status = eci.ArtifactBuilderGroupFailed
	failed.Container.ExitCode = new(int64(7))
	actor := &scriptedArtifactBuilderActor{groups: []eci.ArtifactBuilderGroup{fixture.pending, failed}, logs: ""}
	store := &scriptedArtifactBuilderStore{receipt: fixture.receiptBytes, artifact: fixture.bundle, receiptKey: fixture.receiptKey, artifactKey: fixture.artifactKey}
	runtime := mustRuntime(t, fixture, actor, store)
	if _, err := runtime.Build(context.Background(), fixture.input); err == nil {
		t.Fatal("Build() accepted failed builder group")
	}
	if actor.createCalls != 1 || actor.deleteCalls != 1 || store.deleteCalls != 1 || actor.absentCalls != 1 || store.absentCalls != 1 {
		t.Fatalf("failure cleanup calls = actor create:%d delete:%d absent:%d store delete:%d absent:%d", actor.createCalls, actor.deleteCalls, actor.absentCalls, store.deleteCalls, store.absentCalls)
	}
}

func TestArtifactBuilderRuntimeCancellationUsesIndependentCleanupContext(t *testing.T) {
	fixture := newRuntimeFixture(t)
	actor := &scriptedArtifactBuilderActor{groups: []eci.ArtifactBuilderGroup{fixture.pending}, logs: fixture.output}
	store := &scriptedArtifactBuilderStore{receipt: fixture.receiptBytes, artifact: fixture.bundle, receiptKey: fixture.receiptKey, artifactKey: fixture.artifactKey}
	runtime := mustRuntime(t, fixture, actor, store)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runtime.Build(ctx, fixture.input); err == nil {
		t.Fatal("Build() accepted cancelled context")
	}
	if actor.deleteCalls != 0 || actor.absentCalls != 0 || store.deleteCalls != 1 || store.absentCalls != 1 {
		t.Fatalf("pre-create cancellation cleanup = actor delete:%d absent:%d store delete:%d absent:%d", actor.deleteCalls, actor.absentCalls, store.deleteCalls, store.absentCalls)
	}
}

func TestArtifactBuilderRuntimeCreateErrorCleansLaterVisibleRequestIdentity(t *testing.T) {
	fixture := newRuntimeFixture(t)
	actor := &scriptedArtifactBuilderActor{
		groups:    []eci.ArtifactBuilderGroup{fixture.pending},
		createErr: errors.New("CreateContainerGroup response decode failed"),
		requestMatches: []eci.ArtifactBuilderRequestMatch{
			{},
			{Present: true, GroupID: fixture.succeeded.ID, Group: fixture.succeeded},
			{},
			{},
		},
	}
	store := &scriptedArtifactBuilderStore{receipt: fixture.receiptBytes, artifact: fixture.bundle, receiptKey: fixture.receiptKey, artifactKey: fixture.artifactKey}
	runtime := mustRuntime(t, fixture, actor, store)
	if _, err := runtime.Build(context.Background(), fixture.input); err == nil {
		t.Fatal("Build() accepted uncertain Create failure")
	}
	if actor.requestReconcileCalls < 3 || actor.deleteCalls != 1 || actor.absentCalls != 1 {
		t.Fatalf("request-identity cleanup = reconcile:%d delete:%d absent:%d", actor.requestReconcileCalls, actor.deleteCalls, actor.absentCalls)
	}
}

func TestArtifactBuilderRuntimeRequestDriftStillDeletesExactMatchedGroup(t *testing.T) {
	fixture := newRuntimeFixture(t)
	actor := &scriptedArtifactBuilderActor{
		createErr: errors.New("CreateContainerGroup response decode failed"),
		requestMatches: []eci.ArtifactBuilderRequestMatch{
			{Present: true, GroupID: fixture.succeeded.ID}, {}, {},
		},
		requestErrors: []error{eci.ErrArtifactBuilderIdentityDrift, nil, nil},
	}
	store := &scriptedArtifactBuilderStore{receipt: fixture.receiptBytes, artifact: fixture.bundle, receiptKey: fixture.receiptKey, artifactKey: fixture.artifactKey}
	runtime := mustRuntime(t, fixture, actor, store)
	if _, err := runtime.Build(context.Background(), fixture.input); err == nil {
		t.Fatal("Build() accepted request identity drift")
	}
	if actor.deleteCalls != 1 || actor.absentCalls != 1 || actor.requestReconcileCalls < 3 {
		t.Fatalf("drift cleanup = reconcile:%d delete:%d absent:%d", actor.requestReconcileCalls, actor.deleteCalls, actor.absentCalls)
	}
}

func mustRuntime(t *testing.T, fixture runtimeFixture, actor *scriptedArtifactBuilderActor, store *scriptedArtifactBuilderStore) *ArtifactBuilderRuntime {
	t.Helper()
	runtime, err := NewArtifactBuilderRuntime(ArtifactBuilderRuntimeDependencies{
		Actor: actor, Store: store, RequestFactory: func(remoteci.SourceBaselineBuildInput) (eci.ArtifactBuilderRequest, error) {
			return fixture.request, nil
		},
		Clock: time.Now, PollInterval: time.Millisecond, PollTimeout: time.Second, CleanupTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("NewArtifactBuilderRuntime() error = %v", err)
	}
	return runtime
}

type runtimeFixture struct {
	input, requestInput             remoteci.SourceBaselineBuildInput
	request                         eci.ArtifactBuilderRequest
	pending, running, succeeded     eci.ArtifactBuilderGroup
	bundle, receiptBytes            []byte
	artifactKey, receiptKey, output string
}

func newRuntimeFixture(t *testing.T) runtimeFixture {
	t.Helper()
	input, tree, commit := newRuntimeInput(t)
	artifact := newRuntimeArtifact(t, input, tree, commit)
	request := newRuntimeRequest(t, input)
	pending := runtimeGroup(request, eci.ArtifactBuilderGroupPending, "", nil)
	running := runtimeGroup(request, eci.ArtifactBuilderGroupRunning, eci.ArtifactBuilderContainerRunning, nil)
	exit := int64(0)
	succeeded := runtimeGroup(request, eci.ArtifactBuilderGroupSucceeded, eci.ArtifactBuilderContainerTerminated, &exit)
	succeeded.Container.StartTime = time.Unix(1, 0)
	succeeded.Container.FinishTime = time.Unix(2, 0)
	outputData, err := json.Marshal(artifactBuildRuntimeOutput{Schema: artifactBuildRuntimeSchema, Status: "NOT_VERIFIED", ExecutionProvider: remoteci.PortableExecutionProvider,
		InputDigest: artifact.inputDigest, ArtifactKey: artifact.artifactKey, ReceiptKey: artifact.receiptKey, ArtifactSHA256: artifact.artifactDigest, ArtifactSizeBytes: int64(len(artifact.bundle)), ArtifactIdentityDigest: artifact.receipt.ArtifactIdentityDigest,
		ReceiptSHA256: runtimeDigestBytes(artifact.receiptBytes), ReceiptDigest: artifact.receiptDigest})
	if err != nil {
		t.Fatal(err)
	}
	return runtimeFixture{input: input, request: request, requestInput: input, pending: pending, running: running, succeeded: succeeded, bundle: artifact.bundle, receiptBytes: artifact.receiptBytes, artifactKey: artifact.artifactKey, receiptKey: artifact.receiptKey, output: "progress: artifact build started\n" + artifactBuildRuntimePrefix + string(outputData) + "\nprogress: artifact build completed"}
}

type runtimeArtifactFixture struct {
	bundle, receiptBytes                                                []byte
	receipt                                                             remoteci.SourceBaselineProducerReceipt
	artifactKey, receiptKey, artifactDigest, inputDigest, receiptDigest string
}

func newRuntimeInput(t *testing.T) (remoteci.SourceBaselineBuildInput, string, string) {
	t.Helper()
	tree := "4b825dc642cb6eb9a060e54bf8d69288fbee4904"
	commit, err := remoteci.DeterministicSourceBaselineCommitSHA(tree, gate.GitObjectFormatSHA1)
	if err != nil {
		t.Fatal(err)
	}
	protocolABI, err := remoteci.BuilderSimulationProtocolABIDigest()
	if err != nil {
		t.Fatal(err)
	}
	workloadABI, err := remoteci.RepositoryWorkloadManifestABIDigest()
	if err != nil {
		t.Fatal(err)
	}
	return remoteci.SourceBaselineBuildInput{SchemaVersion: remoteci.SourceBaselineBuilderInputSchema, ArtifactPrefix: "runtime/", RepositoryID: "repository-1", ObjectFormat: "sha1", CommitSHA: strings.Repeat("2", 40), TreeSHA: tree, InputObjectKey: "runtime/inputs/input-1", InputSHA256: "sha256:" + strings.Repeat("a", 64), InputSizeBytes: 123, ExpectedBuilderDigest: "sha256:" + strings.Repeat("b", 64), ExpectedToolchainDigest: "sha256:" + strings.Repeat("c", 64), ProtocolABIDigest: protocolABI, WorkloadManifestABIDigest: workloadABI}, tree, commit
}

func newRuntimeArtifact(t *testing.T, input remoteci.SourceBaselineBuildInput, tree, commit string) runtimeArtifactFixture {
	t.Helper()
	bundle := makeBaselineBundle(t, tree, commit)
	bundleSum := sha256.Sum256(bundle)
	artifactDigest := "sha256:" + hex.EncodeToString(bundleSum[:])
	receipt := remoteci.SourceBaselineProducerReceipt{SchemaVersion: remoteci.SourceBaselineProducerReceiptSchema, ExecutionProvider: remoteci.PortableExecutionProvider,
		ExpectedBuilderDigest: input.ExpectedBuilderDigest, ExpectedToolchainDigest: input.ExpectedToolchainDigest, ObservedBuilderDigest: input.ExpectedBuilderDigest, ObservedToolchainDigest: input.ExpectedToolchainDigest,
		ProtocolABIDigest: input.ProtocolABIDigest, WorkloadManifestABIDigest: input.WorkloadManifestABIDigest, RepositoryID: input.RepositoryID, ObjectFormat: input.ObjectFormat,
		SourceCommitSHA: input.CommitSHA, SourceTreeSHA: tree, BaselineCommitSHA: commit, BaselineTreeSHA: tree, ArtifactSHA256: artifactDigest, ArtifactSizeBytes: int64(len(bundle))}
	var err error
	receipt.ArtifactIdentityDigest, err = remoteci.ArtifactIdentityDigest(receipt.RepositoryID, receipt.ObjectFormat, receipt.SourceCommitSHA, receipt.SourceTreeSHA, receipt.ArtifactSHA256, receipt.ArtifactSizeBytes, remoteci.ObservedArtifactBuilderIdentity{BuilderDigest: receipt.ObservedBuilderDigest, ToolchainDigest: receipt.ObservedToolchainDigest}, receipt.ProtocolABIDigest, receipt.WorkloadManifestABIDigest)
	if err != nil {
		t.Fatal(err)
	}
	artifactKey, receiptKey, err := remoteci.CanonicalSourceBaselineBuilderKeys(input.ArtifactPrefix, input.RepositoryID, input.ObjectFormat, tree, receipt.ArtifactIdentityDigest)
	if err != nil {
		t.Fatal(err)
	}
	receiptBytes, err := receipt.CanonicalEncode()
	if err != nil {
		t.Fatal(err)
	}
	inputDigest, err := remoteci.SourceBaselineBuilderInputDigest(input)
	if err != nil {
		t.Fatal(err)
	}
	receiptDigest, err := remoteci.SourceBaselineProducerReceiptDigest(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return runtimeArtifactFixture{bundle: bundle, receipt: receipt, receiptBytes: receiptBytes, artifactKey: artifactKey, receiptKey: receiptKey, artifactDigest: artifactDigest, inputDigest: inputDigest, receiptDigest: receiptDigest}
}

func newRuntimeRequest(t *testing.T, input remoteci.SourceBaselineBuildInput) eci.ArtifactBuilderRequest {
	t.Helper()
	request, err := eci.NewArtifactBuilderRequest(eci.ArtifactBuilderConfig{Provider: eci.ArtifactBuilderProvider, RegionID: "cn-hz", ContainerGroupName: "group-1", ContainerName: "worker-1",
		Image: "registry.example/remote@sha256:" + strings.Repeat("d", 64), ImageSnapshotID: "snapshot-1", EphemeralStorageGiB: eci.ArtifactBuilderEphemeralStorageGiB, CPU: eci.ArtifactBuilderCPU, MemoryGiB: eci.ArtifactBuilderMemoryGiB,
		VSwitches: []cicontract.ECIVSwitch{{ID: "vsw-zone-a", ZoneID: "cn-test-a"}, {ID: "vsw-zone-b", ZoneID: "cn-test-b"}}, SecurityGroupID: "sg-1", RAMRoleName: "role-1", OSSBucket: "bucket-name", OSSInternalEndpoint: "https://oss.example",
		Input: eci.ArtifactBuilderInput{ArtifactPrefix: input.ArtifactPrefix, Key: input.InputObjectKey, SHA256: input.InputSHA256, SizeBytes: input.InputSizeBytes, RepositoryID: input.RepositoryID, ObjectFormat: input.ObjectFormat, SourceCommitSHA: input.CommitSHA, SourceTreeSHA: input.TreeSHA, ExpectedBuilderDigest: input.ExpectedBuilderDigest, ExpectedToolchainDigest: input.ExpectedToolchainDigest, ProtocolABIDigest: input.ProtocolABIDigest, WorkloadManifestABIDigest: input.WorkloadManifestABIDigest}})
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func runtimeGroup(request eci.ArtifactBuilderRequest, status, containerState string, exitCode *int64) eci.ArtifactBuilderGroup {
	return eci.ArtifactBuilderGroup{ID: "group-1", Name: request.ContainerGroupName, Provider: request.Provider, RegionID: request.RegionID, Image: request.Image, ImageSnapshotID: request.ImageSnapshotID,
		Command: append([]string(nil), request.Command...), Input: request.Input, EphemeralStorageGiB: request.EphemeralStorageGiB, CPU: request.CPU, MemoryGiB: request.MemoryGiB,
		VSwitches: request.VSwitches, ScheduleStrategy: request.ScheduleStrategy, SecurityGroupID: request.SecurityGroupID, RAMRoleName: request.RAMRoleName, OSSBucket: request.OSSBucket,
		OSSInternalEndpoint: request.OSSInternalEndpoint, Tags: runtimeExpectedTags(request), Status: status, Container: eci.ArtifactBuilderContainer{Name: request.ContainerName, Image: request.Image, Command: []string{eci.ArtifactBuilderCommand}, Args: request.Command[1:], State: containerState, ExitCode: exitCode}}
}

func runtimeExpectedTags(request eci.ArtifactBuilderRequest) map[string]string {
	inputDigest := runtimeECIDigest("artifact-builder-input/v1", struct {
		ArtifactPrefix            string `json:"artifact_prefix"`
		Key                       string `json:"input_key"`
		SHA256                    string `json:"input_sha256"`
		SizeBytes                 int64  `json:"input_size_bytes"`
		RepositoryID              string `json:"repository_id"`
		ObjectFormat              string `json:"object_format"`
		SourceCommitSHA           string `json:"source_commit_sha"`
		SourceTreeSHA             string `json:"source_tree_sha"`
		ExpectedBuilderDigest     string `json:"expected_builder_digest"`
		ExpectedToolchainDigest   string `json:"expected_toolchain_digest"`
		ProtocolABIDigest         string `json:"protocol_abi_digest"`
		WorkloadManifestABIDigest string `json:"workload_manifest_abi_digest"`
	}{request.Input.ArtifactPrefix, request.Input.Key, request.Input.SHA256, request.Input.SizeBytes, request.Input.RepositoryID, request.Input.ObjectFormat, request.Input.SourceCommitSHA, request.Input.SourceTreeSHA, request.Input.ExpectedBuilderDigest, request.Input.ExpectedToolchainDigest, request.Input.ProtocolABIDigest, request.Input.WorkloadManifestABIDigest})
	requestDigest := runtimeECIDigest("artifact-builder-request/v1", struct {
		Provider            string
		RegionID            string
		ContainerGroupName  string
		ContainerName       string
		Image               string
		ImageSnapshotID     string
		EphemeralStorageGiB int64
		CPU                 float64
		MemoryGiB           float64
		VSwitches           []cicontract.ECIVSwitch
		ScheduleStrategy    string
		SecurityGroupID     string
		RAMRoleName         string
		OSSBucket           string
		OSSInternalEndpoint string
		ArtifactKind        string
		ArtifactRef         string
		Command             []string
		InputDigest         string
	}{request.Provider, request.RegionID, request.ContainerGroupName, request.ContainerName, request.Image, request.ImageSnapshotID, request.EphemeralStorageGiB, request.CPU, request.MemoryGiB, request.VSwitches, request.ScheduleStrategy, request.SecurityGroupID, request.RAMRoleName, request.OSSBucket, request.OSSInternalEndpoint, request.ArtifactKind, request.ArtifactRef, request.Command, inputDigest})
	return map[string]string{eci.ArtifactBuilderInputDigestTag: inputDigest, eci.ArtifactBuilderRequestDigestTag: requestDigest}
}

func runtimeECIDigest(domain string, value any) string {
	data, _ := json.Marshal(value)
	sum := sha256.Sum256(append([]byte(domain+"\x00"), data...))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func makeBaselineBundle(t *testing.T, tree, commit string) []byte {
	t.Helper()
	root := t.TempDir()
	runGitRuntime(t, root, "init", "--bare", "source.git")
	repo := root + "/source.git"
	if got := strings.TrimSpace(runGitRuntimeOutput(t, repo, "hash-object", "-t", "tree", "-w", "--stdin")); got != tree {
		t.Fatalf("empty tree = %q, want %q", got, tree)
	}
	payload := []byte("tree " + tree + "\nauthor Super Dolphin Source Baseline <source-baseline.invalid> 946684800 +0000\ncommitter Super Dolphin Source Baseline <source-baseline.invalid> 946684800 +0000\n\nsuper-dolphin accepted source baseline\n")
	if got := strings.TrimSpace(runGitRuntimeInput(t, repo, payload, "hash-object", "-t", "commit", "-w", "--stdin")); got != commit {
		t.Fatalf("baseline commit = %q, want %q", got, commit)
	}
	runGitRuntimeInput(t, repo, []byte("create refs/source/baseline "+commit+"\n"), "update-ref", "--stdin")
	bundlePath := root + "/source.bundle"
	runGitRuntime(t, repo, "bundle", "create", bundlePath, "--end-of-options", "refs/source/baseline")
	bundle, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}

func runGitRuntime(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, output)
	}
}

func runGitRuntimeOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return string(runGitRuntimeBytes(t, dir, nil, args...))
}

func runGitRuntimeInput(t *testing.T, dir string, data []byte, args ...string) string {
	t.Helper()
	return string(runGitRuntimeBytes(t, dir, data, args...))
}

func runGitRuntimeBytes(t *testing.T, dir string, data []byte, args ...string) []byte {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	command.Stdin = bytes.NewReader(data)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return output
}

type scriptedArtifactBuilderActor struct {
	groups                                                          []eci.ArtifactBuilderGroup
	logs                                                            string
	createErr                                                       error
	requestMatches                                                  []eci.ArtifactBuilderRequestMatch
	requestErrors                                                   []error
	absentPending                                                   int
	createCalls, describeCalls, logsCalls, deleteCalls, absentCalls int
	requestReconcileCalls                                           int
}

func (actor *scriptedArtifactBuilderActor) Create(context.Context, eci.ArtifactBuilderRequest) (eci.ArtifactBuilderGroup, error) {
	actor.createCalls++
	if actor.createErr != nil {
		return eci.ArtifactBuilderGroup{}, actor.createErr
	}
	return actor.groups[0], nil
}

func (actor *scriptedArtifactBuilderActor) ReconcileRequest(context.Context, eci.ArtifactBuilderRequest) (eci.ArtifactBuilderRequestMatch, error) {
	actor.requestReconcileCalls++
	if len(actor.requestMatches) == 0 {
		return eci.ArtifactBuilderRequestMatch{}, nil
	}
	match := actor.requestMatches[0]
	actor.requestMatches = actor.requestMatches[1:]
	var err error
	if len(actor.requestErrors) != 0 {
		err = actor.requestErrors[0]
		actor.requestErrors = actor.requestErrors[1:]
	}
	return match, err
}

func (actor *scriptedArtifactBuilderActor) Describe(context.Context, string) (eci.ArtifactBuilderGroup, error) {
	actor.describeCalls++
	index := actor.describeCalls
	if index >= len(actor.groups) {
		index = len(actor.groups) - 1
	}
	return actor.groups[index], nil
}

func (actor *scriptedArtifactBuilderActor) Logs(context.Context, string, string) (string, error) {
	actor.logsCalls++
	return actor.logs, nil
}

func (actor *scriptedArtifactBuilderActor) Delete(context.Context, string) error {
	actor.deleteCalls++
	return nil
}
func (actor *scriptedArtifactBuilderActor) ConfirmAbsent(context.Context, string) (bool, error) {
	actor.absentCalls++
	if actor.absentPending > 0 {
		actor.absentPending--
		return false, nil
	}
	return true, nil
}

type scriptedArtifactBuilderStore struct {
	receipt, artifact        []byte
	receiptKey, artifactKey  string
	absentPending            int
	deleteCalls, absentCalls int
}

func (store *scriptedArtifactBuilderStore) ReadReceipt(context.Context, string, int64) ([]byte, oss.PortableArtifactMetadata, error) {
	if store.receipt == nil {
		return nil, oss.PortableArtifactMetadata{}, errors.New("missing receipt")
	}
	return append([]byte(nil), store.receipt...), portableMetadataForRuntime(store.receipt), nil
}

func (store *scriptedArtifactBuilderStore) Resolve(context.Context, string, string) (io.ReadCloser, int64, oss.PortableArtifactMetadata, error) {
	return io.NopCloser(bytes.NewReader(store.artifact)), int64(len(store.artifact)), portableMetadataForRuntime(store.artifact), nil
}

func (store *scriptedArtifactBuilderStore) Delete(context.Context, string) error {
	store.deleteCalls++
	return nil
}
func (store *scriptedArtifactBuilderStore) ConfirmAbsent(context.Context, string) (bool, error) {
	store.absentCalls++
	if store.absentPending > 0 {
		store.absentPending--
		return false, nil
	}
	return true, nil
}

func portableMetadataForRuntime(data []byte) oss.PortableArtifactMetadata {
	sum := sha256.Sum256(data)
	return oss.PortableArtifactMetadata{SHA256: "sha256:" + hex.EncodeToString(sum[:]), SizeBytes: int64(len(data))}
}
