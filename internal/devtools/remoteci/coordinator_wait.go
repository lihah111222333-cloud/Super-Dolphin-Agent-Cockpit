package remoteci

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
	"golang.org/x/sync/errgroup"
)

type pendingRemoteShard struct {
	index   int
	groupID string
}

type remoteTimingWarningRun struct {
	jobID              string
	agentTokenDigest   string
	acceptedGeneration uint64
	store              remoteTimingWarningRecorder
}

type remoteTimingWarningRecorder interface {
	RecordLiveRemoteCITimingWarning(gate.RemoteCITimingWarning) (gate.RemoteCITimingWarning, bool, error)
}

// waitShards 批量轮询云状态，并只为已经终态的分片并行读取报告。
func (coordinator *Coordinator) waitShards(
	ctx context.Context,
	shards []gate.ContainerShard,
	groupIDs []string,
	warningRun remoteTimingWarningRun,
) ([]ShardResult, []gate.RemoteCITimingWarning, error) {
	results, pending := initializeRemoteShardResults(shards, groupIDs)
	failures := make([]error, len(shards))
	warningFailures := make([]error, len(shards))
	warned := make(map[int]struct{}, len(shards))
	warningRetries := make(map[int]pendingRemoteShard)
	warnings := make([]gate.RemoteCITimingWarning, 0)
	timer := time.NewTicker(coordinator.config.PollInterval)
	defer timer.Stop()
	for len(pending) != 0 || len(warningRetries) != 0 {
		observedItems := mergePendingRemoteShards(pending, warningRetries)
		groups, err := coordinator.observePendingShardStatuses(ctx, observedItems, results)
		if err != nil {
			return results, warnings, err
		}
		warnings = append(warnings, coordinator.observeShardTargetWarnings(
			shards, observedItems, groups, warningRun, warned, warningFailures,
		)...)
		pending, err = coordinator.collectObservedRemoteShards(ctx, shards, pending, groups, results, failures, warningRun.agentTokenDigest)
		if err != nil {
			return results, warnings, err
		}
		updateTerminalTimingWarningRetries(observedItems, groups, warningFailures, warningRetries)
		if len(pending) == 0 && len(warningRetries) == 0 {
			break
		}
		select {
		case <-ctx.Done():
			if len(pending) == 0 {
				return results, warnings, errors.Join(errors.Join(warningFailures...), ctx.Err())
			}
			return results, warnings, remoteCloudShardPendingError(results[observedItems[0].index].ContainerStatus, ctx.Err())
		case <-timer.C:
		}
	}
	return results, warnings, errors.Join(errors.Join(failures...), errors.Join(warningFailures...))
}

func mergePendingRemoteShards(pending []pendingRemoteShard, retries map[int]pendingRemoteShard) []pendingRemoteShard {
	merged := append([]pendingRemoteShard(nil), pending...)
	seen := make(map[int]struct{}, len(merged))
	for _, item := range merged {
		seen[item.index] = struct{}{}
	}
	for _, item := range retries {
		if _, ok := seen[item.index]; ok {
			continue
		}
		merged = append(merged, item)
	}
	slices.SortFunc(merged, func(first, second pendingRemoteShard) int { return first.index - second.index })
	return merged
}

// updateTerminalTimingWarningRetries 重试 SQLite busy 的终态告警写入并清理已收敛分片。
func updateTerminalTimingWarningRetries(
	observed []pendingRemoteShard,
	groups map[string]eci.ContainerGroup,
	failures []error,
	retries map[int]pendingRemoteShard,
) {
	for _, item := range observed {
		group, ok := groups[item.groupID]
		if !ok || !terminalECIStatus(group.Status) {
			continue
		}
		if item.index >= 0 && item.index < len(failures) && errors.Is(failures[item.index], gate.ErrDurationLedgerBusy) {
			retries[item.index] = item
			continue
		}
		delete(retries, item.index)
	}
}

// observeShardTargetWarnings 使用 provider worker StartTime 写入 live SQLite 事实。
// 写入失败只会被记录和重试，不会提前退出轮询、取消、kill 或标记 shard 失败。
func (coordinator *Coordinator) observeShardTargetWarnings(
	shards []gate.ContainerShard,
	pending []pendingRemoteShard,
	groups map[string]eci.ContainerGroup,
	warningRun remoteTimingWarningRun,
	warned map[int]struct{},
	failures []error,
) []gate.RemoteCITimingWarning {
	now := coordinator.now().UTC().Truncate(time.Millisecond)
	warnings := make([]gate.RemoteCITimingWarning, 0)
	for _, item := range pending {
		if item.index < 0 || item.index >= len(shards) || item.index >= len(failures) {
			continue
		}
		group, ok := groups[item.groupID]
		if !ok {
			failures[item.index] = fmt.Errorf("remote CI shard %q is missing from timing warning observation", item.groupID)
			continue
		}
		_, alreadyWarned := warned[item.index]
		warning, emitted, clearFailure, err := coordinator.observeShardTargetWarning(
			shards[item.index], item, group, now, warningRun, alreadyWarned,
		)
		if err != nil {
			failures[item.index] = err
			continue
		}
		if clearFailure {
			failures[item.index] = nil
			continue
		}
		if !emitted {
			continue
		}
		warned[item.index] = struct{}{}
		failures[item.index] = nil
		warnings = append(warnings, warning)
	}
	return warnings
}

// observeShardTargetWarning 观察单个分片的目标告警，并坚持 warn_and_continue 语义。
func (coordinator *Coordinator) observeShardTargetWarning(
	shard gate.ContainerShard,
	item pendingRemoteShard,
	group eci.ContainerGroup,
	now time.Time,
	warningRun remoteTimingWarningRun,
	alreadyWarned bool,
) (gate.RemoteCITimingWarning, bool, bool, error) {
	observationTime, observeTarget, err := remoteCITimingWarningObservationTime(group, now)
	if err != nil {
		return gate.RemoteCITimingWarning{}, false, false, fmt.Errorf(
			"remote CI shard %q timing warning observation: %w", item.groupID, err,
		)
	}
	if !observeTarget {
		return gate.RemoteCITimingWarning{}, false, false, nil
	}
	if alreadyWarned {
		return gate.RemoteCITimingWarning{}, false, true, nil
	}
	providerStartedAt, err := observedECIWorkerStartTime(group)
	if err != nil {
		if group.Status != "Running" && group.Status != "Succeeded" {
			return gate.RemoteCITimingWarning{}, false, true, nil
		}
		return gate.RemoteCITimingWarning{}, false, false, fmt.Errorf(
			"remote CI shard %q timing warning evidence: %w", item.groupID, err,
		)
	}
	providerStartedAt = providerStartedAt.UTC().Truncate(time.Millisecond)
	if observationTime.Before(providerStartedAt) {
		return gate.RemoteCITimingWarning{}, false, false, fmt.Errorf(
			"remote CI shard %q provider worker StartTime is after the observation time", item.groupID,
		)
	}
	if observationTime.Sub(providerStartedAt) < cicontract.ShardTargetDuration {
		return gate.RemoteCITimingWarning{}, false, false, nil
	}
	warning := gate.RemoteCITimingWarning{
		JobID: warningRun.jobID, AgentTokenDigest: warningRun.agentTokenDigest,
		AcceptedGeneration: warningRun.acceptedGeneration, Scope: cicontract.TimingScopeShard,
		ShardIdentity: shard.IdentityDigest,
		EvidenceKind:  cicontract.TimingWarningEvidenceRunning, Action: cicontract.TimingWarningWarnAndContinue,
		EvidenceStartedAt: providerStartedAt, ObservedAt: observationTime,
		EvidenceDurationMS: observationTime.Sub(providerStartedAt).Milliseconds(),
		TargetMS:           cicontract.ShardTargetDuration.Milliseconds(),
	}
	warning.WarningText = gate.CanonicalRemoteCITimingWarningText(warning)
	stored, _, err := warningRun.store.RecordLiveRemoteCITimingWarning(warning)
	if err != nil {
		return gate.RemoteCITimingWarning{}, false, false, fmt.Errorf(
			"persist remote CI shard %q live timing warning: %w", item.groupID, err,
		)
	}
	return stored, true, false, nil
}

// remoteCITimingWarningObservationTime 对 Running 使用本次观测时间，对终态只接受 provider 终态时间。
func remoteCITimingWarningObservationTime(group eci.ContainerGroup, now time.Time) (time.Time, bool, error) {
	switch group.Status {
	case "Running":
		return now.UTC().Truncate(time.Millisecond), true, nil
	case "Succeeded":
		if group.SucceededTime.IsZero() {
			return time.Time{}, true, errors.New("ECI Succeeded response is missing provider SucceededTime")
		}
		return group.SucceededTime.UTC().Truncate(time.Millisecond), true, nil
	case "Failed":
		if group.FailedTime.IsZero() {
			return time.Time{}, true, errors.New("ECI Failed response is missing provider FailedTime")
		}
		return group.FailedTime.UTC().Truncate(time.Millisecond), true, nil
	default:
		return time.Time{}, false, nil
	}
}

// observedECIWorkerStartTime 返回唯一 worker 容器的 provider CurrentState.StartTime。
func observedECIWorkerStartTime(group eci.ContainerGroup) (time.Time, error) {
	var startedAt time.Time
	found := false
	for _, container := range group.Containers {
		if container.Name != "worker" {
			continue
		}
		if found {
			return time.Time{}, errors.New("ECI Running response has duplicate worker containers")
		}
		found = true
		startedAt = container.CurrentState.StartTime
	}
	if !found || startedAt.IsZero() {
		return time.Time{}, errors.New("ECI Running response is missing worker CurrentState.StartTime")
	}
	return startedAt, nil
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

// observePendingShardStatuses 一次读取全部未终态分片，不施加仓库批次上限。
func (coordinator *Coordinator) observePendingShardStatuses(
	parent context.Context,
	pending []pendingRemoteShard,
	results []ShardResult,
) (map[string]eci.ContainerGroup, error) {
	observed := make(map[string]eci.ContainerGroup, len(pending))
	if len(pending) == 0 {
		return observed, nil
	}
	ids := pendingRemoteShardIDs(pending)
	observation, cancel := gateprivate.WithTimeout(parent, coordinator.observationTimeout)
	groups, err := coordinator.shardStatuses(observation, ids)
	cancel()
	if err != nil {
		lastStatus := results[pending[0].index].ContainerStatus
		return nil, classifyShardObservationError(
			parent, observation, "status observation", lastStatus, coordinator.observationTimeout, err,
		)
	}
	for _, group := range groups {
		observed[group.ID] = group
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
	expectedAgentTokenDigest string,
) ([]pendingRemoteShard, error) {
	next := make([]pendingRemoteShard, 0, len(pending))
	terminal := make([]pendingRemoteShard, 0, len(pending))
	for _, item := range pending {
		group, ok := groups[item.groupID]
		if !ok {
			return nil, fmt.Errorf("remote CI shard container group %q is missing from status observation", item.groupID)
		}
		results[item.index].ContainerStatus = group.Status
		if terminalECIStatus(group.Status) {
			if err := bindObservedECIShardTiming(&results[item.index], group); err != nil {
				return nil, remoteShardExecutionError(shards[item.index], err)
			}
			terminal = append(terminal, item)
		} else {
			next = append(next, item)
		}
	}
	coordinator.collectTerminalShardReports(ctx, shards, groups, terminal, results, failures, expectedAgentTokenDigest)
	return next, nil
}

// collectTerminalShardReports 并行读取全部终态分片报告，不施加仓库并发上限。
func (coordinator *Coordinator) collectTerminalShardReports(
	ctx context.Context,
	shards []gate.ContainerShard,
	groups map[string]eci.ContainerGroup,
	terminal []pendingRemoteShard,
	results []ShardResult,
	failures []error,
	expectedAgentTokenDigest string,
) {
	var workers errgroup.Group
	for _, item := range terminal {
		workers.Go(func() error {
			report, workerLog, err := coordinator.observeShardReport(
				ctx, shards[item.index], item.groupID, groups[item.groupID], expectedAgentTokenDigest,
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
			timing, err = bindShardCandidateCompileTimingLog(materializerLog, timing)
			if err != nil {
				failure := remoteShardExecutionError(shards[item.index], fmt.Errorf("decode remote CI candidate compile timing: %w", err))
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

// waitShard 保留单分片观察入口，供定点诊断和契约测试使用。
func (coordinator *Coordinator) waitShard(ctx context.Context, shard gate.ContainerShard, groupID string, expectedAgentTokenDigest ...string) (ShardResult, error) {
	var expectedDigest string
	if len(expectedAgentTokenDigest) > 1 {
		return ShardResult{}, errors.New("remote CI shard accepts at most one expected agent token digest")
	}
	if len(expectedAgentTokenDigest) == 1 {
		expectedDigest = expectedAgentTokenDigest[0]
	}
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
			if err := coordinator.bindTerminalShardResult(ctx, shard, groupID, group, expectedDigest, &result); err != nil {
				return result, err
			}
			return result, nil
		}
		select {
		case <-ctx.Done():
			return result, remoteCloudShardPendingError(result.ContainerStatus, ctx.Err())
		case <-timer.C:
		}
	}
}

// bindTerminalShardResult 绑定终态报告、材料化耗时与诊断；任一缺失或不匹配都会立即阻断该分片。
func (coordinator *Coordinator) bindTerminalShardResult(
	ctx context.Context,
	shard gate.ContainerShard,
	groupID string,
	group eci.ContainerGroup,
	expectedAgentTokenDigest string,
	result *ShardResult,
) error {
	if err := bindObservedECIShardTiming(result, group); err != nil {
		return remoteShardExecutionError(shard, err)
	}
	report, workerLog, err := coordinator.observeShardReport(ctx, shard, groupID, group, expectedAgentTokenDigest)
	if err != nil {
		return remoteShardExecutionError(shard, err)
	}
	result.Report = report
	materializerLog, err := coordinator.runtime.DescribeContainerLog(ctx, groupID, "materializer")
	if err != nil {
		return remoteShardExecutionError(shard, fmt.Errorf("describe remote CI materializer log: %w", err))
	}
	timing, err := decodeShardMaterializationTimingLog(materializerLog, shard.IdentityDigest)
	if err != nil {
		return remoteShardExecutionError(shard, fmt.Errorf("decode remote CI materializer timing: %w", err))
	}
	timing, err = bindShardCandidateCompileTimingLog(materializerLog, timing)
	if err != nil {
		return remoteShardExecutionError(shard, fmt.Errorf("decode remote CI candidate compile timing: %w", err))
	}
	result.MaterializationTiming = timing
	result.workerDiagnostic = remoteShardLogTail(workerLog)
	return nil
}

// bindObservedECIShardTiming 只接受权威耗时账本所需的提供方时间戳，禁止以本地轮询时间充当证据。
func bindObservedECIShardTiming(result *ShardResult, group eci.ContainerGroup) error {
	if group.CreationTime.IsZero() {
		return errors.New("ECI terminal response is missing CreationTime")
	}
	materializerStartTime, err := observedECIMaterializerStartTime(group)
	if err != nil {
		return err
	}
	var terminalAt time.Time
	switch group.Status {
	case "Succeeded":
		terminalAt = group.SucceededTime
	default:
		terminalAt = group.FailedTime
	}
	if terminalAt.IsZero() {
		return fmt.Errorf("ECI terminal response is missing terminal time for status %q", group.Status)
	}
	if !materializerStartTime.After(group.CreationTime) || !terminalAt.After(materializerStartTime) {
		return errors.New("ECI provider timestamps are not strictly ordered from CreationTime through materializer StartTime to terminal time")
	}
	result.ECIWaitStartedAt = group.CreationTime.UTC()
	result.ECIWaitCompletedAt = materializerStartTime.UTC()
	result.ECITerminalAt = terminalAt.UTC()
	return nil
}

// observedECIMaterializerStartTime 查找唯一 materializer 初始化容器，并校验其提供方启动时间。
func observedECIMaterializerStartTime(group eci.ContainerGroup) (time.Time, error) {
	var materializerStartTime time.Time
	materializerFound := false
	for _, container := range group.InitContainers {
		if container.Name != "materializer" {
			continue
		}
		if materializerFound {
			return time.Time{}, errors.New("ECI terminal response has duplicate materializer init containers")
		}
		materializerFound = true
		materializerStartTime = container.CurrentState.StartTime
	}
	if !materializerFound || materializerStartTime.IsZero() {
		return time.Time{}, errors.New("ECI terminal response is missing materializer init-container CurrentState.StartTime")
	}
	return materializerStartTime, nil
}

// shardStatuses 批量读取并严格核对每一个 ECI container group 身份。
func (coordinator *Coordinator) shardStatuses(ctx context.Context, groupIDs []string) ([]eci.ContainerGroup, error) {
	if len(groupIDs) == 0 {
		return nil, errors.New("remote CI shard status request is empty")
	}
	groups, err := coordinator.runtime.DescribeContainerGroups(ctx, groupIDs...)
	if err != nil {
		return nil, err
	}
	if len(groups) != len(groupIDs) {
		return nil, errors.New("remote CI shard container group response is incomplete")
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
func (coordinator *Coordinator) shardReport(ctx context.Context, shard gate.ContainerShard, groupID string, group eci.ContainerGroup, expectedAgentTokenDigest ...string) (gate.PlanExecutionReport, string, error) {
	log, err := coordinator.runtime.DescribeContainerLog(ctx, groupID, "worker")
	if err != nil {
		return gate.PlanExecutionReport{}, "", err
	}
	report, err := decodeReportLog(log, shard.GateIDs, expectedAgentTokenDigest...)
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
func decodeReportLog(log string, expected []gate.GateID, expectedAgentTokenDigest ...string) (gate.PlanExecutionReport, error) {
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
	if len(expectedAgentTokenDigest) > 1 {
		return gate.PlanExecutionReport{}, errors.New("remote plan report accepts at most one expected agent token digest")
	}
	if len(expectedAgentTokenDigest) == 0 || expectedAgentTokenDigest[0] == "" {
		return gate.DecodePlanExecutionReportChunksForGateSet(chunks, expected)
	}
	return gate.DecodePlanExecutionReportChunksForGateSetAndAgentTokenDigest(chunks, expected, expectedAgentTokenDigest[0])
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
