package main

import (
	"context"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

func requireRemoteBaselineSourceArtifact(t *testing.T, ctx context.Context, repository string, accepted remoteci.BaselineState, target remoteci.BaselineIdentity, destination string) remoteBaselineSourceArtifact {
	t.Helper()
	artifact, err := buildRemoteBaselineSourceArtifact(ctx, repository, accepted, target, destination)
	if err != nil {
		t.Fatalf("build remote baseline source artifact: %v", err)
	}
	return artifact
}

func assertRemoteBaselineFullArtifact(t *testing.T, artifact remoteBaselineSourceArtifact) {
	t.Helper()
	if artifact.Manifest.Mode != remoteBaselineSourceFull || artifact.Manifest.BundleSHA256 == "" || artifact.Manifest.BundleSize <= 0 || artifact.ManifestSHA256 == "" {
		t.Fatalf("full artifact = %#v", artifact)
	}
}

func assertRemoteBaselineDeltaArtifact(t *testing.T, artifact remoteBaselineSourceArtifact, baseCommit string, baseTree string) {
	t.Helper()
	if artifact.Manifest.Mode != remoteBaselineSourceDelta || artifact.Manifest.BaseCommit != baseCommit || artifact.Manifest.BaseTree != baseTree || artifact.Manifest.BundleSHA256 == "" || artifact.Manifest.BundleSize <= 0 {
		t.Fatalf("delta artifact = %#v", artifact)
	}
}
