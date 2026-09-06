package orchestrationcommand

import (
	"context"
	"errors"
	"fmt"
)

// ErrProviderSubmitterRequired 表示执行网关未收到显式 typed provider seam。
var ErrProviderSubmitterRequired = errors.New("provider submitter is required")

// Execute 先消费一个 durable permit，再调用 typed provider；任一调用错误都保持 sticky unknown，不能在此重试。
func (g *CommandAttemptGateway) Execute(ctx context.Context, permit DispatchPermit, submitter ProviderSubmitter) (Attempt, error) {
	if g == nil || g.store == nil {
		return Attempt{}, ErrAttemptNotFound
	}
	if isNilDependency(submitter) {
		return Attempt{}, ErrProviderSubmitterRequired
	}
	consumed, err := g.Consume(ctx, permit)
	if err != nil {
		return Attempt{}, err
	}
	outcome, submitErr := callProviderSubmit(ctx, permit, submitter)
	if submitErr != nil {
		return consumed, errors.Join(ErrAttemptUnresolved, submitErr)
	}
	if outcome.Kind == ProviderResultPending || outcome.Kind == ProviderResultUnknown {
		return consumed, fmt.Errorf("%w: provider outcome remains unresolved", ErrAttemptUnresolved)
	}
	resolved, err := g.store.ReconcileUnknown(ctx, ProviderResultRequest{DispatchPermit: permit, Result: outcome})
	if err != nil {
		return consumed, errors.Join(ErrAttemptUnresolved, err)
	}
	return resolved, nil
}

// callProviderSubmit 将 provider panic 折叠为 sticky unknown，避免泄露 panic 内容或触发重试。
func callProviderSubmit(ctx context.Context, permit DispatchPermit, submitter ProviderSubmitter) (outcome ProviderResult, err error) {
	defer func() {
		if recover() != nil {
			outcome = ProviderResult{Kind: ProviderResultUnknown}
			err = ErrAttemptUnresolved
		}
	}()
	return submitter.Submit(ctx, permit)
}

// ExecuteProvider 是 Execute 的语义别名，保持 provider boundary 调用方可读性。
func (g *CommandAttemptGateway) ExecuteProvider(ctx context.Context, permit DispatchPermit, submitter ProviderSubmitter) (Attempt, error) {
	return g.Execute(ctx, permit, submitter)
}
