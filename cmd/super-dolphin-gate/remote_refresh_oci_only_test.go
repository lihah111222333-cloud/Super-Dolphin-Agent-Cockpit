package main

import (
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
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

func TestRemoteBaselineRegistryMigrationUsesConfiguredEnterpriseParent(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	configDocument := strings.Replace(
		validRemoteRunConfigJSON(),
		`"runtime": {"image": "registry.example/runtime@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},`,
		`"runtime": {"image": "enterprise.example/runtime@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
  "acr_registry_info": {"instance_id":"cri-enterprise","instance_name":"ci-registry","region_id":"cn-shenzhen","domain":"enterprise.example"},`,
		1,
	)
	configDocument = strings.Replace(configDocument, `"registry.example/runtime"`, `"enterprise.example/runtime"`, 1)
	configDocument = strings.Replace(configDocument, `"registry.example/oci-builder@`, `"enterprise.example/oci-builder@`, 1)
	config, err := loadRemoteRunConfig(writeRemoteRunConfigFixture(t, configDocument))
	if err != nil {
		t.Fatalf("load Enterprise config: %v", err)
	}
	accepted := remoteRunRunnerIdentityState()
	accepted.SchemaVersion, accepted.Generation = remoteci.BaselineStateSchemaVersion, 7
	accepted.RuntimeImage = "personal.example/runtime@" + digest
	accepted.MainCommit, accepted.MainTree = strings.Repeat("c", 40), strings.Repeat("b", 40)
	accepted.OCIProjectCache = &remoteci.BaselineOCIProjectCache{Image: accepted.RuntimeImage, ContentManifestSHA256: digest, MainTree: accepted.MainTree, ToolchainDigest: accepted.ToolchainDigest, Platform: accepted.Platform, CachePath: remoteci.OCIProjectGoBuildCachePath}
	accepted.CreatedAt, accepted.AcceptedAt = time.Date(2026, 8, 2, 1, 0, 0, 0, time.UTC), time.Date(2026, 8, 2, 1, 1, 0, 0, time.UTC)
	migration, err := remoteBaselineRegistryMigrationRequired(config, accepted)
	if err != nil {
		t.Fatalf("detect registry migration: %v", err)
	}
	if !migration {
		t.Fatal("Personal accepted baseline unexpectedly reusable with Enterprise target")
	}
	request := remoteci.OCIBaselineBuilderRequest{JobID: "oci-" + strings.Repeat("a", 24), ParentImage: config.Runtime.Image}
	create := remoteOCIBuildCreateRequest(config, request, "oci-builds/request.json", "oci-builds/context.tar")
	if err := eci.ValidateRegistryAccess(create.RegistryAccess, create.MainImage, create.InitImage); err != nil {
		t.Fatalf("Enterprise first-generation ECI images rejected: %v", err)
	}
	input := remoteBaselineRefreshInput{Identity: remoteci.BaselineIdentity{MainCommit: accepted.MainCommit, MainTree: accepted.MainTree, Platform: accepted.Platform, PolicyDigest: accepted.PolicyDigest, ToolchainDigest: accepted.ToolchainDigest, RuntimeImage: config.Runtime.Image}, GateSourceDigest: digest, RuntimeDependencyDigest: digest}
	cache := &remoteci.BaselineOCIProjectCache{Image: config.Runtime.Image, ContentManifestSHA256: digest, MainTree: input.Identity.MainTree, ToolchainDigest: input.Identity.ToolchainDigest, Platform: input.Identity.Platform, CachePath: remoteci.OCIProjectGoBuildCachePath}
	successor, err := newRemoteOCIBaselineState(accepted, input, cache)
	if err != nil {
		t.Fatalf("construct Enterprise successor: %v", err)
	}
	if successor.Generation != accepted.Generation+1 || successor.RuntimeImage != config.Runtime.Image {
		t.Fatalf("Enterprise successor = %#v", successor)
	}
}
