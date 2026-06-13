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

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimesafe"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
	uiwails "github.com/anthropic-ai/super-agent-v3/internal/ui/wails"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type appDeps struct {
	ensureCodexCLIAvailable func(context.Context) error
	ensureCodexBootstrap    func(context.Context, codexapp.CodexBootstrapConfig) error
	codexAppManagedHome     func() (string, error)
}

var deps = appDeps{
	ensureCodexCLIAvailable: codexapp.EnsureCLIAvailable,
	ensureCodexBootstrap:    codexapp.EnsureCodexBootstrap,
	codexAppManagedHome: func() (string, error) {
		return providershared.AppManagedProviderHome(providershared.ProviderCodex)
	},
}

const (
	codexRelayBaseURLEnv          = "SUPER_DOLPHIN_CODEX_RELAY_BASE_URL"
	codexRelayBootstrapTokenEnv   = "SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN"
	codexRelayPrivilegedAPIKeyEnv = "SUPER_DOLPHIN_CODEX_RELAY_API_KEY"
)

// NewLogger 创建日志器。
func NewLogger() *slog.Logger {
	info := currentBuildInfo()
	pkglogger.ConfigureServiceFromEnv(info.Version)
	pkglogger.Init(os.Getenv("LOG_LEVEL"))

	homeDir, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	logDir, projectName := pkglogger.ResolveProjectLogDir(homeDir, cwd)
	if err := pkglogger.InitWithFile(logDir); err != nil {
		pkglogger.Warn("file logging unavailable", pkglogger.FieldError, err)
	}
	if projectName != "" {
		pkglogger.SetProject(projectName)
	}
	pkglogger.Info("build info",
		"version", info.Version,
		"commit", info.Commit,
		"build_time", info.BuildTime,
		"runtime", info.Runtime,
	)
	return pkglogger.Get()
}

type buildInfo struct {
	Version   string
	Commit    string
	BuildTime string
	Runtime   string
}

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

// applyBuildSetting 应用buildsetting。
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

// NewApp 创建app。
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

// RunDesktop starts the desktop application with the given frontend filesystem.
// When frontendFS is nil the wails module falls back to a built-in placeholder.
// RunDesktop 运行desktop。
func RunDesktop(frontendFS fs.FS) error {
	owner := newAppOwnerContext(context.Background())
	defer owner.Cancel()
	ctx := owner.RootContext()
	if err := runDesktopPreflight(ctx); err != nil {
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
	if err := app.Start(startCtx); err != nil {
		return err
	}
	if wailsApp == nil {
		return errors.New("wails application not available")
	}
	if lifecycle == nil {
		return errors.New("wails lifecycle not available")
	}

	stopper := newDesktopFXStopper(ctx, app)
	watcher := watchFXShutdown(ctx, app, lifecycle, stopper.Stop)
	runErr := wailsApp.Run()
	owner.Cancel()
	preDrainErr := preDrainDesktopRuntime(ctx, owner)
	watcher.StopAndWait()

	stopErr := stopper.Stop()
	return errors.Join(runErr, preDrainErr, stopErr)
}

func runDesktopPreflight(ctx context.Context) error {
	projectRoot, err := platformconfig.PrimeProcessEnvironment()
	if err != nil {
		return fmt.Errorf("desktop preflight: load environment: %w", err)
	}
	if err := ensurePackagedCodexBootstrap(ctx, projectRoot, deps); err != nil {
		return fmt.Errorf("desktop preflight: Codex bootstrap failed: %w", err)
	}
	return nil
}

// ensurePackagedCodexBootstrap 确保packagedcodex启动。
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

// codexRelayBootstrapEnv 处理codexrelay启动env。
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

func stopFXApp(parent context.Context, app *fx.App) error {
	if parent == nil {
		parent = context.Background()
	}
	// Apply a caller-side hard stop so shutdown cannot hang indefinitely.
	// 误判防护：stopFXApp 使用 platformconfig.WithTimeout，FX shutdown 不会无限挂起。
	ctx, cancel := platformconfig.WithTimeout(parent, platformconfig.ShutdownTimeout)
	defer cancel()
	return app.Stop(ctx)
}

func preDrainDesktopRuntime(ctx context.Context, owner *appOwnerContext) error {
	if ctx == nil {
		ctx = context.Background()
	}
	// 误判防护：preDrainDesktopRuntime 用 ShutdownTimeout 包住 runtime drain 和 WaitRuntimeDone。
	drainCtx, cancel := platformconfig.WithTimeout(context.WithoutCancel(ctx), platformconfig.ShutdownTimeout)
	defer cancel()
	return errors.Join(owner.WaitRuntimeDone(drainCtx), owner.DrainRuntime(drainCtx))
}

type shutdownWatcher struct {
	stop chan struct{}
	done chan struct{}
	once sync.Once
}

// StopAndWait 停止wait。
func (w *shutdownWatcher) StopAndWait() {
	if w == nil {
		return
	}
	w.once.Do(func() { close(w.stop) })
	<-w.done
}

type desktopFXStopper struct {
	parent context.Context
	app    *fx.App

	once sync.Once
	err  error
}

func newDesktopFXStopper(parent context.Context, app *fx.App) *desktopFXStopper {
	return &desktopFXStopper{
		parent: parent,
		app:    app,
	}
}

// Stop 停止应用装配流程。
func (s *desktopFXStopper) Stop() error {
	if s == nil {
		return nil
	}
	s.once.Do(func() {
		s.err = stopFXApp(context.WithoutCancel(s.parent), s.app)
	})
	return s.err
}

func watchFXShutdown(ctx context.Context, app *fx.App, lifecycle *uiwails.WailsLifecycle, stopBackend func() error) *shutdownWatcher {
	watcher := &shutdownWatcher{stop: make(chan struct{}), done: make(chan struct{})}
	runtimesafe.SafeGo(ctx, pkglogger.Get(), "app.watchFXShutdown", func(ctx context.Context) {
		defer close(watcher.done)
		runShutdownWatcher(ctx, app.Done(), watcher.stop, stopBackend, func(err error) {
			if err != nil {
				pkglogger.Get().Warn("desktop backend stop before quit failed", "error", err)
				lifecycle.NotifyBackendFailed()
				return
			}
			lifecycle.NotifyBackendStopped()
		})
	})
	return watcher
}

// runShutdownWatcher 运行shutdownwatcher。
func runShutdownWatcher(ctx context.Context, done <-chan os.Signal, stop <-chan struct{}, stopBackend func() error, onStopped func(error)) {
	select {
	case <-done:
		var err error
		if stopBackend != nil {
			err = stopBackend()
		}
		if onStopped != nil {
			onStopped(err)
		}
	case <-stop:
	case <-ctx.Done():
	}
}
