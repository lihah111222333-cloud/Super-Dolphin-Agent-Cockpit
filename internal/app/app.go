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

	"go.uber.org/fx"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
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
	return newFXApp()
}

func Run() error {
	return runApp(NewApp())
}

// RunDesktop starts the desktop application with the given frontend filesystem.
// When frontendFS is nil the wails module falls back to a built-in placeholder.
func RunDesktop(frontendFS fs.FS) error {
	ctx := context.Background()
	var wailsApp *application.App
	var lifecycle *uiwails.WailsLifecycle

	app := newDesktopFXApp(
		fx.Supply(uiwails.FrontendFS{FS: frontendFS}),
		fx.Populate(&wailsApp, &lifecycle),
	)
	startCtx, cancel := platformconfig.WithTimeout(ctx, platformconfig.StartupTimeout)
	defer cancel()
	if err := app.Start(startCtx); err != nil {
		return err
	}
	if wailsApp == nil {
		return errors.New("wails application not available")
	}
	if lifecycle == nil {
		return errors.New("wails lifecycle not available")
	}

	stopWatch := watchFXShutdown(app, lifecycle)
	runErr := wailsApp.Run()
	close(stopWatch)

	stopErr := stopFXApp(ctx, app)
	return errors.Join(runErr, stopErr)
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

func runApp(app *fx.App) error {
	startCtx, cancel := platformconfig.WithTimeout(context.Background(), platformconfig.StartupTimeout)
	defer cancel()
	if err := app.Start(startCtx); err != nil {
		return err
	}
	<-app.Done()
	return stopFXApp(context.Background(), app)
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

func watchFXShutdown(app *fx.App, lifecycle *uiwails.WailsLifecycle) chan struct{} {
	stop := make(chan struct{})
	platformshared.SafeGo(pkglogger.Get(), func() {
		select {
		case <-app.Done():
			lifecycle.NotifyBackendFailed()
		case <-stop:
		}
	})
	return stop
}
