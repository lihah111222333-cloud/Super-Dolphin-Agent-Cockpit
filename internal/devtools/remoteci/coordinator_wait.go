package remoteci

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	goruntime "runtime"
	"slices"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
	"golang.org/x/sync/errgroup"
)

// 阿里云 DescribeContainerGroups 单次最多接受 20 个实例 ID。
const remoteContainerGroupBatchLimit = 20

type pendingRemoteShard struct {
	index   int
	groupID string
}

// waitShards 批量轮询云状态，并只为已经终态的分片并行读取报告。
func (coordinator *Coordinator) waitShards(
	ctx context.Context,
	shards []gate.ContainerShard,
	groupIDs []string,
) ([]ShardResult, error) {
	results, pending := initializeRemoteShardResults(shards, groupIDs)
	failures := make([]error, len(shards))
	timer := time.NewTicker(coordinator.config.PollInterval)
	defer timer.Stop()
	for len(pending) != 0 {
		groups, err := coordinator.observePendingShardStatuses(ctx, pending, results)
		if err != nil {
			return results, err
		}
		pending, err = coordinator.collectObservedRemoteShards(ctx, shards, pending, groups, results, failures)
		if err != nil {
			return results, err
		}
		if len(pending) == 0 {
			break
		}
		select {
		case <-ctx.Done():
			return results, remoteCloudShardPendingError(results[pending[0].index].ContainerStatus, ctx.Err())
		case <-timer.C:
		}
	}
	return results, errors.Join(failures...)
}

// initializeRemoteShardResults 绑定分片、容器组和本轮仍待观察的索引。
func initializeRemoteShardResults(
	shards []gate.ContainerShard,
	groupIDs []string,
) ([]ShardResult, []pendingRemoteShard) {
	results := make([]ShardResult, len(shards))
	for index, shard := range shards {
		results[index] = ShardResult{
			ShardIdentity:     shard.IdentityDigest,
			ExecutedWorkloads: slices.Clone(shard.GateIDs),
			MaterializationTiming: gate.ShardMaterializationTiming{
				Measurement: gate.MaterializationMeasurementNotMeasured,
			},
		}
	}
	pending := make([]pendingRemoteShard, 0, len(groupIDs))
	for index, groupID := range groupIDs {
		if groupID == "" || index >= len(shards) {
			continue
		}
		results[index] = ShardResult{
			ShardIdentity: shards[index].IdentityDigest, ContainerGroup: groupID,
			ExecutedWorkloads: slices.Clone(shards[index].GateIDs),
			MaterializationTiming: gate.ShardMaterializationTiming{
				Measurement: gate.MaterializationMeasurementUnavailable,
			},
		}
		pending = append(pending, pendingRemoteShard{index: index, groupID: groupID})
	}
	return results, pending
}

// observePendingShardStatuses 按云 API 契约分批读取全部未终态分片。
func (coordinator *Coordinator) observePendingShardStatuses(
	parent context.Context,
	pending []pendingRemoteShard,
	results []ShardResult,
) (map[string]eci.ContainerGroup, error) {
	observed := make(map[string]eci.ContainerGroup, len(pending))
	for start := 0; start < len(pending); start += remoteContainerGroupBatchLimit {
		end := min(start+remoteContainerGroupBatchLimit, len(pending))
		ids := pendingRemoteShardIDs(pending[start:end])
		observation, cancel := gateprivate.WithTimeout(parent, coordinator.observationTimeout)
		groups, err := coordinator.shardStatuses(observation, ids)
		cancel()
		if err != nil {
			lastStatus := results[pending[start].index].ContainerStatus
			return nil, classifyShardObservationError(
				parent, observation, "batch status observation", lastStatus, coordinator.observationTimeout, err,
			)
		}
		for _, group := range groups {
			observed[group.ID] = group
		}
	}
	return observed, nil
}

func pendingRemoteShardIDs(pending []pendingRemoteShard) []string {
	ids := make([]string, len(pending))
	for index, shard := range pending {
		ids[index] = shard.groupID
	}
	return ids
}

// collectObservedRemoteShards 更新状态，并并行汇总本轮刚进入终态的报告。
func (coordinator *Coordinator) collectObservedRemoteShards(
	ctx context.Context,
	shards []gate.ContainerShard,
	pending []pendingRemoteShard,
	groups map[string]eci.ContainerGroup,
	results []ShardResult,
	failures []error,
) ([]pendingRemoteShard, error) {
	next := make([]pendingRemoteShard, 0, len(pending))
	terminal := make([]pendingRemoteShard, 0, len(pending))
	for _, item := range pending {
		group, ok := groups[item.groupID]
		if !ok {
			return nil, fmt.Errorf("remote CI shard container group %q is missing from batch observation", item.groupID)
		}
		results[item.index].ContainerStatus = group.Status
		if terminalECIStatus(group.Status) {
			terminal = append(terminal, item)
		} else {
			next = append(next, item)
		}
	}
	coordinator.collectTerminalShardReports(ctx, shards, groups, terminal, results, failures)
	return next, nil
}

// collectTerminalShardReports 以本机可用 CPU 自适应限制 CLI 子进程，避免汇总阶段挤爆内存。
func (coordinator *Coordinator) collectTerminalShardReports(
	ctx context.Context,
	shards []gate.ContainerShard,
	groups map[string]eci.ContainerGroup,
	terminal []pendingRemoteShard,
	results []ShardResult,
	failures []error,
) {
	var workers errgroup.Group
	workers.SetLimit(remoteCoordinatorParallelism(len(terminal)))
	for _, item := range terminal {
		workers.Go(func() error {
			report, workerLog, err := coordinator.observeShardReport(
				ctx, shards[item.index], item.groupID, groups[item.groupID],
			)
			if err != nil {
				failure := remoteShardExecutionError(shards[item.index], err)
				failures[item.index] = failure
				return failure
			}
			results[item.index].Report = report
			materializerLog, err := coordinator.runtime.DescribeContainerLog(ctx, item.groupID, "materializer")
			if err != nil {
				failure := remoteShardExecutionError(shards[item.index], fmt.Errorf("describe remote CI materializer log: %w", err))
				failures[item.index] = failure
				return failure
			}
			timing, err := decodeShardMaterializationTimingLog(materializerLog, shards[item.index].IdentityDigest)
			if err != nil {
				failure := remoteShardExecutionError(shards[item.index], fmt.Errorf("decode remote CI materializer timing: %w", err))
				failures[item.index] = failure
				return failure
			}
			results[item.index].MaterializationTiming = timing
			results[item.index].workerDiagnostic = remoteShardLogTail(workerLog)
			return nil
		})
	}
	_ = workers.Wait()
}

// remoteCoordinatorParallelism 让本机协调器按可用 CPU 扩展有界外部进程数。
func remoteCoordinatorParallelism(workItems int) int {
	return max(1, min(workItems, goruntime.GOMAXPROCS(0)))
}

// waitShard 保留单分片观察入口，供定点诊断和契约测试使用。
func (coordinator *Coordinator) waitShard(ctx context.Context, shard gate.ContainerShard, groupID string) (ShardResult, error) {
	result := ShardResult{
		ShardIdentity: shard.IdentityDigest, ContainerGroup: groupID,
		ExecutedWorkloads: slices.Clone(shard.GateIDs),
	}
	timer := time.NewTicker(coordinator.config.PollInterval)
	defer timer.Stop()
	for {
		group, err := coordinator.observeShardStatus(ctx, groupID, result.ContainerStatus)
		if err != nil {
			return result, remoteShardExecutionError(shard, err)
		}
		result.ContainerStatus = group.Status
		if terminalECIStatus(result.ContainerStatus) {
			report, workerLog, err := coordinator.observeShardReport(ctx, shard, groupID, group)
			if err != nil {
				return result, remoteShardExecutionError(shard, err)
			}
			result.Report = report
			materializerLog, err := coordinator.runtime.DescribeContainerLog(ctx, groupID, "materializer")
			if err != nil {
				return result, remoteShardExecutionError(shard, fmt.Errorf("describe remote CI materializer log: %w", err))
			}
			timing, err := decodeShardMaterializationTimingLog(materializerLog, shard.IdentityDigest)
			if err != nil {
				return result, remoteShardExecutionError(shard, fmt.Errorf("decode remote CI materializer timing: %w", err))
			}
			result.MaterializationTiming = timing
			result.workerDiagnostic = remoteShardLogTail(workerLog)
			return result, nil
		}
		select {
		case <-ctx.Done():
			return result, remoteCloudShardPendingError(result.ContainerStatus, ctx.Err())
		case <-timer.C:
		}
	}
}

// shardStatuses 批量读取并严格核对每一个 ECI container group 身份。
func (coordinator *Coordinator) shardStatuses(ctx context.Context, groupIDs []string) ([]eci.ContainerGroup, error) {
	if len(groupIDs) == 0 || len(groupIDs) > remoteContainerGroupBatchLimit {
		return nil, errors.New("remote CI shard status batch size is invalid")
	}
	groups, err := coordinator.runtime.DescribeContainerGroups(ctx, groupIDs...)
	if err != nil {
		return nil, err
	}
	if len(groups) != len(groupIDs) {
		return nil, errors.New("remote CI shard container group batch is incomplete")
	}
	expected := make(map[string]struct{}, len(groupIDs))
	for _, groupID := range groupIDs {
		if groupID == "" {
			return nil, errors.New("remote CI shard container group identity is missing")
		}
		expected[groupID] = struct{}{}
	}
	for _, group := range groups {
		if _, ok := expected[group.ID]; !ok {
			return nil, errors.New("remote CI shard container group identity is unexpected")
		}
		delete(expected, group.ID)
	}
	if len(expected) != 0 {
		return nil, errors.New("remote CI shard container group identity is missing")
	}
	return groups, nil
}

// shardStatus 读取并校验指定 ECI container group 的唯一身份与状态。
func (coordinator *Coordinator) shardStatus(ctx context.Context, groupID string) (eci.ContainerGroup, error) {
	groups, err := coordinator.shardStatuses(ctx, []string{groupID})
	if err != nil {
		return eci.ContainerGroup{}, err
	}
	return groups[0], nil
}

// shardReport 下载、解码并校验终态 worker 报告与原分片的一致性。
func (coordinator *Coordinator) shardReport(ctx context.Context, shard gate.ContainerShard, groupID string, group eci.ContainerGroup) (gate.PlanExecutionReport, string, error) {
	log, err := coordinator.runtime.DescribeContainerLog(ctx, groupID, "worker")
	if err != nil {
		return gate.PlanExecutionReport{}, "", err
	}
	report, err := decodeReportLog(log, shard.GateIDs)
	if err != nil {
		return gate.PlanExecutionReport{}, log, coordinator.shardReportError(ctx, groupID, group, log, err)
	}
	if report.Profile != shard.Profile || report.PlanDigest != shard.PlanDigest || !slices.Equal(reportGateIDs(report), shard.GateIDs) {
		return gate.PlanExecutionReport{}, log, errors.New("remote CI shard report identity does not match assignment")
	}
	return report, log, nil
}

// shardReportError 在清理失败分片前收集有界 worker 与 materializer 诊断。
func (coordinator *Coordinator) shardReportError(ctx context.Context, groupID string, group eci.ContainerGroup, workerLog string, reportErr error) error {
	base := fmt.Errorf("decode remote CI shard report (status=%s): %w; worker log=%q", group.Status, reportErr, remoteShardLogTail(workerLog))
	if diagnostic := remoteECIGroupDiagnostic(group); diagnostic != "" {
		base = fmt.Errorf("%w; ECI terminal=%q", base, diagnostic)
	}
	if group.Status != "Failed" {
		return base
	}
	materializerLog, err := coordinator.runtime.DescribeContainerLog(ctx, groupID, "materializer")
	if err != nil {
		return errors.Join(base, fmt.Errorf("describe remote CI materializer log: %w", err))
	}
	return fmt.Errorf("%w; materializer log=%q", base, remoteShardLogTail(materializerLog))
}

// decodeReportLog 从普通文本日志中提取有界分块报告。
func decodeReportLog(log string, expected []gate.GateID) (gate.PlanExecutionReport, error) {
	recordLimit, err := gate.PlanExecutionReportRecordLimit(len(expected))
	if err != nil {
		return gate.PlanExecutionReport{}, err
	}
	var chunks []string
	scanner := bufio.NewScanner(strings.NewReader(log))
	scanner.Buffer(make([]byte, 64*1024), 8<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, gate.ExecutorPlanReportChunkPrefix) {
			if len(chunks) >= recordLimit {
				return gate.PlanExecutionReport{}, errors.New("remote plan report exceeds shard record budget")
			}
			chunks = append(chunks, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return gate.PlanExecutionReport{}, err
	}
	return gate.DecodePlanExecutionReportChunksForGateSet(chunks, expected)
}

func reportGateIDs(report gate.PlanExecutionReport) []gate.GateID {
	ids := make([]gate.GateID, len(report.Gates))
	for index, execution := range report.Gates {
		ids[index] = execution.GateID
	}
	return ids
}

func terminalECIStatus(status string) bool {
	switch status {
	case "Succeeded", "Failed", "ScheduleFailed", "Expired":
		return true
	default:
		return false
	}
}
