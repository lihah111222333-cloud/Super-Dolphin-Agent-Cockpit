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
	allReused              bool
	mu                     sync.Mutex
	consumed               bool
	owner                  *Coordinator
	frozenDigest           string
	input                  RunInput
	plan                   gate.GatePlan
	catalog                gate.WorkloadCatalog
	catalogDigest          string
	entrypoint             gate.CIEntrypoint
	reuse                  remoteWorkloadReusePreparation
	planningOverheadDigest string
}

// Prepare 一次性构造精确候选的计划、目录、摘要与复用决策。
func (coordinator *Coordinator) Prepare(ctx context.Context, input RunInput) (*PreparedRun, error) {
	coordinator.progress.phase(ProgressPhasePrepare, "started")
	if err := validateCoordinatorRunInput(ctx, coordinator.config, input); err != nil {
		return nil, err
	}
	plan, catalog, entrypoint, err := buildRemotePlan(input)
	if err != nil {
		return nil, err
	}
	// remoteWorkloadFingerprints( 先冻结 exact-tree digest/compile 输入；observed
	// closure 仅在 identity MISS 后由迁移 resolver 按需捕获。
	inputDigests, compileInputs, snapshot, err := remoteWorkloadFingerprintsWithSnapshot(ctx, input.RepositoryRoot, input.Tree, remoteShardableWorkloads(catalog))
	if err != nil {
		return nil, err
	}
	catalog, err = bindRemoteWorkloadInputDigests(catalog, inputDigests)
	if err != nil {
		return nil, err
	}
	input.WorkloadInputDigests = cloneRemoteWorkloadInputDigests(inputDigests)
	input.workloadInputSnapshot = snapshot
	input.workloadInputClosures = nil
	if err := validateRemoteCompileGroupInputs(catalog, compileInputs); err != nil {
		return nil, err
	}
	input.WorkloadCompileGroupInputs = cloneRemoteCompileGroupInputs(compileInputs)
	catalogDigest, err := gate.WorkloadCatalogDigest(catalog)
	if err != nil {
		return nil, err
	}
	reuse, err := prepareRemoteWorkloadReuse(
		ctx,
		input,
		catalog,
		coordinator.config.WorkerTimeout,
		coordinator.config.ResourcePolicy,
	)
	if err != nil {
		return nil, err
	}
	// Resolver 已经完成 MISS-only closure 捕获；PreparedRun 不再持有 Go/AST/blob
	// cache，只保留负路径需要的 immutable exact-tree entries/index。
	input.workloadInputSnapshot = remoteMigrationSnapshot(snapshot)
	prepared := &PreparedRun{
		allReused:     reuse.allReused(),
		owner:         coordinator,
		input:         input,
		plan:          plan,
		catalog:       catalog,
		catalogDigest: catalogDigest,
		entrypoint:    entrypoint,
		reuse:         reuse,
	}
	frozenDigest, err := prepared.frozenIdentityDigest()
	if err != nil {
		return nil, err
	}
	prepared.frozenDigest = frozenDigest
	coordinator.progress.setCacheCounts(len(reuse.reused), len(reuse.cacheMisses), len(reuse.reused))
	coordinator.progress.phase(ProgressPhasePrepare, "completed")
	return prepared, nil
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

// RefreshPlanningSnapshot 仅在同步校准后，按冻结的 SQLite 权威和计划上下文重新加载耗时计划快照。
func (prepared *PreparedRun) RefreshPlanningSnapshot(store *gate.DurationLedgerStore) error {
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
		return errors.New("remote CI planning shard overhead identity drifted between refresh and execution")
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
	if err := validateCoordinatorRunInput(ctx, coordinator.config, prepared.input); err != nil {
		return result, err
	}
	jobID, err := coordinator.newID()
	if err != nil {
		return result, fmt.Errorf("create remote CI job identity: %w", err)
	}
	result = coordinator.newRunResult(prepared.plan, prepared.catalogDigest, prepared.entrypoint, prepared.input, jobID)
	coordinator.progress.setJobID(jobID)
	coordinator.progress.phase(ProgressPhaseRun, "started")
	defer coordinator.persistPreparedRun(ctx, prepared.input.LedgerStore, &result, &returnErr)
	if err := remoteCIRunNotStartedContextError(ctx); err != nil {
		return result, err
	}
	return coordinator.executePreparedWorkloadMisses(ctx, prepared, jobID, result)
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
		return completeRemoteReuse(prepared.catalog, prepared.reuse.reused, result, coordinator.now)
	}
	tempRoot, err := createRemoteTempRoot()
	if err != nil {
		return result, err
	}
	defer func() {
		returnErr = errors.Join(returnErr, os.RemoveAll(tempRoot))
	}()
	objectKeys := make([]string, 0)
	createdGroups := make([]string, 0)
	defer func() {
		cleanupErr := coordinator.cleanup(jobID, createdGroups, objectKeys)
		returnResult.CleanupComplete = cleanupErr == nil
		returnErr = errors.Join(returnErr, cleanupErr)
	}()
	return coordinator.runRemoteWorkloadMisses(
		ctx, prepared.input, prepared.plan, prepared.catalog, prepared.reuse.cacheMisses,
		prepared.reuse.reused, jobID, tempRoot, &objectKeys, &createdGroups, result,
	)
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

func (prepared *PreparedRun) frozenIdentityDigest() (string, error) {
	input := prepared.input
	input.LedgerSnapshot = gate.DurationLedgerSnapshot{}
	input.LedgerStore = nil
	payload, err := json.Marshal(struct {
		Input              RunInput                    `json:"input"`
		Plan               gate.GatePlan               `json:"plan"`
		Catalog            gate.WorkloadCatalog        `json:"catalog"`
		CatalogDigest      string                      `json:"catalog_digest"`
		Entrypoint         gate.CIEntrypoint           `json:"entrypoint"`
		ReuseIdentities    []gate.WorkloadPassIdentity `json:"reuse_identities"`
		ReusedWorkloads    []gate.WorkloadPassEvidence `json:"reused_workloads"`
		CacheMissWorkloads []gate.GateID               `json:"cache_miss_workloads"`
	}{input, prepared.plan, prepared.catalog, prepared.catalogDigest, prepared.entrypoint, prepared.reuse.identities, prepared.reuse.reusedWorkloads, prepared.reuse.cacheMisses})
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
	return nil
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
