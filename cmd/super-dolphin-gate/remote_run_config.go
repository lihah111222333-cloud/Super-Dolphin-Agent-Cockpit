package main

import (
	"errors"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/shardresource"
)

const remoteRunConfigSchemaVersion uint32 = 10

const (
	remoteContainerReportAllowance = 30 * time.Second
)

const remoteCalibrationResultSchemaVersion uint32 = 4

var errRemoteCalibrationSamplesIncomplete = errors.New("remote calibration samples are incomplete")

type remoteRunConfig struct {
	SchemaVersion          uint32                                    `json:"schema_version"`
	AliyunCLI              string                                    `json:"aliyun_cli"`
	CredentialProfile      string                                    `json:"credential_profile"`
	RegionID               string                                    `json:"region_id"`
	VSwitches              []cicontract.ECIVSwitch                   `json:"vswitches"`
	SecurityGroupID        string                                    `json:"security_group_id"`
	WorkerRoleName         string                                    `json:"worker_role_name"`
	GenerationOneProvision *cicontract.GenerationOneProvisionReceipt `json:"generation_one_provision,omitempty"`
	OSS                    struct {
		Bucket           string `json:"bucket"`
		Endpoint         string `json:"endpoint"`
		InternalEndpoint string `json:"internal_endpoint"`
		SourcePrefix     string `json:"source_prefix"`
	} `json:"oss"`
	Capacity struct {
		ResourcePolicy shardresource.Policy `json:"resource_policy"`
	} `json:"capacity"`
}

// Validate 校验 normal CI 所需的云身份、对象存储和分片资源。
func (config remoteRunConfig) Validate() error {
	if config.SchemaVersion != remoteRunConfigSchemaVersion {
		return errors.New("remote CI config schema_version must equal 10")
	}
	if err := validateRemoteCloudIdentity(config); err != nil {
		return err
	}
	if err := validateRemoteStorageConfig(config); err != nil {
		return err
	}
	if err := validateRemoteShardCapacity(config); err != nil {
		return err
	}
	if config.GenerationOneProvision != nil {
		if err := config.GenerationOneProvision.Validate(); err != nil {
			return errors.New("remote CI generation_one_provision is invalid: " + err.Error())
		}
	}
	return nil
}

type remoteRunOptions struct {
	ConfigPath            string
	RepositoryRoot        string
	RemoteName            string
	RemoteURL             string
	Commit                string
	Tree                  string
	ParentCommit          string
	Base                  string
	Profile               string
	Scenario              string
	Entrypoint            string
	Tests                 []string
	WorkloadID            string
	CompletionReceiptPath string
	LocalRef              string
	RemoteRef             string
	ObservedRemote        string
	UpdateKind            string
	LedgerPath            string
	AgentTokenDigest      string
	Calibration           bool
	Force                 bool
	ProgressObserver      remoteci.ProgressObserver
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
	SchemaVersion                uint32    `json:"schema_version"`
	Force                        bool      `json:"force"`
	Commit                       string    `json:"commit"`
	Tree                         string    `json:"tree"`
	RunnerManifestDigest         string    `json:"runner_manifest_digest"`
	CalibrationResourceClassID   string    `json:"calibration_resource_class_id"`
	CalibrationResourceCPU       float64   `json:"calibration_resource_cpu"`
	CalibrationResourceMemoryGiB float64   `json:"calibration_resource_memory_gib"`
	CommitJobID                  string    `json:"commit_job_id"`
	PushJobID                    string    `json:"push_job_id"`
	ReleaseJobID                 string    `json:"release_job_id"`
	LedgerGeneration             uint64    `json:"ledger_generation"`
	WorkloadCount                int       `json:"workload_count"`
	RacePackageCount             int       `json:"race_package_count"`
	CompletedAt                  time.Time `json:"completed_at"`
}
