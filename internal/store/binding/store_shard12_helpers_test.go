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

func (s *bindingQuerierStub) RebindAgentThreadTx(ctx context.Context, arg sqlc.RebindAgentThreadTxParams) error {
	if s.rebindAgentThreadTxFn != nil {
		return s.rebindAgentThreadTxFn(ctx, arg)
	}
	return nil
}

func (s *bindingQuerierStub) BindAgentThread(ctx context.Context, arg sqlc.BindAgentThreadParams) error {
	if s.bindAgentThreadFn != nil {
		return s.bindAgentThreadFn(ctx, arg)
	}
	return nil
}

func (s *bindingQuerierStub) DeleteAgentProviderBindingByAgentID(ctx context.Context, arg sqlc.DeleteAgentProviderBindingByAgentIDParams) error {
	if s.deleteAgentProviderBindingByIDFn != nil {
		return s.deleteAgentProviderBindingByIDFn(ctx, arg.AgentID)
	}
	return nil
}

func (s *bindingQuerierStub) GetAgentProviderBindingByAgentID(ctx context.Context, arg sqlc.GetAgentProviderBindingByAgentIDParams) (sqlc.AgentProviderBinding, error) {
	if s.getAgentProviderBindingByAgentIDFn != nil {
		return s.getAgentProviderBindingByAgentIDFn(ctx, arg.AgentID)
	}
	return sqlc.AgentProviderBinding{}, nil
}

func (s *bindingQuerierStub) GetAgentProviderBindingByProviderThread(ctx context.Context, arg sqlc.GetAgentProviderBindingByProviderThreadParams) (sqlc.AgentProviderBinding, error) {
	if s.getByProviderThreadFn != nil {
		return s.getByProviderThreadFn(ctx, arg)
	}
	return sqlc.AgentProviderBinding{}, nil
}

func (s *bindingQuerierStub) GetThreadByAgent(ctx context.Context, arg sqlc.GetThreadByAgentParams) (string, error) {
	if s.getThreadByAgentFn != nil {
		return s.getThreadByAgentFn(ctx, arg.AgentID)
	}
	return "", nil
}

func (s *bindingQuerierStub) ListAgentThreadBindings(ctx context.Context) ([]sqlc.AgentProviderBinding, error) {
	if s.listAgentThreadBindingsFn != nil {
		return s.listAgentThreadBindingsFn(ctx)
	}
	return nil, nil
}

func (s *bindingQuerierStub) UnbindAgentThread(ctx context.Context, arg sqlc.UnbindAgentThreadParams) error {
	if s.unbindAgentThreadFn != nil {
		return s.unbindAgentThreadFn(ctx, arg.AgentID)
	}
	return nil
}

func (s *bindingQuerierStub) UpdateAgentCwd(ctx context.Context, arg sqlc.UpdateAgentCwdParams) error {
	if s.updateAgentCwdFn != nil {
		return s.updateAgentCwdFn(ctx, arg)
	}
	return nil
}

func (s *bindingQuerierStub) UpdateAgentProviderBindingArchived(ctx context.Context, arg sqlc.UpdateAgentProviderBindingArchivedParams) error {
	if s.updateArchivedFn != nil {
		return s.updateArchivedFn(ctx, arg)
	}
	return nil
}

func (s *bindingQuerierStub) UpdateAgentProviderBindingProviderThreadID(ctx context.Context, arg sqlc.UpdateAgentProviderBindingProviderThreadIDParams) error {
	if s.updateProviderThreadIDFn != nil {
		return s.updateProviderThreadIDFn(ctx, arg)
	}
	return nil
}

func (s *bindingQuerierStub) UpdateAgentProviderBindingSessionUUID(ctx context.Context, arg sqlc.UpdateAgentProviderBindingSessionUUIDParams) error {
	if s.updateSessionUUIDFn != nil {
		return s.updateSessionUUIDFn(ctx, arg)
	}
	return nil
}

func (s *bindingQuerierStub) UpsertAgentProviderBinding(ctx context.Context, arg sqlc.UpsertAgentProviderBindingParams) error {
	if s.upsertAgentProviderBindingFn != nil {
		return s.upsertAgentProviderBindingFn(ctx, arg)
	}
	return nil
}
