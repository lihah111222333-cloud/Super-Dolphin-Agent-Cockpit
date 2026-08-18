package main

import (
	"flag"
	"io"
	"os"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	catalog "github.com/lihah111222333-cloud/super-dolphin-agent/scripts/mcp_lsp_workload_catalog"
)

func remoteAuthorityFlagNames() []string {
	return []string{
		"config", "repository", "commit", "tree", "parent", "base", "scenario", "entrypoint",
		"test", "workload", "gate-workload", "gate-workload-manifest", "target", "completion-receipt",
		"local-ref", "remote-ref", "observed-remote", "update-kind", "ledger", "agent-token", "force",
	}
}

func canonicalizeUniqueAuthorityArgs(args []string, names ...string) ([]string, error) {
	canonical, err := catalog.CanonicalizeAuthorityFlags(args, names...)
	if err != nil {
		return nil, err
	}
	if err := catalog.ValidateNoDuplicateAuthorityFlags(canonical, names...); err != nil {
		return nil, err
	}
	return canonical, nil
}

// parseRemoteRunOptions 保持 remote run/calibrate 的 remote-only 兼容边界。
func parseRemoteRunOptions(args []string) (remoteRunOptions, error) {
	return parseRunOptions(args, "remote", false)
}

// parseRunOptions 为 test 提供 local/remote/auto/hybrid 目标解析，remote 入口仍只接受 remote。
func parseRunOptions(args []string, defaultTarget string, allowNonRemote bool) (remoteRunOptions, error) {
	var options remoteRunOptions
	var agentToken string
	args, err := canonicalRunOptionArgs(args, defaultTarget, allowNonRemote)
	if err != nil {
		return options, err
	}
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
	options.Tests = append([]string(nil), tests...)
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
	return options, nil
}

// canonicalRunOptionArgs 在 flag parser 前统一 authority 拼写并执行原始输入门禁。
func canonicalRunOptionArgs(args []string, defaultTarget string, allowNonRemote bool) ([]string, error) {
	canonical, err := catalog.CanonicalizeAuthorityFlags(args, remoteAuthorityFlagNames()...)
	if err != nil {
		return nil, err
	}
	if err := validateRunInputBeforeParse(canonical, defaultTarget, allowNonRemote); err != nil {
		return nil, err
	}
	return canonical, nil
}

// validateRunInputBeforeParse 在 flag 覆盖值前拒绝重复与跨域 authority。
func validateRunInputBeforeParse(args []string, defaultTarget string, allowNonRemote bool) error {
	if err := rejectDuplicateAuthorityFlags(args); err != nil {
		return err
	}
	if !allowNonRemote {
		return nil
	}
	target, err := requestedWorkloadTarget(args, defaultTarget)
	if err != nil {
		return err
	}
	if target == "local" {
		return rejectRawNonRemoteAuthorityFlags(args)
	}
	return nil
}

const singleAuthorityFlagSet = "\x00--config\x00--repository\x00--commit\x00--tree\x00--parent\x00--base\x00--scenario\x00--entrypoint\x00--workload\x00--gate-workload-manifest\x00--target\x00--completion-receipt\x00--local-ref\x00--remote-ref\x00--observed-remote\x00--update-kind\x00--ledger\x00--job-id\x00--agent-token\x00--force\x00"

// rejectDuplicateAuthorityFlags 在标准 flag parser 覆盖值前拒绝重复 authority 输入。
func rejectDuplicateAuthorityFlags(args []string) error {
	seen := make(map[string]int)
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--" {
			break
		}
		name, hasEquals := authorityFlagName(arg)
		if !isSingleAuthorityFlag(name) {
			continue
		}
		if previous, exists := seen[name]; exists {
			return protocolError("duplicate authority flag %s at argument positions %d and %d", name, previous, index)
		}
		seen[name] = index
		if hasEquals || name == "--force" {
			continue
		}
		if index+1 >= len(args) {
			continue
		}
		index++
	}
	return nil
}

func isSingleAuthorityFlag(name string) bool {
	return strings.Contains(singleAuthorityFlagSet, "\x00"+name+"\x00")
}

func authorityFlagName(arg string) (string, bool) {
	if !strings.HasPrefix(arg, "--") {
		return "", false
	}
	name, _, hasEquals := strings.Cut(arg, "=")
	return name, hasEquals
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
	filtered, err := withoutDeferredAgentToken(args)
	return filtered, true, err
}

// withoutDeferredAgentToken 在非 remote 规划解析前移除原始 token 参数；
// 原始参数只会在冻结 remote 子集的后续握手阶段使用。
func withoutDeferredAgentToken(args []string) ([]string, error) {
	canonical, err := catalog.CanonicalizeAuthorityFlags(args, remoteAuthorityFlagNames()...)
	if err != nil {
		return nil, err
	}
	args = canonical
	filtered := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--" {
			filtered = append(filtered, args[index:]...)
			break
		}
		if arg == "--agent-token" {
			if index+1 >= len(args) || args[index+1] == "--" || strings.HasPrefix(args[index+1], "-") {
				return nil, protocolError("--agent-token requires a value")
			}
			index++
			continue
		}
		if value, ok := strings.CutPrefix(arg, "--agent-token="); ok {
			if value == "" {
				return nil, protocolError("--agent-token requires a value")
			}
			continue
		}
		filtered = append(filtered, arg)
	}
	return filtered, nil
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
	if err := validateRemoteTargetNamespace(options, allowNonRemote); err != nil {
		return err
	}
	if allowNonRemote && options.Target == "local" {
		if err := validateNonRemoteAuthorityFlags(flags); err != nil {
			return err
		}
	}
	if err := validateRemoteSelectorNamespace(options, allowNonRemote, gateWorkloads); err != nil {
		return err
	}
	return validateRemoteConfigPath(options)
}

func validateRemoteTargetNamespace(options remoteRunOptions, allowNonRemote bool) error {
	if allowNonRemote || options.Target == "remote" {
		return nil
	}
	return protocolError("remote run/calibrate accepts only --target=remote")
}

// validateRemoteSelectorNamespace 按场景拆分 remote-only 与 test selector 语义。
func validateRemoteSelectorNamespace(options remoteRunOptions, allowNonRemote bool, gateWorkloads remoteStringListFlag) error {
	if allowNonRemote {
		return nil
	}
	if options.Scenario == "test" {
		return validateRemoteTestSelectorNamespace(options)
	}
	return validateRemoteNonTestSelectorNamespace(options, gateWorkloads)
}

// validateRemoteTestSelectorNamespace 保留 remote scenario=test 的 canonical 选择器入口。
func validateRemoteTestSelectorNamespace(options remoteRunOptions) error {
	if len(options.GateWorkloadIDs) != 0 && len(options.Tests) != 0 {
		return protocolError("--gate-workload and --test selectors are mutually exclusive")
	}
	if options.WorkloadID != "" || options.CompletionReceiptPath != "" {
		return protocolError("remote scenario=test does not accept catalog workload authority flags")
	}
	return nil
}

// validateRemoteNonTestSelectorNamespace 拒绝非 test 场景携带 test 或 gate workload 选择器。
func validateRemoteNonTestSelectorNamespace(options remoteRunOptions, gateWorkloads remoteStringListFlag) error {
	if len(gateWorkloads) != 0 || strings.TrimSpace(options.GateWorkloadManifest) != "" {
		return protocolError("remote run/calibrate does not accept --gate-workload selectors")
	}
	if len(options.Tests) != 0 {
		return protocolError("remote run/calibrate does not accept test/workload selectors")
	}
	if options.WorkloadID != "" || options.CompletionReceiptPath != "" {
		return protocolError("remote run/calibrate does not accept catalog workload authority flags")
	}
	return nil
}

const remoteOnlyTestAuthorityFlags = "\x00--config\x00--commit\x00--tree\x00--parent\x00--base\x00--scenario\x00--entrypoint\x00--workload\x00--completion-receipt\x00--local-ref\x00--remote-ref\x00--observed-remote\x00--update-kind\x00--agent-token\x00--force\x00"

func validateNonRemoteAuthorityFlags(flags *flag.FlagSet) error {
	var violation string
	flags.Visit(func(flagValue *flag.Flag) {
		if violation == "" && strings.Contains(remoteOnlyTestAuthorityFlags, "\x00--"+flagValue.Name+"\x00") {
			violation = flagValue.Name
		}
	})
	if violation != "" {
		return protocolError("non-remote target rejects remote-only --%s authority flag", violation)
	}
	return nil
}

func rejectRawNonRemoteAuthorityFlags(args []string) error {
	for _, arg := range args {
		if arg == "--" {
			break
		}
		name, _ := authorityFlagName(arg)
		name = strings.TrimPrefix(name, "--")
		if strings.Contains(remoteOnlyTestAuthorityFlags, "\x00--"+name+"\x00") {
			return protocolError("non-remote target rejects remote-only --%s authority flag", name)
		}
	}
	return nil
}

func validateRemoteConfigPath(options remoteRunOptions) error {
	if options.Target != "remote" || strings.TrimSpace(options.ConfigPath) != "" {
		return nil
	}
	return protocolError("remote run requires --config and valid optional flags")
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
	if err := gatecontract.DecodeCanonicalStrictJSON(data, &config); err != nil {
		return remoteRunConfig{}, err
	}
	if err := config.Validate(); err != nil {
		return remoteRunConfig{}, err
	}
	return config, nil

}
