package gate

import (
	"context"
	"maps"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

func concurrentCompileGroupExecution(groups []CompileGroup) bool {
	if len(groups) < 2 {
		return false
	}
	for _, group := range groups {
		if !group.ConcurrentExecution {
			return false
		}
	}
	return true
}

// executeConcurrentCompileGroupBatches 按资源证明并发执行同一 shard 的 race groups。
func executeConcurrentCompileGroupBatches(ctx context.Context, groups []CompileGroup, artifacts map[GateID]compiledGroupArtifact, now func() time.Time) (map[GateID]PlanGateExecution, map[string]error) {
	groupResults := make([]map[GateID]PlanGateExecution, len(groups))
	groupErrors := make([]error, len(groups))
	var wait sync.WaitGroup
	for index, group := range groups {
		if len(group.BatchPlan) == 0 || len(group.WorkloadIDs) == 0 {
			continue
		}
		artifact, ok := artifacts[group.WorkloadIDs[0]]
		if !ok {
			continue
		}
		wait.Add(1)
		safego.Go(ctx, nil, "gate.compile-group.parallel."+group.GroupID, func(groupCtx context.Context) {
			defer wait.Done()
			groupResults[index], groupErrors[index] = executeCompileGroupBatchWaves(groupCtx, artifact, group, now)
		})
	}
	wait.Wait()
	results := make(map[GateID]PlanGateExecution)
	errorsByGroup := make(map[string]error)
	for index, group := range groups {
		maps.Copy(results, groupResults[index])
		if groupErrors[index] != nil {
			errorsByGroup[group.GroupID] = groupErrors[index]
		}
	}
	return results, errorsByGroup
}
