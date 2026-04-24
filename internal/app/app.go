package app

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"

	"go.uber.org/fx"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimesafe"
	uiwails "github.com/anthropic-ai/super-agent-v3/internal/ui/wails"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func NewLogger() *slog.Logger {
	pkglogger.Init(os.Getenv("LOG_LEVEL"))
	info := currentBuildInfo()
	pkglogger.Info("build info",
		"version", info.Version,
		"commit", info.Commit,
		"build_time", info.BuildTime,
		"runtime", info.Runtime,
	)

	homeDir, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	logDir, projectName := pkglogger.ResolveProjectLogDir(homeDir, cwd)
	if err := pkglogger.InitWithFile(logDir); err != nil {
		pkglogger.Warn("file logging unavailable", pkglogger.FieldError, err)
	}
	if projectName != "" {
		pkglogger.SetProject(projectName)
	}
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

func NewApp() *fx.App {
	owner := newAppOwnerContext(context.Background())
	return newFXApp(fx.Supply(fx.Annotate(owner, fx.As(new(RootCtxProvider)))))
}

func Run() error {
	owner := newAppOwnerContext(context.Background())
	defer owner.Cancel()
	return runApp(owner, newFXApp(fx.Supply(fx.Annotate(owner, fx.As(new(RootCtxProvider))))))
}

// RunDesktop starts the desktop application with the given frontend filesystem.
// When frontendFS is nil the wails module falls back to a built-in placeholder.
func RunDesktop(frontendFS fs.FS) error {
	owner := newAppOwnerContext(context.Background())
	defer owner.Cancel()
	ctx := owner.RootContext()
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

	watcher := watchFXShutdown(ctx, app, lifecycle)
	runErr := wailsApp.Run()
	owner.Cancel()
	preDrainErr := preDrainDesktopRuntime(ctx, owner)
	watcher.StopAndWait()

	stopErr := stopFXApp(context.WithoutCancel(ctx), app)
	return errors.Join(runErr, preDrainErr, stopErr)
}

func newFXApp(options ...fx.Option) *fx.App {
	base := []fx.Option{
		Module,
		fx.Invoke(BindRuntime),
		// Caller-side start/stop deadlines are applied where App.Start/App.Stop are invoked.
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
		// Caller-side start/stop deadlines are applied where App.Start/App.Stop are invoked.
	}
	base = append(base, options...)
	return fx.New(base...)
}

func stopFXApp(parent context.Context, app *fx.App) error {
	if parent == nil {
		parent = context.Background()
	}
	// Apply a caller-side hard stop so shutdown cannot hang indefinitely.
	ctx, cancel := platformconfig.WithTimeout(parent, platformconfig.ShutdownTimeout)
	defer cancel()
	return app.Stop(ctx)
}

func preDrainDesktopRuntime(ctx context.Context, owner *appOwnerContext) error {
	if ctx == nil {
		ctx = context.Background()
	}
	drainCtx, cancel := platformconfig.WithTimeout(context.WithoutCancel(ctx), platformconfig.ShutdownTimeout)
	defer cancel()
	return errors.Join(owner.WaitRuntimeDone(drainCtx), owner.DrainRuntime(drainCtx))
}

type shutdownWatcher struct {
	stop chan struct{}
	done chan struct{}
	once sync.Once
}

func (w *shutdownWatcher) StopAndWait() {
	if w == nil {
		return
	}
	w.once.Do(func() { close(w.stop) })
	<-w.done
}

func watchFXShutdown(ctx context.Context, app *fx.App, lifecycle *uiwails.WailsLifecycle) *shutdownWatcher {
	watcher := &shutdownWatcher{stop: make(chan struct{}), done: make(chan struct{})}
	runtimesafe.SafeGo(ctx, pkglogger.Get(), "app.watchFXShutdown", func(ctx context.Context) {
		defer close(watcher.done)
		runShutdownWatcher(ctx, app.Done(), watcher.stop, lifecycle.NotifyBackendFailed)
	})
	return watcher
}

func runShutdownWatcher(ctx context.Context, done <-chan os.Signal, stop <-chan struct{}, onFail func()) {
	select {
	case <-done:
		onFail()
	case <-stop:
	case <-ctx.Done():
	}
}
