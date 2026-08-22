package remotecicli

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociworkflow"
)

func preparedExecuteStageTimes(snapshot heteroci.SnapshotSet, clock func() time.Time) ([]time.Time, error) {
	captured := time.UnixMilli(snapshot.CapturedAtUnixMilli).UTC()
	reserved := clock()
	if captured.UnixMilli() <= 0 || reserved.Before(captured) {
		return nil, ErrExecuteComposition
	}
	times := make([]time.Time, heterociexec.PostgreSQLReceiptLifecycleStageCount)
	for index := range times {
		times[index] = reserved
	}
	times[0], times[1], times[2] = captured, captured, captured
	return times, nil
}

func blockedExecuteStageTimes(snapshot heteroci.SnapshotSet, clock func() time.Time) ([]time.Time, error) {
	return preparedExecuteStageTimes(snapshot, clock)
}

func validateExecuteStageTimes(times []time.Time) error {
	if len(times) != heterociexec.PostgreSQLReceiptLifecycleStageCount {
		return ErrExecuteComposition
	}
	for index, value := range times {
		if value.UnixMilli() <= 0 || index > 0 && value.Before(times[index-1]) {
			return ErrExecuteComposition
		}
	}
	return nil
}

func preparedExecuteTerminalShards(snapshot heterociworkflow.PreparedRunSnapshot, at time.Time) ([]heterociexec.PostgreSQLTerminalShard, error) {
	shards := make([]heterociexec.PostgreSQLTerminalShard, len(snapshot.BindingPlan.Bindings))
	for index, binding := range snapshot.BindingPlan.Bindings {
		shards[index] = executeTerminalShardFromBinding(string(snapshot.ExternalOperationID), binding, at, at)
	}
	if _, err := heterociexec.PostgreSQLTerminalNodeSetDigest(shards); err != nil {
		return nil, err
	}
	return shards, nil
}

func executeTerminalShardFromBinding(operationID string, binding heterociworkflow.ExecutorBinding, startedAt, finishedAt time.Time) heterociexec.PostgreSQLTerminalShard {
	return heterociexec.PostgreSQLTerminalShard{
		ChildOperationID: executeChildOperationID(operationID, binding.NodeID),
		NodeID:           binding.NodeID, NodeIDDigest: digestBytes([]byte(binding.NodeID)), ExecutorKind: string(binding.ExecutorKind),
		ShardDigest: binding.ShardDigest, BatchSetDigest: binding.BatchSetDigest, UnitSetDigest: binding.UnitSetDigest,
		StartedAt: startedAt, FinishedAt: finishedAt, ExitCode: 0,
		CleanupState: heterociexec.PostgreSQLReceiptCleanupVerified, Status: heterociexec.PostgreSQLReceiptStatusSucceeded,
	}
}

func projectPreparedExecuteLifecycle(snapshot heterociworkflow.PreparedRunSnapshot, shards []heterociexec.PostgreSQLTerminalShard, stageTimes []time.Time) (heterociexec.PostgreSQLLifecycleProjection, error) {
	nodeSetDigest, err := heterociexec.PostgreSQLTerminalNodeSetDigest(shards)
	if err != nil {
		return heterociexec.PostgreSQLLifecycleProjection{}, err
	}
	return heterociexec.ProjectPostgreSQLLifecycle(heterociexec.PostgreSQLLifecycleProjectionInput{
		OperationID: string(snapshot.ExternalOperationID), RequestDigest: snapshot.RequestDigest,
		ManifestDigest: snapshot.ManifestDigest, PlanDigest: snapshot.BindingPlan.PlanDigest,
		ExecutorBindingDigest: snapshot.BindingDigest, NodeSetDigest: nodeSetDigest,
		AuthorityGeneration: int64(snapshot.Fence.OwnerEpoch), StageTimes: stageTimes, TerminalShards: shards,
		Status: heterociexec.PostgreSQLReceiptStatusSucceeded, CleanupState: heterociexec.PostgreSQLReceiptCleanupVerified, ExitCode: 0,
	})
}

type executeNodeTiming struct {
	started  time.Time
	finished time.Time
	count    int
}

func succeededExecuteTerminalShards(snapshot heterociworkflow.PreparedRunSnapshot, result heterociworkflow.RunResult) ([]heterociexec.PostgreSQLTerminalShard, time.Time, time.Time, error) {
	if err := validateSucceededExecuteResult(snapshot, result); err != nil {
		return nil, time.Time{}, time.Time{}, ErrExecuteComposition
	}
	timings, err := executeLeaseTimings(result.Leases)
	if err != nil {
		return nil, time.Time{}, time.Time{}, err
	}
	shards := make([]heterociexec.PostgreSQLTerminalShard, len(snapshot.BindingPlan.Bindings))
	var aggregateStart, aggregateFinish time.Time
	for index, binding := range snapshot.BindingPlan.Bindings {
		timing, ok := timings[binding.NodeID]
		if !ok || timing.count == 0 {
			return nil, time.Time{}, time.Time{}, ErrExecuteComposition
		}
		shards[index] = executeTerminalShardFromBinding(string(snapshot.ExternalOperationID), binding, timing.started, timing.finished)
		aggregateStart, aggregateFinish = minExecuteTime(aggregateStart, timing.started), maxExecuteTime(aggregateFinish, timing.finished)
	}
	if _, err := heterociexec.PostgreSQLTerminalNodeSetDigest(shards); err != nil {
		return nil, time.Time{}, time.Time{}, err
	}
	return shards, aggregateStart, aggregateFinish, nil
}

func validateSucceededExecuteResult(snapshot heterociworkflow.PreparedRunSnapshot, result heterociworkflow.RunResult) error {
	for _, validate := range []func() error{
		func() error { return validateSucceededExecuteAuthority(result) },
		func() error { return validateSucceededExecuteFence(snapshot.Fence, result.Fence) },
		func() error { return validateSucceededExecuteBinding(snapshot, result) },
		func() error { return validateSucceededExecuteCoverage(snapshot.Units, result) },
	} {
		if err := validate(); err != nil {
			return err
		}
	}
	return nil
}

func validateSucceededExecuteAuthority(result heterociworkflow.RunResult) error {
	if result.Authority != heteroci.AuthorityLocalNonAuthoritative || result.Status != heterociworkflow.ResultStatusNotVerified {
		return ErrExecuteComposition
	}
	return nil
}

func validateSucceededExecuteFence(prepared, terminal heteroci.OwnerFence) error {
	if terminal.Scope != prepared.Scope || terminal.OwnerID != prepared.OwnerID || terminal.OwnerEpoch != prepared.OwnerEpoch || terminal.Revision < prepared.Revision {
		return ErrExecuteComposition
	}
	return nil
}

func validateSucceededExecuteBinding(snapshot heterociworkflow.PreparedRunSnapshot, result heterociworkflow.RunResult) error {
	if result.ExecutorBindingDigest != snapshot.BindingDigest || result.BindingPlan.BindingDigest != snapshot.BindingDigest || result.BindingPlan.PlanDigest != snapshot.BindingPlan.PlanDigest {
		return ErrExecuteComposition
	}
	if err := heterociworkflow.ValidateDurableExecutorBindingPlan(result.BindingPlan); err != nil {
		return ErrExecuteComposition
	}
	return nil
}

func validateSucceededExecuteCoverage(units []heteroci.ExecutionUnit, result heterociworkflow.RunResult) error {
	if len(result.Leases) != len(units) || len(result.Units) != len(units) {
		return ErrExecuteComposition
	}
	for _, unit := range result.Units {
		if unit.State != heteroci.LeaseStateSucceeded {
			return ErrExecuteComposition
		}
	}
	return nil
}

func executeLeaseTimings(leases []heteroci.RoutingLease) (map[string]executeNodeTiming, error) {
	timings := make(map[string]executeNodeTiming)
	for _, lease := range leases {
		if lease.State != heteroci.LeaseStateSucceeded || lease.StartedAtUnixMilli <= 0 || lease.FinishedAtUnixMilli < lease.StartedAtUnixMilli {
			return nil, ErrExecuteComposition
		}
		started, finished := time.UnixMilli(lease.StartedAtUnixMilli).UTC(), time.UnixMilli(lease.FinishedAtUnixMilli).UTC()
		timing := timings[lease.NodeID]
		timing.started, timing.finished, timing.count = minExecuteTime(timing.started, started), maxExecuteTime(timing.finished, finished), timing.count+1
		timings[lease.NodeID] = timing
	}
	return timings, nil
}

func minExecuteTime(left, right time.Time) time.Time {
	if left.IsZero() || right.Before(left) {
		return right
	}
	return left
}

func maxExecuteTime(left, right time.Time) time.Time {
	if left.IsZero() || right.After(left) {
		return right
	}
	return left
}

func compareExecuteFirstEvents(left, right []heterociexec.PostgreSQLReceiptEvent) error {
	if len(left) != len(right) {
		return ErrExecuteComposition
	}
	for index := range left {
		leftBytes, leftDigest, leftErr := heterociexec.EncodePostgreSQLReceiptEvent(left[index])
		rightBytes, rightDigest, rightErr := heterociexec.EncodePostgreSQLReceiptEvent(right[index])
		if leftErr != nil || rightErr != nil || leftDigest != rightDigest || !bytes.Equal(leftBytes, rightBytes) {
			return ErrExecuteComposition
		}
	}
	return nil
}

func blockedExecuteBindingDigest(requestDigest, planDigest string) string {
	return digestBytes([]byte("remote-ci-blocked-executor-binding/v1\x00" + requestDigest + "\x00" + planDigest))
}

func executeChildOperationID(operationID, nodeID string) string {
	return strings.TrimPrefix(digestBytes([]byte("remote-ci-child-operation/v1\x00"+operationID+"\x00"+nodeID)), "sha256:")
}

func writeExecuteEvent(writer io.Writer, event heterociexec.PostgreSQLReceiptEvent, limit int64) error {
	encoded, _, err := heterociexec.EncodePostgreSQLReceiptEvent(event)
	if err != nil {
		return err
	}
	return writeBoundedExecuteJSON(writer, encoded, limit)
}

func writeExecuteTerminal(writer io.Writer, encoded []byte, limit int64) error {
	return writeBoundedExecuteJSON(writer, encoded, limit)
}

func writeBoundedExecuteJSON(writer io.Writer, encoded []byte, limit int64) error {
	if writer == nil || len(encoded) == 0 || limit <= 0 || int64(len(encoded)+1) > limit {
		return ErrExecuteComposition
	}
	payload := append(append([]byte(nil), encoded...), '\n')
	written, err := writer.Write(payload)
	if err != nil || written != len(payload) {
		return errors.Join(ErrExecuteComposition, err)
	}
	return nil
}
