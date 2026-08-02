package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

type remoteResolvedTarget struct {
	commit     string
	tree       string
	revision   string
	bundleBase string
	sourceBase string
}

// resolveRemoteRunInput 将已验证的 Git source、基线、账本和 workload 组装为运行输入。
func resolveRemoteRunInput(
	options remoteRunOptions,
	config remoteRunConfig,
	state remoteci.BaselineState,
	runnerIdentity string,
) (remoteci.RunInput, error) {
	if err := validateRunnableRemoteBaseline(config, state); err != nil {
		return remoteci.RunInput{}, err
	}
	repositoryRoot, scenario, profile, target, source, err := resolveRemoteRunSource(options)
	if err != nil {
		return remoteci.RunInput{}, err
	}
	entrypoint := remoteRunEntrypoint(options)
	ledger, ledgerStore, err := loadRemoteRunLedger(
		options,
		scenario,
		state,
		runnerIdentity,
	)
	if err != nil {
		return remoteci.RunInput{}, err
	}
	inventory, err := buildRemoteRunInventory(repositoryRoot, target, scenario, options.Tests, state.Platform)
	if err != nil {
		return remoteci.RunInput{}, err
	}
	candidateGateSource, candidateGateToolchain, reuseBaselineGate, err := resolveRemoteCandidateGateIdentity(
		repositoryRoot,
		target.tree,
		state.MainTree,
	)
	if err != nil {
		return remoteci.RunInput{}, err
	}
	return remoteci.RunInput{
		RepositoryRoot:       repositoryRoot,
		RemoteName:           options.RemoteName,
		RemoteURL:            options.RemoteURL,
		RequesterFingerprint: options.RequesterFingerprint,
		Commit:               target.commit, Tree: target.tree, Base: target.bundleBase,
		RunnerBaseCommit: state.MainCommit, RunnerBaseTree: state.MainTree,
		Source: source, Profile: profile, Entrypoint: entrypoint,
		Platform: state.Platform, PolicyDigest: state.PolicyDigest,
		ToolchainDigest: state.ToolchainDigest,
		LedgerSnapshot:  ledger,
		LedgerStore:     ledgerStore,
		Inventory:       inventory,
		SelectedTests:   scenario == "test",
		Calibration:     options.Calibration,
		RunnerImage:     state.RuntimeImage, RunnerIdentityDigest: runnerIdentity,
		ImageCacheID:                 state.ImageCacheID,
		BaselineManifestDigest:       state.BaselineManifestDigest,
		RunnerConfigDigest:           remoteRuntimeImageDigest(state.RuntimeImage),
		GateBinarySHA256:             state.GateBinarySHA256,
		CandidateGateSourceSHA256:    candidateGateSource,
		CandidateGateToolchainSHA256: candidateGateToolchain,
		ReuseBaselineGateCLI:         reuseBaselineGate,
		RuntimeSeedSHA256:            state.RuntimeSeedSHA256, ForceRerun: options.ForceRerun,
		OCIProjectCache: state.OCIProjectCache,
	}, nil
}

// resolveRemoteCandidateGateIdentity 比较候选与已接受基线的精确 CLI 编译闭包。
func resolveRemoteCandidateGateIdentity(repositoryRoot, candidateTree, baselineTree string) (string, string, bool, error) {
	ctx := context.Background()
	candidateSource, candidateToolchain, _, err := remoteci.LoadGateCLICompileClosure(ctx, repositoryRoot, candidateTree)
	if err != nil {
		return "", "", false, fmt.Errorf("resolve candidate gate CLI compile closure: %w", err)
	}
	if candidateTree == baselineTree {
		return candidateSource, candidateToolchain, true, nil
	}
	baselineSource, baselineToolchain, _, err := remoteci.LoadGateCLICompileClosure(ctx, repositoryRoot, baselineTree)
	if err != nil {
		return "", "", false, fmt.Errorf("resolve baseline gate CLI compile closure: %w", err)
	}
	reuse := candidateSource == baselineSource && candidateToolchain == baselineToolchain
	return candidateSource, candidateToolchain, reuse, nil
}

// resolveRemoteRunSource 固定仓库根、场景、目标对象及 source 契约。
func resolveRemoteRunSource(options remoteRunOptions) (string, string, gatecontract.Profile, remoteResolvedTarget, gatecontract.SourceSpec, error) {
	repositoryRoot, err := filepath.Abs(options.RepositoryRoot)
	if err != nil {
		return "", "", "", remoteResolvedTarget{}, gatecontract.SourceSpec{}, err
	}
	repositoryRoot, err = filepath.EvalSymlinks(repositoryRoot)
	if err != nil {
		return "", "", "", remoteResolvedTarget{}, gatecontract.SourceSpec{}, fmt.Errorf("resolve remote CI repository root: %w", err)
	}
	scenario, profile, err := resolveRemoteScenario(options)
	if err != nil {
		return "", "", "", remoteResolvedTarget{}, gatecontract.SourceSpec{}, err
	}
	target, err := resolveRemoteRunTarget(repositoryRoot, scenario, options)
	if err != nil {
		return "", "", "", remoteResolvedTarget{}, gatecontract.SourceSpec{}, err
	}
	objectFormat, err := remoteGitOutput(repositoryRoot, "rev-parse", "--show-object-format")
	if err != nil {
		return "", "", "", remoteResolvedTarget{}, gatecontract.SourceSpec{}, err
	}
	source, err := remoteGateSource(options, profile, objectFormat, target.commit, target.tree, target.sourceBase)
	return repositoryRoot, scenario, profile, target, source, err
}

// loadRemoteRunLedger 读取账本并验证它与当前已接受基线可比较。
func loadRemoteRunLedger(
	options remoteRunOptions,
	scenario string,
	state remoteci.BaselineState,
	runnerIdentity string,
) (gatecontract.DurationLedgerSnapshot, *gatecontract.DurationLedgerStore, error) {
	store, err := gatecontract.NewDurationLedgerStore(options.LedgerPath)
	if err != nil {
		return gatecontract.DurationLedgerSnapshot{}, nil, err
	}
	ledger, err := store.LoadPlanning(gatecontract.PlanningContext{
		Platform:         state.Platform,
		Runner:           runnerIdentity,
		Toolchain:        state.ToolchainDigest,
		TargetDurationMS: gatecontract.FullCITargetDurationMS,
	})
	if err != nil {
		return gatecontract.DurationLedgerSnapshot{}, nil, err
	}
	if err := validateRemoteDurationCalibration(options, scenario, state, runnerIdentity, ledger.Ledger); err != nil {
		return gatecontract.DurationLedgerSnapshot{}, nil, err
	}
	return ledger, store, nil
}

// buildRemoteRunInventory 从固定 revision 构建 inventory 并应用 test 场景选择器。
func buildRemoteRunInventory(repositoryRoot string, target remoteResolvedTarget, scenario string, selectors []string, platform string) (gatecontract.WorkloadInventory, error) {
	inventory, err := remoteci.BuildWorkloadInventory(context.Background(), repositoryRoot, target.revision, target.sourceBase, platform)
	if err != nil {
		return gatecontract.WorkloadInventory{}, err
	}
	if scenario != "test" {
		return inventory, nil
	}
	return selectRemoteTests(inventory, selectors)
}

// remoteRunEntrypoint 解析入口标识。
func remoteRunEntrypoint(options remoteRunOptions) gatecontract.CIEntrypointID {
	entrypoint := gatecontract.CIEntrypointManualCLI
	if options.Entrypoint != "" {
		entrypoint = gatecontract.CIEntrypointID(options.Entrypoint)
	}
	return entrypoint
}

// resolveRemoteRunTarget 固定显式树或提交 source 的权威对象及其基线。
func resolveRemoteRunTarget(
	repositoryRoot string,
	scenario string,
	options remoteRunOptions,
) (remoteResolvedTarget, error) {
	if options.Tree != "" {
		return resolveRemoteTreeTarget(repositoryRoot, scenario, options)
	}
	return resolveRemoteCommitTarget(repositoryRoot, scenario, options)
}

// resolveRemoteTreeTarget 校验显式树场景并解析其父提交身份。
func resolveRemoteTreeTarget(
	repositoryRoot string,
	scenario string,
	options remoteRunOptions,
) (remoteResolvedTarget, error) {
	if scenario == "push" || scenario == "full" {
		return remoteResolvedTarget{}, fmt.Errorf(
			"remote CI scenario %q requires a commit source",
			scenario,
		)
	}
	if options.ParentCommit == "" || options.Base != "" {
		return remoteResolvedTarget{}, errors.New(
			"explicit tree source requires --parent and does not accept --base",
		)
	}
	tree, err := remoteGitOutput(
		repositoryRoot,
		"rev-parse",
		"--verify",
		"--end-of-options",
		options.Tree+"^{tree}",
	)
	if err != nil {
		return remoteResolvedTarget{}, err
	}
	parent, err := remoteGitOutput(
		repositoryRoot,
		"rev-parse",
		"--verify",
		"--end-of-options",
		options.ParentCommit+"^{commit}",
	)
	if err != nil {
		return remoteResolvedTarget{}, err
	}
	return remoteResolvedTarget{
		tree: tree, revision: tree, bundleBase: parent, sourceBase: parent,
	}, nil
}

// resolveRemoteCommitTarget 解析提交、树及场景对应的 bundle/source 基线。
func resolveRemoteCommitTarget(
	repositoryRoot string,
	scenario string,
	options remoteRunOptions,
) (remoteResolvedTarget, error) {
	if options.ParentCommit != "" {
		return remoteResolvedTarget{}, errors.New("--parent is only valid with --tree")
	}
	commit, err := remoteGitOutput(
		repositoryRoot,
		"rev-parse",
		"--verify",
		"--end-of-options",
		options.Commit+"^{commit}",
	)
	if err != nil {
		return remoteResolvedTarget{}, err
	}
	bundleBase, sourceBase, err := resolveRemoteBases(repositoryRoot, commit, scenario, options)
	if err != nil {
		return remoteResolvedTarget{}, err
	}
	tree, err := remoteGitOutput(
		repositoryRoot,
		"rev-parse",
		"--verify",
		"--end-of-options",
		commit+"^{tree}",
	)
	if err != nil {
		return remoteResolvedTarget{}, err
	}
	return remoteResolvedTarget{
		commit: commit, tree: tree, revision: commit,
		bundleBase: bundleBase, sourceBase: sourceBase,
	}, nil
}

// validateRemoteDurationCalibration 确保非测试运行使用与已接受基线一致的首代校准。
func validateRemoteDurationCalibration(
	options remoteRunOptions,
	scenario string,
	state remoteci.BaselineState,
	runnerIdentity string,
	ledger gatecontract.DurationLedger,
) error {
	if options.Calibration || scenario == "test" {
		return nil
	}
	calibration := ledger.Calibration
	if calibration == nil {
		return errors.New("remote CI duration ledger has not completed first-generation calibration")
	}
	if !remoteDurationCalibrationMatches(calibration, state, runnerIdentity) {
		return errors.New("remote CI duration calibration does not match the accepted runner baseline")
	}
	return nil
}

func validRemoteRuntimeImage(reference string) bool {
	name, digest, ok := strings.Cut(reference, "@")
	return ok && strings.TrimSpace(name) != "" &&
		len(digest) == len("sha256:")+64 &&
		strings.HasPrefix(digest, "sha256:") &&
		strings.Trim(digest[len("sha256:"):], "0123456789abcdef") == ""
}

func remoteRuntimeImageDigest(reference string) string {
	_, digest, ok := strings.Cut(reference, "@")
	if !ok {
		return ""
	}
	return digest
}

func remoteRuntimeImageRepository(reference string) string {
	repository, _, _ := strings.Cut(reference, "@")
	return repository
}

// validateAcceptedRemoteBaseline 绑定运行配置与已接受的 OCI 基线镜像。
func validateAcceptedRemoteBaseline(config remoteRunConfig, state remoteci.BaselineState) error {
	if err := state.Validate(); err != nil {
		return err
	}
	if remoteRuntimeImageRepository(state.RuntimeImage) != config.OCICache.RegistryRepository {
		return errors.New("accepted baseline runtime image does not match configured OCI cache repository")
	}
	if state.OCIProjectCache == nil {
		return errors.New("accepted baseline must use OCI project cache")
	}
	return state.OCIProjectCache.ValidateForBaseline(state.MainTree, state.ToolchainDigest, state.Platform, state.RuntimeImage)
}

// validateRunnableRemoteBaseline rejects an accepted baseline whose OCI identity cannot run.
func validateRunnableRemoteBaseline(config remoteRunConfig, state remoteci.BaselineState) error {
	if err := validateAcceptedRemoteBaseline(config, state); err != nil {
		return err
	}
	return nil
}

// resolveRemoteBases 根据场景和推送类型解析 bundle 与 source 的可验证基线。
func resolveRemoteBases(
	repositoryRoot string,
	commit string,
	scenario string,
	options remoteRunOptions,
) (string, string, error) {
	bundleBase, err := remoteGitOutput(repositoryRoot, "rev-parse", "--verify", "--end-of-options", commit+"^1^{commit}")
	if err != nil {
		bundleBase = commit
	}
	if scenario != "push" {
		return resolveRemoteNonPushBase(repositoryRoot, commit, bundleBase, options.Base)
	}
	return resolveRemotePushBase(repositoryRoot, commit, bundleBase, options)
}

// resolveRemoteNonPushBase 解析 commit/full 场景的可选祖先基线。
func resolveRemoteNonPushBase(
	repositoryRoot string,
	commit string,
	bundleBase string,
	baseRevision string,
) (string, string, error) {
	if baseRevision == "" {
		return bundleBase, bundleBase, nil
	}
	base, err := remoteGitOutput(repositoryRoot, "rev-parse", "--verify", "--end-of-options", baseRevision+"^{commit}")
	if err != nil {
		return "", "", err
	}
	if _, err := remoteGitOutput(repositoryRoot, "merge-base", "--is-ancestor", base, commit); err != nil {
		return "", "", errors.New("remote CI base must be an ancestor of commit")
	}
	return base, base, nil
}

// resolveRemotePushBase 按 create、fast-forward 或 force 解析推送基线。
func resolveRemotePushBase(
	repositoryRoot string,
	commit string,
	bundleBase string,
	options remoteRunOptions,
) (string, string, error) {
	switch gatecontract.UpdateKind(options.UpdateKind) {
	case gatecontract.UpdateKindCreate:
		if options.Base != "" {
			return "", "", errors.New("push create must not include --base")
		}
		return bundleBase, "", nil
	case gatecontract.UpdateKindFastForward:
		return resolveRemoteFastForwardPushBase(repositoryRoot, commit, bundleBase, options.Base)
	case gatecontract.UpdateKindForce:
		return resolveRemoteForcePushBase(repositoryRoot, bundleBase, options.Base)
	default:
		return "", "", fmt.Errorf("unsupported push update kind %q", options.UpdateKind)
	}
}

// resolveRemoteFastForwardPushBase 校验 fast-forward 基线确为目标提交祖先。
func resolveRemoteFastForwardPushBase(
	repositoryRoot string,
	commit string,
	bundleBase string,
	baseRevision string,
) (string, string, error) {
	if baseRevision == "" {
		baseRevision = bundleBase
	}
	base, err := remoteGitOutput(repositoryRoot, "rev-parse", "--verify", "--end-of-options", baseRevision+"^{commit}")
	if err != nil {
		return "", "", err
	}
	if _, err := remoteGitOutput(repositoryRoot, "merge-base", "--is-ancestor", base, commit); err != nil {
		return "", "", errors.New("fast-forward push base must be an ancestor of commit")
	}
	return base, base, nil
}

// resolveRemoteForcePushBase 解析 force push 显式 source 基线。
func resolveRemoteForcePushBase(
	repositoryRoot string,
	bundleBase string,
	baseRevision string,
) (string, string, error) {
	if baseRevision == "" {
		return "", "", errors.New("force push requires --base")
	}
	base, err := remoteGitOutput(repositoryRoot, "rev-parse", "--verify", "--end-of-options", baseRevision+"^{commit}")
	if err != nil {
		return "", "", err
	}
	return bundleBase, base, nil
}
