package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/app"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/embeddedpg"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rlimit"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimeenv"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

const defaultDesktopDatabaseURL = "postgres://postgres:agent@127.0.0.1:5432/super_dolphin?sslmode=disable"

func main() {
	rlimit.Init()
	if err := os.Setenv("SUPER_DOLPHIN_PROCESS_ROLE", "desktop"); err != nil {
		pkglogger.Get().Error("agent-terminal startup env failed", "error", err)
		os.Exit(1)
	}
	if err := runtimeenv.ConfigurePackagedApp(); err != nil {
		pkglogger.Get().Error("agent-terminal packaged runtime env failed", "error", err)
		os.Exit(1)
	}
	if err := runtimeenv.LoadVideoEnv(); err != nil {
		pkglogger.Get().Error("agent-terminal video env failed", "error", err)
		os.Exit(1)
	}
	if err := configureDesktopDatabaseEnvironment(); err != nil {
		pkglogger.Get().Error("agent-terminal database env failed", "error", err)
		os.Exit(1)
	}
	if err := app.RunDesktop(frontendDistFS()); err != nil {
		pkglogger.Get().Error("agent-terminal failed", "error", err)
		os.Exit(1)
	}
}

func configureDesktopDatabaseEnvironment() error {
	if _, err := config.PrimeProcessEnvironment(); err != nil {
		return err
	}
	return configureDefaultDesktopDatabaseURL(os.Getenv, os.Setenv)
}

func configureDefaultDesktopDatabaseURL(getenv func(string) string, setenv func(string, string) error) error {
	if strings.TrimSpace(getenv("DATABASE_URL")) != "" || strings.TrimSpace(getenv("POSTGRES_CONNECTION_STRING")) != "" {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(getenv(embeddedpg.EnvProcessRole)), "desktop") {
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(getenv("SUPER_DOLPHIN_RUNTIME_MODE")), "packaged") {
		return nil
	}
	if desktopEmbeddedPostgresRequested(getenv) {
		return nil
	}
	if err := setenv("DATABASE_URL", defaultDesktopDatabaseURL); err != nil {
		return fmt.Errorf("set default desktop DATABASE_URL: %w", err)
	}
	return nil
}

func desktopEmbeddedPostgresRequested(getenv func(string) string) bool {
	if strings.EqualFold(strings.TrimSpace(getenv(embeddedpg.EnvEmbeddedPostgres)), "true") {
		return true
	}
	for _, key := range []string{embeddedpg.EnvPostgresBinDir, embeddedpg.EnvPostgresShareDir, embeddedpg.EnvPostgresPort} {
		if strings.TrimSpace(getenv(key)) != "" {
			return true
		}
	}
	return false
}
