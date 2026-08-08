package remoteci

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
)

// observeShardReport 为终态日志汇总设置独立看门狗，和仍在运行的云端分片作出明确区分。
func (coordinator *Coordinator) observeShardReport(
	parent context.Context,
	shard gate.ContainerShard,
	groupID string,
	group eci.ContainerGroup,
	expectedAgentTokenDigest ...string,
) (gate.PlanExecutionReport, string, error) {
	observation, cancel := gateprivate.WithTimeout(parent, coordinator.observationTimeout)
	defer cancel()
	report, workerLog, err := coordinator.shardReport(observation, shard, groupID, group, expectedAgentTokenDigest...)
	if err != nil {
		return gate.PlanExecutionReport{}, workerLog, classifyShardObservationError(
			parent, observation, "terminal report aggregation", group.Status, coordinator.observationTimeout, err,
		)
	}
	return report, workerLog, nil
}

// classifyShardObservationError 将云端尚未终态、控制面探测停滞和普通 API 错误分成稳定诊断。
func classifyShardObservationError(
	parent context.Context,
	observation context.Context,
	phase string,
	lastStatus string,
	timeout time.Duration,
	err error,
) error {
	if parentErr := parent.Err(); parentErr != nil {
		if terminalECIStatus(lastStatus) {
			return fmt.Errorf(
				"remote CI coordinator did not aggregate terminal cloud shard before run deadline (last_status=%q phase=%s): %w",
				lastStatus, phase, parentErr,
			)
		}
		return remoteCloudShardPendingError(lastStatus, parentErr)
	}
	if errors.Is(observation.Err(), context.DeadlineExceeded) {
		return fmt.Errorf(
			"remote CI coordinator observation stalled (last_status=%q phase=%s timeout=%s): %w",
			lastStatus, phase, timeout, err,
		)
	}
	return err
}

// remoteCloudShardPendingError 标记协调器仍可观测、但云端分片在作业时限内尚未终态。
func remoteCloudShardPendingError(lastStatus string, err error) error {
	return fmt.Errorf(
		"remote CI cloud shard has not reached terminal state before run deadline (last_status=%q): %w",
		lastStatus, err,
	)
}
