package remotecicli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterocinode"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociworkflow"
)

const automaticRunnerOutputMaxBytes = 4 << 20

// AutomaticSSHRunnerConfig 显式绑定一个 manifest node 的 SSH 与平台 runner 参数。
type AutomaticSSHRunnerConfig struct {
	SSHExecutable string
	Host          string
	Platform      heteroci.Platform
	Timeout       time.Duration
	WaitDelay     time.Duration
	LogWriter     io.Writer
	Linux         *AutomaticLinuxRunnerConfig
	Windows       *AutomaticWindowsRunnerConfig
}

// AutomaticLinuxRunnerConfig 绑定 Ubuntu Docker、共享源码/cache 与 worker identity。
type AutomaticLinuxRunnerConfig struct {
	RunAsUser         string
	DockerGroup       string
	RunnerExecutable  string
	RepoPath          string
	SharedRoot        string
	CacheRoot         string
	RepositoryID      string
	NodeID            string
	ObjectFormat      string
	SourceCommit      string
	SourceTree        string
	ToolchainIdentity string
	ModuleIdentity    string
	WorkloadIdentity  string
	DockerImage       string
	WorkerBinary      string
	WorkerIdentity    string
}

// AutomaticWindowsRunnerConfig 绑定 Windows 私有源码/cache 与 exact source identity。
type AutomaticWindowsRunnerConfig struct {
	RunnerExecutable  string
	RepoPath          string
	ObjectStoreRoot   string
	WorktreeRoot      string
	CacheRoot         string
	RepositoryID      string
	NodeID            string
	SourceCommit      string
	SourceTree        string
	ToolchainIdentity string
	ModuleIdentity    string
	WorkloadIdentity  string
}

type automaticSSHRunner struct {
	config AutomaticSSHRunnerConfig
}

// NewAutomaticSSHRunner 构造无 command、node 或平台 fallback 的 runner。
func NewAutomaticSSHRunner(config AutomaticSSHRunnerConfig) (heterociexec.Runner, error) {
	if err := validateAutomaticSSHRunnerConfig(config); err != nil {
		return nil, err
	}
	return &automaticSSHRunner{config: config}, nil
}

// Run 只执行已编码的 node batch command，并要求 stdout 含唯一 canonical batch result。
func (runner *automaticSSHRunner) Run(ctx context.Context, command heterociexec.Command) (heterociexec.RunOutcome, error) {
	if runner == nil || ctx == nil || command.Platform != runner.config.Platform {
		return heterociexec.RunOutcome{}, errors.New("automatic SSH runner input is invalid")
	}
	remoteArgs, err := runner.remoteRunnerArgs(command)
	if err != nil {
		return heterociexec.RunOutcome{}, err
	}
	remoteCommand, err := quoteAutomaticRemoteCommand(runner.config.Platform, remoteArgs)
	if err != nil {
		return heterociexec.RunOutcome{}, err
	}
	runCtx, cancel := context.WithTimeout(ctx, runner.config.Timeout)
	defer cancel()
	stdout, stderr := &boundedAutomaticOutput{}, &boundedAutomaticOutput{}
	ssh := exec.CommandContext(runCtx, runner.config.SSHExecutable,
		"-o", "BatchMode=yes", "-o", "ConnectTimeout="+strconv.FormatInt(max(int64(runner.config.Timeout/time.Second), 1), 10),
		"--", runner.config.Host, remoteCommand)
	ssh.WaitDelay = runner.config.WaitDelay
	ssh.Stdout = io.MultiWriter(runner.config.LogWriter, stdout)
	ssh.Stderr = io.MultiWriter(runner.config.LogWriter, stderr)
	runErr := ssh.Run()
	result, resultErr := decodeAutomaticBatchOutput(stdout.Bytes())
	if resultErr != nil {
		return heterociexec.RunOutcome{}, errors.Join(errors.New("automatic SSH runner batch result is unavailable"), runErr, resultErr, runCtx.Err())
	}
	exitCode := automaticSSHExitCode(runErr)
	if err := validateAutomaticBatchExit(exitCode, result); err != nil {
		return heterociexec.RunOutcome{}, err
	}
	return heterociexec.RunOutcome{ExitCode: exitCode, Outcomes: projectAutomaticBatchOutcomes(result.Outcomes)}, nil
}

// remoteRunnerArgs 把 canonical command 包装到唯一平台 runner argv。
func (runner *automaticSSHRunner) remoteRunnerArgs(command heterociexec.Command) ([]string, error) {
	switch runner.config.Platform {
	case heteroci.PlatformLinuxAMD64:
		if len(command.Argv) == 0 || command.CommandLine != "" || command.CommandBase64 != "" {
			return nil, errors.New("automatic Linux command is not canonical")
		}
		return automaticLinuxRunnerArgs(*runner.config.Linux, command.Argv), nil
	case heteroci.PlatformWindowsAMD64:
		if command.CommandBase64 == "" {
			return nil, errors.New("automatic Windows command base64 is missing")
		}
		return automaticWindowsRunnerArgs(*runner.config.Windows, command.CommandBase64), nil
	default:
		return nil, errors.New("automatic runner platform is unsupported")
	}
}

func automaticLinuxRunnerArgs(config AutomaticLinuxRunnerConfig, command []string) []string {
	args := []string{"sudo", "-n", "--", "systemd-run", "--quiet", "--scope", "--slice=super-dolphin-ci.slice", "--uid=" + config.RunAsUser, "--gid=" + config.DockerGroup, "--", config.RunnerExecutable,
		"--repo", config.RepoPath, "--mode", "docker", "--runtime", "ubuntu", "--docker-image", config.DockerImage,
		"--worker-binary", config.WorkerBinary, "--worker-identity", config.WorkerIdentity,
		"--shared-root", config.SharedRoot, "--cache-root", config.CacheRoot,
		"--repository-id", config.RepositoryID, "--node-id", config.NodeID,
		"--object-format", config.ObjectFormat, "--source-commit", config.SourceCommit, "--source-tree", config.SourceTree,
		"--toolchain-identity", config.ToolchainIdentity, "--goos", "linux", "--goarch", "amd64", "--cgo-enabled", "0",
		"--module-identity", config.ModuleIdentity, "--workload-identity", config.WorkloadIdentity,
		"--timeout-seconds", "120", "--"}
	return append(args, command...)
}

func automaticWindowsRunnerArgs(config AutomaticWindowsRunnerConfig, commandBase64 string) []string {
	return []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", config.RunnerExecutable,
		"-RepoPath", config.RepoPath, "-CommandBase64", commandBase64, "-NodeId", config.NodeID, "-RepositoryId", config.RepositoryID,
		"-ObjectStoreRoot", config.ObjectStoreRoot, "-WorktreeRoot", config.WorktreeRoot, "-CacheRoot", config.CacheRoot,
		"-ToolchainIdentity", config.ToolchainIdentity, "-GOOS", "windows", "-GOARCH", "amd64", "-CGOEnabled", "0",
		"-ModuleIdentity", config.ModuleIdentity, "-WorkloadIdentity", config.WorkloadIdentity,
		"-SourceCommit", config.SourceCommit, "-SourceTree", config.SourceTree, "-TimeoutSeconds", "120"}
}

// validateAutomaticSSHRunnerConfig 校验 SSH 依赖与恰好一个平台配置。
func validateAutomaticSSHRunnerConfig(config AutomaticSSHRunnerConfig) error {
	if !validAutomaticRunnerText(config.SSHExecutable) || !validAutomaticRunnerText(config.Host) || config.Timeout <= 0 || config.WaitDelay <= 0 || config.LogWriter == nil {
		return errors.New("automatic SSH runner dependencies are invalid")
	}
	switch config.Platform {
	case heteroci.PlatformLinuxAMD64:
		return validateAutomaticLinuxSelection(config)
	case heteroci.PlatformWindowsAMD64:
		return validateAutomaticWindowsSelection(config)
	default:
		return errors.New("automatic SSH runner platform config is invalid")
	}
}

func validateAutomaticLinuxSelection(config AutomaticSSHRunnerConfig) error {
	if config.Linux == nil || config.Windows != nil {
		return errors.New("automatic Linux runner selection is invalid")
	}
	return validateAutomaticLinuxRunner(*config.Linux)
}

func validateAutomaticWindowsSelection(config AutomaticSSHRunnerConfig) error {
	if config.Windows == nil || config.Linux != nil {
		return errors.New("automatic Windows runner selection is invalid")
	}
	return validateAutomaticWindowsRunner(*config.Windows)
}

func validateAutomaticLinuxRunner(config AutomaticLinuxRunnerConfig) error {
	values := []string{config.RunAsUser, config.DockerGroup, config.RunnerExecutable, config.RepoPath, config.SharedRoot, config.CacheRoot, config.RepositoryID, config.NodeID,
		config.ObjectFormat, config.SourceCommit, config.SourceTree, config.ToolchainIdentity, config.ModuleIdentity, config.WorkloadIdentity,
		config.DockerImage, config.WorkerBinary, config.WorkerIdentity}
	for _, value := range values {
		if !validAutomaticRunnerText(value) {
			return errors.New("automatic Linux runner field is invalid")
		}
	}
	return nil
}

func validateAutomaticWindowsRunner(config AutomaticWindowsRunnerConfig) error {
	values := []string{config.RunnerExecutable, config.RepoPath, config.ObjectStoreRoot, config.WorktreeRoot, config.CacheRoot,
		config.RepositoryID, config.NodeID, config.SourceCommit, config.SourceTree, config.ToolchainIdentity, config.ModuleIdentity, config.WorkloadIdentity}
	for _, value := range values {
		if !validAutomaticRunnerText(value) {
			return errors.New("automatic Windows runner field is invalid")
		}
	}
	return nil
}

func validAutomaticRunnerText(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && strings.IndexFunc(value, unicode.IsControl) < 0
}

// quoteAutomaticRemoteCommand 按目标平台生成无 shell fallback 的 SSH 命令。
func quoteAutomaticRemoteCommand(platform heteroci.Platform, args []string) (string, error) {
	if platform == heteroci.PlatformWindowsAMD64 {
		for _, argument := range args {
			if !automaticWindowsSSHArgumentSafe(argument) {
				return "", errors.New("automatic Windows SSH argument is unsafe")
			}
		}
		return strings.Join(args, " "), nil
	}
	quoted := make([]string, len(args))
	for index, argument := range args {
		quoted[index] = "'" + strings.ReplaceAll(argument, "'", "'\"'\"'") + "'"
	}
	return strings.Join(quoted, " "), nil
}

func automaticWindowsSSHArgumentSafe(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_./:=+-", character) {
			return false
		}
	}
	return true
}

type boundedAutomaticOutput struct {
	mu   sync.Mutex
	data bytes.Buffer
}

// Write 以固定上限同时承载协议解析和可观察日志。
func (output *boundedAutomaticOutput) Write(data []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	if output.data.Len()+len(data) > automaticRunnerOutputMaxBytes {
		return 0, errors.New("automatic runner output exceeds limit")
	}
	return output.data.Write(data)
}

// Bytes 返回当前捕获内容的独立副本。
func (output *boundedAutomaticOutput) Bytes() []byte {
	output.mu.Lock()
	defer output.mu.Unlock()
	return append([]byte(nil), output.data.Bytes()...)
}

// decodeAutomaticBatchOutput 从混合日志中要求恰好一个 canonical batch result。
func decodeAutomaticBatchOutput(output []byte) (heterocinode.BatchResult, error) {
	var found *heterocinode.BatchResult
	for line := range bytes.SplitSeq(bytes.TrimSpace(output), []byte{'\n'}) {
		if !bytes.Contains(line, []byte(`"schema_version":"heteroci-node-batch-result/v1"`)) {
			continue
		}
		result, err := heterocinode.DecodeBatchResult(bytes.TrimSuffix(line, []byte{'\r'}))
		if err != nil || found != nil {
			return heterocinode.BatchResult{}, errors.New("automatic runner batch result count is invalid")
		}
		found = &result
	}
	if found == nil {
		return heterocinode.BatchResult{}, errors.New("automatic runner batch result is missing")
	}
	return *found, nil
}

func automaticSSHExitCode(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return exitErr.ExitCode()
	}
	return -1
}

func validateAutomaticBatchExit(exitCode int, result heterocinode.BatchResult) error {
	if exitCode == 0 && result.Outcome == heterocinode.BatchOutcomeSuccess {
		return nil
	}
	if exitCode > 0 && result.Outcome == heterocinode.BatchOutcomeFailed {
		return nil
	}
	return errors.New("automatic runner exit status disagrees with batch result")
}

func projectAutomaticBatchOutcomes(outcomes []heterocinode.BatchOutcome) []heterociworkflow.BatchOutcome {
	projected := make([]heterociworkflow.BatchOutcome, len(outcomes))
	for index, outcome := range outcomes {
		state := heteroci.LeaseStateSucceeded
		if outcome.Status == heterocinode.BatchOutcomeFailed {
			state = heteroci.LeaseStateFailed
		}
		projected[index] = heterociworkflow.BatchOutcome{UnitID: outcome.UnitID, State: state}
	}
	return projected
}
