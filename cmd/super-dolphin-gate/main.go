// Package main provides the standalone Super Dolphin gate coordinator and worker CLI.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
)

const remoteWorkerSetupAllowance = 2 * time.Minute

var (
	errSchedulerNotWired = gatecontract.WithExitCode(gatecontract.ExitInfrastructure, errors.New("scheduler client not wired"))
	gateSourceDigest     string
	gateToolchainDigest  string
)

func main() {
	if productionBootstrapRunnerProgram(os.Args[0]) {
		if err := runProductionBootstrapRunnerCLI(os.Args[1:], os.Stdout); err != nil {
			_ = writeCLIError(os.Stderr, err)
			os.Exit(int(gatecontract.ExitCodeOf(err)))
		}
		return
	}
	if isProductionSelfUpdateCommand(os.Args[1:]) {
		os.Exit(runProductionSelfUpdateCLI(os.Args[2:], os.Stderr))
	}
	os.Exit(runCLI(os.Args[1:], os.Stdout, os.Stderr))
}

func runCLI(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "worker" {
		return runWorkerCLI(args[1:], stdout, stderr)
	}
	err := dispatchCLI(args, stdout)
	if err == nil {
		return int(gatecontract.ExitOK)
	}
	exitCode := gatecontract.ExitCodeOf(err)
	if writeErr := writeCLIError(stderr, err); writeErr != nil {
		return int(gatecontract.ExitInfrastructure)
	}
	return int(exitCode)
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
	case "race-package-patterns":
		if err := writeRacePackagePatterns(args[1:], stdout); err != nil {
			_ = writeCLIError(stderr, err)
			return true, int(gatecontract.ExitCodeOf(err))
		}
		return true, int(gatecontract.ExitOK)
	default:
		return false, 0
	}
}

// writeRacePackagePatterns 输出 cache seed 与实际 race gate 共用的包注册表。
func writeRacePackagePatterns(args []string, output io.Writer) error {
	if len(args) != 0 {
		return gatecontract.WithExitCode(gatecontract.ExitProtocol, errors.New("race-package-patterns does not accept arguments"))
	}
	for _, pattern := range gatecontract.RaceSensitivePackagePatterns() {
		if _, err := fmt.Fprintln(output, pattern); err != nil {
			return gatecontract.WithExitCode(gatecontract.ExitInfrastructure, fmt.Errorf("write race package patterns: %w", err))
		}
	}
	return nil
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
	if gateSourceDigest == "" || gateToolchainDigest == "" {
		return gatecontract.WithExitCode(gatecontract.ExitInfrastructure, errors.New("gate CLI build identity is not linked"))
	}
	if _, err := fmt.Fprintf(stdout, "gate_source_sha256=%s\nplatform=%s/%s\ntoolchain_digest=%s\n", gateSourceDigest, runtime.GOOS, runtime.GOARCH, gateToolchainDigest); err != nil {
		return gatecontract.WithExitCode(gatecontract.ExitInfrastructure, fmt.Errorf("write gate CLI identity: %w", err))
	}
	return nil
}

// workerExecutionContext 为缓存准备保留独立租约，并把实际 workload 时限延迟到 executor 内启动。
func workerExecutionContext(parent context.Context) (context.Context, context.CancelFunc, error) {
	raw, configured := os.LookupEnv(gatecontract.ExecutorWorkloadTimeoutEnvironment)
	if !configured {
		return parent, func() {}, nil
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

// dispatchCLI 将固定命令面分派到 plan 或未接线 scheduler 边界。
func dispatchCLI(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return protocolError("subcommand is required (plan, test, codemap, project-map, submit, workflow, workflow-host, run, worker, remote run, status, wait, logs, receipt verify, grant, provision)")
	}
	if handled, err := dispatchPrimaryCLI(args, stdout); handled {
		return err
	}
	return dispatchCoordinatorCLI(args, stdout)
}

// dispatchPrimaryCLI 分派 coordinator 命令空间之外的固定子命令。
func dispatchPrimaryCLI(args []string, stdout io.Writer) (bool, error) {
	switch args[0] {
	case "plan":
		return true, runPlan(args[1:], stdout)
	case "test":
		return true, runTestInvocation(args[1:], stdout)
	case "codemap", "project-map":
		return true, runGeneratedMapCLI(args[0], args[1:], stdout)
	case "hook":
		return true, runHook(args[1:], os.Stdin, stdout)
	case "bootstrap":
		return true, runProductionBootstrapControllerCLI(args[1:], os.Stdin, stdout)
	case "provision":
		return true, runProductionProvisionCLI(args[1:], stdout)
	case "closure", "frontend-code-size":
		return true, runLocalGuardCLI(args, stdout)
	case "remote":
		return true, runRemote(args[1:], os.Stdin, stdout)
	case "_remote-materialize":
		return true, runRemoteMaterialize(args[1:], stdout)
	case "_remote-build-test-binaries":
		return true, runRemoteBuildTestBinaries(args[1:], stdout)
	case "_remote-build-oci-baseline":
		return true, runRemoteBuildOCIBaseline(args[1:], stdout)
	default:
		return false, nil
	}
}

// runGeneratedMapCLI 分派两个精确树绑定的生成地图命令。
func runGeneratedMapCLI(command string, args []string, stdout io.Writer) error {
	if command == "codemap" {
		return runCodemapCLI(args, stdout)
	}
	return runProjectMapCLI(args, stdout)
}

// dispatchCoordinatorCLI 分派既有 coordinator 与 receipt 命令。
func dispatchCoordinatorCLI(args []string, stdout io.Writer) error {
	switch args[0] {
	case "submit":
		return runSubmit(args[1:], stdout)
	case "_production-launcher":
		return runProductionLauncherCLI(args[1:], stdout)
	case "workflow":
		return runWorkflow(args[1:], stdout)
	case "workflow-host":
		return runWorkflowHost(args[1:], stdout)
	default:
		return dispatchCoordinatorOperationsCLI(args, stdout)
	}
}

// dispatchCoordinatorOperationsCLI 分派运行、查询、日志、owner、receipt 与 grant 子命令，并保留未知命令的协议错误。
func dispatchCoordinatorOperationsCLI(args []string, stdout io.Writer) error {
	switch args[0] {
	case "run":
		if _, err := parseRequiredFlag("run", "job-token", args[1:]); err != nil {
			return err
		}
		return errSchedulerNotWired
	case "status":
		return runStatus(args[1:], stdout)
	case "wait":
		return runWait(args[1:], stdout)
	case "logs":
		return runLogs(args[1:], stdout)
	case "_owner":
		return runOwnerProcess(args[1:], stdout)
	case "receipt":
		return runReceipt(args[1:])
	case "grant":
		return runGrant(args[1:], stdout)
	case "requester":
		return runRequesterCLI(args[1:], stdout)
	default:
		return protocolError("unknown subcommand %q", args[0])
	}
}

func runPlan(args []string, stdout io.Writer) error {
	plan, err := parsePlan(args)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(plan); err != nil {
		return gatecontract.WithExitCode(gatecontract.ExitInfrastructure, fmt.Errorf("encode plan JSON: %w", err))
	}
	return nil
}

func runReceipt(args []string) error {
	if len(args) == 0 || args[0] != "verify" {
		return protocolError("receipt subcommand must be verify")
	}
	if _, err := parseRequiredFlag("receipt verify", "input", args[1:]); err != nil {
		return err
	}
	return errSchedulerNotWired
}

type planFlags struct {
	profile        string
	objectFormat   string
	sourceTree     string
	commit         string
	tree           string
	parent         string
	baseKind       string
	base           string
	head           string
	localRef       string
	remoteRef      string
	observedRemote string
	updateKind     string
}

// parsePlan 严格解析 profile 与唯一 SourceSpec，并生成 canonical plan。
func parsePlan(args []string) (gatecontract.GatePlan, error) {
	options := planFlags{}
	flags := flag.NewFlagSet("plan", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	registerPlanFlags(flags, &options)
	if err := flags.Parse(args); err != nil {
		return gatecontract.GatePlan{}, protocolError("parse plan flags: %v", err)
	}
	if flags.NArg() != 0 {
		return gatecontract.GatePlan{}, protocolError("unexpected positional arguments: %v", flags.Args())
	}
	profile := gatecontract.Profile(options.profile)
	if err := profile.Validate(); err != nil {
		return gatecontract.GatePlan{}, protocolError("%v", err)
	}
	source, err := options.sourceSpec()
	if err != nil {
		return gatecontract.GatePlan{}, err
	}
	plan, err := gatecontract.BuildGatePlan(profile, source)
	if err != nil {
		return gatecontract.GatePlan{}, gatecontract.WithExitCode(gatecontract.ExitRegistryInvariant, err)
	}
	return plan, nil
}

func registerPlanFlags(flags *flag.FlagSet, options *planFlags) {
	flags.StringVar(&options.profile, "profile", "", "gate profile")
	flags.StringVar(&options.objectFormat, "object-format", "", "Git object format")
	flags.StringVar(&options.sourceTree, "source-tree", "", "normalized source tree OID")
	flags.StringVar(&options.commit, "commit", "", "commit OID")
	flags.StringVar(&options.tree, "tree", "", "tree OID")
	flags.StringVar(&options.parent, "parent", "", "optional tree parent commit OID")
	flags.StringVar(&options.baseKind, "base-kind", "", "range base kind")
	flags.StringVar(&options.base, "base", "", "range base commit OID")
	flags.StringVar(&options.head, "head", "", "range head commit OID")
	flags.StringVar(&options.localRef, "local-ref", "", "range local ref")
	flags.StringVar(&options.remoteRef, "remote-ref", "", "range remote ref")
	flags.StringVar(&options.observedRemote, "observed-remote", "", "observed remote OID")
	flags.StringVar(&options.updateKind, "update-kind", "", "range update kind")
}

// sourceSpec 将互斥 CLI flags 转为严格 SourceSpec。
func (o planFlags) sourceSpec() (gatecontract.SourceSpec, error) {
	commitSet := o.commit != ""
	treeSet := o.tree != "" || o.parent != ""
	rangeSet := o.rangeRequested()
	if boolCount(commitSet, treeSet, rangeSet) != 1 {
		return gatecontract.SourceSpec{}, sourceError("exactly one commit, tree, or range source is required")
	}
	spec := gatecontract.SourceSpec{ObjectFormat: gatecontract.GitObjectFormat(o.objectFormat), SourceTreeSHA: o.sourceTree}
	switch {
	case commitSet:
		spec.Kind = gatecontract.SourceKindCommit
		spec.Commit = &gatecontract.CommitSource{SHA: o.commit}
	case treeSet:
		spec.Kind = gatecontract.SourceKindTree
		spec.Tree = &gatecontract.TreeSource{SHA: o.tree, ParentCommitSHA: o.parent}
	case rangeSet:
		spec.Kind = gatecontract.SourceKindRange
		spec.Range = &gatecontract.RangeSource{
			BaseKind: gatecontract.BaseKind(o.baseKind), BaseSHA: o.base, HeadSHA: o.head,
			LocalRef: o.localRef, RemoteRef: o.remoteRef, ObservedRemoteSHA: o.observedRemote,
			UpdateKind: gatecontract.UpdateKind(o.updateKind),
		}
	}
	if err := spec.Validate(); err != nil {
		return gatecontract.SourceSpec{}, sourceError("%v", err)
	}
	return spec, nil
}

func (o planFlags) rangeRequested() bool {
	values := []string{o.baseKind, o.base, o.head, o.localRef, o.remoteRef, o.observedRemote, o.updateKind}
	for _, value := range values {
		if value != "" {
			return true
		}
	}
	return false
}

func parseRequiredFlag(command, name string, args []string) (string, error) {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	value := flags.String(name, "", name)
	if err := flags.Parse(args); err != nil {
		return "", protocolError("parse %s flags: %v", command, err)
	}
	if flags.NArg() != 0 {
		return "", protocolError("unexpected positional arguments: %v", flags.Args())
	}
	if strings.TrimSpace(*value) == "" {
		return "", protocolError("--%s is required", name)
	}
	return *value, nil
}

func boolCount(values ...bool) int {
	count := 0
	for _, value := range values {
		if value {
			count++
		}
	}
	return count
}

func protocolError(format string, args ...any) error {
	return gatecontract.WithExitCode(gatecontract.ExitProtocol, fmt.Errorf(format, args...))
}

func sourceError(format string, args ...any) error {
	return gatecontract.WithExitCode(gatecontract.ExitSourceMismatch, fmt.Errorf(format, args...))
}
