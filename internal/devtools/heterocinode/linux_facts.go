package heterocinode

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

const linuxDockerReservationFormat = "{{index .Config.Labels \"super-dolphin-owner-epoch\"}} {{.HostConfig.NanoCpus}} {{.HostConfig.Memory}}"

// LinuxFactsProviderConfig 显式绑定 Docker、cgroup v2 与 boot identity 路径。
type LinuxFactsProviderConfig struct {
	DockerExecutable string
	CgroupRoot       string
	BootIDPath       string
	Timeout          time.Duration
	WaitDelay        time.Duration
}

// LinuxFactsProvider 从 Docker 与指定 cgroup v2 边界采集 Linux 节点事实。
type LinuxFactsProvider struct {
	runner     linuxDockerRunner
	cgroupRoot string
	bootIDPath string
}

type linuxDockerRunner interface {
	Run(context.Context, ...string) (string, error)
}

// NewLinuxFactsProvider 构造不发现默认 Docker/cgroup 路径的 Linux facts provider。
func NewLinuxFactsProvider(config LinuxFactsProviderConfig) (*LinuxFactsProvider, error) {
	if err := validateLinuxFactsProviderConfig(config); err != nil {
		return nil, err
	}
	return &LinuxFactsProvider{
		runner:     linuxExecDockerRunner{executable: config.DockerExecutable, timeout: config.Timeout, waitDelay: config.WaitDelay},
		cgroupRoot: config.CgroupRoot, bootIDPath: config.BootIDPath,
	}, nil
}

// Collect 验证 Linux/amd64 能力、当前 owner 容器和 external reservations 后生成 live facts。
func (provider *LinuxFactsProvider) Collect(ctx context.Context, config Config, request heteroci.ProbeRequest) (Facts, error) {
	if provider == nil || provider.runner == nil || config.Node.Platform != heteroci.PlatformLinuxAMD64 {
		return Facts{}, errors.New("linux facts provider dependencies are invalid")
	}
	bootID, total, available, err := provider.collectLinuxResourceFacts(ctx, request.OwnerEpoch)
	if err != nil {
		return Facts{}, err
	}
	capabilities, err := provider.verifyLinuxCapabilities(ctx, config.Node.Capabilities)
	if err != nil {
		return Facts{}, err
	}
	return Facts{BootID: bootID, Capabilities: capabilities, Total: total, Available: available}, nil
}

// collectLinuxResourceFacts 读取 boot、Docker、external reservations 与 cgroup 交集。
func (provider *LinuxFactsProvider) collectLinuxResourceFacts(ctx context.Context, ownerEpoch heteroci.RoutingOwnerEpoch) (string, heteroci.ResourceCapacity, heteroci.ResourceCapacity, error) {
	bootID, err := readLinuxFactFile(provider.bootIDPath, 256)
	if err != nil || bootID == "" {
		return "", heteroci.ResourceCapacity{}, heteroci.ResourceCapacity{}, errors.New("linux boot identity is unavailable")
	}
	dockerCPUs, dockerMemory, err := provider.readDockerCapacity(ctx)
	if err != nil {
		return "", heteroci.ResourceCapacity{}, heteroci.ResourceCapacity{}, err
	}
	reserved, err := provider.readExternalReservations(ctx, ownerEpoch)
	if err != nil {
		return "", heteroci.ResourceCapacity{}, heteroci.ResourceCapacity{}, err
	}
	cpuMax, err := readLinuxFactFile(filepath.Join(provider.cgroupRoot, "cpu.max"), 256)
	if err != nil {
		return "", heteroci.ResourceCapacity{}, heteroci.ResourceCapacity{}, errors.New("linux cgroup cpu.max is unavailable")
	}
	memoryMax, err := readLinuxFactFile(filepath.Join(provider.cgroupRoot, "memory.max"), 256)
	if err != nil {
		return "", heteroci.ResourceCapacity{}, heteroci.ResourceCapacity{}, errors.New("linux cgroup memory.max is unavailable")
	}
	total, available, err := heteroci.DeriveLinuxCapacity(heteroci.LinuxCapacityFacts{
		DockerLogicalCPUs: dockerCPUs, DockerMemoryBytes: dockerMemory,
		CgroupCPUMax: cpuMax, CgroupMemoryMax: memoryMax, Reserved: reserved,
	})
	if err != nil {
		return "", heteroci.ResourceCapacity{}, heteroci.ResourceCapacity{}, fmt.Errorf("derive linux capacity: %w", err)
	}
	return bootID, total, available, nil
}

// readDockerCapacity 读取 Docker 报告的逻辑 CPU 与总内存。
func (provider *LinuxFactsProvider) readDockerCapacity(ctx context.Context) (int64, int64, error) {
	output, err := provider.runner.Run(ctx, "info", "--format", "{{.NCPU}} {{.MemTotal}}")
	if err != nil {
		return 0, 0, errors.New("linux Docker capacity query failed")
	}
	fields := strings.Fields(output)
	if len(fields) != 2 {
		return 0, 0, errors.New("linux Docker capacity output is invalid")
	}
	cpus, cpuErr := strconv.ParseInt(fields[0], 10, 64)
	memory, memoryErr := strconv.ParseInt(fields[1], 10, 64)
	if cpuErr != nil || memoryErr != nil || cpus <= 0 || memory <= 0 {
		return 0, 0, errors.New("linux Docker capacity values are invalid")
	}
	return cpus, memory, nil
}

func (provider *LinuxFactsProvider) readExternalReservations(ctx context.Context, ownerEpoch heteroci.RoutingOwnerEpoch) (heteroci.ResourceCapacity, error) {
	output, err := provider.runner.Run(ctx, "ps", "--quiet", "--no-trunc")
	if err != nil {
		return heteroci.ResourceCapacity{}, errors.New("linux Docker running container query failed")
	}
	var reserved heteroci.ResourceCapacity
	for containerID := range strings.FieldsSeq(output) {
		reservation, currentOwner, err := provider.inspectContainerReservation(ctx, containerID, ownerEpoch)
		if err != nil {
			return heteroci.ResourceCapacity{}, err
		}
		if !currentOwner {
			reserved.CPUMilli += reservation.CPUMilli
			reserved.MemoryBytes += reservation.MemoryBytes
			reserved.MaxConcurrency++
		}
	}
	return reserved, nil
}

// inspectContainerReservation 区分当前 owner 容器与必须扣减的 external 容器资源。
func (provider *LinuxFactsProvider) inspectContainerReservation(ctx context.Context, containerID string, ownerEpoch heteroci.RoutingOwnerEpoch) (heteroci.ResourceCapacity, bool, error) {
	output, err := provider.runner.Run(ctx, "inspect", "--format", linuxDockerReservationFormat, containerID)
	if err != nil {
		return heteroci.ResourceCapacity{}, false, errors.New("linux Docker container reservation query failed")
	}
	fields := strings.Fields(output)
	if len(fields) != 3 {
		return heteroci.ResourceCapacity{}, false, errors.New("linux Docker container reservation output is invalid")
	}
	if fields[0] == strconv.FormatInt(int64(ownerEpoch), 10) {
		return heteroci.ResourceCapacity{}, true, nil
	}
	nanoCPUs, cpuErr := strconv.ParseInt(fields[1], 10, 64)
	memory, memoryErr := strconv.ParseInt(fields[2], 10, 64)
	if cpuErr != nil || memoryErr != nil || nanoCPUs <= 0 || nanoCPUs%1_000_000 != 0 || memory <= 0 {
		return heteroci.ResourceCapacity{}, false, errors.New("linux Docker external reservation is not bounded")
	}
	return heteroci.ResourceCapacity{CPUMilli: nanoCPUs / 1_000_000, MemoryBytes: memory, MaxConcurrency: 1}, false, nil
}

func (provider *LinuxFactsProvider) verifyLinuxCapabilities(ctx context.Context, configured []heteroci.CapabilityConfig) ([]heteroci.ActiveCapabilitySnapshot, error) {
	active := make([]heteroci.ActiveCapabilitySnapshot, 0, len(configured))
	for _, capability := range configured {
		if capability.Runtime != heteroci.RuntimeUbuntuContainer {
			return nil, errors.New("linux facts provider only supports qualified ubuntu_container")
		}
		imageID, err := provider.runner.Run(ctx, "image", "inspect", "--format", "{{.Id}}", capability.Image)
		if err != nil || imageID != capability.ImageDigest {
			return nil, errors.New("linux immutable runtime image is unavailable")
		}
		active = append(active, heteroci.ActiveCapabilitySnapshot{Runtime: capability.Runtime, ImageDigest: capability.ImageDigest})
	}
	return active, nil
}

// validateLinuxFactsProviderConfig 校验全部路径和进程等待边界均由调用方显式提供。
func validateLinuxFactsProviderConfig(config LinuxFactsProviderConfig) error {
	if err := validateLinuxExecutable(config.DockerExecutable); err != nil {
		return err
	}
	if err := validateLinuxFactPath(config.CgroupRoot, true); err != nil {
		return err
	}
	if err := validateLinuxFactPath(config.BootIDPath, false); err != nil {
		return err
	}
	if config.Timeout < time.Second || config.Timeout > 120*time.Second || config.WaitDelay <= 0 || config.WaitDelay > config.Timeout {
		return errors.New("linux facts provider timing is invalid")
	}
	return nil
}

func validateLinuxExecutable(path string) error {
	if err := validateLinuxFactPath(path, false); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&0o111 == 0 {
		return errors.New("linux Docker executable is invalid")
	}
	return nil
}

// validateLinuxFactPath 拒绝相对、非 clean、symlink 或类型漂移的 facts 路径。
func validateLinuxFactPath(path string, directory bool) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("linux facts path is invalid")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return errors.New("linux facts path contains a symlink")
	}
	info, err := os.Lstat(path)
	if err != nil || directory != info.IsDir() || !directory && !info.Mode().IsRegular() {
		return errors.New("linux facts path type is invalid")
	}
	return nil
}

// readLinuxFactFile 完整读取单行 bounded fact，并聚合 Close 失败。
func readLinuxFactFile(path string, maxBytes int64) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || int64(len(data)) > maxBytes {
		return "", errors.New("linux fact file read failed")
	}
	value := strings.TrimSuffix(string(data), "\n")
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return "", errors.New("linux fact file value is invalid")
	}
	return value, nil
}

type linuxExecDockerRunner struct {
	executable string
	timeout    time.Duration
	waitDelay  time.Duration
}

// Run 在 context timeout 与 WaitDelay 双重边界内调用唯一 Docker executable。
func (runner linuxExecDockerRunner) Run(ctx context.Context, args ...string) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, runner.timeout)
	defer cancel()
	command := exec.CommandContext(runCtx, runner.executable, args...)
	command.WaitDelay = runner.waitDelay
	var stdout bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = io.Discard
	if err := command.Run(); err != nil || runCtx.Err() != nil || stdout.Len() > 1<<20 {
		return "", errors.New("docker command failed")
	}
	return strings.TrimSpace(stdout.String()), nil
}
