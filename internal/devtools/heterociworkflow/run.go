package heterociworkflow

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociowner"
)

// Execute 构造并运行一次显式依赖绑定的异构 workflow。
func Execute(ctx context.Context, deps Dependencies, request RunRequest) (RunResult, error) {
	workflow, err := New(deps)
	if err != nil {
		return RunResult{}, err
	}
	return workflow.Run(ctx, request)
}

// Run 并行推进独立 lane 的确定性 wave/batch 图；任意 mutation 或 batch 错误均 fail-closed。
// 每个 batch 只允许一次 ExecuteBatch，且执行端在首个 Start 前完成唯一绑定。
func (workflow *Workflow) Run(ctx context.Context, request RunRequest) (RunResult, error) {
	if err := validateRunInvocation(workflow, ctx, request); err != nil {
		return RunResult{}, err
	}
	prepared, err := workflow.planRun(ctx, request)
	if err != nil {
		return RunResult{}, err
	}
	prepared.state = newExecutionState(prepared.planned)
	if err := workflow.prepareExecution(ctx, request, &prepared); err != nil {
		return RunResult{}, workflow.failPlannedRun(ctx, prepared.runIdentity.DurableRunID, &prepared, err)
	}
	if err := workflow.executeRunBatches(ctx, &prepared); err != nil {
		return RunResult{}, workflow.failPlannedRun(ctx, prepared.runIdentity.DurableRunID, &prepared, err)
	}
	return buildRunResult(prepared.manifest, prepared.planned, prepared.state), nil
}

type preparedRun struct {
	acquired       heteroci.OwnerFence
	runIdentity    heteroci.TopologyRunIdentity
	ids            operationIDs
	manifest       heteroci.Manifest
	sourceSnapshot heteroci.SnapshotSet
	planned        heterociowner.DurableReservedRoutingPlan
	state          *executionState
	batches        []BatchPlan
	executors      map[ExecutorKey]BatchExecutor
}

// planRun 完成 strict load、owner acquire、initial snapshot 与 durable PlanAndReserve。
func (workflow *Workflow) planRun(ctx context.Context, request RunRequest) (preparedRun, error) {
	runIdentity, err := heteroci.DeriveTopologyRunIdentity(request.RepositoryID, request.SourceTreeSHA, request.RunID)
	if err != nil {
		return preparedRun{}, fmt.Errorf("derive heteroci durable run identity: %w", err)
	}
	ids, err := deriveRunOperationIDs(runIdentity.DurableRunID, request.Units)
	if err != nil {
		return preparedRun{}, err
	}
	manifest, err := workflow.loadRunManifest(request.ManifestPath)
	if err != nil {
		return preparedRun{}, err
	}
	fence, err := workflow.deps.Owner.Acquire(ctx)
	if err != nil {
		return preparedRun{}, fmt.Errorf("acquire heteroci owner: %w", err)
	}
	if err := fence.Validate(); err != nil {
		return preparedRun{}, fmt.Errorf("validate acquired heteroci owner fence: %w", err)
	}
	if err := workflow.rejectExistingRun(ctx, runIdentity.DurableRunID); err != nil {
		return preparedRun{}, err
	}
	now, err := workflow.now()
	if err != nil {
		return preparedRun{}, err
	}
	snapshot, err := workflow.acquireSnapshot(ctx, manifest, fence, now, request.HeartbeatTTL)
	if err != nil {
		return preparedRun{}, err
	}
	planned, err := workflow.deps.Owner.PlanAndReserve(ctx, fence, heteroci.TopologyPlanRequest{
		OperationID:    ids.plan,
		RunIdentity:    runIdentity,
		Manifest:       manifest,
		SourceSnapshot: snapshot,
		Units:          request.Units,
		HeartbeatTTL:   request.HeartbeatTTL,
	})
	if err != nil {
		return preparedRun{}, fmt.Errorf("plan and reserve heteroci run: %w", err)
	}
	return preparedRun{acquired: fence, runIdentity: runIdentity, ids: ids, manifest: manifest, sourceSnapshot: snapshot, planned: planned}, nil
}

// prepareExecution 校验 durable read-back、构建批次并在首个 Start 前绑定全部 executor。
func (workflow *Workflow) prepareExecution(ctx context.Context, request RunRequest, prepared *preparedRun) error {
	if err := validatePlannedProjection(prepared.acquired, prepared.ids.plan, request.Units, prepared.planned); err != nil {
		return err
	}
	state, err := workflow.deps.Owner.ReadRun(ctx, prepared.runIdentity.DurableRunID)
	if err != nil {
		return fmt.Errorf("%w: read durable run before execution: %w", ErrExecutionState, err)
	}
	if err := validatePreExecutionRunState(prepared.acquired, string(prepared.runIdentity.DurableRunID), prepared.manifest, request.Units, request.HeartbeatTTL, prepared.planned, state); err != nil {
		return err
	}
	now, err := workflow.now()
	if err != nil {
		return err
	}
	prepared.batches, err = BuildBatches(string(prepared.runIdentity.DurableRunID), prepared.manifest, prepared.planned, request.Units, now, request.HeartbeatTTL)
	if err != nil {
		return fmt.Errorf("%w: build execution batches: %w", ErrExecutionState, err)
	}
	prepared.executors, err = workflow.bindExecutors(ctx, prepared.batches)
	return err
}

// executeRunBatches 并行推进独立 lane，并保持同一 local node 的 wave barrier。
func (workflow *Workflow) executeRunBatches(ctx context.Context, prepared *preparedRun) error {
	return workflow.executeRunBatchesConcurrent(ctx, prepared)
}

// failPlannedRun 对 PlanAndReserve 之后的失败执行统一 bounded cleanup。
func (workflow *Workflow) failPlannedRun(ctx context.Context, runID heteroci.TopologyRunID, prepared *preparedRun, primary error) error {
	cleanupErr := workflow.cleanup(ctx, runID, prepared.planned.Leases, prepared.state, prepared.ids)
	return joinWorkflowFailure(primary, cleanupErr)
}

type operationIDs struct {
	plan   heteroci.OperationID
	start  map[string]heteroci.OperationID
	finish map[string]heteroci.OperationID
	cancel map[string]heteroci.OperationID
}

// executionState 保存当前 fence 和本次 workflow 已观察到的 durable lease 状态。
type executionState struct {
	mu        sync.Mutex
	fence     heteroci.OwnerFence
	decision  heteroci.RoutingDecision
	planned   map[string]heteroci.RoutingLease
	started   map[string]heteroci.StartedRoutingLease
	uncertain map[string]struct{}
	terminal  map[string]heteroci.RoutingLease
	results   map[string]UnitResult
	failures  []error
}

// validateRunInvocation 校验 workflow、context 和请求边界，不产生任何 owner mutation。
func validateRunInvocation(workflow *Workflow, ctx context.Context, request RunRequest) error {
	if err := validateWorkflowReady(workflow); err != nil {
		return err
	}
	return validateRunRequest(ctx, request)
}

// validateWorkflowReady 校验已构造 workflow 的所有显式依赖仍然存在。
func validateWorkflowReady(workflow *Workflow) error {
	if workflow == nil {
		return ErrInvalidDependencies
	}
	if isNilWorkflowValue(workflow.deps.Owner) || isNilWorkflowValue(workflow.deps.ProbeTransport) || isNilWorkflowValue(workflow.deps.Executors) {
		return ErrInvalidDependencies
	}
	if workflow.deps.LoadManifest == nil || workflow.deps.AcquireSnapshot == nil || workflow.deps.Now == nil {
		return ErrInvalidDependencies
	}
	if workflow.deps.CleanupTimeout <= 0 {
		return ErrInvalidDependencies
	}
	return nil
}

// validateRunRequest 校验 context、manifest path、run audit、TTL 和 execution units。
func validateRunRequest(ctx context.Context, request RunRequest) error {
	if ctx == nil {
		return fmt.Errorf("%w: context is required", ErrInvalidRequest)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: context is already done: %w", ErrInvalidRequest, err)
	}
	if request.ManifestPath == "" || strings.TrimSpace(request.ManifestPath) != request.ManifestPath {
		return fmt.Errorf("%w: manifest path is invalid", ErrInvalidRequest)
	}
	if err := heteroci.ValidateTopologyAuditIdentity(request.RepositoryID, request.SourceTreeSHA, request.RunID); err != nil {
		return fmt.Errorf("%w: run audit identity is invalid: %w", ErrInvalidRequest, err)
	}
	if err := validateWorkflowRunID(request.RunID); err != nil {
		return fmt.Errorf("%w: run identity is invalid", ErrInvalidRequest)
	}
	if request.HeartbeatTTL <= 0 || request.HeartbeatTTL.Milliseconds() <= 0 {
		return fmt.Errorf("%w: heartbeat TTL is invalid", ErrInvalidRequest)
	}
	if err := heteroci.ValidateExecutionUnits(request.Units); err != nil {
		return fmt.Errorf("%w: execution units are invalid: %w", ErrInvalidRequest, err)
	}
	return nil
}

// deriveRunOperationIDs 在任何 owner mutation 前预计算整次运行的确定性 operation identity。
func deriveRunOperationIDs(runID heteroci.TopologyRunID, units []heteroci.ExecutionUnit) (operationIDs, error) {
	plan, err := DeriveOperationID(runID, OperationPhasePlan, "")
	if err != nil {
		return operationIDs{}, err
	}
	ids := operationIDs{plan: plan, start: make(map[string]heteroci.OperationID, len(units)), finish: make(map[string]heteroci.OperationID, len(units)), cancel: make(map[string]heteroci.OperationID, len(units))}
	for _, unit := range units {
		start, err := DeriveOperationID(runID, OperationPhaseStart, unit.UnitID)
		if err != nil {
			return operationIDs{}, err
		}
		finish, err := DeriveOperationID(runID, OperationPhaseFinish, unit.UnitID)
		if err != nil {
			return operationIDs{}, err
		}
		cancel, err := DeriveOperationID(runID, OperationPhaseCancelPlanned, unit.UnitID)
		if err != nil {
			return operationIDs{}, err
		}
		ids.start[unit.UnitID] = start
		ids.finish[unit.UnitID] = finish
		ids.cancel[unit.UnitID] = cancel
	}
	return ids, nil
}

// loadRunManifest 通过显式 loader 读取并再次校验本地非权威清单。
func (workflow *Workflow) loadRunManifest(path string) (heteroci.Manifest, error) {
	manifest, err := workflow.deps.LoadManifest(path)
	if err != nil {
		return heteroci.Manifest{}, fmt.Errorf("%w: load heteroci manifest: %w", ErrInvalidRequest, err)
	}
	if err := manifest.Validate(); err != nil {
		return heteroci.Manifest{}, fmt.Errorf("%w: validate heteroci manifest: %w", ErrInvalidRequest, err)
	}
	if manifest.Authority != heteroci.AuthorityLocalNonAuthoritative {
		return heteroci.Manifest{}, fmt.Errorf("%w: manifest authority is not local non-authoritative", ErrInvalidRequest)
	}
	return manifest, nil
}

// rejectExistingRun 防止 takeover 后同一 run identity 在新 epoch 被再次规划。
func (workflow *Workflow) rejectExistingRun(ctx context.Context, runID heteroci.TopologyRunID) error {
	states, err := workflow.deps.Owner.LocateRun(ctx, runID)
	if err != nil {
		return fmt.Errorf("%w: locate durable run before planning: %w", ErrExecutionState, err)
	}
	if len(states) != 0 {
		return fmt.Errorf("%w: run identity already has durable state", ErrExecutionState)
	}
	return nil
}

// now 读取并校验 workflow clock 的正 Unix 时间。
func (workflow *Workflow) now() (time.Time, error) {
	now := workflow.deps.Now()
	if now.UnixMilli() <= 0 {
		return time.Time{}, fmt.Errorf("%w: workflow clock must be after Unix epoch", ErrInvalidRequest)
	}
	return now, nil
}

// acquireSnapshot 采集并校验一次与 owner epoch 绑定的实时快照。
func (workflow *Workflow) acquireSnapshot(ctx context.Context, manifest heteroci.Manifest, fence heteroci.OwnerFence, now time.Time, ttl time.Duration) (heteroci.SnapshotSet, error) {
	snapshot, err := workflow.deps.AcquireSnapshot(ctx, manifest, fence.OwnerEpoch, workflow.deps.ProbeTransport, now, ttl)
	if err != nil {
		return heteroci.SnapshotSet{}, fmt.Errorf("acquire heteroci snapshot: %w", err)
	}
	if snapshot.OwnerEpoch != fence.OwnerEpoch {
		return heteroci.SnapshotSet{}, fmt.Errorf("%w: snapshot owner epoch drifted", ErrExecutionState)
	}
	if err := snapshot.ValidateAgainst(manifest, now, ttl); err != nil {
		return heteroci.SnapshotSet{}, fmt.Errorf("%w: validate heteroci snapshot: %w", ErrExecutionState, err)
	}
	return snapshot, nil
}

// validatePlannedProjection 校验 owner plan 返回值和调用输入的不可变绑定。
func validatePlannedProjection(acquired heteroci.OwnerFence, operationID heteroci.OperationID, units []heteroci.ExecutionUnit, planned heterociowner.DurableReservedRoutingPlan) error {
	if err := validatePlannedFence(acquired, operationID, planned); err != nil {
		return err
	}
	if err := validatePlannedSnapshots(acquired, planned); err != nil {
		return err
	}
	return validatePlannedAuthorityAndCoverage(units, planned)
}

// validatePlannedFence 校验 operation 与 committed fence 的 owner/revision 绑定。
func validatePlannedFence(acquired heteroci.OwnerFence, operationID heteroci.OperationID, planned heterociowner.DurableReservedRoutingPlan) error {
	if err := planned.OperationID.Validate(); err != nil {
		return fmt.Errorf("%w: durable plan operation is invalid", ErrExecutionState)
	}
	if planned.OperationID != operationID {
		return fmt.Errorf("%w: durable plan operation drifted", ErrExecutionState)
	}
	if !validNextFence(acquired, planned.Fence) {
		return fmt.Errorf("%w: durable plan fence or operation drifted", ErrExecutionState)
	}
	if err := planned.Fence.Validate(); err != nil {
		return fmt.Errorf("%w: durable plan fence is invalid", ErrExecutionState)
	}
	return nil
}

// validatePlannedSnapshots 校验 source/effective snapshot 与 acquired epoch 一致。
func validatePlannedSnapshots(acquired heteroci.OwnerFence, planned heterociowner.DurableReservedRoutingPlan) error {
	if planned.SourceSnapshot.OwnerEpoch != acquired.OwnerEpoch || planned.EffectiveSnapshot.OwnerEpoch != acquired.OwnerEpoch {
		return fmt.Errorf("%w: durable plan snapshot epoch drifted", ErrExecutionState)
	}
	return nil
}

// validatePlannedAuthorityAndCoverage 校验本地 authority 与 lease 数量覆盖。
func validatePlannedAuthorityAndCoverage(units []heteroci.ExecutionUnit, planned heterociowner.DurableReservedRoutingPlan) error {
	if planned.Decision.Authority != heteroci.AuthorityLocalNonAuthoritative || planned.Plan.Authority != heteroci.AuthorityLocalNonAuthoritative {
		return fmt.Errorf("%w: durable plan authority is invalid", ErrExecutionState)
	}
	if len(planned.Leases) != len(units) {
		return fmt.Errorf("%w: durable plan lease coverage is incomplete", ErrExecutionState)
	}
	return nil
}

// validatePreExecutionRunState 确保 Start 前 run 仍是唯一、完整且全 planned 的投影。
func validatePreExecutionRunState(fence heteroci.OwnerFence, runID string, manifest heteroci.Manifest, units []heteroci.ExecutionUnit, ttl time.Duration, planned heterociowner.DurableReservedRoutingPlan, state heterociowner.DurableRunState) error {
	if err := validateRunStateHead(fence, runID, ttl, planned, state); err != nil {
		return err
	}
	if err := validateRunStateProjection(manifest, units, planned, state); err != nil {
		return err
	}
	return validateRunStateLeases(runID, planned.Leases, state.Leases)
}

type durableRunHeadIdentity struct {
	runID         string
	scope         heteroci.RoutingOwnerScope
	ownerID       heteroci.RoutingOwnerID
	ownerEpoch    heteroci.RoutingOwnerEpoch
	fenceRevision heteroci.RoutingOwnerRevision
	lastRevision  heteroci.RoutingOwnerRevision
	heartbeatTTL  time.Duration
	released      bool
}

// validateRunStateHead 用 comparable identity 校验 read-back run 的 owner head。
func validateRunStateHead(fence heteroci.OwnerFence, runID string, ttl time.Duration, planned heterociowner.DurableReservedRoutingPlan, state heterociowner.DurableRunState) error {
	observed := durableRunHeadIdentity{runID: state.RunID, scope: state.Fence.Scope, ownerID: state.Fence.OwnerID, ownerEpoch: state.Fence.OwnerEpoch, fenceRevision: state.Fence.Revision, lastRevision: state.LastRevision, heartbeatTTL: state.HeartbeatTTL, released: state.Released}
	want := durableRunHeadIdentity{runID: runID, scope: fence.Scope, ownerID: planned.Fence.OwnerID, ownerEpoch: planned.Fence.OwnerEpoch, fenceRevision: planned.Fence.Revision, lastRevision: planned.Fence.Revision, heartbeatTTL: ttl}
	if observed != want {
		return fmt.Errorf("%w: durable run identity or head drifted", ErrExecutionState)
	}
	return nil
}

// validateRunStateProjection 校验 manifest、units 和 decision read-back 不可变。
func validateRunStateProjection(manifest heteroci.Manifest, units []heteroci.ExecutionUnit, planned heterociowner.DurableReservedRoutingPlan, state heterociowner.DurableRunState) error {
	if !reflect.DeepEqual(state.Manifest, manifest) {
		return fmt.Errorf("%w: durable run projection drifted", ErrExecutionState)
	}
	if !reflect.DeepEqual(state.Units, units) {
		return fmt.Errorf("%w: durable run projection drifted", ErrExecutionState)
	}
	if !reflect.DeepEqual(state.Decision, planned.Decision) {
		return fmt.Errorf("%w: durable run projection drifted", ErrExecutionState)
	}
	return nil
}

// validateRunStateLeases 校验 read-back leases 与 PlanAndReserve 投影逐 unit 完全一致。
func validateRunStateLeases(runID string, planned, observed []heteroci.RoutingLease) error {
	if len(observed) != len(planned) {
		return fmt.Errorf("%w: durable run lease count drifted", ErrExecutionState)
	}
	want := make(map[string]heteroci.RoutingLease, len(planned))
	for _, lease := range planned {
		want[lease.UnitID] = lease
	}
	seen := make(map[string]struct{}, len(observed))
	for _, lease := range observed {
		if _, duplicate := seen[lease.UnitID]; duplicate {
			return fmt.Errorf("%w: durable run lease projection is duplicated", ErrExecutionState)
		}
		expected, ok := want[lease.UnitID]
		if !validObservedPlannedLease(ok, runID, expected, lease) {
			return fmt.Errorf("%w: durable run lease projection drifted", ErrExecutionState)
		}
		seen[lease.UnitID] = struct{}{}
	}
	return nil
}

// validObservedPlannedLease 判断 read-back lease 是否等于对应 planned projection。
func validObservedPlannedLease(found bool, runID string, expected, observed heteroci.RoutingLease) bool {
	return found && observed.RunID == runID && observed.State == heteroci.LeaseStatePlanned && reflect.DeepEqual(observed, expected)
}

// bindExecutors 在任何 Start 前按 batch 首次出现顺序绑定唯一 executor。
func (workflow *Workflow) bindExecutors(ctx context.Context, batches []BatchPlan) (map[ExecutorKey]BatchExecutor, error) {
	bound := make(map[ExecutorKey]BatchExecutor, len(batches))
	for _, batch := range batches {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("%w: bind executor context: %w", ErrExecutorBinding, err)
		}
		if _, exists := bound[batch.Batch.Key]; exists {
			continue
		}
		executor, err := workflow.deps.Executors.Bind(batch.Batch.Key)
		if err != nil {
			return nil, fmt.Errorf("%w: bind executor: %w", ErrExecutorBinding, err)
		}
		if isNilWorkflowValue(executor) {
			return nil, fmt.Errorf("%w: bound executor is nil", ErrExecutorBinding)
		}
		bound[batch.Batch.Key] = executor
	}
	return bound, nil
}

// validatePreparedSourceSnapshot 校验一次采集的 source snapshot 在所有 Start 前保持不变。
func validatePreparedSourceSnapshot(prepared *preparedRun) error {
	if prepared == nil {
		return fmt.Errorf("%w: prepared execution state is missing", ErrExecutionState)
	}
	if !reflect.DeepEqual(prepared.sourceSnapshot, prepared.planned.SourceSnapshot) {
		return fmt.Errorf("%w: source snapshot identity drifted before Start", ErrExecutionState)
	}
	return nil
}

// startBatchUnitsLocked 在已持有 state 锁时顺序推进 owner Start 和 fence。
func (workflow *Workflow) startBatchUnitsLocked(ctx context.Context, runID heteroci.TopologyRunID, decision heteroci.RoutingDecision, ids operationIDs, plan BatchPlan, snapshot heteroci.SnapshotSet, state *executionState) ([]heteroci.StartedRoutingLease, error) {
	started := make([]heteroci.StartedRoutingLease, 0, len(plan.Batch.Units))
	for _, unit := range plan.Batch.Units {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("%w: start context: %w", ErrExecutionState, err)
		}
		start, err := workflow.deps.Owner.Start(ctx, state.fence, ids.start[unit.UnitID], runID, unit.UnitID, snapshot)
		if err != nil {
			state.uncertain[unit.UnitID] = struct{}{}
			return nil, fmt.Errorf("%w: start unit: %w", ErrExecutionState, err)
		}
		validationNow, err := workflow.now()
		if err != nil {
			state.uncertain[unit.UnitID] = struct{}{}
			return nil, err
		}
		expected := state.planned[unit.UnitID]
		if err := validateStartedLease(state.fence, string(runID), unit.UnitID, expected, decision, start, validationNow); err != nil {
			state.uncertain[unit.UnitID] = struct{}{}
			return nil, err
		}
		state.fence = start.Fence
		state.started[unit.UnitID] = start
		started = append(started, start)
	}
	return started, nil
}

// executeBoundBatch 调用唯一已绑定 executor 一次并验证 canonical 本地结果。
func (workflow *Workflow) executeBoundBatch(ctx context.Context, batch ExecutionBatch, executors map[ExecutorKey]BatchExecutor, state *executionState) (map[string]heteroci.RoutingLeaseState, error) {
	executor := executors[batch.Key]
	if isNilWorkflowValue(executor) {
		return nil, fmt.Errorf("%w: executor disappeared after binding", ErrExecutionState)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: execute context: %w", ErrExecutionState, err)
	}
	result, err := workflow.executeWithReceipt(ctx, executor, batch, state)
	if err != nil {
		return nil, err
	}
	outcomes, err := normalizeBatchResult(batch, result)
	if err != nil {
		markBatchUncertain(state, batch)
		return nil, err
	}
	return outcomes, nil
}

// finishBatchUnitsLocked 在已持有 state 锁时顺序推进 owner Finish 和终态。
func (workflow *Workflow) finishBatchUnitsLocked(ctx context.Context, runID heteroci.TopologyRunID, decision heteroci.RoutingDecision, ids operationIDs, batch ExecutionBatch, outcomes map[string]heteroci.RoutingLeaseState, state *executionState) error {
	var outcomeFailures []error
	for _, unit := range batch.Units {
		terminal := outcomes[unit.UnitID]
		if terminal == heteroci.LeaseStateFailed {
			failure := &BatchOutcomeError{BatchID: batch.BatchID, UnitID: unit.UnitID}
			outcomeFailures = append(outcomeFailures, failure)
			state.failures = append(state.failures, failure)
		}
		startedLease := state.started[unit.UnitID].Lease
		previousFence := state.fence
		transition, finishErr := workflow.deps.Owner.Finish(ctx, previousFence, ids.finish[unit.UnitID], runID, unit.UnitID, terminal)
		transition, err := workflow.finishUnitWithRecovery(ctx, previousFence, runID, unit.UnitID, ids.finish[unit.UnitID], terminal, startedLease, decision, transition, finishErr, state)
		if err != nil {
			return joinOutcomeFailures(err, outcomeFailures)
		}
		state.fence = transition.Fence
		state.terminal[unit.UnitID] = transition.Lease
		delete(state.started, unit.UnitID)
		state.results[unit.UnitID] = UnitResult{Authority: heteroci.AuthorityLocalNonAuthoritative, Status: ResultStatusNotVerified, UnitID: unit.UnitID, BatchID: batch.BatchID, State: transition.Lease.State}
	}
	return joinOutcomeFailures(nil, outcomeFailures)
}

// joinOutcomeFailures 保留已知 batch failure 与后续 Finish 错误的完整错误链。
func joinOutcomeFailures(primary error, failures []error) error {
	if primary == nil && len(failures) == 0 {
		return nil
	}
	errorsToJoin := append([]error(nil), failures...)
	if primary != nil {
		errorsToJoin = append(errorsToJoin, primary)
	}
	return errors.Join(errorsToJoin...)
}

// executeWithReceipt 将 executor 错误收敛为一次 receipt 查询，禁止猜测失败或再次执行。
func (workflow *Workflow) executeWithReceipt(ctx context.Context, executor BatchExecutor, batch ExecutionBatch, state *executionState) (BatchResult, error) {
	result, err := executor.ExecuteBatch(ctx, cloneExecutionBatch(batch))
	if err == nil {
		return result, nil
	}
	reader, ok := executor.(BatchReceiptReader)
	if !ok {
		markBatchUncertain(state, batch)
		return BatchResult{}, errors.Join(ErrExecutionState, ErrBatchReceiptRequired, err)
	}
	receiptCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), workflow.deps.CleanupTimeout)
	defer cancel()
	receipt, found, receiptErr := reader.ReadBatchReceipt(receiptCtx, batch)
	if receiptErr != nil {
		markBatchUncertain(state, batch)
		return BatchResult{}, errors.Join(ErrExecutionState, ErrBatchReceiptRequired, receiptErr)
	}
	if !found {
		markBatchUncertain(state, batch)
		return BatchResult{}, errors.Join(ErrExecutionState, ErrBatchReceiptRequired)
	}
	return receipt, nil
}

// cloneExecutionBatch 隔离 executor 对 slice backing array 的意外修改。
func cloneExecutionBatch(batch ExecutionBatch) ExecutionBatch {
	batch.Units = append([]heteroci.ExecutionUnit(nil), batch.Units...)
	batch.Assignments = append([]heteroci.RouteAssignment(nil), batch.Assignments...)
	batch.Started = append([]heteroci.StartedRoutingLease(nil), batch.Started...)
	return batch
}

// markBatchUncertain 记录没有 canonical receipt 的 started unit，cleanup 不得擅自 Finish。
func markBatchUncertain(state *executionState, batch ExecutionBatch) {
	state.mu.Lock()
	defer state.mu.Unlock()
	markBatchUncertainLocked(state, batch)
}

func markBatchUncertainLocked(state *executionState, batch ExecutionBatch) {
	for _, unit := range batch.Units {
		if _, started := state.started[unit.UnitID]; started {
			state.uncertain[unit.UnitID] = struct{}{}
		}
	}
}

// validateStartedLease 校验 Start 返回的 lease/fence 绑定，避免把错节点交给 executor。
func validateStartedLease(previous heteroci.OwnerFence, runID, unitID string, planned heteroci.RoutingLease, decision heteroci.RoutingDecision, started heteroci.StartedRoutingLease, now time.Time) error {
	if err := started.ValidateAgainst(decision, now); err != nil {
		return fmt.Errorf("%w: started lease projection is invalid", ErrExecutionState)
	}
	if !validNextFence(previous, started.Fence) || started.Lease.RunID != runID || started.Lease.UnitID != unitID {
		return fmt.Errorf("%w: started lease identity drifted", ErrExecutionState)
	}
	if started.Lease.State != heteroci.LeaseStateStarted || planned.UnitID != unitID || !sameLeaseIdentity(planned, started.Lease) {
		return fmt.Errorf("%w: started lease binding drifted", ErrExecutionState)
	}
	return nil
}

// validNextFence 判断 mutation fence 是否保持 owner identity 并严格推进 revision。
func validNextFence(previous, next heteroci.OwnerFence) bool {
	return next.Scope == previous.Scope && next.OwnerID == previous.OwnerID && next.OwnerEpoch == previous.OwnerEpoch && next.Revision == previous.Revision+1
}

// sameLeaseIdentity 比较 lease 的不可变路由绑定字段，不比较生命周期时间和状态。
func sameLeaseIdentity(planned, observed heteroci.RoutingLease) bool {
	return immutableLeaseIdentityOf(planned) == immutableLeaseIdentityOf(observed)
}

type immutableLeaseIdentity struct {
	schemaVersion   string
	authority       string
	runID           string
	decisionDigest  string
	unitID          string
	executor        heteroci.ExecutorKind
	nodeID          string
	endpoint        string
	probeIdentity   string
	probeBootID     string
	reservedAtMilli int64
}

// immutableLeaseIdentityOf 提取 lease 生命周期之外的 comparable identity。
func immutableLeaseIdentityOf(lease heteroci.RoutingLease) immutableLeaseIdentity {
	return immutableLeaseIdentity{schemaVersion: lease.SchemaVersion, authority: lease.Authority, runID: lease.RunID, decisionDigest: lease.DecisionDigest, unitID: lease.UnitID, executor: lease.Executor, nodeID: lease.NodeID, endpoint: lease.Endpoint, probeIdentity: lease.ProbeIdentity, probeBootID: lease.ProbeBootID, reservedAtMilli: lease.ReservedAtUnixMilli}
}

// validateFinishedLease 校验 Finish 返回的 terminal lease/fence 绑定。
func validateFinishedLease(previous heteroci.OwnerFence, runID, unitID string, started heteroci.RoutingLease, decision heteroci.RoutingDecision, transition heterociowner.DurableLeaseTransition, now time.Time) error {
	if err := transition.Lease.ValidateAgainst(decision, now); err != nil {
		return fmt.Errorf("%w: terminal lease projection is invalid", ErrExecutionState)
	}
	if !validNextFence(previous, transition.Fence) || transition.Lease.RunID != runID || transition.Lease.UnitID != unitID {
		return fmt.Errorf("%w: terminal lease identity drifted", ErrExecutionState)
	}
	if !validFinishState(transition.Lease.State) || !sameLeaseIdentity(started, transition.Lease) {
		return fmt.Errorf("%w: terminal lease identity drifted", ErrExecutionState)
	}
	return nil
}

// validFinishState 判断 terminal transition 是否为 Finish 允许的状态。
func validFinishState(state heteroci.RoutingLeaseState) bool {
	return state == heteroci.LeaseStateSucceeded || state == heteroci.LeaseStateFailed
}

// normalizeBatchResult 检查 executor 是否恰好返回每个 unit 的 canonical terminal outcome。
func normalizeBatchResult(batch ExecutionBatch, result BatchResult) (map[string]heteroci.RoutingLeaseState, error) {
	if err := validateBatchResultHeader(batch, result); err != nil {
		return nil, err
	}
	want := indexBatchUnitIDs(batch.Units)
	outcomes, err := indexBatchOutcomes(want, result.Outcomes)
	if err != nil {
		return nil, err
	}
	if len(outcomes) != len(want) {
		return nil, fmt.Errorf("%w: outcome coverage is incomplete", ErrBatchResult)
	}
	return outcomes, nil
}

// validateBatchResultHeader 校验本地 authority/status 与 outcome 数量。
func validateBatchResultHeader(batch ExecutionBatch, result BatchResult) error {
	if result.BatchID != batch.BatchID || result.RunID != batch.RunID || result.Executor != batch.Key {
		return fmt.Errorf("%w: batch result identity is not bound to current batch", ErrBatchResult)
	}
	if err := validateBatchResultDigests(batch, result); err != nil {
		return err
	}
	return validateBatchResultAuthority(result)
}

// validateBatchResultDigests 重算 material identity，并校验 command digest 形状。
func validateBatchResultDigests(batch ExecutionBatch, result BatchResult) error {
	expectedMaterial, err := DeriveExecutionMaterialDigest(batch)
	if err != nil || batch.MaterialDigest != expectedMaterial || result.MaterialDigest != expectedMaterial || !validWorkflowDigest(result.CommandDigest) {
		return fmt.Errorf("%w: batch result material or command digest is not bound to current batch", ErrBatchResult)
	}
	if len(result.Outcomes) != len(batch.Units) {
		return fmt.Errorf("%w: outcome count does not match batch", ErrBatchResult)
	}
	return nil
}

// validateBatchResultAuthority 校验本地非权威结果的固定 authority/status。
func validateBatchResultAuthority(result BatchResult) error {
	if result.Authority != heteroci.AuthorityLocalNonAuthoritative || result.Status != ResultStatusNotVerified {
		return fmt.Errorf("%w: batch result authority or status is invalid", ErrBatchResult)
	}
	return nil
}

// indexBatchUnitIDs 建立 batch unit identity 集合。
func indexBatchUnitIDs(units []heteroci.ExecutionUnit) map[string]struct{} {
	want := make(map[string]struct{}, len(units))
	for _, unit := range units {
		want[unit.UnitID] = struct{}{}
	}
	return want
}

// indexBatchOutcomes 校验并索引 executor 的唯一 terminal outcome。
func indexBatchOutcomes(want map[string]struct{}, values []BatchOutcome) (map[string]heteroci.RoutingLeaseState, error) {
	outcomes := make(map[string]heteroci.RoutingLeaseState, len(values))
	for _, outcome := range values {
		if !validBatchOutcome(want, outcome) {
			return nil, fmt.Errorf("%w: outcome identity or state is invalid", ErrBatchResult)
		}
		if _, duplicate := outcomes[outcome.UnitID]; duplicate {
			return nil, fmt.Errorf("%w: duplicate outcome", ErrBatchResult)
		}
		outcomes[outcome.UnitID] = outcome.State
	}
	return outcomes, nil
}

// validBatchOutcome 判断 outcome 是否属于 batch 且是合法 terminal state。
func validBatchOutcome(want map[string]struct{}, outcome BatchOutcome) bool {
	_, ok := want[outcome.UnitID]
	return ok && (outcome.State == heteroci.LeaseStateSucceeded || outcome.State == heteroci.LeaseStateFailed)
}

// newExecutionState 初始化本次 workflow 的 durable lease 状态表。
func newExecutionState(planned heterociowner.DurableReservedRoutingPlan) *executionState {
	leases := make(map[string]heteroci.RoutingLease, len(planned.Leases))
	for _, lease := range planned.Leases {
		leases[lease.UnitID] = lease
	}
	return &executionState{fence: planned.Fence, decision: planned.Decision, planned: leases, started: make(map[string]heteroci.StartedRoutingLease), uncertain: make(map[string]struct{}), terminal: make(map[string]heteroci.RoutingLease), results: make(map[string]UnitResult)}
}

// cleanup 收敛本次已启动 lease 和剩余 planned lease；使用 bounded detached context 且不重试 executor。
func (workflow *Workflow) cleanup(ctx context.Context, runID heteroci.TopologyRunID, planned []heteroci.RoutingLease, state *executionState, ids operationIDs) error {
	if state == nil {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), workflow.deps.CleanupTimeout)
	defer cancel()
	cleanupErrors := workflow.cleanupStarted(cleanupCtx, runID, state, ids)
	cleanupErrors = append(cleanupErrors, workflow.cleanupPlanned(cleanupCtx, runID, planned, state, ids)...)
	if len(cleanupErrors) != 0 {
		return errors.Join(cleanupErrors...)
	}
	return nil
}

// cleanupStarted 将 outcome 已知未执行的 started lease 收敛为 failed；未知 receipt 必须保留。
func (workflow *Workflow) cleanupStarted(ctx context.Context, runID heteroci.TopologyRunID, state *executionState, ids operationIDs) []error {
	var cleanupErrors []error
	for _, unitID := range sortedStartedIDs(state.started) {
		if _, uncertain := state.uncertain[unitID]; uncertain {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("%w: started batch outcome for unit is unresolved", ErrBatchReceiptRequired))
			continue
		}
		if err := ctx.Err(); err != nil {
			cleanupErrors = append(cleanupErrors, err)
			break
		}
		now, err := workflow.now()
		if err != nil {
			cleanupErrors = append(cleanupErrors, err)
			continue
		}
		startedLease := state.started[unitID].Lease
		transition, err := workflow.deps.Owner.Finish(ctx, state.fence, ids.finish[unitID], runID, unitID, heteroci.LeaseStateFailed)
		if err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("finish started unit cleanup: %w", err))
			continue
		}
		if err := validateFinishedLease(state.fence, string(runID), unitID, startedLease, state.decision, transition, now); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("validate finished unit cleanup: %w", err))
			continue
		}
		state.fence = transition.Fence
		state.terminal[unitID] = transition.Lease
		delete(state.started, unitID)
	}
	return cleanupErrors
}

// cleanupPlanned 取消尚未 Start 的 leases，并跳过已 terminal 或仍 started 的 unit。
func (workflow *Workflow) cleanupPlanned(ctx context.Context, runID heteroci.TopologyRunID, planned []heteroci.RoutingLease, state *executionState, ids operationIDs) []error {
	var cleanupErrors []error
	for _, lease := range sortedPlannedLeases(planned) {
		if _, done := state.terminal[lease.UnitID]; done {
			continue
		}
		if _, started := state.started[lease.UnitID]; started {
			continue
		}
		if _, uncertain := state.uncertain[lease.UnitID]; uncertain {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("%w: planned unit start outcome for %s is unresolved", ErrBatchReceiptRequired, lease.UnitID))
			continue
		}
		if err := ctx.Err(); err != nil {
			cleanupErrors = append(cleanupErrors, err)
			break
		}
		now, err := workflow.now()
		if err != nil {
			cleanupErrors = append(cleanupErrors, err)
			continue
		}
		transition, err := workflow.deps.Owner.CancelPlanned(ctx, state.fence, ids.cancel[lease.UnitID], runID, lease.UnitID)
		if err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("cancel planned unit cleanup: %w", err))
			continue
		}
		if err := validateCancelledLease(state.fence, string(runID), lease.UnitID, lease, state.decision, transition, now); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("validate cancelled unit cleanup: %w", err))
			continue
		}
		state.fence = transition.Fence
		state.terminal[lease.UnitID] = transition.Lease
	}
	return cleanupErrors
}

// validateCancelledLease 校验 cleanup CancelPlanned 返回的 fence、unit、状态和不可变 lease identity。
func validateCancelledLease(previous heteroci.OwnerFence, runID, unitID string, planned heteroci.RoutingLease, decision heteroci.RoutingDecision, transition heterociowner.DurableLeaseTransition, now time.Time) error {
	if err := transition.Lease.ValidateAgainst(decision, now); err != nil {
		return fmt.Errorf("%w: cancelled lease projection is invalid", ErrExecutionState)
	}
	if !validNextFence(previous, transition.Fence) || transition.Lease.RunID != runID || transition.Lease.UnitID != unitID {
		return fmt.Errorf("%w: cancelled lease identity drifted", ErrExecutionState)
	}
	if transition.Lease.State != heteroci.LeaseStateCancelled || !sameLeaseIdentity(planned, transition.Lease) {
		return fmt.Errorf("%w: cancelled lease binding drifted", ErrExecutionState)
	}
	return nil
}

// sortedStartedIDs 返回 started unit 的稳定字典序。
func sortedStartedIDs(started map[string]heteroci.StartedRoutingLease) []string {
	ids := make([]string, 0, len(started))
	for unitID := range started {
		ids = append(ids, unitID)
	}
	sort.Strings(ids)
	return ids
}

// sortedPlannedLeases 返回 planned lease 的稳定 unit 顺序。
func sortedPlannedLeases(leases []heteroci.RoutingLease) []heteroci.RoutingLease {
	copyLeases := append([]heteroci.RoutingLease(nil), leases...)
	sort.Slice(copyLeases, func(left, right int) bool { return copyLeases[left].UnitID < copyLeases[right].UnitID })
	return copyLeases
}

// buildRunResult 生成不含任何 remote authority 写入的本地终态结果。
func buildRunResult(manifest heteroci.Manifest, planned heterociowner.DurableReservedRoutingPlan, state *executionState) RunResult {
	leases := make([]heteroci.RoutingLease, 0, len(planned.Leases))
	for _, lease := range planned.Leases {
		if terminal, ok := state.terminal[lease.UnitID]; ok {
			leases = append(leases, terminal)
		} else {
			leases = append(leases, lease)
		}
	}
	sort.Slice(leases, func(left, right int) bool { return leases[left].UnitID < leases[right].UnitID })
	results := make([]UnitResult, 0, len(state.results))
	for _, result := range state.results {
		results = append(results, result)
	}
	sort.Slice(results, func(left, right int) bool { return results[left].UnitID < results[right].UnitID })
	return RunResult{Authority: heteroci.AuthorityLocalNonAuthoritative, Status: ResultStatusNotVerified, Fence: state.fence, Manifest: manifest, Plan: planned.Plan, Decision: planned.Decision, Leases: leases, Units: results}
}

// joinWorkflowFailure 保留主错误并明确 cleanup 是否也失败。
func joinWorkflowFailure(primary, cleanupErr error) error {
	if cleanupErr == nil {
		return primary
	}
	return errors.Join(primary, ErrCleanupIncomplete, cleanupErr)
}
