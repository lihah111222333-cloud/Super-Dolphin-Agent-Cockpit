package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

func ensureRemoteDurationCalibration(
	options remoteRunOptions,
	state remoteci.BaselineState,
	runnerIdentity string,
) error {
	return ensureRemoteDurationCalibrationWithRun(options, state, runnerIdentity, func(calibrationOptions remoteRunOptions) error {
		return executeRemoteCalibration(calibrationOptions, io.Discard)
	})
}

// ensureRemoteDurationCalibrationWithRun 允许测试以受控执行器验证自动校准状态机。
func ensureRemoteDurationCalibrationWithRun(
	options remoteRunOptions,
	state remoteci.BaselineState,
	runnerIdentity string,
	run func(remoteRunOptions) error,
) error {
	if options.Calibration {
		return nil
	}
	if run == nil {
		return infrastructureError("automatic remote duration calibration executor is nil")
	}
	scenario, _, err := resolveRemoteScenario(options)
	if err != nil {
		return sourceError("resolve remote CI scenario before automatic calibration: %v", err)
	}
	if scenario == "test" {
		return nil
	}
	ready, err := remoteDurationCalibrationReady(options.LedgerPath, state, runnerIdentity)
	if err != nil && !errors.Is(err, gatecontract.ErrDurationLedgerMetadataMissing) {
		return infrastructureError("inspect automatic remote duration calibration: %v", err)
	}
	if ready {
		return nil
	}
	return withRemoteCalibrationLock(options.LedgerPath, func() error {
		return completeAutomaticRemoteCalibration(options, state, runnerIdentity, run)
	})
}

// completeAutomaticRemoteCalibration 在持有校准锁时复查、修复或执行当前候选的校准。
func completeAutomaticRemoteCalibration(options remoteRunOptions, state remoteci.BaselineState, runnerIdentity string, run func(remoteRunOptions) error) error {
	ready, err := prepareAutomaticRemoteCalibrationLedger(options.LedgerPath, state, runnerIdentity)
	if err != nil {
		return infrastructureError("prepare automatic remote duration calibration: %v", err)
	}
	if ready {
		return nil
	}
	accepted, err := tryAcceptAutomaticRemoteCalibration(options, state, runnerIdentity)
	if err != nil {
		return infrastructureError("repair automatic remote duration calibration: %v", err)
	}
	if accepted {
		return nil
	}
	calibrationOptions, err := remoteAutomaticCalibrationOptions(options, state)
	if err != nil {
		return err
	}
	return run(calibrationOptions)
}

// tryAcceptAutomaticRemoteCalibration 用已有同身份成功样本重建校准元数据，不启动 ECI。
func tryAcceptAutomaticRemoteCalibration(
	options remoteRunOptions,
	state remoteci.BaselineState,
	runnerIdentity string,
) (bool, error) {
	if strings.TrimSpace(options.ConfigPath) == "" || strings.TrimSpace(options.RepositoryRoot) == "" {
		return false, nil
	}
	calibrationOptions, err := remoteAutomaticCalibrationOptions(options, state)
	if err != nil {
		return false, err
	}
	identity, err := resolveRemoteCalibrationIdentity(
		calibrationOptions.RepositoryRoot,
		calibrationOptions.Commit,
	)
	if err != nil {
		return false, err
	}
	config, err := loadRemoteRunConfig(calibrationOptions.ConfigPath)
	if err != nil {
		return false, err
	}
	runOptions := [3]remoteRunOptions{}
	runOptions[0], runOptions[1], runOptions[2] = remoteCalibrationRunOptions(
		calibrationOptions,
		identity.commit,
		identity.tree,
		identity.base,
	)
	var inputs [3]remoteci.RunInput
	for index, current := range runOptions {
		input, err := resolveRemoteRunInput(current, config, state, runnerIdentity)
		if err != nil {
			return false, err
		}
		inputs[index] = input
	}
	catalogs, digests, err := remoteCalibrationCatalogs(inputs)
	if err != nil {
		return false, err
	}
	calibration := remoteDurationCalibrationFromInputs(
		identity.commit,
		inputs,
		digests,
		time.Now().UTC(),
	)
	store, err := gatecontract.NewDurationLedgerStore(options.LedgerPath)
	if err != nil {
		return false, err
	}
	return acceptRemoteDurationCalibrationFromExistingSamples(
		store,
		calibration,
		catalogs[:]...,
	)
}

func acceptRemoteDurationCalibrationFromExistingSamples(
	store *gatecontract.DurationLedgerStore,
	calibration gatecontract.DurationCalibration,
	catalogs ...gatecontract.WorkloadCatalog,
) (bool, error) {
	snapshot, err := store.LoadPlanning(remoteCalibrationPlanningContext(calibration))
	if err != nil {
		return false, err
	}
	if _, _, err := verifyRemoteCalibrationIndexedEvidence(
		snapshot,
		calibration,
		nil,
		catalogs...,
	); err != nil {
		if errors.Is(err, errRemoteCalibrationSamplesIncomplete) {
			return false, nil
		}
		return false, err
	}
	if _, err := acceptRemoteDurationCalibration(store, calibration, catalogs...); err != nil {
		return false, err
	}
	return true, nil
}

// withRemoteCalibrationLock 跨 Agent 串行校准，等待者在锁内复查后直接复用结果。
func withRemoteCalibrationLock(ledgerPath string, run func() error) (resultErr error) {
	if strings.TrimSpace(ledgerPath) == "" {
		return protocolError("remote duration calibration ledger path is required")
	}
	store, err := gatecontract.NewDurationLedgerStore(ledgerPath)
	if err != nil {
		return infrastructureError("resolve remote duration calibration ledger path: %v", err)
	}
	ctx, cancel := gateprivate.WithTimeout(context.Background(), remoteCalibrationLockWaitTimeout)
	defer cancel()
	lock, err := gateprivate.AcquireExclusiveFileLock(
		ctx,
		store.AuthorityPath()+".calibration.lock",
	)
	if err != nil {
		return infrastructureError("acquire remote duration calibration lock: %v", err)
	}
	defer func() {
		if err := lock.Release(); err != nil {
			resultErr = errors.Join(resultErr, infrastructureError("release remote duration calibration lock: %v", err))
		}
	}()
	return run()
}

// remoteDurationCalibrationReady 判断账本是否已有与当前稳定 runner 身份可比较的校准。
func remoteDurationCalibrationReady(
	ledgerPath string,
	state remoteci.BaselineState,
	runnerIdentity string,
) (bool, error) {
	store, err := gatecontract.NewDurationLedgerStore(ledgerPath)
	if err != nil {
		return false, err
	}
	snapshot, err := store.LoadMetadata()
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return remoteDurationCalibrationMatches(snapshot.Ledger.Calibration, state, runnerIdentity), nil
}

// prepareAutomaticRemoteCalibrationLedger 迁移同基线旧身份，并仅清除真正失配的校准。
func prepareAutomaticRemoteCalibrationLedger(
	ledgerPath string,
	state remoteci.BaselineState,
	runnerIdentity string,
) (bool, error) {
	store, err := gatecontract.NewDurationLedgerStore(ledgerPath)
	if err != nil {
		return false, err
	}
	for attempt := range 16 {
		metadata, err := store.LoadMetadata()
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if remoteDurationCalibrationMatches(metadata.Ledger.Calibration, state, runnerIdentity) {
			return true, nil
		}
		ready, retry, err := repairAutomaticRemoteCalibrationLedger(store, state, runnerIdentity)
		if err != nil {
			return false, err
		}
		if !retry {
			return ready, nil
		}
		time.Sleep(time.Duration(attempt+1) * 10 * time.Millisecond)
	}
	return false, errors.New("reset remote duration calibration exceeded retry limit")
}

// repairAutomaticRemoteCalibrationLedger 执行一次迁移或失配校准清理，并报告是否应重试 CAS。
func repairAutomaticRemoteCalibrationLedger(store *gatecontract.DurationLedgerStore, state remoteci.BaselineState, runnerIdentity string) (bool, bool, error) {
	snapshot, err := store.Load()
	if err != nil {
		return false, false, err
	}
	ledger := snapshot.Ledger
	legacyIdentity := legacyRemoteRunnerIdentityDigest(state)
	changed := migrateLegacyRemoteDurationSamples(&ledger, state, legacyIdentity, runnerIdentity)
	changed, ready := reconcileLegacyRemoteCalibration(&ledger, state, legacyIdentity, runnerIdentity, changed)
	if !changed && !ready {
		return false, false, nil
	}
	if _, err := store.CompareAndSwap(snapshot.Generation, ledger); err == nil {
		return ready, false, nil
	} else if errors.Is(err, gatecontract.ErrDurationLedgerConflict) || errors.Is(err, gatecontract.ErrDurationLedgerBusy) {
		return false, true, nil
	} else {
		return false, false, err
	}
}

// reconcileLegacyRemoteCalibration 更新旧身份校准，或清除与当前 runner 不可比的记录。
func reconcileLegacyRemoteCalibration(ledger *gatecontract.DurationLedger, state remoteci.BaselineState, legacyIdentity, runnerIdentity string, changed bool) (bool, bool) {
	if legacyRemoteDurationCalibrationMatches(ledger.Calibration, state, legacyIdentity) {
		calibration := *ledger.Calibration
		calibration.Runner = runnerIdentity
		ledger.Calibration = &calibration
		return true, true
	}
	if ledger.Calibration != nil {
		ledger.Calibration = nil
		return true, false
	}
	return changed, false
}

func legacyRemoteDurationCalibrationMatches(
	calibration *gatecontract.DurationCalibration,
	state remoteci.BaselineState,
	legacyIdentity string,
) bool {
	return calibration != nil &&
		calibration.SchemaVersion == gatecontract.DurationCalibrationSchemaVersion &&
		calibration.Platform == state.Platform &&
		calibration.Runner == legacyIdentity &&
		calibration.Toolchain == state.ToolchainDigest
}

func migrateLegacyRemoteDurationSamples(
	ledger *gatecontract.DurationLedger,
	state remoteci.BaselineState,
	legacyIdentity string,
	runnerIdentity string,
) bool {
	changed := false
	for index := range ledger.Samples {
		bucket := &ledger.Samples[index].Bucket
		if bucket.Platform != state.Platform || bucket.Toolchain != state.ToolchainDigest ||
			bucket.Runner != legacyIdentity {
			continue
		}
		bucket.Runner = runnerIdentity
		changed = true
	}
	return changed
}

// remoteAutomaticCalibrationOptions 只传递校准所需身份，并优先绑定当前候选源码。
func remoteAutomaticCalibrationOptions(options remoteRunOptions, state remoteci.BaselineState) (remoteRunOptions, error) {
	commit, err := resolveRemoteAutomaticCalibrationCommit(options, state)
	if err != nil {
		return remoteRunOptions{}, err
	}
	return remoteRunOptions{
		ConfigPath: options.ConfigPath, RepositoryRoot: options.RepositoryRoot,
		Commit: commit, MaxShards: options.MaxShards,
		StatePath: options.StatePath, LedgerPath: options.LedgerPath,
	}, nil
}

// resolveRemoteAutomaticCalibrationCommit 将显式 tree/parent 物化为不移动 ref 的确定性提交。
func resolveRemoteAutomaticCalibrationCommit(options remoteRunOptions, state remoteci.BaselineState) (string, error) {
	hasTree, hasParent := options.Tree != "", options.ParentCommit != ""
	if hasTree != hasParent {
		return "", sourceError("automatic remote calibration requires tree and parent together")
	}
	if hasTree {
		if options.Commit != "" {
			return "", sourceError("automatic remote calibration candidate cannot mix commit with tree and parent")
		}
		return createRemoteCalibrationCandidateCommit(options.RepositoryRoot, options.Tree, options.ParentCommit)
	}
	if options.Commit != "" {
		return options.Commit, nil
	}
	if strings.TrimSpace(state.MainCommit) == "" {
		return "", protocolError("accepted remote baseline main commit is required for automatic calibration")
	}
	return state.MainCommit, nil
}

// createRemoteCalibrationCandidateCommit 创建固定作者与时间的候选提交，并复核其树和父提交。
func createRemoteCalibrationCandidateCommit(repositoryRoot string, tree string, parent string) (string, error) {
	resolvedTree, err := remoteGitOutput(repositoryRoot, "rev-parse", "--verify", "--end-of-options", tree+"^{tree}")
	if err != nil {
		return "", sourceError("resolve automatic calibration candidate tree: %v", err)
	}
	if resolvedTree != tree {
		return "", sourceError("automatic calibration candidate tree must be a full object ID")
	}
	resolvedParent, err := remoteGitOutput(repositoryRoot, "rev-parse", "--verify", "--end-of-options", parent+"^{commit}")
	if err != nil {
		return "", sourceError("resolve automatic calibration candidate parent: %v", err)
	}
	if resolvedParent != parent {
		return "", sourceError("automatic calibration candidate parent must be a full object ID")
	}
	args := []string{"commit-tree", tree, "-p", parent, "-m", "automatic remote CI calibration candidate"}
	command := exec.Command("git", args...)
	command.Dir = repositoryRoot
	command.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Super Dolphin CI", "GIT_AUTHOR_EMAIL=ci@super-dolphin.invalid",
		"GIT_COMMITTER_NAME=Super Dolphin CI", "GIT_COMMITTER_EMAIL=ci@super-dolphin.invalid",
		"GIT_AUTHOR_DATE=@0 +0000", "GIT_COMMITTER_DATE=@0 +0000",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", sourceError("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	commit := strings.TrimSpace(string(output))
	identity, err := resolveRemoteCalibrationIdentity(repositoryRoot, commit)
	if err != nil {
		return "", err
	}
	if identity.tree != tree || identity.base != parent {
		return "", sourceError("automatic calibration candidate commit identity drifted")
	}
	return commit, nil
}

// resolveRemoteRunnerIdentity 从已接受基线树解析 Worker 生产闭包，而不是绑定整颗 CLI。
func resolveRemoteRunnerIdentity(repositoryRoot string, state remoteci.BaselineState) (string, error) {
	root, err := filepath.Abs(repositoryRoot)
	if err != nil {
		return "", err
	}
	workerExecutionDigest, err := remoteci.ResolveWorkerExecutionDigest(
		context.Background(),
		root,
		state.MainTree,
	)
	if err != nil {
		return "", err
	}
	return remoteRunnerIdentityDigest(state, workerExecutionDigest), nil
}

// remoteRunnerIdentityDigest 只绑定 Worker 执行语义，不把协调器、hook 或 Seed 代际混入校准身份。
func remoteRunnerIdentityDigest(state remoteci.BaselineState, workerExecutionDigest string) string {
	material := strings.Join([]string{
		"super-dolphin-gate-runner-identity-v2",
		state.RuntimeImage,
		state.PolicyDigest,
		state.ToolchainDigest,
		workerExecutionDigest,
	}, "\x00")
	digest := sha256.Sum256([]byte(material))
	return fmt.Sprintf("sha256:%x", digest)
}

// legacyRemoteRunnerIdentityDigest 仅用于把同一已接受基线的旧耗时样本迁移到窄身份。
func legacyRemoteRunnerIdentityDigest(state remoteci.BaselineState) string {
	material := strings.Join([]string{
		"super-dolphin-gate-runner-identity-v1",
		state.RuntimeImage,
		state.PolicyDigest,
		state.ToolchainDigest,
		state.GateBinarySHA256,
		state.RuntimeSeedSHA256,
	}, "\x00")
	digest := sha256.Sum256([]byte(material))
	return fmt.Sprintf("sha256:%x", digest)
}

// remoteDurationCalibrationMatches 核对稳定 runner、平台和工具链校准身份。
func remoteDurationCalibrationMatches(
	calibration *gatecontract.DurationCalibration,
	state remoteci.BaselineState,
	runnerIdentity string,
) bool {
	return calibration != nil &&
		calibration.SchemaVersion == gatecontract.DurationCalibrationSchemaVersion &&
		calibration.Platform == state.Platform &&
		calibration.Runner == runnerIdentity &&
		calibration.Toolchain == state.ToolchainDigest
}
