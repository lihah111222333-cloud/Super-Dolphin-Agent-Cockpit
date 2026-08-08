package eci

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

const (
	sourceVolumeName = "source-data"
	workVolumeName   = "work-data"
	tempVolumeName   = "temp-data"
)

// validateConfig 在启动 CLI 前阻断不完整基础设施配置和不可表示的资源值。
func validateConfig(config Config) error {
	fields := []struct {
		name  string
		value string
	}{
		{"CLI binary", config.Binary}, {"region ID", config.RegionID}, {"security group ID", config.SecurityGroupID},
		{"worker role name", config.WorkerRoleName}, {"profile", config.Profile},
	}
	if err := cicontract.ValidateECIMultiZoneScheduling(cicontract.ECIMultiZoneScheduleStrategy, config.VSwitches); err != nil {
		return err
	}
	for _, field := range fields {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("ECI %s is required", field.name)
		}
	}
	switch config.SpotStrategy {
	case SpotStrategyAsPriceGo:
		if config.SpotDurationHours != 1 {
			return errors.New("ECI spot duration must equal one hour")
		}
	case SpotStrategyNoSpot:
		if config.SpotDurationHours != 0 {
			return errors.New("ECI pay-as-you-go must not set a spot duration")
		}
	default:
		return fmt.Errorf("ECI spot strategy must be %q or %q", SpotStrategyAsPriceGo, SpotStrategyNoSpot)
	}
	if config.Deadline <= 0 || config.Deadline%time.Second != 0 {
		return errors.New("ECI deadline must be a positive whole number of seconds")
	}
	return validateRegistryCredentialConfig(config)
}

// validateRegistryCredentialConfig 校验静态凭据与延迟 loader 的互斥边界。
func validateRegistryCredentialConfig(config Config) error {
	if err := validateOptionalRegistryCredential(config.RegistryCredential); err != nil {
		return err
	}
	if config.RegistryCredentialLoader != nil && hasRegistryCredentialValues(config.RegistryCredential) {
		return errors.New("ECI registry credential must use either a static credential or a deferred loader")
	}
	return nil
}

// hasRegistryCredentialValues 判断静态凭据是否带入任一字段。
func hasRegistryCredentialValues(credential RegistryCredential) bool {
	return credential.Server != "" || credential.UserName != "" || credential.Password != ""
}

// validateOptionalRegistryCredential 只允许构造客户端时完全不带凭据或完整带入三个凭据字段。
func validateOptionalRegistryCredential(credential RegistryCredential) error {
	values := []string{credential.Server, credential.UserName, credential.Password}
	populated := 0
	for _, value := range values {
		if value != "" {
			populated++
		}
	}
	if populated != 0 && populated != len(values) {
		return errors.New("ECI registry credential must be either absent or complete")
	}
	if populated != 0 {
		for _, value := range values {
			if !validRegistryCredentialValue(value) {
				return errors.New("ECI registry credential contains leading, trailing, or control whitespace")
			}
		}
	}
	return nil
}

// validateRegistryCredential 要求创建分片时提供完整凭据并绑定两个不可变镜像的同一 registry。
func validateRegistryCredential(credential RegistryCredential, request CreateRequest) error {
	if !validRegistryCredentialValue(credential.Server) || !validRegistryCredentialValue(credential.UserName) || !validRegistryCredentialValue(credential.Password) {
		return errors.New("ECI private registry credential is required for container creation")
	}
	if strings.Contains(credential.Server, "://") || strings.ContainsAny(credential.Server, "/ ") || len(credential.Server) > 256 {
		return errors.New("ECI registry credential server is invalid")
	}
	if len(credential.UserName) > 256 || len(credential.Password) > 256 {
		return errors.New("ECI registry credential exceeds 256 characters")
	}
	if !imagesUseRegistry(credential.Server, request.MainImage, request.InitImage) {
		return errors.New("ECI registry credential server does not match the immutable container images")
	}
	return nil
}

// validRegistryCredentialValue rejects empty values and any whitespace/control
// rune so credentials cannot be split across CLI arguments or error output.
func validRegistryCredentialValue(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && strings.IndexFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) < 0
}

// imagesUseRegistry 要求主容器和物料容器都绑定同一个显式 registry 域名。
func imagesUseRegistry(server string, images ...string) bool {
	for _, image := range images {
		imageServer, _, found := strings.Cut(image, "/")
		if !found || imageServer != server {
			return false
		}
	}
	return true
}

func validateResources(cpu float64, memoryGiB float64) error {
	allowed := map[float64]map[float64]bool{
		2: {4: true},
		4: {8: true},
		8: {16: true},
	}
	if cpu > 8 || memoryGiB > 16 || !allowed[cpu][memoryGiB] {
		return errors.New("ECI resources must use exactly 2C/4GiB, 4C/8GiB, or 8C/16GiB")
	}
	return nil
}

// validateCreateRequest 按容器、卷、挂载和标签阶段校验 ECI 分片请求。
func validateCreateRequest(request CreateRequest) error {
	if strings.TrimSpace(request.ImageCacheSnapshotID) == "" {
		return errors.New("ECI image cache snapshot ID is required; container groups must pin an explicitly Ready image snapshot")
	}
	for _, check := range []func(CreateRequest) error{
		validateContainerNames, validateRequestImages, validateRequestContainers, validateRequestVolumes,
		validateRequestMounts, validateRequestTags,
	} {
		if err := check(request); err != nil {
			return err
		}
	}
	if err := validateConfigFileProjectionValues(request.ConfigFileVolumes,
		append(sensitiveEnvironmentValues(request.Environment), sensitiveEnvironmentValues(request.InitContainer.Environment)...)...,
	); err != nil {
		return err
	}
	return nil
}

// validateRequestImages requires each container to name its own immutable image.
// There is deliberately no config-level fallback: the caller must bind both roles.
func validateRequestImages(request CreateRequest) error {
	for index, image := range []string{request.MainImage, request.InitImage} {
		if !imageDigestPattern.MatchString(image) {
			return fmt.Errorf("ECI image %d must be a repository@sha256:<64 lowercase hex> digest reference", index+1)
		}
	}
	return nil
}

func validateContainerNames(request CreateRequest) error {
	if !eciNamePattern.MatchString(request.ContainerGroupName) || !eciNamePattern.MatchString(request.ContainerName) ||
		!eciNamePattern.MatchString(request.InitContainer.Name) || request.InitContainer.Name == request.ContainerName {
		return errors.New("ECI container names are invalid or not distinct")
	}
	return nil
}

func validateRequestContainers(request CreateRequest) error {
	if err := validateResources(request.Resources.CPU, request.Resources.MemoryGiB); err != nil {
		return err
	}
	if err := validateContainerInput("task container", request.Command, request.Args, request.Environment); err != nil {
		return err
	}
	return validateContainerInput("init container", request.InitContainer.Command, request.InitContainer.Args, request.InitContainer.Environment)
}

// validateRequestVolumes 校验三个 shard-local EmptyDir 以及额外只读 ConfigFileVolume 投影。
func validateRequestVolumes(request CreateRequest) error {
	emptyDirNames := []string{request.SourceVolume.Name, request.WorkVolume.Name, request.TempVolume.Name}
	if err := validateRequestVolumeNames(emptyDirNames); err != nil {
		return err
	}
	return validateConfigFileVolumes(request.ConfigFileVolumes, emptyDirNames)
}

// validateRequestVolumeNames 校验固定 EmptyDir 卷名集合。
func validateRequestVolumeNames(names []string) error {
	expected := []string{sourceVolumeName, workVolumeName, tempVolumeName}
	if len(names) != len(expected) {
		return fmt.Errorf("ECI request must declare exactly %d EmptyDir volumes", len(expected))
	}
	for index, name := range names {
		if name != expected[index] {
			return fmt.Errorf("ECI request EmptyDir volume %d must be %q, got %q", index+1, expected[index], name)
		}
	}
	return nil
}

func validateRequestMounts(request CreateRequest) error {
	mainNames := createMainMountNames(request)
	mainReadOnly := []string{request.SourceVolume.Name}
	if err := validateVolumeMounts("task container", request.MainVolumeMounts, mainNames, mainNames, mainReadOnly); err != nil {
		return err
	}
	initAllowed := createInitMountNames(request)
	initRequired := createRequiredInitMountNames(request)
	return validateVolumeMounts("init container", request.InitVolumeMounts, initAllowed, initRequired, createConfigFileVolumeNames(request))
}

func validateRequestTags(request CreateRequest) error {
	if len(request.Tags) > 20 {
		return errors.New("ECI supports at most 20 tags")
	}
	for key, value := range request.Tags {
		if err := validateTag(key, value); err != nil {
			return err
		}
	}
	return nil
}

// validateContainerInput 校验容器命令、参数和环境变量的显式输入边界。
func validateContainerInput(owner string, command []string, args []string, environment map[string]string) error {
	if len(command) == 0 || len(command) > 20 {
		return fmt.Errorf("ECI %s command must contain 1-20 entries", owner)
	}
	for index, value := range command {
		if strings.TrimSpace(value) == "" || len(value) > 256 {
			return fmt.Errorf("ECI %s command %d must be non-empty and at most 256 characters", owner, index+1)
		}
	}
	if len(args) > 10 {
		return fmt.Errorf("ECI %s arguments support at most 10 entries", owner)
	}
	for key, value := range environment {
		if !environmentKeyPattern.MatchString(key) {
			return fmt.Errorf("ECI %s environment variable %q has an invalid name", owner, key)
		}
		if len(value) > 256 {
			return fmt.Errorf("ECI %s environment variable %q exceeds 256 characters", owner, key)
		}
	}
	return nil
}

// validateVolumeMounts 校验必需卷均已挂载，且所有挂载只引用允许卷并保持路径唯一。
func validateVolumeMounts(
	owner string,
	mounts []VolumeMount,
	allowedNames []string,
	requiredNames []string,
	readOnlyNames []string,
) error {
	if len(mounts) < len(requiredNames) {
		return fmt.Errorf("ECI %s must mount every declared volume at least once", owner)
	}
	if err := validateMountSet(owner, mounts, allowedNames...); err != nil {
		return err
	}
	mounted := make(map[string]struct{}, len(allowedNames))
	for _, mount := range mounts {
		mounted[mount.Name] = struct{}{}
	}
	for _, name := range requiredNames {
		if _, exists := mounted[name]; !exists {
			return fmt.Errorf("ECI %s must mount every declared volume at least once", owner)
		}
	}
	expectedReadOnly := make(map[string]bool, len(allowedNames))
	for _, name := range readOnlyNames {
		expectedReadOnly[name] = true
	}
	for _, mount := range mounts {
		if mount.ReadOnly != expectedReadOnly[mount.Name] {
			return fmt.Errorf("ECI %s volume mount %q has invalid read-only policy", owner, mount.Name)
		}
	}
	return nil
}
