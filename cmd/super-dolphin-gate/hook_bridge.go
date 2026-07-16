package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gatehook"
)

var errHookReceiptUnavailable = errors.New("coordinator does not expose a signed gate receipt")

type hookCoordinator interface {
	gatehook.Coordinator
	Close() error
}

type hookCoordinatorConnector func(context.Context) (hookCoordinator, error)

type hookCoordinatorClient interface {
	coordinatorClient
	StatusInvocation(context.Context, string, string) (jobStatus, error)
}

type hookCoordinatorBridge struct {
	client hookCoordinatorClient
}

var _ hookCoordinator = (*hookCoordinatorBridge)(nil)

func connectProductionHookCoordinator(ctx context.Context) (hookCoordinator, error) {
	client, err := connectProductionCoordinator(ctx)
	if err != nil {
		return nil, err
	}
	typed, ok := client.(hookCoordinatorClient)
	if !ok {
		return nil, errors.Join(errors.New("production coordinator lacks hook invocation lookup"), client.Close())
	}
	return &hookCoordinatorBridge{client: typed}, nil
}

// Submit 将 typed hook 请求接到 canonical plan 与 durable coordinator submit。
func (bridge *hookCoordinatorBridge) Submit(
	ctx context.Context,
	request gatehook.SubmitRequest,
) (gatehook.JobStatus, error) {
	if bridge == nil || bridge.client == nil {
		return gatehook.JobStatus{}, errCoordinatorDependency
	}
	if err := request.Validate(); err != nil {
		return gatehook.JobStatus{}, fmt.Errorf("validate hook submit: %w", err)
	}
	plan, err := gatecontract.BuildGatePlan(request.Profile, request.Source)
	if err != nil {
		return gatehook.JobStatus{}, fmt.Errorf("build hook gate plan: %w", err)
	}
	invocationID := coordinatorHookInvocationID(request.Invocation)
	status, submitErr := bridge.client.Submit(ctx, submitRequest{
		RepositoryRoot: request.Repository.WorktreeRoot,
		Plan:           plan,
		InvocationID:   invocationID,
	})
	return adaptCoordinatorHookStatus(status, submitErr)
}

// Status 按 invocation 和活动 worktree 查询 coordinator 状态。
func (bridge *hookCoordinatorBridge) Status(
	ctx context.Context,
	request gatehook.StatusRequest,
) (gatehook.JobStatus, error) {
	if bridge == nil || bridge.client == nil {
		return gatehook.JobStatus{}, errCoordinatorDependency
	}
	if err := request.Validate(); err != nil {
		return gatehook.JobStatus{}, fmt.Errorf("validate hook status: %w", err)
	}
	status, statusErr := bridge.client.StatusInvocation(
		ctx,
		coordinatorHookInvocationID(request.Invocation),
		request.Repository.WorktreeRoot,
	)
	return adaptCoordinatorHookStatus(status, statusErr)
}

// Wait 先证明 job 属于 invocation，再等待同一个 durable job。
func (bridge *hookCoordinatorBridge) Wait(
	ctx context.Context,
	request gatehook.WaitRequest,
) (gatehook.JobStatus, error) {
	if bridge == nil || bridge.client == nil {
		return gatehook.JobStatus{}, errCoordinatorDependency
	}
	if err := request.Validate(); err != nil {
		return gatehook.JobStatus{}, fmt.Errorf("validate hook wait: %w", err)
	}
	invocationID := coordinatorHookInvocationID(request.Invocation)
	observed, err := bridge.client.StatusInvocation(ctx, invocationID, request.Repository.WorktreeRoot)
	if err != nil {
		return gatehook.JobStatus{}, err
	}
	if observed.JobID != request.JobID || observed.InvocationID != invocationID {
		return gatehook.JobStatus{}, fmt.Errorf("%w: wait job is not bound to hook invocation", errCoordinatorState)
	}
	status, waitErr := bridge.client.Wait(ctx, request.JobID)
	return adaptCoordinatorHookStatus(status, waitErr)
}

// Close 关闭 bridge 持有的 coordinator transport。
func (bridge *hookCoordinatorBridge) Close() error {
	if bridge == nil || bridge.client == nil {
		return nil
	}
	return bridge.client.Close()
}

func coordinatorHookInvocationID(identity gatehook.InvocationIdentity) string {
	digest := sha256.Sum256([]byte(identity.Owner + "\x00" + identity.Key))
	return fmt.Sprintf("hook-%x", digest)
}

// adaptCoordinatorHookStatus 将内部状态收敛到 gatehook 的严格 decision DTO。
func adaptCoordinatorHookStatus(status jobStatus, statusErr error) (gatehook.JobStatus, error) {
	if statusErr != nil {
		return gatehook.JobStatus{}, statusErr
	}
	if status.Terminal != status.State.terminal() {
		return gatehook.JobStatus{}, fmt.Errorf("%w: coordinator terminal flag drifted from state", errCoordinatorState)
	}
	state, err := hookJobState(status.State)
	if err != nil {
		return gatehook.JobStatus{}, err
	}
	adapted := gatehook.JobStatus{
		JobID:         status.JobID,
		State:         state,
		QueuePosition: status.QueuePosition,
		SourceTreeSHA: status.JobSourceTreeSHA,
		Summary:       hookJobSummary(status.State),
	}
	if status.State == jobStatePassed {
		return adapted, fmt.Errorf(
			"%w: passed job %s is not acceptable for source tree %s",
			errHookReceiptUnavailable,
			status.JobID,
			status.JobSourceTreeSHA,
		)
	}
	if err := adapted.Validate(); err != nil {
		return adapted, fmt.Errorf("validate coordinator hook status: %w", err)
	}
	return adapted, nil
}

// hookJobState 将 coordinator 状态映射到公开 hook 状态集合。
func hookJobState(state jobState) (gatehook.JobState, error) {
	switch state {
	case jobStateQueued:
		return gatehook.JobStateQueued, nil
	case jobStateStarted:
		return gatehook.JobStateRunning, nil
	case jobStatePassed:
		return gatehook.JobStatePassed, nil
	case jobStateFailed:
		return gatehook.JobStateFailed, nil
	case jobStateInfraFailed:
		return gatehook.JobStateInfraFailed, nil
	case jobStateCancelled:
		return gatehook.JobStateCancelled, nil
	case jobStateTimeout:
		return gatehook.JobStateTimeout, nil
	default:
		return "", fmt.Errorf("%w: unsupported coordinator job state %q", errCoordinatorState, state)
	}
}

func hookJobSummary(state jobState) string {
	switch state {
	case jobStateFailed:
		return "gate job failed; inspect structured coordinator status"
	case jobStateInfraFailed:
		return "gate job infrastructure failed; inspect structured coordinator status"
	case jobStateCancelled:
		return "gate job was cancelled; submit the same source again"
	case jobStateTimeout:
		return "gate job timed out; inspect status before retrying"
	default:
		return ""
	}
}
