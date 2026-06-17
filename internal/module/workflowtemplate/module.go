package workflowtemplate

import (
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared/workflowtemplates"
	"go.uber.org/fx"
)

// Service 提供只读的内置政企工作流模板能力。
type Service interface {
	ListTemplates(filter workflowtemplates.ListFilter) []workflowtemplates.TemplateSummary
	GetTemplate(id string) (workflowtemplates.Template, bool)
	RenderDAGDraft(req workflowtemplates.RenderRequest) (workflowtemplates.DAGDraft, error)
}

type service struct {
	registry *workflowtemplates.Registry
}

// NewService 加载仓库内置模板资产，不依赖数据库或外部执行器。
func NewService() (Service, error) {
	registry, err := workflowtemplates.NewDefaultRegistry()
	if err != nil {
		return nil, err
	}
	return &service{registry: registry}, nil
}

// ListTemplates 返回模板摘要列表，并按前端目录筛选条件缩小结果。
func (s *service) ListTemplates(filter workflowtemplates.ListFilter) []workflowtemplates.TemplateSummary {
	return s.registry.ListTemplates(filter)
}

// GetTemplate 返回模板完整定义。
func (s *service) GetTemplate(id string) (workflowtemplates.Template, bool) {
	return s.registry.GetTemplate(id)
}

// RenderDAGDraft 根据用户参数渲染 DAG 草案但不落库。
func (s *service) RenderDAGDraft(req workflowtemplates.RenderRequest) (workflowtemplates.DAGDraft, error) {
	return s.registry.RenderDAGDraft(req)
}

var Module = fx.Module("workflowtemplate",
	fx.Provide(NewService),
	fx.Provide(NewHandlers),
)
