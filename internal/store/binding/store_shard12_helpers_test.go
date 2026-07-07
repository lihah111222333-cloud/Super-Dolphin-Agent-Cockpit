package binding

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type bindingQuerierStub struct {
	bindAgentThreadFn                  func(context.Context, sqlc.BindAgentThreadParams) error
	deleteAgentProviderBindingByIDFn   func(context.Context, string) error
	getAgentProviderBindingByAgentIDFn func(context.Context, string) (sqlc.AgentProviderBinding, error)
	getByProviderThreadFn              func(context.Context, sqlc.GetAgentProviderBindingByProviderThreadParams) (sqlc.AgentProviderBinding, error)
	getThreadByAgentFn                 func(context.Context, string) (string, error)
	listAgentThreadBindingsFn          func(context.Context) ([]sqlc.AgentProviderBinding, error)
	unbindAgentThreadFn                func(context.Context, string) error
	updateAgentCwdFn                   func(context.Context, sqlc.UpdateAgentCwdParams) error
	updateArchivedFn                   func(context.Context, sqlc.UpdateAgentProviderBindingArchivedParams) error
	updateProviderThreadIDFn           func(context.Context, sqlc.UpdateAgentProviderBindingProviderThreadIDParams) error
	updateSessionUUIDFn                func(context.Context, sqlc.UpdateAgentProviderBindingSessionUUIDParams) error
	upsertAgentProviderBindingFn       func(context.Context, sqlc.UpsertAgentProviderBindingParams) error
	rebindAgentThreadTxFn              func(context.Context, sqlc.RebindAgentThreadTxParams) error
}

type bindingQuerierAdapter struct {
	bindingThreadQuerier
	bindingProviderQuerier
	bindingMutationQuerier
}

func newBindingQuerierTestAdapter(stub *bindingQuerierStub) *bindingQuerierAdapter {
	return &bindingQuerierAdapter{
		bindingThreadQuerier:   bindingThreadQuerier{stub: stub},
		bindingProviderQuerier: bindingProviderQuerier{stub: stub},
		bindingMutationQuerier: bindingMutationQuerier{stub: stub},
	}
}

type bindingThreadQuerier struct {
	stub *bindingQuerierStub
}

func (q bindingThreadQuerier) RebindAgentThreadTx(ctx context.Context, arg sqlc.RebindAgentThreadTxParams) error {
	if q.stub.rebindAgentThreadTxFn != nil {
		return q.stub.rebindAgentThreadTxFn(ctx, arg)
	}
	return nil
}

func (q bindingThreadQuerier) BindAgentThread(ctx context.Context, arg sqlc.BindAgentThreadParams) error {
	if q.stub.bindAgentThreadFn != nil {
		return q.stub.bindAgentThreadFn(ctx, arg)
	}
	return nil
}

func (q bindingThreadQuerier) GetThreadByAgent(ctx context.Context, arg sqlc.GetThreadByAgentParams) (string, error) {
	if q.stub.getThreadByAgentFn != nil {
		return q.stub.getThreadByAgentFn(ctx, arg.AgentID)
	}
	return "", nil
}

func (q bindingThreadQuerier) ListAgentThreadBindings(ctx context.Context) ([]sqlc.AgentProviderBinding, error) {
	if q.stub.listAgentThreadBindingsFn != nil {
		return q.stub.listAgentThreadBindingsFn(ctx)
	}
	return nil, nil
}

func (q bindingThreadQuerier) UnbindAgentThread(ctx context.Context, arg sqlc.UnbindAgentThreadParams) error {
	if q.stub.unbindAgentThreadFn != nil {
		return q.stub.unbindAgentThreadFn(ctx, arg.AgentID)
	}
	return nil
}

type bindingProviderQuerier struct {
	stub *bindingQuerierStub
}

func (q bindingProviderQuerier) DeleteAgentProviderBindingByAgentID(ctx context.Context, arg sqlc.DeleteAgentProviderBindingByAgentIDParams) error {
	if q.stub.deleteAgentProviderBindingByIDFn != nil {
		return q.stub.deleteAgentProviderBindingByIDFn(ctx, arg.AgentID)
	}
	return nil
}

func (q bindingProviderQuerier) GetAgentProviderBindingByAgentID(ctx context.Context, arg sqlc.GetAgentProviderBindingByAgentIDParams) (sqlc.AgentProviderBinding, error) {
	if q.stub.getAgentProviderBindingByAgentIDFn != nil {
		return q.stub.getAgentProviderBindingByAgentIDFn(ctx, arg.AgentID)
	}
	return sqlc.AgentProviderBinding{}, nil
}

func (q bindingProviderQuerier) GetAgentProviderBindingByProviderThread(ctx context.Context, arg sqlc.GetAgentProviderBindingByProviderThreadParams) (sqlc.AgentProviderBinding, error) {
	if q.stub.getByProviderThreadFn != nil {
		return q.stub.getByProviderThreadFn(ctx, arg)
	}
	return sqlc.AgentProviderBinding{}, nil
}

func (q bindingProviderQuerier) UpdateAgentProviderBindingArchived(ctx context.Context, arg sqlc.UpdateAgentProviderBindingArchivedParams) error {
	if q.stub.updateArchivedFn != nil {
		return q.stub.updateArchivedFn(ctx, arg)
	}
	return nil
}

type bindingMutationQuerier struct {
	stub *bindingQuerierStub
}

func (q bindingMutationQuerier) UpdateAgentCwd(ctx context.Context, arg sqlc.UpdateAgentCwdParams) error {
	if q.stub.updateAgentCwdFn != nil {
		return q.stub.updateAgentCwdFn(ctx, arg)
	}
	return nil
}

func (q bindingMutationQuerier) UpdateAgentProviderBindingProviderThreadID(ctx context.Context, arg sqlc.UpdateAgentProviderBindingProviderThreadIDParams) error {
	if q.stub.updateProviderThreadIDFn != nil {
		return q.stub.updateProviderThreadIDFn(ctx, arg)
	}
	return nil
}

func (q bindingMutationQuerier) UpdateAgentProviderBindingSessionUUID(ctx context.Context, arg sqlc.UpdateAgentProviderBindingSessionUUIDParams) error {
	if q.stub.updateSessionUUIDFn != nil {
		return q.stub.updateSessionUUIDFn(ctx, arg)
	}
	return nil
}

func (q bindingMutationQuerier) UpsertAgentProviderBinding(ctx context.Context, arg sqlc.UpsertAgentProviderBindingParams) error {
	if q.stub.upsertAgentProviderBindingFn != nil {
		return q.stub.upsertAgentProviderBindingFn(ctx, arg)
	}
	return nil
}
