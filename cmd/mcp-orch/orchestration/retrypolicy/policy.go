package retrypolicy

import (
	"encoding/json"
	"fmt"
)

// RetryPolicy 是 dispatcher 执行 DAG wakeup 时使用的最终重试策略。
type RetryPolicy struct {
	MaxAttempts int
	FailFast    bool
}

// dagSchedulePolicy 对应 DAG metadata.schedule 中的默认重试配置。
type dagSchedulePolicy struct {
	DefaultRetry int  `json:"default_retry,omitempty"`
	FailFast     bool `json:"fail_fast,omitempty"`
}

// dagMetadataPolicy 是 DAG metadata 的最小解析视图，只读取 schedule。
type dagMetadataPolicy struct {
	Schedule dagSchedulePolicy `json:"schedule"`
}

// nodeExecutionPolicy 表示节点 execution.retry 覆盖值及其是否显式配置。
type nodeExecutionPolicy struct {
	Retry    int
	HasRetry bool
}

// nodeExecutionEnvelope 是 node.config 的最小解析视图，只读取 execution.retry。
type nodeExecutionEnvelope struct {
	Execution struct {
		Retry *int `json:"retry,omitempty"`
	} `json:"execution"`
}

// ResolveRetryPolicy 合并 DAG 默认 retry 与节点级 execution.retry。
// MaxAttempts 至少为 1，retry=0 表示只尝试一次。
func ResolveRetryPolicy(dagMetadata, nodeConfig json.RawMessage) (RetryPolicy, error) {
	dagPolicy, err := decodeDAGSchedulePolicy(dagMetadata)
	if err != nil {
		return RetryPolicy{}, err
	}
	nodePolicy, err := decodeNodeExecutionPolicy(nodeConfig)
	if err != nil {
		return RetryPolicy{}, err
	}
	retryCount := dagPolicy.DefaultRetry
	if nodePolicy.HasRetry {
		retryCount = nodePolicy.Retry
	}
	if retryCount < 0 {
		return RetryPolicy{}, fmt.Errorf("retry must be non-negative, got %d", retryCount)
	}
	return RetryPolicy{MaxAttempts: retryCount + 1, FailFast: dagPolicy.FailFast}, nil
}

// decodeDAGSchedulePolicy 解析 DAG metadata.schedule，空 metadata 使用零值策略。
func decodeDAGSchedulePolicy(raw json.RawMessage) (dagSchedulePolicy, error) {
	if len(raw) == 0 {
		return dagSchedulePolicy{}, nil
	}
	var envelope dagMetadataPolicy
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return dagSchedulePolicy{}, fmt.Errorf("decode dag metadata schedule policy: %w", err)
	}
	return envelope.Schedule, nil
}

// decodeNodeExecutionPolicy 解析节点 execution.retry，区分未配置和显式配置 0。
func decodeNodeExecutionPolicy(raw json.RawMessage) (nodeExecutionPolicy, error) {
	if len(raw) == 0 {
		return nodeExecutionPolicy{}, nil
	}
	var envelope nodeExecutionEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nodeExecutionPolicy{}, fmt.Errorf("decode node execution policy: %w", err)
	}
	if envelope.Execution.Retry == nil {
		return nodeExecutionPolicy{}, nil
	}
	return nodeExecutionPolicy{Retry: *envelope.Execution.Retry, HasRetry: true}, nil
}
