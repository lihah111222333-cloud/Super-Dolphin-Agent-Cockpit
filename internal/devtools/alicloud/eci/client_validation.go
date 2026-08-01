package eci

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// validateConfig 在启动 CLI 前阻断不完整基础设施配置和不可表示的资源值。
func validateConfig(config Config) error {
	fields := []struct {
		name  string
		value string
	}{
		{"CLI binary", config.Binary}, {"region ID", config.RegionID}, {"vSwitch ID", config.VSwitchID}, {"security group ID", config.SecurityGroupID},
		{"worker role name", config.WorkerRoleName}, {"profile", config.Profile}, {"image", config.Image},
	}
	for _, field := range fields {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("ECI %s is required", field.name)
		}
	}
	if !imageDigestPattern.MatchString(config.Image) {
		return errors.New("ECI image must be a repository@sha256:<64 lowercase hex> digest reference")
	}
	if config.SpotStrategy != SpotStrategyAsPriceGo {
		return errors.New("ECI spot strategy must equal SpotAsPriceGo")
	}
	if config.SpotDurationHours != 1 {
		return errors.New("ECI spot duration must equal one hour")
	}
	if !config.FallbackToPayAsYouGo {
		return errors.New("ECI pay-as-you-go fallback must be enabled")
	}
	if config.Deadline <= 0 || config.Deadline%time.Second != 0 {
		return errors.New("ECI deadline must be a positive whole number of seconds")
	}
	return nil
}

func validateResources(cpu float64, memoryGiB float64) error {
	allowed := map[float64]map[float64]bool{
		2: {2: true, 4: true, 8: true, 16: true},
		4: {4: true, 8: true, 16: true, 32: true},
		8: {8: true, 16: true, 32: true},
	}
	if cpu > 8 || memoryGiB > 32 || !allowed[cpu][memoryGiB] {
		return errors.New("ECI resources must use an exact spot tier within 8 vCPU and 32 GiB")
	}
	return nil
}

// validateCreateRequest 按容器、卷、挂载和标签阶段校验 ECI 分片请求。
func validateCreateRequest(request CreateRequest) error {
	for _, check := range []func(CreateRequest) error{
		validateContainerNames, validateRequestContainers, validateRequestVolumes,
		validateRequestMounts, validateRequestTags,
	} {
		if err := check(request); err != nil {
			return err
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

// validateRequestVolumes 校验 DataCache 卷身份、卷名唯一性和宿主挂载路径。
func validateRequestVolumes(request CreateRequest) error {
	hostPaths := createHostPathVolumes(request)
	if err := validateDataCacheHostPaths(request.DataCacheBucket, hostPaths); err != nil {
		return err
	}
	names := append(createHostPathVolumeNames(request), request.ExpandedVolume.Name, request.SourceVolume.Name, request.WorkVolume.Name, request.TempVolume.Name)
	return validateRequestVolumeNames(request.BootstrapVolume, names)
}

// validateDataCacheHostPaths 校验 DataCache 身份与每个宿主路径卷。
func validateDataCacheHostPaths(bucket string, hostPaths []HostPathVolume) error {
	if !dataCacheBucketPattern.MatchString(bucket) || bucket == "eci-system" {
		return errors.New("ECI DataCache volume identity is invalid")
	}
	if len(hostPaths) > maxDataCacheHostPaths {
		return fmt.Errorf("ECI supports at most %d DataCache HostPath volumes", maxDataCacheHostPaths)
	}
	for _, volume := range hostPaths {
		if volume.Type != "Directory" || validateMountPath(volume.Path) != nil {
			return errors.New("ECI DataCache volume identity is invalid")
		}
	}
	return nil
}

// validateRequestVolumeNames 校验卷名集合及可选 bootstrap 卷。
func validateRequestVolumeNames(bootstrap OSSVolume, names []string) error {
	if bootstrap != (OSSVolume{}) {
		if !validSeedOSSVolume(bootstrap) {
			return errors.New("ECI bootstrap OSS volume identity is invalid")
		}
		names = append(names, "current-gate")
	}
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if !eciNamePattern.MatchString(name) {
			return errors.New("ECI volume name is invalid")
		}
		seen[name] = struct{}{}
	}
	if len(seen) != len(names) {
		return errors.New("ECI volume names must be distinct")
	}
	return nil
}

func validateRequestMounts(request CreateRequest) error {
	hostNames := createHostPathVolumeNames(request)
	mainNames := createMainMountNames(request)
	mainReadOnly := append(append([]string{}, hostNames...), request.ExpandedVolume.Name, request.SourceVolume.Name)
	if err := validateVolumeMounts("task container", request.MainVolumeMounts, mainNames, mainNames, mainReadOnly); err != nil {
		return err
	}
	initReadOnly := append([]string{}, hostNames...)
	if request.BootstrapVolume != (OSSVolume{}) {
		initReadOnly = append(initReadOnly, "current-gate")
	}
	initAllowed := createInitMountNames(request)
	initRequired := createRequiredInitMountNames(request)
	return validateVolumeMounts("init container", request.InitVolumeMounts, initAllowed, initRequired, initReadOnly)
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
