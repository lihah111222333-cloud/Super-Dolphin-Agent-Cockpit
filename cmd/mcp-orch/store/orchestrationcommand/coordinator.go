package orchestrationcommand

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
)

// Coordinator 先验证已有回执，再以日志版本CAS追加事实；不持有任何provider执行器。
type Coordinator struct {
	store    *Store
	verifier ReceiptVerifier
}

// NewCoordinator 缺少明确回执验证器时立即拒绝；没有默认成功实现。
func NewCoordinator(store *Store, verifier ReceiptVerifier) (*Coordinator, error) {
	if store == nil || store.queries == nil || verifierIsNil(verifier) {
		return nil, errors.New("command coordinator requires store and receipt verifier")
	}
	return &Coordinator{store: store, verifier: verifier}, nil
}

func verifierIsNil(verifier ReceiptVerifier) bool {
	if verifier == nil {
		return true
	}
	value := reflect.ValueOf(verifier)
	switch value.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Func, reflect.Slice, reflect.Chan:
		return value.IsNil()
	default:
		return false
	}
}

// Prepare 显式创建未接受状态；不存在生命周期记录时不能以缺失数据猜测pending。
func (c *Coordinator) Prepare(ctx context.Context, commandID, operationID string) (Snapshot, error) {
	if err := c.ready(ctx); err != nil {
		return Snapshot{}, err
	}
	if err := validateText("command_id", commandID, 128); err != nil {
		return Snapshot{}, err
	}
	if err := validateText("operation_id", operationID, 128); err != nil {
		return Snapshot{}, err
	}
	if _, err := c.store.Get(ctx, commandID); err != nil {
		return Snapshot{}, err
	}
	request := TransitionRequest{CommandID: commandID, OperationID: operationID, Trigger: TriggerPrepare}
	digest, err := transitionDigest(request)
	if err != nil {
		return Snapshot{}, err
	}
	return c.store.prepareLifecycle(ctx, request, digest)
}

// Apply 同一操作重放返回其原始历史结果；未知或过期CAS不会盲目重发或新建attempt。
func (c *Coordinator) Apply(ctx context.Context, r TransitionRequest) (Snapshot, error) {
	if err := c.ready(ctx); err != nil {
		return Snapshot{}, err
	}
	if err := validateTransitionRequest(r); err != nil {
		return Snapshot{}, errors.Join(ErrInvalidTransition, err)
	}
	digest, err := transitionDigest(r)
	if err != nil {
		return Snapshot{}, err
	}
	prior, err := c.store.GetOperation(ctx, r.CommandID, r.OperationID)
	if err == nil {
		return matchOperation(prior, digest)
	}
	if !errors.Is(err, ErrLifecycleNotFound) {
		return Snapshot{}, err
	}
	return c.applyUnrecordedOperation(ctx, r, digest)
}

// applyUnrecordedOperation 校验尚未观察到结果的操作；预读之后的竞争仍由原操作查询和 CAS 裁决。
func (c *Coordinator) applyUnrecordedOperation(ctx context.Context, r TransitionRequest, digest string) (Snapshot, error) {
	current, err := c.store.GetState(ctx, r.CommandID)
	if err != nil {
		return Snapshot{}, err
	}
	plan, err := planTransition(ctx, current, r, digest)
	if err != nil {
		if errors.Is(err, ErrTransitionConflict) {
			return c.store.resolveLifecycleConflict(ctx, r.CommandID, r.OperationID, digest)
		}
		return Snapshot{}, err
	}
	intent, err := c.store.Get(ctx, r.CommandID)
	if err != nil {
		return Snapshot{}, err
	}
	if err := c.verifier.VerifyCommandTransition(ctx, Verification{Intent: intent, Current: current, Request: r, NextPhase: plan.NextPhase}); err != nil {
		return Snapshot{}, errors.Join(ErrEvidenceRejected, err)
	}
	return c.store.appendLifecycle(ctx, plan)
}

func (c *Coordinator) ready(ctx context.Context) error {
	if c == nil || c.store == nil || verifierIsNil(c.verifier) {
		return errors.New("command coordinator dependencies are missing")
	}
	return c.store.ready(ctx)
}

func transitionDigest(r TransitionRequest) (string, error) {
	raw, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(append([]byte("orch.command.transition/v1\n"), raw...))
	return hex.EncodeToString(digest[:]), nil
}

func matchOperation(prior Snapshot, digest string) (Snapshot, error) {
	if prior.OperationDigest != digest {
		return Snapshot{}, ErrOperationConflict
	}
	return prior, nil
}
