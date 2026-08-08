// Package eci provides a narrow, credential-free adapter for the Aliyun ECI CLI.
package eci

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
	"golang.org/x/sync/errgroup"
)

const (
	SpotStrategyAsPriceGo = "SpotAsPriceGo"
	SpotStrategyNoSpot    = "NoSpot"
	// maxDescribeContainerGroupIDs 是 provider 单请求协议上限，不是分片并发上限。
	maxDescribeContainerGroupIDs = 20
	maxContainerLogBytes         = 1 << 20
	maxContainerLogTail          = 2000
	maxCLIAttempts               = 12
	cliAttemptTimeout            = 15 * time.Second
	cliProcessWaitDelay          = time.Second
	initialCLIRetryDelay         = 500 * time.Millisecond
	maxCLIRetryDelay             = 8 * time.Second
	clientTokenEntropyBytes      = 32
)

// Config describes the fixed infrastructure used by every ECI shard.
// Profile is the name of an already configured Aliyun CLI profile; it is never read or persisted here.
type Config struct {
	Binary            string
	RegionID          string
	VSwitches         []cicontract.ECIVSwitch
	SecurityGroupID   string
	WorkerRoleName    string
	Profile           string
	Deadline          time.Duration
	SpotStrategy      string
	SpotDurationHours int64
	// RegistryCredentialLoader 仅于首次真实创建容器组前调用一次。
	RegistryCredentialLoader func() (RegistryCredential, error)
}

// RegistryCredential 仅在进程内存中携带一份短期 private registry credential。
type RegistryCredential struct {
	Server   string `cli:"Server"`
	UserName string `cli:"UserName"`
	Password string `cli:"Password"`
}

// CreateRequest describes the caller-controlled identity of one ECI shard.
type CreateRequest struct {
	ContainerGroupName   string
	ContainerName        string
	ImageCacheSnapshotID string
	MainImage            string
	InitImage            string
	Resources            Resources
	Command              []string
	Args                 []string
	Environment          map[string]string
	Tags                 map[string]string
	InitContainer        InitContainer
	SourceVolume         EmptyDirVolume
	WorkVolume           EmptyDirVolume
	TempVolume           EmptyDirVolume
	ConfigFileVolumes    []ConfigFileVolume
	MainVolumeMounts     []VolumeMount
	InitVolumeMounts     []VolumeMount
}

// Resources is an exact CPU and memory tier requested for one ECI container group.
type Resources struct {
	CPU       float64
	MemoryGiB float64
}

// InitContainer materializes immutable shard input into the shared source and work volumes.
type InitContainer struct {
	Name        string
	Command     []string
	Args        []string
	Environment map[string]string
}

// EmptyDirVolume names one shard-local ECI EmptyDir volume.
type EmptyDirVolume struct {
	Name string
}

// VolumeMount binds a named shard volume to an absolute container path.
type VolumeMount struct {
	Name      string
	MountPath string
	ReadOnly  bool
}

// ContainerGroup is the subset of ECI state required by the caller.
type ContainerGroup struct {
	ID             string                `json:"ContainerGroupId"`
	Name           string                `json:"ContainerGroupName"`
	RegionID       string                `json:"RegionId"`
	CPU            float64               `json:"Cpu"`
	MemoryGiB      float64               `json:"Memory"`
	Status         string                `json:"Status"`
	CreationTime   time.Time             `json:"CreationTime"`
	SucceededTime  time.Time             `json:"SucceededTime"`
	FailedTime     time.Time             `json:"FailedTime"`
	Containers     []ContainerStatus     `json:"Containers,omitempty"`
	InitContainers []ContainerStatus     `json:"InitContainers,omitempty"`
	Tags           []ContainerGroupTag   `json:"Tags,omitempty"`
	Events         []ContainerGroupEvent `json:"Events,omitempty"`
}

// ContainerGroupTag 保留 ECI 返回的 immutable identity label。
type ContainerGroupTag struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

// ContainerStatus captures the terminal state reported for one ECI container.
type ContainerStatus struct {
	Name         string         `json:"Name"`
	Image        string         `json:"Image"`
	CurrentState ContainerState `json:"CurrentState"`
}

// ContainerState preserves the exit evidence needed to diagnose reportless workers.
type ContainerState struct {
	State      string    `json:"State"`
	StartTime  time.Time `json:"StartTime"`
	FinishTime time.Time `json:"FinishTime"`
	ExitCode   *int64    `json:"ExitCode,omitempty"`
	Reason     string    `json:"Reason,omitempty"`
	Message    string    `json:"Message,omitempty"`
}

// ContainerGroupEvent preserves bounded ECI event evidence for terminal failures.
type ContainerGroupEvent struct {
	Type          string `json:"Type,omitempty"`
	Reason        string `json:"Reason,omitempty"`
	Message       string `json:"Message,omitempty"`
	Count         int64  `json:"Count,omitempty"`
	LastTimestamp string `json:"LastTimestamp,omitempty"`
}

// ImageCache 保存容器组固定引用前必须确认的 ECI 缓存生命周期证据。
type ImageCache struct {
	ID         string                `json:"ImageCacheId"`
	RegionID   string                `json:"RegionId"`
	SnapshotID string                `json:"SnapshotId"`
	Name       string                `json:"ImageCacheName"`
	Status     string                `json:"Status"`
	Progress   string                `json:"Progress,omitempty"`
	Images     []string              `json:"Images,omitempty"`
	Events     []ContainerGroupEvent `json:"Events,omitempty"`
}

// CommandRunner executes one external command. It permits deterministic unit tests.
type CommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

// Client invokes the installed Aliyun CLI using a named profile.
type Client struct {
	config             Config
	runner             CommandRunner
	wait               func(context.Context, time.Duration) error
	attemptTimeout     time.Duration
	newClientToken     func() (string, error)
	credentialOnce     sync.Once
	credentialErr      error
	registryCredential RegistryCredential
}

// New 使用本机已安装的 aliyun CLI 创建客户端，凭据始终由指定 profile 管理。
func New(config Config) (*Client, error) {
	canonical, err := canonicalOfficialAliyunCLI(config.Binary)
	if err != nil {
		return nil, err
	}
	config.Binary = canonical
	return NewWithRunner(config, execRunner{})
}

// canonicalOfficialAliyunCLI resolves PATH once and then enforces the trusted
// executable ownership, permissions, and realpath boundary used by production.
func canonicalOfficialAliyunCLI(binary string) (string, error) {
	if binary == "" || binary != strings.TrimSpace(binary) {
		return "", errors.New("production ECI client requires the official aliyun CLI binary")
	}
	if !filepath.IsAbs(binary) {
		resolved, err := exec.LookPath(binary)
		if err != nil {
			return "", fmt.Errorf("production ECI client requires the official aliyun CLI binary: resolve %q: %w", binary, err)
		}
		binary = resolved
	}
	canonical, err := gateprivate.CanonicalCurrentOrRootExecutable("official aliyun CLI", binary)
	if err != nil {
		return "", fmt.Errorf("production ECI client requires the official aliyun CLI binary: %w", err)
	}
	if filepath.Base(canonical) != "aliyun" {
		return "", errors.New("production ECI client requires the official aliyun CLI binary")
	}
	return canonical, nil
}

// NewWithRunner 注入命令执行边界，供调用方隔离进程执行并让测试精确验证请求。
func NewWithRunner(config Config, runner CommandRunner) (*Client, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	if runner == nil {
		return nil, errors.New("ECI command runner is required")
	}
	return &Client{
		config:         config,
		runner:         runner,
		wait:           waitForRetry,
		attemptTimeout: cliAttemptTimeout,
		newClientToken: generateClientToken,
	}, nil
}

// CreateContainerGroup 创建一个分片容器组；CLI 或响应不完整时立即返回错误。
func (c *Client) CreateContainerGroup(ctx context.Context, request CreateRequest) (ContainerGroup, error) {
	if err := validateCreateRequest(request); err != nil {
		return ContainerGroup{}, err
	}
	if err := c.loadRegistryCredential(); err != nil {
		return ContainerGroup{}, fmt.Errorf("load ECI registry credential: %w", err)
	}
	if err := validateRegistryCredential(c.registryCredential, request); err != nil {
		return ContainerGroup{}, err
	}
	if err := validateConfigFileProjectionValues(request.ConfigFileVolumes,
		c.registryCredential.UserName,
		c.registryCredential.Password,
	); err != nil {
		return ContainerGroup{}, err
	}
	return c.createContainerGroup(ctx, request, c.config.SpotStrategy)
}

// loadRegistryCredential 在首次真实 ECI 创建边界解析延迟凭据且只执行一次；全命中复用不会到达此处。
func (c *Client) loadRegistryCredential() error {
	c.credentialOnce.Do(func() {
		if c.config.RegistryCredentialLoader == nil {
			c.credentialErr = errors.New("ECI registry credential loader is required for container creation")
			return
		}
		credential, err := c.config.RegistryCredentialLoader()
		if err != nil {
			c.credentialErr = err
			return
		}
		c.registryCredential = credential
	})
	return c.credentialErr
}

// createContainerGroup 使用单一计费策略执行一次可幂等重试并协调不确定响应。
func (c *Client) createContainerGroup(ctx context.Context, request CreateRequest, spotStrategy string) (ContainerGroup, error) {
	args := []string{
		"--VSwitchId", joinedVSwitchIDs(c.config.VSwitches),
		"--ScheduleStrategy", cicontract.ECIMultiZoneScheduleStrategy,
		"--SecurityGroupId", c.config.SecurityGroupID,
		"--RamRoleName", c.config.WorkerRoleName,
		"--Cpu", formatResource(request.Resources.CPU),
		"--Memory", formatResource(request.Resources.MemoryGiB),
		"--EphemeralStorage", strconv.Itoa(cicontract.ECIEphemeralStorageGiB),
		"--SpotStrategy", spotStrategy,
		"--RestartPolicy", "Never",
		"--ActiveDeadlineSeconds", strconv.FormatInt(int64(c.config.Deadline/time.Second), 10),
		"--ContainerGroupName", request.ContainerGroupName,
		"--ImageSnapshotId", request.ImageCacheSnapshotID,
		"--Container.1.Name", request.ContainerName,
		"--Container.1.Image", request.MainImage,
		"--Container.1.Cpu", formatResource(request.Resources.CPU),
		"--Container.1.Memory", formatResource(request.Resources.MemoryGiB),
		"--Container.1.ImagePullPolicy", "IfNotPresent",
		"--ImageRegistryCredential.1.Server", c.registryCredential.Server,
		"--ImageRegistryCredential.1.UserName", c.registryCredential.UserName,
		"--ImageRegistryCredential.1.Password", c.registryCredential.Password,
		"--Container.1.SecurityContext.ReadOnlyRootFilesystem", "true",
		"--Container.1.SecurityContext.RunAsUser", strconv.Itoa(cicontract.RemoteWorkerUID),
		"--Container.1.SecurityContextRunAsGroup", strconv.Itoa(cicontract.RemoteWorkerGID),
		"--InitContainer.1.Name", request.InitContainer.Name,
		"--InitContainer.1.Image", request.InitImage,
		"--InitContainer.1.ImagePullPolicy", "IfNotPresent",
		"--InitContainer.1.SecurityContext.ReadOnlyRootFilesystem", "true",
		"--InitContainer.1.SecurityContext.RunAsUser", "0",
	}
	if spotStrategy != SpotStrategyNoSpot {
		args = append(args[:12], append([]string{"--SpotDuration", strconv.FormatInt(c.config.SpotDurationHours, 10)}, args[12:]...)...)
	}
	volumeArgs := make([]string, 0)
	emptyDirs := createEmptyDirVolumes(request)
	volumeArgs = appendEmptyDirVolumes(volumeArgs, 1, emptyDirs)
	volumeArgs = appendConfigFileVolumes(volumeArgs, len(emptyDirs)+1, request.ConfigFileVolumes)
	initIndex := slices.Index(args, "--InitContainer.1.Name")
	args = append(args[:initIndex], append(volumeArgs, args[initIndex:]...)...)
	args = appendIndexedValues(args, "--Container.1.Command", request.Command)
	args = appendIndexedValues(args, "--Container.1.Arg", request.Args)
	args = appendIndexedMap(args, "--Container.1.EnvironmentVar", request.Environment)
	args = appendVolumeMounts(args, "--Container.1.VolumeMount", orderedVolumeMounts(
		request.MainVolumeMounts,
		createMainMountNames(request)...,
	))
	args = appendIndexedValues(args, "--InitContainer.1.Command", request.InitContainer.Command)
	args = appendIndexedValues(args, "--InitContainer.1.Arg", request.InitContainer.Args)
	args = appendIndexedMap(args, "--InitContainer.1.EnvironmentVar", request.InitContainer.Environment)
	args = appendVolumeMounts(args, "--InitContainer.1.VolumeMount", orderedVolumeMounts(
		request.InitVolumeMounts,
		createInitMountNames(request)...,
	))
	args = appendIndexedMap(args, "--Tag", request.Tags)
	output, err := c.run(ctx, "CreateContainerGroup", args...)
	if err != nil {
		registrySecrets := map[string]string{"registry_username": c.registryCredential.UserName, "registry_password": c.registryCredential.Password}
		createErr := fmt.Errorf("create ECI container group: %w", redactEnvironmentValues(
			err,
			request.Environment,
			request.InitContainer.Environment,
			registrySecrets,
			configFileProjectionRedactionValues(request.ConfigFileVolumes),
		))
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

func joinedVSwitchIDs(vSwitches []cicontract.ECIVSwitch) string {
	ids := make([]string, len(vSwitches))
	for index, vSwitch := range vSwitches {
		ids[index] = vSwitch.ID
	}
	return strings.Join(ids, ",")
}

// DescribeContainerGroups 查询指定容器组，缺少任何关键状态都不能被当作有效结果。
func (c *Client) DescribeContainerGroups(ctx context.Context, ids ...string) ([]ContainerGroup, error) {
	if err := validateContainerGroupIDs(ids); err != nil {
		return nil, fmt.Errorf("encode ECI container group IDs: %w", err)
	}
	return c.describeContainerGroupBatches(ctx, ids)
}

// describeContainerGroupBatches 并行 fanout provider 的 20-ID 请求批次，并按输入批次顺序合并结果。
// 每个批次仍受阿里云单请求上限约束；这里没有仓库级并发 cap，避免慢批次串行拖住整轮协调器轮询。
func (c *Client) describeContainerGroupBatches(ctx context.Context, ids []string) ([]ContainerGroup, error) {
	batchCount := (len(ids) + maxDescribeContainerGroupIDs - 1) / maxDescribeContainerGroupIDs
	batches := make([][]ContainerGroup, batchCount)
	failures := make([]error, batchCount)
	var workers errgroup.Group
	for batchIndex := range batches {
		start := batchIndex * maxDescribeContainerGroupIDs
		end := min(start+maxDescribeContainerGroupIDs, len(ids))
		workers.Go(func() error {
			batch, err := c.describeContainerGroupBatch(ctx, ids[start:end])
			if err != nil {
				failures[batchIndex] = err
				return err
			}
			batches[batchIndex] = batch
			return nil
		})
	}
	if err := workers.Wait(); err != nil {
		return nil, errors.Join(failures...)
	}
	groups := make([]ContainerGroup, 0, len(ids))
	for _, batch := range batches {
		groups = append(groups, batch...)
	}
	return groups, nil
}

// describeContainerGroupBatch 按阿里云单次最多二十个 ID 的限制查询一个批次。
func (c *Client) describeContainerGroupBatch(ctx context.Context, ids []string) ([]ContainerGroup, error) {
	encodedIDs, err := encodeContainerGroupIDs(ids)
	if err != nil {
		return nil, fmt.Errorf("encode ECI container group IDs: %w", err)
	}
	output, err := c.run(ctx, "DescribeContainerGroups", "--ContainerGroupIds", string(encodedIDs), "--WithEvent", "true")
	if err != nil {
		return nil, fmt.Errorf("describe ECI container groups: %w", err)
	}
	var response struct {
		ContainerGroups []ContainerGroup `json:"ContainerGroups"`
	}
	if err := decodeJSON(output, &response); err != nil {
		return nil, fmt.Errorf("decode DescribeContainerGroups response: %w", err)
	}
	if err := validateContainerGroups(response.ContainerGroups); err != nil {
		return nil, err
	}
	return response.ContainerGroups, nil
}

func encodeContainerGroupIDs(ids []string) ([]byte, error) {
	if err := validateContainerGroupIDs(ids); err != nil {
		return nil, err
	}
	return json.Marshal(ids)
}

func validateContainerGroupIDs(ids []string) error {
	if len(ids) == 0 {
		return errors.New("at least one ECI container group ID is required")
	}
	for index, id := range ids {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("ECI container group ID %d is required", index+1)
		}
	}
	return nil
}

func validateContainerGroups(groups []ContainerGroup) error {
	if len(groups) == 0 {
		return errors.New("DescribeContainerGroups response contains no container groups")
	}
	for _, group := range groups {
		if strings.TrimSpace(group.ID) == "" || strings.TrimSpace(group.Status) == "" {
			return errors.New("DescribeContainerGroups response contains an incomplete container group")
		}
	}
	return nil
}

// DescribeContainerLog 返回一个容器的日志内容，拒绝缺失 Content 的响应。
func (c *Client) DescribeContainerLog(ctx context.Context, containerGroupID string, containerName string) (string, error) {
	if strings.TrimSpace(containerGroupID) == "" || strings.TrimSpace(containerName) == "" {
		return "", errors.New("ECI container group ID and container name are required")
	}
	output, err := c.run(
		ctx,
		"DescribeContainerLog",
		"--ContainerGroupId", containerGroupID,
		"--ContainerName", containerName,
		"--Tail", strconv.Itoa(maxContainerLogTail),
		"--LimitBytes", strconv.Itoa(maxContainerLogBytes),
		"--Timestamps", "false",
	)
	if err != nil {
		return "", fmt.Errorf("describe ECI container log: %w", err)
	}
	var response struct {
		Content *string `json:"Content"`
	}
	if err := decodeJSON(output, &response); err != nil {
		return "", fmt.Errorf("decode DescribeContainerLog response: %w", err)
	}
	if response.Content == nil {
		return "", errors.New("DescribeContainerLog response is missing Content")
	}
	if len(*response.Content) > maxContainerLogBytes {
		return "", errors.New("DescribeContainerLog response exceeds requested byte limit")
	}
	return *response.Content, nil
}

// DeleteContainerGroup 删除一个容器组，并要求 CLI 确认返回 RequestId。
func (c *Client) DeleteContainerGroup(ctx context.Context, containerGroupID string) error {
	if strings.TrimSpace(containerGroupID) == "" {
		return errors.New("ECI container group ID is required")
	}
	output, err := c.run(ctx, "DeleteContainerGroup", "--ContainerGroupId", containerGroupID)
	if err != nil {
		return fmt.Errorf("delete ECI container group: %w", err)
	}
	var response struct {
		RequestID string `json:"RequestId"`
	}
	if err := decodeJSON(output, &response); err != nil {
		return fmt.Errorf("decode DeleteContainerGroup response: %w", err)
	}
	if strings.TrimSpace(response.RequestID) == "" {
		return errors.New("DeleteContainerGroup response is missing RequestId")
	}
	return nil
}

// run 执行一次有界、可取消且仅面向明确传输瞬态错误的 ECI CLI 重试序列。
func (c *Client) run(ctx context.Context, action string, args ...string) ([]byte, error) {
	commandArgs, err := c.commandArgs(action, args)
	if err != nil {
		return nil, err
	}
	for attempt := 1; attempt <= maxCLIAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("aliyun eci %s: %w", action, err)
		}
		attemptContext, cancel := gateprivate.WithTimeout(ctx, c.attemptTimeout)
		output, err := c.runner.Run(attemptContext, c.config.Binary, commandArgs...)
		cancel()
		if err == nil {
			return output, nil
		}
		safeErr := redactSensitiveQueryValues(err)
		if !isTransientCLIError(safeErr) || attempt == maxCLIAttempts {
			return nil, fmt.Errorf("aliyun eci %s: %w", action, safeErr)
		}
		if err := c.wait(ctx, cliRetryDelay(attempt)); err != nil {
			return nil, fmt.Errorf("aliyun eci %s retry wait: %w", action, err)
		}
	}
	return nil, fmt.Errorf("aliyun eci %s retry attempts exhausted", action)
}

// MaxControlPlaneRetryDuration 返回一次 ECI 控制面调用完成全部瞬态重试所需的严格上界。
func MaxControlPlaneRetryDuration() time.Duration {
	total := time.Duration(maxCLIAttempts) * cliAttemptTimeout
	for attempt := 1; attempt < maxCLIAttempts; attempt++ {
		total += cliRetryDelay(attempt)
	}
	return total
}

// commandArgs 保留调用方的稳定幂等 token；未提供时生成一次并复用于该次 run 的全部重试。
func (c *Client) commandArgs(action string, args []string) ([]string, error) {
	commandArgs := []string{"eci", action, "--RegionId", c.config.RegionID, "--profile", c.config.Profile}
	if isIdempotentCreateAction(action) && !containsCommandArgument(args, "--ClientToken") {
		clientToken, err := c.newClientToken()
		if err != nil {
			return nil, fmt.Errorf("generate ECI client token: %w", err)
		}
		if strings.TrimSpace(clientToken) == "" {
			return nil, errors.New("generated ECI client token is empty")
		}
		commandArgs = append(commandArgs, "--ClientToken", clientToken)
	}
	commandArgs = append(commandArgs, args...)
	return commandArgs, nil
}

func isIdempotentCreateAction(action string) bool {
	return action == "CreateContainerGroup"
}

func containsCommandArgument(args []string, name string) bool {
	return slices.Contains(args, name)
}

// generateClientToken 生成 ECI ClientToken 长度上限内的随机幂等 token。
func generateClientToken() (string, error) {
	var entropy [clientTokenEntropyBytes]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(entropy[:]), nil
}

// waitForRetry 等待退避窗口，并在调用 context 结束时立即返回。
func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// cliRetryDelay 为第 attempt 次失败计算有界指数退避。
func cliRetryDelay(attempt int) time.Duration {
	delay := initialCLIRetryDelay * time.Duration(1<<(attempt-1))
	if delay > maxCLIRetryDelay {
		return maxCLIRetryDelay
	}
	return delay
}

// isTransientCLIError 仅接受明确的网络、DNS 与传输层瞬态错误。
func isTransientCLIError(err error) bool {
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var dnsError *net.DNSError
	if errors.As(err, &dnsError) && (dnsError.IsTimeout || dnsError.IsTemporary) {
		return true
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return true
	}
	message := strings.ToLower(err.Error())
	for _, fragment := range transientCLIErrorFragments {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return false
}

func createEmptyDirVolumes(request CreateRequest) []EmptyDirVolume {
	return []EmptyDirVolume{request.SourceVolume, request.WorkVolume, request.TempVolume}
}

func createMainMountNames(request CreateRequest) []string {
	return []string{request.SourceVolume.Name, request.WorkVolume.Name, request.TempVolume.Name}
}

func createInitMountNames(request CreateRequest) []string {
	names := []string{request.SourceVolume.Name, request.WorkVolume.Name, request.TempVolume.Name}
	return append(names, createConfigFileVolumeNames(request)...)
}

func createRequiredInitMountNames(request CreateRequest) []string {
	names := []string{request.SourceVolume.Name, request.WorkVolume.Name}
	return append(names, createConfigFileVolumeNames(request)...)
}

func createConfigFileVolumeNames(request CreateRequest) []string {
	names := make([]string, len(request.ConfigFileVolumes))
	for index, volume := range request.ConfigFileVolumes {
		names[index] = volume.Name
	}
	return names
}

func appendEmptyDirVolumes(args []string, start int, volumes []EmptyDirVolume) []string {
	for index, volume := range volumes {
		prefix := fmt.Sprintf("--Volume.%d", start+index)
		args = append(args, prefix+".Name", volume.Name, prefix+".Type", "EmptyDirVolume")
	}
	return args
}

// validateMountSet 校验挂载名称、路径及各自唯一性。
func validateMountSet(owner string, mounts []VolumeMount, names ...string) error {
	allowed := make(map[string]struct{}, len(names))
	for _, name := range names {
		allowed[name] = struct{}{}
	}
	seenMounts, seenPaths := map[string]struct{}{}, map[string]struct{}{}
	for _, mount := range mounts {
		if _, ok := allowed[mount.Name]; !ok {
			return fmt.Errorf("ECI %s volume mount %q does not reference a declared volume", owner, mount.Name)
		}
		if _, exists := seenMounts[mount.Name]; exists {
			return fmt.Errorf("ECI %s volume mount %q is duplicated", owner, mount.Name)
		}
		if err := validateMountPath(mount.MountPath); err != nil {
			return fmt.Errorf("ECI %s volume mount %q: %w", owner, mount.Name, err)
		}
		for existing := range seenPaths {
			if mountPathsOverlap(existing, mount.MountPath) {
				return fmt.Errorf("ECI %s mount paths %q and %q overlap", owner, existing, mount.MountPath)
			}
		}
		seenMounts[mount.Name], seenPaths[mount.MountPath] = struct{}{}, struct{}{}
	}
	return nil
}

func mountPathsOverlap(left string, right string) bool {
	return left == right || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}

// validateMountPath 拒绝根路径、非规范绝对路径和危险控制字符。
func validateMountPath(mountPath string) error {
	if len(mountPath) > 1024 || mountPath == "/" || !path.IsAbs(mountPath) || path.Clean(mountPath) != mountPath || strings.ContainsAny(mountPath, ":\x00") {
		return errors.New("mount path must be a clean absolute path of at most 1024 characters without colon or NUL")
	}
	return nil
}

var (
	eciNamePattern                 = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,126}[a-z0-9])$`)
	environmentKeyPattern          = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	imageDigestPattern             = regexp.MustCompile(`^[a-z0-9][a-z0-9._/:-]*@sha256:[a-f0-9]{64}$`)
	sensitiveQueryParameterPattern = regexp.MustCompile(`(?i)((?:AccessKeyId|AccessKeySecret|Signature|SecurityToken)=)[^&#\s"'<>]+`)
	transientCLIErrorFragments     = []string{
		"tls handshake timeout",
		"i/o timeout",
		"unexpected eof",
		": eof",
		" eof",
		"context deadline exceeded",
		"client.timeout exceeded",
		"connection reset",
		"temporary failure in name resolution",
		"server misbehaving",
		"no such host",
		"connection timed out",
		"operation timed out",
		"transport is closing",
		"use of closed network connection",
		"throttling.user",
		"user flow control",
	}
)

func appendIndexedValues(args []string, prefix string, values []string) []string {
	for index, value := range values {
		key := fmt.Sprintf("%s.%d", prefix, index+1)
		if strings.HasPrefix(value, "-") {
			args = append(args, key+"="+value)
			continue
		}
		args = append(args, key, value)
	}
	return args
}

func appendIndexedMap(args []string, prefix string, values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for index, key := range keys {
		args = append(args, fmt.Sprintf("%s.%d.Key", prefix, index+1), key, fmt.Sprintf("%s.%d.Value", prefix, index+1), values[key])
	}
	return args
}

// orderedVolumeMounts 按固定卷名顺序生成 CLI 挂载参数。
func orderedVolumeMounts(mounts []VolumeMount, names ...string) []VolumeMount {
	ordered := make([]VolumeMount, 0, len(mounts))
	for _, name := range names {
		for _, mount := range mounts {
			if mount.Name == name {
				ordered = append(ordered, mount)
			}
		}
	}
	return ordered
}

func appendVolumeMounts(args []string, prefix string, mounts []VolumeMount) []string {
	for index, mount := range mounts {
		numberedPrefix := fmt.Sprintf("%s.%d", prefix, index+1)
		args = append(args,
			numberedPrefix+".Name", mount.Name,
			numberedPrefix+".MountPath", mount.MountPath,
			numberedPrefix+".ReadOnly", strconv.FormatBool(mount.ReadOnly),
		)
	}
	return args
}

// validateTag 校验 ECI 标签键值的长度、字符和前缀限制。
func validateTag(key string, value string) error {
	if !validTagKey(key) {
		return fmt.Errorf("ECI tag key %q is invalid", key)
	}
	if !validTagValue(value) {
		return fmt.Errorf("ECI tag value for %q is invalid", key)
	}
	return nil
}

// validTagKey 校验标签键不使用保留前缀或 URL 形式。
func validTagKey(key string) bool {
	lower := strings.ToLower(key)
	return key != "" && len(key) <= 64 && !strings.HasPrefix(lower, "aliyun") &&
		!strings.HasPrefix(lower, "acs:") && !strings.Contains(lower, "http://") && !strings.Contains(lower, "https://")
}

func validTagValue(value string) bool {
	lower := strings.ToLower(value)
	return len(value) <= 128 && !strings.HasPrefix(lower, "acs:") &&
		!strings.Contains(lower, "http://") && !strings.Contains(lower, "https://")
}

type redactedError struct {
	err             error
	values          []string
	queryParameters bool
}

// Error 返回已脱敏的底层错误字符串。
func (e redactedError) Error() string {
	message := e.err.Error()
	if e.queryParameters {
		message = sensitiveQueryParameterPattern.ReplaceAllString(message, "${1}<redacted>")
	}
	for _, value := range e.values {
		message = strings.ReplaceAll(message, value, "<redacted>")
	}
	return message
}

// Unwrap 暴露原始错误供 errors.Is 和 errors.As 判断。
func (e redactedError) Unwrap() error {
	return e.err
}

// redactSensitiveQueryValues 从 CLI 错误文本中移除凭据和签名查询值。
func redactSensitiveQueryValues(err error) error {
	return redactedError{err: err, queryParameters: true}
}

// redactEnvironmentValues 从错误文本中移除环境变量值。
func redactEnvironmentValues(err error, environments ...map[string]string) error {
	valueCount := 0
	for _, environment := range environments {
		valueCount += len(environment)
	}
	values := make([]string, 0, valueCount)
	for _, environment := range environments {
		for _, value := range environment {
			if value != "" {
				values = append(values, value)
			}
		}
	}
	sort.Slice(values, func(left, right int) bool { return len(values[left]) > len(values[right]) })
	return redactedError{err: err, values: values}
}

func decodeJSON(output []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(output))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("response contains more than one JSON value")
		}
		return fmt.Errorf("decode trailing response data: %w", err)
	}
	return nil
}

func formatResource(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

type execRunner struct{}

// Run 在受调用方 context 控制的有界子进程中执行 CLI，且不捕获或持久化 profile 凭据。
func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	// CLI 后代若在父进程退出后仍持有 stdout/stderr 管道，CommandContext 默认的
	// Wait 会越过 context 无界等待。固定 WaitDelay 使每次 ECI 控制面调用真正受到看门狗约束。
	command.WaitDelay = cliProcessWaitDelay
	output, err := command.Output()
	if err == nil {
		return output, nil
	}
	err = preserveCommandContextError(ctx, err)
	var exitError *exec.ExitError
	if errors.As(err, &exitError) && len(exitError.Stderr) > 0 {
		return output, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(exitError.Stderr)))
	}
	return output, err
}

// preserveCommandContextError 保留 CommandContext 杀进程时被退出状态遮蔽的超时或取消原因。
func preserveCommandContextError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return errors.Join(err, contextErr)
	}
	return err
}
