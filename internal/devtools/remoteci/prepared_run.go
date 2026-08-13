package remoteci

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// PreparedRun 在 job、临时目录、OSS 或 ECI 产生副作用前冻结单个远程 CI 候选的目录与复用决策。
type PreparedRun struct {
	allReused                   bool
	mu                          sync.Mutex
	consumed                    bool
	owner                       *Coordinator
	frozenDigest                string
	input                       RunInput
	plan                        gate.GatePlan
	catalog                     gate.WorkloadCatalog
	executionCatalog            gate.WorkloadCatalog
	catalogDigest               string
	entrypoint                  gate.CIEntrypoint
	scope                       *gate.RemoteCIExecutionScope
	excluded                    []gate.GateID
	reuse                       remoteWorkloadReusePreparation
	planningOverheadDigest      string
	verifiedExecutionSnapshotID string
}

// Prepare 一次性构造精确候选的计划、目录、摘要与复用决策。
func (coordinator *Coordinator) Prepare(ctx context.Context, input RunInput) (*PreparedRun, error) {
	coordinator.progress.phase(ProgressPhasePrepare, "started")
	if err := validateCoordinatorPrepareInput(ctx, input); err != nil {
		return nil, err
	}
	coordinator.progress.phase(ProgressPhasePrepare, "input_validated")
	plan, catalog, entrypoint, err := buildRemotePlan(input)
	if err != nil {
		return nil, err
	}
	coordinator.progress.phase(ProgressPhasePrepare, "plan_built")
	coordinator.progress.phase(ProgressPhasePrepare, "identity_started")
	input, catalog, catalogDigest, fingerprintSnapshot, err := prepareRemoteWorkloadIdentity(
		ctx,
		input,
		catalog,
	)
	if err != nil {
		return nil, err
	}
	coordinator.progress.phase(ProgressPhasePrepare, "identity_completed")
	coordinator.progress.phase(ProgressPhasePrepare, "reuse_started")
	reuse, err := prepareRemoteWorkloadReuse(
		ctx,
		input,
		catalog,
		coordinator.config.WorkerTimeout,
		coordinator.config.ResourcePolicy,
		fingerprintSnapshot,
		func(state string) { coordinator.progress.phase(ProgressPhasePrepare, state) },
	)
	if err != nil {
		return nil, err
	}
	coordinator.progress.setCacheCounts(len(reuse.reused), len(reuse.cacheMisses), len(reuse.reused))
	coordinator.progress.phase(ProgressPhasePrepare, "reuse_completed")
	if reuse.allReused() {
		if err := validateAllHitExecutionIdentity(input); err != nil {
			return nil, err
		}
	}
	if !reuse.allReused() {
		coordinator.progress.phase(ProgressPhasePrepare, "compile_inputs_started")
		compileInputs, compileErr := remoteCompileGroupInputsForMisses(
			ctx,
			fingerprintSnapshot,
			catalog,
			reuse.cacheMisses,
		)
		if compileErr != nil {
			return nil, compileErr
		}
		input.WorkloadCompileGroupInputs = cloneRemoteCompileGroupInputs(compileInputs)
		coordinator.progress.phase(ProgressPhasePrepare, "compile_inputs_completed")
	} else {
		coordinator.progress.phase(ProgressPhasePrepare, "compile_inputs_skipped")
	}
	scope, err := gate.NewRemoteCIFullExecutionScope(catalog)
	if err != nil {
		return nil, fmt.Errorf("construct full remote CI execution scope: %w", err)
	}
	coordinator.progress.phase(ProgressPhasePrepare, "scope_built")
	return coordinator.freezePreparedRun(input, plan, catalog, catalog, catalogDigest, entrypoint, &scope, nil, reuse)
}

// freezePreparedRun 固化已经完成身份和复用决策的远程运行，之后不得改变其执行范围。
func (coordinator *Coordinator) freezePreparedRun(
	input RunInput,
	plan gate.GatePlan,
	catalog gate.WorkloadCatalog,
	executionCatalog gate.WorkloadCatalog,
	catalogDigest string,
	entrypoint gate.CIEntrypoint,
	scope *gate.RemoteCIExecutionScope,
	excluded []gate.GateID,
	reuse remoteWorkloadReusePreparation,
) (*PreparedRun, error) {
	prepared := &PreparedRun{
		allReused:        reuse.allReused(),
		owner:            coordinator,
		input:            input,
		plan:             plan,
		catalog:          catalog,
		executionCatalog: executionCatalog,
		catalogDigest:    catalogDigest,
		entrypoint:       entrypoint,
		scope:            scope,
		excluded:         excluded,
		reuse:            reuse,
	}
	frozenDigest, err := prepared.frozenIdentityDigest()
	if err != nil {
		return nil, err
	}
	prepared.frozenDigest = frozenDigest
	coordinator.progress.setCacheCounts(len(reuse.reused), len(reuse.cacheMisses), len(reuse.reused))
	coordinator.progress.observeReuseDecision(reuse.diagnostic())
	coordinator.progress.phase(ProgressPhasePrepare, "completed")
	return prepared, nil
}

// prepareRemoteWorkloadIdentity 在 PASS lookup 前冻结 exact-tree correctness 输入。
func prepareRemoteWorkloadIdentity(
	ctx context.Context,
	input RunInput,
	catalog gate.WorkloadCatalog,
) (RunInput, gate.WorkloadCatalog, string, *remoteGitTreeSnapshot, error) {
	inputDigests, _, fingerprintSnapshot, err := remoteWorkloadFingerprintsWithSnapshot(
		ctx,
		input.RepositoryRoot,
		input.Tree,
		remoteShardableWorkloads(catalog),
	)
	if err != nil {
		return RunInput{}, gate.WorkloadCatalog{}, "", nil, err
	}
	workerExecutionSemanticDigest, err := fingerprintSnapshot.workerExecutionDigest(ctx)
	if err != nil {
		return RunInput{}, gate.WorkloadCatalog{}, "", nil, fmt.Errorf("derive remote worker execution semantic digest (%s): %w", fingerprintSnapshot.workerExecutionSourceDiagnostic(), err)
	}
	input.WorkerExecutionSemanticDigest = workerExecutionSemanticDigest
	catalog, err = bindRemoteWorkloadInputDigests(catalog, inputDigests)
	if err != nil {
		return RunInput{}, gate.WorkloadCatalog{}, "", nil, err
	}
	input.WorkloadInputDigests = cloneRemoteWorkloadInputDigests(inputDigests)
	input.WorkloadCompileGroupInputs = nil
	catalogDigest, err := gate.WorkloadCatalogDigest(catalog)
	if err != nil {
		return RunInput{}, gate.WorkloadCatalog{}, "", nil, err
	}
	return input, catalog, catalogDigest, fingerprintSnapshot, nil
}

// AllReused 返回 Prepare 已冻结的不可变复用决策。
func (prepared *PreparedRun) AllReused() bool {
	if prepared == nil {
		return false
	}
	prepared.mu.Lock()
	defer prepared.mu.Unlock()
	return prepared.allReused
}

// WorkloadReuseDecision 返回 Prepare 已冻结的 workload identity 与严格 MISS 副本。
// 返回值仅用于执行前审计；调用方修改切片或元素不会改变 PreparedRun。
func (prepared *PreparedRun) WorkloadReuseDecision() ([]gate.WorkloadPassIdentity, []gate.GateID) {
	if prepared == nil {
		return nil, nil
	}
	prepared.mu.Lock()
	defer prepared.mu.Unlock()
	identities := append([]gate.WorkloadPassIdentity(nil), prepared.reuse.identities...)
	misses := append([]gate.GateID(nil), prepared.reuse.cacheMisses...)
	return identities, misses
}

// RefreshWorkloadPassesAfterCalibration 在 MISS-only 云端身份绑定前，只针对冻结
// MISS 重读同一 SQLite authority。校准新产生的 exact PASS 会原子更新准备结果，
// 并从编译闭包中移除；候选树、目录、identity 和执行范围保持不变。
func (prepared *PreparedRun) RefreshWorkloadPassesAfterCalibration(store *gate.DurationLedgerStore) (int, error) {
	if prepared == nil {
		return 0, errors.New("prepared remote CI run is required")
	}
	prepared.mu.Lock()
	defer prepared.mu.Unlock()
	if err := prepared.validateWorkloadPassRefreshLocked(store); err != nil {
		return 0, err
	}
	if prepared.allReused {
		return 0, nil
	}
	prepared.owner.progress.phase(ProgressPhasePrepare, "reuse_post_calibration_started")
	nextReuse, recovered, err := refreshRemoteWorkloadReuseAfterCalibration(prepared.reuse, store)
	if err != nil {
		return 0, err
	}
	if recovered == 0 {
		prepared.owner.progress.phase(ProgressPhasePrepare, "reuse_post_calibration_completed")
		return 0, nil
	}
	return recovered, prepared.applyWorkloadPassRefreshLocked(store, nextReuse)
}

// validateWorkloadPassRefreshLocked 校验校准后 PASS 重读仍在无副作用冻结边界内。
func (prepared *PreparedRun) validateWorkloadPassRefreshLocked(store *gate.DurationLedgerStore) error {
	if prepared.consumed {
		return errors.New("prepared remote CI run is already consumed")
	}
	if err := prepared.validateFrozenLocked(); err != nil {
		return err
	}
	if err := prepared.validateLocked(); err != nil {
		return err
	}
	if prepared.verifiedExecutionSnapshotID != "" || prepared.planningOverheadDigest != "" {
		return errors.New("post-calibration PASS refresh must precede MISS execution and planning binding")
	}
	return validatePlanningSnapshotStore(prepared, store)
}

// applyWorkloadPassRefreshLocked 提交已经完整验证的 post-calibration 复用决策。
func (prepared *PreparedRun) applyWorkloadPassRefreshLocked(store *gate.DurationLedgerStore, nextReuse remoteWorkloadReusePreparation) error {
	nextInput := prepared.input
	nextInput.LedgerStore = store
	nextInput.WorkloadCompileGroupInputs = retainRemoteCompileGroupInputsForMisses(nextInput.WorkloadCompileGroupInputs, nextReuse.cacheMisses)
	nextAllReused := nextReuse.allReused()
	if nextAllReused {
		if err := validateAllHitExecutionIdentity(nextInput); err != nil {
			return err
		}
	}
	previousInput, previousReuse, previousAllReused := prepared.input, prepared.reuse, prepared.allReused
	prepared.input, prepared.reuse, prepared.allReused = nextInput, nextReuse, nextAllReused
	digest, err := prepared.frozenIdentityDigest()
	if err != nil {
		prepared.input, prepared.reuse, prepared.allReused = previousInput, previousReuse, previousAllReused
		return err
	}
	prepared.frozenDigest = digest
	prepared.owner.progress.setCacheCounts(len(nextReuse.reused), len(nextReuse.cacheMisses), len(nextReuse.reused))
	prepared.owner.progress.observeReuseDecision(nextReuse.diagnostic())
	prepared.owner.progress.phase(ProgressPhasePrepare, "reuse_post_calibration_completed")
	return nil
}

// retainRemoteCompileGroupInputsForMisses 只保留校准后仍需执行的编译闭包输入。
func retainRemoteCompileGroupInputsForMisses(inputs map[string]gate.CompileGroupInput, misses []gate.GateID) map[string]gate.CompileGroupInput {
	if len(inputs) == 0 || len(misses) == 0 {
		return nil
	}
	missSet := make(map[string]struct{}, len(misses))
	for _, workloadID := range misses {
		missSet[string(workloadID)] = struct{}{}
	}
	result := make(map[string]gate.CompileGroupInput, len(inputs))
	for workloadID, input := range inputs {
		if _, missed := missSet[workloadID]; missed {
			result[workloadID] = input
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// RemoteExecutionScope 返回冻结执行范围与排除项副本，并在身份漂移时立即拒绝。
func (prepared *PreparedRun) RemoteExecutionScope() (gate.RemoteCIExecutionScope, []gate.GateID, error) {
	if prepared == nil {
		return gate.RemoteCIExecutionScope{}, nil, errors.New("prepared remote CI run is required")
	}
	prepared.mu.Lock()
	defer prepared.mu.Unlock()
	if err := prepared.validateFrozenLocked(); err != nil {
		return gate.RemoteCIExecutionScope{}, nil, err
	}
	if prepared.scope == nil {
		return gate.RemoteCIExecutionScope{}, nil, errors.New("prepared remote CI execution scope is required")
	}
	return *prepared.scope, append([]gate.GateID(nil), prepared.excluded...), nil
}

// BindPreparedMissExecution 在严格 MISS 决策后一次性绑定候选 Gate 与实时 ImageCache 身份。
// 绑定前后都校验 frozen identity；失败不会留下部分字段或可执行配置。
func (coordinator *Coordinator) BindPreparedMissExecution(
	ctx context.Context,
	prepared *PreparedRun,
	binding MissExecutionBinding,
) error {
	if prepared == nil {
		return errors.New("prepared remote CI run is required")
	}
	prepared.mu.Lock()
	defer prepared.mu.Unlock()
	if prepared.consumed {
		return errors.New("prepared remote CI run is already consumed")
	}
	if prepared.owner != coordinator {
		return errors.New("prepared remote CI run belongs to a different coordinator")
	}
	if err := prepared.validateFrozenLocked(); err != nil {
		return err
	}
	if err := prepared.validateLocked(); err != nil {
		return err
	}
	if prepared.allReused {
		return errors.New("all-reused remote CI run cannot bind MISS execution inputs")
	}
	if prepared.verifiedExecutionSnapshotID != "" {
		return errors.New("prepared remote CI MISS execution inputs are already bound")
	}
	next := prepared.input
	next.CandidateGateSourceSHA256 = binding.CandidateGateSourceSHA256
	next.CandidateGateToolchainSHA256 = binding.CandidateGateToolchainSHA256
	next.ExecutionRunnerImage = binding.ExecutionRunnerImage
	next.ExecutionImageCacheSnapshotID = binding.ExecutionImageCacheSnapshotID
	next.ImageCacheOnly = binding.ImageCacheOnly
	config := coordinator.config
	config.ImageCacheSnapshotID = binding.ExecutionImageCacheSnapshotID
	if err := validateCoordinatorRunInput(ctx, config, next); err != nil {
		return err
	}
	previous := prepared.input
	prepared.input = next
	digest, err := prepared.frozenIdentityDigest()
	if err != nil {
		prepared.input = previous
		return err
	}
	prepared.verifiedExecutionSnapshotID = binding.ExecutionImageCacheSnapshotID
	prepared.frozenDigest = digest
	return nil
}

// ReloadPlanningSnapshot 仅在同步校准后，按冻结的 SQLite 权威和计划上下文重新加载耗时计划快照。
func (prepared *PreparedRun) ReloadPlanningSnapshot(store *gate.DurationLedgerStore) error {
	if prepared == nil {
		return errors.New("prepared remote CI run is required")
	}
	prepared.mu.Lock()
	defer prepared.mu.Unlock()
	if prepared.consumed {
		return errors.New("prepared remote CI run is already consumed")
	}
	if err := prepared.validateFrozenLocked(); err != nil {
		return err
	}
	if err := prepared.validateLocked(); err != nil {
		return err
	}
	if prepared.allReused {
		return nil
	}
	return prepared.reloadPlanningSnapshotLocked(store)
}

// reloadPlanningSnapshotLocked 在已冻结且未消费的互斥边界内重载并验证 SQLite 计划快照。
func (prepared *PreparedRun) reloadPlanningSnapshotLocked(store *gate.DurationLedgerStore) error {
	if err := validatePlanningSnapshotStore(prepared, store); err != nil {
		return err
	}
	planningContext := remotePlanningContext(prepared.input)
	current, err := loadAndValidatePlanningSnapshot(store, planningContext)
	if err != nil {
		return err
	}
	prepared.input.LedgerSnapshot = current
	prepared.input.LedgerStore = store
	if err := prepared.validateFinalPlanningSnapshotLocked(); err != nil {
		return err
	}
	return prepared.bindPlanningOverheadDigest(current)
}

// validatePlanningSnapshotStore 确保刷新仍使用冻结的同一 SQLite authority。
func validatePlanningSnapshotStore(prepared *PreparedRun, store *gate.DurationLedgerStore) error {
	if store == nil {
		return errors.New("remote CI planning duration ledger SQLite authority is required")
	}
	if prepared.input.LedgerStore == nil || store.AuthorityPath() != prepared.input.LedgerStore.AuthorityPath() {
		return errors.New("remote CI planning duration ledger authority drifted")
	}
	return nil
}

// loadAndValidatePlanningSnapshot 从 authority 加载并验证 generation、账本和上下文。
func loadAndValidatePlanningSnapshot(store *gate.DurationLedgerStore, planningContext gate.PlanningContext) (gate.DurationLedgerSnapshot, error) {
	current, err := store.LoadPlanning(planningContext)
	if err != nil {
		return gate.DurationLedgerSnapshot{}, fmt.Errorf("load remote CI planning snapshot from authority: %w", err)
	}
	if current.Generation == 0 {
		return gate.DurationLedgerSnapshot{}, errors.New("remote CI planning duration ledger generation is required")
	}
	if err := gate.ValidateDurationLedger(current.Ledger); err != nil {
		return gate.DurationLedgerSnapshot{}, fmt.Errorf("validate remote CI planning duration ledger: %w", err)
	}
	if _, err := gate.DurationSampleIndexFromSnapshot(current, planningContext); err != nil {
		return gate.DurationLedgerSnapshot{}, fmt.Errorf("validate remote CI planning snapshot context: %w", err)
	}
	return current, nil
}

// bindPlanningOverheadDigest 绑定 normal miss 的 accepted overhead 身份并拒绝刷新漂移。
func (prepared *PreparedRun) bindPlanningOverheadDigest(snapshot gate.DurationLedgerSnapshot) error {
	if prepared.allReused || prepared.input.Calibration {
		return nil
	}
	digest, err := remoteShardOverheadIdentityDigest(*snapshot.Ledger.ShardOverhead)
	if err != nil {
		return fmt.Errorf("derive remote CI shard overhead identity: %w", err)
	}
	if prepared.planningOverheadDigest != "" && prepared.planningOverheadDigest != digest {
		return errors.New("remote CI planning shard overhead identity drifted between reload and execution")
	}
	prepared.planningOverheadDigest = digest
	return nil
}

// RunPrepared 创建独立 job，并消费一个先前冻结的计划。
func (coordinator *Coordinator) RunPrepared(ctx context.Context, prepared *PreparedRun) (result RunResult, returnErr error) {
	if err := remoteCIRunNotStartedContextError(ctx); err != nil {
		return result, err
	}
	if err := prepared.consume(coordinator); err != nil {
		return result, err
	}
	if err := validatePreparedRunInput(ctx, prepared); err != nil {
		return result, err
	}
	jobID, err := coordinator.newID()
	if err != nil {
		return result, fmt.Errorf("create remote CI job identity: %w", err)
	}
	result = coordinator.newRunResult(prepared.plan, prepared.catalogDigest, prepared.entrypoint, prepared.input, jobID)
	result.Scope = prepared.scope
	// 先绑定 reuse 投影，再检查 job 身份后的取消边界，确保取消 provisional 仍能验证 all-hit。
	prepared.reuse.apply(&result)
	coordinator.progress.setJobID(jobID)
	coordinator.progress.phase(ProgressPhaseRun, "started")
	defer coordinator.persistPreparedRun(ctx, prepared.input.LedgerStore, &result, &returnErr)
	if err := remoteCIRunNotStartedContextError(ctx); err != nil {
		return result, err
	}
	return coordinator.executePreparedWorkloadMisses(ctx, prepared, jobID, result)
}

// validateAllHitExecutionIdentity 禁止调用方把 MISS-only Gate/ECI 身份带入纯 PASS 复用。
func validateAllHitExecutionIdentity(input RunInput) error {
	if input.CandidateGateSourceSHA256 != "" ||
		input.CandidateGateToolchainSHA256 != "" ||
		input.ExecutionRunnerImage != "" ||
		input.ExecutionImageCacheSnapshotID != "" ||
		input.ImageCacheOnly {
		return errors.New("remote CI all-hit cannot carry MISS execution identity")
	}
	return nil
}

// validatePreparedRunInput 对 all-hit 保持 correctness-only 校验，对 MISS 强制要求显式实时绑定。
func validatePreparedRunInput(ctx context.Context, prepared *PreparedRun) error {
	if prepared.allReused {
		if err := validateAllHitExecutionIdentity(prepared.input); err != nil {
			return err
		}
		return validateCoordinatorPrepareInput(ctx, prepared.input)
	}
	if prepared.verifiedExecutionSnapshotID == "" {
		return errors.New("prepared remote CI MISS execution inputs are not bound")
	}
	config := prepared.owner.config
	config.ImageCacheSnapshotID = prepared.verifiedExecutionSnapshotID
	return validateCoordinatorRunInput(ctx, config, prepared.input)
}

// persistPreparedRun 在所有返回边界记录同一个候选身份及其非权威终态。
func (coordinator *Coordinator) persistPreparedRun(ctx context.Context, store *gate.DurationLedgerStore, result *RunResult, returnErr *error) {
	markRemoteRunContextTerminalStatus(ctx, result)
	result.CompletedAt = coordinator.now().UTC()
	persistErr := recordRemoteCIRun(store, *result, *returnErr)
	*returnErr = errors.Join(*returnErr, persistErr)
	coordinator.progress.emitFinal(*result)
}

// executePreparedWorkloadMisses 负责已建立身份后的 catalog 写入、复用分支和 miss 清理。
func (coordinator *Coordinator) executePreparedWorkloadMisses(ctx context.Context, prepared *PreparedRun, jobID string, result RunResult) (returnResult RunResult, returnErr error) {
	if err := prepared.input.LedgerStore.RecordWorkloadCatalog(prepared.catalog, gate.WorkloadCatalogObservation{
		AcceptedGeneration: prepared.input.AcceptedGeneration,
		SourceTreeSHA:      prepared.input.Tree,
		Entrypoint:         prepared.entrypoint.ID,
		Profile:            prepared.plan.Profile,
		ObservedAt:         result.StartedAt,
	}); err != nil {
		return result, err
	}
	prepared.reuse.apply(&result)
	if prepared.allReused {
		coordinator.progress.phase(ProgressPhaseRun, "completed")
		coordinator.progress.emitTerminal(nil, "completed")
		coordinator.progress.beginCleanup(0)
		coordinator.progress.phase(ProgressPhaseCleanup, "completed")
		return completeRemoteReuse(prepared.executionCatalog, prepared.reuse.reused, result, coordinator.now)
	}
	tempRoot, err := createRemoteTempRoot()
	if err != nil {
		return result, err
	}
	objectKeys := make([]string, 0)
	createdGroups := make([]string, 0)
	defer func() {
		cleanupComplete, cleanupErr := finalizePreparedCleanup(
			tempRoot,
			func() error { return coordinator.cleanup(jobID, createdGroups, objectKeys) },
		)
		returnResult.CleanupComplete = cleanupComplete
		returnErr = errors.Join(returnErr, cleanupErr)
	}()
	return coordinator.runRemoteWorkloadMisses(
		ctx, prepared.input, prepared.plan, prepared.catalog, prepared.executionCatalog, prepared.reuse.cacheMisses,
		prepared.reuse.reused, jobID, tempRoot, &objectKeys, &createdGroups, result,
	)
}

// finalizePreparedCleanup 原子汇总宿主临时目录和远端对象清理结果。
// 两个清理动作都会执行，任一失败都会让 CleanupComplete 保持为 false。
func finalizePreparedCleanup(tempRoot string, remoteCleanup func() error) (bool, error) {
	if remoteCleanup == nil {
		return false, errors.New("remote cleanup callback is required")
	}
	tempErr := os.RemoveAll(tempRoot)
	remoteErr := remoteCleanup()
	return tempErr == nil && remoteErr == nil, errors.Join(tempErr, remoteErr)
}

// remoteCIRunNotStartedContextError 将无法建立候选身份的调用取消明确标记为未启动。
// 身份已由 newRunResult 建立后，调用方必须走 RunPrepared 的持久化 defer。
func remoteCIRunNotStartedContextError(ctx context.Context) error {
	if ctx == nil {
		return errors.New("remote CI run was not started: context is required")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("remote CI run was not started: %w", err)
	}
	return nil
}

// markRemoteRunContextTerminalStatus 将调用方取消边界映射为可审计的非权威终态。
// 只有协调器尚未确定其它终态时才更新，避免把已完成 PASS 改写成取消。
func markRemoteRunContextTerminalStatus(ctx context.Context, result *RunResult) {
	if ctx == nil || result == nil || result.Status != gate.ResultStatusFailed {
		return
	}
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		result.Status = gate.ResultStatusCancelled
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		result.Status = gate.ResultStatusTimeout
	}
}

// consume 在同一互斥边界校验所有权与冻结身份，并保证每个准备结果只能被执行一次。
func (prepared *PreparedRun) consume(coordinator *Coordinator) error {
	if prepared == nil {
		return errors.New("prepared remote CI run is required")
	}
	prepared.mu.Lock()
	defer prepared.mu.Unlock()
	if prepared.consumed {
		return errors.New("prepared remote CI run is already consumed")
	}
	if prepared.owner != coordinator {
		return errors.New("prepared remote CI run belongs to a different coordinator")
	}
	if err := prepared.validateFrozenLocked(); err != nil {
		return err
	}
	if err := prepared.validateLocked(); err != nil {
		return err
	}
	if !prepared.allReused {
		if err := prepared.reloadPlanningSnapshotLocked(prepared.input.LedgerStore); err != nil {
			return err
		}
	}
	prepared.consumed = true
	return nil
}

func (prepared *PreparedRun) validateFrozenLocked() error {
	if prepared.owner == nil || prepared.frozenDigest == "" {
		return errors.New("prepared remote CI run frozen identity is required")
	}
	digest, err := prepared.frozenIdentityDigest()
	if err != nil {
		return err
	}
	if digest != prepared.frozenDigest {
		return errors.New("prepared remote CI run identity drifted")
	}
	return nil
}

// frozenIdentityDigest 计算 Prepare 冻结载荷的唯一摘要，禁止执行前字段漂移。
func (prepared *PreparedRun) frozenIdentityDigest() (string, error) {
	input := prepared.input
	input.LedgerSnapshot = gate.DurationLedgerSnapshot{}
	input.LedgerStore = nil
	if prepared.scope == nil {
		return "", errors.New("prepared remote CI execution scope is required")
	}
	scopeDigest, err := prepared.scope.Digest()
	if err != nil {
		return "", fmt.Errorf("digest prepared remote CI execution scope: %w", err)
	}
	payload, err := json.Marshal(struct {
		Input                     RunInput                      `json:"input"`
		Plan                      gate.GatePlan                 `json:"plan"`
		Catalog                   gate.WorkloadCatalog          `json:"catalog"`
		CatalogDigest             string                        `json:"catalog_digest"`
		Entrypoint                gate.CIEntrypoint             `json:"entrypoint"`
		ScopeDigest               string                        `json:"scope_digest"`
		Excluded                  []gate.GateID                 `json:"excluded"`
		ReuseIdentities           []gate.WorkloadPassIdentity   `json:"reuse_identities"`
		ReusedWorkloads           []gate.WorkloadPassEvidence   `json:"reused_workloads"`
		ReexecutedWorkloadResults []gate.RemoteCIWorkloadResult `json:"reexecuted_workload_results"`
		CacheMissWorkloads        []gate.GateID                 `json:"cache_miss_workloads"`
	}{
		Input: input, Plan: prepared.plan, Catalog: prepared.catalog, CatalogDigest: prepared.catalogDigest,
		Entrypoint: prepared.entrypoint, ScopeDigest: scopeDigest,
		Excluded: prepared.excluded, ReuseIdentities: prepared.reuse.identities,
		ReusedWorkloads: prepared.reuse.reusedWorkloads, ReexecutedWorkloadResults: prepared.reuse.reexecutedWorkloadResults,
		CacheMissWorkloads: prepared.reuse.cacheMisses,
	})
	if err != nil {
		return "", fmt.Errorf("encode prepared remote CI identity: %w", err)
	}
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", sum), nil
}

// validateLocked 校验冻结计划、目录摘要和复用决策仍与准备时的候选上下文一致。
func (prepared *PreparedRun) validateLocked() error {
	if err := prepared.plan.Validate(); err != nil {
		return fmt.Errorf("validate prepared remote CI plan: %w", err)
	}
	catalogDigest, err := gate.WorkloadCatalogDigest(prepared.catalog)
	if err != nil {
		return err
	}
	if prepared.catalogDigest != catalogDigest {
		return errors.New("prepared remote CI catalog digest drifted")
	}
	if prepared.entrypoint.ID == "" || prepared.plan.Profile != prepared.input.Profile {
		return errors.New("prepared remote CI plan context drifted")
	}
	if prepared.allReused != prepared.reuse.allReused() {
		return errors.New("prepared remote CI reuse decision drifted")
	}
	return validatePreparedRemoteExecutionScope(prepared)
}

// validateFinalPlanningSnapshotLocked 在 normal miss 产生任何 OSS、临时目录或 ECI 副作用前，
// 强制校验同一 SQLite authority 的最终规划快照。all-hit 仍允许在无 accepted
// overhead 的 generation-one 状态下结束；normal miss 只依赖 accepted overhead aggregate，
// calibration metadata 可为空。
func (prepared *PreparedRun) validateFinalPlanningSnapshotLocked() error {
	if prepared.allReused || prepared.input.Calibration {
		return nil
	}
	snapshot := prepared.input.LedgerSnapshot
	planning := remotePlanningContext(prepared.input)
	overhead := snapshot.Ledger.ShardOverhead
	if err := validateFinalPlanningSnapshotGeneration(snapshot); err != nil {
		return err
	}
	if err := validateFinalPlanningOverhead(overhead, planning, prepared.input.AcceptedGeneration); err != nil {
		return err
	}
	if _, err := gate.DurationSampleIndexFromSnapshot(snapshot, planning); err != nil {
		return fmt.Errorf("validate final remote CI planning snapshot context: %w", err)
	}
	return nil
}

// validateFinalPlanningSnapshotGeneration 要求 normal miss 绑定有效的 ledger generation。
func validateFinalPlanningSnapshotGeneration(snapshot gate.DurationLedgerSnapshot) error {
	if snapshot.Generation == 0 {
		return errors.New("remote CI normal workload miss planning snapshot generation is required")
	}
	return nil
}

// validateFinalPlanningOverhead 校验最终快照中的 accepted shard overhead 与规划身份一致。
func validateFinalPlanningOverhead(overhead *gate.ShardOrchestrationOverhead, planning gate.PlanningContext, generation uint64) error {
	if overhead == nil {
		return errors.New("remote CI normal workload miss requires accepted shard overhead")
	}
	if err := gate.ValidateShardOrchestrationOverhead(*overhead); err != nil {
		return fmt.Errorf("validate final remote CI shard overhead: %w", err)
	}
	if overhead.AcceptedGeneration != generation {
		return fmt.Errorf("final remote CI shard overhead accepted generation %d does not match run generation %d", overhead.AcceptedGeneration, generation)
	}
	if overhead.AcceptedSnapshotID != planning.AcceptedSnapshotID ||
		overhead.Platform != planning.Platform ||
		overhead.Runner != planning.Runner ||
		overhead.Toolchain != planning.Toolchain {
		return errors.New("final remote CI shard overhead identity does not match accepted planning context")
	}
	return nil
}

// remoteShardOverheadIdentityDigest 使用结构体 JSON 的仓库 canonical 编码绑定完整 overhead 身份。
func remoteShardOverheadIdentityDigest(overhead gate.ShardOrchestrationOverhead) (string, error) {
	encoded, err := json.Marshal(overhead)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", digest), nil
}
