package workflowtemplate

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/creachadair/jrpc2/handler"

	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared/workflowtemplates"
)

type listParams struct {
	Category         string `json:"category,omitempty"`
	BusinessFlow     string `json:"business_flow,omitempty"`
	OutputType       string `json:"output_type,omitempty"`
	SupportsSchedule *bool  `json:"supports_schedule,omitempty"`
	Locale           string `json:"locale,omitempty"`
}

type getParams struct {
	TemplateID      string `json:"templateId,omitempty"`
	TemplateIDSnake string `json:"template_id,omitempty"`
	ID              string `json:"id,omitempty"`
	Version         any    `json:"version,omitempty"`
}

type renderParams struct {
	TemplateID      string         `json:"templateId,omitempty"`
	TemplateIDSnake string         `json:"template_id,omitempty"`
	ID              string         `json:"id,omitempty"`
	Version         any            `json:"version,omitempty"`
	Values          map[string]any `json:"values,omitempty"`
	UserInputs      map[string]any `json:"user_inputs,omitempty"`
	RuntimeContext  map[string]any `json:"runtime_context,omitempty"`
	Locale          string         `json:"locale,omitempty"`
}

type saveParams struct {
	TemplateID       string                           `json:"templateId,omitempty"`
	TemplateIDSnake  string                           `json:"template_id,omitempty"`
	Version          int                              `json:"version,omitempty"`
	Title            workflowtemplates.LocalizedText  `json:"title,omitempty"`
	Description      workflowtemplates.LocalizedText  `json:"description,omitempty"`
	Category         string                           `json:"category,omitempty"`
	BusinessFlow     string                           `json:"business_flow,omitempty"`
	OutputTypes      []string                         `json:"output_types,omitempty"`
	Tags             []string                         `json:"tags,omitempty"`
	RequiresReview   bool                             `json:"requires_review,omitempty"`
	SupportsSchedule bool                             `json:"supports_schedule,omitempty"`
	Trust            workflowtemplates.TrustMetadata  `json:"trust,omitempty"`
	Compatibility    workflowtemplates.Compatibility  `json:"compatibility,omitempty"`
	UISchema         []workflowtemplates.UIField      `json:"ui_schema,omitempty"`
	Validation       workflowtemplates.ValidationRule `json:"validation,omitempty"`
	Draft            workflowtemplates.DAGDraft       `json:"draft,omitempty"`
}

type rollbackParams struct {
	TemplateID      string `json:"templateId,omitempty"`
	TemplateIDSnake string `json:"template_id,omitempty"`
	ID              string `json:"id,omitempty"`
	Version         int    `json:"version,omitempty"`
}

type listResponse struct {
	Templates []workflowtemplates.TemplateSummary `json:"templates"`
}

type getResponse struct {
	Template workflowtemplates.Template `json:"template"`
}

type renderResponse struct {
	Draft workflowtemplates.DAGDraft `json:"draft"`
}

type saveResponse struct {
	Template workflowtemplates.TemplateSummary `json:"template"`
}

type rollbackResponse struct {
	Template workflowtemplates.TemplateSummary `json:"template"`
}

// NewHandlers 注册政企工作流模板目录、渲染和版本保存 RPC。
func NewHandlers(svc Service) platformrpc.HandlerMapResult {
	return platformrpc.HandlerMapResult{Handlers: handler.Map{
		"workflowTemplates/list": platformrpc.StrictHandler(func(_ context.Context, p listParams) (listResponse, error) {
			return handleListTemplates(svc, p), nil
		}),
		"workflowTemplates/get": platformrpc.StrictHandler(func(_ context.Context, p getParams) (getResponse, error) {
			return handleGetTemplate(svc, p)
		}),
		"workflowTemplates/renderDag": platformrpc.StrictHandler(func(_ context.Context, p renderParams) (renderResponse, error) {
			return handleRenderDAG(svc, p)
		}),
		"workflowTemplates/save": platformrpc.StrictHandler(func(_ context.Context, p saveParams) (saveResponse, error) {
			return handleSaveTemplate(svc, p)
		}),
		"workflowTemplates/rollback": platformrpc.StrictHandler(func(_ context.Context, p rollbackParams) (rollbackResponse, error) {
			return handleRollbackTemplate(svc, p)
		}),
	}}
}

func handleListTemplates(svc Service, p listParams) listResponse {
	return listResponse{Templates: svc.ListTemplates(workflowtemplates.ListFilter{
		Category:         p.Category,
		BusinessFlow:     p.BusinessFlow,
		OutputType:       p.OutputType,
		SupportsSchedule: p.SupportsSchedule,
	})}
}

func handleGetTemplate(svc Service, p getParams) (getResponse, error) {
	id := p.templateID()
	if id == "" {
		return getResponse{}, platformrpc.ErrInvalidParams("workflowTemplates/get: templateId is required")
	}
	tpl, ok := svc.GetTemplate(id)
	if !ok {
		return getResponse{}, platformrpc.ErrNotFound("workflowTemplates/get: template not found")
	}
	if err := matchVersion("workflowTemplates/get", tpl, p.Version); err != nil {
		return getResponse{}, err
	}
	return getResponse{Template: tpl}, nil
}

func handleRenderDAG(svc Service, p renderParams) (renderResponse, error) {
	id := p.templateID()
	if id == "" {
		return renderResponse{}, platformrpc.ErrInvalidParams("workflowTemplates/renderDag: templateId is required")
	}
	tpl, ok := svc.GetTemplate(id)
	if !ok {
		return renderResponse{}, platformrpc.ErrNotFound("workflowTemplates/renderDag: template not found")
	}
	if err := matchVersion("workflowTemplates/renderDag", tpl, p.Version); err != nil {
		return renderResponse{}, err
	}
	draft, err := svc.RenderDAGDraft(workflowtemplates.RenderRequest{
		TemplateID:     id,
		Version:        p.Version,
		Values:         p.Values,
		UserInputs:     p.UserInputs,
		RuntimeContext: p.RuntimeContext,
		TemplateLocale: p.Locale,
	})
	if err != nil {
		return renderResponse{}, platformrpc.ErrInvalidParams(err.Error())
	}
	return renderResponse{Draft: draft}, nil
}

func handleSaveTemplate(svc Service, p saveParams) (saveResponse, error) {
	id := p.templateID()
	if id == "" {
		return saveResponse{}, platformrpc.ErrInvalidParams("workflowTemplates/save: templateId is required")
	}
	if p.Version <= 0 {
		return saveResponse{}, platformrpc.ErrInvalidParams("workflowTemplates/save: version must be a positive integer")
	}
	summary, err := svc.SaveTemplate(p.saveRequest(id))
	if err != nil {
		return saveResponse{}, platformrpc.ErrInvalidParams(err.Error())
	}
	return saveResponse{Template: summary}, nil
}

func handleRollbackTemplate(svc Service, p rollbackParams) (rollbackResponse, error) {
	id := p.templateID()
	if id == "" {
		return rollbackResponse{}, platformrpc.ErrInvalidParams("workflowTemplates/rollback: templateId is required")
	}
	if p.Version <= 0 {
		return rollbackResponse{}, platformrpc.ErrInvalidParams("workflowTemplates/rollback: version must be a positive integer")
	}
	summary, err := svc.RollbackTemplate(id, p.Version)
	if err != nil {
		return rollbackResponse{}, platformrpc.ErrNotFound(err.Error())
	}
	return rollbackResponse{Template: summary}, nil
}

func (p getParams) templateID() string {
	return firstNonEmpty(p.TemplateID, p.TemplateIDSnake, p.ID)
}

func (p renderParams) templateID() string {
	return firstNonEmpty(p.TemplateID, p.TemplateIDSnake, p.ID)
}

func (p saveParams) templateID() string {
	return firstNonEmpty(p.TemplateID, p.TemplateIDSnake)
}

func (p rollbackParams) templateID() string {
	return firstNonEmpty(p.TemplateID, p.TemplateIDSnake, p.ID)
}

func (p saveParams) saveRequest(id string) workflowtemplates.SaveTemplateRequest {
	return workflowtemplates.SaveTemplateRequest{
		TemplateID:       id,
		Version:          p.Version,
		Title:            p.Title,
		Description:      p.Description,
		Category:         p.Category,
		BusinessFlow:     p.BusinessFlow,
		OutputTypes:      append([]string(nil), p.OutputTypes...),
		Tags:             append([]string(nil), p.Tags...),
		RequiresReview:   p.RequiresReview,
		SupportsSchedule: p.SupportsSchedule,
		Trust:            p.Trust,
		Compatibility:    p.Compatibility,
		UISchema:         append([]workflowtemplates.UIField(nil), p.UISchema...),
		Validation:       p.Validation,
		Draft:            p.Draft,
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func matchVersion(method string, tpl workflowtemplates.Template, version any) error {
	got := versionText(version)
	if got == "" {
		return nil
	}
	if got != strconv.Itoa(tpl.Version) {
		return platformrpc.ErrNotFound(fmt.Sprintf("%s: template %q version %s not found", method, tpl.ID, got))
	}
	return nil
}

func versionText(value any) string {
	switch current := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(current)
	case int:
		return strconv.Itoa(current)
	case float64:
		return strconv.FormatFloat(current, 'f', -1, 64)
	case json.Number:
		return strings.TrimSpace(current.String())
	default:
		return strings.TrimSpace(fmt.Sprint(current))
	}
}
