package main

import (
	"flag"
	"io"
	"os"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// parseRemoteRunOptions 严格解析 remote run 和 calibrate 共用的身份及存储参数。
func parseRemoteRunOptions(args []string) (remoteRunOptions, error) {
	var options remoteRunOptions
	var agentToken string
	flags := flag.NewFlagSet("remote run", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.ConfigPath, "config", "", "remote CI config path")
	flags.StringVar(&options.RepositoryRoot, "repository", ".", "Git repository root")
	flags.StringVar(&options.Commit, "commit", "", "commit revision")
	flags.StringVar(&options.Tree, "tree", "", "explicit Git tree revision")
	flags.StringVar(&options.ParentCommit, "parent", "", "parent commit for an explicit tree")
	flags.StringVar(&options.Base, "base", "", "base commit revision")
	flags.StringVar(&options.Profile, "profile", "", "legacy gate profile override")
	flags.StringVar(&options.Scenario, "scenario", "", "remote CI scenario: commit, push, full, or test")
	flags.StringVar(&options.Entrypoint, "entrypoint", "", "canonical CI entrypoint ID")
	var tests remoteStringListFlag
	flags.Var(&tests, "test", "exact Go package or frontend Vitest file; repeatable")
	flags.StringVar(&options.LocalRef, "local-ref", "", "local ref for push profile")
	flags.StringVar(&options.RemoteRef, "remote-ref", "", "remote ref for push profile")
	flags.StringVar(&options.ObservedRemote, "observed-remote", "", "observed remote commit for push profile")
	flags.StringVar(&options.UpdateKind, "update-kind", "", "push update kind")
	flags.StringVar(&options.LedgerPath, "ledger", "", "remote baseline and duration ledger SQLite authority path")
	flags.StringVar(&agentToken, "agent-token", "", "remote CI agent token")
	if err := flags.Parse(args); err != nil {
		return options, protocolError("parse remote run flags: %v", err)
	}
	if err := validateRemoteRunFlags(flags, options); err != nil {
		return options, err
	}
	if err := normalizeRemoteSQLiteAuthority(options.ConfigPath, &options.LedgerPath); err != nil {
		return options, err
	}
	if err := normalizeRemoteRunSource(&options); err != nil {
		return options, err
	}
	token, err := resolveRemoteCIAgentToken(agentToken)
	if err != nil {
		return options, protocolError("resolve CI agent token: %v", err)
	}
	options.AgentTokenDigest, err = cicontract.AgentTokenDigest(token)
	if err != nil {
		return options, protocolError("digest CI agent token: %v", err)
	}
	options.Tests = append([]string(nil), tests...)
	return options, nil
}

// validateRemoteRunFlags 校验解析后必须存在的存储参数。
func validateRemoteRunFlags(flags *flag.FlagSet, options remoteRunOptions) error {
	if flags.NArg() != 0 || strings.TrimSpace(options.ConfigPath) == "" {
		return protocolError("remote run requires --config and valid optional flags")
	}
	return nil
}

// normalizeRemoteRunSource 应用默认 HEAD 并拒绝混合 commit 与 tree 身份。
func normalizeRemoteRunSource(options *remoteRunOptions) error {
	if options.Commit == "" && options.Tree == "" {
		options.Commit = "HEAD"
	}
	if options.Commit != "" && options.Tree != "" {
		return protocolError("remote run accepts exactly one of --commit or --tree")
	}
	return nil
}

func loadRemoteRunConfig(path string) (remoteRunConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return remoteRunConfig{}, err
	}
	var config remoteRunConfig
	if err := gatecontract.DecodeStrictJSON(data, &config); err != nil {
		return remoteRunConfig{}, err
	}
	if err := config.Validate(); err != nil {
		return remoteRunConfig{}, err
	}
	return config, nil

}
