package main

import (
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

func TestNewRemoteOCIBaselineStateRejectsGenerationOverflow(t *testing.T) {
	accepted := remoteci.BaselineState{Generation: ^uint64(0)}
	if _, err := newRemoteOCIBaselineState(accepted, remoteBaselineRefreshInput{}, nil); err == nil || !strings.Contains(err.Error(), "generation is exhausted") {
		t.Fatalf("newRemoteOCIBaselineState() error = %v, want generation overflow", err)
	}
}

func TestNewRemoteOCIBaselineStateBindsOnlyOCIIdentity(t *testing.T) {
	digest := testRemoteBaselineDigest("OCI successor build")
	commit := strings.Repeat("b", 40)
	tree := strings.Repeat("c", 40)
	input := remoteBaselineRefreshInput{
		Identity:         remoteci.BaselineIdentity{MainCommit: commit, MainTree: tree, Platform: "linux/amd64", PolicyDigest: digest, ToolchainDigest: digest, RuntimeImage: "registry.example.com/super@" + digest},
		GateSourceDigest: digest, RuntimeDependencyDigest: digest,
	}
	accepted := remoteci.BaselineState{Generation: 1, ImageCacheSnapshotID: "snap-baseline-1", RuntimeImage: input.Identity.RuntimeImage}
	build := testRemoteOCIBaselineBuild(t, accepted, input, digest)
	state, err := newRemoteOCIBaselineState(accepted, input, build, eci.ImageCache{ID: "imc-baseline-2", SnapshotID: "snap-baseline-2", Status: "Ready"})
	if err != nil {
		t.Fatalf("newRemoteOCIBaselineState() error = %v", err)
	}
	if state.Generation != 2 || state.OCIProjectCache != build.Cache || state.CreatedAt.Location() != time.UTC || !state.AcceptedAt.Equal(state.CreatedAt) {
		t.Fatalf("OCI state binding = %#v", state)
	}
}

func TestNewRemoteOCIBaselineStateRejectsCacheDigestAsDeltaIdentity(t *testing.T) {
	digest := testRemoteBaselineDigest("OCI successor build")
	commit := strings.Repeat("b", 40)
	tree := strings.Repeat("c", 40)
	input := remoteBaselineRefreshInput{Identity: remoteci.BaselineIdentity{MainCommit: commit, MainTree: tree, Platform: "linux/amd64", PolicyDigest: digest, ToolchainDigest: digest, RuntimeImage: "registry.example.com/super@" + digest}, GateSourceDigest: digest, RuntimeDependencyDigest: digest}
	accepted := remoteci.BaselineState{Generation: 1, ImageCacheSnapshotID: "snap-baseline-1", RuntimeImage: input.Identity.RuntimeImage}
	build := testRemoteOCIBaselineBuild(t, accepted, input, digest)
	build.Cache.ContentManifestSHA256 = testRemoteBaselineDigest("different OCI cache content manifest")
	if _, err := newRemoteOCIBaselineState(accepted, input, build, eci.ImageCache{ID: "imc-baseline-2", SnapshotID: "snap-baseline-2", Status: "Ready"}); err == nil || !strings.Contains(err.Error(), "cache identity") {
		t.Fatalf("newRemoteOCIBaselineState() error = %v, want cache/receipt mismatch", err)
	}
}

func testRemoteOCIBaselineBuild(t *testing.T, accepted remoteci.BaselineState, input remoteBaselineRefreshInput, digest string) *remoteOCIBaselineBuild {
	t.Helper()
	jobID := "oci-" + strings.Repeat("d", 24)
	prefix := "remote-ci/oci-builds/" + jobID + "/"
	request := remoteci.OCIBaselineBuilderRequest{
		SchemaVersion: remoteci.OCIBaselineBuilderRequestSchemaVersion, JobID: jobID, TransferMode: cicontract.RefreshTransferAcceptedSnapshotDelta,
		ParentGeneration: accepted.Generation, ParentStateSHA256: digest, OutputRepository: "registry.example.com/super-dolphin/baseline", ParentImage: accepted.RuntimeImage,
		ParentImageCacheID: "imc-baseline-1", ParentImageSnapshotID: accepted.ImageCacheSnapshotID,
		ParentSourceManifest: digest, ParentSourceImagePath: cicontract.SourceSnapshotManifestPath, ParentSourceClosure: digest,
		TargetCommit: input.Identity.MainCommit, TargetTree: input.Identity.MainTree, TargetSourceManifest: digest, TargetSourceClosure: digest, ImageInputDigest: digest,
		PolicyDigest: input.Identity.PolicyDigest, ToolchainDigest: input.Identity.ToolchainDigest, Platform: input.Identity.Platform, RuntimeDependencyDigest: input.RuntimeDependencyDigest,
		DeltaArchiveKey: prefix + "source.snapshot.delta.tar", DeltaArchiveSHA256: digest, DeltaArchiveSize: 1, JobKey: prefix + "request.job.json",
	}
	receipts := make([]cicontract.RefreshCheckObservation, 0, len(cicontract.RefreshChecks()))
	for _, check := range cicontract.RefreshChecks() {
		receipt := cicontract.RefreshCheckObservation{Check: check, Executed: true, Passed: true, SourceTree: request.TargetTree, AcceptedSnapshotID: request.ParentImageSnapshotID, PlanDigest: request.ImageInputDigest, StartedAtUnixMS: 100, CompletedAtUnixMS: 101, DurationMS: 1, TestBodyNotApplicable: true}
		if check == cicontract.RefreshCheckDependency {
			receipt.CandidateCompileNotApplicable = true
		} else {
			receipt.CandidateCompileMS = 1
		}
		var err error
		receipt.ReceiptSHA256, err = cicontract.RefreshCheckObservationReceiptDigest(receipt)
		if err != nil {
			t.Fatalf("RefreshCheckObservationReceiptDigest() error = %v", err)
		}
		receipts = append(receipts, receipt)
	}
	result := remoteci.OCIBaselineBuilderResult{SchemaVersion: remoteci.OCIBaselineBuilderResultSchemaVersion, JobID: request.JobID, TransferMode: request.TransferMode, ParentGeneration: request.ParentGeneration, ParentStateSHA256: request.ParentStateSHA256, OutputRepository: request.OutputRepository, ParentImage: request.ParentImage, ParentImageCacheID: request.ParentImageCacheID, ParentImageSnapshotID: request.ParentImageSnapshotID, ParentSourceManifest: request.ParentSourceManifest, ParentSourceImagePath: request.ParentSourceImagePath, ParentSourceClosure: request.ParentSourceClosure, TargetCommit: request.TargetCommit, TargetTree: request.TargetTree, TargetSourceManifest: request.TargetSourceManifest, TargetSourceClosure: request.TargetSourceClosure, ImageInputDigest: request.ImageInputDigest, PolicyDigest: request.PolicyDigest, ToolchainDigest: request.ToolchainDigest, Platform: request.Platform, RuntimeDependencyDigest: request.RuntimeDependencyDigest, DeltaArchiveKey: request.DeltaArchiveKey, DeltaArchiveSHA256: request.DeltaArchiveSHA256, DeltaArchiveSize: request.DeltaArchiveSize, JobKey: request.JobKey, RefreshReceipts: receipts, Repository: request.OutputRepository, Image: request.OutputRepository + "@" + digest, ConfigDigest: digest}
	return &remoteOCIBaselineBuild{Request: request, Result: result, Cache: &remoteci.BaselineOCIProjectCache{Image: result.Image, ContentManifestSHA256: result.ImageInputDigest, MainTree: input.Identity.MainTree, ToolchainDigest: input.Identity.ToolchainDigest, Platform: input.Identity.Platform, CachePath: remoteci.OCIProjectGoBuildCachePath}}
}
