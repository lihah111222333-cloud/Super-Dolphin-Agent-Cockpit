package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"sync/atomic"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/app"
	recovery "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdaterecovery"
	platformrunner "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runner"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type recoveryActionAvailability struct {
	Check   bool `json:"check"`
	Retry   bool `json:"retry"`
	Restore bool `json:"restore"`
}

type recoverySurfaceState struct {
	Mode       app.StartupMode            `json:"mode"`
	Projection app.RecoveryProjection     `json:"projection"`
	LastAction string                     `json:"last_action"`
	Actions    recoveryActionAvailability `json:"actions"`
}

type recoveryBinding struct {
	runtime *app.RecoveryRuntime
	effects recoveryEffects
}

type recoveryEffects struct {
	Quit func()
}

type recoveryWireFailure struct {
	code         app.RecoveryPublicErrorCode
	diagnosticID string
}

// Error 只返回 Wails 可消费的安全 wire token，绝不串联原始底层错误。
func (failure recoveryWireFailure) Error() string {
	return string(failure.code) + "|" + failure.diagnosticID
}

// recoveryPublicFailure 是 Wails 的错误出口；日志和 wire 都只保留审核过的公开错误字段。
func recoveryPublicFailure(code app.RecoveryPublicErrorCode, cause error) error {
	return recoveryPublicFailureWithLogger(slog.Default(), code, cause)
}

// recoveryPublicFailureWithLogger 使用指定 logger 记录公开字段，供边界测试验证日志不含原始错误。
func recoveryPublicFailureWithLogger(logger *slog.Logger, code app.RecoveryPublicErrorCode, cause error) error {
	failure, err := app.NewRecoveryPublicFailure(code, cause)
	if err != nil {
		failure = app.RecoveryFallbackFailure()
	}
	logger.Error(
		"Recovery Wails operation failed",
		"recovery_error_code", failure.Code,
		"public_message", failure.PublicMessage,
		"diagnostic_id", failure.DiagnosticID,
	)
	return recoveryWireFailure{code: failure.Code, diagnosticID: failure.DiagnosticID}
}

type recoveryRestoreOps struct {
	Restore    func(context.Context) (recovery.Transaction, error)
	Projection func(context.Context) (app.RecoveryProjection, error)
	Quit       func()
}

type recoveryApplication interface {
	Run() error
	Quit()
}

type recoveryRunActor struct {
	application recoveryApplication
	done        *atomic.Bool
	result      chan<- error
}

// Run 阻塞运行 Recovery 窗口，并在返回前发布完成状态。
func (actor recoveryRunActor) Run(context.Context) error {
	err := actor.application.Run()
	actor.done.Store(true)
	actor.result <- err
	return err
}

type recoveryQuitActor struct {
	application recoveryApplication
	done        *atomic.Bool
}

// Run 等待生命周期取消，仅在窗口仍运行时请求一次退出。
func (actor recoveryQuitActor) Run(ctx context.Context) error {
	<-ctx.Done()
	if !actor.done.Load() {
		actor.application.Quit()
	}
	return nil
}

// State 返回 Recovery mode 与 exact journal 的 typed projection。
func (binding *recoveryBinding) State(ctx context.Context) (recoverySurfaceState, error) {
	state, err := binding.stateAfter(ctx, "state")
	if err != nil {
		return recoverySurfaceState{}, recoveryPublicFailure(app.RecoveryPublicCodeStateFailed, err)
	}
	return state, nil
}

// Check 校验 current candidate 后返回刷新后的 Recovery state。
func (binding *recoveryBinding) Check(ctx context.Context) (recoverySurfaceState, error) {
	runtime, err := binding.requireAvailableAction(ctx, "check")
	if err != nil {
		return recoverySurfaceState{}, recoveryPublicFailure(app.RecoveryPublicCodeCheckFailed, err)
	}
	if err := runtime.Check.Check(ctx); err != nil {
		return recoverySurfaceState{}, recoveryPublicFailure(app.RecoveryPublicCodeCheckFailed, err)
	}
	state, err := binding.stateAfter(ctx, "check")
	if err != nil {
		return recoverySurfaceState{}, recoveryPublicFailure(app.RecoveryPublicCodeCheckFailed, err)
	}
	return state, nil
}

// Retry 重放 exact journal intent 后返回刷新后的 Recovery state。
func (binding *recoveryBinding) Retry(ctx context.Context) (recoverySurfaceState, error) {
	runtime, err := binding.requireAvailableAction(ctx, "retry")
	if err != nil {
		return recoverySurfaceState{}, recoveryPublicFailure(app.RecoveryPublicCodeRetryFailed, err)
	}
	if _, err := runtime.Retry.Retry(ctx); err != nil {
		return recoverySurfaceState{}, recoveryPublicFailure(app.RecoveryPublicCodeRetryFailed, err)
	}
	state, err := binding.stateAfter(ctx, "retry")
	if err != nil {
		return recoverySurfaceState{}, recoveryPublicFailure(app.RecoveryPublicCodeRetryFailed, err)
	}
	return state, nil
}

// Restore 使用 current lease 显式恢复旧 release，并返回刷新后的 state。
func (binding *recoveryBinding) Restore(ctx context.Context) (recoverySurfaceState, error) {
	runtime, err := binding.requireAvailableAction(ctx, "restore")
	if err != nil {
		return recoverySurfaceState{}, recoveryPublicFailure(app.RecoveryPublicCodeRestoreFailed, err)
	}
	state, err := completeRecoveryRestore(ctx, recoveryRestoreOps{
		Restore: runtime.Restore.Restore, Projection: runtime.CurrentProjection, Quit: binding.effects.Quit,
	})
	if err != nil {
		return recoverySurfaceState{}, recoveryPublicFailure(app.RecoveryPublicCodeRestoreFailed, err)
	}
	return state, nil
}

// completeRecoveryRestore 等待 transaction-owned rollback/restart 收敛成功后刷新状态并退出 Recovery。
func completeRecoveryRestore(ctx context.Context, ops recoveryRestoreOps) (recoverySurfaceState, error) {
	if ops.Restore == nil || ops.Projection == nil || ops.Quit == nil {
		return recoverySurfaceState{}, errors.New("Recovery restore effects are required")
	}
	_, err := ops.Restore(ctx)
	if err != nil {
		return recoverySurfaceState{}, err
	}
	defer ops.Quit()
	projection, err := ops.Projection(ctx)
	if err != nil {
		return recoverySurfaceState{}, err
	}
	state := newRecoverySurfaceState(projection, "restore")
	return state, nil
}

func (binding *recoveryBinding) stateAfter(ctx context.Context, action string) (recoverySurfaceState, error) {
	runtime, err := binding.recoveryRuntime()
	if err != nil {
		return recoverySurfaceState{}, err
	}
	projection, err := runtime.CurrentProjection(ctx)
	if err != nil {
		return recoverySurfaceState{}, err
	}
	return newRecoverySurfaceState(projection, action), nil
}

func newRecoverySurfaceState(projection app.RecoveryProjection, action string) recoverySurfaceState {
	projection.Reason = app.NormalizeRecoveryReason(projection.Reason)
	actions := recoveryActionsFor(projection)
	return recoverySurfaceState{
		Mode: app.StartupModeRecovery, Projection: projection, LastAction: action,
		Actions: actions,
	}
}

// recoveryActionsFor 按持久 journal 状态和 exact lease fail-closed 计算可用动作。
func recoveryActionsFor(projection app.RecoveryProjection) recoveryActionAvailability {
	hasIdentity := projection.TransactionID != "" && projection.AttemptID != ""
	if !hasIdentity {
		return recoveryActionAvailability{}
	}
	actions := recoveryActionAvailability{}
	switch projection.State {
	case recovery.StatePrepared, recovery.StateBackupRetained, recovery.StateInstallPending:
		actions.Restore = true
	case recovery.StateBackupPending, recovery.StateRollbackPending:
		actions.Retry = true
		actions.Restore = true
	case recovery.StateRolledBack:
		actions.Restore = true
	case recovery.StateCommitPending:
		actions.Check = projection.CandidateSHA256 != ""
		actions.Retry = true
	case recovery.StateProbation:
		actions.Check = projection.CandidateSHA256 != ""
		actions.Restore = !projection.LeasePresent || projection.LeaseOwner != "" && projection.LeaseGeneration > 0
	}
	return actions
}

func (binding *recoveryBinding) requireAvailableAction(ctx context.Context, action string) (*app.RecoveryRuntime, error) {
	runtime, err := binding.recoveryRuntime()
	if err != nil {
		return nil, err
	}
	projection, err := runtime.CurrentProjection(ctx)
	if err != nil {
		return nil, err
	}
	available := recoveryActionsFor(projection)
	allowed := map[string]bool{"check": available.Check, "retry": available.Retry, "restore": available.Restore}
	if enabled, known := allowed[action]; !known || !enabled {
		return nil, fmt.Errorf("Recovery action %q is unavailable for transaction state %q", action, projection.State)
	}
	return runtime, nil
}

func (binding *recoveryBinding) recoveryRuntime() (*app.RecoveryRuntime, error) {
	if binding == nil || binding.runtime == nil {
		return nil, errors.New("Recovery binding runtime is required")
	}
	return binding.runtime, nil
}

type recoveryDesktopSurface struct {
	frontend fs.FS
}

func newRecoveryDesktopSurface(frontend fs.FS) *recoveryDesktopSurface {
	return &recoveryDesktopSurface{frontend: frontend}
}

// Run 创建只绑定四个 Recovery action 的 Wails application，并阻塞到窗口退出。
func (surface *recoveryDesktopSurface) Run(ctx context.Context, runtime *app.RecoveryRuntime) error {
	if surface == nil || surface.frontend == nil || runtime == nil {
		return errors.New("Recovery desktop surface requires frontend assets and runtime")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	binding := &recoveryBinding{runtime: runtime}
	wailsApp := application.New(application.Options{
		Name: "Super Dolphin Recovery", Description: "Super Dolphin Recovery",
		Services: []application.Service{application.NewService(binding)},
		Assets:   application.AssetOptions{Handler: http.FileServer(http.FS(surface.frontend))},
		Mac:      application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
	})
	binding.effects.Quit = wailsApp.Quit
	wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "recovery", Title: "Super Dolphin Recovery", URL: "/recovery.html",
		Width: 760, Height: 620, MinWidth: 640, MinHeight: 520,
		BackgroundColour: application.NewRGB(17, 24, 39),
	})
	return runRecoveryApplication(ctx, wailsApp)
}

// runRecoveryApplication 用 RunGroup 连接 Wails 阻塞生命周期与上层 context，并等待全部 actor 退出。
func runRecoveryApplication(ctx context.Context, wailsApp recoveryApplication) error {
	if ctx == nil || wailsApp == nil {
		return errors.New("Recovery application lifecycle requires context and application")
	}
	done := &atomic.Bool{}
	result := make(chan error, 1)
	groupErr := platformrunner.RunGroup(ctx, []platformrunner.Runner{
		recoveryRunActor{application: wailsApp, done: done, result: result},
		recoveryQuitActor{application: wailsApp, done: done},
	}, platformrunner.GroupOptions{})
	select {
	case runErr := <-result:
		if runErr != nil {
			return runErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return nil
	default:
		return groupErr
	}
}
