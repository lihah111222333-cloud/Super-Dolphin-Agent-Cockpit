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
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/shardresource"
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
	expectedResource, hasExpectedResource, err := remoteCalibrationResourceForOptions(options)
	if err != nil {
		return infrastructureError("load configured remote calibration resource: %v", err)
	}
	resourceArgs := optionalCalibrationResource(hasExpectedResource, expectedResource)
	ready, err := remoteDurationCalibrationReady(options.LedgerPath, state, runnerIdentity, resourceArgs...)
	if err != nil && !errors.Is(err, gatecontract.ErrDurationLedgerMetadataMissing) {
		return infrastructureError("inspect automatic remote duration calibration: %v", err)
	}
	if ready {
		return nil
	}
	return completeAutomaticRemoteCalibration(options, state, runnerIdentity, run, resourceArgs...)
}

func remoteCalibrationResourceForOptions(options remoteRunOptions) (shardresource.Class, bool, error) {
	if strings.TrimSpace(options.ConfigPath) == "" {
		return shardresource.Class{}, false, nil
	}
	config, err := loadRemoteRunConfig(options.ConfigPath)
	if err != nil {
		return shardresource.Class{}, false, err
	}
	resource, err := config.Capacity.ResourcePolicy.ResolveCalibrationClass()
	if err != nil {
		return shardresource.Class{}, false, err
	}
	return resource, true, nil
}

func optionalCalibrationResource(has bool, resource shardresource.Class) []shardresource.Class {
	if !has {
		return nil
	}
	return []shardresource.Class{resource}
}

// completeAutomaticRemoteCalibration 通过 SQLite 复查或执行当前候选的校准。
func completeAutomaticRemoteCalibration(options remoteRunOptions, state remoteci.BaselineState, runnerIdentity string, run func(remoteRunOptions) error, expected ...shardresource.Class) error {
	ready, err := prepareAutomaticRemoteCalibrationLedger(options.LedgerPath, state, runnerIdentity, expected...)
	if err != nil {
		return infrastructureError("prepare automatic remote duration calibration: %v", err)
	}
	if ready {
		return nil
	}
	accepted, err := tryAcceptAutomaticRemoteCalibration(options, state, runnerIdentity)
	if err != nil {
		return infrastructureError("accept automatic remote duration calibration: %v", err)
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
	runOptions := [3]remoteRunOptions{}
	runOptions[0], runOptions[1], runOptions[2] = remoteCalibrationRunOptions(
		calibrationOptions,
		identity.commit,
		identity.tree,
		identity.base,
	)
	var inputs [3]remoteci.RunInput
	for index, current := range runOptions {
		input, err := resolveRemoteRunInput(current, state, runnerIdentity)
		if err != nil {
			return false, err
		}
		inputs[index] = input
	}
	catalogs, digests, available, err := loadAutomaticRemoteCalibrationCatalogs(inputs)
	if err != nil {
		return false, err
	}
	if !available {
		return false, nil
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

// loadAutomaticRemoteCalibrationCatalogs 按三次运行的 observation identity 回读唯一 SQLite catalog。
func loadAutomaticRemoteCalibrationCatalogs(inputs [3]remoteci.RunInput) ([3]gatecontract.WorkloadCatalog, [3]string, bool, error) {
	var catalogs [3]gatecontract.WorkloadCatalog
	var digests [3]string
	for index, input := range inputs {
		if input.LedgerStore == nil {
			return catalogs, digests, false, infrastructureError("load exact remote calibration catalog: authority store is required")
		}
		record, err := input.LedgerStore.LoadWorkloadCatalogRecordByObservationIdentity(
			input.Tree,
			input.Entrypoint,
			input.Profile,
			input.AcceptedGeneration,
		)
		if errors.Is(err, gatecontract.ErrWorkloadCatalogObservationNotFound) {
			return catalogs, digests, false, nil
		}
		if err != nil {
			return catalogs, digests, false, err
		}
		if err := validateRemoteCalibrationCatalogInputDigests(record.Catalog); err != nil {
			return catalogs, digests, false, err
		}
		catalogs[index], digests[index] = record.Catalog, record.CatalogDigest
	}
	return catalogs, digests, true, nil
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
	if _, _, err := verifyRemoteCalibrationAcceptanceEvidence(
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

// remoteDurationCalibrationReady 判断账本是否已有与当前稳定 runner 身份可比较的校准。
func remoteDurationCalibrationReady(
	ledgerPath string,
	state remoteci.BaselineState,
	runnerIdentity string,
	expected ...shardresource.Class,
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
	return remoteDurationCalibrationSnapshotReady(store, snapshot, state, runnerIdentity, expected)
}

// remoteDurationCalibrationSnapshotReady 仅在稳定 runner、完整目录、样本与 accepted overhead 均可验证时返回 ready。
func remoteDurationCalibrationSnapshotReady(
	store *gatecontract.DurationLedgerStore,
	snapshot gatecontract.DurationLedgerSnapshot,
	state remoteci.BaselineState,
	runnerIdentity string,
	expected []shardresource.Class,
) (bool, error) {
	if len(expected) > 1 {
		return false, errors.New("remote duration calibration accepts at most one expected resource")
	}
	calibration := snapshot.Ledger.Calibration
	if !remoteDurationCalibrationMatches(calibration, state, runnerIdentity) || !calibrationResourceMatches(calibration, expected) {
		return false, nil
	}
	catalogs, available, err := loadRemoteDurationCalibrationCatalogs(store, *calibration)
	if err != nil {
		return false, err
	}
	if !available {
		return false, nil
	}
	planning, err := store.LoadPlanning(remoteCalibrationPlanningContext(*calibration))
	if err != nil {
		return false, err
	}
	if _, _, err := verifyRemoteCalibrationAcceptanceEvidence(planning, *calibration, nil, catalogs...); err != nil {
		if errors.Is(err, errRemoteCalibrationSamplesIncomplete) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// loadRemoteDurationCalibrationCatalogs 按校准元数据 digest 恢复三个持久化目录及观测。
func loadRemoteDurationCalibrationCatalogs(
	store *gatecontract.DurationLedgerStore,
	calibration gatecontract.DurationCalibration,
) ([]gatecontract.WorkloadCatalog, bool, error) {
	digests := [...]string{
		calibration.CommitCatalogDigest,
		calibration.PushCatalogDigest,
		calibration.ReleaseCatalogDigest,
	}
	catalogs := make([]gatecontract.WorkloadCatalog, 0, len(digests))
	for _, digest := range digests {
		record, err := store.LoadWorkloadCatalogRecord(digest)
		if errors.Is(err, gatecontract.ErrWorkloadCatalogNotFound) ||
			errors.Is(err, gatecontract.ErrWorkloadCatalogObservationNotFound) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		if len(record.Observations) == 0 {
			return nil, false, nil
		}
		catalogs = append(catalogs, record.Catalog)
	}
	return catalogs, true, nil
}

// prepareAutomaticRemoteCalibrationLedger 只接受当前 runner 身份的完整校准，并清除失配元数据。
func prepareAutomaticRemoteCalibrationLedger(
	ledgerPath string,
	state remoteci.BaselineState,
	runnerIdentity string,
	expected ...shardresource.Class,
) (bool, error) {
	store, err := gatecontract.NewDurationLedgerStore(ledgerPath)
	if err != nil {
		return false, err
	}
	if len(expected) > 1 {
		return false, errors.New("remote duration calibration accepts at most one expected resource")
	}
	for attempt := range 16 {
		ready, retry, done, err := prepareAutomaticRemoteCalibrationAttempt(store, state, runnerIdentity, expected)
		if err != nil {
			return false, err
		}
		if done {
			return ready, nil
		}
		if retry {
			time.Sleep(time.Duration(attempt+1) * 10 * time.Millisecond)
		}
	}
	return false, errors.New("clear remote duration calibration exceeded retry limit")
}

// prepareAutomaticRemoteCalibrationAttempt 执行一次校准元数据读取或窄边界清除。
func prepareAutomaticRemoteCalibrationAttempt(
	store *gatecontract.DurationLedgerStore,
	state remoteci.BaselineState,
	runnerIdentity string,
	expected []shardresource.Class,
) (ready, retry, done bool, err error) {
	snapshot, err := store.LoadMetadata()
	if errors.Is(err, os.ErrNotExist) {
		return false, false, true, nil
	}
	if err != nil {
		return false, false, false, err
	}
	if remoteDurationCalibrationMatches(snapshot.Ledger.Calibration, state, runnerIdentity) && calibrationResourceMatches(snapshot.Ledger.Calibration, expected) {
		ready, err := remoteDurationCalibrationSnapshotReady(store, snapshot, state, runnerIdentity, expected)
		return ready, false, true, err
	}
	if snapshot.Ledger.Calibration == nil {
		return false, false, true, nil
	}
	// 窄边界 CAS 只清除校准元数据，不物化或重写样本。
	if _, err := store.CompareAndSwapCalibration(snapshot.Generation, nil); err == nil {
		return false, false, true, nil
	} else if errors.Is(err, gatecontract.ErrDurationLedgerConflict) || errors.Is(err, gatecontract.ErrDurationLedgerBusy) {
		return false, true, false, nil
	} else {
		return false, false, false, err
	}
}

// calibrationResourceMatches 核对校准账本是否绑定调用方要求的资源规格。
func calibrationResourceMatches(calibration *gatecontract.DurationCalibration, expected []shardresource.Class) bool {
	if len(expected) == 0 {
		return true
	}
	if len(expected) != 1 || calibration == nil {
		return false
	}
	resource := expected[0]
	return calibration.CalibrationResourceClassID == resource.ID && calibration.CalibrationResourceCPU == resource.VCPU && calibration.CalibrationResourceMemoryGiB == resource.MemoryGiB
}

// remoteAutomaticCalibrationOptions 只传递校准所需身份，并优先绑定当前候选源码。
func remoteAutomaticCalibrationOptions(options remoteRunOptions, state remoteci.BaselineState) (remoteRunOptions, error) {
	commit, err := resolveRemoteAutomaticCalibrationCommit(options, state)
	if err != nil {
		return remoteRunOptions{}, err
	}
	return remoteRunOptions{
		ConfigPath: options.ConfigPath, RepositoryRoot: options.RepositoryRoot,
		Commit:           commit,
		LedgerPath:       options.LedgerPath,
		AgentTokenDigest: options.AgentTokenDigest,
		ProgressObserver: options.ProgressObserver,
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

// remoteRunnerIdentityDigest 只绑定 Worker 执行语义；镜像、缓存、源码代际与 Gate 二进制都不是校准输入。
func remoteRunnerIdentityDigest(state remoteci.BaselineState, workerExecutionDigest string) string {
	material := strings.Join([]string{
		"super-dolphin-gate-runner-identity-v3",
		state.Platform,
		state.PolicyDigest,
		state.ToolchainDigest,
		state.RuntimeSeedSHA256,
		workerExecutionDigest,
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
		gatecontract.ValidateDurationCalibration(*calibration) == nil &&
		calibration.SchemaVersion == gatecontract.DurationCalibrationSchemaVersion &&
		calibration.Platform == state.Platform &&
		calibration.Runner == runnerIdentity &&
		calibration.Toolchain == state.ToolchainDigest
}
