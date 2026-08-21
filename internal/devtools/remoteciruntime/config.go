package remoteciruntime

import (
	"errors"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/shardresource"
)

// ConfigSchemaVersion 是 normal remote CI 配置的唯一 schema 版本。
const ConfigSchemaVersion uint32 = 10

// OSSConfig 描述 remote CI 唯一 OSS 对象传输边界。
type OSSConfig struct {
	Bucket           string `json:"bucket"`
	Endpoint         string `json:"endpoint"`
	InternalEndpoint string `json:"internal_endpoint"`
	SourcePrefix     string `json:"source_prefix"`
}

// CapacityConfig 描述 remote CI 唯一分片资源策略。
type CapacityConfig struct {
	ResourcePolicy shardresource.Policy `json:"resource_policy"`
}

// Config 是 Gate 与未来独立 host 共享的 strict remote runtime 配置。
type Config struct {
	SchemaVersion          uint32                                    `json:"schema_version"`
	AliyunCLI              string                                    `json:"aliyun_cli"`
	CredentialProfile      string                                    `json:"credential_profile"`
	RegionID               string                                    `json:"region_id"`
	VSwitches              []cicontract.ECIVSwitch                   `json:"vswitches"`
	SecurityGroupID        string                                    `json:"security_group_id"`
	WorkerRoleName         string                                    `json:"worker_role_name"`
	OSS                    OSSConfig                                 `json:"oss"`
	Capacity               CapacityConfig                            `json:"capacity"`
	GenerationOneProvision *cicontract.GenerationOneProvisionReceipt `json:"generation_one_provision,omitempty"`
}

// LoadConfig 从显式路径 strict decode 并完整校验 remote runtime 配置。
func LoadConfig(configPath string) (Config, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return Config{}, err
	}
	var config Config
	if err := gatecontract.DecodeCanonicalStrictJSON(data, &config); err != nil {
		return Config{}, err
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

// Validate 校验 normal CI 所需的云身份、对象存储和分片资源。
func (config Config) Validate() error {
	if config.SchemaVersion != ConfigSchemaVersion {
		return errors.New("remote CI config schema_version must equal 10")
	}
	if err := validateCloudIdentity(config); err != nil {
		return err
	}
	if err := validateStorageConfig(config); err != nil {
		return err
	}
	if err := validateShardCapacity(config); err != nil {
		return err
	}
	if config.GenerationOneProvision != nil {
		if err := config.GenerationOneProvision.Validate(); err != nil {
			return errors.New("remote CI generation_one_provision is invalid: " + err.Error())
		}
	}
	return nil
}

func validateCloudIdentity(config Config) error {
	for _, value := range []string{config.AliyunCLI, config.CredentialProfile, config.RegionID, config.SecurityGroupID, config.WorkerRoleName} {
		if strings.TrimSpace(value) == "" {
			return errors.New("remote CI Aliyun identity and network settings are incomplete")
		}
	}
	if err := cicontract.ValidateECIMultiZoneScheduling(cicontract.ECIMultiZoneScheduleStrategy, config.VSwitches); err != nil {
		return err
	}
	if path.Base(config.AliyunCLI) != "aliyun" {
		return errors.New("remote CI cloud client must be the Alibaba Cloud aliyun CLI")
	}
	return nil
}

func validateStorageConfig(config Config) error {
	if strings.TrimSpace(config.OSS.Bucket) == "" || strings.TrimSpace(config.OSS.Endpoint) == "" || strings.TrimSpace(config.OSS.InternalEndpoint) == "" ||
		!validOSSPrefix(config.OSS.SourcePrefix) {
		return errors.New("remote CI OSS settings are incomplete")
	}
	return nil
}

func validOSSPrefix(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || strings.HasPrefix(value, "/") ||
		!strings.HasSuffix(value, "/") || strings.ContainsAny(value, "\\\x00\r\n") {
		return false
	}
	withoutSlash := strings.TrimSuffix(value, "/")
	for segment := range strings.SplitSeq(withoutSlash, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return path.Clean(withoutSlash) == withoutSlash
}

func validateShardCapacity(config Config) error {
	if err := config.Capacity.ResourcePolicy.Validate(); err != nil {
		return fmt.Errorf("remote CI resource policy: %w", err)
	}
	if _, err := config.Capacity.ResourcePolicy.ResolveCalibrationClass(); err != nil {
		return fmt.Errorf("remote CI calibration resource class: %w", err)
	}
	return nil
}
