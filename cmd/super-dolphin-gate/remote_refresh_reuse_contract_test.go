package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

func TestBindRemoteBaselineDeltaReusesAnchorAndRejectsRuntimeSeedDrift(t *testing.T) {
	accepted := remoteBaselineStateFixture()
	generation := accepted.Generation + 1
	acceptedAt := accepted.AcceptedAt.Add(time.Minute)
	manifest := remoteci.BaselineManifest{
		SchemaVersion:             remoteci.BaselineManifestSchemaVersion,
		Generation:                generation,
		MainCommit:                repeatRemoteHex("a", 40),
		MainTree:                  repeatRemoteHex("b", 40),
		Platform:                  accepted.Platform,
		PolicyDigest:              "sha256:" + repeatRemoteHex("e", 64),
		ToolchainDigest:           accepted.ToolchainDigest,
		RuntimeImage:              accepted.RuntimeImage,
		GateSourceSHA256:          "sha256:" + repeatRemoteHex("f", 64),
		RuntimeSeedManifestSHA256: accepted.RuntimeSeedSHA256,
		Layers: []remoteci.BaselineLayer{{
			Name: "source", BaseCommit: accepted.MainCommit, BaseTree: accepted.MainTree,
			TargetCommit: repeatRemoteHex("a", 40), TargetTree: repeatRemoteHex("b", 40),
		}},
	}
	stage := remoteBaselineArtifactStage{
		generation: generation, generationPrefix: fmt.Sprintf("baseline-artifacts/%d/", generation),
	}
	var state remoteci.BaselineState
	if err := bindRemoteBaselineDelta(&state, accepted, stage, manifest, "sha256:"+repeatRemoteHex("c", 64), acceptedAt); err != nil {
		t.Fatalf("bindRemoteBaselineDelta() error = %v", err)
	}
	if state.Anchor != accepted.Anchor || state.DataCacheID != accepted.Anchor.DataCacheID ||
		len(state.Deltas) != len(accepted.Deltas)+1 || state.Deltas[len(state.Deltas)-1].BaseCommit != accepted.MainCommit {
		t.Fatalf("delta state did not reuse the accepted Anchor chain: %#v", state)
	}

	manifest.RuntimeSeedManifestSHA256 = "sha256:" + repeatRemoteHex("d", 64)
	if err := bindRemoteBaselineDelta(&remoteci.BaselineState{}, accepted, stage, manifest, "sha256:"+repeatRemoteHex("c", 64), acceptedAt); err == nil {
		t.Fatal("bindRemoteBaselineDelta() accepted runtime-seed drift")
	}
}

func TestRemoteBaselineRenewTokenIsDailyAndGenerationScoped(t *testing.T) {
	first := remoteBaselineRenewToken(7, time.Date(2026, 7, 27, 1, 5, 0, 0, time.UTC))
	sameDay := remoteBaselineRenewToken(7, time.Date(2026, 7, 27, 23, 59, 0, 0, time.UTC))
	nextDay := remoteBaselineRenewToken(7, time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC))
	nextGeneration := remoteBaselineRenewToken(8, time.Date(2026, 7, 27, 1, 5, 0, 0, time.UTC))
	if first != sameDay || first == nextDay || first == nextGeneration || !strings.Contains(first, "renew-7-") {
		t.Fatalf("renew tokens = %q, %q, %q, %q", first, sameDay, nextDay, nextGeneration)
	}
}

func TestUploadRemoteBaselineArtifactsCleansPartialGeneration(t *testing.T) {
	failure := errors.New("injected bundle upload failure")
	store := &fakeRemoteBaselineOSSStore{uploadErrors: []error{nil, failure}}
	stage := remoteBaselineArtifactStage{
		generationPrefix: "baseline-artifacts/7/",
		inputPrefix:      "baseline-artifacts/7/input/",
		outputPrefix:     "baseline-artifacts/7/output/",
		seedScriptPath:   "/tmp/seed.sh",
		source: remoteBaselineSourceArtifact{
			ManifestPath: "/tmp/source-manifest.json",
			BundlePath:   "/tmp/source.bundle",
		},
	}
	err := uploadRemoteBaselineArtifactsWithCleanup(context.Background(), store, stage)
	if err == nil || !strings.Contains(err.Error(), failure.Error()) {
		t.Fatalf("uploadRemoteBaselineArtifactsWithCleanup() error = %v", err)
	}
	if len(store.uploadedKeys) != 2 || store.uploadedKeys[0] != stage.inputPrefix+"seed.sh" ||
		len(store.deletedPrefixes) != 1 ||
		store.deletedPrefixes[0] != stage.generationPrefix {
		t.Fatalf("uploads = %v, deleted prefixes = %v", store.uploadedKeys, store.deletedPrefixes)
	}
}
