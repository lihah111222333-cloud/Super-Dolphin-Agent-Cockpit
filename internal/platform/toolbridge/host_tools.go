package toolbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared/workflowtemplates"
)

// HostToolRegistry 暴露由 host 进程**直接执行**（不走 mcp-orch / mcp-lsp peer）
// 的工具集合。当前生产装配只保留 memory_read / memory_write host-direct。
//
// nil HostToolRegistry 等价于 "no host-direct tools"：所有 ListToolsForCodex /
// routeToolCall 调用必须 nil-safe，保证 standalone 模式仍可运行。
type HostToolCall struct {
	Name      string
	Arguments json.RawMessage
	CWD       string
	AgentID   string
	ThreadID  string
	TurnID    string
	CallID    string
}

type HostToolRegistry interface {
	// ListHostTools 列出 host 直跑的工具，结果会与 peer 工具合并送给模型。
	ListHostTools() []mcpdto.MCPTool
	// HasTool 判断给定工具名是否由本 registry 处理。routeToolCall 用它做分支
	// 决策，避免把 skill_* 工具误投到 peer。
	HasTool(name string) bool
	// CallHostTool 同进程执行工具调用。cwd 由 Handler 从 thread context 解析后注入，
	// 不暴露给模型；arguments 是模型填的 JSON，只允许 schema 定义的字段。
	CallHostTool(ctx context.Context, call HostToolCall) (any, error)
}

type HostToolCWDPolicy interface {
	RequiresCWD(name string) bool
}

// Removed skill tool names are kept only so stale Codex tool calls and
// shadowing MCP peers are rejected explicitly. The implementations were
// removed with the provider-native mirror cutover.
const (
	ToolNameReadSection             = "skill_read_section"
	ToolNameLegacySkillExpandBody   = "skill_expand_body"
	ToolNameLegacySkillReadResource = "skill_read_resource"
)

const (
	ToolNameWorkflowTemplateList      = "workflow_template_list"
	ToolNameWorkflowTemplateGet       = "workflow_template_get"
	ToolNameWorkflowTemplateRenderDAG = "workflow_template_render_dag"
	ToolNameWorkflowTemplateSave      = "workflow_template_save"
	ToolNameWorkflowTemplateRollback  = "workflow_template_rollback"
)

type WorkflowTemplateHostToolRegistry struct {
	registry *workflowtemplates.Registry
	loadErr  error
}

type workflowTemplateListInput struct {
	Category         string `json:"category,omitempty"`
	BusinessFlow     string `json:"business_flow,omitempty"`
	OutputType       string `json:"output_type,omitempty"`
	SupportsSchedule *bool  `json:"supports_schedule,omitempty"`
	Locale           string `json:"locale,omitempty"`
}

type workflowTemplateGetInput struct {
	TemplateID      string `json:"template_id,omitempty"`
	TemplateIDCamel string `json:"templateId,omitempty"`
	ID              string `json:"id,omitempty"`
	Version         any    `json:"version,omitempty"`
	Locale          string `json:"locale,omitempty"`
}

type workflowTemplateRenderInput struct {
	TemplateID      string         `json:"template_id,omitempty"`
	TemplateIDCamel string         `json:"templateId,omitempty"`
	ID              string         `json:"id,omitempty"`
	Version         any            `json:"version,omitempty"`
	Values          map[string]any `json:"values,omitempty"`
	UserInputs      map[string]any `json:"user_inputs,omitempty"`
	RuntimeContext  map[string]any `json:"runtime_context,omitempty"`
}

type workflowTemplateSaveInput struct {
	TemplateID       string                           `json:"template_id,omitempty"`
	TemplateIDCamel  string                           `json:"templateId,omitempty"`
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

type workflowTemplateRollbackInput struct {
	TemplateID      string `json:"template_id,omitempty"`
	TemplateIDCamel string `json:"templateId,omitempty"`
	ID              string `json:"id,omitempty"`
	Version         int    `json:"version,omitempty"`
}

type workflowTemplateListResult struct {
	Templates []workflowtemplates.TemplateSummary `json:"templates"`
}

type workflowTemplateGetResult struct {
	Template workflowtemplates.Template `json:"template"`
}

type workflowTemplateRenderResult struct {
	Draft workflowtemplates.DAGDraft `json:"draft"`
}

type workflowTemplateSaveResult struct {
	Template workflowtemplates.TemplateSummary `json:"template"`
}

type workflowTemplateRollbackResult struct {
	Template workflowtemplates.TemplateSummary `json:"template"`
}

// NewWorkflowTemplateHostToolRegistry 创建只读模板工具注册表，供 DAG Designer 读取同一份内置模板资产。
func NewWorkflowTemplateHostToolRegistry() *WorkflowTemplateHostToolRegistry {
	registry, err := workflowtemplates.NewDefaultRegistry()
	return &WorkflowTemplateHostToolRegistry{registry: registry, loadErr: err}
}

// ListHostTools 返回模板库的 list/get/render 三个只读工具，不创建 DAG、不写文件。
func (r *WorkflowTemplateHostToolRegistry) ListHostTools() []mcpdto.MCPTool {
	if r == nil {
		return nil
	}
	listSchema, _ := json.Marshal(workflowTemplateListInputSchema())
	getSchema, _ := json.Marshal(workflowTemplateGetInputSchema())
	renderSchema, _ := json.Marshal(workflowTemplateRenderInputSchema())
	saveSchema, _ := json.Marshal(workflowTemplateSaveInputSchema())
	rollbackSchema, _ := json.Marshal(workflowTemplateRollbackInputSchema())
	return []mcpdto.MCPTool{
		{Name: ToolNameWorkflowTemplateList, Description: descriptionWorkflowTemplateList, InputSchema: listSchema},
		{Name: ToolNameWorkflowTemplateGet, Description: descriptionWorkflowTemplateGet, InputSchema: getSchema},
		{Name: ToolNameWorkflowTemplateRenderDAG, Description: descriptionWorkflowTemplateRenderDAG, InputSchema: renderSchema},
		{Name: ToolNameWorkflowTemplateSave, Description: descriptionWorkflowTemplateSave, InputSchema: saveSchema},
		{Name: ToolNameWorkflowTemplateRollback, Description: descriptionWorkflowTemplateRollback, InputSchema: rollbackSchema},
	}
}

// HasTool 判断工具名是否由政企模板库注册表处理。
func (r *WorkflowTemplateHostToolRegistry) HasTool(name string) bool {
	return r != nil && isWorkflowTemplateToolName(name)
}

// RequiresCWD 声明模板库工具不依赖工作目录，避免无关 cwd 缺失阻断模板读取。
func (r *WorkflowTemplateHostToolRegistry) RequiresCWD(name string) bool {
	return !isWorkflowTemplateToolName(name)
}

// CallHostTool 执行模板库只读工具；渲染只返回 DAG 草稿，不落库、不启动运行。
func (r *WorkflowTemplateHostToolRegistry) CallHostTool(_ context.Context, call HostToolCall) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("workflow template tools are not configured")
	}
	if r.loadErr != nil {
		return nil, fmt.Errorf("workflow template registry unavailable: %w", r.loadErr)
	}
	switch strings.TrimSpace(call.Name) {
	case ToolNameWorkflowTemplateList:
		return r.list(call.Arguments)
	case ToolNameWorkflowTemplateGet:
		return r.get(call.Arguments)
	case ToolNameWorkflowTemplateRenderDAG:
		return r.renderDAG(call.Arguments)
	case ToolNameWorkflowTemplateSave:
		return r.save(call.Arguments)
	case ToolNameWorkflowTemplateRollback:
		return r.rollback(call.Arguments)
	default:
		return nil, fmt.Errorf("workflow template tools: unknown tool %q", call.Name)
	}
}

func isWorkflowTemplateToolName(name string) bool {
	switch strings.TrimSpace(name) {
	case ToolNameWorkflowTemplateList, ToolNameWorkflowTemplateGet, ToolNameWorkflowTemplateRenderDAG, ToolNameWorkflowTemplateSave, ToolNameWorkflowTemplateRollback:
		return true
	default:
		return false
	}
}

func (r *WorkflowTemplateHostToolRegistry) list(raw json.RawMessage) (workflowTemplateListResult, error) {
	var input workflowTemplateListInput
	if err := decodeWorkflowTemplateToolInput(raw, &input); err != nil {
		return workflowTemplateListResult{}, err
	}
	templates := r.registry.ListTemplates(workflowtemplates.ListFilter{
		Category:         input.Category,
		BusinessFlow:     input.BusinessFlow,
		OutputType:       input.OutputType,
		SupportsSchedule: input.SupportsSchedule,
	})
	return workflowTemplateListResult{Templates: templates}, nil
}

func (r *WorkflowTemplateHostToolRegistry) get(raw json.RawMessage) (workflowTemplateGetResult, error) {
	var input workflowTemplateGetInput
	if err := decodeWorkflowTemplateToolInput(raw, &input); err != nil {
		return workflowTemplateGetResult{}, err
	}
	tpl, err := r.getTemplateByInput(input.TemplateID, input.TemplateIDCamel, input.ID, input.Version)
	if err != nil {
		return workflowTemplateGetResult{}, err
	}
	return workflowTemplateGetResult{Template: tpl}, nil
}

func (r *WorkflowTemplateHostToolRegistry) renderDAG(raw json.RawMessage) (workflowTemplateRenderResult, error) {
	var input workflowTemplateRenderInput
	if err := decodeWorkflowTemplateToolInput(raw, &input); err != nil {
		return workflowTemplateRenderResult{}, err
	}
	tpl, err := r.getTemplateByInput(input.TemplateID, input.TemplateIDCamel, input.ID, input.Version)
	if err != nil {
		return workflowTemplateRenderResult{}, err
	}
	values := input.Values
	if len(values) == 0 {
		values = input.UserInputs
	}
	draft, err := r.registry.RenderDAGDraft(workflowtemplates.RenderRequest{
		TemplateID:     tpl.ID,
		Version:        input.Version,
		Values:         values,
		UserInputs:     input.UserInputs,
		RuntimeContext: input.RuntimeContext,
	})
	if err != nil {
		return workflowTemplateRenderResult{}, err
	}
	return workflowTemplateRenderResult{Draft: draft}, nil
}

func (r *WorkflowTemplateHostToolRegistry) save(raw json.RawMessage) (workflowTemplateSaveResult, error) {
	var input workflowTemplateSaveInput
	if err := decodeWorkflowTemplateToolInput(raw, &input); err != nil {
		return workflowTemplateSaveResult{}, err
	}
	id := firstWorkflowTemplateID(input.TemplateID, input.TemplateIDCamel)
	tpl, err := workflowtemplates.TemplateFromSaveRequest(workflowtemplates.SaveTemplateRequest{
		TemplateID:       id,
		Version:          input.Version,
		Title:            input.Title,
		Description:      input.Description,
		Category:         input.Category,
		BusinessFlow:     input.BusinessFlow,
		OutputTypes:      append([]string(nil), input.OutputTypes...),
		Tags:             append([]string(nil), input.Tags...),
		RequiresReview:   input.RequiresReview,
		SupportsSchedule: input.SupportsSchedule,
		Trust:            input.Trust,
		Compatibility:    input.Compatibility,
		UISchema:         append([]workflowtemplates.UIField(nil), input.UISchema...),
		Validation:       input.Validation,
		Draft:            input.Draft,
	})
	if err != nil {
		return workflowTemplateSaveResult{}, err
	}
	if err := r.registry.SaveTemplate(tpl); err != nil {
		return workflowTemplateSaveResult{}, err
	}
	summary, err := r.summaryByID(tpl.ID)
	if err != nil {
		return workflowTemplateSaveResult{}, err
	}
	return workflowTemplateSaveResult{Template: summary}, nil
}

func (r *WorkflowTemplateHostToolRegistry) rollback(raw json.RawMessage) (workflowTemplateRollbackResult, error) {
	var input workflowTemplateRollbackInput
	if err := decodeWorkflowTemplateToolInput(raw, &input); err != nil {
		return workflowTemplateRollbackResult{}, err
	}
	id := firstWorkflowTemplateID(input.TemplateID, input.TemplateIDCamel, input.ID)
	if id == "" {
		return workflowTemplateRollbackResult{}, fmt.Errorf("workflow template input: template_id is required")
	}
	if input.Version <= 0 {
		return workflowTemplateRollbackResult{}, fmt.Errorf("workflow template rollback: version must be a positive integer")
	}
	if err := r.registry.RollbackTemplate(id, input.Version); err != nil {
		return workflowTemplateRollbackResult{}, err
	}
	summary, err := r.summaryByID(id)
	if err != nil {
		return workflowTemplateRollbackResult{}, err
	}
	return workflowTemplateRollbackResult{Template: summary}, nil
}

func (r *WorkflowTemplateHostToolRegistry) summaryByID(id string) (workflowtemplates.TemplateSummary, error) {
	for _, summary := range r.registry.ListTemplates() {
		if summary.ID == id {
			return summary, nil
		}
	}
	return workflowtemplates.TemplateSummary{}, fmt.Errorf("workflow template %q summary not found", id)
}

func firstWorkflowTemplateID(ids ...string) string {
	for _, id := range ids {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (r *WorkflowTemplateHostToolRegistry) getTemplateByInput(ids ...any) (workflowtemplates.Template, error) {
	id, version := workflowTemplateIDAndVersion(ids...)
	if id == "" {
		return workflowtemplates.Template{}, fmt.Errorf("workflow template input: template_id is required")
	}
	tpl, ok := r.registry.GetTemplate(id)
	if !ok {
		return workflowtemplates.Template{}, fmt.Errorf("workflow template %q not found", id)
	}
	if version != "" && version != strconv.Itoa(tpl.Version) {
		return workflowtemplates.Template{}, fmt.Errorf("workflow template %q version %s not found", id, version)
	}
	return tpl, nil
}

func workflowTemplateIDAndVersion(values ...any) (string, string) {
	var id string
	for i, value := range values {
		text := workflowTemplateVersionString(value)
		if i < 3 && id == "" {
			id = text
			continue
		}
		if i >= 3 {
			return id, text
		}
	}
	return id, ""
}

func workflowTemplateVersionString(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		return strconv.Itoa(v)
	case json.Number:
		return strings.TrimSpace(v.String())
	default:
		if value == nil {
			return ""
		}
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func workflowTemplateSummaryMatches(tpl workflowtemplates.TemplateSummary, input workflowTemplateListInput) bool {
	category := strings.TrimSpace(input.Category)
	if category != "" && tpl.Category != category {
		return false
	}
	businessFlow := strings.TrimSpace(input.BusinessFlow)
	if businessFlow != "" && tpl.BusinessFlow != businessFlow {
		return false
	}
	outputType := strings.TrimSpace(input.OutputType)
	if outputType != "" && !workflowTemplateHasOutputType(tpl.OutputTypes, outputType) {
		return false
	}
	return input.SupportsSchedule == nil || tpl.SupportsSchedule == *input.SupportsSchedule
}

func workflowTemplateHasOutputType(outputTypes []string, want string) bool {
	for _, outputType := range outputTypes {
		if strings.TrimSpace(outputType) == want {
			return true
		}
	}
	return false
}

func decodeWorkflowTemplateToolInput(raw json.RawMessage, dst any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		trimmed = []byte("{}")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("invalid workflow template tool input: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("invalid workflow template tool input: trailing JSON")
	}
	return nil
}

func workflowTemplateListInputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"category":      map[string]any{"type": "string"},
			"business_flow": map[string]any{"type": "string"},
			"output_type":   map[string]any{"type": "string", "enum": []string{"video", "pptx", "docx", "xlsx", "markdown", "pdf", "json", "md", "mp4"}},
			"supports_schedule": map[string]any{
				"type": "boolean",
			},
			"locale": map[string]any{"type": "string"},
		},
		"additionalProperties": false,
	}
}

func workflowTemplateGetInputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"template_id": map[string]any{"type": "string"},
			"templateId":  map[string]any{"type": "string"},
			"id":          map[string]any{"type": "string"},
			"version":     map[string]any{"type": []string{"string", "number"}},
			"locale":      map[string]any{"type": "string"},
		},
		"required":             []string{"template_id"},
		"additionalProperties": false,
	}
}

func workflowTemplateRenderInputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"template_id": map[string]any{"type": "string"},
			"templateId":  map[string]any{"type": "string"},
			"id":          map[string]any{"type": "string"},
			"version":     map[string]any{"type": []string{"string", "number"}},
			"values":      map[string]any{"type": "object"},
			"user_inputs": map[string]any{"type": "object"},
			"runtime_context": map[string]any{
				"type":        "object",
				"description": "Optional runtime hints; this read-only tool does not create or run DAGs.",
			},
		},
		"required":             []string{"template_id"},
		"additionalProperties": false,
	}
}

func workflowTemplateSaveInputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"template_id":       map[string]any{"type": "string"},
			"templateId":        map[string]any{"type": "string"},
			"version":           map[string]any{"type": "integer", "minimum": 1},
			"title":             map[string]any{"type": "object"},
			"description":       map[string]any{"type": "object"},
			"category":          map[string]any{"type": "string"},
			"business_flow":     map[string]any{"type": "string"},
			"output_types":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"tags":              map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"requires_review":   map[string]any{"type": "boolean"},
			"supports_schedule": map[string]any{"type": "boolean"},
			"trust":             map[string]any{"type": "object"},
			"compatibility":     map[string]any{"type": "object"},
			"ui_schema":         map[string]any{"type": "array"},
			"validation":        map[string]any{"type": "object"},
			"draft":             map[string]any{"type": "object"},
		},
		"required":             []string{"template_id", "version", "category", "compatibility", "trust", "draft"},
		"additionalProperties": false,
	}
}

func workflowTemplateRollbackInputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"template_id": map[string]any{"type": "string"},
			"templateId":  map[string]any{"type": "string"},
			"id":          map[string]any{"type": "string"},
			"version":     map[string]any{"type": "integer", "minimum": 1},
		},
		"required":             []string{"template_id", "version"},
		"additionalProperties": false,
	}
}

const descriptionWorkflowTemplateList = "List built-in government-enterprise workflow template summaries. Read-only; does not create DAGs or write files."
const descriptionWorkflowTemplateGet = "Get one built-in government-enterprise workflow template with ui_schema, dag_template, review node, and final output contract."
const descriptionWorkflowTemplateRenderDAG = "Render a built-in workflow template into a DAG draft from values/user_inputs. Read-only; does not persist or run the DAG."
const descriptionWorkflowTemplateSave = "Save a validated DAG draft as a versioned reusable workflow template. Does not create or run DAGs."
const descriptionWorkflowTemplateRollback = "Make a previous workflow template version active again. Does not create or run DAGs."
