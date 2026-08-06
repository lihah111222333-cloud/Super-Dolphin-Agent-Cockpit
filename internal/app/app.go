package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"

	"go.uber.org/fx"

	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"

	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/codexapp"
	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
	uiwails "github.com/lihah111222333-cloud/super-dolphin-agent/internal/ui/wails"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// appDeps 抽出桌面 preflight 依赖，便于测试替换外部 Codex bootstrap。
type appDeps struct {
	ensureCodexBootstrap func(context.Context, codexapp.CodexBootstrapConfig) error
	codexAppManagedHome  func() (string, error)
}

// newAppDeps 创建单个桌面启动流程拥有的 preflight 依赖描述符。
func newAppDeps() appDeps {
	return appDeps{
		ensureCodexBootstrap: codexapp.EnsureCodexBootstrap,
		codexAppManagedHome: func() (string, error) {
			return providershared.AppManagedProviderHome(providershared.ProviderCodex)
		},
	}
}

// packaged Codex relay 环境变量。
const (
	codexRelayBaseURLEnv          = "SUPER_DOLPHIN_CODEX_RELAY_BASE_URL"
	codexRelayBootstrapTokenEnv   = "SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN"
	codexRelayPrivilegedAPIKeyEnv = "SUPER_DOLPHIN_CODEX_RELAY_API_KEY"
)

// loggerResult 显式发布日志 runtime owner 和当前日志器，供 Fx 图分别注入。
type loggerResult struct {
	fx.Out

	Runtime *pkglogger.Runtime
	Logger  *slog.Logger
}

// NewLogger 初始化应用日志器并记录构建信息。
func NewLogger() loggerResult {
	info := currentBuildInfo()
	logRuntime := pkglogger.NewRuntime(pkglogger.RuntimeConfig{
		ServiceVersion: info.Version,
	})
	logRuntime.ConfigureServiceFromEnv(info.Version)
	logRuntime.Init(os.Getenv("LOG_LEVEL"))

	homeDir, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	logDir, projectName := pkglogger.ResolveProjectLogDir(homeDir, cwd)
	if err := logRuntime.InitWithFile(logDir); err != nil {
		logRuntime.Get().Warn("file logging unavailable", pkglogger.FieldError, err)
	}
	if projectName != "" {
		logRuntime.SetProject(projectName)
	}
	logRuntime.Get().Info("build info",
		"version", info.Version,
		"commit", info.Commit,
		"build_time", info.BuildTime,
		"runtime", info.Runtime,
	)
	logRuntime.BindDefault()
	return loggerResult{Runtime: logRuntime, Logger: logRuntime.Get()}
}

// buildInfo 保存启动日志中展示的构建元数据。
type buildInfo struct {
	Version   string
	Commit    string
	BuildTime string
	Runtime   string
}

// currentBuildInfo 从 Go build info 中提取版本、提交和构建时间。
func currentBuildInfo() buildInfo {
	info := buildInfo{
		Version: "dev",
		Commit:  "unknown",
		Runtime: runtime.GOOS + "/" + runtime.GOARCH,
	}
	meta, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}
	if version := strings.TrimSpace(meta.Main.Version); version != "" && version != "(devel)" {
		info.Version = version
	}
	for _, setting := range meta.Settings {
		applyBuildSetting(&info, setting.Key, setting.Value)
	}
	return info
}

// applyBuildSetting 将 Go build setting 合并到 buildInfo。
func applyBuildSetting(info *buildInfo, key, value string) {
	if info == nil {
		return
	}
	switch key {
	case "vcs.revision":
		value = strings.TrimSpace(value)
		if len(value) > 7 {
			value = value[:7]
		}
		if value != "" {
			info.Commit = value
		}
	case "vcs.time":
		if value = strings.TrimSpace(value); value != "" {
			info.BuildTime = value
		}
	}
}

// NewApp 创建后台模式 Fx 应用。
func NewApp() *fx.App {
	owner := newAppOwnerContext(context.Background())
	return newFXApp(fx.Supply(fx.Annotate(owner, fx.As(new(RootCtxProvider)))))
}

// Run 启动应用装配后台流程。
func Run() error {
	owner := newAppOwnerContext(context.Background())
	defer owner.Cancel()
	return runApp(owner, newFXApp(fx.Supply(fx.Annotate(owner, fx.As(new(RootCtxProvider))))))
}

// DesktopACKPublisher 把健康 ACK 写入与 Wails Run 退出放入同一线性化顺序。
type DesktopACKPublisher func(write func() error) error

// RunDesktop 启动桌面 Wails 应用，并在 backend 与前端 Wails RPC 握手就绪后调用 ready。
// 前端 filesystem 为空时由 wails 模块降级到内置占位页；运行结束前会先 drain runtime。
func RunDesktop(
	parent context.Context,
	frontendFS fs.FS,
	ready func(context.Context, DesktopACKPublisher) error,
) error {
	if parent == nil || ready == nil {
		return errors.New("desktop parent context and ready callback are required")
	}
	owner := newAppOwnerContext(parent)
	defer owner.Cancel()
	coordinator, err := newDesktopShutdownCoordinator(owner)
	if err != nil {
		return err
	}
	owner.desktopShutdown = coordinator
	ctx := owner.RootContext()
	if err := runDesktopPreflight(ctx, newAppDeps()); err != nil {
		return err
	}
	var wailsApp *application.App
	var lifecycle *uiwails.WailsLifecycle
	var activation *uiwails.ActivationReadiness

	app := newDesktopFXApp(
		fx.Supply(fx.Annotate(owner, fx.As(new(RootCtxProvider)))),
		fx.Supply(uiwails.FrontendFS{FS: frontendFS}),
		fx.Populate(&wailsApp, &lifecycle, &activation),
	)
	startCtx, cancelStart := platformconfig.WithTimeout(ctx, platformconfig.StartupTimeout)
	defer cancelStart()
	stopper := newDesktopFXStopper(ctx, app)
	if err := prepareDesktopRuntime(startCtx, app.Start, func() error {
		return validateDesktopRuntime(wailsApp, lifecycle)
	}, stopper.Stop); err != nil {
		return coordinator.FailStartup(err)
	}
	if err := coordinator.Configure(stopper.Stop, lifecycle); err != nil {
		return coordinator.FailStartup(errors.Join(err, stopper.Stop()))
	}

	runErr := runActivatedDesktop(startCtx, activation, ready, wailsApp.Run, lifecycle.RequestQuit)
	return coordinator.Shutdown(ctx, runErr)
}

// RunHeadlessDesktop 启动真实桌面后端图和 RPC bridge，但不创建原生 Wails 窗口。
// 该入口供无桌面会话的远程 worker 验证后端 RPC；原生桌面验证仍使用 RunDesktop。
func RunHeadlessDesktop(
	parent context.Context,
	frontendFS fs.FS,
	ready func(context.Context, DesktopACKPublisher) error,
) error {
	if parent == nil || ready == nil {
		return errors.New("headless desktop parent context and ready callback are required")
	}
	owner := newAppOwnerContext(parent)
	defer owner.Cancel()
	ctx := owner.RootContext()
	if err := runDesktopPreflight(ctx, newAppDeps()); err != nil {
		return err
	}
	var wailsApp *application.App
	var lifecycle *uiwails.WailsLifecycle

	app := newDesktopFXApp(
		fx.Supply(fx.Annotate(owner, fx.As(new(RootCtxProvider)))),
		fx.Supply(uiwails.FrontendFS{FS: frontendFS}),
		fx.Populate(&wailsApp, &lifecycle),
	)
	startCtx, cancelStart := platformconfig.WithTimeout(ctx, platformconfig.StartupTimeout)
	defer cancelStart()
	stopper := newDesktopFXStopper(ctx, app)
	if err := prepareDesktopRuntime(startCtx, app.Start, func() error {
		return validateDesktopRuntime(wailsApp, lifecycle)
	}, stopper.Stop); err != nil {
		return err
	}
	if err := runHeadlessDesktopReady(ctx, ready); err != nil {
		return errors.Join(err, stopper.Stop())
	}

	var runErr error
	select {
	case <-ctx.Done():
		runErr = context.Cause(ctx)
	case <-app.Done():
	}
	owner.Cancel()
	preDrainErr := preDrainDesktopRuntime(ctx, owner)
	stopErr := stopper.Stop()
	return errors.Join(runErr, preDrainErr, stopErr)
}

// validateDesktopRuntime 校验 Fx 已提供真实 Wails 应用与生命周期组件。
func validateDesktopRuntime(wailsApp *application.App, lifecycle *uiwails.WailsLifecycle) error {
	if wailsApp == nil {
		return errors.New("wails application not available")
	}
	if lifecycle == nil {
		return errors.New("wails lifecycle not available")
	}
	return nil
}

// runHeadlessDesktopReady 发布无窗口运行时 ACK，并拒绝空写入动作。
func runHeadlessDesktopReady(
	ctx context.Context,
	ready func(context.Context, DesktopACKPublisher) error,
) error {
	return ready(ctx, func(write func() error) error {
		if write == nil {
			return errors.New("headless desktop ACK write is required")
		}
		return write()
	})
}

// prepareDesktopRuntime 串行执行 Fx Start 与 Wails 依赖校验；ACK 由前端 RPC 握手驱动。
func prepareDesktopRuntime(
	ctx context.Context,
	start func(context.Context) error,
	validate func() error,
	stop func() error,
) error {
	if ctx == nil || start == nil || validate == nil || stop == nil {
		return errors.New("desktop startup lifecycle dependencies are required")
	}
	if err := start(ctx); err != nil {
		return err
	}
	if err := validate(); err != nil {
		return errors.Join(err, stop())
	}
	return nil
}

var (
	errDesktopNotActivated     = errors.New("wails application exited before ApplicationStarted")
	errDesktopNotFrontendReady = errors.New("wails application exited before frontend readiness")
	errDesktopRunBeforeACK     = errors.New("wails application exited before activation ACK completed")
)

type desktopActivationGate struct {
	mu           sync.Mutex
	runExited    bool
	ackAttempted bool
	ackCommitted bool
	ackErr       error
}

type desktopActivationSnapshot struct {
	ackAttempted bool
	ackCommitted bool
	ackErr       error
}

func (gate *desktopActivationGate) publish(write func() error) error {
	if write == nil {
		return errors.New("desktop activation ACK write is required")
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.runExited {
		return errDesktopRunBeforeACK
	}
	if gate.ackAttempted {
		return errors.New("desktop activation ACK was already attempted")
	}
	gate.ackAttempted = true
	gate.ackErr = write()
	if gate.ackErr != nil {
		return gate.ackErr
	}
	gate.ackCommitted = true
	return nil
}

func (gate *desktopActivationGate) markRunExited() desktopActivationSnapshot {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	gate.runExited = true
	return desktopActivationSnapshot{
		ackAttempted: gate.ackAttempted,
		ackCommitted: gate.ackCommitted,
		ackErr:       gate.ackErr,
	}
}

func desktopActivationResult(
	runErr error,
	activated bool,
	frontendReady bool,
	snapshot desktopActivationSnapshot,
	readyErr error,
) error {
	if !activated {
		return errors.Join(runErr, errDesktopNotActivated, readyErr)
	}
	if !frontendReady {
		return errors.Join(runErr, errDesktopNotFrontendReady, readyErr)
	}
	if snapshot.ackCommitted {
		return errors.Join(runErr, readyErr)
	}
	if snapshot.ackAttempted {
		return errors.Join(runErr, snapshot.ackErr, readyErr)
	}
	return errors.Join(runErr, errDesktopRunBeforeACK, readyErr)
}

// runActivatedDesktop 在调用方 goroutine 运行 Wails，并仅在前端 Wails RPC 握手后写入 ACK。
func runActivatedDesktop(
	ctx context.Context,
	activation *uiwails.ActivationReadiness,
	ready func(context.Context, DesktopACKPublisher) error,
	run func() error,
	quit func(),
) error {
	if ctx == nil || activation == nil || ready == nil || run == nil || quit == nil {
		return errors.New("desktop activation lifecycle dependencies are required")
	}
	activationCtx, cancelActivation := context.WithCancelCause(ctx)
	defer cancelActivation(context.Canceled)
	gate := &desktopActivationGate{}
	readyDone := make(chan error, 1)
	runtimesafe.SafeGo(activationCtx, pkglogger.Get(), "app.desktopActivation", func(context.Context) {
		if err := activation.WaitForFrontendReady(activationCtx); err != nil {
			quit()
			readyDone <- errors.Join(errDesktopNotFrontendReady, err)
			return
		}
		err := ready(activationCtx, gate.publish)
		if err != nil {
			quit()
		}
		readyDone <- err
	})

	runErr := run()
	snapshot := gate.markRunExited()
	activated := activation.Activated()
	frontendReady := activation.FrontendReady()
	if !activated {
		cancelActivation(errDesktopNotActivated)
	} else if !frontendReady {
		cancelActivation(errDesktopNotFrontendReady)
	} else {
		cancelActivation(errDesktopRunBeforeACK)
	}
	readyErr := <-readyDone
	return desktopActivationResult(runErr, activated, frontendReady, snapshot, readyErr)
}

// runDesktopPreflight 在 Fx 启动前准备桌面运行环境。
// packaged runtime 需要先完成 Codex relay bootstrap，失败时阻止桌面继续启动。
func runDesktopPreflight(ctx context.Context, d appDeps) error {
	projectRoot, err := platformconfig.PrimeProcessEnvironment()
	if err != nil {
		return fmt.Errorf("desktop preflight: load environment: %w", err)
	}
	if err := ensurePackagedCodexBootstrap(ctx, projectRoot, d); err != nil {
		return fmt.Errorf("desktop preflight: Codex bootstrap failed: %w", err)
	}
	return nil
}

// ensurePackagedCodexBootstrap 在打包运行时配置 Codex relay。
// 检测到 runtime-manifest.json 才要求 relay 配置，开发模式不强制。
func ensurePackagedCodexBootstrap(ctx context.Context, projectRoot string, d appDeps) error {
	required, err := packagedCodexRelayRequired(projectRoot)
	if err != nil {
		return err
	}
	if !required {
		return nil
	}
	baseURL, bootstrapToken, configured, err := codexRelayBootstrapEnv()
	if err != nil {
		return err
	}
	if !configured {
		return fmt.Errorf("packaged Codex relay config missing: set %s and %s in %s or the process environment", codexRelayBaseURLEnv, codexRelayBootstrapTokenEnv, filepath.Join(projectRoot, ".env"))
	}
	home, err := d.codexAppManagedHome()
	if err != nil {
		return fmt.Errorf("resolve app-managed Codex home: %w", err)
	}
	return d.ensureCodexBootstrap(ctx, codexapp.CodexBootstrapConfig{
		Home:                home,
		RelayBaseURL:        baseURL,
		RelayBootstrapToken: bootstrapToken,
	})
}

// packagedCodexRelayRequired 判断当前项目根是否处于打包 runtime。
func packagedCodexRelayRequired(projectRoot string) (bool, error) {
	projectRoot = strings.TrimSpace(projectRoot)
	if projectRoot == "" {
		return false, nil
	}
	info, err := os.Stat(filepath.Join(projectRoot, "runtime-manifest.json"))
	if err == nil {
		return !info.IsDir(), nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("inspect packaged runtime manifest: %w", err)
}

// codexRelayBootstrapEnv 读取打包 Codex relay bootstrap 配置。
// 特权 API key 不能被打进包内，发现后立即报错。
func codexRelayBootstrapEnv() (baseURL string, bootstrapToken string, configured bool, err error) {
	if strings.TrimSpace(os.Getenv(codexRelayPrivilegedAPIKeyEnv)) != "" {
		return "", "", false, fmt.Errorf("%s is a privileged relay API key env and must not be packaged; use %s", codexRelayPrivilegedAPIKeyEnv, codexRelayBootstrapTokenEnv)
	}
	baseURL = strings.TrimSpace(os.Getenv(codexRelayBaseURLEnv))
	bootstrapToken = strings.TrimSpace(os.Getenv(codexRelayBootstrapTokenEnv))
	if baseURL == "" && bootstrapToken == "" {
		return "", "", false, nil
	}
	var problems []error
	if baseURL == "" {
		problems = append(problems, fmt.Errorf("%s is required when %s is set", codexRelayBaseURLEnv, codexRelayBootstrapTokenEnv))
	}
	if bootstrapToken == "" {
		problems = append(problems, fmt.Errorf("%s is required when %s is set", codexRelayBootstrapTokenEnv, codexRelayBaseURLEnv))
	}
	if err := errors.Join(problems...); err != nil {
		return "", "", false, err
	}
	return baseURL, bootstrapToken, true, nil
}

// newFXApp 组装后台模式 Fx 应用。
func newFXApp(options ...fx.Option) *fx.App {
	base := []fx.Option{
		Module,
		fx.Invoke(BindRuntime),
		fx.StartTimeout(platformconfig.StartupTimeout),
		fx.StopTimeout(platformconfig.ShutdownTimeout),
	}
	base = append(base, options...)
	return fx.New(base...)
}

// runApp 启动后台 Fx 应用并等待 shutdown。
// app.Done 返回后先取消 owner root，再使用无取消 context 执行 Stop。
func runApp(owner *appOwnerContext, app *fx.App) error {
	if owner == nil {
		owner = newAppOwnerContext(context.Background())
	}
	ctx := owner.RootContext()
	startCtx, cancel := platformconfig.WithTimeout(ctx, platformconfig.StartupTimeout)
	defer cancel()
	if err := app.Start(startCtx); err != nil {
		return err
	}
	<-app.Done()
	owner.Cancel()
	return stopFXApp(context.WithoutCancel(ctx), app)
}

// newDesktopFXApp 组装桌面模式 Fx 应用。
func newDesktopFXApp(options ...fx.Option) *fx.App {
	base := []fx.Option{
		Module,
		uiwails.Module,
		fx.Invoke(BindRuntime),
		fx.StartTimeout(platformconfig.StartupTimeout),
		fx.StopTimeout(platformconfig.ShutdownTimeout),
	}
	base = append(base, options...)
	return fx.New(base...)
}

// stopFXApp 在全局 ShutdownTimeout 内停止 Fx 应用。
func stopFXApp(parent context.Context, app *fx.App) error {
	if parent == nil {
		parent = context.Background()
	}
	// 调用侧统一套 ShutdownTimeout，确保 Fx shutdown 不会无限挂起。
	// 误判防护：stopFXApp 使用 platformconfig.WithTimeout，FX shutdown 不会无限挂起。
	ctx, cancel := platformconfig.WithTimeout(parent, platformconfig.ShutdownTimeout)
	defer cancel()
	return app.Stop(ctx)
}

// preDrainDesktopRuntime 在 Wails 退出后等待 runtime 完成并 drain 内存提取。
func preDrainDesktopRuntime(ctx context.Context, owner *appOwnerContext) error {
	if ctx == nil {
		ctx = context.Background()
	}
	// 误判防护：preDrainDesktopRuntime 用 ShutdownTimeout 包住 runtime drain 和 WaitRuntimeDone。
	drainCtx, cancel := platformconfig.WithTimeout(context.WithoutCancel(ctx), platformconfig.ShutdownTimeout)
	defer cancel()
	return errors.Join(owner.WaitRuntimeDone(drainCtx), owner.DrainRuntime(drainCtx))
}

// desktopFXStopper 确保桌面 Fx Stop 只执行一次。
type desktopFXStopper struct {
	parent context.Context
	app    *fx.App

	once sync.Once
	err  error
}

// newDesktopFXStopper 创建桌面 Fx 停止器。
func newDesktopFXStopper(parent context.Context, app *fx.App) *desktopFXStopper {
	return &desktopFXStopper{
		parent: parent,
		app:    app,
	}
}

// Stop 停止桌面 Fx 应用且只执行一次。
// 使用 WithoutCancel 的父 context，避免外层取消打断 Stop hooks 收尾。
func (s *desktopFXStopper) Stop() error {
	if s == nil {
		return nil
	}
	s.once.Do(func() {
		s.err = stopFXApp(context.WithoutCancel(s.parent), s.app)
	})
	return s.err
}
