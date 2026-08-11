package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/shardresource"
)

type remoteResolvedTarget struct {
	commit                   string
	tree                     string
	revision                 string
	bundleBase               string
	sourceBase               string
	candidateObjectAuthority gatecontract.CandidateObjectAuthority
}

// resolveRemoteRunInput 将已验证的 Git source、基线、账本和 workload 组装为运行输入。
func resolveRemoteRunInput(
	options remoteRunOptions,
	state remoteci.BaselineState,
	runnerIdentity string,
) (remoteci.RunInput, error) {
	if err := validateRunnableRemoteBaseline(state); err != nil {
		return remoteci.RunInput{}, err
	}
	repositoryRoot, scenario, profile, target, source, err := resolveRemoteRunSource(options)
	if err != nil {
		return remoteci.RunInput{}, err
	}
	entrypoint := remoteRunEntrypoint(options)
	ledger, ledgerStore, err := loadRemoteRunLedger(options, state, runnerIdentity)
	if err != nil {
		return remoteci.RunInput{}, err
	}
	calibrationResource, err := remoteRunInputCalibrationResource(options)
	if err != nil {
		return remoteci.RunInput{}, err
	}
	inventory, err := buildRemoteRunInventory(repositoryRoot, target, scenario, options.Tests, state.Platform)
	if err != nil {
		return remoteci.RunInput{}, err
	}
	return remoteci.RunInput{
		AcceptedGeneration: state.Generation,
		RepositoryRoot:     repositoryRoot,
		RemoteName:         options.RemoteName,
		RemoteURL:          options.RemoteURL,
		AgentTokenDigest:   options.AgentTokenDigest,
		Commit:             target.commit, Tree: target.tree, Base: target.bundleBase,
		RunnerBaseCommit: state.MainCommit, RunnerBaseTree: state.MainTree,
		Source: source, Profile: profile, Entrypoint: entrypoint,
		Platform: state.Platform, PolicyDigest: state.PolicyDigest,
		ToolchainDigest:     state.ToolchainDigest,
		LedgerSnapshot:      ledger,
		LedgerStore:         ledgerStore,
		Inventory:           inventory,
		SelectedTests:       scenario == "test",
		Calibration:         options.Calibration,
		CalibrationResource: calibrationResource,
		Force:               options.Force,
		RunnerImage:         state.RuntimeImage, RunnerIdentityDigest: runnerIdentity,
		ImageCacheSnapshotID:   state.ImageCacheSnapshotID,
		BaselineManifestDigest: state.BaselineManifestDigest,
		RunnerConfigDigest:     remoteRuntimeImageDigest(state.RuntimeImage),
		GateBinarySHA256:       state.GateBinarySHA256,
		RuntimeSeedSHA256:      state.RuntimeSeedSHA256,
		OCIProjectCache:        state.OCIProjectCache,
	}, nil
}

// remoteRunInputCalibrationResource 让所有校准 RunInput 在离开唯一解析入口前绑定固定资源身份。
func remoteRunInputCalibrationResource(options remoteRunOptions) (shardresource.Class, error) {
	if !options.Calibration {
		return shardresource.Class{}, nil
	}
	resource, configured, err := remoteCalibrationResourceForOptions(options)
	if err != nil {
		return shardresource.Class{}, fmt.Errorf("load calibration run input resource: %w", err)
	}
	if !configured {
		return shardresource.Class{}, errors.New("calibration run input requires a configured resource")
	}
	return resource, nil
}

// resolveRemoteCandidateGateIdentity 读取 exact candidate tree 的 Gate 编译闭包与工具链身份。
func resolveRemoteCandidateGateIdentity(repositoryRoot, candidateTree string) (string, string, error) {
	ctx := context.Background()
	candidateSource, candidateToolchain, _, err := remoteci.LoadGateCLICompileClosure(ctx, repositoryRoot, candidateTree)
	if err != nil {
		return "", "", fmt.Errorf("resolve candidate gate CLI compile closure: %w", err)
	}
	return candidateSource, candidateToolchain, nil
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
	candidateObjectAuthority, err := gatecontract.CaptureCandidateObjectAuthority()
	if err != nil {
		return "", "", "", remoteResolvedTarget{}, gatecontract.SourceSpec{}, fmt.Errorf("capture candidate Git object authority: %w", err)
	}
	scenario, profile, err := resolveRemoteScenario(options)
	if err != nil {
		return "", "", "", remoteResolvedTarget{}, gatecontract.SourceSpec{}, err
	}
	target, err := resolveRemoteRunTarget(repositoryRoot, scenario, options)
	if err != nil {
		return "", "", "", remoteResolvedTarget{}, gatecontract.SourceSpec{}, err
	}
	target.candidateObjectAuthority = candidateObjectAuthority
	objectFormat, err := remoteGitOutput(repositoryRoot, "rev-parse", "--show-object-format")
	if err != nil {
		return "", "", "", remoteResolvedTarget{}, gatecontract.SourceSpec{}, err
	}
	source, err := remoteGateSource(options, profile, objectFormat, target.commit, target.tree, target.sourceBase)
	return repositoryRoot, scenario, profile, target, source, err
}

// loadRemoteRunLedger 从唯一 SQLite authority 读取 planning snapshot；校准要求
// 由 normal 入口在 PreparedRun 判定为 miss 后单独验证。普通运行第一次只读元数据，
// 避免空账本在 PASS 复用判定与自动校准之前自锁；miss 路径随后刷新严格 planning snapshot。
func loadRemoteRunLedger(
	options remoteRunOptions,
	state remoteci.BaselineState,
	runnerIdentity string,
) (gatecontract.DurationLedgerSnapshot, *gatecontract.DurationLedgerStore, error) {
	store, err := gatecontract.NewDurationLedgerStore(options.LedgerPath)
	if err != nil {
		return gatecontract.DurationLedgerSnapshot{}, nil, err
	}
	if !options.Calibration {
		ledger, err := store.LoadMetadata()
		if err != nil {
			return gatecontract.DurationLedgerSnapshot{}, nil, err
		}
		return ledger, store, nil
	}
	context, err := remoteRunPlanningContext(options, state, runnerIdentity)
	if err != nil {
		return gatecontract.DurationLedgerSnapshot{}, nil, err
	}
	ledger, err := store.LoadPlanning(context)
	if err != nil {
		return gatecontract.DurationLedgerSnapshot{}, nil, err
	}
	return ledger, store, nil
}

// remoteRunPlanningContext 将固定校准规格绑定到第一次 planning 读取，避免在后置 RunInput 配置前丢失资源身份。
func remoteRunPlanningContext(options remoteRunOptions, state remoteci.BaselineState, runnerIdentity string) (gatecontract.PlanningContext, error) {
	context := gatecontract.PlanningContext{
		Platform:           state.Platform,
		Runner:             runnerIdentity,
		Toolchain:          state.ToolchainDigest,
		Calibration:        options.Calibration,
		TargetDurationMS:   gatecontract.FullCITargetDurationMS,
		AcceptedSnapshotID: state.ImageCacheSnapshotID,
	}
	if !options.Calibration {
		return context, nil
	}
	resource, configured, err := remoteCalibrationResourceForOptions(options)
	if err != nil {
		return gatecontract.PlanningContext{}, fmt.Errorf("load calibration planning resource: %w", err)
	}
	if !configured {
		return gatecontract.PlanningContext{}, errors.New("calibration planning requires a configured resource")
	}
	context.CalibrationResourceClassID = resource.ID
	context.CalibrationResourceCPU = resource.VCPU
	context.CalibrationResourceMemoryGiB = resource.MemoryGiB
	return context, nil
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

// validateRemoteDurationCalibration 确保普通运行使用与已接受基线一致的首代校准。
func validateRemoteDurationCalibration(
	options remoteRunOptions,
	state remoteci.BaselineState,
	runnerIdentity string,
	ledger gatecontract.DurationLedger,
) error {
	if options.Calibration {
		return nil
	}
	calibration := ledger.Calibration
	if calibration == nil {
		return errors.New("remote CI duration ledger has not completed first-generation calibration")
	}
	if !remoteDurationCalibrationMatches(calibration, state, runnerIdentity) {
		return errors.New("remote CI duration calibration does not match the accepted runner baseline")
	}
	resource, configured, err := remoteCalibrationResourceForOptions(options)
	if err != nil {
		return fmt.Errorf("load configured remote calibration resource: %w", err)
	}
	if configured && !calibrationResourceMatches(calibration, []shardresource.Class{resource}) {
		return errors.New("remote CI duration calibration resource does not match configured independent calibration class")
	}
	return nil
}

func remoteRuntimeImageDigest(reference string) string {
	_, digest, ok := strings.Cut(reference, "@")
	if !ok {
		return ""
	}
	return digest
}

// validateAcceptedRemoteBaseline 验证已接受 OCI 基线的不可变镜像和 ImageCache authority。
func validateAcceptedRemoteBaseline(state remoteci.BaselineState) error {
	if err := state.Validate(); err != nil {
		return err
	}
	if state.OCIProjectCache == nil {
		return errors.New("accepted baseline must use OCI project cache")
	}
	return state.OCIProjectCache.ValidateForBaseline(state.MainTree, state.ToolchainDigest, state.Platform, state.RuntimeImage)
}

// validateRunnableRemoteBaseline rejects an accepted baseline whose OCI identity cannot run.
func validateRunnableRemoteBaseline(state remoteci.BaselineState) error {
	if err := validateAcceptedRemoteBaseline(state); err != nil {
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
