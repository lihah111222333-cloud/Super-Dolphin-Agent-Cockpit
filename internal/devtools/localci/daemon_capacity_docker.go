package localci

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
)

const (
	dockerInfoJSONFormat = "{{json .}}"
	containerWorkDir     = "/workspace/work"
	containerWorkTmpfs   = "rw,exec,nosuid,nodev,size=5368709120,uid=65532,gid=65532,mode=0700"
	containerTempTmpfs   = "rw,noexec,nosuid,nodev,size=2147483648"
)

var containerRuntimeEnvironment = []string{
	"HOME=/workspace/work/home",
	"TMPDIR=/workspace/work/tmp",
	"GOCACHE=/workspace/work/go-cache",
	"GOMODCACHE=/workspace/work/go-mod-cache",
	"npm_config_cache=/workspace/work/npm-cache",
	"XDG_CACHE_HOME=/workspace/work/xdg-cache",
	"PLAYWRIGHT_BROWSERS_PATH=/opt/super-dolphin-gate/runtime/frontend/node_modules/.cache/ms-playwright",
}

type dockerDaemonCapacityInspector struct {
	runner dockerRunner
	now    func() time.Time
}

// dockerInfoPayload 固化受支持的 Docker info 顶层字段合同。
// 未消费字段保留原始 JSON，使顶层字段漂移仍在此边界失败。
type dockerInfoPayload struct {
	ID       string `json:"ID"`
	NCPU     int64  `json:"NCPU"`
	MemTotal int64  `json:"MemTotal"`

	Containers          json.RawMessage
	ContainersRunning   json.RawMessage
	ContainersPaused    json.RawMessage
	ContainersStopped   json.RawMessage
	Images              json.RawMessage
	Driver              json.RawMessage
	DriverStatus        json.RawMessage
	Plugins             json.RawMessage
	MemoryLimit         json.RawMessage
	SwapLimit           json.RawMessage
	KernelMemoryTCP     json.RawMessage
	CpuCfsPeriod        json.RawMessage
	CpuCfsQuota         json.RawMessage
	CPUShares           json.RawMessage
	CPUSet              json.RawMessage
	PidsLimit           json.RawMessage
	IPv4Forwarding      json.RawMessage
	BridgeNfIptables    json.RawMessage
	BridgeNfIP6tables   json.RawMessage
	Debug               json.RawMessage
	NFd                 json.RawMessage
	OomKillDisable      json.RawMessage
	NGoroutines         json.RawMessage
	SystemTime          json.RawMessage
	LoggingDriver       json.RawMessage
	CgroupDriver        json.RawMessage
	CgroupVersion       json.RawMessage
	NEventsListener     json.RawMessage
	KernelVersion       json.RawMessage
	OperatingSystem     json.RawMessage
	OSVersion           json.RawMessage
	OSType              json.RawMessage
	Architecture        json.RawMessage
	IndexServerAddress  json.RawMessage
	RegistryConfig      json.RawMessage
	GenericResources    json.RawMessage
	DockerRootDir       json.RawMessage
	HttpProxy           json.RawMessage
	HttpsProxy          json.RawMessage
	NoProxy             json.RawMessage
	Name                json.RawMessage
	Labels              json.RawMessage
	ExperimentalBuild   json.RawMessage
	ServerVersion       json.RawMessage
	ClusterStore        json.RawMessage
	ClusterAdvertise    json.RawMessage
	Runtimes            json.RawMessage
	DefaultRuntime      json.RawMessage
	Swarm               json.RawMessage
	LiveRestoreEnabled  json.RawMessage
	Isolation           json.RawMessage
	InitBinary          json.RawMessage
	ContainerdCommit    json.RawMessage
	RuncCommit          json.RawMessage
	InitCommit          json.RawMessage
	SecurityOptions     json.RawMessage
	ProductLicense      json.RawMessage
	DefaultAddressPools json.RawMessage
	FirewallBackend     json.RawMessage
	CDISpecDirs         json.RawMessage
	DiscoveredDevices   json.RawMessage
	Containerd          json.RawMessage
	Warnings            json.RawMessage
	ClientInfo          json.RawMessage
}

func newDockerDaemonCapacityInspector(runner dockerRunner) (*dockerDaemonCapacityInspector, error) {
	if isNilDockerRunner(runner) {
		return nil, errors.New("docker daemon capacity runner is nil")
	}
	return &dockerDaemonCapacityInspector{runner: runner, now: time.Now}, nil
}

// InspectDaemonCapacity 仅通过固定 CLI 命令读取当前 active Docker daemon。
func (inspector *dockerDaemonCapacityInspector) InspectDaemonCapacity(
	ctx context.Context,
	daemonID string,
) (DaemonCapacity, error) {
	if err := inspector.validateInputs(ctx, daemonID); err != nil {
		return DaemonCapacity{}, err
	}
	output, err := inspector.runner.Run(ctx, "info", "--format", dockerInfoJSONFormat)
	if err != nil {
		return DaemonCapacity{}, fmt.Errorf("read docker daemon info: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return DaemonCapacity{}, fmt.Errorf("read docker daemon info: %w", err)
	}
	info, err := decodeDockerInfo(output)
	if err != nil {
		return DaemonCapacity{}, err
	}
	if info.ID != daemonID {
		return DaemonCapacity{}, fmt.Errorf(
			"docker daemon identity mismatch: requested %q, inspected %q",
			daemonID,
			info.ID,
		)
	}
	capacity := DaemonCapacity{
		DaemonID:    info.ID,
		ObservedAt:  inspector.now().UTC(),
		LogicalCPUs: info.NCPU,
		MemoryBytes: info.MemTotal,
	}
	if err := capacity.Validate(); err != nil {
		return DaemonCapacity{}, fmt.Errorf("validate docker daemon info capacity: %w", err)
	}
	return capacity, nil
}

// validateInputs 在执行 Docker CLI 前校验 inspector、请求身份和上下文。
func (inspector *dockerDaemonCapacityInspector) validateInputs(ctx context.Context, daemonID string) error {
	if inspector == nil {
		return errors.New("docker daemon capacity inspector is nil")
	}
	if ctx == nil {
		return errors.New("docker daemon capacity context is nil")
	}
	if err := validateDaemonID(daemonID); err != nil {
		return err
	}
	if isNilDockerRunner(inspector.runner) {
		return errors.New("docker daemon capacity runner is nil")
	}
	if inspector.now == nil {
		return errors.New("docker daemon capacity clock is nil")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("read docker daemon info: %w", err)
	}
	return nil
}

func decodeDockerInfo(output string) (dockerInfoPayload, error) {
	decoder := json.NewDecoder(strings.NewReader(output))
	decoder.DisallowUnknownFields()
	var info dockerInfoPayload
	if err := decoder.Decode(&info); err != nil {
		return dockerInfoPayload{}, fmt.Errorf("decode docker daemon info JSON: %w", err)
	}
	var trailing json.RawMessage
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return info, nil
	}
	if err == nil {
		return dockerInfoPayload{}, errors.New("docker daemon info contains trailing JSON")
	}
	return dockerInfoPayload{}, fmt.Errorf("docker daemon info contains trailing output: %w", err)
}

func isNilDockerRunner(runner dockerRunner) bool {
	if runner == nil {
		return true
	}
	value := reflect.ValueOf(runner)
	kind := value.Kind()
	return kind >= reflect.Chan && kind <= reflect.Slice && value.IsNil()
}

const (
	normalExecutionTimeout  = 10 * time.Minute
	releaseExecutionTimeout = 30 * time.Minute
)

type dockerRunner interface {
	Run(ctx context.Context, args ...string) (string, error)
}

type execDockerRunner struct{}

// Run 执行单个 Docker CLI 操作并保留标准错误上下文。
func (execDockerRunner) Run(ctx context.Context, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "docker", args...)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker %s: %w: %s", args[0], err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

type dockerExecutor struct {
	runner            dockerRunner
	seccompPath       string
	seccompDigest     string
	trustedSourceRoot string
}

type containerRequest struct {
	Image     string
	SourceDir string
	Command   []string
	Release   bool
	Labels    map[string]string
}

type containerLifecycleEvidence struct {
	ContainerID   string
	ExitCode      int
	Removed       bool
	Killed        bool
	Deadline      time.Time
	SeccompDigest string
}

// newDockerExecutor 固定 seccomp 摘要和受信源码临时根。
func newDockerExecutor(runner dockerRunner, seccompPath string, trustedSourceRoot string) (*dockerExecutor, error) {
	if runner == nil {
		return nil, errors.New("docker runner is required")
	}
	if !filepath.IsAbs(seccompPath) || filepath.Clean(seccompPath) != seccompPath {
		return nil, errors.New("seccomp profile path must be canonical and absolute")
	}
	if info, err := os.Stat(seccompPath); err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("seccomp profile must be an existing regular file")
	}
	seccompDigest, err := fileDigest(seccompPath)
	if err != nil {
		return nil, fmt.Errorf("digest seccomp profile: %w", err)
	}
	root, err := trustedDirectory(trustedSourceRoot)
	if err != nil {
		return nil, fmt.Errorf("trusted source root: %w", err)
	}
	if root == string(filepath.Separator) {
		return nil, errors.New("trusted source root cannot be the filesystem root")
	}
	return &dockerExecutor{runner: runner, seccompPath: seccompPath, seccompDigest: seccompDigest, trustedSourceRoot: root}, nil
}

// Run 创建一次性容器，执行 gate，并返回清理结果证据。
func (executor *dockerExecutor) Run(ctx context.Context, request containerRequest) (containerLifecycleEvidence, error) {
	var evidence containerLifecycleEvidence
	if err := executor.validateContainerRequest(request); err != nil {
		return evidence, err
	}
	if err := executor.verifySeccomp(); err != nil {
		return evidence, err
	}
	runContext, cancel := platformconfig.WithTimeout(ctx, executionTimeout(request.Release))
	defer cancel()
	evidence.SeccompDigest = executor.seccompDigest
	evidence.Deadline, _ = runContext.Deadline()
	containerID, err := executor.create(runContext, request)
	evidence.ContainerID = containerID
	if evidence.ContainerID == "" {
		return evidence, err
	}
	if err != nil {
		return executor.removeAndJoin(ctx, evidence, err)
	}
	if _, err := executor.runner.Run(runContext, "start", evidence.ContainerID); err != nil {
		return executor.removeAndJoin(ctx, evidence, fmt.Errorf("start gate container: %w", err))
	}
	evidence, err = executor.wait(runContext, ctx, evidence)
	return executor.removeAndJoin(ctx, evidence, err)
}

func (executor *dockerExecutor) wait(runContext context.Context, parentContext context.Context, evidence containerLifecycleEvidence) (containerLifecycleEvidence, error) {
	exitCode, err := executor.runner.Run(runContext, "wait", evidence.ContainerID)
	if err != nil {
		killContext, killCancel := platformconfig.WithTimeout(context.WithoutCancel(parentContext), 30*time.Second)
		defer killCancel()
		if _, killErr := executor.runner.Run(killContext, "kill", evidence.ContainerID); killErr != nil {
			return evidence, errors.Join(fmt.Errorf("wait for gate container: %w", err), fmt.Errorf("kill gate container: %w", killErr))
		}
		evidence.Killed = true
		return evidence, fmt.Errorf("wait for gate container: %w", err)
	}
	parsedExitCode, err := strconv.Atoi(strings.TrimSpace(exitCode))
	if err != nil {
		return evidence, fmt.Errorf("parse gate container exit code %q: %w", strings.TrimSpace(exitCode), err)
	}
	evidence.ExitCode = parsedExitCode
	if parsedExitCode != 0 {
		return evidence, fmt.Errorf("gate container exited with code %d", parsedExitCode)
	}
	return evidence, nil
}

// create 仅在 Docker 输出唯一规范 container ID 时返回，保留可验证 ID 以便失败后清理。
func (executor *dockerExecutor) create(ctx context.Context, request containerRequest) (string, error) {
	createOutput, err := executor.runner.Run(ctx, executor.createArgs(request)...)
	fields := strings.Fields(createOutput)
	if len(fields) == 0 || !isContainerID(fields[0]) {
		if err != nil {
			return "", errors.Join(fmt.Errorf("create gate container: %w", err), errors.New("docker create returned no verifiable container ID"))
		}
		return "", errors.New("docker create returned no verifiable container ID")
	}
	if len(fields) != 1 {
		if err != nil {
			return fields[0], errors.Join(fmt.Errorf("create gate container: %w", err), errors.New("docker create returned trailing output after container ID"))
		}
		return fields[0], errors.New("docker create returned trailing output after container ID")
	}
	if err != nil {
		return fields[0], fmt.Errorf("create gate container: %w", err)
	}
	return fields[0], nil
}

func (executor *dockerExecutor) remove(parentContext context.Context, containerID string) error {
	cleanupContext, cleanupCancel := platformconfig.WithTimeout(context.WithoutCancel(parentContext), 30*time.Second)
	defer cleanupCancel()
	_, err := executor.runner.Run(cleanupContext, "rm", "--force", containerID)
	return err
}

func (executor *dockerExecutor) removeAndJoin(ctx context.Context, evidence containerLifecycleEvidence, runErr error) (containerLifecycleEvidence, error) {
	if cleanupErr := executor.remove(ctx, evidence.ContainerID); cleanupErr != nil {
		return evidence, errors.Join(runErr, fmt.Errorf("remove gate container: %w", cleanupErr))
	}
	evidence.Removed = true
	return evidence, runErr
}

func (executor *dockerExecutor) verifySeccomp() error {
	currentDigest, err := fileDigest(executor.seccompPath)
	if err != nil {
		return fmt.Errorf("verify seccomp profile: %w", err)
	}
	if currentDigest != executor.seccompDigest {
		return errors.New("seccomp profile changed after executor construction")
	}
	return nil
}

func (executor *dockerExecutor) createArgs(request containerRequest) []string {
	args := []string{
		"create",
		"--cpus=" + strconv.FormatInt(workloadLogicalCPUs, 10),
		"--memory=" + strconv.FormatInt(workloadMemoryGiB, 10) + "g",
		"--pids-limit=512", "--storage-opt=size=10G", "--init", "--read-only", "--user=65532:65532",
		"--cap-drop=ALL", "--security-opt=no-new-privileges", "--security-opt=seccomp=" + executor.seccompPath,
		"--network=none", "--mount=type=bind,src=" + request.SourceDir + ",dst=/workspace/source,readonly",
		"--tmpfs=/tmp:" + containerTempTmpfs, "--tmpfs=" + containerWorkDir + ":" + containerWorkTmpfs,
		"--workdir=" + containerWorkDir,
	}
	for _, entry := range containerRuntimeEnvironment {
		args = append(args, "--env="+entry)
	}
	args = append(args, "--log-driver=local", "--log-opt=max-size=10m", "--log-opt=max-file=3")
	labelKeys := make([]string, 0, len(request.Labels))
	for key := range request.Labels {
		labelKeys = append(labelKeys, key)
	}
	slices.Sort(labelKeys)
	for _, key := range labelKeys {
		args = append(args, "--label="+key+"="+request.Labels[key])
	}
	args = append(args, "--entrypoint="+request.Command[0], request.Image)
	return append(args, request.Command[1:]...)
}

// validateContainerRequest 校验不可变镜像、受信源码挂载和 gate 命令。
func (executor *dockerExecutor) validateContainerRequest(request containerRequest) error {
	if !strings.Contains(request.Image, "@sha256:") || !sha256DigestPattern.MatchString(request.Image[strings.LastIndex(request.Image, "@")+1:]) {
		return errors.New("container image must use an immutable platform manifest digest")
	}
	if err := executor.validateSourceDirectory(request.SourceDir); err != nil {
		return err
	}
	if len(request.Command) == 0 || !strings.HasPrefix(request.Command[0], "/") || filepath.Clean(request.Command[0]) != request.Command[0] {
		return errors.New("gate command must use a canonical absolute executable path")
	}
	return validateContainerLabels(request.Labels)
}

// validateContainerLabels 拒绝 Docker CLI 无法无歧义表达的 key/value。
func validateContainerLabels(labels map[string]string) error {
	for key, value := range labels {
		if key == "" || strings.TrimSpace(key) != key || strings.ContainsAny(key, "=\x00\r\n\t ") {
			return errors.New("container label key is not canonical")
		}
		if value == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\x00\r\n") {
			return errors.New("container label value is not canonical")
		}
	}
	return nil
}

// validateSourceDirectory 将源码限制在受信临时根的真实子目录中。
func (executor *dockerExecutor) validateSourceDirectory(sourceDirectory string) error {
	if strings.ContainsAny(sourceDirectory, ",\x00\r\n") {
		return errors.New("source directory contains Docker mount CSV control characters")
	}
	sourceDir, err := trustedDirectory(sourceDirectory)
	if err != nil {
		return fmt.Errorf("source directory: %w", err)
	}
	relative, err := filepath.Rel(executor.trustedSourceRoot, sourceDir)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("source directory must be a child of the trusted temporary root")
	}
	return nil
}

// trustedDirectory 解析真实目录，阻止符号链接逃逸。
func trustedDirectory(directory string) (string, error) {
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return "", errors.New("path must be canonical and absolute")
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("path must be a directory")
	}
	return resolved, nil
}

func executionTimeout(release bool) time.Duration {
	if release {
		return releaseExecutionTimeout
	}
	return normalExecutionTimeout
}

func fileDigest(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// isContainerID 只接受 Docker 返回的十六进制容器标识。
func isContainerID(value string) bool {
	if len(value) < 12 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func requireRecoveryRunner(ctx context.Context, runner *FreshContainerRunner) error {
	if ctx == nil || runner == nil || runner.docker == nil {
		return errors.New("recovery runner and context are required")
	}
	return nil
}

// removeControlledBuilder 在限定时间内清理 builder、BuildKit 容器及其 ownership 记录。
func (runner *DockerBuildxRunner) removeControlledBuilder(ctx context.Context, builderName string) error {
	cleanupCtx, cancel := BoundedCleanupContext(ctx, buildxBuilderCleanupLimit)
	defer cancel()
	_, builderRemoveErr := runner.executor.Run(cleanupCtx, bytes.NewReader(nil), "buildx", "rm", "--force", builderName)
	_, containerRemoveErr := runner.executor.Run(cleanupCtx, bytes.NewReader(nil), "container", "rm", "--force", controlledBuildxContainerName(builderName))
	confirmErr := runner.confirmControlledBuilderRemoved(cleanupCtx, builderName)
	if confirmErr != nil {
		errorsToJoin := []error{confirmErr}
		if builderRemoveErr != nil {
			errorsToJoin = append(errorsToJoin, fmt.Errorf("run bounded controlled buildx builder cleanup: %w", builderRemoveErr))
		}
		if containerRemoveErr != nil {
			errorsToJoin = append(errorsToJoin, fmt.Errorf("run bounded controlled buildx builder container cleanup: %w", containerRemoveErr))
		}
		return errors.Join(errorsToJoin...)
	}
	if err := os.Remove(runner.controlledBuilderOwnerPath(builderName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove controlled buildx builder ownership record: %w", err)
	}
	return nil
}

// RecoverControlledBuilders 在接纳构建容量前回收本 runner 记录或前缀归属的 builder。
func (runner *DockerBuildxRunner) RecoverControlledBuilders(ctx context.Context) error {
	if err := runner.validateControlledBuilderRecovery(ctx); err != nil {
		return err
	}
	names, err := runner.controlledBuilderNamesForRecovery(ctx)
	if err != nil {
		return err
	}
	for _, builderName := range sortedBuildxBuilderNames(names) {
		if err := runner.removeControlledBuilder(ctx, builderName); err != nil {
			return fmt.Errorf("recover controlled buildx builder %q: %w", builderName, err)
		}
	}
	return nil
}

// validateControlledBuilderRecovery 在读取或删除资源前校验 recovery 依赖。
func (runner *DockerBuildxRunner) validateControlledBuilderRecovery(ctx context.Context) error {
	if runner == nil || buildxCommandExecutorIsNil(runner.executor) {
		return errors.New("docker buildx runner is required for controlled builder recovery")
	}
	if ctx == nil {
		return errors.New("controlled buildx builder recovery context is required")
	}
	return ctx.Err()
}

// controlledBuilderNamesForRecovery 合并持久化记录、builder 列表与容器列表中的归属名称。
func (runner *DockerBuildxRunner) controlledBuilderNamesForRecovery(ctx context.Context) (map[string]struct{}, error) {
	ownerNames, err := runner.recordedControlledBuilderNames()
	if err != nil {
		return nil, err
	}
	builderNames, err := runner.listControlledBuilderNames(ctx)
	if err != nil {
		return nil, err
	}
	containerNames, err := runner.listControlledBuilderContainerNames(ctx)
	if err != nil {
		return nil, err
	}
	return mergeControlledBuildxBuilderNames(ownerNames, builderNames, containerNames), nil
}

// mergeControlledBuildxBuilderNames 去重所有来源的受控 builder 名称。
func mergeControlledBuildxBuilderNames(nameLists ...[]string) map[string]struct{} {
	names := make(map[string]struct{})
	for _, namesToMerge := range nameLists {
		for _, name := range namesToMerge {
			names[name] = struct{}{}
		}
	}
	return names
}

// recordControlledBuilder 在创建前原子写入并同步 ownership 记录。
func (runner *DockerBuildxRunner) recordControlledBuilder(builderName string, request BuildKitBuildRequest) error {
	owner := controlledBuildxOwner{BuilderName: builderName, SourceTreeSHA: request.SourceTreeSHA, InputDigest: request.InputDigest}
	data, err := json.Marshal(owner)
	if err != nil {
		return fmt.Errorf("encode controlled buildx builder ownership record: %w", err)
	}
	file, err := os.OpenFile(runner.controlledBuilderOwnerPath(builderName), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("record controlled buildx builder ownership before create: %w", err)
	}
	if _, writeErr := file.Write(data); writeErr != nil {
		closeErr := file.Close()
		return errors.Join(fmt.Errorf("write controlled buildx builder ownership record: %w", writeErr), closeErr)
	}
	if syncErr := file.Sync(); syncErr != nil {
		closeErr := file.Close()
		return errors.Join(fmt.Errorf("sync controlled buildx builder ownership record: %w", syncErr), closeErr)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close controlled buildx builder ownership record: %w", err)
	}
	return nil
}

// recordedControlledBuilderNames 读取并校验已持久化的受控 builder 名称。
func (runner *DockerBuildxRunner) recordedControlledBuilderNames() ([]string, error) {
	entries, err := os.ReadDir(runner.ownerRoot)
	if err != nil {
		return nil, fmt.Errorf("read controlled buildx builder ownership records: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		builderName, err := runner.recordedControlledBuilderName(entry)
		if err != nil {
			return nil, err
		}
		names = append(names, builderName)
	}
	sort.Strings(names)
	return names, nil
}

// recordedControlledBuilderName 校验一个 ownership 文件与其 builder 身份一致。
func (runner *DockerBuildxRunner) recordedControlledBuilderName(entry os.DirEntry) (string, error) {
	if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
		return "", errors.New("controlled buildx builder ownership record directory contains an invalid entry")
	}
	builderName := strings.TrimSuffix(entry.Name(), ".json")
	if !validControlledBuildxBuilderName(builderName) {
		return "", errors.New("controlled buildx builder ownership record contains an invalid builder name")
	}
	data, err := os.ReadFile(filepath.Join(runner.ownerRoot, entry.Name()))
	if err != nil {
		return "", fmt.Errorf("read controlled buildx builder ownership record: %w", err)
	}
	var owner controlledBuildxOwner
	if err := json.Unmarshal(data, &owner); err != nil {
		return "", fmt.Errorf("decode controlled buildx builder ownership record: %w", err)
	}
	if owner.BuilderName != builderName || len(owner.SourceTreeSHA) < 12 || !strings.HasPrefix(builderName, buildxBuilderNamePrefix+owner.SourceTreeSHA[:12]+"-") {
		return "", errors.New("controlled buildx builder ownership record does not match its builder identity")
	}
	if err := validateDigest("controlled buildx builder ownership input digest", owner.InputDigest); err != nil {
		return "", err
	}
	return builderName, nil
}

// confirmControlledBuilderRemoved 确认 builder 与对应容器都不再存在。
func (runner *DockerBuildxRunner) confirmControlledBuilderRemoved(ctx context.Context, builderName string) error {
	builderNames, err := runner.listControlledBuilderNames(ctx)
	if err != nil {
		return err
	}
	if slices.Contains(builderNames, builderName) {
		return errors.New("controlled buildx builder remains after cleanup")
	}
	containerNames, err := runner.listControlledBuilderContainerNames(ctx)
	if err != nil {
		return err
	}
	if slices.Contains(containerNames, builderName) {
		return errors.New("controlled buildx builder container remains after cleanup")
	}
	return nil
}

// listControlledBuilderNames 列出并验证受本 runner 前缀约束的 builder。
func (runner *DockerBuildxRunner) listControlledBuilderNames(ctx context.Context) ([]string, error) {
	output, err := runner.executor.Run(ctx, bytes.NewReader(nil), "buildx", "ls", "--format", "{{.Name}}")
	if err != nil {
		return nil, fmt.Errorf("list controlled buildx builders: %w", err)
	}
	return parseControlledBuildxBuilderNames(output, "builder")
}

// listControlledBuilderContainerNames 列出并验证受本 runner 前缀约束的 BuildKit 容器。
func (runner *DockerBuildxRunner) listControlledBuilderContainerNames(ctx context.Context) ([]string, error) {
	output, err := runner.executor.Run(ctx, bytes.NewReader(nil), "container", "ls", "--all", "--filter", "name=^/buildx_buildkit_"+buildxBuilderNamePrefix, "--format", "{{.Names}}")
	if err != nil {
		return nil, fmt.Errorf("list controlled buildx builder containers: %w", err)
	}
	return parseControlledBuildxContainerNames(output)
}

// parseControlledBuildxBuilderNames 从 builder 列表筛选并校验受控名称。
func parseControlledBuildxBuilderNames(output string, subject string) ([]string, error) {
	names := make([]string, 0)
	for rawName := range strings.SplitSeq(output, "\n") {
		name := strings.TrimSpace(rawName)
		name = strings.TrimSuffix(name, "*")
		if name == "" || !strings.HasPrefix(name, buildxBuilderNamePrefix) {
			continue
		}
		if !validRecoverableControlledBuildxBuilderName(name) {
			return nil, fmt.Errorf("controlled buildx %s listing contains an invalid builder name", subject)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// parseControlledBuildxContainerNames 从容器列表筛选并校验对应的受控 builder 名称。
func parseControlledBuildxContainerNames(output string) ([]string, error) {
	const containerPrefix = "buildx_buildkit_"
	names := make([]string, 0)
	for rawName := range strings.SplitSeq(output, "\n") {
		containerName := strings.TrimSpace(rawName)
		if containerName == "" || !strings.HasPrefix(containerName, containerPrefix+buildxBuilderNamePrefix) {
			continue
		}
		builderName, found := strings.CutSuffix(strings.TrimPrefix(containerName, containerPrefix), "0")
		if !found || !validRecoverableControlledBuildxBuilderName(builderName) {
			return nil, errors.New("controlled buildx container listing contains an invalid builder name")
		}
		names = append(names, builderName)
	}
	sort.Strings(names)
	return names, nil
}

// validControlledBuildxBuilderName 判断名称是否符合受控 builder 的固定格式。
func validControlledBuildxBuilderName(builderName string) bool {
	return controlledBuildxBuilderNamePattern.MatchString(builderName)
}

// validRecoverableControlledBuildxBuilderName 仅扩展枚举兼容历史 candidate 临时目录命名。
func validRecoverableControlledBuildxBuilderName(builderName string) bool {
	return validControlledBuildxBuilderName(builderName) || legacyControlledBuildxBuilderNamePattern.MatchString(builderName)
}

// sortedBuildxBuilderNames 返回按字典序稳定排列的受控 builder 名称。
func sortedBuildxBuilderNames(names map[string]struct{}) []string {
	values := make([]string, 0, len(names))
	for name := range names {
		values = append(values, name)
	}
	sort.Strings(values)
	return values
}

// controlledBuilderOwnerPath 返回受控 builder 的 ownership 记录路径。
func (runner *DockerBuildxRunner) controlledBuilderOwnerPath(builderName string) string {
	return filepath.Join(runner.ownerRoot, builderName+".json")
}

const noContainerNetwork = "none"
