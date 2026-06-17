package workflowtemplate

import (
	"context"
	"encoding/json"
	"errors"
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

type listResponse struct {
	Templates []workflowtemplates.TemplateSummary `json:"templates"`
}

type getResponse struct {
	Template workflowtemplates.Template `json:"template"`
}

type renderResponse struct {
	Draft workflowtemplates.DAGDraft `json:"draft"`
}

// NewHandlers 注册政企工作流模板只读 RPC。
func NewHandlers(svc Service) platformrpc.HandlerMapResult {
	return platformrpc.HandlerMapResult{Handlers: handler.Map{
		"workflowTemplates/list": platformrpc.StrictHandler(func(_ context.Context, p listParams) (listResponse, error) {
			return listResponse{Templates: svc.ListTemplates(workflowtemplates.ListFilter{
				Category:         p.Category,
				BusinessFlow:     p.BusinessFlow,
				OutputType:       p.OutputType,
				SupportsSchedule: p.SupportsSchedule,
			})}, nil
		}),
		"workflowTemplates/get": platformrpc.StrictHandler(func(_ context.Context, p getParams) (getResponse, error) {
			id := p.templateID()
			if id == "" {
				return getResponse{}, errors.New("workflowTemplates/get: templateId is required")
			}
			tpl, ok := svc.GetTemplate(id)
			if !ok {
				return getResponse{}, errors.New("workflowTemplates/get: template not found")
			}
			if err := matchVersion(tpl, p.Version); err != nil {
				return getResponse{}, err
			}
			return getResponse{Template: tpl}, nil
		}),
		"workflowTemplates/renderDag": platformrpc.StrictHandler(func(_ context.Context, p renderParams) (renderResponse, error) {
			id := p.templateID()
			if id == "" {
				return renderResponse{}, errors.New("workflowTemplates/renderDag: templateId is required")
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
				return renderResponse{}, err
			}
			return renderResponse{Draft: draft}, nil
		}),
	}}
}

func (p getParams) templateID() string {
	return firstNonEmpty(p.TemplateID, p.TemplateIDSnake, p.ID)
}

func (p renderParams) templateID() string {
	return firstNonEmpty(p.TemplateID, p.TemplateIDSnake, p.ID)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func matchVersion(tpl workflowtemplates.Template, version any) error {
	got := versionText(version)
	if got == "" {
		return nil
	}
	if got != strconv.Itoa(tpl.Version) {
		return fmt.Errorf("workflowTemplates/get: template %q version %s not found", tpl.ID, got)
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
