package eci

import (
	"errors"
	"fmt"
	"maps"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// ArtifactBuilderLifecycle 只保存单个 builder group 的本地状态观察，不调用 provider。
type ArtifactBuilderLifecycle struct {
	request ArtifactBuilderRequest
	group   ArtifactBuilderGroup
	logs    string
	state   ArtifactBuilderState
}

// NewArtifactBuilderLifecycle 创建一个尚未产生云副作用的 builder 状态机。
func NewArtifactBuilderLifecycle(request ArtifactBuilderRequest) (*ArtifactBuilderLifecycle, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	return &ArtifactBuilderLifecycle{request: cloneArtifactBuilderRequest(request), state: ArtifactBuilderStateNew}, nil
}

// Request 返回状态机绑定的不可变 request 副本。
func (lifecycle *ArtifactBuilderLifecycle) Request() ArtifactBuilderRequest {
	if lifecycle == nil {
		return ArtifactBuilderRequest{}
	}
	return cloneArtifactBuilderRequest(lifecycle.request)
}

// State 返回当前 builder lifecycle 状态。
func (lifecycle *ArtifactBuilderLifecycle) State() ArtifactBuilderState {
	if lifecycle == nil {
		return ""
	}
	return lifecycle.state
}

// Group 返回最近一次通过 live identity 校验的 group 副本。
func (lifecycle *ArtifactBuilderLifecycle) Group() ArtifactBuilderGroup {
	if lifecycle == nil {
		return ArtifactBuilderGroup{}
	}
	return cloneArtifactBuilderGroup(lifecycle.group)
}

// Logs 返回最近一次通过有界校验的日志副本。
func (lifecycle *ArtifactBuilderLifecycle) Logs() string {
	if lifecycle == nil {
		return ""
	}
	return lifecycle.logs
}

// ObserveCreated 接收 Create 的单个 group 结果并校验 live identity。
func (lifecycle *ArtifactBuilderLifecycle) ObserveCreated(group ArtifactBuilderGroup) error {
	if lifecycle == nil || lifecycle.state != ArtifactBuilderStateNew {
		return ErrArtifactBuilderInvalidState
	}
	next, err := lifecycle.observeGroup(group)
	if err != nil {
		recordArtifactBuilderObserveFailure(lifecycle, group, err)
		return err
	}
	lifecycle.group = cloneArtifactBuilderGroup(group)
	lifecycle.state = next
	return nil
}

// Observe 接收后续 Describe 的单个 group 结果并拒绝回退或第二个 group。
func (lifecycle *ArtifactBuilderLifecycle) Observe(group ArtifactBuilderGroup) error {
	if lifecycle == nil || (lifecycle.state != ArtifactBuilderStateCreated && lifecycle.state != ArtifactBuilderStateRunning) {
		return ErrArtifactBuilderInvalidState
	}
	if group.ID != lifecycle.group.ID {
		lifecycle.group = cloneArtifactBuilderGroup(group)
		lifecycle.state = ArtifactBuilderStateIdentityDrift
		return fmt.Errorf("%w: artifact builder lifecycle cannot observe a second container group", ErrArtifactBuilderIdentityDrift)
	}
	next, err := lifecycle.observeGroup(group)
	if err != nil {
		recordArtifactBuilderObserveFailure(lifecycle, group, err)
		return err
	}
	if !validArtifactBuilderObservationTransition(lifecycle.state, next) {
		return fmt.Errorf("%w: %s to %s", ErrArtifactBuilderInvalidState, lifecycle.state, next)
	}
	lifecycle.group = cloneArtifactBuilderGroup(group)
	lifecycle.state = next
	return nil
}

func recordArtifactBuilderObserveFailure(lifecycle *ArtifactBuilderLifecycle, group ArtifactBuilderGroup, err error) {
	lifecycle.group = cloneArtifactBuilderGroup(group)
	if errors.Is(err, ErrArtifactBuilderIdentityDrift) {
		lifecycle.state = ArtifactBuilderStateIdentityDrift
		return
	}
	lifecycle.state = ArtifactBuilderStateError
}

// ObserveLogs 接收 bounded builder logs；超限日志必须失败而不能静默截断。
func (lifecycle *ArtifactBuilderLifecycle) ObserveLogs(logs string) error {
	if lifecycle == nil || lifecycle.state == ArtifactBuilderStateNew || lifecycle.state == ArtifactBuilderStateAbsent || lifecycle.state == ArtifactBuilderStateDeleteRequested {
		return ErrArtifactBuilderInvalidState
	}
	if len([]byte(logs)) > ArtifactBuilderMaxLogBytes {
		return errors.New("artifact builder logs exceed bounded byte limit")
	}
	lifecycle.logs = logs
	return nil
}

// MarkCancelled 将取消结果收口到 cleanup-only 状态；取消永远不能形成成功证据。
func (lifecycle *ArtifactBuilderLifecycle) MarkCancelled() error {
	if lifecycle == nil || !artifactBuilderCleanupEligibleState(lifecycle.state) {
		return ErrArtifactBuilderInvalidState
	}
	lifecycle.state = ArtifactBuilderStateCancelled
	return nil
}

func artifactBuilderCleanupEligibleState(state ArtifactBuilderState) bool {
	switch state {
	case ArtifactBuilderStateCreated, ArtifactBuilderStateRunning,
		ArtifactBuilderStateIdentityDrift, ArtifactBuilderStateError:
		return true
	default:
		return false
	}
}

// MarkDeleteRequested 记录成功调用 Delete 后的 cleanup 阶段。
func (lifecycle *ArtifactBuilderLifecycle) MarkDeleteRequested() error {
	if lifecycle == nil || !artifactBuilderDeleteRequestedState(lifecycle.state) {
		return ErrArtifactBuilderInvalidState
	}
	lifecycle.state = ArtifactBuilderStateDeleteRequested
	return nil
}

func artifactBuilderDeleteRequestedState(state ArtifactBuilderState) bool {
	switch state {
	case ArtifactBuilderStateCreated, ArtifactBuilderStateRunning,
		ArtifactBuilderStateSucceeded, ArtifactBuilderStateFailed,
		ArtifactBuilderStateIdentityDrift, ArtifactBuilderStateError,
		ArtifactBuilderStateCancelled:
		return true
	default:
		return false
	}
}

// ConfirmAbsent 只接受 provider 明确返回的 absence proof。
func (lifecycle *ArtifactBuilderLifecycle) ConfirmAbsent(absent bool) error {
	if lifecycle == nil || lifecycle.state != ArtifactBuilderStateDeleteRequested {
		return ErrArtifactBuilderInvalidState
	}
	if !absent {
		return ErrArtifactBuilderStillPresent
	}
	lifecycle.state = ArtifactBuilderStateAbsent
	return nil
}

// observeGroup 校验 live identity、终态时间和 exit code，并返回下一个本地状态。
func (lifecycle *ArtifactBuilderLifecycle) observeGroup(group ArtifactBuilderGroup) (ArtifactBuilderState, error) {
	if err := validateArtifactBuilderGroupIdentity(lifecycle.request, group); err != nil {
		return "", err
	}
	switch group.Status {
	case ArtifactBuilderGroupPending, ArtifactBuilderGroupCreating:
		return artifactBuilderPendingState(group)
	case ArtifactBuilderGroupRunning:
		return artifactBuilderRunningState(group)
	case ArtifactBuilderGroupSucceeded:
		return artifactBuilderTerminalState(group, true)
	case ArtifactBuilderGroupFailed:
		return artifactBuilderTerminalState(group, false)
	case ArtifactBuilderGroupTerminated:
		return artifactBuilderTerminatedState(group)
	case ArtifactBuilderGroupError:
		return ArtifactBuilderStateError, nil
	case ArtifactBuilderGroupCancelled:
		return ArtifactBuilderStateCancelled, nil
	case ArtifactBuilderGroupIdentityDrift:
		return "", fmt.Errorf("%w: provider reported identity drift", ErrArtifactBuilderIdentityDrift)
	default:
		return "", fmt.Errorf("artifact builder group status %q is unsupported", group.Status)
	}
}

func artifactBuilderPendingState(group ArtifactBuilderGroup) (ArtifactBuilderState, error) {
	if group.Container.State != "" {
		return "", errors.New("artifact builder pending group must not claim a container state")
	}
	return ArtifactBuilderStateCreated, nil
}

func artifactBuilderRunningState(group ArtifactBuilderGroup) (ArtifactBuilderState, error) {
	if group.Container.State != ArtifactBuilderContainerRunning {
		return "", errors.New("artifact builder running group must report a running container")
	}
	return ArtifactBuilderStateRunning, nil
}

func artifactBuilderTerminalState(group ArtifactBuilderGroup, success bool) (ArtifactBuilderState, error) {
	if err := validateArtifactBuilderTerminal(group, success); err != nil {
		return "", err
	}
	if success {
		return ArtifactBuilderStateSucceeded, nil
	}
	return ArtifactBuilderStateFailed, nil
}

func artifactBuilderTerminatedState(group ArtifactBuilderGroup) (ArtifactBuilderState, error) {
	if err := validateArtifactBuilderTerminal(group, group.Container.ExitCode != nil && *group.Container.ExitCode == 0); err != nil {
		return "", err
	}
	if *group.Container.ExitCode == 0 {
		return ArtifactBuilderStateSucceeded, nil
	}
	return ArtifactBuilderStateFailed, nil
}

// validateArtifactBuilderTerminal 校验终态 exit code 与时间区间证据。
func validateArtifactBuilderTerminal(group ArtifactBuilderGroup, success bool) error {
	if group.Container.State != ArtifactBuilderContainerTerminated || group.Container.ExitCode == nil {
		return errors.New("artifact builder terminal group must include terminated state and exit code")
	}
	if group.Container.StartTime.IsZero() || group.Container.FinishTime.IsZero() || group.Container.FinishTime.Before(group.Container.StartTime) {
		return errors.New("artifact builder terminal group must include an ordered live time interval")
	}
	if success && *group.Container.ExitCode != 0 {
		return errors.New("artifact builder succeeded group must have exit code zero")
	}
	return nil
}

// validArtifactBuilderObservationTransition 判断 Describe 观察状态是否单向推进。
func validArtifactBuilderObservationTransition(current, next ArtifactBuilderState) bool {
	if current == ArtifactBuilderStateCreated {
		return next == ArtifactBuilderStateCreated || next == ArtifactBuilderStateRunning || artifactBuilderTerminalObservationState(next)
	}
	if current == ArtifactBuilderStateRunning {
		return next == ArtifactBuilderStateRunning || artifactBuilderTerminalObservationState(next)
	}
	return false
}

func artifactBuilderTerminalObservationState(state ArtifactBuilderState) bool {
	switch state {
	case ArtifactBuilderStateSucceeded, ArtifactBuilderStateFailed, ArtifactBuilderStateError, ArtifactBuilderStateCancelled:
		return true
	default:
		return false
	}
}

func cloneArtifactBuilderRequest(request ArtifactBuilderRequest) ArtifactBuilderRequest {
	request.VSwitches = append([]cicontract.ECIVSwitch(nil), request.VSwitches...)
	request.Command = append([]string(nil), request.Command...)
	return request
}

func cloneArtifactBuilderGroup(group ArtifactBuilderGroup) ArtifactBuilderGroup {
	group.Command = append([]string(nil), group.Command...)
	group.VSwitches = append([]cicontract.ECIVSwitch(nil), group.VSwitches...)
	group.Tags = cloneArtifactBuilderTags(group.Tags)
	group.Container.Command = append([]string(nil), group.Container.Command...)
	group.Container.Args = append([]string(nil), group.Container.Args...)
	if group.Container.ExitCode != nil {
		exitCode := *group.Container.ExitCode
		group.Container.ExitCode = &exitCode
	}
	return group
}

func cloneArtifactBuilderTags(tags map[string]string) map[string]string {
	if tags == nil {
		return nil
	}
	clone := make(map[string]string, len(tags))
	maps.Copy(clone, tags)
	return clone
}
