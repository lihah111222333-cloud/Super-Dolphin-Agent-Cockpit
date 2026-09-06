// Package main is the unregistered RecoveryCustody SQLite authority candidate.
// It is intentionally separate from mcp-orch production composition.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.New(slog.NewTextHandler(os.Stderr, nil)).Error("mcp-recovery-custody failed", "error", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	configPath, err := parseArgs(args)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}
	deps, err := processDependencies()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	service, err := newService(ctx, cfg, deps)
	if err != nil {
		return err
	}
	defer func() { _ = service.close() }()
	return service.serve(ctx)
}

// parseArgs 只接受唯一绝对配置参数，并拒绝位置参数和隐式默认路径。
func parseArgs(args []string) (string, error) {
	flags := flag.NewFlagSet("mcp-recovery-custody", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var configPath string
	flags.StringVar(&configPath, "config", "", "absolute JSON configuration path")
	if err := flags.Parse(args); err != nil {
		return "", fmt.Errorf("parse recovery custody flags: %w", err)
	}
	if flags.NArg() != 0 {
		return "", errors.New("mcp-recovery-custody does not accept positional arguments")
	}
	if configPath == "" {
		return "", errors.New("mcp-recovery-custody requires --config <absolute path>")
	}
	if !filepath.IsAbs(configPath) || filepath.Clean(configPath) != configPath {
		return "", errors.New("mcp-recovery-custody config must be an absolute canonical path")
	}
	return configPath, nil
}
