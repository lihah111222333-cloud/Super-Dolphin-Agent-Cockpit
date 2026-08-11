package main

import (
	"flag"
	"io"
	"os"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// parseRemoteRunOptions 保持 remote run/calibrate 的 remote-only 兼容边界。
func parseRemoteRunOptions(args []string) (remoteRunOptions, error) {
	return parseRunOptions(args, "remote", false)
}

// parseRunOptions 为 test 提供 local/remote/auto/hybrid 目标解析，remote 入口仍只接受 remote。
func parseRunOptions(args []string, defaultTarget string, allowNonRemote bool) (remoteRunOptions, error) {
	var options remoteRunOptions
	var agentToken string
	parseArgs, deferAgentToken, err := prepareRunOptionParseArgs(args, defaultTarget, allowNonRemote)
	if err != nil {
		return options, err
	}
	flags := flag.NewFlagSet("remote run", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.ConfigPath, "config", "", "remote CI config path")
	flags.StringVar(&options.RepositoryRoot, "repository", ".", "Git repository root")
	flags.StringVar(&options.Commit, "commit", "", "commit revision")
	flags.StringVar(&options.Tree, "tree", "", "explicit Git tree revision")
	flags.StringVar(&options.ParentCommit, "parent", "", "parent commit for an explicit tree")
	flags.StringVar(&options.Base, "base", "", "base commit revision")
	flags.StringVar(&options.Scenario, "scenario", "", "remote CI scenario: commit, push, full, or test")
	flags.StringVar(&options.Entrypoint, "entrypoint", "", "canonical CI entrypoint ID")
	var tests remoteStringListFlag
	flags.Var(&tests, "test", "exact Go package or frontend Vitest file; repeatable")
	flags.StringVar(&options.WorkloadID, "workload", "", "catalog-owned MCP-LSP workload ID")
	var gateWorkloads remoteStringListFlag
	flags.Var(&gateWorkloads, "gate-workload", "expanded gate workload ID; repeatable")
	flags.StringVar(&options.GateWorkloadManifest, "gate-workload-manifest", "", "absolute JSON array of expanded gate workload IDs")
	flags.StringVar(&options.Target, "target", defaultTarget, "workload authority target: local, remote, auto, or hybrid")
	flags.StringVar(&options.CompletionReceiptPath, "completion-receipt", "", "absolute root-cohort completion receipt path")
	flags.StringVar(&options.LocalRef, "local-ref", "", "local ref for push profile")
	flags.StringVar(&options.RemoteRef, "remote-ref", "", "remote ref for push profile")
	flags.StringVar(&options.ObservedRemote, "observed-remote", "", "observed remote commit for push profile")
	flags.StringVar(&options.UpdateKind, "update-kind", "", "push update kind")
	flags.StringVar(&options.LedgerPath, "ledger", "", "remote baseline and duration ledger SQLite authority path")
	flags.StringVar(&agentToken, "agent-token", "", "remote CI agent token")
	flags.BoolVar(&options.Force, "force", false, "force execution of every shardable workload and bypass PASS reuse")
	if err := flags.Parse(parseArgs); err != nil {
		return options, protocolError("parse remote run flags: %v", err)
	}
	gateWorkloadIDs, err := mergeWorkloadSelections(gateWorkloads, options.GateWorkloadManifest)
	if err != nil {
		return options, err
	}
	options.GateWorkloadIDs = gateWorkloadIDs
	if err := validateRemoteRunFlags(flags, options, allowNonRemote, gateWorkloads); err != nil {
		return options, err
	}
	if err := normalizeRunLedger(&options); err != nil {
		return options, err
	}
	if err := normalizeRemoteRunSource(&options); err != nil {
		return options, err
	}
	if !deferAgentToken {
		if err := bindRunAgentToken(&options, agentToken); err != nil {
			return options, err
		}
	}
	options.Tests = append([]string(nil), tests...)
	return options, nil
}

// prepareRunOptionParseArgs 在规划解析前确定 target；非 remote 调用必须剥离原始 token，
// 只允许冻结的 remote 子集在后续握手阶段重新读取原始参数。
func prepareRunOptionParseArgs(args []string, defaultTarget string, allowNonRemote bool) ([]string, bool, error) {
	target, err := requestedWorkloadTarget(args, defaultTarget)
	if err != nil {
		return nil, false, err
	}
	if !allowNonRemote || target == "remote" {
		return args, false, nil
	}
	return withoutDeferredAgentToken(args), true, nil
}

// withoutDeferredAgentToken 在非 remote 规划解析前移除原始 token 参数；
// 原始参数只会在冻结 remote 子集的后续握手阶段使用。
func withoutDeferredAgentToken(args []string) []string {
	filtered := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--agent-token" {
			if index+1 < len(args) {
				index++
			}
			continue
		}
		if strings.HasPrefix(arg, "--agent-token=") {
			continue
		}
		filtered = append(filtered, arg)
	}
	return filtered
}

// normalizeRunLedger selects the single SQLite authority required by the target namespace.
func normalizeRunLedger(options *remoteRunOptions) error {
	if options.Target == "remote" {
		return normalizeRemoteSQLiteAuthority(options.ConfigPath, &options.LedgerPath)
	}
	if strings.TrimSpace(options.LedgerPath) == "" {
		return protocolError("non-remote target requires --ledger")
	}
	return nil
}

// bindRunAgentToken 仅为 remote 执行路径解析并摘要 ECI agent token。
func bindRunAgentToken(options *remoteRunOptions, agentToken string) error {
	// Local/auto/hybrid selection is deliberately evaluated before the remote
	// token handshake.  A caller may still provide an already-issued token for
	// an auto/hybrid invocation, but the phase-two sentinel must remain deferred
	// until a frozen remote subset actually exists.
	if options.Target != "remote" && (strings.TrimSpace(agentToken) == "" || agentToken == cicontract.AgentTokenIssueValue) {
		return nil
	}
	token, tokenErr := resolveRemoteCIAgentToken(agentToken)
	if tokenErr != nil {
		return protocolError("resolve CI agent token: %v", tokenErr)
	}
	digest, err := cicontract.AgentTokenDigest(token)
	if err != nil {
		return protocolError("digest CI agent token: %v", err)
	}
	options.AgentTokenDigest = digest
	return nil
}

// validateRemoteRunFlags 校验解析后必须存在的存储参数与入口 authority 边界。
func validateRemoteRunFlags(flags *flag.FlagSet, options remoteRunOptions, allowNonRemote bool, gateWorkloads remoteStringListFlag) error {
	if flags.NArg() != 0 {
		return protocolError("remote run requires valid optional flags")
	}
	if err := validateWorkloadAuthorityTarget(options.Target); err != nil {
		return err
	}
	if !allowNonRemote && options.Target != "remote" {
		return protocolError("remote run/calibrate accepts only --target=remote")
	}
	if !allowNonRemote && (len(gateWorkloads) != 0 || strings.TrimSpace(options.GateWorkloadManifest) != "") {
		return protocolError("remote run/calibrate does not accept --gate-workload selectors")
	}
	if options.Target == "remote" && strings.TrimSpace(options.ConfigPath) == "" {
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
