package gate

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// RemoteCIContainerTerminalEvidence 保存一个 ECI container 或 init-container 的
// 有界终态字段。它只包含 provider 终态，不包含容器环境变量或日志正文。
type RemoteCIContainerTerminalEvidence struct {
	Name     string `json:"name"`
	State    string `json:"state,omitempty"`
	ExitCode *int64 `json:"exit_code,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Message  string `json:"message,omitempty"`
}

// RemoteCIEventEvidence 保存提供方最近一次终态观察中的单条事件。
type RemoteCIEventEvidence struct {
	Type          string `json:"type,omitempty"`
	Reason        string `json:"reason,omitempty"`
	Message       string `json:"message,omitempty"`
	Count         int64  `json:"count,omitempty"`
	LastTimestamp string `json:"last_timestamp,omitempty"`
}

// RemoteCITerminalEvidence 是 ECI 终态诊断的唯一结构化 producer。
// SQLite 将其两个嵌套集合规范化存储；不得新增 terminal_evidence_json 第二真相源。
type RemoteCITerminalEvidence struct {
	Containers     []RemoteCIContainerTerminalEvidence `json:"containers,omitempty"`
	InitContainers []RemoteCIContainerTerminalEvidence `json:"init_containers,omitempty"`
	Events         []RemoteCIEventEvidence             `json:"events,omitempty"`
}

const (
	remoteCITerminalEvidenceMaxContainers = 32
	remoteCITerminalEvidenceMaxEvents     = 3
	remoteCITerminalEvidenceMaxFieldBytes = 1024
)

// Clone 深复制终态证据，避免 receipt、coordinator result 与 SQLite projection 共享可变 slice。
func (e *RemoteCITerminalEvidence) Clone() *RemoteCITerminalEvidence {
	if e == nil {
		return nil
	}
	clone := &RemoteCITerminalEvidence{
		Containers:     cloneRemoteCIContainerEvidence(e.Containers),
		InitContainers: cloneRemoteCIContainerEvidence(e.InitContainers),
		Events:         append([]RemoteCIEventEvidence(nil), e.Events...),
	}
	for index := range clone.Containers {
		clone.Containers[index].ExitCode = cloneInt64Pointer(clone.Containers[index].ExitCode)
	}
	for index := range clone.InitContainers {
		clone.InitContainers[index].ExitCode = cloneInt64Pointer(clone.InitContainers[index].ExitCode)
	}
	return clone
}

func cloneRemoteCIContainerEvidence(values []RemoteCIContainerTerminalEvidence) []RemoteCIContainerTerminalEvidence {
	if values == nil {
		return nil
	}
	return append([]RemoteCIContainerTerminalEvidence(nil), values...)
}

func cloneInt64Pointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

// Validate 校验规范化投影可安全写入，且拒绝未命名容器或重复终态事件。
func (e *RemoteCITerminalEvidence) Validate() error {
	if e == nil {
		return errors.New("remote CI terminal evidence is required")
	}
	if err := validateRemoteCITerminalEvidenceShape(e); err != nil {
		return err
	}
	if err := validateRemoteCIContainerEvidence(e.Containers); err != nil {
		return fmt.Errorf("remote CI container terminal evidence: %w", err)
	}
	if err := validateRemoteCIContainerEvidence(e.InitContainers); err != nil {
		return fmt.Errorf("remote CI init-container terminal evidence: %w", err)
	}
	for index, event := range e.Events {
		if err := validateRemoteCIEventEvidence(event); err != nil {
			return fmt.Errorf("remote CI terminal event %d: %w", index, err)
		}
	}
	return nil
}

func validateRemoteCITerminalEvidenceShape(e *RemoteCITerminalEvidence) error {
	if len(e.Containers) > remoteCITerminalEvidenceMaxContainers || len(e.InitContainers) > remoteCITerminalEvidenceMaxContainers {
		return fmt.Errorf("remote CI terminal evidence contains too many containers")
	}
	if len(e.Events) > remoteCITerminalEvidenceMaxEvents {
		return fmt.Errorf("remote CI terminal evidence contains too many events")
	}
	if len(e.Containers)+len(e.InitContainers)+len(e.Events) == 0 {
		return errors.New("remote CI terminal evidence is empty")
	}
	return nil
}

// validateRemoteCIContainerEvidence 校验容器名称唯一且 provider 字段有界。
func validateRemoteCIContainerEvidence(values []RemoteCIContainerTerminalEvidence) error {
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		if strings.TrimSpace(value.Name) == "" || value.Name != strings.TrimSpace(value.Name) || !utf8.ValidString(value.Name) {
			return fmt.Errorf("container %d name is required", index)
		}
		if _, exists := seen[value.Name]; exists {
			return fmt.Errorf("container %q is duplicated", value.Name)
		}
		seen[value.Name] = struct{}{}
		if err := validateRemoteCITerminalEvidenceFields(value.State, value.Reason, value.Message); err != nil {
			return fmt.Errorf("container %q: %w", value.Name, err)
		}
		if err := validateRemoteCIContainerState(value.State); err != nil {
			return fmt.Errorf("container %q: %w", value.Name, err)
		}
	}
	return nil
}

func validateRemoteCIEventEvidence(value RemoteCIEventEvidence) error {
	if err := validateRemoteCITerminalEvidenceFields(value.Type, value.Reason, value.Message); err != nil {
		return err
	}
	if err := validateRemoteCIEventType(value.Type); err != nil {
		return err
	}
	if value.Count < 0 {
		return errors.New("event count must not be negative")
	}
	if len(value.LastTimestamp) > remoteCITerminalEvidenceMaxFieldBytes {
		return errors.New("event timestamp exceeds bounded size")
	}
	if value.LastTimestamp != "" {
		if _, err := time.Parse(time.RFC3339, value.LastTimestamp); err != nil {
			return errors.New("event timestamp is invalid")
		}
	}
	return nil
}

// validateRemoteCIContainerState 只接受 ECI CurrentState 的稳定状态枚举。
func validateRemoteCIContainerState(state string) error {
	if state == "" || state == "Waiting" || state == "Running" || state == "Terminated" {
		return nil
	}
	return errors.New("container state is invalid")
}

// validateRemoteCIEventType 只接受 Kubernetes 事件公开的两个类型枚举。
func validateRemoteCIEventType(eventType string) error {
	if eventType == "" || eventType == "Normal" || eventType == "Warning" {
		return nil
	}
	return errors.New("event type is invalid")
}

func validateRemoteCITerminalEvidenceFields(values ...string) error {
	for _, value := range values {
		if !utf8.ValidString(value) {
			return errors.New("provider field is not valid UTF-8")
		}
		if len(value) > remoteCITerminalEvidenceMaxFieldBytes {
			return errors.New("provider field exceeds bounded size")
		}
	}
	return nil
}
