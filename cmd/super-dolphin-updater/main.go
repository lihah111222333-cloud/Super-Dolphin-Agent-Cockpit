package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

const updaterDetachedEnv = "SUPER_DOLPHIN_UPDATER_DETACHED"

func main() {
	pkglogger.InitWithConsoleWriter(os.Stderr)
	req, err := parseInstallRequest(os.Args[1:])
	if err != nil {
		pkglogger.Get().Error("super-dolphin-updater request failed", "error", err)
		os.Exit(2)
	}
	if os.Getenv(updaterDetachedEnv) != "1" {
		if err := startDetachedUpdater(req.LogPath); err != nil {
			pkglogger.Get().Error("super-dolphin-updater detach failed", "error", err)
			os.Exit(1)
		}
		return
	}
	if err := install(req); err != nil {
		pkglogger.Get().Error("super-dolphin-updater install failed", "error", err)
		os.Exit(1)
	}
}

func parseInstallRequest(args []string) (installRequest, error) {
	flags := flag.NewFlagSet("super-dolphin-updater", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	var req installRequest
	flags.StringVar(&req.DMGPath, "dmg", "", "path to the signed Super Dolphin DMG")
	flags.StringVar(&req.TargetAppPath, "target", "", "path to the installed Super Dolphin.app")
	flags.BoolVar(&req.Restart, "restart", false, "reopen the target app after replacement")
	flags.BoolVar(&req.AllowUnsigned, "allow-unsigned", false, "allow ad-hoc signed gray test updates without Developer ID")
	flags.IntVar(&req.WaitPID, "wait-pid", 0, "wait for the current app process to exit before replacement")
	flags.StringVar(&req.LogPath, "log", "", "path to write detached updater logs")
	if err := flags.Parse(args); err != nil {
		return installRequest{}, err
	}
	if flags.NArg() != 0 {
		return installRequest{}, fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}
	return req, nil
}

func startDetachedUpdater(logPath string) error {
	output, closeOutput, err := openDetachedOutput(logPath)
	if err != nil {
		return err
	}
	defer closeOutput()
	cmd := exec.Command(os.Args[0], os.Args[1:]...)
	cmd.Env = append(os.Environ(), updaterDetachedEnv+"=1")
	cmd.Stdout = output
	cmd.Stderr = output
	configureDetachedCommand(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start detached updater: %w", err)
	}
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("release detached updater: %w", err)
	}
	return nil
}

func openDetachedOutput(logPath string) (*os.File, func(), error) {
	path := strings.TrimSpace(logPath)
	if path == "" {
		file, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		if err != nil {
			return nil, func() {}, fmt.Errorf("open updater devnull: %w", err)
		}
		return file, func() { _ = file.Close() }, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, func() {}, fmt.Errorf("create updater log dir: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, func() {}, fmt.Errorf("open updater log: %w", err)
	}
	return file, func() { _ = file.Close() }, nil
}
