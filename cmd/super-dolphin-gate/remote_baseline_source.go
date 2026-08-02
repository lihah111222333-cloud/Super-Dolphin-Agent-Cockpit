package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gatehook"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

// remoteBaselineSourceManifestSchemaVersion 标识基线源码清单的当前格式。
const remoteBaselineSourceManifestSchemaVersion uint32 = 1

// remoteBaselineSourceMode 表示远端基线源码的传输策略。
type remoteBaselineSourceMode string

const (
	remoteBaselineSourceFull  remoteBaselineSourceMode = "full"
	remoteBaselineSourceDelta remoteBaselineSourceMode = "delta"
	remoteBaselineSourceReuse remoteBaselineSourceMode = "reuse"
)

// remoteBaselineSourceManifest 记录远端物化基线所需的提交、树和 bundle 摘要。
type remoteBaselineSourceManifest struct {
	SchemaVersion uint32                   `json:"schema_version"`
	Mode          remoteBaselineSourceMode `json:"mode"`
	BaseCommit    string                   `json:"base_commit,omitempty"`
	BaseTree      string                   `json:"base_tree,omitempty"`
	TargetCommit  string                   `json:"target_commit"`
	TargetTree    string                   `json:"target_tree"`
	BundleFile    string                   `json:"bundle_file,omitempty"`
	BundleSHA256  string                   `json:"bundle_sha256,omitempty"`
	BundleSize    int64                    `json:"bundle_size,omitempty"`
}

// remoteBaselineSourceArtifact 保存本地待上传的基线源码产物路径和清单。
type remoteBaselineSourceArtifact struct {
	Manifest       remoteBaselineSourceManifest
	ManifestPath   string
	ManifestSHA256 string
	BundlePath     string
}

// buildRemoteBaselineSourceArtifact 构建完整历史 Anchor 或基于已接受 main 的增量 Git bundle。
func buildRemoteBaselineSourceArtifact(
	ctx context.Context,
	repositoryRoot string,
	accepted remoteci.BaselineState,
	identity remoteci.BaselineIdentity,
	destination string,
) (remoteBaselineSourceArtifact, error) {
	repositoryRoot, err := filepath.Abs(repositoryRoot)
	if err != nil {
		return remoteBaselineSourceArtifact{}, err
	}
	repositoryRoot, err = filepath.EvalSymlinks(repositoryRoot)
	if err != nil {
		return remoteBaselineSourceArtifact{}, err
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return remoteBaselineSourceArtifact{}, fmt.Errorf("create baseline source artifact directory: %w", err)
	}
	manifest, err := selectRemoteBaselineSourceManifest(ctx, repositoryRoot, accepted, identity)
	if err != nil {
		return remoteBaselineSourceArtifact{}, err
	}
	artifact, err := buildRemoteBaselineSourceBundle(
		ctx, repositoryRoot, accepted, identity, destination, manifest,
	)
	if err != nil {
		return remoteBaselineSourceArtifact{}, err
	}
	artifact.ManifestPath = filepath.Join(destination, "source-manifest.json")
	if err := ensureRemoteBaselineSourceAbsent(artifact.ManifestPath); err != nil {
		return remoteBaselineSourceArtifact{}, err
	}
	encoded, err := json.MarshalIndent(artifact.Manifest, "", "  ")
	if err != nil {
		return remoteBaselineSourceArtifact{}, fmt.Errorf("encode baseline source manifest: %w", err)
	}
	if err := os.WriteFile(artifact.ManifestPath, append(encoded, '\n'), 0o600); err != nil {
		return remoteBaselineSourceArtifact{}, fmt.Errorf("write baseline source manifest: %w", err)
	}
	artifact.ManifestSHA256, _, err = remoteBaselineFileDigest(artifact.ManifestPath)
	if err != nil {
		return remoteBaselineSourceArtifact{}, err
	}
	return artifact, nil
}

// selectRemoteBaselineSourceManifest 决定复用、增量或完整 bundle，并绑定目标身份。
func selectRemoteBaselineSourceManifest(
	ctx context.Context,
	repositoryRoot string,
	accepted remoteci.BaselineState,
	identity remoteci.BaselineIdentity,
) (remoteBaselineSourceManifest, error) {
	manifest := remoteBaselineSourceManifest{
		SchemaVersion: remoteBaselineSourceManifestSchemaVersion,
		TargetCommit:  identity.MainCommit,
		TargetTree:    identity.MainTree,
	}
	targetMatches, err := remoteBaselineGitCommitMatchesTree(ctx, repositoryRoot, identity.MainCommit, identity.MainTree)
	if err != nil {
		return remoteBaselineSourceManifest{}, err
	}
	if !targetMatches {
		return remoteBaselineSourceManifest{}, errors.New("remote baseline target commit does not match target tree")
	}
	if accepted.SchemaVersion == 0 {
		manifest.Mode = remoteBaselineSourceFull
		return manifest, nil
	}
	if accepted.SourceHistoryVersion != remoteci.BaselineSourceHistorySchemaVersion {
		return remoteBaselineSourceManifest{}, errors.New("accepted baseline source history cannot be represented as a Delta; full source rebuild is forbidden")
	}
	if accepted.MainCommit == identity.MainCommit && accepted.MainTree == identity.MainTree {
		manifest.Mode = remoteBaselineSourceReuse
		return manifest, nil
	}
	baseMatches, err := remoteBaselineGitCommitMatchesTree(ctx, repositoryRoot, accepted.MainCommit, accepted.MainTree)
	if err != nil {
		return remoteBaselineSourceManifest{}, err
	}
	if !baseMatches {
		return remoteBaselineSourceManifest{}, errors.New("accepted baseline commit does not match its tree; full source rebuild is forbidden")
	}
	manifest.Mode = remoteBaselineSourceDelta
	manifest.BaseCommit = accepted.MainCommit
	manifest.BaseTree = accepted.MainTree
	return manifest, nil
}

// buildRemoteBaselineSourceBundle 生成非复用模式的二进制 Git bundle 和摘要。
func buildRemoteBaselineSourceBundle(
	ctx context.Context,
	repositoryRoot string,
	accepted remoteci.BaselineState,
	identity remoteci.BaselineIdentity,
	destination string,
	manifest remoteBaselineSourceManifest,
) (remoteBaselineSourceArtifact, error) {
	artifact := remoteBaselineSourceArtifact{Manifest: manifest}
	if manifest.Mode == remoteBaselineSourceReuse {
		return artifact, nil
	}
	artifact.BundlePath = filepath.Join(destination, "source.bundle")
	if err := ensureRemoteBaselineSourceAbsent(artifact.BundlePath); err != nil {
		return remoteBaselineSourceArtifact{}, err
	}
	if err := writeRemoteBaselineSourceBundle(
		ctx, repositoryRoot, accepted.MainCommit, identity.MainCommit, artifact.BundlePath, manifest.Mode,
	); err != nil {
		return remoteBaselineSourceArtifact{}, err
	}
	artifact.Manifest.BundleFile = filepath.Base(artifact.BundlePath)
	var err error
	artifact.Manifest.BundleSHA256, artifact.Manifest.BundleSize, err = remoteBaselineFileDigest(artifact.BundlePath)
	if err != nil {
		return remoteBaselineSourceArtifact{}, err
	}
	return artifact, nil
}

// writeRemoteBaselineSourceBundle 按已决定的模式写入并校验 Git bundle。
func writeRemoteBaselineSourceBundle(
	ctx context.Context,
	repositoryRoot string,
	baseCommit string,
	targetCommit string,
	bundlePath string,
	mode remoteBaselineSourceMode,
) error {
	switch mode {
	case remoteBaselineSourceFull:
		return buildRemoteBaselineFullBundle(ctx, repositoryRoot, targetCommit, bundlePath)
	case remoteBaselineSourceDelta:
		return buildRemoteBaselineDeltaBundle(ctx, repositoryRoot, baseCommit, targetCommit, bundlePath)
	default:
		return fmt.Errorf("unsupported baseline source mode %q", mode)
	}
}

// buildRemoteBaselineFullBundle 从目标 commit 构建包含完整可达历史的独立 Git bundle。
func buildRemoteBaselineFullBundle(
	ctx context.Context,
	repositoryRoot string,
	targetCommit string,
	bundlePath string,
) error {
	tempRoot, err := os.MkdirTemp("", "super-dolphin-baseline-full-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempRoot)
	snapshotRoot := filepath.Join(tempRoot, "snapshot")
	if err := remoteBaselineGitRun(ctx, tempRoot, "init", "--quiet", snapshotRoot); err != nil {
		return err
	}
	repositoryURL := (&url.URL{Scheme: "file", Path: repositoryRoot}).String()
	if err := remoteBaselineGitRun(
		ctx,
		snapshotRoot,
		"fetch",
		"--quiet",
		repositoryURL,
		targetCommit,
	); err != nil {
		return err
	}
	if err := remoteBaselineGitRun(ctx, snapshotRoot, "checkout", "--quiet", "--detach", "FETCH_HEAD"); err != nil {
		return err
	}
	if err := remoteBaselineGitRun(ctx, snapshotRoot, "bundle", "create", bundlePath, "HEAD"); err != nil {
		return err
	}
	return remoteBaselineGitRun(ctx, snapshotRoot, "bundle", "verify", bundlePath)
}

// buildRemoteBaselineDeltaBundle 仅封装 accepted base 到目标 commit 的增量对象。
func buildRemoteBaselineDeltaBundle(
	ctx context.Context,
	repositoryRoot string,
	baseCommit string,
	targetCommit string,
	bundlePath string,
) error {
	tempRoot, err := os.MkdirTemp("", "super-dolphin-baseline-delta-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempRoot)
	bareRoot := filepath.Join(tempRoot, "bundle.git")
	if err := remoteBaselineGitRun(
		ctx,
		tempRoot,
		"clone",
		"--quiet",
		"--shared",
		"--bare",
		repositoryRoot,
		bareRoot,
	); err != nil {
		return err
	}
	const targetRef = "refs/heads/super-dolphin-baseline-target"
	if err := remoteBaselineGitRun(ctx, bareRoot, "update-ref", targetRef, targetCommit); err != nil {
		return err
	}
	if err := remoteBaselineGitRun(
		ctx,
		bareRoot,
		"bundle",
		"create",
		bundlePath,
		targetRef,
		"^"+baseCommit,
	); err != nil {
		return err
	}
	if err := remoteBaselineGitRun(ctx, repositoryRoot, "bundle", "verify", bundlePath); err != nil {
		return err
	}
	return verifyRemoteBaselineDeltaBundle(ctx, repositoryRoot, baseCommit, targetCommit, bundlePath)
}

// remoteBaselineGitCommitMatchesTree 确认已接受提交对象存在且仍绑定已验收树。
func remoteBaselineGitCommitMatchesTree(
	ctx context.Context,
	repositoryRoot string,
	commit string,
	tree string,
) (bool, error) {
	command := exec.CommandContext(ctx, "git", "rev-parse", "--verify", "--end-of-options", commit+"^{tree}")
	command.Dir = repositoryRoot
	output, err := command.CombinedOutput()
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) && exitError.ExitCode() == 128 {
			return false, nil
		}
		return false, fmt.Errorf("git rev-parse accepted baseline tree: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)) == tree, nil
}

// verifyRemoteBaselineDeltaBundle 从仅含 accepted base 的仓库重放差量并验证目标对象可达。
func verifyRemoteBaselineDeltaBundle(ctx context.Context, repositoryRoot, baseCommit, targetCommit, bundlePath string) error {
	tempRoot, err := os.MkdirTemp("", "super-dolphin-baseline-delta-verify-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempRoot)
	verificationRoot := filepath.Join(tempRoot, "repository")
	if err := remoteBaselineGitRun(ctx, tempRoot, "init", "--quiet", verificationRoot); err != nil {
		return err
	}
	repositoryURL := (&url.URL{Scheme: "file", Path: repositoryRoot}).String()
	if err := remoteBaselineGitRun(ctx, verificationRoot, "fetch", "--quiet", repositoryURL, baseCommit); err != nil {
		return err
	}
	if err := remoteBaselineGitRun(ctx, verificationRoot, "checkout", "--quiet", "--detach", "FETCH_HEAD"); err != nil {
		return err
	}
	if err := remoteBaselineGitRun(ctx, verificationRoot, "fetch", "--quiet", bundlePath, targetCommit); err != nil {
		return err
	}
	if err := remoteBaselineGitRun(ctx, verificationRoot, "checkout", "--quiet", "--detach", "FETCH_HEAD"); err != nil {
		return err
	}
	return remoteBaselineGitRun(ctx, verificationRoot, "fsck", "--connectivity-only")
}

// remoteBaselineGitRun 在指定目录执行 Git 命令并保留失败输出。
func remoteBaselineGitRun(ctx context.Context, directory string, args ...string) error {
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"git %s: %w: %s",
			strings.Join(args, " "),
			err,
			strings.TrimSpace(string(output)),
		)
	}
	return nil
}

// remoteBaselineFileDigest 计算文件 SHA-256 摘要和字节数。
func remoteBaselineFileDigest(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

// ensureRemoteBaselineSourceAbsent 拒绝覆盖已存在的基线源码产物。
func ensureRemoteBaselineSourceAbsent(path string) error {
	_, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return err
	default:
		return fmt.Errorf("baseline source artifact %q already exists", path)
	}
}

// runRemoteHook 分派远程 Git hook 的严格适配器。
func runRemoteHook(args []string, input io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return protocolError("remote hook requires pre-commit or pre-push")
	}
	switch args[0] {
	case "pre-commit":
		return runRemotePreCommitHook(args[1:], stdout)
	case "pre-push":
		return runRemotePrePushHook(args[1:], input, stdout)
	default:
		return protocolError("remote hook adapter must be pre-commit or pre-push")
	}
}

// runRemotePreCommitHook 将显式 tree/parent 绑定为本地快速门禁。
func runRemotePreCommitHook(args []string, stdout io.Writer) error {
	options, err := parseRemoteRunOptions(args)
	if err != nil {
		return err
	}
	if err := validateRemotePreCommitOptions(options); err != nil {
		return err
	}
	options.Scenario = "commit"
	options.Entrypoint = string(gatecontract.CIEntrypointGitPreCommit)
	result, _, runErr := executeRemoteRun(options)
	if runErr != nil {
		return emitRemoteRunResult(stdout, result, runErr)
	}
	if err := validateAuthoritativeRemoteHookResult(
		result,
		gatecontract.CIEntrypointGitPreCommit,
		gatecontract.ProfileLocalFast,
		options.Tree,
		"",
		"",
		options.RequesterFingerprint,
	); err != nil {
		return infrastructureError("validate remote pre-commit result: %v", err)
	}
	return emitRemoteRunResult(stdout, result, nil)
}

// runRemotePrePushHook 为每个规范化 ref update 运行并验证独立远程门禁。
func runRemotePrePushHook(args []string, input io.Reader, stdout io.Writer) error {
	options, remoteName, remoteURL, err := parseRemotePrePushOptions(args)
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
	result, _, runErr := executeRemoteRun(runOptions)
	if runErr != nil {
		return emitRemoteRunResult(stdout, result, fmt.Errorf("ref update %d: %w", index+1, runErr))
	}
	if err := validateAuthoritativeRemoteHookResult(
		result,
		gatecontract.CIEntrypointGitPrePush,
		gatecontract.ProfilePush,
		submit.Source.SourceTreeSHA,
		runOptions.RemoteName,
		runOptions.RemoteURL,
		runOptions.RequesterFingerprint,
	); err != nil {
		return infrastructureError("validate remote pre-push result %d: %v", index+1, err)
	}
	return emitRemoteRunResult(stdout, result, nil)
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
			return protocolError("remote pre-commit hook requires one --tree and --parent with only storage and shard-limit flags")
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

// parseRemotePrePushOptions 仅接收 pre-push 所需的存储和并发参数。
func parseRemotePrePushOptions(args []string) (remoteRunOptions, string, string, error) {
	var options remoteRunOptions
	var requesterFingerprint string
	flags := flag.NewFlagSet("remote hook pre-push", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.ConfigPath, "config", "", "remote CI config path")
	flags.StringVar(&options.RepositoryRoot, "repository", ".", "Git repository root")
	flags.StringVar(&options.StatePath, "state", "", "accepted baseline state path")
	flags.StringVar(&options.LedgerPath, "ledger", "", "duration ledger path")
	flags.StringVar(
		&requesterFingerprint,
		"requester-fingerprint",
		"",
		"logical requester fingerprint generated by requester create",
	)
	flags.UintVar(&options.MaxShards, "max-shards", 0, "override configured maximum shard count")
	if err := flags.Parse(args); err != nil {
		return options, "", "", protocolError("parse remote pre-push flags: %v", err)
	}
	if flags.NArg() != 2 || strings.TrimSpace(options.ConfigPath) == "" || options.MaxShards > gatecontract.MaxContainerShards {
		return options, "", "", protocolError("remote pre-push requires --config and exact remote name and URL")
	}
	if err := normalizeRemoteSQLiteAuthority(options.ConfigPath, &options.StatePath, &options.LedgerPath); err != nil {
		return options, "", "", err
	}
	requester, err := resolveRequesterFingerprint(
		requesterFingerprint,
		os.Getenv(gatecontract.RequesterFingerprintEnvironment),
	)
	if err != nil {
		return options, "", "", protocolError("resolve requester fingerprint: %v", err)
	}
	options.RequesterFingerprint = requester
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
	requesterFingerprint gatecontract.RequesterFingerprint,
) error {
	if !result.Authoritative || result.Entrypoint != entrypoint || result.Profile != profile ||
		result.SourceTreeSHA != tree || result.RemoteName != remoteName || result.RemoteURL != remoteURL ||
		result.RequesterFingerprint != requesterFingerprint ||
		result.Status != gatecontract.ResultStatusPassed || !result.CleanupComplete {
		return errors.New("remote hook result is incomplete or bound to a different invocation")
	}
	expectedBinding, err := remoteci.CandidateTestBinaryReceiptBindingDigestFromBuilds(result.CandidateTestBinaryBuilds, result.SourceTreeSHA)
	if err != nil || result.CandidateTestBinaryReceiptBindingDigest != expectedBinding {
		return errors.New("remote hook result candidate test binary binding is invalid or drifted")
	}
	return nil
}

const remoteLegacyBaselineStateSchemaVersionV2 uint32 = 2

var remoteLegacyDataCacheIDPattern = regexp.MustCompile(`^edc-[a-z0-9]+$`)

type remoteLegacyBaselineStateV2 struct {
	SchemaVersion          uint32                     `json:"schema_version"`
	Generation             uint64                     `json:"generation"`
	MainCommit             string                     `json:"main_commit"`
	MainTree               string                     `json:"main_tree"`
	Platform               string                     `json:"platform"`
	PolicyDigest           string                     `json:"policy_digest"`
	ToolchainDigest        string                     `json:"toolchain_digest"`
	RuntimeImage           string                     `json:"runtime_image"`
	BaselineManifestDigest string                     `json:"baseline_manifest_digest"`
	DataCacheID            string                     `json:"data_cache_id"`
	DataCacheBucket        string                     `json:"data_cache_bucket"`
	DataCachePath          string                     `json:"data_cache_path"`
	DataCacheSizeGiB       int                        `json:"data_cache_size_gib"`
	SourceObjectPrefix     string                     `json:"source_object_prefix"`
	CreatedAt              time.Time                  `json:"created_at"`
	AcceptedAt             time.Time                  `json:"accepted_at"`
	Previous               *remoteci.BaselineCacheRef `json:"previous,omitempty"`
}

type remoteLegacyBaselineMigration struct {
	generation uint64
	references []remoteci.BaselineCacheRef
}

// loadRemoteBaselineStateForRefresh 只从 SQLite 读取决策状态；旧 JSON 仅可在空账本时严格导入一次。
func loadRemoteBaselineStateForRefresh(ledgerPath, legacyPath string, config remoteRunConfig) (remoteci.BaselineState, *remoteLegacyBaselineMigration, error) {
	databasePath := remoteBaselineDatabasePath(ledgerPath)
	stored, err := loadStoredRemoteBaselineState(databasePath)
	if err == nil {
		return stored.state, stored.legacy, nil
	}
	if !errors.Is(err, errRemoteBaselineStateNotFound) && !errors.Is(err, os.ErrNotExist) {
		return remoteci.BaselineState{}, nil, err
	}
	if err := validateRemoteBaselineLegacyFile(legacyPath); errors.Is(err, os.ErrNotExist) {
		return remoteci.BaselineState{}, nil, nil
	} else if err != nil {
		return remoteci.BaselineState{}, nil, err
	}
	data, err := os.ReadFile(legacyPath)
	if errors.Is(err, os.ErrNotExist) {
		return remoteci.BaselineState{}, nil, nil
	}
	if err != nil {
		return remoteci.BaselineState{}, nil, fmt.Errorf("read legacy remote baseline JSON: %w", err)
	}
	schemaVersion, err := remoteBaselineStateSchemaVersion(data)
	if err != nil {
		return remoteci.BaselineState{}, nil, err
	}
	if schemaVersion == remoteci.BaselineStateSchemaVersion ||
		schemaVersion == remoteci.BaselineStatePreviousSchemaVersion ||
		schemaVersion == 4 {
		var state remoteci.BaselineState
		if err := decodeRemoteBaselineRefreshJSON(data, &state); err != nil {
			return remoteci.BaselineState{}, nil, fmt.Errorf("decode legacy remote baseline state: %w", err)
		}
		if err := state.Validate(); err != nil {
			return remoteci.BaselineState{}, nil, fmt.Errorf("validate legacy remote baseline state: %w", err)
		}
		if err := writeRemoteBaselineState(databasePath, state); err != nil {
			return remoteci.BaselineState{}, nil, fmt.Errorf("import legacy remote baseline JSON into SQLite: %w", err)
		}
		if err := retireRemoteBaselineLegacyFile(legacyPath); err != nil {
			return remoteci.BaselineState{}, nil, fmt.Errorf("retire imported legacy remote baseline JSON: %w", err)
		}
		return state, nil, nil
	}
	if schemaVersion != remoteLegacyBaselineStateSchemaVersionV2 {
		return remoteci.BaselineState{}, nil, fmt.Errorf("remote baseline state schema %d cannot be refreshed", schemaVersion)
	}
	var legacy remoteLegacyBaselineStateV2
	if err := decodeRemoteBaselineRefreshJSON(data, &legacy); err != nil {
		return remoteci.BaselineState{}, nil, fmt.Errorf("decode legacy remote baseline state: %w", err)
	}
	migration, err := newRemoteLegacyBaselineMigration(config, legacy)
	if err != nil {
		return remoteci.BaselineState{}, nil, err
	}
	if err := storeRemoteBaselineState(databasePath, remoteBaselineStoredState{legacy: migration}); err != nil {
		return remoteci.BaselineState{}, nil, fmt.Errorf("import legacy remote baseline marker into SQLite: %w", err)
	}
	if err := retireRemoteBaselineLegacyFile(legacyPath); err != nil {
		return remoteci.BaselineState{}, nil, fmt.Errorf("retire imported legacy remote baseline JSON: %w", err)
	}
	return remoteci.BaselineState{}, migration, nil
}

func retireRemoteBaselineLegacyFile(path string) error {
	if err := os.Remove(path); err != nil {
		return err
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return errors.New("legacy remote baseline JSON remains after retirement")
	}
	return nil
}

func validateRemoteBaselineLegacyFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 {
		return errors.New("legacy remote baseline JSON must be an owner-only regular file")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) {
		return errors.New("legacy remote baseline JSON owner is invalid")
	}
	return nil
}

// remoteBaselineStateSchemaVersion 严格读取状态对象中的唯一 schema_version。
func remoteBaselineStateSchemaVersion(data []byte) (uint32, error) {
	var envelope struct {
		SchemaVersion uint32 `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return 0, err
	}
	if envelope.SchemaVersion == 0 {
		return 0, errors.New("remote baseline state schema_version is invalid")
	}
	return envelope.SchemaVersion, nil
}

// decodeRemoteBaselineRefreshJSON 拒绝旧状态中的未知字段和多个 JSON 值。
func decodeRemoteBaselineRefreshJSON(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("remote baseline state contains multiple JSON values")
		}
		return err
	}
	return nil
}

// newRemoteLegacyBaselineMigration 校验 v2 当前代和前一代资源均属于当前远程配置。
func newRemoteLegacyBaselineMigration(config remoteRunConfig, legacy remoteLegacyBaselineStateV2) (*remoteLegacyBaselineMigration, error) {
	if legacy.SchemaVersion != remoteLegacyBaselineStateSchemaVersionV2 || legacy.Generation == 0 || legacy.Generation == math.MaxUint64 {
		return nil, errors.New("legacy remote baseline schema or generation is invalid")
	}
	current := remoteci.BaselineCacheRef{
		Generation: legacy.Generation, DataCacheID: legacy.DataCacheID,
		DataCacheBucket: legacy.DataCacheBucket, DataCachePath: legacy.DataCachePath,
		SourceObjectPrefix: legacy.SourceObjectPrefix, AcceptedAt: legacy.AcceptedAt,
	}
	if err := validateRemoteLegacyBaselineReference(config, current); err != nil {
		return nil, fmt.Errorf("legacy remote baseline current generation: %w", err)
	}
	references := make([]remoteci.BaselineCacheRef, 0, 2)
	if legacy.Previous != nil {
		if legacy.Previous.Generation >= legacy.Generation {
			return nil, errors.New("legacy remote baseline previous generation is not older than current")
		}
		if err := validateRemoteLegacyBaselineReference(config, *legacy.Previous); err != nil {
			return nil, fmt.Errorf("legacy remote baseline previous generation: %w", err)
		}
		references = append(references, *legacy.Previous)
	}
	references = append(references, current)
	return &remoteLegacyBaselineMigration{generation: legacy.Generation, references: references}, nil
}

// validateRemoteLegacyBaselineReference 绑定旧资源到当前 DataCache 路径和 OSS generation 前缀。
func validateRemoteLegacyBaselineReference(config remoteRunConfig, reference remoteci.BaselineCacheRef) error {
	if reference.Generation == 0 ||
		!remoteLegacyDataCacheIDPattern.MatchString(reference.DataCacheID) ||
		reference.DataCacheBucket != config.DataCache.Bucket ||
		reference.DataCachePath != remoteBaselineCachePath(config, reference.Generation) ||
		reference.SourceObjectPrefix != remoteBaselineSourcePrefix(config, reference.Generation) ||
		reference.AcceptedAt.IsZero() ||
		reference.AcceptedAt.Location() != time.UTC ||
		strings.TrimSpace(reference.DataCacheID) != reference.DataCacheID {
		return errors.New("legacy remote baseline resource identity is invalid")
	}
	return nil
}

// nextRemoteBaselineGeneration 延续旧状态的代际编号，避免复用既有云资源名称。
func nextRemoteBaselineGeneration(accepted remoteci.BaselineState, legacy *remoteLegacyBaselineMigration) (uint64, error) {
	generation := accepted.Generation
	if legacy != nil {
		generation = legacy.generation
	}
	if generation == math.MaxUint64 {
		return 0, errors.New("remote baseline generation is exhausted")
	}
	return generation + 1, nil
}

// cleanupLegacyRemoteBaselines 在新 DataCache 可用后幂等删除所有不兼容的 v2 资源。
func cleanupLegacyRemoteBaselines(ctx context.Context, cache remoteBaselineDataCacheClient, store remoteBaselineOSSStore, legacy *remoteLegacyBaselineMigration) error {
	if legacy == nil {
		return nil
	}
	for _, reference := range legacy.references {
		if err := removeRetiredRemoteDataCache(ctx, cache, reference); err != nil {
			return fmt.Errorf("delete legacy DataCache generation %d: %w", reference.Generation, err)
		}
		if err := store.DeletePrefix(ctx, reference.SourceObjectPrefix); err != nil {
			return fmt.Errorf("delete legacy OSS generation %d: %w", reference.Generation, err)
		}
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
