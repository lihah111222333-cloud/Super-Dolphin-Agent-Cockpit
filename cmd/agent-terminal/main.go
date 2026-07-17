// Package main 是桌面终端应用的入口，负责初始化运行环境并启动 Wails 桌面 UI。
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/app"
	recovery "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdaterecovery"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/pidregistry"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rlimit"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimeenv"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

type terminalDeps struct {
	selectStartup func(context.Context) (app.StartupSelection, error)
	runNormal     func(context.Context, app.StartupSelection) error
	runRecovery   func(context.Context, app.StartupSelection) error
}

type cooperativeTerminationServer interface {
	WaitForActivation(context.Context) error
	Close() error
}

type terminalMainDeps struct {
	prepareReleaseFilesystemHelper func() (func() error, error)
	prepareSchemaFilesystemWorker  func() (func() error, error)
	startTermination               func(context.CancelFunc) (cooperativeTerminationServer, bool, error)
	run                            func(context.Context, terminalDeps) error
	terminal                       terminalDeps
}

// main 在任何 normal preflight 前运行 early selector。
func main() {
	os.Exit(runAgentTerminalProcess())
}

// runAgentTerminalProcess 在 signal/UI 前依次分流 release 与 schema filesystem helper。
func runAgentTerminalProcess() int {
	if handled, err := recovery.RunReleaseFilesystemHelperIfRequested(os.Stdin, os.Stdout); handled {
		if err != nil {
			slog.Error("release filesystem helper failed", "error", err)
			return 2
		}
		return 0
	}
	if handled, err := app.RunSchemaFilesystemWorkerIfRequested(os.Stdin, os.Stdout); handled {
		if err != nil {
			slog.Error("schema filesystem worker failed", "error", err)
			return 2
		}
		return 0
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	return runMain(ctx, stop, productionTerminalMainDeps())
}

// runMain 返回退出码，确保最外层 os.Exit 前完成全部 cleanup defer。
func runMain(ctx context.Context, stop context.CancelFunc, deps terminalMainDeps) (exitCode int) {
	if err := validateTerminalMainDeps(ctx, stop, deps); err != nil {
		pkglogger.Get().Error("agent-terminal main dependencies are incomplete")
		return 1
	}
	defer stop()
	cleanupReleaseHelper, err := deps.prepareReleaseFilesystemHelper()
	if err != nil {
		pkglogger.Get().Error("agent-terminal release filesystem helper preparation failed", "error", err)
		return 1
	}
	defer func() { exitCode = cleanupFilesystemHelper("release", cleanupReleaseHelper, exitCode) }()
	cleanupSchemaWorker, err := deps.prepareSchemaFilesystemWorker()
	if err != nil {
		pkglogger.Get().Error("agent-terminal schema filesystem worker preparation failed", "error", err)
		return 1
	}
	defer func() { exitCode = cleanupFilesystemHelper("schema", cleanupSchemaWorker, exitCode) }()
	terminationServer, parked, err := deps.startTermination(stop)
	if err != nil {
		pkglogger.Get().Error("agent-terminal termination endpoint failed", "error", err)
		return 1
	}
	if terminationServer != nil {
		defer func() { exitCode = closeTerminationServer(terminationServer, exitCode) }()
	}
	return executeTerminalMain(ctx, deps, terminationServer, parked)
}

// validateTerminalMainDeps 在任何启动副作用前校验 early helper 与运行依赖完整。
func validateTerminalMainDeps(ctx context.Context, stop context.CancelFunc, deps terminalMainDeps) error {
	if ctx == nil || stop == nil || deps.prepareReleaseFilesystemHelper == nil ||
		deps.prepareSchemaFilesystemWorker == nil || deps.startTermination == nil || deps.run == nil {
		return errors.New("agent-terminal main dependencies are incomplete")
	}
	return nil
}

func cleanupFilesystemHelper(name string, cleanup func() error, exitCode int) int {
	if cleanup == nil {
		return 1
	}
	if err := cleanup(); err != nil {
		pkglogger.Get().Error("agent-terminal filesystem helper cleanup failed", "helper", name, "error", err)
		if exitCode == 0 {
			return 1
		}
	}
	return exitCode
}

func closeTerminationServer(server cooperativeTerminationServer, exitCode int) int {
	if err := server.Close(); err != nil {
		pkglogger.Get().Error("agent-terminal termination endpoint cleanup failed", "error", err)
		if exitCode == 0 {
			return 1
		}
	}
	return exitCode
}

// executeTerminalMain 执行 parked 激活等待或正常 terminal 主流程并返回退出码。
func executeTerminalMain(
	ctx context.Context,
	deps terminalMainDeps,
	terminationServer cooperativeTerminationServer,
	parked bool,
) int {
	if parked {
		if terminationServer == nil {
			pkglogger.Get().Error("agent-terminal parked startup requires termination server")
			return 1
		}
		if err := terminationServer.WaitForActivation(ctx); err != nil {
			if !errors.Is(err, context.Canceled) {
				pkglogger.Get().Error("agent-terminal rollback activation failed", "error", err)
				return 1
			}
			return 0
		}
	}
	if err := deps.run(ctx, deps.terminal); err != nil && !errors.Is(err, context.Canceled) {
		pkglogger.Get().Error("agent-terminal failed", "error", err)
		return 1
	}
	return 0
}

func productionTerminalMainDeps() terminalMainDeps {
	return terminalMainDeps{
		prepareReleaseFilesystemHelper: recovery.PrepareReleaseFilesystemHelper,
		prepareSchemaFilesystemWorker:  app.PrepareSchemaFilesystemWorker,
		startTermination: func(cancel context.CancelFunc) (cooperativeTerminationServer, bool, error) {
			return startCooperativeTermination(cancel)
		},
		run: runAgentTerminal, terminal: productionTerminalDeps(),
	}
}

// runAgentTerminal 严格按 selector 判定只运行 normal 或 Recovery graph。
func runAgentTerminal(ctx context.Context, deps terminalDeps) error {
	if deps.selectStartup == nil || deps.runNormal == nil || deps.runRecovery == nil {
		return errors.New("agent-terminal startup dependencies are required")
	}
	selection, err := deps.selectStartup(ctx)
	if err != nil && selection.Mode != app.StartupModeRecovery {
		return err
	}
	switch selection.Mode {
	case app.StartupModeNormal:
		if err := deps.runNormal(ctx, selection); err != nil {
			if selection.HasActiveProbation() {
				return err
			}
			selection.Mode = app.StartupModeRecovery
			selection.Projection.Reason = err.Error()
			return deps.runRecovery(ctx, selection)
		}
		return nil
	case app.StartupModeRecovery:
		return errors.Join(err, deps.runRecovery(ctx, selection))
	default:
		return errors.New("agent-terminal selector returned invalid startup mode")
	}
}

func productionTerminalDeps() terminalDeps {
	return terminalDeps{
		selectStartup: selectStartup,
		runNormal:     runNormalDesktop,
		runRecovery:   runRecoveryRuntime,
	}
}

// startCooperativeTermination 根据 rollback token 选择普通或 parked 认证端点。
func startCooperativeTermination(cancel context.CancelFunc) (*pidregistry.CooperativeTerminationServer, bool, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, false, err
	}
	launch, err := runtimeenv.ResolveRecoveryLaunch(executable, os.Environ())
	if err != nil {
		return nil, false, err
	}
	parked, err := rollbackStartupIsParked(os.Args[1:], launch)
	if err != nil {
		return nil, false, err
	}
	if !launch.ContractPresent {
		return nil, false, nil
	}
	start := pidregistry.StartCooperativeTerminationServer
	if parked {
		start = pidregistry.StartParkedCooperativeTerminationServer
	}
	server, err := start(
		launch.TerminationEndpoint,
		launch.TerminationToken,
		cancel,
	)
	return server, parked, err
}

// rollbackStartupIsParked 要求命令行 durable token 与 frozen contract 完全一致。
func rollbackStartupIsParked(args []string, launch runtimeenv.RecoveryLaunch) (bool, error) {
	const prefix = "--super-dolphin-rollback-launch-token="
	var token string
	for _, argument := range args {
		value, found := strings.CutPrefix(argument, prefix)
		if !found {
			continue
		}
		if token != "" || value == "" {
			return false, errors.New("rollback startup launch token argument is invalid")
		}
		token = value
	}
	if token == "" {
		return false, nil
	}
	if !launch.ContractPresent || token != launch.TerminationToken {
		return false, fmt.Errorf("rollback startup token does not match frozen launch contract")
	}
	return true, nil
}

// selectStartup 只读取 frozen launch contract 与 Task 1 transaction journal。
func selectStartup(ctx context.Context) (app.StartupSelection, error) {
	executable, err := os.Executable()
	if err != nil {
		return app.StartupSelection{}, err
	}
	launch, err := runtimeenv.ResolveRecoveryLaunch(executable, os.Environ())
	if err != nil {
		return app.StartupSelection{}, err
	}
	store, available, err := recoveryStoreForLaunch(launch)
	if err != nil {
		return app.StartupSelection{}, err
	}
	if !available {
		return app.StartupSelection{Mode: app.StartupModeNormal}, nil
	}
	stable, err := pidregistry.CaptureStableProcessIdentity(os.Getpid())
	if err != nil {
		return recoveryStartupFailure(store, err), err
	}
	if launch.ContractPresent && stable.ExecutableIdentity != launch.ExecutableIdentity {
		err = pidregistry.ErrStableProcessIdentityMismatch
		return recoveryStartupFailure(store, err), err
	}
	return app.SelectStartup(ctx, app.StartupSelectorInput{
		Store: store,
		Process: recovery.ProcessIdentity{
			PID: stable.PID, StartToken: stable.ProcessStartToken,
			ExecutableIdentity: stable.ExecutableIdentity, ExecutableSHA256: launch.ExecutableSHA256,
			TerminationEndpoint: launch.TerminationEndpoint, TerminationToken: launch.TerminationToken,
		},
		ExpectedTransactionID: recovery.TransactionID(launch.TransactionID),
		LeaseWait:             2 * time.Second,
	})
}

// recoveryStoreForLaunch 区分无 journal 的 normal 启动与缺失 probation root 的 fail-fast 错误。
func recoveryStoreForLaunch(launch runtimeenv.RecoveryLaunch) (*recovery.Store, bool, error) {
	if launch.TransactionRoot == "" {
		return nil, false, nil
	}
	if _, err := os.Stat(launch.TransactionRoot); err != nil {
		if errors.Is(err, os.ErrNotExist) && !launch.ContractPresent {
			return nil, false, nil
		}
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, errors.New("probation transaction root is missing")
		}
		return nil, false, err
	}
	store, err := recovery.NewStore(launch.TransactionRoot)
	return store, err == nil, err
}

func recoveryStartupFailure(store *recovery.Store, err error) app.StartupSelection {
	return app.StartupSelection{
		Mode: app.StartupModeRecovery, Store: store,
		Projection: app.RecoveryProjection{Reason: err.Error()},
	}
}

// runNormalDesktop 仅在 selector 明确返回 normal 后执行原有全部 preflight。
func runNormalDesktop(ctx context.Context, selection app.StartupSelection) error {
	rlimit.Init()
	if err := os.Setenv("SUPER_DOLPHIN_PROCESS_ROLE", "desktop"); err != nil {
		return err
	}
	if err := runtimeenv.ConfigurePackagedApp(); err != nil {
		return err
	}
	if err := runtimeenv.LoadVideoEnv(); err != nil {
		return err
	}
	frontendFS, err := frontendDistFS()
	if err != nil {
		return err
	}
	return app.RunDesktop(ctx, frontendFS, func(readyCtx context.Context, publish app.DesktopACKPublisher) error {
		return publish(func() error {
			return selection.RecordReadyACK(readyCtx, time.Now())
		})
	})
}

// runRecoveryRuntime 只构造 frozen Recovery graph 与独立 Wails recovery surface。
func runRecoveryRuntime(ctx context.Context, selection app.StartupSelection) error {
	runtime, err := app.NewRecoveryRuntime(selection)
	if err != nil {
		return err
	}
	frontendFS, err := frontendDistFS()
	if err != nil {
		return err
	}
	return runtime.Run(ctx, newRecoveryDesktopSurface(frontendFS))
}
