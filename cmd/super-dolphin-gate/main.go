// Package main provides the standalone Super Dolphin gate coordinator and worker CLI.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
)

const remoteWorkerSetupAllowance = 2 * time.Minute

var (
	gateSourceDigest    string
	gateToolchainDigest string
)

func main() {
	os.Exit(runCLI(os.Args[1:], os.Stdout, os.Stderr))
}

func runCLI(args []string, stdout, stderr io.Writer) int {
	if topLevelHelpRequested(args) {
		if err := writeTopLevelUsage(stdout); err != nil {
			return int(gatecontract.ExitInfrastructure)
		}
		return int(gatecontract.ExitOK)
	}
	if len(args) > 0 && args[0] == "worker" {
		return runWorkerCLI(args[1:], stdout, stderr)
	}
	err := dispatchCLI(args, stdout, stderr)
	if err == nil {
		return int(gatecontract.ExitOK)
	}
	exitCode := gatecontract.ExitCodeOf(err)
	if writeErr := writeCLIError(stderr, err); writeErr != nil {
		return int(gatecontract.ExitInfrastructure)
	}
	return int(exitCode)
}

// topLevelHelpRequested 只识别 CLI 根命令帮助，避免进入配置、token 或执行分派。
func topLevelHelpRequested(args []string) bool {
	return len(args) > 0 && (args[0] == "--help" || args[0] == "-h")
}

// writeTopLevelUsage 在执行前给出最小 authority 入口，详细 test 参数由 test --help 说明。
func writeTopLevelUsage(stdout io.Writer) error {
	_, err := fmt.Fprint(stdout, `Usage: super-dolphin-gate <command> [flags]

Commands:
  super-dolphin-gate test --target=remote --config <path> --ledger <path> --test <package#Test>
      Run an explicit test through the Alibaba Cloud ECI authority.
  super-dolphin-gate remote run --target=remote --config <path>
      Run the canonical remote CI authority path.

Use super-dolphin-gate test --help to discover local, auto, hybrid, remote,
and --gate-workload selection. This help command does not read configuration,
request an agent token, open a ledger, or create ECI work.
`)
	return err
}

func writeCLIError(stderr io.Writer, commandErr error) error {
	if _, err := fmt.Fprintf(stderr, "super-dolphin-gate: %v\n", commandErr); err != nil {
		return fmt.Errorf("write CLI error: %w", err)
	}
	return nil
}

// runWorkerCLI 绑定容器信号并执行同一二进制内的 worker 命令空间。
func runWorkerCLI(args []string, stdout, stderr io.Writer) int {
	if handled, exitCode := runWorkerBuiltinCommand(args, stdout, stderr); handled {
		return exitCode
	}
	return runWorkerExecutor(args, stdout, stderr)
}

// runWorkerBuiltinCommand 处理不需要 executor 生命周期的内建 worker 子命令。
func runWorkerBuiltinCommand(args []string, stdout, stderr io.Writer) (bool, int) {
	if len(args) == 0 {
		return false, 0
	}
	switch args[0] {
	case "cli-identity":
		if err := writeGateCLIIdentity(args[1:], stdout); err != nil {
			_ = writeCLIError(stderr, err)
			return true, int(gatecontract.ExitCodeOf(err))
		}
		return true, int(gatecontract.ExitOK)
	case "go-cache-proxy":
		return true, runWorkerGoCacheProxy(args[1:], stdout, stderr)
	case "validate-go-distribution":
		if err := validateWorkerGoDistribution(args[1:], runtime.GOOS, runtime.GOARCH); err != nil {
			_ = writeCLIError(stderr, err)
			return true, int(gatecontract.ExitCodeOf(err))
		}
		return true, int(gatecontract.ExitOK)
	case "race-package-patterns":
		if err := writeWorkerRacePackagePatterns(args[1:], stdout); err != nil {
			_ = writeCLIError(stderr, err)
			return true, int(gatecontract.ExitCodeOf(err))
		}
		return true, int(gatecontract.ExitOK)
	default:
		return false, 0
	}
}

// runWorkerGoCacheProxy 透传构建缓存代理的退出语义。
func runWorkerGoCacheProxy(args []string, stdout, stderr io.Writer) int {
	if err := gatecontract.ExecuteGoBuildCacheProxy(args, os.Stdin, stdout); err != nil {
		if writeErr := writeCLIError(stderr, err); writeErr != nil {
			return int(gatecontract.ExitInfrastructure)
		}
		return 1
	}
	return 0
}

// validateWorkerGoDistribution 校验 worker 实际运行平台与锁定的远程 Go 发行版一致。
func validateWorkerGoDistribution(args []string, goos, goarch string) error {
	if len(args) != 0 {
		return protocolError("worker validate-go-distribution does not accept arguments")
	}
	if err := gatecontract.ValidateRemoteGoDistributionPlatform(goos, goarch); err != nil {
		return gatecontract.WithExitCode(gatecontract.ExitInfrastructure, fmt.Errorf("validate remote Go distribution: %w", err))
	}
	return nil
}

// writeWorkerRacePackagePatterns 输出唯一登记表中的并发包模式，供 closure seed 直接消费。
func writeWorkerRacePackagePatterns(args []string, stdout io.Writer) error {
	if len(args) != 0 {
		return protocolError("worker race-package-patterns does not accept arguments")
	}
	for _, pattern := range gatecontract.RaceSensitivePackagePatterns() {
		if _, err := fmt.Fprintln(stdout, pattern); err != nil {
			return gatecontract.WithExitCode(gatecontract.ExitInfrastructure, fmt.Errorf("write race package patterns: %w", err))
		}
	}
	return nil
}

// runWorkerExecutor 为普通 worker 命令绑定信号和执行生命周期。
func runWorkerExecutor(args []string, stdout, stderr io.Writer) int {
	termContext, stopTerm := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stopTerm()
	interruptContext, stopInterrupt := signal.NotifyContext(termContext, os.Interrupt)
	defer stopInterrupt()

	executionContext, stopExecution, contextErr := workerExecutionContext(interruptContext)
	defer stopExecution()
	commandErr := contextErr
	if commandErr == nil {
		commandErr = gatecontract.ExecuteExecutor(executionContext, args, stdout, stderr)
	}
	if commandErr != nil {
		if err := writeCLIError(stderr, commandErr); err != nil {
			return int(gatecontract.ExitInfrastructure)
		}
	}

	exitCode := gatecontract.ExecutorExitCode(commandErr)
	return workerSignalExitCode(termContext, interruptContext, exitCode)
}

// workerSignalExitCode 让容器信号优先覆盖 executor 返回码。
func workerSignalExitCode(termContext, interruptContext context.Context, exitCode int) int {
	if termContext.Err() != nil {
		return signalExitCode(syscall.SIGTERM)
	}
	if interruptContext.Err() != nil {
		return signalExitCode(syscall.SIGINT)
	}
	return exitCode
}

// writeGateCLIIdentity 输出由链接器绑定的编译闭包身份，供 Seed 在复用前独立核对。
func writeGateCLIIdentity(args []string, stdout io.Writer) error {
	if len(args) != 0 {
		return protocolError("worker cli-identity does not accept arguments")
	}
	sourceDigest, toolchainDigest, err := linkedGateCLIIdentity()
	if err != nil {
		return gatecontract.WithExitCode(gatecontract.ExitInfrastructure, err)
	}
	if _, err := fmt.Fprintf(stdout, "gate_source_sha256=%s\nplatform=%s/%s\ntoolchain_digest=%s\n", sourceDigest, runtime.GOOS, runtime.GOARCH, toolchainDigest); err != nil {
		return gatecontract.WithExitCode(gatecontract.ExitInfrastructure, fmt.Errorf("write gate CLI identity: %w", err))
	}
	return nil
}

// workerExecutionContext 为缓存准备保留独立租约，并把实际 workload 时限延迟到 executor 内启动。
func workerExecutionContext(parent context.Context) (context.Context, context.CancelFunc, error) {
	raw, configured := os.LookupEnv(gatecontract.ExecutorWorkloadTimeoutEnvironment)
	if !configured {
		ctx, cancel := context.WithCancel(parent)
		cancel()
		return ctx, func() {}, fmt.Errorf("%s is required", gatecontract.ExecutorWorkloadTimeoutEnvironment)
	}
	timeout, err := time.ParseDuration(raw)
	if err == nil {
		err = gatecontract.ValidateExecutorWorkloadTimeout(timeout)
	}
	if err != nil {
		ctx, cancel := context.WithCancel(parent)
		cancel()
		return ctx, func() {}, fmt.Errorf("invalid executor workload timeout: %w", err)
	}
	workloadCtx, err := gatecontract.WithExecutorWorkloadTimeout(parent, timeout)
	if err != nil {
		ctx, cancel := context.WithCancel(parent)
		cancel()
		return ctx, func() {}, fmt.Errorf("configure executor workload timeout: %w", err)
	}
	ctx, cancel := gateprivate.WithTimeout(workloadCtx, timeout+remoteWorkerSetupAllowance)
	return ctx, cancel, nil
}

func signalExitCode(caught os.Signal) int {
	signalValue, ok := caught.(syscall.Signal)
	if !ok {
		return 1
	}
	return 128 + int(signalValue)
}

// dispatchCLI 将固定命令面分派到已接线的 scheduler 边界。
func dispatchCLI(args []string, stdout io.Writer, progressWriters ...io.Writer) error {
	if len(args) == 0 {
		return protocolError("subcommand is required (test, codemap, capability-contract, project-map, worker, remote run)")
	}
	if handled, err := dispatchPrimaryCLI(args, stdout, progressWriters...); handled {
		return err
	}
	return protocolError("unknown subcommand %q", args[0])
}

// dispatchPrimaryCLI 分派 coordinator 命令空间之外的固定子命令。
func dispatchPrimaryCLI(args []string, stdout io.Writer, progressWriters ...io.Writer) (bool, error) {
	switch args[0] {
	case "test":
		return true, runTestInvocation(args[1:], stdout, progressWriters...)
	case "codemap", "capability-contract", "project-map":
		return true, runGeneratedMapCLI(args[0], args[1:], stdout)
	case "closure", "frontend-code-size":
		return true, runLocalGuardCLI(args, stdout)
	case "remote":
		return true, runRemote(args[1:], os.Stdin, stdout, progressWriters...)
	case "launcher":
		return true, runLauncherCLI(args[1:])
	case "_remote-materialize":
		return true, runRemoteMaterialize(args[1:], stdout)
	case "_remote-install-manifest":
		return true, runRemoteInstallManifest(args[1:], stdout)
	default:
		return false, nil
	}
}

// runGeneratedMapCLI 分派精确树绑定的生成物命令。
func runGeneratedMapCLI(command string, args []string, stdout io.Writer) error {
	if command == "codemap" {
		return runCodemapCLI(args, stdout)
	}
	if command == "capability-contract" {
		return runCapabilityContractCLI(args, stdout)
	}
	return runProjectMapCLI(args, stdout)
}
func protocolError(format string, args ...any) error {
	return gatecontract.WithExitCode(gatecontract.ExitProtocol, fmt.Errorf(format, args...))
}

func sourceError(format string, args ...any) error {
	return gatecontract.WithExitCode(gatecontract.ExitSourceMismatch, fmt.Errorf(format, args...))
}
