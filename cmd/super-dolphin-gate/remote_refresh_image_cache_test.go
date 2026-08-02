package main

import (
	"context"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

type fakeRemoteBaselineImageCacheAuthority struct {
	created eci.ImageCache
	ready   eci.ImageCache
	deletes []string
}

func (fake *fakeRemoteBaselineImageCacheAuthority) CreateImageCache(_ context.Context, _ eci.ImageCacheCreateRequest) (eci.ImageCache, error) {
	return fake.created, nil
}

func (fake *fakeRemoteBaselineImageCacheAuthority) WaitImageCacheReady(_ context.Context, _ string) (eci.ImageCache, error) {
	return fake.ready, nil
}

func (fake *fakeRemoteBaselineImageCacheAuthority) DeleteImageCache(_ context.Context, id string) error {
	fake.deletes = append(fake.deletes, id)
	return nil
}

func TestPromoteRemoteBaselineImageCacheCASPromotesThenRetiresAccepted(t *testing.T) {
	accepted, input, cache := imageCachePromotionFixture(t)
	ledger := t.TempDir() + "/baseline.sqlite"
	if err := writeRemoteBaselineState(ledger, accepted); err != nil {
		t.Fatalf("write accepted baseline: %v", err)
	}
	authority := &fakeRemoteBaselineImageCacheAuthority{
		created: eci.ImageCache{ID: "imc-successor", Name: "sd-baseline-2-" + input.Identity.MainTree[:12]},
		ready:   eci.ImageCache{ID: "imc-successor", Name: "sd-baseline-2-" + input.Identity.MainTree[:12], SnapshotID: "snap-successor", Status: "Ready", Images: []string{cache.Image}},
	}
	state, err := promoteRemoteBaselineImageCache(context.Background(), authority, ledger, accepted, input, cache)
	if err != nil {
		t.Fatalf("promote ImageCache successor: %v", err)
	}
	if state.Generation != 2 || state.ImageCacheID != "imc-successor" || state.ImageCacheSnapshotID != "snap-successor" || !state.ImageCacheReady {
		t.Fatalf("successor state = %#v", state)
	}
	if len(authority.deletes) != 1 || authority.deletes[0] != accepted.ImageCacheID {
		t.Fatalf("retired caches = %#v, want accepted cache only", authority.deletes)
	}
}

func TestPromoteRemoteBaselineImageCacheDeletesCandidateBeforeCASOnInvalidReadyResult(t *testing.T) {
	accepted, input, cache := imageCachePromotionFixture(t)
	ledger := t.TempDir() + "/baseline.sqlite"
	if err := writeRemoteBaselineState(ledger, accepted); err != nil {
		t.Fatalf("write accepted baseline: %v", err)
	}
	authority := &fakeRemoteBaselineImageCacheAuthority{
		created: eci.ImageCache{ID: "imc-candidate", Name: "sd-baseline-2-" + input.Identity.MainTree[:12]},
		ready:   eci.ImageCache{ID: "imc-candidate", Name: "sd-baseline-2-" + input.Identity.MainTree[:12], SnapshotID: "snap-candidate", Status: "Ready"},
	}
	if _, err := promoteRemoteBaselineImageCache(context.Background(), authority, ledger, accepted, input, cache); err == nil || !strings.Contains(err.Error(), "does not contain") {
		t.Fatalf("promote invalid ImageCache result error = %v", err)
	}
	if len(authority.deletes) != 1 || authority.deletes[0] != "imc-candidate" {
		t.Fatalf("candidate cleanup = %#v", authority.deletes)
	}
	persisted, err := loadRemoteBaselineState(ledger, false)
	if err != nil || !remoteBaselineStatesEquivalent(persisted, accepted) {
		t.Fatalf("accepted SQLite state changed after rejected candidate: %#v, %v", persisted, err)
	}
}

func imageCachePromotionFixture(t *testing.T) (remoteci.BaselineState, remoteBaselineRefreshInput, *remoteci.BaselineOCIProjectCache) {
	t.Helper()
	digest := "sha256:" + strings.Repeat("a", 64)
	input := remoteBaselineRefreshInput{Identity: remoteci.BaselineIdentity{MainCommit: strings.Repeat("b", 40), MainTree: strings.Repeat("c", 40), Platform: "linux/amd64", PolicyDigest: digest, ToolchainDigest: digest, RuntimeImage: "registry.example/runtime@" + digest}, GateSourceDigest: digest, RuntimeDependencyDigest: digest}
	cache := &remoteci.BaselineOCIProjectCache{Image: input.Identity.RuntimeImage, ContentManifestSHA256: digest, MainTree: input.Identity.MainTree, ToolchainDigest: input.Identity.ToolchainDigest, Platform: input.Identity.Platform, CachePath: remoteci.OCIProjectGoBuildCachePath}
	accepted, err := newRemoteOCIBaselineState(remoteci.BaselineState{}, input, cache, eci.ImageCache{ID: "imc-accepted", SnapshotID: "snap-accepted", Status: "Ready"})
	if err != nil {
		t.Fatalf("construct accepted baseline: %v", err)
	}
	input.Identity.MainCommit = strings.Repeat("d", 40)
	input.Identity.MainTree = strings.Repeat("e", 40)
	cache = &remoteci.BaselineOCIProjectCache{Image: input.Identity.RuntimeImage, ContentManifestSHA256: digest, MainTree: input.Identity.MainTree, ToolchainDigest: input.Identity.ToolchainDigest, Platform: input.Identity.Platform, CachePath: remoteci.OCIProjectGoBuildCachePath}
	return accepted, input, cache
}
