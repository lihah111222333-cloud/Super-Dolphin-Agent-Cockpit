package main

import (
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

func TestNewRemoteOCIBaselineStateRejectsGenerationOverflow(t *testing.T) {
	accepted := remoteci.BaselineState{Generation: ^uint64(0)}
	if _, err := newRemoteOCIBaselineState(accepted, remoteBaselineRefreshInput{}, nil); err == nil || !strings.Contains(err.Error(), "generation is exhausted") {
		t.Fatalf("newRemoteOCIBaselineState() error = %v, want generation overflow", err)
	}
}

func TestNewRemoteOCIBaselineStateBindsOnlyOCIIdentity(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	commit := strings.Repeat("b", 40)
	tree := strings.Repeat("c", 40)
	input := remoteBaselineRefreshInput{
		Identity:         remoteci.BaselineIdentity{MainCommit: commit, MainTree: tree, Platform: "linux/amd64", PolicyDigest: digest, ToolchainDigest: digest, RuntimeImage: "registry.example.com/super@" + digest},
		GateSourceDigest: digest, RuntimeDependencyDigest: digest,
	}
	cache := &remoteci.BaselineOCIProjectCache{Image: input.Identity.RuntimeImage, ContentManifestSHA256: digest, MainTree: tree, ToolchainDigest: digest, Platform: "linux/amd64", CachePath: remoteci.OCIProjectGoBuildCachePath}
	state, err := newRemoteOCIBaselineState(remoteci.BaselineState{}, input, cache)
	if err != nil {
		t.Fatalf("newRemoteOCIBaselineState() error = %v", err)
	}
	if state.Generation != 1 || state.OCIProjectCache != cache || state.CreatedAt.Location() != time.UTC || !state.AcceptedAt.Equal(state.CreatedAt) {
		t.Fatalf("OCI state binding = %#v", state)
	}
}
