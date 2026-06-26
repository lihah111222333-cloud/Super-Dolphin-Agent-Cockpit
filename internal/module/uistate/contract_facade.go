package uistate

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// projectStateFacadeAdapter 将 uistate.Service 收窄为 contract.UIProjectStateFacade。
// 这样其他模块只能读取项目快照，不能调用 uistate 私有的状态和偏好方法。
type projectStateFacadeAdapter struct {
	svc Service
}

// GetProjects 读取项目快照并转换到 contract 层 DTO；nil service 在 optional DI 中返回空结果。
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

// NewProjectStateFacade 创建 contract.UIProjectStateFacade，避免外部模块直接依赖 uistate.Service。
func NewProjectStateFacade(svc Service) contract.UIProjectStateFacade {
	return projectStateFacadeAdapter{svc: svc}
}
