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

const (
	linuxWineExpectedVersion        = "wine-9.0"
	linuxWineExpectedPackageVersion = "9.0~repack-4build3"
	linuxWineQualificationScript    = "set -eu; baked_prefix=\"${WINEPREFIX:?}\"; mkdir -p /tmp/wine-prefix; cp -a -- \"$baked_prefix\"/. /tmp/wine-prefix/; export WINEARCH=win64; export WINEPREFIX=/tmp/wine-prefix; export WINELOADER=/usr/lib/wine/wine64; export WINESERVER=/usr/lib/wine/wineserver64; test \"$(/usr/bin/dpkg-query -W -f='${Version}' wine64)\" = \"" + linuxWineExpectedPackageVersion + "\"; test -x /opt/super-dolphin/smoke/wine-smoke.exe; /usr/bin/xvfb-run --auto-servernum --server-args='-screen 0 1024x768x24 -nolisten tcp' /usr/lib/wine/wine64 /opt/super-dolphin/smoke/wine-smoke.exe 2>/dev/null; /usr/lib/wine/wineserver64 -k 2>/dev/null; /usr/lib/wine/wineserver64 -w 2>/dev/null; printf '%s\\n' '" + linuxWineExpectedVersion + "|smoke-ok'"
)

// LinuxFactsProviderConfig 显式绑定 Docker、cgroup v2 与 boot identity 路径。
type LinuxFactsProviderConfig struct {
	DockerExecutable  string
	DockerHost        string
	DockerServerID    string
	OSReleasePath     string
	UnameExecutable   string
	MachineIDPath     string
	MachineIDSHA256   string
	OSReleaseIdentity string
	UnameIdentity     string
	CgroupRoot        string
	BootIDPath        string
	Timeout           time.Duration
	WaitDelay         time.Duration
}

// LinuxFactsProvider 从 Docker 与指定 cgroup v2 边界采集 Linux 节点事实。
type LinuxFactsProvider struct {
	runner         linuxDockerRunner
	attestor       linuxPhysicalAttestor
	dockerHost     string
	dockerServerID string
	cgroupRoot     string
	bootIDPath     string
}

type linuxDockerRunner interface {
	Run(context.Context, ...string) (string, error)
}

// NewLinuxFactsProvider 构造不发现默认 Docker/cgroup 路径的 Linux facts provider。
func NewLinuxFactsProvider(config LinuxFactsProviderConfig) (*LinuxFactsProvider, error) {
	if err := validateLinuxFactsProviderConfig(config); err != nil {
		return nil, err
	}
	runner := linuxExecDockerRunner{executable: config.DockerExecutable, dockerHost: config.DockerHost, timeout: config.Timeout, waitDelay: config.WaitDelay}
	return &LinuxFactsProvider{
		runner:         runner,
		attestor:       linuxExecPhysicalAttestor{dockerRunner: runner, machineIDPath: config.MachineIDPath, machineIDSHA256: config.MachineIDSHA256, osReleasePath: config.OSReleasePath, osReleaseIdentity: config.OSReleaseIdentity, unameExecutable: config.UnameExecutable, unameIdentity: config.UnameIdentity},
		dockerHost:     config.DockerHost,
		dockerServerID: config.DockerServerID,
		cgroupRoot:     config.CgroupRoot,
		bootIDPath:     config.BootIDPath,
	}, nil
}

// Collect 验证 Linux/amd64 能力、当前 owner 容器和 external reservations 后生成 live facts。
func (provider *LinuxFactsProvider) Collect(ctx context.Context, config Config, request heteroci.ProbeRequest) (Facts, error) {
	if provider == nil || provider.runner == nil || provider.attestor == nil || config.Node.Platform != heteroci.PlatformLinuxAMD64 {
		return Facts{}, errors.New("linux facts provider dependencies are invalid")
	}
	if err := provider.verifyDockerServer(ctx); err != nil {
		return Facts{}, err
	}
	physicalAttestation, err := provider.attestor.Attest(ctx)
	if err != nil {
		return Facts{}, err
	}
	bootID, total, available, err := provider.collectLinuxResourceFacts(ctx, request.OwnerEpoch)
	if err != nil {
		return Facts{}, err
	}
	capabilities, err := provider.verifyLinuxCapabilities(ctx, config.Node.Capabilities)
	if err != nil {
		return Facts{}, err
	}
	return Facts{BootID: bootID, PhysicalAttestation: physicalAttestation, Capabilities: capabilities, Total: total, Available: available}, nil
}

func (provider *LinuxFactsProvider) verifyDockerServer(ctx context.Context) error {
	output, err := provider.runner.Run(ctx, "info", "--format", "{{.ID}}")
	if err != nil || output != provider.dockerServerID {
		return errors.New("linux Docker server identity is unavailable or drifted")
	}
	return nil
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
	if len(configured) != 2 || configured[0].Runtime != heteroci.RuntimeUbuntuContainer || configured[1].Runtime != heteroci.RuntimeUbuntuWineContainer {
		return nil, errors.New("linux facts provider requires sorted ubuntu and Ubuntu Wine capabilities")
	}
	active := make([]heteroci.ActiveCapabilitySnapshot, 0, len(configured))
	for _, capability := range configured {
		imageID, err := provider.runner.Run(ctx, "image", "inspect", "--format", "{{.Id}}", capability.Image)
		if err != nil || imageID != capability.ImageDigest {
			return nil, errors.New("linux immutable runtime image is unavailable")
		}
		if capability.Runtime == heteroci.RuntimeUbuntuWineContainer {
			if err := provider.verifyWineRuntime(ctx, imageID); err != nil {
				return nil, err
			}
		}
		active = append(active, heteroci.ActiveCapabilitySnapshot{Runtime: capability.Runtime, ImageDigest: capability.ImageDigest})
	}
	return active, nil
}

// verifyWineRuntime executes the baked Wine smoke from the exact inspected image without privilege or network.
func (provider *LinuxFactsProvider) verifyWineRuntime(ctx context.Context, imageID string) error {
	if imageID == "" {
		return errors.New("linux Wine image identity is missing")
	}
	output, err := provider.runner.Run(ctx,
		"run", "--rm", "--network", "none", "--user", "65532:65532", "--cap-drop", "ALL",
		"--security-opt", "no-new-privileges", "--read-only", "--tmpfs", "/tmp:rw,noexec,nosuid,nodev",
		"--entrypoint", "/bin/sh", imageID, "-c", linuxWineQualificationScript,
	)
	if err != nil || output != linuxWineExpectedVersion+"|smoke-ok" {
		return errors.New("linux Wine runtime qualification failed")
	}
	return nil
}

// validateLinuxFactsProviderConfig 校验全部路径和进程等待边界均由调用方显式提供。
func validateLinuxFactsProviderConfig(config LinuxFactsProviderConfig) error {
	if err := validateLinuxExecutable(config.DockerExecutable); err != nil {
		return err
	}
	if err := validateLinuxDockerSocket(config.DockerHost); err != nil {
		return err
	}
	if err := ValidateDockerServerID(config.DockerServerID); err != nil {
		return err
	}
	if err := validateLinuxFactPath(config.OSReleasePath, false); err != nil {
		return err
	}
	if err := validateLinuxExecutable(config.UnameExecutable); err != nil {
		return err
	}
	if err := validateLinuxFactPath(config.MachineIDPath, false); err != nil {
		return err
	}
	for name, value := range map[string]string{"machine_id_sha256": config.MachineIDSHA256, "os_release_identity": config.OSReleaseIdentity, "uname_identity": config.UnameIdentity} {
		if err := ValidateSHA256Identity(value); err != nil {
			return fmt.Errorf("%s is invalid: %w", name, err)
		}
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

// ValidateDockerHost 校验 caller-supplied Unix Docker endpoint 的严格 wire 形状。
func ValidateDockerHost(value string) error {
	socketPath, ok := strings.CutPrefix(value, "unix://")
	if !ok || strings.TrimSpace(value) != value || socketPath == "" || strings.Contains(socketPath, "\\") || strings.IndexFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return errors.New("linux Docker host must use unix:// with a clean absolute socket path")
	}
	if !filepath.IsAbs(socketPath) || socketPath == "/" || filepath.Clean(socketPath) != socketPath || strings.Contains(socketPath, "//") || strings.HasSuffix(socketPath, "/") {
		return errors.New("linux Docker host socket path is not absolute and clean")
	}
	for component := range strings.SplitSeq(strings.TrimPrefix(socketPath, "/"), "/") {
		if component == "" || component == "." || component == ".." {
			return errors.New("linux Docker host socket path is not absolute and clean")
		}
	}
	return nil
}

func validateLinuxDockerSocket(value string) error {
	if err := ValidateDockerHost(value); err != nil {
		return err
	}
	socketPath := strings.TrimPrefix(value, "unix://")
	info, err := os.Lstat(socketPath)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeSocket == 0 {
		return errors.New("linux Docker host must point to a non-symlink Unix socket")
	}
	resolved, err := filepath.EvalSymlinks(socketPath)
	if err != nil || resolved != socketPath {
		return errors.New("linux Docker host socket path contains a symlink")
	}
	return nil
}

// ValidateDockerServerID 校验 caller-supplied Docker daemon identity。
func ValidateDockerServerID(value string) error {
	if value == "" || len(value) > 128 || !isDockerServerIDAlphaNumeric(value[0]) {
		return errors.New("linux Docker server ID must be a safe opaque ID")
	}
	for index := 1; index < len(value); index++ {
		character := value[index]
		if !isDockerServerIDAlphaNumeric(character) && !strings.ContainsRune("._:-", rune(character)) {
			return errors.New("linux Docker server ID must be a safe opaque ID")
		}
	}
	return nil
}

func isDockerServerIDAlphaNumeric(character byte) bool {
	return character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9'
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
	dockerHost string
	timeout    time.Duration
	waitDelay  time.Duration
}

// Run 在 context timeout 与 WaitDelay 双重边界内调用唯一 Docker executable。
func (runner linuxExecDockerRunner) Run(ctx context.Context, args ...string) (string, error) {
	if err := validateLinuxDockerSocket(runner.dockerHost); err != nil {
		return "", err
	}
	runCtx, cancel := context.WithTimeout(ctx, runner.timeout)
	defer cancel()
	command := exec.CommandContext(runCtx, runner.executable, args...)
	command.Env = dockerCommandEnvironment(runner.dockerHost)
	command.WaitDelay = runner.waitDelay
	var stdout bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = io.Discard
	if err := command.Run(); err != nil || runCtx.Err() != nil || stdout.Len() > 1<<20 {
		return "", errors.New("docker command failed")
	}
	return strings.TrimSpace(stdout.String()), nil
}

func dockerCommandEnvironment(dockerHost string) []string {
	environment := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "DOCKER_CONTEXT=") || strings.HasPrefix(entry, "DOCKER_HOST=") {
			continue
		}
		environment = append(environment, entry)
	}
	return append(environment, "DOCKER_HOST="+dockerHost)
}
