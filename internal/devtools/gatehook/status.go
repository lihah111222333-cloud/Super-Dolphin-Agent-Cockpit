package gatehook

import (
	"errors"
	"fmt"
	"strings"
)

// JobState 是 coordinator 返回给 adapter 的稳定状态集合。
type JobState string

const (
	JobStateQueued      JobState = "queued"
	JobStateRunning     JobState = "running"
	JobStatePassed      JobState = "passed"
	JobStateFailed      JobState = "failed"
	JobStateCancelled   JobState = "cancelled"
	JobStateTimeout     JobState = "timeout"
	JobStateInfraFailed JobState = "infra_failed"
	JobStateSuperseded  JobState = "superseded"
	JobStateTreeChanged JobState = "tree_changed"
)

// JobStatus 是 decision adapter 唯一接受的 coordinator 观察结果。
type JobStatus struct {
	JobID         string   `json:"job_id"`
	State         JobState `json:"state"`
	QueuePosition int      `json:"queue_position"`
	SourceTreeSHA string   `json:"source_tree_sha"`
	ReceiptID     string   `json:"receipt_id,omitempty"`
	Summary       string   `json:"summary,omitempty"`
	LogRef        string   `json:"log_ref,omitempty"`
}

// Validate 校验状态字段闭包，未知或不完整状态一律失败。
func (s JobStatus) Validate() error {
	if err := validateToken("job_id", s.JobID); err != nil {
		return err
	}
	if strings.TrimSpace(s.SourceTreeSHA) == "" {
		return errors.New("job source_tree_sha is required")
	}
	if strings.ContainsAny(s.Summary, "\r\n") || strings.ContainsAny(s.LogRef, "\r\n") {
		return errors.New("job summary and log_ref must be single-line")
	}
	return s.validateStateFields()
}

// validateStateFields 校验每个 job state 的必填字段。
func (s JobStatus) validateStateFields() error {
	switch s.State {
	case JobStateQueued:
		if s.QueuePosition <= 0 {
			return errors.New("queued job requires positive queue_position")
		}
	case JobStateRunning:
		if s.QueuePosition < 0 {
			return errors.New("running job queue_position must not be negative")
		}
	case JobStatePassed:
		if err := validateToken("receipt_id", s.ReceiptID); err != nil {
			return fmt.Errorf("passed job: %w", err)
		}
	case JobStateFailed, JobStateCancelled, JobStateTimeout, JobStateInfraFailed,
		JobStateSuperseded, JobStateTreeChanged:
		if strings.TrimSpace(s.Summary) == "" {
			return fmt.Errorf("%s job requires an actionable summary", s.State)
		}
	default:
		return fmt.Errorf("unsupported job state %q", s.State)
	}
	return nil
}

// HookDecision 是供应商无关的 hook 状态判定。
type HookDecision struct {
	Continue bool   `json:"continue,omitempty"`
	Decision string `json:"decision,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// DecisionForStatus 仅在有效 passed receipt 与当前 tree 完全一致时继续。
func DecisionForStatus(status JobStatus, currentTreeSHA string) (HookDecision, error) {
	if err := status.Validate(); err != nil {
		return HookDecision{}, err
	}
	if strings.TrimSpace(currentTreeSHA) == "" {
		return HookDecision{}, errors.New("current tree sha is required")
	}
	if status.SourceTreeSHA != currentTreeSHA {
		return blockDecision(status, JobStateTreeChanged,
			fmt.Sprintf("tree changed: job tree %s, current tree %s", status.SourceTreeSHA, currentTreeSHA)), nil
	}
	if status.State == JobStatePassed {
		return HookDecision{Continue: true}, nil
	}
	return blockDecision(status, status.State, status.Summary), nil
}

// WaitRequestForStatus 将可查询 job 转成统一 wait 请求。
func WaitRequestForStatus(repository RepositoryIdentity, invocation InvocationIdentity, status JobStatus) (WaitRequest, error) {
	if err := status.Validate(); err != nil {
		return WaitRequest{}, err
	}
	request := WaitRequest{Repository: repository, Invocation: invocation, JobID: status.JobID}
	return request, request.Validate()
}

func blockDecision(status JobStatus, state JobState, detail string) HookDecision {
	reason := fmt.Sprintf(
		"gate status=%s job=%s queue_position=%d; status: super-dolphin-gate status --job %s; wait: super-dolphin-gate wait --job %s",
		state,
		status.JobID,
		status.QueuePosition,
		status.JobID,
		status.JobID,
	)
	if strings.TrimSpace(detail) != "" {
		reason += "; action: " + detail
	}
	if strings.TrimSpace(status.LogRef) != "" {
		reason += "; log: " + status.LogRef
	}
	return HookDecision{Decision: "block", Reason: reason}
}
