package uistate

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// projectStateFacadeAdapter wraps a uistate.Service so the module can
// supply the contract.UIProjectStateFacade without leaking any of its
// other, uistate-private methods (GetState, GetSidebar, Preferences,
// ...) across the module boundary.
//
// P22 P4 S1b: ui/wails depends on contract.UIProjectStateFacade instead
// of uistate.Service; this adapter is the seam.
type projectStateFacadeAdapter struct {
	svc Service
}

// GetProjects satisfies contract.UIProjectStateFacade. It forwards to
// the underlying Service and converts the internal *ProjectsState into
// the contract-level *contract.ProjectsSnapshot. A nil service returns
// (nil, nil) so optional DI wiring stays a cheap no-op.
// GetProjects 读取projects。
func (a projectStateFacadeAdapter) GetProjects(ctx context.Context) (*contract.ProjectsSnapshot, error) {
	if a.svc == nil {
		return nil, nil
	}
	state, err := a.svc.GetProjects(ctx)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, nil
	}
	return &contract.ProjectsSnapshot{
		Projects: state.Projects,
		Active:   state.Active,
	}, nil
}

// NewProjectStateFacade constructs the contract.UIProjectStateFacade
// implementation backed by the uistate Service. Exposed via fx.Provide
// in Module so other packages can consume the facade without importing
// this one.
// NewProjectStateFacade 创建项目状态facade。
func NewProjectStateFacade(svc Service) contract.UIProjectStateFacade {
	return projectStateFacadeAdapter{svc: svc}
}
