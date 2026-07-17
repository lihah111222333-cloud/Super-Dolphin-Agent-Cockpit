// Package main 是桌面终端应用的入口，负责初始化运行环境并启动 Wails 桌面 UI。
package main

import (
	"context"
	"errors"
	"fmt"
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

// main 在任何 normal preflight 前运行 early selector。
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	terminationServer, parked, err := startCooperativeTermination(stop)
	if err != nil {
		pkglogger.Get().Error("agent-terminal termination endpoint failed", "error", err)
		os.Exit(1)
	}
	if terminationServer != nil {
		defer terminationServer.Close()
	}
	if parked {
		if err := terminationServer.WaitForCommit(ctx); err != nil {
			pkglogger.Get().Error("agent-terminal rollback commit failed", "error", err)
			return
		}
	}
	if err := runAgentTerminal(ctx, productionTerminalDeps()); err != nil && !errors.Is(err, context.Canceled) {
		pkglogger.Get().Error("agent-terminal failed", "error", err)
		os.Exit(1)
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
