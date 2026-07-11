package workflowtemplate

import (
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared/workflowtemplates"
	"go.uber.org/fx"
)

// Service 提供只读的内置政企工作流模板能力。
type Service interface {
	ListTemplates(filter workflowtemplates.ListFilter) []workflowtemplates.TemplateSummary
	GetTemplate(id string) (workflowtemplates.Template, bool)
	RenderDAGDraft(req workflowtemplates.RenderRequest) (workflowtemplates.DAGDraft, error)
	SaveTemplate(req workflowtemplates.SaveTemplateRequest) (workflowtemplates.TemplateSummary, error)
	RollbackTemplate(id string, version int) (workflowtemplates.TemplateSummary, error)
}

type service struct {
	registry *workflowtemplates.Registry
}

// NewRegistry 加载仓库内置模板资产，作为 RPC 和 host tool 共用的进程内模板注册表。
func NewRegistry() (*workflowtemplates.Registry, error) {
	return workflowtemplates.NewDefaultRegistry()
}

// NewService 使用注入的模板注册表创建服务，不依赖数据库或外部执行器。
func NewService(registry *workflowtemplates.Registry) (Service, error) {
	if registry == nil {
		return nil, fmt.Errorf("workflowTemplates: registry is required")
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

// SaveTemplate 保存一个已验证 DAG 草案为模板版本，不启动 DAG 也不写 runtime 状态。
func (s *service) SaveTemplate(req workflowtemplates.SaveTemplateRequest) (workflowtemplates.TemplateSummary, error) {
	tpl, err := workflowtemplates.TemplateFromSaveRequest(req)
	if err != nil {
		return workflowtemplates.TemplateSummary{}, err
	}
	if err := s.registry.SaveTemplate(tpl); err != nil {
		return workflowtemplates.TemplateSummary{}, err
	}
	return s.templateSummary(tpl.ID)
}

// RollbackTemplate 把指定历史版本设为当前模板版本。
func (s *service) RollbackTemplate(id string, version int) (workflowtemplates.TemplateSummary, error) {
	if err := s.registry.RollbackTemplate(id, version); err != nil {
		return workflowtemplates.TemplateSummary{}, err
	}
	return s.templateSummary(id)
}

func (s *service) templateSummary(id string) (workflowtemplates.TemplateSummary, error) {
	for _, summary := range s.registry.ListTemplates() {
		if summary.ID == id {
			return summary, nil
		}
	}
	return workflowtemplates.TemplateSummary{}, fmt.Errorf("workflowTemplates: saved template %q not found", id)
}

var Module = fx.Module("workflowtemplate",
	fx.Provide(
		NewRegistry,
		NewService,
		NewHandlers,
	),
)
