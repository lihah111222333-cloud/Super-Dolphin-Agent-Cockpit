package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gatehook"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

// runRemoteHook 分派远程 Git hook 的严格适配器。
func runRemoteHook(args []string, input io.Reader, stdout io.Writer, progressWriters ...io.Writer) error {
	if len(args) == 0 {
		return protocolError("remote hook requires pre-commit or pre-push")
	}
	switch args[0] {
	case "pre-commit":
		return runRemotePreCommitHook(args[1:], stdout, progressWriters...)
	case "pre-push":
		return runRemotePrePushHook(args[1:], input, stdout, progressWriters...)
	default:
		return protocolError("remote hook adapter must be pre-commit or pre-push")
	}
}

// runRemotePreCommitHook 将显式 tree/parent 绑定为远程 ECI 快速门禁。
func runRemotePreCommitHook(args []string, stdout io.Writer, progressWriters ...io.Writer) error {
	if err := requireRemoteCIAgentToken([]string{"remote", "hook", "pre-commit"}, args, stdout); err != nil {
		return err
	}
	options, err := parseRemoteRunOptions(args)
	if err != nil {
		return err
	}
	if err := validateRemotePreCommitOptions(options); err != nil {
		return err
	}
	options.Scenario = "commit"
	options.Entrypoint = string(gatecontract.CIEntrypointGitPreCommit)
	progress, err := newRemoteProgressObserver(progressWriters...)
	if err != nil {
		return err
	}
	options.ProgressObserver = progress
	result, input, runErr := executeRemoteRun(options)
	runErr = errors.Join(runErr, remoteci.ProgressError(progress))
	if runErr != nil {
		return emitRemoteRunResult(stdout, input.LedgerStore, result, runErr)
	}
	if err := validateAuthoritativeRemoteHookResult(
		result,
		gatecontract.CIEntrypointGitPreCommit,
		gatecontract.ProfileLocalFast,
		options.Tree,
		"",
		"",
		options.AgentTokenDigest,
	); err != nil {
		return infrastructureError("validate remote pre-commit result: %v", err)
	}
	return emitRemoteRunResult(stdout, input.LedgerStore, result, nil)
}

// runRemotePrePushHook 为每个规范化 ref update 运行并验证独立远程门禁。
func runRemotePrePushHook(args []string, input io.Reader, stdout io.Writer, progressWriters ...io.Writer) error {
	agentTokenDigest, err := requireRemoteCIAgentTokenDigest([]string{"remote", "hook", "pre-push"}, args, stdout)
	if err != nil {
		return err
	}
	options, remoteName, remoteURL, err := parseRemotePrePushOptions(args, agentTokenDigest)
	if err != nil {
		return err
	}
	options.RemoteName, options.RemoteURL, err = canonicalRemoteIdentity(remoteName, remoteURL)
	if err != nil {
		return protocolError("canonicalize remote pre-push identity: %v", err)
	}
	deliveryID, err := newHookDeliveryID()
	if err != nil {
		return infrastructureError("create remote pre-push delivery identity: %v", err)
	}
	progress, err := newRemoteProgressObserver(progressWriters...)
	if err != nil {
		return err
	}
	options.ProgressObserver = progress
	requests, err := gatehook.NormalizePrePush(context.Background(), options.RepositoryRoot, deliveryID, input)
	if err != nil {
		return sourceError("normalize remote pre-push refs: %v", err)
	}
	for index, request := range requests {
		if err := runRemotePrePushRequest(options, request, index, stdout); err != nil {
			return err
		}
	}
	return nil
}

// runRemotePrePushRequest 执行并验证单个规范化 ref update 的权威远程门禁。
func runRemotePrePushRequest(options remoteRunOptions, request gatehook.Request, index int, stdout io.Writer) error {
	submit, err := validateRemotePrePushRequest(request, index)
	if err != nil {
		return err
	}
	runOptions, err := remotePushRunOptions(options, submit)
	if err != nil {
		return protocolError("remote pre-push request %d: %v", index+1, err)
	}
	result, input, runErr := executeRemoteRun(runOptions)
	runErr = errors.Join(runErr, remoteci.ProgressError(runOptions.ProgressObserver))
	if runErr != nil {
		return emitRemoteRunResult(stdout, input.LedgerStore, result, fmt.Errorf("ref update %d: %w", index+1, runErr))
	}
	if err := validateAuthoritativeRemoteHookResult(
		result,
		gatecontract.CIEntrypointGitPrePush,
		gatecontract.ProfilePush,
		submit.Source.SourceTreeSHA,
		runOptions.RemoteName,
		runOptions.RemoteURL,
		runOptions.AgentTokenDigest,
	); err != nil {
		return infrastructureError("validate remote pre-push result %d: %v", index+1, err)
	}
	return emitRemoteRunResult(stdout, input.LedgerStore, result, nil)
}

// validateRemotePreCommitOptions 拒绝 pre-commit 不拥有的远程运行参数。
func validateRemotePreCommitOptions(options remoteRunOptions) error {
	invalid := []bool{
		options.Tree == "",
		options.ParentCommit == "",
		options.Commit != "",
		options.Base != "",
		options.Profile != "",
		options.Scenario != "",
		options.Entrypoint != "",
		len(options.Tests) != 0,
		options.LocalRef != "",
		options.RemoteRef != "",
		options.ObservedRemote != "",
		options.UpdateKind != "",
	}
	for _, value := range invalid {
		if value {
			return protocolError("remote pre-commit hook requires one --tree and --parent with only storage flags")
		}
	}
	return nil
}

// validateRemotePrePushRequest 只接受规范化后的范围提交请求。
func validateRemotePrePushRequest(request gatehook.Request, index int) (gatehook.SubmitRequest, error) {
	if request.Kind != gatehook.RequestKindSubmit || request.Submit == nil ||
		request.Submit.Entrypoint != gatecontract.CIEntrypointGitPrePush ||
		request.Submit.Source.Kind != gatecontract.SourceKindRange ||
		request.Submit.Source.Range == nil {
		return gatehook.SubmitRequest{}, protocolError(
			"remote pre-push request %d is not a canonical range submission", index+1,
		)
	}
	return *request.Submit, nil
}

// parseRemotePrePushOptions 仅接收 pre-push 所需的存储参数和前置验证摘要。
func parseRemotePrePushOptions(args []string, agentTokenDigest string) (remoteRunOptions, string, string, error) {
	var options remoteRunOptions
	if err := cicontract.ValidateAgentTokenDigest(agentTokenDigest); err != nil {
		return options, "", "", protocolError("validate CI agent token digest: %v", err)
	}
	flags := flag.NewFlagSet("remote hook pre-push", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.ConfigPath, "config", "", "remote CI config path")
	flags.StringVar(&options.RepositoryRoot, "repository", ".", "Git repository root")
	flags.StringVar(&options.LedgerPath, "ledger", "", "remote baseline and duration ledger SQLite authority path")
	flags.BoolVar(&options.Force, "force", false, "force execution of every shardable workload and bypass PASS reuse")
	if err := flags.Parse(args); err != nil {
		return options, "", "", protocolError("parse remote pre-push flags: %v", err)
	}
	if flags.NArg() != 2 || strings.TrimSpace(options.ConfigPath) == "" {
		return options, "", "", protocolError("remote pre-push requires --config and exact remote name and URL")
	}
	if err := normalizeRemoteSQLiteAuthority(options.ConfigPath, &options.LedgerPath); err != nil {
		return options, "", "", err
	}
	options.AgentTokenDigest = agentTokenDigest
	return options, flags.Arg(0), flags.Arg(1), nil
}

// canonicalRemoteIdentity 将 Git hook 传入的远端标识规范化为可审计且无凭证的身份。
func canonicalRemoteIdentity(remoteName string, remoteURL string) (string, string, error) {
	if remoteName == "" || remoteName != strings.TrimSpace(remoteName) {
		return "", "", errors.New("remote name must be non-empty and exact")
	}
	parsed, err := parseCanonicalRemoteURL(remoteURL)
	if err != nil {
		return "", "", err
	}
	if err := validateCanonicalRemoteCredentials(parsed); err != nil {
		return "", "", err
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.RawPath = ""
	return remoteName, parsed.String(), nil
}

// parseCanonicalRemoteURL 拒绝非绝对、含查询或片段的远端 URL。
func parseCanonicalRemoteURL(remoteURL string) (*url.URL, error) {
	if remoteURL == "" || remoteURL != strings.TrimSpace(remoteURL) {
		return nil, errors.New("remote URL must be non-empty and exact")
	}
	if strings.ContainsAny(remoteURL, "?#") {
		return nil, errors.New("remote URL must not contain a fragment or query")
	}
	parsed, err := url.ParseRequestURI(remoteURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("remote URL must be absolute")
	}
	if parsed.Fragment != "" || parsed.RawQuery != "" {
		return nil, errors.New("remote URL must not contain a fragment or query")
	}
	return parsed, nil
}

// validateCanonicalRemoteCredentials 只接受 Git SSH 的无密码 git 用户信息。
func validateCanonicalRemoteCredentials(parsed *url.URL) error {
	if parsed.User != nil {
		username := parsed.User.Username()
		_, hasPassword := parsed.User.Password()
		if parsed.Scheme != "ssh" || username != "git" || hasPassword {
			return errors.New("remote URL must be credential-free")
		}
	}
	return nil
}

// remotePushRunOptions 将规范化 ref update 绑定到远程 push 运行参数。
func remotePushRunOptions(options remoteRunOptions, submit gatehook.SubmitRequest) (remoteRunOptions, error) {
	if err := submit.Validate(); err != nil {
		return remoteRunOptions{}, err
	}
	update := submit.Source.Range
	if update == nil {
		return remoteRunOptions{}, errors.New("remote push source range is missing")
	}
	options.RepositoryRoot = submit.Repository.WorktreeRoot
	options.Commit = update.HeadSHA
	options.Scenario = "push"
	options.Entrypoint = string(submit.Entrypoint)
	options.LocalRef = update.LocalRef
	options.RemoteRef = update.RemoteRef
	options.ObservedRemote = update.ObservedRemoteSHA
	options.UpdateKind = string(update.UpdateKind)
	switch update.BaseKind {
	case gatecontract.BaseKindCommit:
		options.Base = update.BaseSHA
	case gatecontract.BaseKindEmptyTree:
		options.Base = ""
	default:
		return remoteRunOptions{}, fmt.Errorf("unsupported push base kind %q", update.BaseKind)
	}
	return options, nil
}

// validateAuthoritativeRemoteHookResult 确认 hook 只接受当前调用的完整权威成功回执。
func validateAuthoritativeRemoteHookResult(
	result remoteci.RunResult,
	entrypoint gatecontract.CIEntrypointID,
	profile gatecontract.Profile,
	tree string,
	remoteName string,
	remoteURL string,
	agentTokenDigest string,
) error {
	if !result.Authoritative || result.Entrypoint != entrypoint || result.Profile != profile ||
		result.SourceTreeSHA != tree || result.RemoteName != remoteName || result.RemoteURL != remoteURL ||
		result.AgentTokenDigest != agentTokenDigest ||
		result.Status != gatecontract.ResultStatusPassed || !result.CleanupComplete {
		return errors.New("remote hook result is incomplete or bound to a different invocation")
	}
	return nil
}

// remoteGitOutput 在指定仓库执行只读 Git 查询并保留可诊断错误。
func remoteGitOutput(repositoryRoot string, args ...string) (string, error) {
	command := exec.Command("git", args...)
	command.Dir = repositoryRoot
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}
