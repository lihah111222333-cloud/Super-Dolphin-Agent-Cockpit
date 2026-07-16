package localci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
)

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

func (executor *dockerExecutor) create(ctx context.Context, request containerRequest) (string, error) {
	createOutput, err := executor.runner.Run(ctx, executor.createArgs(request)...)
	if err != nil {
		return "", fmt.Errorf("create gate container: %w", err)
	}
	fields := strings.Fields(createOutput)
	if len(fields) == 0 || !isContainerID(fields[0]) {
		return "", errors.New("docker create returned no verifiable container ID")
	}
	if len(fields) != 1 {
		return fields[0], errors.New("docker create returned trailing output after container ID")
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
		"--pids-limit=512", "--storage-opt=size=10G", "--read-only", "--user=65532:65532",
		"--cap-drop=ALL", "--security-opt=no-new-privileges", "--security-opt=seccomp=" + executor.seccompPath,
		"--network=none", "--mount=type=bind,src=" + request.SourceDir + ",dst=/workspace/source,readonly",
		"--tmpfs=/tmp:rw,noexec,nosuid,nodev,size=2147483648", "--log-driver=local", "--log-opt=max-size=10m", "--log-opt=max-file=3",
		request.Image,
	}
	return append(args, request.Command...)
}

// validateContainerRequest 校验不可变镜像、受信源码挂载和 gate 命令。
func (executor *dockerExecutor) validateContainerRequest(request containerRequest) error {
	if !strings.Contains(request.Image, "@sha256:") || !sha256DigestPattern.MatchString(request.Image[strings.LastIndex(request.Image, "@")+1:]) {
		return errors.New("container image must use an immutable platform manifest digest")
	}
	if err := executor.validateSourceDirectory(request.SourceDir); err != nil {
		return err
	}
	if len(request.Command) == 0 || request.Command[0] == "" {
		return errors.New("gate command is required")
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
