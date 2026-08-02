package eci

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const (
	seedInputMountPath          = "/input"
	seedOutputMountPath         = "/output"
	seedBaselineLayersMountPath = "/layers"
	seedDirectCacheLayersPath   = "/direct-cache-layers"
	seedPreviousMountPath       = "/previous"
	seedScriptMountPath         = "/bootstrap"
	seedScratchMountPath        = "/tmp"
)

// OSSVolume identifies a same-region OSS prefix used by a baseline seed.
type OSSVolume struct {
	Bucket   string
	Endpoint string
	Path     string
	RoleName string
}

// DirectCacheLayer identifies one immutable Go build-cache delta mounted newest-first.
// It deliberately carries the full accepted identity so the seed request's idempotence
// boundary cannot collapse distinct cache layers onto the same mount.
type DirectCacheLayer struct {
	DataCacheID        string
	DataCacheBucket    string
	DataCachePath      string
	SizeGiB            int
	Generation         uint64
	SourceObjectPrefix string
	ManifestDigest     string
	TreeSHA256         string
	ParentChainSHA256  string
	RuntimeGoSHA256    string
	RuntimeDepsSHA256  string
}

// SeedRequest describes one on-demand ECI baseline materialization container.
type SeedRequest struct {
	ContainerGroupName    string
	ContainerName         string
	ClientToken           string
	Resources             Resources
	Command               []string
	Args                  []string
	AutoCreateEIP         bool
	EIPBandwidth          int
	Environment           map[string]string
	Tags                  map[string]string
	Input                 OSSVolume
	Output                OSSVolume
	BaselineLayers        OSSVolume
	Script                []byte
	DataCacheBucket       string
	PreviousDataCachePath string
	DirectCacheLayers     []DirectCacheLayer
}

// CreateSeedContainerGroup starts one short-lived baseline builder backed by OSS input and output volumes.
// CreateSeedContainerGroup 创建基线种子容器组并保持其 OSS 与 DataCache 身份绑定。
func (c *Client) CreateSeedContainerGroup(ctx context.Context, request SeedRequest) (ContainerGroup, error) {
	return c.createWithSpotFallback(ctx, "ECI baseline seed", func(strategy string) (ContainerGroup, error) {
		return c.createSeedContainerGroup(ctx, request, strategy)
	})
}

func (c *Client) createSeedContainerGroup(ctx context.Context, request SeedRequest, spotStrategy string) (ContainerGroup, error) {
	args, err := c.seedCreateArgs(request, spotStrategy)
	if err != nil {
		return ContainerGroup{}, err
	}
	output, err := c.run(ctx, "CreateContainerGroup", args...)
	if err != nil {
		createErr := fmt.Errorf(
			"create ECI baseline seed: %w",
			redactEnvironmentValues(err, request.Environment),
		)
		if !isTransientCLIError(createErr) {
			return ContainerGroup{}, createErr
		}
		return c.reconcileCreatedContainerGroup(ctx, request.ContainerGroupName, request.Tags, createErr)
	}
	group, err := decodeCreatedContainerGroup(output, request.ContainerGroupName)
	if err != nil {
		return c.reconcileCreatedContainerGroup(ctx, request.ContainerGroupName, request.Tags, err)
	}
	return group, nil
}

// seedCreateArgs 在执行前完整编码并绑定种子容器的 OSS、DataCache 和资源输入。
func (c *Client) seedCreateArgs(request SeedRequest, spotStrategy string) ([]string, error) {
	if err := validateSeedRequest(request); err != nil {
		return nil, err
	}
	input, err := seedOSSVolumeOptions(request.Input)
	if err != nil {
		return nil, fmt.Errorf("encode ECI seed input OSS volume: %w", err)
	}
	output, err := seedOSSVolumeOptions(request.Output)
	if err != nil {
		return nil, fmt.Errorf("encode ECI seed output OSS volume: %w", err)
	}
	args := c.seedBaseArgs(request, input, output, spotStrategy)
	nextVolumeIndex := 5
	if request.BaselineLayers != (OSSVolume{}) {
		baselineLayers, err := seedOSSVolumeOptions(request.BaselineLayers)
		if err != nil {
			return nil, fmt.Errorf("encode ECI seed baseline layers OSS volume: %w", err)
		}
		args = appendSeedOSSFlexVolume(args, nextVolumeIndex, "baseline-layers", baselineLayers)
		nextVolumeIndex++
	}
	if request.PreviousDataCachePath != "" || len(request.DirectCacheLayers) != 0 {
		args = append(args, "--DataCacheBucket", request.DataCacheBucket)
	}
	if request.PreviousDataCachePath != "" {
		args = appendHostPathVolumes(args, nextVolumeIndex, []HostPathVolume{{Name: "previous-data", Path: request.PreviousDataCachePath, Type: "Directory"}})
		nextVolumeIndex++
	}
	for index, layer := range request.DirectCacheLayers {
		args = appendHostPathVolumes(args, nextVolumeIndex, []HostPathVolume{{Name: directCacheLayerVolumeName(index), Path: layer.DataCachePath, Type: "Directory"}})
		nextVolumeIndex++
	}
	args = appendIndexedValues(args, "--Container.1.Command", request.Command)
	args = appendIndexedValues(args, "--Container.1.Arg", request.Args)
	args = appendIndexedMap(args, "--Container.1.EnvironmentVar", request.Environment)
	args = appendVolumeMounts(args, "--Container.1.VolumeMount", seedVolumeMounts(request))
	return appendIndexedMap(args, "--Tag", request.Tags), nil
}

// seedBaseArgs 生成 seed ECI 的固定资源、网络、安全和 OSS 卷参数。
func (c *Client) seedBaseArgs(request SeedRequest, input []byte, output []byte, spotStrategy string) []string {
	args := []string{
		"--ClientToken", seedClientToken(request.ClientToken, spotStrategy),
		"--VSwitchId", c.config.VSwitchID,
		"--SecurityGroupId", c.config.SecurityGroupID,
		"--RamRoleName", c.config.WorkerRoleName,
		"--Cpu", formatResource(request.Resources.CPU),
		"--Memory", formatResource(request.Resources.MemoryGiB),
		"--SpotStrategy", spotStrategy,
	}
	if spotStrategy != SpotStrategyNoSpot {
		args = append(args, "--SpotDuration", strconv.FormatInt(c.config.SpotDurationHours, 10))
	}
	return append(args,
		"--RestartPolicy", "Never",
		"--ActiveDeadlineSeconds", strconv.FormatInt(int64(c.config.Deadline.Seconds()), 10),
		"--AutoMatchImageCache", "true",
		"--AutoCreateEip", strconv.FormatBool(request.AutoCreateEIP),
		"--EipBandwidth", strconv.Itoa(request.EIPBandwidth),
		"--ContainerGroupName", request.ContainerGroupName,
		"--Container.1.Name", request.ContainerName,
		"--Container.1.Image", c.config.Image,
		"--Container.1.Cpu", formatResource(request.Resources.CPU),
		"--Container.1.Memory", formatResource(request.Resources.MemoryGiB),
		"--Container.1.ImagePullPolicy", "IfNotPresent",
		"--Container.1.SecurityContext.ReadOnlyRootFilesystem", "false",
		"--Container.1.SecurityContext.RunAsUser", "0",
		"--Container.1.SecurityContextRunAsGroup", "0",
		"--Volume.1.Name", "input-data",
		"--Volume.1.Type", "FlexVolume",
		"--Volume.1.FlexVolume.Driver", "alicloud/oss",
		"--Volume.1.FlexVolume.Options", string(input),
		"--Volume.2.Name", "output-data",
		"--Volume.2.Type", "FlexVolume",
		"--Volume.2.FlexVolume.Driver", "alicloud/oss",
		"--Volume.2.FlexVolume.Options", string(output),
		"--Volume.3.Name", "script-data",
		"--Volume.3.Type", "ConfigFileVolume",
		"--Volume.3.ConfigFileVolume.DefaultMode", "365",
		"--Volume.3.ConfigFileVolume.ConfigFileToPath.1.Path", "seed.sh",
		"--Volume.3.ConfigFileVolume.ConfigFileToPath.1.Content", base64.StdEncoding.EncodeToString(request.Script),
		"--Volume.3.ConfigFileVolume.ConfigFileToPath.1.Mode", "365",
		"--Volume.4.Name", "scratch-data",
		"--Volume.4.Type", "EmptyDirVolume",
	)
}

func seedClientToken(base, spotStrategy string) string {
	if spotStrategy == SpotStrategyNoSpot {
		return base + "-regular"
	}
	return base + "-spot"
}

func seedVolumeMounts(request SeedRequest) []VolumeMount {
	mounts := []VolumeMount{{Name: "input-data", MountPath: seedInputMountPath, ReadOnly: true}, {Name: "output-data", MountPath: seedOutputMountPath}, {Name: "script-data", MountPath: seedScriptMountPath, ReadOnly: true}, {Name: "scratch-data", MountPath: seedScratchMountPath}}
	if request.BaselineLayers != (OSSVolume{}) {
		mounts = append(mounts, VolumeMount{Name: "baseline-layers", MountPath: seedBaselineLayersMountPath, ReadOnly: true})
	}
	if request.PreviousDataCachePath != "" {
		mounts = append(mounts, VolumeMount{Name: "previous-data", MountPath: seedPreviousMountPath, ReadOnly: true})
	}
	for index := range request.DirectCacheLayers {
		mounts = append(mounts, VolumeMount{Name: directCacheLayerVolumeName(index), MountPath: directCacheLayerMountPath(index), ReadOnly: true})
	}
	return mounts
}

func directCacheLayerVolumeName(index int) string {
	return "direct-cache-layer-" + strconv.Itoa(index)
}

func directCacheLayerMountPath(index int) string {
	return seedDirectCacheLayersPath + "/layer-" + strconv.Itoa(index)
}

// validateSeedRequest 校验种子容器、输入对象及输出位置的完整约束。
func validateSeedRequest(request SeedRequest) error {
	for _, check := range []func(SeedRequest) error{validateSeedIdentity, validateSeedResources, validateSeedScriptAndEIP, validateSeedOSS, validateSeedDataCache, validateSeedTags} {
		if err := check(request); err != nil {
			return err
		}
	}
	return nil
}

func validateSeedResources(request SeedRequest) error {
	return validateResources(request.Resources.CPU, request.Resources.MemoryGiB)
}

// validateSeedIdentity 校验种子实例名称、稳定幂等 token 与容器入口。
func validateSeedIdentity(request SeedRequest) error {
	if !eciNamePattern.MatchString(request.ContainerGroupName) || !eciNamePattern.MatchString(request.ContainerName) {
		return errors.New("ECI seed container group and container names are invalid")
	}
	if request.ClientToken == "" || len(request.ClientToken) > 56 ||
		strings.TrimSpace(request.ClientToken) != request.ClientToken ||
		strings.ContainsAny(request.ClientToken, "\x00\r\n") {
		return errors.New("ECI seed client token is invalid")
	}
	return validateContainerInput("seed container", request.Command, request.Args, request.Environment)
}

// validateSeedScriptAndEIP 校验脚本字节边界和 EIP 带宽合同。
func validateSeedScriptAndEIP(request SeedRequest) error {
	if len(request.Script) == 0 || len(request.Script) > 32<<10 || strings.ContainsRune(string(request.Script), '\x00') {
		return errors.New("ECI seed script must contain 1-32768 bytes without NUL")
	}
	if request.AutoCreateEIP && (request.EIPBandwidth < 1 || request.EIPBandwidth > 100) {
		return errors.New("ECI seed requires an automatic EIP bandwidth within 1..100 Mbit/s")
	}
	if !request.AutoCreateEIP && request.EIPBandwidth != 0 {
		return errors.New("ECI seed requires EIP bandwidth 0 when automatic EIP is disabled")
	}
	return nil
}

// validateSeedOSS 校验输入、输出和可选基线层 OSS 的共享身份与路径隔离。
func validateSeedOSS(request SeedRequest) error {
	if !validSeedOSSVolume(request.Input) || !validSeedOSSVolume(request.Output) {
		return errors.New("ECI seed OSS input or output is invalid")
	}
	if request.BaselineLayers == (OSSVolume{}) {
		return nil
	}
	if !validSeedOSSVolume(request.BaselineLayers) {
		return errors.New("ECI seed baseline layers OSS volume must be fully configured")
	}
	if !seedVolumesShareIdentity(request.BaselineLayers, request.Input, request.Output) {
		return errors.New("ECI seed baseline layers OSS volume must share input and output identity")
	}
	if request.BaselineLayers.Path == request.Input.Path || request.BaselineLayers.Path == request.Output.Path {
		return errors.New("ECI seed baseline layers OSS path must differ from input and output")
	}
	return nil
}

// seedVolumesShareIdentity 判断基线层与输入输出是否共用 OSS 身份。
func seedVolumesShareIdentity(baseline, input, output OSSVolume) bool {
	return baseline.Bucket == input.Bucket && baseline.Bucket == output.Bucket &&
		baseline.Endpoint == input.Endpoint && baseline.Endpoint == output.Endpoint &&
		baseline.RoleName == input.RoleName && baseline.RoleName == output.RoleName
}

// validateSeedDataCache 校验可选 Anchor 与 direct DataCache 的完整、有限身份。
func validateSeedDataCache(request SeedRequest) error {
	hasPrevious := request.PreviousDataCachePath != ""
	hasDirectLayers := len(request.DirectCacheLayers) != 0
	if err := validateSeedDataCacheMounts(request, hasPrevious, hasDirectLayers); err != nil {
		return err
	}
	return validateSeedDirectCacheLayers(request)
}

// validateSeedDataCacheMounts 校验 Anchor 或 direct 层共用的 DataCache 挂载前提。
func validateSeedDataCacheMounts(request SeedRequest, hasPrevious, hasDirectLayers bool) error {
	if (hasPrevious || hasDirectLayers) != (request.DataCacheBucket != "") {
		return errors.New("ECI seed DataCache bucket and mounted paths must be provided together")
	}
	if (hasPrevious || hasDirectLayers) && (!dataCacheBucketPattern.MatchString(request.DataCacheBucket) || request.DataCacheBucket == "eci-system") {
		return errors.New("ECI seed Anchor DataCache identity is invalid")
	}
	if hasPrevious && validateMountPath(request.PreviousDataCachePath) != nil {
		return errors.New("ECI seed Anchor DataCache identity is invalid")
	}
	return nil
}

// validateSeedDirectCacheLayers 校验有限层的身份、去重和 newest-first 顺序。
func validateSeedDirectCacheLayers(request SeedRequest) error {
	if len(request.DirectCacheLayers) > 3 {
		return errors.New("ECI seed supports at most three direct cache layers")
	}
	seenIDs := make(map[string]struct{}, len(request.DirectCacheLayers))
	seenPaths := make(map[string]struct{}, len(request.DirectCacheLayers))
	var newerGeneration uint64
	for index, layer := range request.DirectCacheLayers {
		if err := validateSeedDirectCacheLayerIdentity(layer, request.DataCacheBucket); err != nil {
			return err
		}
		if _, exists := seenIDs[layer.DataCacheID]; exists {
			return errors.New("ECI seed direct cache layer IDs must be distinct")
		}
		if _, exists := seenPaths[layer.DataCachePath]; exists {
			return errors.New("ECI seed direct cache layer paths must be distinct")
		}
		if index != 0 && layer.Generation >= newerGeneration {
			return errors.New("ECI seed direct cache layers must be newest-first")
		}
		seenIDs[layer.DataCacheID] = struct{}{}
		seenPaths[layer.DataCachePath] = struct{}{}
		newerGeneration = layer.Generation
	}
	return nil
}

// validateSeedDirectCacheLayerIdentity 校验单层不可变身份，保留统一的 fail-fast 错误。
func validateSeedDirectCacheLayerIdentity(layer DirectCacheLayer, bucket string) error {
	if slices.Contains([]string{
		layer.DataCacheID,
		layer.SourceObjectPrefix,
		layer.ManifestDigest,
		layer.TreeSHA256,
		layer.ParentChainSHA256,
		layer.RuntimeGoSHA256,
		layer.RuntimeDepsSHA256,
	}, "") {
		return errors.New("ECI seed direct cache layer identity is invalid")
	}
	if layer.DataCacheBucket != bucket || validateMountPath(layer.DataCachePath) != nil || layer.SizeGiB <= 0 || layer.Generation == 0 {
		return errors.New("ECI seed direct cache layer identity is invalid")
	}
	return nil
}

func validateSeedTags(request SeedRequest) error {
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

func seedOSSVolumeOptions(volume OSSVolume) ([]byte, error) {
	return json.Marshal(map[string]string{
		"bucket":    volume.Bucket,
		"url":       volume.Endpoint,
		"path":      volume.Path,
		"ramRole":   volume.RoleName,
		"otherOpts": "-o max_stat_cache_size=0 -o allow_other",
	})
}

func appendSeedOSSFlexVolume(args []string, index int, name string, options []byte) []string {
	prefix := fmt.Sprintf("--Volume.%d", index)
	return append(args,
		prefix+".Name", name,
		prefix+".Type", "FlexVolume",
		prefix+".FlexVolume.Driver", "alicloud/oss",
		prefix+".FlexVolume.Options", string(options),
	)
}

func validSeedOSSVolume(volume OSSVolume) bool {
	return ossBucketPattern.MatchString(volume.Bucket) &&
		internalOSSEndpointPattern.MatchString(volume.Endpoint) &&
		validSeedOSSPath(volume.Path) &&
		strings.TrimSpace(volume.RoleName) != ""
}

func validSeedOSSPath(value string) bool {
	return value != "/" && path.IsAbs(value) && path.Clean(value) == value &&
		len(value) <= 1024 && !strings.ContainsAny(value, ":\x00")
}

var (
	ossBucketPattern           = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)
	internalOSSEndpointPattern = regexp.MustCompile(`^oss-[a-z0-9-]+-internal\.aliyuncs\.com$`)
)
