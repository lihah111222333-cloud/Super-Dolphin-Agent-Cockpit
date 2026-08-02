package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

type remoteOCIBuildPrefixStore struct {
	prefix string
	err    error
}

func (store *remoteOCIBuildPrefixStore) DeletePrefix(_ context.Context, prefix string) error {
	store.prefix = prefix
	return store.err
}

func TestRemoteOCIBuildKitArgsBindReceiptToTheImmutableRequest(t *testing.T) {
	request := remoteOCIWorkerCheckRequest()
	for _, want := range []string{
		"--opt=build-arg:BUILD_SOURCE_TREE=" + request.TargetTree,
		"--opt=build-arg:ACCEPTED_SNAPSHOT_ID=" + request.ParentImageSnapshotID,
		"--opt=build-arg:IMAGE_INPUT_DIGEST=" + request.ImageInputDigest,
	} {
		if !strings.Contains(strings.Join(remoteOCIBuildKitArgs("/work", request), "\n"), want) {
			t.Fatalf("BuildKit args do not bind %q", want)
		}
	}
}

func TestRemoteOCIBuildUsesOneDaemonSessionWithoutDiskCacheTransfer(t *testing.T) {
	source, err := os.ReadFile("remote_oci_baseline_worker.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, required := range []string{
		"startRemoteOCIBuildKit(ctx, workspace)",
		"buildKit.run(ctx, receiptArgs)",
		"buildKit.run(ctx, imageArgs)",
		"DecodeOCIBuilderRefreshReceiptArtifact(receiptData, request)",
		"/usr/bin/buildkitd",
		"/usr/bin/buildctl",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("OCI build worker does not retain one-daemon receipt-to-push reuse: %q", required)
		}
	}
	for _, forbidden := range []string{"--export-cache=type=local", "--import-cache=type=local", "buildctl-daemonless.sh", "remoteOCIBuildCheckReceipts", "strings.SplitSeq"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("OCI build worker retains disk cache transfer path: %q", forbidden)
		}
	}
}

func remoteOCIWorkerCheckRequest() remoteci.OCIBaselineBuilderRequest {
	return remoteci.OCIBaselineBuilderRequest{ImageInputDigest: "sha256:plan", TargetTree: strings.Repeat("a", 40), ParentImageSnapshotID: "snap-accepted"}
}

func TestCleanupRemoteOCIBuildObjectsDeletesExactJobPrefix(t *testing.T) {
	store := &remoteOCIBuildPrefixStore{}
	if err := cleanupRemoteOCIBuildObjects(store, "source/oci-builds/oci-123"); err != nil {
		t.Fatalf("cleanup remote OCI objects: %v", err)
	}
	if store.prefix != "source/oci-builds/oci-123" {
		t.Fatalf("deleted prefix = %q", store.prefix)
	}
}

func TestCleanupRemoteOCIBuildObjectsReturnsDeletionFailure(t *testing.T) {
	store := &remoteOCIBuildPrefixStore{err: errors.New("unavailable")}
	if err := cleanupRemoteOCIBuildObjects(store, "source/oci-builds/oci-123"); err == nil {
		t.Fatal("cleanup remote OCI objects unexpectedly accepted deletion failure")
	}
}

func TestRemoteOCIInitScriptCopiesAcceptedSnapshotAndDelta(t *testing.T) {
	script := remoteOCIInitScript("source/oci-builds/oci-123/request.job.json", "source/oci-builds/oci-123/source.snapshot.delta.tar")
	for _, want := range []string{
		cicontract.SourceSnapshotRootPath,
		cicontract.SourceSnapshotManifestPath,
		"/work-data/root",
		"/work-data/manifest.json",
		"/source-data/source.snapshot.delta.tar",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("init script missing %q: %s", want, script)
		}
	}
}
