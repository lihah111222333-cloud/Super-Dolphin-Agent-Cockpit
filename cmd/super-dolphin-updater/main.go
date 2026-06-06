package main

import (
	"flag"
	"fmt"
	"os"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

func main() {
	pkglogger.InitWithConsoleWriter(os.Stderr)
	req, err := parseInstallRequest(os.Args[1:])
	if err != nil {
		pkglogger.Get().Error("super-dolphin-updater request failed", "error", err)
		os.Exit(2)
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
	if err := flags.Parse(args); err != nil {
		return installRequest{}, err
	}
	if flags.NArg() != 0 {
		return installRequest{}, fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}
	return req, nil
}
