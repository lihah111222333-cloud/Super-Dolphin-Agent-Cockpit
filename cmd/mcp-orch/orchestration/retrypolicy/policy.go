package retrypolicy

import (
	"encoding/json"
	"fmt"
)

type RetryPolicy struct {
	MaxAttempts int
	FailFast    bool
}

type dagSchedulePolicy struct {
	DefaultRetry int  `json:"default_retry,omitempty"`
	FailFast     bool `json:"fail_fast,omitempty"`
}

type dagMetadataPolicy struct {
	Schedule dagSchedulePolicy `json:"schedule"`
}

type nodeExecutionPolicy struct {
	Retry    int
	HasRetry bool
}

type nodeExecutionEnvelope struct {
	Execution struct {
		Retry *int `json:"retry,omitempty"`
	} `json:"execution"`
}

// ResolveRetryPolicy 解析重试策略。
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
	maxAttempts := retryCount + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	return RetryPolicy{MaxAttempts: maxAttempts, FailFast: dagPolicy.FailFast}, nil
}

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
