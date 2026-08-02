package main

import (
	"flag"
	"io"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// parseRemoteBaselineRefreshOptions 解析并校验 refresh 命令行参数。
func parseRemoteBaselineRefreshOptions(args []string) (remoteBaselineRefreshOptions, error) {
	var options remoteBaselineRefreshOptions
	flags := flag.NewFlagSet("remote baseline-refresh", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.ConfigPath, "config", "", "remote CI config path")
	flags.StringVar(&options.LedgerPath, "ledger", "", "duration ledger SQLite authority path")
	flags.StringVar(&options.RepositoryRoot, "repository", ".", "Git repository root")
	flags.StringVar(&options.Remote, "remote", "origin", "Git remote")
	flags.StringVar(&options.Ref, "ref", "refs/heads/main", "remote Git ref")
	flags.StringVar(&options.Platform, "platform", cicontract.TargetPlatform, "baseline target platform")
	if err := flags.Parse(args); err != nil {
		return options, protocolError("parse remote baseline-refresh flags: %v", err)
	}
	if flags.NArg() != 0 || strings.TrimSpace(options.ConfigPath) == "" ||
		strings.TrimSpace(options.Remote) == "" || !strings.HasPrefix(options.Ref, "refs/heads/") ||
		cicontract.ValidateTargetPlatform(options.Platform) != nil {
		return options, protocolError("remote baseline-refresh requires --config and valid optional flags")
	}
	if err := normalizeRemoteSQLiteAuthority(options.ConfigPath, &options.LedgerPath); err != nil {
		return options, err
	}
	return options, nil
}
