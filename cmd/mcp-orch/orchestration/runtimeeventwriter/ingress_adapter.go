package runtimeeventwriter

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/taskeventsource"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
)

var (
	// ErrIngressAdapterInvalid marks a missing or malformed candidate seam.
	ErrIngressAdapterInvalid = errors.New("runtime event ingress adapter is invalid")
	// ErrIngressAdapterCommitUnknown marks an append whose commit outcome is not proven.
	ErrIngressAdapterCommitUnknown = errors.New("runtime event ingress adapter commit outcome is unknown")
	// ErrIngressAdapterFenced marks an adapter that refuses replay after an unknown commit.
	ErrIngressAdapterFenced = errors.New("runtime event ingress adapter is fenced")
	// ErrIngressAdapterNotify marks a committed append whose local wake-up failed.
	ErrIngressAdapterNotify = errors.New("runtime event ingress adapter post-commit notification failed")
)

// TaskIngress is the narrow task-event candidate port implemented by
// taskeventsource.RuntimeEventTxCandidate. It carries no provider or transport
// operation and cannot be used to allocate identity or sequence independently.
type TaskIngress interface {
	Append(context.Context, taskeventsource.RuntimeEvent) (eventstore.AppendResult, error)
}

// CandidateIngressAdapter adds a post-commit notification seam to an existing
// runtime writer. The wrapped ingress remains responsible for identity/source
// fence checks, owner-derived sequence allocation, dense gap handling, and
// commit-unknown fencing. This adapter never opens a transaction or writes a
// second event owner.
type CandidateIngressAdapter struct {
	runtime  Ingress
	task     TaskIngress
	notifier eventstore.EventNotifier

	mu     sync.Mutex
	fenced bool
}

// AtomicBindingAvailable 在不探测或开启事务的前提下报告底层 ingress 是否具备
// transaction-bound binding seam；缺少该 seam 时上层必须 fail-closed。
func (a *CandidateIngressAdapter) AtomicBindingAvailable() bool {
	if a == nil || a.runtime == nil {
		return false
	}
	bound, ok := a.runtime.(BoundIngress)
	return ok && !isNilIngressAdapterDependency(bound)
}

var _ Ingress = (*CandidateIngressAdapter)(nil)
var _ TaskIngress = (*taskeventsource.RuntimeEventTxCandidate)(nil)

// NewCandidateIngressAdapter 将 runtimeeventwriter.Writer（或其他窄 Ingress）与显式提交后通知器连接；缺失或 typed-nil 依赖在追加前失败。
func NewCandidateIngressAdapter(ingress Ingress, notifier eventstore.EventNotifier) (*CandidateIngressAdapter, error) {
	if isNilIngressAdapterDependency(ingress) || isNilIngressAdapterDependency(notifier) {
		return nil, fmt.Errorf("%w: runtime ingress and notifier are required", ErrIngressAdapterInvalid)
	}
	return &CandidateIngressAdapter{runtime: ingress, notifier: notifier}, nil
}

// NewTaskCandidateIngressAdapter 将事务绑定 task candidate 接入同一提交后通知规则；该适配器保持 candidate-only，不注册 Fx、provider 或公共工具路径。
func NewTaskCandidateIngressAdapter(ingress TaskIngress, notifier eventstore.EventNotifier) (*CandidateIngressAdapter, error) {
	if isNilIngressAdapterDependency(ingress) || isNilIngressAdapterDependency(notifier) {
		return nil, fmt.Errorf("%w: task ingress and notifier are required", ErrIngressAdapterInvalid)
	}
	return &CandidateIngressAdapter{task: ingress, notifier: notifier}, nil
}

// Append 委托 runtime 事件，并且仅在下层证明 materialized 且提交成功后通知；gap、stale、回滚和未知提交结果均不发出唤醒。
func (a *CandidateIngressAdapter) Append(ctx context.Context, event Event) (eventstore.AppendResult, error) {
	if err := a.validate(ctx); err != nil {
		return eventstore.AppendResult{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fenced {
		return eventstore.AppendResult{}, errors.Join(ErrIngressAdapterFenced, ErrIngressAdapterCommitUnknown)
	}
	if a.runtime == nil {
		return eventstore.AppendResult{}, fmt.Errorf("%w: runtime ingress is unavailable", ErrIngressAdapterInvalid)
	}
	result, err := a.runtime.Append(ctx, event)
	return a.finish(result, err)
}

// AppendBound 将完整 binding 校验与 canonical append 委托给同一事务 owner；
// 仅支持 context-only 校验的底层 ingress 会被拒绝，避免重新引入 TOCTOU 窗口。
func (a *CandidateIngressAdapter) AppendBound(ctx context.Context, event Event, verifier BindingVerifierTx) (eventstore.AppendResult, error) {
	if err := a.validate(ctx); err != nil {
		return eventstore.AppendResult{}, err
	}
	if isNilIngressAdapterDependency(verifier) {
		return eventstore.AppendResult{}, fmt.Errorf("%w: transactional binding verifier is required", ErrIngressAdapterInvalid)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fenced {
		return eventstore.AppendResult{}, errors.Join(ErrIngressAdapterFenced, ErrIngressAdapterCommitUnknown)
	}
	bound, ok := a.runtime.(BoundIngress)
	if !ok || isNilIngressAdapterDependency(bound) {
		return eventstore.AppendResult{}, fmt.Errorf("%w: wrapped ingress has no atomic binding seam", ErrIngressAdapterInvalid)
	}
	result, err := bound.AppendBound(ctx, event, verifier)
	return a.finish(result, err)
}

// AppendTask 是 taskeventsource runtime observation 的等价入口；它保留 task candidate 的 identity/source 校验并应用同一提交后通知屏障。
func (a *CandidateIngressAdapter) AppendTask(ctx context.Context, event taskeventsource.RuntimeEvent) (eventstore.AppendResult, error) {
	if err := a.validate(ctx); err != nil {
		return eventstore.AppendResult{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fenced {
		return eventstore.AppendResult{}, errors.Join(ErrIngressAdapterFenced, ErrIngressAdapterCommitUnknown)
	}
	if a.task == nil {
		return eventstore.AppendResult{}, fmt.Errorf("%w: task ingress is unavailable", ErrIngressAdapterInvalid)
	}
	result, err := a.task.Append(ctx, event)
	return a.finish(result, err)
}

func (a *CandidateIngressAdapter) validate(ctx context.Context) error {
	if isNilIngressAdapterDependency(a) || isNilIngressAdapterDependency(ctx) || isNilIngressAdapterDependency(a.notifier) {
		return fmt.Errorf("%w: adapter, context, or notifier is missing", ErrIngressAdapterInvalid)
	}
	if (a.runtime == nil) == (a.task == nil) {
		return fmt.Errorf("%w: exactly one ingress port is required", ErrIngressAdapterInvalid)
	}
	return nil
}

// finish 统一处理未知提交围栏、gap/stale 拒绝和提交后的 bounded 通知结果。
func (a *CandidateIngressAdapter) finish(result eventstore.AppendResult, err error) (eventstore.AppendResult, error) {
	if isUnknownIngressAdapterError(err) {
		a.fenced = true
		return eventstore.AppendResult{}, errors.Join(ErrIngressAdapterCommitUnknown, err)
	}
	if err != nil {
		return eventstore.AppendResult{}, err
	}
	if err := validateAdapterResult(result); err != nil {
		return eventstore.AppendResult{}, err
	}
	if err := a.notifier.Notify(*result.Event); err != nil {
		// Canonical data is already committed. Preserve the result so callers can
		// distinguish a wake-up failure from a rolled-back append.
		return result, errors.Join(ErrIngressAdapterNotify, err)
	}
	return result, nil
}

// validateAdapterResult 防止任意伪造 ingress 通过 adapter 冒充 canonical materialization。
func validateAdapterResult(result eventstore.AppendResult) error {
	if err := validateMaterializedHeaders(result); err != nil {
		return err
	}
	if err := validateReceiptEventFrame(result); err != nil {
		return err
	}
	if err := validateReceiptFrameKey(result.Receipt); err != nil {
		return err
	}
	return validateReceiptState(result.Receipt, result.State)
}

// ValidateMaterializedResult 证明 receipt、event、frame、cursor 与 source
// watermark 共同描述一次已 materialized 的 canonical append。
func ValidateMaterializedResult(result eventstore.AppendResult) error {
	return validateAdapterResult(result)
}

// validateMaterializedHeaders 要求 receipt 与 event 都明确标记 materialized。
func validateMaterializedHeaders(result eventstore.AppendResult) error {
	if result.Event == nil || result.Event.Status != "materialized" || result.Receipt.Status != "materialized" {
		return fmt.Errorf("%w: successful append is not materialized", ErrIngressAdapterInvalid)
	}
	return nil
}

// validateReceiptEventFrame 比较 receipt 与 materialized event 的完整帧身份。
func validateReceiptEventFrame(result eventstore.AppendResult) error {
	receipt, event := result.Receipt, result.Event
	if receipt.SourceID != event.SourceID || receipt.SourceEpoch != event.SourceEpoch || receipt.Lane != event.Lane || receipt.SourceSequence != event.SourceSequence || receipt.CanonicalCursor != event.CanonicalCursor || receipt.EnvelopeHash != event.EnvelopeHash || !reflect.DeepEqual(receipt.Envelope, event.Envelope) {
		return fmt.Errorf("%w: receipt and event frames do not match", ErrIngressAdapterInvalid)
	}
	return nil
}

// validateReceiptFrameKey 校验 materialized frame 的 source tuple 与 cursor。
func validateReceiptFrameKey(receipt eventstore.IngressReceipt) error {
	if receipt.SourceSequence < 1 || receipt.SourceID == "" || receipt.SourceEpoch == "" || receipt.Lane == "" || receipt.CanonicalCursor == "" || receipt.EnvelopeHash == "" {
		return fmt.Errorf("%w: materialized frame is incomplete", ErrIngressAdapterInvalid)
	}
	expectedCursor, err := eventstore.CanonicalCursor(receipt.SourceID, receipt.SourceEpoch, receipt.Lane, receipt.SourceSequence)
	if err != nil || expectedCursor != receipt.CanonicalCursor {
		return fmt.Errorf("%w: materialized frame cursor is invalid", ErrIngressAdapterInvalid)
	}
	return validateReceiptCanonicalHash(receipt)
}

func validateReceiptCanonicalHash(receipt eventstore.IngressReceipt) error {
	expectedHash, err := eventstore.CanonicalEnvelopeHash(receipt.Envelope)
	if err != nil || expectedHash != receipt.EnvelopeHash {
		return fmt.Errorf("%w: materialized frame envelope hash is not canonical", ErrIngressAdapterInvalid)
	}
	return nil
}

// validateReceiptState 确认 materialized event 对应 active source watermark。
func validateReceiptState(receipt eventstore.IngressReceipt, state eventstore.SourceState) error {
	if !stateMatchesReceipt(receipt, state) {
		return fmt.Errorf("%w: materialized source state is not bound", ErrIngressAdapterInvalid)
	}
	if state.DedupFloor < 0 || state.DedupFloor > state.MaterializedSequence {
		return fmt.Errorf("%w: materialized source dedup floor is invalid", ErrIngressAdapterInvalid)
	}
	if receipt.Envelope.WriterEpoch != "" && state.WriterEpoch != receipt.Envelope.WriterEpoch {
		return fmt.Errorf("%w: materialized writer epoch is not bound", ErrIngressAdapterInvalid)
	}
	return nil
}

// stateMatchesReceipt 校验 source/lane 水位与 materialized receipt 的一致性。
func stateMatchesReceipt(receipt eventstore.IngressReceipt, state eventstore.SourceState) bool {
	return state.SourceID == receipt.SourceID && state.SourceEpoch == receipt.SourceEpoch && state.Lane == receipt.Lane && state.Status == "active" && state.MaterializedSequence >= receipt.SourceSequence && state.NextSequence > state.MaterializedSequence && state.Revision >= 1
}

func isUnknownIngressAdapterError(err error) bool {
	return errors.Is(err, ErrCommitUnknown) || errors.Is(err, eventstore.ErrTransactionCommitUnknown) ||
		errors.Is(err, taskeventsource.ErrRuntimeEventTxCandidateCommitUnknown)
}

func isNilIngressAdapterDependency(value any) bool {
	if value == nil {
		return true
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	default:
		return false
	}
}
