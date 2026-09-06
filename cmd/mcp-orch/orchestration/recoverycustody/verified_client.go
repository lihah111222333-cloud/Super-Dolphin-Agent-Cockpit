package recoverycustody

import (
	"context"
	"errors"
	"fmt"
	"reflect"
)

// VerifiedClient 是 authority transport 可以信任的 Recovery Custody client。
// 未导出的 capability marker 防止其他 package 用结构上相同的方法伪造该能力。
type VerifiedClient interface {
	Client
	isVerifiedClient() bool
}

type verifiedClient struct {
	client   Client
	verifier ReceiptVerifier
}

// NewVerifiedClient 包装 authority client，并在暴露 terminal receipt 前执行外部验证。
// verifier 必须是实际 authority-owned verifier；本函数不提供默认或测试替身。
func NewVerifiedClient(client Client, verifier ReceiptVerifier) (VerifiedClient, error) {
	if isNilDependency(client) {
		return nil, errors.New("recovery custody verified client requires a backend")
	}
	if isNilDependency(verifier) {
		return nil, errors.New("recovery custody verified client requires a receipt verifier")
	}
	return &verifiedClient{client: client, verifier: verifier}, nil
}

func (*verifiedClient) isVerifiedClient() bool { return true }

// Submit 验证底层 client 的返回形状，并在 terminal receipt 上执行 verifier。
func (c *verifiedClient) Submit(ctx context.Context, request SubmissionRequest) (Outcome, error) {
	if ctx == nil {
		return Outcome{}, ErrContextRequired
	}
	handle, err := NewSubmissionHandle(request)
	if err != nil {
		return Outcome{}, err
	}
	return c.call(ctx, handle, func() (Outcome, error) { return c.client.Submit(ctx, request) })
}

// Query 验证底层 client 的返回形状，并在 terminal receipt 上执行 verifier。
func (c *verifiedClient) Query(ctx context.Context, handle SubmissionHandle) (Outcome, error) {
	if ctx == nil {
		return Outcome{}, ErrContextRequired
	}
	if err := handle.Validate(); err != nil {
		return Outcome{}, err
	}
	return c.call(ctx, handle, func() (Outcome, error) { return c.client.Query(ctx, handle) })
}

// call 统一收紧底层结果，并把未通过验证的终态折叠为不可用。
func (c *verifiedClient) call(ctx context.Context, handle SubmissionHandle, invoke func() (Outcome, error)) (Outcome, error) {
	if ctx == nil {
		return Outcome{}, ErrContextRequired
	}
	outcome, backendErr := invoke()
	if err := ctx.Err(); err != nil {
		return Outcome{}, err
	}
	if backendErr != nil {
		return c.backendErrorOutcome(handle, outcome, backendErr)
	}
	if outcome.Handle != handle {
		if hasTerminalClaim(outcome) {
			return unavailableOutcome(handle), ErrCustodyUnavailable
		}
		return Outcome{}, ErrCustodyUnavailable
	}
	if err := outcome.Validate(); err != nil {
		if hasTerminalClaim(outcome) {
			return unavailableOutcome(handle), ErrCustodyUnavailable
		}
		return Outcome{}, fmt.Errorf("%w: invalid backend outcome", ErrCustodyUnavailable)
	}
	if outcome.Status != StatusAvailable && outcome.Status != StatusAborted {
		return outcome, nil
	}
	return c.verifyTerminal(ctx, handle, outcome)
}

func (c *verifiedClient) backendErrorOutcome(handle SubmissionHandle, outcome Outcome, backendErr error) (Outcome, error) {
	if hasTerminalClaim(outcome) {
		return unavailableOutcome(handle), ErrCustodyUnavailable
	}
	return outcome, backendErr
}

func hasTerminalClaim(outcome Outcome) bool {
	return outcome.Receipt != nil || outcome.Status == StatusAvailable || outcome.Status == StatusAborted
}

func (c *verifiedClient) verifyTerminal(ctx context.Context, handle SubmissionHandle, outcome Outcome) (Outcome, error) {
	verified, err := ApplyVerifiedReceipt(ctx, Outcome{Handle: handle, Status: StatusPending}, *outcome.Receipt, c.verifier)
	if err != nil {
		return unavailableOutcome(handle), fmt.Errorf("%w: terminal receipt verification failed", ErrCustodyUnavailable)
	}
	return verified, nil
}

func unavailableOutcome(handle SubmissionHandle) Outcome {
	return Outcome{Handle: handle, Status: StatusIndeterminate, ReasonCode: "recovery_custody_unavailable"}
}

func isNilDependency(value any) bool {
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

var _ VerifiedClient = (*verifiedClient)(nil)
