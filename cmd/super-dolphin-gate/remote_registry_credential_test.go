package main

import (
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/shardresource"
)

// TestLoadRemoteRegistryCredentialRequiresBothEnvironmentValues 覆盖环境短期凭据的完整加载与 fail-fast。
func TestLoadRemoteRegistryCredentialRequiresBothEnvironmentValues(t *testing.T) {
	t.Setenv(remoteRegistryUsernameEnvironment, "ci-user")
	t.Setenv(remoteRegistryTokenEnvironment, "ci-token")
	credential, err := loadRemoteRegistryCredential()
	if err != nil {
		t.Fatalf("loadRemoteRegistryCredential() error = %v", err)
	}
	if credential.Server != remoteRuntimeRegistryServer || credential.UserName != "ci-user" || credential.Password != "ci-token" {
		t.Fatalf("loadRemoteRegistryCredential() = %#v", credential)
	}
	t.Setenv(remoteRegistryTokenEnvironment, "")
	if _, err := loadRemoteRegistryCredential(); err == nil {
		t.Fatal("loadRemoteRegistryCredential() accepted an empty token")
	}
}

// TestNewRemoteRunCoordinatorDefersRegistryCredentialReadUntilActualCreate 验证实际创建前延迟读取 registry 凭据。
// 验证全命中准备可在无 GHCR 环境变量时构造运行时。
func TestNewRemoteRunCoordinatorDefersRegistryCredentialReadUntilActualCreate(t *testing.T) {
	t.Setenv(remoteRegistryUsernameEnvironment, "")
	t.Setenv(remoteRegistryTokenEnvironment, "")
	config := remoteRunConfig{
		AliyunCLI:         "aliyun",
		CredentialProfile: "ci-profile",
		RegionID:          "cn-shenzhen",
		VSwitches: []cicontract.ECIVSwitch{
			{ID: "vsw-zone-a", ZoneID: "cn-test-a"},
			{ID: "vsw-zone-b", ZoneID: "cn-test-b"},
		},
		SecurityGroupID: "sg-remote-ci",
		WorkerRoleName:  "remote-ci-worker",
	}
	config.OSS.Bucket = "ci-bucket"
	config.OSS.Endpoint = "https://oss-cn-shenzhen.aliyuncs.com"
	config.OSS.InternalEndpoint = "oss-cn-shenzhen-internal.aliyuncs.com"
	config.OSS.SourcePrefix = "baseline-artifacts/source-bundles/"
	config.Capacity.ResourcePolicy = deferredCredentialTestResourcePolicy()
	input := remoteci.RunInput{
		Profile:              gatecontract.ProfileLocalFast,
		ImageCacheSnapshotID: "snap-accepted-baseline",
	}
	coordinator, _, err := newRemoteRunCoordinator(config, input)
	if err != nil {
		t.Fatalf("newRemoteRunCoordinator() error = %v", err)
	}
	if coordinator == nil {
		t.Fatal("newRemoteRunCoordinator() returned nil coordinator")
	}
}

func deferredCredentialTestResourcePolicy() shardresource.Policy {
	return shardresource.Policy{
		Classes: []shardresource.Class{
			{ID: "small", VCPU: 2, MemoryGiB: 4},
			{ID: "medium", VCPU: 4, MemoryGiB: 8},
			{ID: "maximum", VCPU: 8, MemoryGiB: 16},
		},
		Bootstrap: shardresource.BootstrapClasses{
			Guard: "small", NodeTest: "small", GoTest: "small",
		},
		CalibrationResource:         shardresource.Class{ID: "calibration", VCPU: 4, MemoryGiB: 8},
		FastWorkloadMaxDurationMS:   5_000,
		MediumWorkloadMaxDurationMS: 70_000,
		HeadroomPercent:             25,
		MinSamplesToDownsize:        5,
	}
}
