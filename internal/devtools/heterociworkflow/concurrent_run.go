package heterociworkflow

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

// executeRunBatchesConcurrent 按 lane 并行执行，且让同一 local node 的 wave 保持顺序。
func (workflow *Workflow) executeRunBatchesConcurrent(ctx context.Context, request RunRequest, prepared *preparedRun) error {
	if prepared == nil || prepared.state == nil {
		return fmt.Errorf("%w: prepared execution state is missing", ErrExecutionState)
	}
	lanes := buildConcurrentBatchLanes(prepared.batches)
	if len(lanes) == 0 {
		return nil
	}
	executionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	coordinator := &concurrentExecutionCoordinator{state: prepared.state}
	results := make(chan concurrentLaneResult, len(lanes))
	var waitGroup sync.WaitGroup
	for _, lane := range lanes {
		waitGroup.Go(func() {
			err := workflow.executeConcurrentBatchLane(executionCtx, request, prepared, lane, coordinator)
			if err != nil {
				cancel()
			}
			results <- concurrentLaneResult{firstIndex: lane.firstIndex, err: err}
		})
	}
	waitGroup.Wait()
	close(results)
	ordered := make([]concurrentLaneResult, 0, len(lanes))
	for result := range results {
		if result.err != nil {
			ordered = append(ordered, result)
		}
	}
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].firstIndex < ordered[right].firstIndex })
	if len(ordered) == 0 {
		return nil
	}
	errorsToJoin := make([]error, 0, len(ordered))
	for _, result := range ordered {
		errorsToJoin = append(errorsToJoin, fmt.Errorf("execute batch lane at index %d: %w", result.firstIndex, result.err))
	}
	return errors.Join(errorsToJoin...)
}

type concurrentLaneResult struct {
	firstIndex int
	err        error
}

type concurrentBatchLane struct {
	firstIndex int
	batches    []BatchPlan
}

type concurrentBatchLaneKey struct {
	kind    heteroci.ExecutorKind
	nodeID  string
	batchID BatchID
}

// buildConcurrentBatchLanes 按节点聚合 local wave，并让每个 ECI batch 保持独立。
func buildConcurrentBatchLanes(batches []BatchPlan) []concurrentBatchLane {
	lanes := make([]concurrentBatchLane, 0, len(batches))
	indexes := make(map[concurrentBatchLaneKey]int, len(batches))
	for index, batch := range batches {
		key := concurrentBatchLaneKey{kind: batch.Batch.Key.Kind, nodeID: batch.Batch.Key.NodeID}
		if batch.Batch.Key.Kind != heteroci.ExecutorKindLocalNode {
			key.batchID = batch.Batch.BatchID
		}
		laneIndex, found := indexes[key]
		if !found {
			laneIndex = len(lanes)
			indexes[key] = laneIndex
			lanes = append(lanes, concurrentBatchLane{firstIndex: index})
		}
		lanes[laneIndex].batches = append(lanes[laneIndex].batches, batch)
	}
	for index := range lanes {
		if len(lanes[index].batches) < 2 || lanes[index].batches[0].Batch.Key.Kind != heteroci.ExecutorKindLocalNode {
			continue
		}
		sort.SliceStable(lanes[index].batches, func(left, right int) bool {
			return lanes[index].batches[left].Batch.Wave < lanes[index].batches[right].Batch.Wave
		})
	}
	return lanes
}

// executeConcurrentBatchLane 顺序推进单 lane，且不会重试任何 batch。
func (workflow *Workflow) executeConcurrentBatchLane(ctx context.Context, request RunRequest, prepared *preparedRun, lane concurrentBatchLane, coordinator *concurrentExecutionCoordinator) error {
	for _, plan := range lane.batches {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%w: batch lane canceled: %w", ErrExecutionState, err)
		}
		if err := workflow.executeConcurrentBatch(ctx, request, prepared, plan, coordinator); err != nil {
			return err
		}
	}
	return nil
}

// executeConcurrentBatch 串行保护 owner mutation，并在锁外执行 ExecuteBatch。
func (workflow *Workflow) executeConcurrentBatch(ctx context.Context, request RunRequest, prepared *preparedRun, plan BatchPlan, coordinator *concurrentExecutionCoordinator) error {
	started, err := coordinator.prepareAndStart(ctx, workflow, request, prepared, plan)
	if err != nil {
		return err
	}
	plan.Batch.Started = started
	plan.Batch.MaterialDigest, err = DeriveExecutionMaterialDigest(plan.Batch)
	if err != nil {
		markBatchUncertain(prepared.state, plan.Batch)
		return err
	}
	outcomes, err := workflow.executeBoundBatch(ctx, plan.Batch, prepared.executors, prepared.state)
	if err != nil {
		return err
	}
	return coordinator.finish(ctx, workflow, request, prepared, plan.Batch, outcomes)
}

type concurrentExecutionCoordinator struct {
	state *executionState
}

// prepareAndStart 在单锁内捕获当前 fence 并完成全部 Start mutation。
func (coordinator *concurrentExecutionCoordinator) prepareAndStart(ctx context.Context, workflow *Workflow, request RunRequest, prepared *preparedRun, plan BatchPlan) ([]heteroci.StartedRoutingLease, error) {
	coordinator.state.mu.Lock()
	defer coordinator.state.mu.Unlock()
	snapshot, now, err := workflow.prepareBatchSnapshot(ctx, prepared.manifest, request.HeartbeatTTL, coordinator.state.fence)
	if err != nil {
		return nil, err
	}
	started, err := workflow.startBatchUnitsLocked(ctx, request.RunID, prepared.planned.Decision, prepared.ids, plan, snapshot, now, coordinator.state)
	if err != nil {
		return nil, err
	}
	return started, nil
}

// finish 串行完成 Finish mutation，并携带最新 owner fence。
func (coordinator *concurrentExecutionCoordinator) finish(ctx context.Context, workflow *Workflow, request RunRequest, prepared *preparedRun, batch ExecutionBatch, outcomes map[string]heteroci.RoutingLeaseState) error {
	coordinator.state.mu.Lock()
	defer coordinator.state.mu.Unlock()
	return workflow.finishBatchUnitsLocked(ctx, request.RunID, prepared.planned.Decision, prepared.ids, batch, outcomes, coordinator.state)
}
