package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gatehook"
)

var errHookReceiptInvalid = errors.New("coordinator result receipt is not authoritative")

type hookCoordinator interface {
	gatehook.Coordinator
	AuthorizeGitPush(context.Context, gitPushGrantRequest) error
	Close() error
}

type hookCoordinatorConnector func(context.Context) (hookCoordinator, error)

type hookCoordinatorClient interface {
	coordinatorClient
	StatusInvocation(context.Context, string, string) (jobStatus, error)
	ResultReceipt(context.Context, string) (gatecontract.ResultReceipt, error)
}

type hookCoordinatorBridge struct {
	client    hookCoordinatorClient
	authority hookResultReceiptAuthority
	grants    *actionGrantService
}

type gitPushGrantRequest struct {
	Status          gatehook.JobStatus
	Submit          gatehook.SubmitRequest
	RemoteURL       string
	ActionAttemptID string
}

var _ hookCoordinator = (*hookCoordinatorBridge)(nil)

// connectProductionHookCoordinator 装配回执复验与独立 ActionGrant authority。
func connectProductionHookCoordinator(ctx context.Context) (hookCoordinator, error) {
	client, err := connectProductionCoordinator(ctx)
	if err != nil {
		return nil, err
	}
	typed, ok := client.(hookCoordinatorClient)
	if !ok {
		return nil, errors.Join(errors.New("production coordinator lacks hook invocation lookup"), client.Close())
	}
	config, err := loadProductionCoordinatorConfig()
	if err != nil {
		return nil, errors.Join(err, client.Close())
	}
	authority, err := newProductionHookResultReceiptAuthority(ctx, config)
	if err != nil {
		return nil, errors.Join(err, client.Close())
	}
	transport, ok := client.(*coordinatorTransportClient)
	if !ok {
		return nil, errors.Join(errors.New("production coordinator lacks action grant store"), client.Close())
	}
	grants, err := newProductionActionGrantService(config, transport.store, authority)
	if err != nil {
		return nil, errors.Join(err, client.Close())
	}
	return &hookCoordinatorBridge{client: typed, authority: authority, grants: grants}, nil
}

// AuthorizeGitPush 签发并消费一个绑定精确 typed ref update 的单次授权。
func (bridge *hookCoordinatorBridge) AuthorizeGitPush(
	ctx context.Context,
	request gitPushGrantRequest,
) error {
	if bridge == nil || bridge.client == nil || bridge.grants == nil {
		return errors.New("action grant service is not configured")
	}
	rangeSource, err := validateGitPushGrantRequest(request)
	if err != nil {
		return err
	}
	receipt, err := bridge.loadGitPushGrantReceipt(ctx, request)
	if err != nil {
		return err
	}
	nonce := gitPushGrantNonce(receipt, request)
	grant, err := bridge.grants.Issue(ctx, actionGrantIntent{
		Receipt: receipt, InvocationOwner: request.Submit.Invocation.Owner,
		Audience: gatecontract.ActionAudienceGitPush, ActionPolicy: string(rangeSource.UpdateKind),
		RemoteURL: request.RemoteURL, Ref: rangeSource.RemoteRef,
		OldSHA: rangeSource.ObservedRemoteSHA, NewSHA: rangeSource.HeadSHA,
		ActionAttemptID: request.ActionAttemptID, RequestNonce: nonce,
	})
	if err != nil {
		return fmt.Errorf("issue git.push action grant: %w", err)
	}
	_, err = bridge.grants.Consume(ctx, grant, actionGrantExpectation{
		Audience: gatecontract.ActionAudienceGitPush, RepoID: receipt.RepoID,
		InvocationID: receipt.InvocationID, SourceTreeSHA: receipt.Source.SourceTreeSHA,
		Generation: receipt.Generation, RemoteURL: request.RemoteURL, Ref: rangeSource.RemoteRef,
		OldSHA: rangeSource.ObservedRemoteSHA, NewSHA: rangeSource.HeadSHA,
		ActionAttemptID: request.ActionAttemptID,
	})
	if err != nil {
		return fmt.Errorf("consume git.push action grant: %w", err)
	}
	return nil
}

// validateGitPushGrantRequest 校验 passed 状态及 pre-push range 输入完整绑定。
func validateGitPushGrantRequest(request gitPushGrantRequest) (*gatecontract.RangeSource, error) {
	if request.Status.State != gatehook.JobStatePassed || request.Status.ReceiptID == "" {
		return nil, errors.New("git.push action grant requires a passed receipt status")
	}
	if err := request.Submit.Validate(); err != nil {
		return nil, fmt.Errorf("validate git.push grant submit: %w", err)
	}
	rangeSource := request.Submit.Source.Range
	if request.Submit.Entrypoint != gatecontract.CIEntrypointGitPrePush || rangeSource == nil {
		return nil, errors.New("git.push action grant requires an exact pre-push range source")
	}
	if strings.TrimSpace(request.RemoteURL) == "" {
		return nil, errors.New("git.push action grant requires the exact remote URL")
	}
	if err := gatecontract.ValidateActionAttemptID(request.ActionAttemptID); err != nil {
		return nil, fmt.Errorf("validate git.push action attempt: %w", err)
	}
	return rangeSource, nil
}

// loadGitPushGrantReceipt 重载并比较 hook status、invocation 与 source tree。
func (bridge *hookCoordinatorBridge) loadGitPushGrantReceipt(
	ctx context.Context,
	request gitPushGrantRequest,
) (gatecontract.ResultReceipt, error) {
	receipt, err := bridge.client.ResultReceipt(ctx, request.Status.JobID)
	if err != nil {
		return gatecontract.ResultReceipt{}, fmt.Errorf("load git.push grant receipt: %w", err)
	}
	status := jobStatus{
		JobID: request.Status.JobID, ReceiptID: request.Status.ReceiptID,
		InvocationID:     coordinatorHookInvocationID(request.Submit.Invocation),
		JobSourceTreeSHA: request.Status.SourceTreeSHA,
	}
	if !resultReceiptMatchesHookStatus(receipt, status, request.Submit.Source.SourceTreeSHA) {
		return gatecontract.ResultReceipt{}, errors.New("git.push grant receipt does not match typed hook status")
	}
	return receipt, nil
}

func gitPushGrantNonce(receipt gatecontract.ResultReceipt, request gitPushGrantRequest) string {
	rangeSource := request.Submit.Source.Range
	payload := strings.Join([]string{
		receipt.ReceiptID, request.Submit.Invocation.Owner, request.Submit.Invocation.Key,
		request.RemoteURL, rangeSource.RemoteRef, rangeSource.ObservedRemoteSHA,
		rangeSource.HeadSHA, request.Submit.Source.SourceTreeSHA, request.ActionAttemptID,
	}, "\x00")
	digest := sha256.Sum256([]byte(payload))
	return fmt.Sprintf("sha256:%x", digest)
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
		Entrypoint:     request.Entrypoint, AuthorityOwner: authorityOwnerForHook(request),
		AuthorityAttestation: authorityAttestationForHook(request),
	})
	return bridge.adaptCoordinatorHookStatus(ctx, status, submitErr, request.Source.SourceTreeSHA)
}

func authorityOwnerForHook(request gatehook.SubmitRequest) gatecontract.CIEntrypointOwner {
	for _, entrypoint := range gatecontract.CIEntrypointRegistry() {
		if entrypoint.ID == request.Entrypoint {
			return entrypoint.Owner
		}
	}
	return ""
}

func authorityAttestationForHook(request gatehook.SubmitRequest) string {
	for _, entrypoint := range gatecontract.CIEntrypointRegistry() {
		if entrypoint.ID == request.Entrypoint && entrypoint.Authoritative {
			return request.Invocation.Key
		}
	}
	return ""
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
	return bridge.adaptCoordinatorHookStatus(ctx, status, statusErr, request.ExpectedSourceTreeSHA)
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
	expectedTree := observed.JobSourceTreeSHA
	if waitErr == nil && status.State == jobStatePassed {
		_, source, sourceErr := gatehook.CurrentWorktreeSource(ctx, request.Repository.WorktreeRoot)
		if sourceErr != nil {
			return gatehook.JobStatus{}, fmt.Errorf("read current wait source: %w", sourceErr)
		}
		expectedTree = source.SourceTreeSHA
	}
	return bridge.adaptCoordinatorHookStatus(ctx, status, waitErr, expectedTree)
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
func (bridge *hookCoordinatorBridge) adaptCoordinatorHookStatus(
	ctx context.Context,
	status jobStatus,
	statusErr error,
	expectedTree string,
) (gatehook.JobStatus, error) {
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
		if err := bridge.attachPassedResultReceipt(ctx, status, expectedTree, &adapted); err != nil {
			return adapted, err
		}
	}
	if err := adapted.Validate(); err != nil {
		return adapted, fmt.Errorf("validate coordinator hook status: %w", err)
	}
	return adapted, nil
}

// attachPassedResultReceipt 从 coordinator 拉取、验签并绑定 passed receipt。
func (bridge *hookCoordinatorBridge) attachPassedResultReceipt(
	ctx context.Context,
	status jobStatus,
	expectedTree string,
	adapted *gatehook.JobStatus,
) error {
	if bridge == nil || bridge.client == nil || bridge.authority == nil {
		return fmt.Errorf("%w: receipt verification dependency is missing", errHookReceiptInvalid)
	}
	receipt, err := bridge.client.ResultReceipt(ctx, status.JobID)
	if err != nil {
		return fmt.Errorf("%w: read receipt: %v", errHookReceiptInvalid, err)
	}
	if err := bridge.authority.VerifyCurrentResultReceipt(ctx, receipt); err != nil {
		return fmt.Errorf("%w: %v", errHookReceiptInvalid, err)
	}
	if !resultReceiptMatchesHookStatus(receipt, status, expectedTree) {
		return fmt.Errorf("%w: receipt identity does not match current job and source", errHookReceiptInvalid)
	}
	adapted.ReceiptID = receipt.ReceiptID
	return nil
}

// resultReceiptMatchesHookStatus 比较 receipt 与当前 job、invocation 和 tree 绑定。
func resultReceiptMatchesHookStatus(
	receipt gatecontract.ResultReceipt,
	status jobStatus,
	expectedTree string,
) bool {
	return status.ReceiptID == receipt.ReceiptID &&
		receipt.ReceiptID == resultReceiptID(status.JobID) &&
		receipt.SchemaVersion == gatecontract.ResultReceiptSchemaVersion &&
		len(receipt.ShardReceipts) == gatecontract.MaxContainerShards &&
		receipt.InvocationID == status.InvocationID &&
		receipt.Source.SourceTreeSHA == status.JobSourceTreeSHA &&
		receipt.Source.SourceTreeSHA == expectedTree &&
		receipt.Status == gatecontract.ResultStatusPassed
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
