package main

import (
	"flag"
	"io"
	"strings"
)

// parseRemoteBaselineRefreshOptions 解析并校验 refresh 命令行参数。
func parseRemoteBaselineRefreshOptions(args []string) (remoteBaselineRefreshOptions, error) {
	var options remoteBaselineRefreshOptions
	flags := flag.NewFlagSet("remote baseline-refresh", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.ConfigPath, "config", "", "remote CI config path")
	flags.StringVar(&options.StatePath, "state", "", "accepted baseline state path")
	flags.StringVar(&options.LedgerPath, "ledger", "", "duration ledger SQLite authority path")
	flags.StringVar(&options.RepositoryRoot, "repository", ".", "Git repository root")
	flags.StringVar(&options.Remote, "remote", "origin", "Git remote")
	flags.StringVar(&options.Ref, "ref", "refs/heads/main", "remote Git ref")
	flags.StringVar(&options.Platform, "platform", "linux/amd64", "baseline target platform")
	if err := flags.Parse(args); err != nil {
		return options, protocolError("parse remote baseline-refresh flags: %v", err)
	}
	if flags.NArg() != 0 || strings.TrimSpace(options.ConfigPath) == "" || strings.TrimSpace(options.LedgerPath) == "" ||
		strings.TrimSpace(options.Remote) == "" || !strings.HasPrefix(options.Ref, "refs/heads/") ||
		(options.Platform != "linux/amd64" && options.Platform != "linux/arm64") {
		return options, protocolError("remote baseline-refresh requires --config and valid optional flags")
	}
	return options, nil
}
