package remotecicli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/oss"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

// TestBuilderSimulateMissingRequestUsesStrictTerminalChannels 锁定公开 builder-simulate 的失败协议。
func TestBuilderSimulateMissingRequestUsesStrictTerminalChannels(t *testing.T) {
	t.Parallel()

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := Run([]string{"builder-simulate"}, stdout, stderr)
	if code != remoteci.BuilderSimulationExitCodeContractFailure {
		t.Fatalf("builder-simulate exit code = %d, want %d", code, remoteci.BuilderSimulationExitCodeContractFailure)
	}
	result, err := remoteci.DecodeBuilderSimulationResult(stdout.Bytes())
	if err != nil {
		t.Fatalf("builder-simulate stdout is not a strict result: %v; stdout=%q", err, stdout.String())
	}
	if result.ProcessExitCode != code || result.Error.Code != "request" {
		t.Fatalf("builder-simulate result = %+v, want request contract failure", result)
	}
	if strings.Contains(stdout.String(), "repository_root") || strings.Contains(stdout.String(), "credential_profile") {
		t.Fatalf("builder-simulate stdout leaked configuration: %q", stdout.String())
	}
	if _, err := remoteci.DecodeBuilderSimulationEventJSONL(stderr.Bytes()); err != nil {
		t.Fatalf("builder-simulate stderr is not strict JSONL: %v; stderr=%q", err, stderr.String())
	}
	if strings.Contains(stderr.String(), "builder-simulate:") || strings.Contains(stderr.String(), "super-dolphin-remote-ci:") {
		t.Fatalf("builder-simulate stderr leaked plain diagnostic: %q", stderr.String())
	}
}

// TestBuilderSimulationGroupNameRejectsInvalidDigest 锁定 group identity 不允许 fallback。
func TestBuilderSimulationGroupNameRejectsInvalidDigest(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"", "sha256:short", "not-a-digest"} {
		if _, err := builderSimulationGroupName(value); err == nil {
			t.Fatalf("builderSimulationGroupName(%q) accepted an invalid digest", value)
		}
	}
	name, err := builderSimulationGroupName("sha256:" + strings.Repeat("a", 64))
	if err != nil || name != "sd-builder-aaaaaaaaaaaaaaaa" {
		t.Fatalf("builderSimulationGroupName() = %q, %v", name, err)
	}
}

// TestBuilderSimulationInputPathUsesCanonicalOSTempRoot 锁定 macOS 临时目录 symlink 的 canonical 解析。
func TestBuilderSimulationInputPathUsesCanonicalOSTempRoot(t *testing.T) {
	t.Parallel()

	path, cleanup, err := createBuilderSimulationInputPath()
	if err != nil {
		t.Fatalf("createBuilderSimulationInputPath() error = %v", err)
	}
	t.Cleanup(func() {
		if err := cleanup(); err != nil {
			t.Errorf("cleanup input path: %v", err)
		}
	})
	wantRoot, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks(os.TempDir()) error = %v", err)
	}
	if gotRoot := filepath.Dir(filepath.Dir(path)); gotRoot != wantRoot {
		t.Fatalf("input path parent = %q, want canonical OS temp root %q", gotRoot, wantRoot)
	}
	if err := validateAbsolutePathComponents(filepath.Dir(path), true); err != nil {
		t.Fatalf("created input directory is not canonical: %v", err)
	}
}

// TestBuilderSimulationImageCacheDriftIsRejectedBeforeBuild 锁定 caller cache identity 交叉核验。
func TestBuilderSimulationImageCacheDriftIsRejectedBeforeBuild(t *testing.T) {
	t.Parallel()

	request := remoteci.BuilderSimulationRequest{ECI: remoteci.BuilderSimulationECI{
		ImageCacheID: "cache-1", RegionID: "region-1", ImageSnapshotID: "snapshot-1", Image: "registry.example/builder@sha256:" + strings.Repeat("a", 64),
	}}
	cache := eci.ImageCache{ID: "cache-1", RegionID: "region-1", Status: "Ready", SnapshotID: "snapshot-1", Images: []string{"registry.example/other@sha256:" + strings.Repeat("b", 64)}}
	if err := validateBuilderSimulationImageCache(cache, request); err == nil {
		t.Fatal("validateBuilderSimulationImageCache accepted an image identity drift")
	}
	cache.SnapshotID = "snapshot-2"
	if err := validateBuilderSimulationImageCache(cache, request); err == nil {
		t.Fatal("validateBuilderSimulationImageCache accepted a snapshot identity drift")
	}
}

// TestBuilderSimulationCleanupRecorderRequiresBothProofs 锁定 request、ID 与 provider error 的 absence 矩阵。
func TestBuilderSimulationCleanupRecorderRequiresBothProofs(t *testing.T) {
	t.Parallel()

	known := &builderSimulationRecorder{}
	known.captureGroup(eci.ArtifactBuilderGroup{ID: "group-1"})
	known.recordRequestReconcile(false)
	known.recordRequestReconcile(false)
	if known.groupAbsenceProof() {
		t.Fatal("known group was treated as absent without ID proof")
	}
	known.groupIDAbsent = true
	if !known.groupAbsenceProof() {
		t.Fatal("known group absence proof was not accepted after both proofs")
	}
	known.captureGroup(eci.ArtifactBuilderGroup{ID: "group-2"})
	if known.groupAbsenceProof() {
		t.Fatal("foreign later-visible group identity was accepted")
	}

	failed := &builderSimulationRecorder{}
	failed.createAttempted = true
	failed.recordRequestReconcileError()
	failed.recordRequestReconcileError()
	if failed.groupAbsenceProof() {
		t.Fatal("provider reconcile errors were treated as absence proof")
	}
}

// TestBuilderSimulationFailureCloudPreservesObservedTerminalEvidence 锁定 provider evidence 与 simulation outcome 分域。
func TestBuilderSimulationFailureCloudPreservesObservedTerminalEvidence(t *testing.T) {
	t.Parallel()

	exitCode := int64(7)
	start := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	finish := start.Add(time.Second)
	recorder := &builderSimulationRecorder{}
	recorder.captureGroup(eci.ArtifactBuilderGroup{ID: "group-1", Status: eci.ArtifactBuilderGroupSucceeded, Container: eci.ArtifactBuilderContainer{ExitCode: &exitCode, StartTime: start, FinishTime: finish}})
	cloud := builderSimulationFailureCloud(recorder)
	if cloud.Status != remoteci.BuilderSimulationCloudStatusFailed || cloud.Group != "group-1" || cloud.ContainerExitCode != 7 || cloud.StartedAt == "" || cloud.FinishedAt == "" {
		t.Fatalf("failure cloud projection = %+v", cloud)
	}
}

func TestBuilderSimulationFailureLogProjectsStageAndDigest(t *testing.T) {
	var stderr bytes.Buffer
	writer := newBuilderSimulationEventWriter(&stderr, "operation-1", builderSimulationResultDigest("1"))
	recorder := &builderSimulationRecorder{writer: writer}
	logs := "progress: artifact build failed stage=build_bundle\nsecret=/private/worktree\n"
	recorder.observe(ArtifactBuilderRuntimeEvent{Stage: "failure_log", GroupID: "group-1", Status: "build_bundle", Digest: digestText(logs)})
	events, err := remoteci.DecodeBuilderSimulationEventJSONL(stderr.Bytes())
	if err != nil {
		t.Fatalf("failure log event JSONL decode error = %v; stderr=%q", err, stderr.String())
	}
	if len(events) != 1 {
		t.Fatalf("failure log event count = %d, want 1", len(events))
	}
	event := events[0]
	if event.Stage != remoteci.BuilderSimulationEventStageECIGroupObserved || event.State != remoteci.BuilderSimulationEventStateFailed || event.ResourceDigest != digestText(logs) || event.ErrorCode != "build_bundle" {
		t.Fatalf("failure log projection = %#v", event)
	}
	if strings.Contains(stderr.String(), "private/worktree") {
		t.Fatalf("failure log projection leaked raw log: %q", stderr.String())
	}
}

// TestBuilderSimulationFailureStageReportsCreateWithoutGroupID 锁定 create 错误不会伪装成 OSS staging 失败。
func TestBuilderSimulationFailureStageReportsCreateWithoutGroupID(t *testing.T) {
	if got := builderSimulationFailureStage(&builderSimulationRecorder{createAttempted: true}); got != remoteci.BuilderSimulationEventStageECICreateRequested {
		t.Fatalf("failure stage = %q, want ECI create", got)
	}
}

// TestBuilderSimulationOutputChannelsAreStrict verifies canonical stdout and JSONL stderr.
func TestBuilderSimulationOutputChannelsAreStrict(t *testing.T) {
	t.Parallel()

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	writer := newBuilderSimulationEventWriter(stderr, "operation-1", builderSimulationResultDigest("1"))
	if err := writeBuilderSimulationResult(stdout, writer, builderSimulationTestResult()); err != nil {
		t.Fatalf("writeBuilderSimulationResult() error = %v", err)
	}
	if _, err := remoteci.DecodeBuilderSimulationResult(stdout.Bytes()); err != nil {
		t.Fatalf("stdout result decode error = %v", err)
	}
	if _, err := remoteci.DecodeBuilderSimulationEventJSONL(stderr.Bytes()); err != nil {
		t.Fatalf("stderr event decode error = %v", err)
	}
}

// TestBuilderSimulationLocalCleanupFailurePrecedesTerminal verifies host cleanup maps to exit 4.
func TestBuilderSimulationLocalCleanupFailurePrecedesTerminal(t *testing.T) {
	t.Parallel()

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	writer := newBuilderSimulationEventWriter(stderr, "operation-1", builderSimulationResultDigest("1"))
	writer.cleanupLocal = func() error { return errors.New("injected cleanup failure") }
	err := writeBuilderSimulationResult(stdout, writer, builderSimulationTestResult())
	var exitErr builderSimulationExitError
	if !errors.As(err, &exitErr) || exitErr.code != remoteci.BuilderSimulationExitCodeCleanupFailure {
		t.Fatalf("local cleanup error = %v, want exit 4", err)
	}
	result, decodeErr := remoteci.DecodeBuilderSimulationResult(stdout.Bytes())
	if decodeErr != nil || result.ProcessExitCode != remoteci.BuilderSimulationExitCodeCleanupFailure || result.Cleanup.HostInputAbsent {
		t.Fatalf("cleanup result = %+v, decode error = %v", result, decodeErr)
	}
}

// TestBuilderSimulationRuntimeStoreReadsNestedBuilderObjects 锁定 production adapter 接受唯一 builder nested artifact/receipt key。
func TestBuilderSimulationRuntimeStoreReadsNestedBuilderObjects(t *testing.T) {
	prefix := "baseline-artifacts/builder-simulation/test/"
	artifactKey, key, err := remoteci.CanonicalSourceBaselineBuilderKeys(prefix, "repository-1", "sha1", strings.Repeat("1", 40), builderSimulationResultDigest("2"))
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("receipt")
	store := &builderSimulationRuntimeStore{portable: &oss.PortableArtifactStore{}, prefix: prefix,
		objects: builderSimulationObjectReaderFunc(func(context.Context, string, int64) ([]byte, error) { return payload, nil })}
	got, metadata, err := store.ReadReceipt(context.Background(), key, 1<<20)
	if err != nil {
		t.Fatalf("ReadReceipt() error = %v", err)
	}
	if !bytes.Equal(got, payload) || metadata.SHA256 != runtimeDigestBytes(payload) || metadata.SizeBytes != int64(len(payload)) {
		t.Fatalf("ReadReceipt() = %q, %+v", got, metadata)
	}
	assertBuilderSimulationRuntimeArtifact(t, store, artifactKey, payload)
}

// assertBuilderSimulationRuntimeArtifact 校验 nested artifact 的实际字节、长度与摘要。
func assertBuilderSimulationRuntimeArtifact(t *testing.T, store *builderSimulationRuntimeStore, artifactKey string, payload []byte) {
	t.Helper()
	body, size, metadata, err := store.Resolve(context.Background(), remoteci.PortableSourceBaselineArtifactKind, artifactKey)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	artifactBytes, readErr := io.ReadAll(body)
	closeErr := body.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(artifactBytes, payload) || size != int64(len(payload)) || metadata.SHA256 != runtimeDigestBytes(payload) {
		t.Fatalf("Resolve() = %q, %d, %+v, read=%v close=%v", artifactBytes, size, metadata, readErr, closeErr)
	}
}

type builderSimulationObjectReaderFunc func(context.Context, string, int64) ([]byte, error)

// Read 将测试函数适配为 bounded object reader。
func (read builderSimulationObjectReaderFunc) Read(ctx context.Context, key string, maxBytes int64) ([]byte, error) {
	return read(ctx, key, maxBytes)
}

// builderSimulationTestResult creates a complete successful terminal fixture.
func builderSimulationTestResult() remoteci.BuilderSimulationResult {
	return remoteci.BuilderSimulationResult{
		SchemaVersion: remoteci.BuilderSimulationResultSchema, Status: remoteci.BuilderSimulationResultStatusNotVerified,
		Outcome: remoteci.BuilderSimulationOutcomeSucceeded, ProcessExitCode: remoteci.BuilderSimulationExitCodeSuccess,
		OperationID: "operation-1", RequestDigest: builderSimulationResultDigest("1"), EngineBindingDigest: builderSimulationResultDigest("2"), TargetBindingDigest: builderSimulationResultDigest("3"),
		Artifact:         remoteci.BuilderSimulationResultArtifact{Kind: remoteci.BuilderSimulationArtifactKindPortableRuntime, BytesSHA256: builderSimulationResultDigest("4"), IdentityDigest: builderSimulationResultDigest("5"), SizeBytes: 128, ReceiptDigest: builderSimulationResultDigest("6")},
		Cloud:            remoteci.BuilderSimulationResultCloud{Provider: "aliyun-eci/v1", Group: "group-1", Status: remoteci.BuilderSimulationCloudStatusSucceeded, ContainerExitCode: 0, StartedAt: "2026-08-20T00:00:00Z", FinishedAt: "2026-08-20T00:00:01Z"},
		Cleanup:          remoteci.BuilderSimulationResultCleanup{ContainerGroupAbsent: true, InputStagingAbsent: true, HostInputAbsent: true, PublicationState: remoteci.BuilderSimulationPublicationStateValidatedPair, ConfirmedAt: "2026-08-20T00:00:01Z"},
		AuthorityEffects: remoteci.BuilderSimulationAuthorityEffectsNone,
	}
}

// builderSimulationTestDigest returns a nonzero canonical digest for protocol fixtures.
func builderSimulationResultDigest(_ string) string { return "sha256:" + strings.Repeat("a", 64) }
