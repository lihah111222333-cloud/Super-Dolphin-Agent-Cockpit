package main

import (
	"errors"
	"strings"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/shardresource"
)

const remoteRunConfigSchemaVersion uint32 = 7

const (
	remoteContainerReportAllowance = 30 * time.Second
)

const remoteCalibrationResultSchemaVersion uint32 = 2

const remoteCalibrationLockWaitTimeout = 75 * time.Minute

var errRemoteCalibrationSamplesIncomplete = errors.New("remote calibration samples are incomplete")

type remoteRunConfig struct {
	SchemaVersion     uint32 `json:"schema_version"`
	AliyunCLI         string `json:"aliyun_cli"`
	CredentialProfile string `json:"credential_profile"`
	RegionID          string `json:"region_id"`
	VSwitchID         string `json:"vswitch_id"`
	SecurityGroupID   string `json:"security_group_id"`
	WorkerRoleName    string `json:"worker_role_name"`
	OSS               struct {
		Bucket           string `json:"bucket"`
		Endpoint         string `json:"endpoint"`
		InternalEndpoint string `json:"internal_endpoint"`
		SourcePrefix     string `json:"source_prefix"`
		BaselinePrefix   string `json:"baseline_prefix"`
	} `json:"oss"`
	Runtime struct {
		Image string `json:"image"`
	} `json:"runtime"`
	OCICache struct {
		RegistryRepository string `json:"registry_repository"`
		RemoteBuilderImage string `json:"remote_builder_image"`
	} `json:"oci_cache"`
	Capacity struct {
		SeedClass      string               `json:"seed_class"`
		ResourcePolicy shardresource.Policy `json:"resource_policy"`
	} `json:"capacity"`
}

// Validate 校验远程运行需要的云身份、不可变镜像、缓存容量上限和分片资源。
func (config remoteRunConfig) Validate() error {
	if config.SchemaVersion != remoteRunConfigSchemaVersion {
		return errors.New("remote CI config schema_version must equal 7")
	}
	if err := validateRemoteCloudIdentity(config); err != nil {
		return err
	}
	if err := validateRemoteStorageConfig(config); err != nil {
		return err
	}
	if !validRemoteRuntimeImage(config.Runtime.Image) {
		return errors.New("remote CI runtime image must use an immutable digest")
	}
	if err := validateRemoteOCICacheConfig(config); err != nil {
		return err
	}
	if err := validateRemoteShardCapacity(config); err != nil {
		return err
	}
	return nil
}

// validateRemoteOCICacheConfig 仅接受远程 OCI 缓存仓库和固定 ECI builder
// 镜像，不接受宿主 BuildKit 或 Buildx 配置。
func validateRemoteOCICacheConfig(config remoteRunConfig) error {
	cache := config.OCICache
	if cache.RegistryRepository == "" || strings.Contains(cache.RegistryRepository, "@") || strings.Contains(cache.RegistryRepository, "://") {
		return errors.New("remote OCI cache registry_repository is invalid")
	}
	if !validRemoteRuntimeImage(cache.RemoteBuilderImage) {
		return errors.New("remote OCI cache remote_builder_image must use an immutable digest")
	}
	return nil
}

type remoteRunOptions struct {
	ConfigPath           string
	RepositoryRoot       string
	RemoteName           string
	RemoteURL            string
	Commit               string
	Tree                 string
	ParentCommit         string
	Base                 string
	Profile              string
	Scenario             string
	Entrypoint           string
	Tests                []string
	LocalRef             string
	RemoteRef            string
	ObservedRemote       string
	UpdateKind           string
	LedgerPath           string
	RequesterFingerprint gatecontract.RequesterFingerprint
	Calibration          bool
	ForceRerun           bool
}

type remoteStringListFlag []string

// String 将重复测试选择器编码为 flag.Value 可显示的逗号列表。
func (values *remoteStringListFlag) String() string { return strings.Join(*values, ",") }

// Set 拒绝空测试选择器并保留调用顺序。
func (values *remoteStringListFlag) Set(value string) error {
	if value == "" {
		return errors.New("test selector must not be empty")
	}
	*values = append(*values, value)
	return nil
}

type remoteCalibrationResult struct {
	SchemaVersion        uint32    `json:"schema_version"`
	Commit               string    `json:"commit"`
	Tree                 string    `json:"tree"`
	RunnerManifestDigest string    `json:"runner_manifest_digest"`
	CommitJobID          string    `json:"commit_job_id"`
	PushJobID            string    `json:"push_job_id"`
	ReleaseJobID         string    `json:"release_job_id"`
	LedgerGeneration     uint64    `json:"ledger_generation"`
	WorkloadCount        int       `json:"workload_count"`
	RacePackageCount     int       `json:"race_package_count"`
	CompletedAt          time.Time `json:"completed_at"`
}
