package main

import (
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

func TestApplyRemoteImageCacheRuntimePreservesCorrectnessIdentity(t *testing.T) {
	input := remoteci.RunInput{AcceptedGeneration: 1, RunnerIdentityDigest: "sha256:" + strings.Repeat("a", 64), RunnerImage: "accepted", ImageCacheSnapshotID: "accepted-snapshot"}
	runtime := remoteImageCacheRuntime{Image: "172.16.26.240:5000/sdci/successor@sha256:" + strings.Repeat("b", 64), SnapshotID: "s-refresh", CacheOnly: true}
	if err := applyRemoteImageCacheRuntime(&input, runtime); err != nil {
		t.Fatal(err)
	}
	if input.ExecutionRunnerImage != runtime.Image || input.ExecutionImageCacheSnapshotID != runtime.SnapshotID || !input.ImageCacheOnly || input.AcceptedGeneration != 1 || input.RunnerImage != "accepted" || input.ImageCacheSnapshotID != "accepted-snapshot" {
		t.Fatalf("applied runtime = %#v", input)
	}
}

func TestValidateRemoteImageCacheRefreshBindingRequiresAcceptedOCIBase(t *testing.T) {
	state := remoteRunRunnerIdentityState()
	state.ImageCacheSnapshotID = "s-accepted"
	config := remoteRunConfig{RegionID: state.RegionID}
	receipt := cicontract.ImageCacheRefreshReceipt{
		RegionID: state.RegionID, OCIBaseImage: state.RuntimeImage,
		BaseImage: state.RuntimeImage, BaseSnapshotID: state.ImageCacheSnapshotID,
		Image: state.RuntimeImage + "-successor", ImageCacheSnapshotID: state.ImageCacheSnapshotID + "-successor",
	}
	if err := validateRemoteImageCacheRefreshBinding(config, state, receipt); err != nil {
		t.Fatalf("valid binding error = %v", err)
	}
	receipt.OCIBaseImage = "ghcr.io/other/runtime@sha256:" + strings.Repeat("c", 64)
	if err := validateRemoteImageCacheRefreshBinding(config, state, receipt); err == nil {
		t.Fatal("foreign OCI base unexpectedly passed")
	}
}
