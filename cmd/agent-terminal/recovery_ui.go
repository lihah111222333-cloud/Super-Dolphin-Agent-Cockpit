package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"sync"
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
	Failure    app.RecoveryFailure        `json:"failure"`
}

type recoveryBinding struct {
	runtime  *app.RecoveryRuntime
	effects  recoveryEffects
	failure  atomic.Pointer[app.RecoveryFailure]
	actionMu sync.Mutex
}

type recoveryEffects struct {
	Quit func()
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
	binding.actionMu.Lock()
	defer binding.actionMu.Unlock()
	state, err := binding.stateAfter(ctx, "state")
	return state, recoveryWailsError(err)
}

// Check 校验 current candidate 后返回刷新后的 Recovery state。
func (binding *recoveryBinding) Check(ctx context.Context) (recoverySurfaceState, error) {
	binding.actionMu.Lock()
	defer binding.actionMu.Unlock()
	runtime, err := binding.requireAvailableAction(ctx, "check")
	if err != nil {
		return recoverySurfaceState{}, recoveryWailsError(err)
	}
	if err := runtime.Check.Check(ctx); err != nil {
		return recoverySurfaceState{}, recoveryWailsError(err)
	}
	binding.failure.Store(nil)
	runtime.ClearFailure()
	state, err := binding.stateAfter(ctx, "check")
	return state, recoveryWailsError(err)
}

// Retry 重放 exact journal intent 后返回刷新后的 Recovery state。
func (binding *recoveryBinding) Retry(ctx context.Context) (recoverySurfaceState, error) {
	binding.actionMu.Lock()
	defer binding.actionMu.Unlock()
	runtime, err := binding.requireAvailableAction(ctx, "retry")
	if err != nil {
		return recoverySurfaceState{}, recoveryWailsError(err)
	}
	if _, err := runtime.Retry.Retry(ctx); err != nil {
		projection, projectionErr := runtime.CurrentProjection(ctx)
		if projectionErr != nil {
			return recoverySurfaceState{}, recoveryWailsError(projectionErr)
		}
		failure := app.RecoveryFailureForError(err, projection.TransactionID)
		if failure.Code != "" {
			binding.failure.Store(&failure)
		}
		return recoverySurfaceState{}, recoveryWailsError(err)
	}
	binding.failure.Store(nil)
	runtime.ClearFailure()
	state, err := binding.stateAfter(ctx, "retry")
	return state, recoveryWailsError(err)
}

// Restore 使用 current lease 显式恢复旧 release，并返回刷新后的 state。
func (binding *recoveryBinding) Restore(ctx context.Context) (recoverySurfaceState, error) {
	binding.actionMu.Lock()
	defer binding.actionMu.Unlock()
	runtime, err := binding.requireAvailableAction(ctx, "restore")
	if err != nil {
		return recoverySurfaceState{}, recoveryWailsError(err)
	}
	state, err := completeRecoveryRestore(ctx, recoveryRestoreOps{
		Restore: runtime.Restore.Restore, Projection: runtime.CurrentProjection, Quit: binding.effects.Quit,
	})
	if err == nil {
		binding.failure.Store(nil)
		runtime.ClearFailure()
	}
	return state, recoveryWailsError(err)
}

func recoveryWailsError(err error) error {
	if err == nil {
		return nil
	}
	return errors.New("RECOVERY_OPERATION_FAILED")
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

// stateAfter 刷新 exact journal，并仅合并同一 transaction 的内存失败元数据。
func (binding *recoveryBinding) stateAfter(ctx context.Context, action string) (recoverySurfaceState, error) {
	runtime, err := binding.recoveryRuntime()
	if err != nil {
		return recoverySurfaceState{}, err
	}
	projection, err := runtime.CurrentProjection(ctx)
	if err != nil {
		return recoverySurfaceState{}, err
	}
	if failure := binding.failure.Load(); failure != nil {
		if failure.TransactionID == "" || failure.TransactionID == string(projection.TransactionID) {
			return newRecoverySurfaceStateWithFailure(projection, action, *failure), nil
		}
		binding.failure.Store(nil)
	}
	return newRecoverySurfaceStateWithFailure(projection, action, runtime.CurrentFailure()), nil
}

func newRecoverySurfaceState(projection app.RecoveryProjection, action string) recoverySurfaceState {
	return newRecoverySurfaceStateWithFailure(projection, action, app.RecoveryFailure{})
}

func newRecoverySurfaceStateWithFailure(projection app.RecoveryProjection, action string, failure app.RecoveryFailure) recoverySurfaceState {
	actions := recoveryActionsFor(projection)
	projection.Reason = "Recovery action is required; sensitive diagnostics remain preserved internally."
	if failure.Code != "" {
		failure.TransactionID = string(projection.TransactionID)
		projection.Reason = app.RecoveryReasonForFailure(failure.Code)
		switch failure.Action {
		case app.RecoveryActionWaitThenRetry:
			actions = recoveryActionAvailability{Retry: failure.Retryable && actions.Retry}
		case app.RecoveryActionRestartApplication, app.RecoveryActionPreserveStateExportDiagnostics:
			actions = recoveryActionAvailability{}
		default:
			actions = recoveryActionAvailability{}
		}
	}
	return recoverySurfaceState{
		Mode: app.StartupModeRecovery, Projection: projection, LastAction: action,
		Actions: actions, Failure: failure,
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
